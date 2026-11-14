// Epic 58 story 58.09 — the API client signed in by bearer (/tv/ in the launcher): the
// Authorization header, no cookie and no CSRF header, one renewal and retry on a 401, and
// the home's address set at run time for the --bundle-ui build.
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  api,
  ApiError,
  apiUrl,
  authHeaders,
  setApiBase,
  setBearerAuth,
  type BearerAuth,
} from "./api";

function fakeBearer(tokens: string[], renew: boolean): BearerAuth & { renewals: number } {
  let i = 0;
  return {
    renewals: 0,
    token: () => tokens[i] ?? null,
    async refresh() {
      this.renewals++;
      if (!renew) return false;
      i++;
      return true;
    },
    logoutBody: () => ({ refresh_token: "r" }),
    clear: () => undefined,
  };
}

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": status >= 400 ? "application/problem+json" : "application/json" },
  });

afterEach(() => {
  setBearerAuth(null);
  setApiBase("");
  vi.unstubAllGlobals();
  document.cookie = "mkt_csrf=; expires=Thu, 01 Jan 1970 00:00:00 GMT";
});

describe("bearer sign-in", () => {
  it("sends the access token, no cookie, and no CSRF header", async () => {
    document.cookie = "mkt_csrf=csrf1";
    const fetchMock = vi.fn(async () => json(200, { ok: true }));
    vi.stubGlobal("fetch", fetchMock);
    setBearerAuth(fakeBearer(["a1"], true));
    await api.post("/api/things", { x: 1 });
    const init = (fetchMock.mock.calls[0] as unknown as [string, RequestInit])[1];
    const h = new Headers(init.headers);
    expect(h.get("Authorization")).toBe("Bearer a1");
    expect(h.get("X-Maktaba-CSRF")).toBeNull();
    expect(init.credentials).toBe("omit");
    expect(authHeaders()).toEqual({ Authorization: "Bearer a1" });
  });

  it("renews once on a 401 and tries again with the new token", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(json(401, { title: "expired", status: 401 }))
      .mockResolvedValueOnce(json(200, { ok: true }));
    vi.stubGlobal("fetch", fetchMock);
    const b = fakeBearer(["a1", "a2"], true);
    setBearerAuth(b);
    await expect(api.get("/api/auth/me")).resolves.toEqual({ ok: true });
    expect(b.renewals).toBe(1);
    const second = (fetchMock.mock.calls[1] as unknown as [string, RequestInit])[1];
    expect(new Headers(second.headers).get("Authorization")).toBe("Bearer a2");
  });

  it("gives the 401 back when the renewal is refused", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => json(401, { title: "expired", status: 401 }))
    );
    const b = fakeBearer(["a1"], false);
    setBearerAuth(b);
    const err = await api.get("/api/auth/me").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(401);
    expect(b.renewals).toBe(1);
  });

  it("without a bearer: the cookie and the CSRF header, as before", async () => {
    document.cookie = "mkt_csrf=csrf1";
    const fetchMock = vi.fn(async () => json(200, {}));
    vi.stubGlobal("fetch", fetchMock);
    await api.post("/api/things", {});
    const init = (fetchMock.mock.calls[0] as unknown as [string, RequestInit])[1];
    expect(init.credentials).toBe("include");
    expect(new Headers(init.headers).get("Authorization")).toBeNull();
    expect(new Headers(init.headers).get("X-Maktaba-CSRF")).toBe("csrf1");
  });

  it("reaches the home at the address the bundled build was given", async () => {
    const fetchMock = vi.fn(async () => json(200, {}));
    vi.stubGlobal("fetch", fetchMock);
    setApiBase("https://1-2-3-4.h.example/");
    expect(apiUrl("/api/x")).toBe("https://1-2-3-4.h.example/api/x");
    await api.get("/api/x");
    expect((fetchMock.mock.calls[0] as unknown as [string])[0]).toBe(
      "https://1-2-3-4.h.example/api/x"
    );
  });
});
