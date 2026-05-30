// Story 11.10 hardening — logout must scrub any cached authenticated
// API responses so a second user on a shared browser is never served
// the previous user's library / saved searches / jobs.
//
// NOTE: the service-worker *runtime* fetch path (public/sw.js) cannot
// be exercised in this jsdom harness — it needs a real ServiceWorker
// global + Cache Storage controller. That path is integration-only.
// What IS unit-testable, and is the actual logout security boundary,
// is `purgeApiCacheOnLogout`: it must delete every `mkt-api-*` cache
// and notify the controlling SW. These tests prove exactly that.
import { describe, it, expect, afterEach, vi } from "vitest";
import { purgeApiCacheOnLogout } from "./sw";

function mockCaches(initial: string[]) {
  const store = new Set(initial);
  const cachesMock = {
    keys: vi.fn(async () => [...store]),
    delete: vi.fn(async (name: string) => store.delete(name)),
  };
  vi.stubGlobal("caches", cachesMock);
  return { store, cachesMock };
}

describe("purgeApiCacheOnLogout", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("deletes every mkt-api-* cache but leaves the shell cache", async () => {
    const { store, cachesMock } = mockCaches([
      "mkt-api-v1",
      "mkt-api-v2",
      "mkt-shell-v2",
      "other-cache",
    ]);

    await purgeApiCacheOnLogout();

    expect(cachesMock.delete).toHaveBeenCalledWith("mkt-api-v1");
    expect(cachesMock.delete).toHaveBeenCalledWith("mkt-api-v2");
    expect(cachesMock.delete).not.toHaveBeenCalledWith("mkt-shell-v2");
    expect(cachesMock.delete).not.toHaveBeenCalledWith("other-cache");
    expect([...store].sort()).toEqual(["mkt-shell-v2", "other-cache"]);
  });

  it("simulated logout/login: user B never sees user A's cached API body", async () => {
    // Stand-in for the URL-keyed SWR store a legacy SW left behind:
    // /api/videos cached while user A was signed in.
    const apiCache = new Map<string, string>([["/api/videos", "userA-library"]]);
    const store = new Map<string, Map<string, string>>([["mkt-api-v2", apiCache]]);
    vi.stubGlobal("caches", {
      keys: vi.fn(async () => [...store.keys()]),
      delete: vi.fn(async (name: string) => store.delete(name)),
      match: vi.fn(async (k: string) => {
        for (const c of store.values()) if (c.has(k)) return c.get(k);
        return undefined;
      }),
    });

    // User A logs out -> purge runs.
    await purgeApiCacheOnLogout();

    // User B logs in on the same browser and the SW would consult the
    // cache for /api/videos: it must be gone.
    expect(
      await (caches as unknown as { match(k: string): Promise<unknown> }).match("/api/videos")
    ).toBeUndefined();
    expect(store.has("mkt-api-v2")).toBe(false);
  });

  it("posts PURGE_API_CACHE to the controlling service worker", async () => {
    mockCaches([]);
    const postMessage = vi.fn();
    vi.stubGlobal("navigator", {
      serviceWorker: { controller: { postMessage } },
    });

    await purgeApiCacheOnLogout();

    expect(postMessage).toHaveBeenCalledWith({ type: "PURGE_API_CACHE" });
  });

  it("never throws when Cache Storage is unavailable", async () => {
    vi.stubGlobal("caches", undefined);
    await expect(purgeApiCacheOnLogout()).resolves.toBeUndefined();
  });

  it("swallows a caches.delete failure (logout must not be blocked)", async () => {
    vi.stubGlobal("caches", {
      keys: vi.fn(async () => ["mkt-api-v2"]),
      delete: vi.fn(async () => {
        throw new Error("quota");
      }),
    });
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    await expect(purgeApiCacheOnLogout()).resolves.toBeUndefined();
    expect(warn).toHaveBeenCalled();
    warn.mockRestore();
  });
});
