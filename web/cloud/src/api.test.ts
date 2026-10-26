import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, CloudApiError, onSessionChange, setAccessToken } from "./api";
import { json, stubFetch, user } from "./test/harness";

beforeEach(() => setAccessToken(null));
afterEach(() => vi.unstubAllGlobals());

describe("cloud api client", () => {
  it("sends the bearer token, and on a 401 refreshes once and retries", async () => {
    setAccessToken("old");
    const f = stubFetch({
      "GET /v1/account/me": (call, n) =>
        n === 1
          ? json(401, { error: "unauthorized", message: "expired" })
          : json(200, { id: "u1", auth: call.headers.Authorization }),
      "POST /v1/auth/refresh": [200, { access_token: "new", expires_in: 900, user: user() }],
    });
    const seen = vi.fn();
    const off = onSessionChange(seen);

    const me = await api.get<{ id: string; auth: string }>("/v1/account/me");

    off();
    expect(me).toEqual({ id: "u1", auth: "Bearer new" });
    expect(f.calls.map((c) => `${c.method} ${c.path}`)).toEqual([
      "GET /v1/account/me",
      "POST /v1/auth/refresh",
      "GET /v1/account/me",
    ]);
    expect(f.calls[0].headers.Authorization).toBe("Bearer old");
    expect(f.fn.mock.calls[1][1]).toMatchObject({ method: "POST", credentials: "include" });
    expect(seen).toHaveBeenCalledWith(expect.objectContaining({ access_token: "new" }));
  });

  it("shares one refresh between concurrent 401s", async () => {
    setAccessToken("old");
    const f = stubFetch({
      "GET /v1/a": (_c, n) => (n === 1 ? json(401, {}) : json(200, { ok: "a" })),
      "GET /v1/b": (_c, n) => (n === 1 ? json(401, {}) : json(200, { ok: "b" })),
      "POST /v1/auth/refresh": [200, { access_token: "new", expires_in: 900, user: user() }],
    });
    await Promise.all([api.get("/v1/a"), api.get("/v1/b")]);
    expect(f.find("POST", "/v1/auth/refresh")).toHaveLength(1);
  });

  it("gives up after a failed refresh: the 401 surfaces and the session is gone", async () => {
    setAccessToken("old");
    const f = stubFetch({
      "GET /v1/account/me": [401, { error: "unauthorized", message: "expired" }],
      "POST /v1/auth/refresh": [401, { error: "no_session", message: "none" }],
    });
    const seen = vi.fn();
    const off = onSessionChange(seen);
    const err = await api.get("/v1/account/me").catch((e: unknown) => e);
    off();
    expect(err).toBeInstanceOf(CloudApiError);
    expect((err as CloudApiError).status).toBe(401);
    expect(f.find("GET", "/v1/account/me")).toHaveLength(1);
    expect(seen).toHaveBeenCalledWith(null);
  });

  it("does not refresh for unauthenticated calls (login)", async () => {
    const f = stubFetch({
      "POST /v1/auth/login": [401, { error: "invalid_credentials", message: "bad" }],
    });
    const err = (await api
      .post("/v1/auth/login", { email: "a", password: "b" }, { auth: false })
      .catch((e: unknown) => e)) as CloudApiError;
    expect(err.code).toBe("invalid_credentials");
    expect(err.message).toBe("bad");
    expect(f.find("POST", "/v1/auth/refresh")).toHaveLength(0);
  });

  it("parses {error, message} and Retry-After", async () => {
    stubFetch({
      "POST /v1/auth/login": () =>
        json(429, { error: "too_many_attempts", message: "slow down" }, { "Retry-After": "1800" }),
    });
    const err = (await api
      .post("/v1/auth/login", {}, { auth: false })
      .catch((e: unknown) => e)) as CloudApiError;
    expect(err.status).toBe(429);
    expect(err.code).toBe("too_many_attempts");
    expect(err.retryAfter).toBe(1800);
  });

  it("returns undefined for a 204", async () => {
    stubFetch({ "POST /v1/account/sessions/revoke-all": [204] });
    await expect(api.post("/v1/account/sessions/revoke-all")).resolves.toBeUndefined();
  });
});
