// Epic 58 story 58.09 — /tv/'s bearer session in the launcher: tokens kept in the home's
// origin, one renewal at a time, signed out when the home refuses the refresh token.
import { afterEach, describe, expect, it, vi } from "vitest";
import { isTokenAnswer, TvSession } from "./session";

const KEY = "mkt:tv:session";
const answer = (n: number) => ({
  access_token: `a${n}`,
  access_expires_in: 900,
  refresh_token: `r${n}`,
});

afterEach(() => {
  localStorage.clear();
  vi.unstubAllGlobals();
});

describe("TvSession", () => {
  it("keeps a sign-in's tokens in the home's storage and reads them back", () => {
    const s = new TvSession(() => 1000);
    expect(s.signedIn()).toBe(false);
    expect(s.token()).toBeNull();
    s.save(answer(1));
    expect(s.token()).toBe("a1");
    expect(JSON.parse(localStorage.getItem(KEY)!)).toEqual({
      access: "a1",
      accessUntil: 1000 + 900_000,
      refresh: "r1",
    });
    const again = new TvSession();
    expect(again.signedIn()).toBe(true);
    expect(again.logoutBody()).toEqual({ refresh_token: "r1" });
    again.clear();
    expect(localStorage.getItem(KEY)).toBeNull();
    expect(again.logoutBody()).toBeUndefined();
  });

  it("ignores a stored value it cannot use", () => {
    localStorage.setItem(KEY, "{not json");
    expect(new TvSession().signedIn()).toBe(false);
    localStorage.setItem(KEY, JSON.stringify({ access: "a", refresh: "" }));
    expect(new TvSession().signedIn()).toBe(false);
  });

  it("renews once for many callers, with the refresh token and no cookie", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify(answer(2)), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    const s = new TvSession();
    s.save(answer(1));
    const [a, b] = await Promise.all([s.refresh(), s.refresh()]);
    expect(a && b).toBe(true);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/auth/refresh");
    expect(init.credentials).toBe("omit");
    expect(JSON.parse(init.body as string)).toEqual({ refresh_token: "r1" });
    expect(s.token()).toBe("a2");
  });

  it("is signed out when the home refuses the refresh token, kept on no answer or 429", async () => {
    const s = new TvSession();
    s.save(answer(1));
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => Promise.reject(new TypeError("offline")))
    );
    expect(await s.refresh()).toBe(false);
    expect(s.signedIn()).toBe(true);
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("", { status: 429 }))
    );
    expect(await s.refresh()).toBe(false);
    expect(s.signedIn()).toBe(true);
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("", { status: 401 }))
    );
    expect(await s.refresh()).toBe(false);
    expect(s.signedIn()).toBe(false);
    expect(await s.refresh()).toBe(false);
  });

  it("tells a token answer from anything else", () => {
    expect(isTokenAnswer(answer(1))).toBe(true);
    expect(isTokenAnswer({ looked_up: true })).toBe(false);
    expect(isTokenAnswer(null)).toBe(false);
  });
});
