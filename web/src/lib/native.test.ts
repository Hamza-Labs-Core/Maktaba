// Tests for the native-runtime detection + lifecycle-consumer wiring
// (Story 12.1 / 12.2). The Capacitor bridge in apps/mobile/src/
// native-shell.ts dispatches `mkt:*` DOM events; these tests verify the
// web side actually consumes them (the gap-analysis flagged ~7 "unwired"
// ACs whose only defect was that no importer/consumer existed).

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  isNativeRuntime,
  installNativeConsumers,
  deepLinkToPath,
  consumePendingDeepLink,
  __resetNativeConsumersForTest,
} from "./native";

describe("deepLinkToPath (Story 12.9)", () => {
  it("maps custom-scheme watch link with query", () => {
    expect(deepLinkToPath("maktaba://watch/123?t=42")).toBe("/watch/123?t=42");
  });

  it("maps https Universal/App link", () => {
    expect(deepLinkToPath("https://maktaba.app/library/abc")).toBe("/library/abc");
  });

  it.each(["search", "queue", "settings", "collection"])("accepts the %s route", (route) => {
    expect(deepLinkToPath(`maktaba://${route}`)).toBe(`/${route}`);
  });

  it("rejects unknown routes and garbage", () => {
    expect(deepLinkToPath("maktaba://evil/1")).toBeNull();
    expect(deepLinkToPath("not a url")).toBeNull();
    expect(deepLinkToPath("https://maktaba.app/")).toBeNull();
  });
});

describe("isNativeRuntime", () => {
  afterEach(() => {
    delete (window as unknown as { Capacitor?: unknown }).Capacitor;
  });

  it("is false in a plain browser (no Capacitor global)", () => {
    expect(isNativeRuntime()).toBe(false);
  });

  it("is true when Capacitor.isNativePlatform() reports native", () => {
    (window as unknown as { Capacitor?: unknown }).Capacitor = {
      isNativePlatform: () => true,
    };
    expect(isNativeRuntime()).toBe(true);
  });

  it("is false when Capacitor exists but reports web platform", () => {
    (window as unknown as { Capacitor?: unknown }).Capacitor = {
      isNativePlatform: () => false,
    };
    expect(isNativeRuntime()).toBe(false);
  });
});

describe("installNativeConsumers", () => {
  beforeEach(() => {
    __resetNativeConsumersForTest();
  });
  afterEach(() => {
    __resetNativeConsumersForTest();
    vi.restoreAllMocks();
  });

  it("is idempotent — listeners attached once even if called twice", () => {
    const spy = vi.spyOn(window, "addEventListener");
    installNativeConsumers();
    const firstCount = spy.mock.calls.length;
    installNativeConsumers();
    expect(spy.mock.calls.length).toBe(firstCount);
  });

  it("pauses on mkt:appBackgrounded and resumes on mkt:appResumed", () => {
    installNativeConsumers();
    expect(document.documentElement.dataset.mktAppState).toBeUndefined();

    window.dispatchEvent(new CustomEvent("mkt:appBackgrounded"));
    expect(document.documentElement.dataset.mktAppState).toBe("background");

    window.dispatchEvent(new CustomEvent("mkt:appResumed"));
    expect(document.documentElement.dataset.mktAppState).toBe("active");
  });

  it("clears the named caches on mkt:lowMemory", async () => {
    const deleted: string[] = [];
    (globalThis as unknown as { caches?: unknown }).caches = {
      keys: async () => ["mkt-thumbs", "mkt-api", "other"],
      delete: async (k: string) => {
        deleted.push(k);
        return true;
      },
    };
    installNativeConsumers();
    window.dispatchEvent(new CustomEvent("mkt:lowMemory"));
    // Allow the async cache sweep to settle.
    await new Promise((r) => setTimeout(r, 0));
    expect(deleted).toContain("mkt-thumbs");
    expect(deleted).toContain("mkt-api");
    expect(deleted).not.toContain("other");
    delete (globalThis as unknown as { caches?: unknown }).caches;
  });

  it("routes a deep link and preserves it for post-login resume", () => {
    installNativeConsumers();
    const navs: string[] = [];
    window.addEventListener("mkt:navigate", (e) => navs.push((e as CustomEvent).detail.path));

    window.dispatchEvent(
      new CustomEvent("mkt:deepLink", {
        detail: { raw: "maktaba://watch/77?t=5" },
      })
    );
    expect(navs).toEqual(["/watch/77?t=5"]);

    // Survives until consumed (login flow), then is cleared.
    expect(consumePendingDeepLink()).toBe("/watch/77?t=5");
    expect(consumePendingDeepLink()).toBeNull();
  });

  it("ignores a foreign/unknown deep link", () => {
    installNativeConsumers();
    const navs: string[] = [];
    window.addEventListener("mkt:navigate", (e) => navs.push((e as CustomEvent).detail.path));
    window.dispatchEvent(new CustomEvent("mkt:deepLink", { detail: { raw: "maktaba://evil/1" } }));
    expect(navs).toEqual([]);
    expect(consumePendingDeepLink()).toBeNull();
  });

  it("re-dispatches a debounced mkt:wsThrottle after backgrounding", () => {
    vi.useFakeTimers();
    const events: string[] = [];
    window.addEventListener("mkt:wsThrottle", () => events.push("throttle"));
    installNativeConsumers();

    window.dispatchEvent(new CustomEvent("mkt:appBackgrounded"));
    expect(events).toHaveLength(0); // not immediate
    vi.advanceTimersByTime(60_000); // Story 12.1 AC: 60 s WS throttle
    expect(events).toEqual(["throttle"]);

    // Resuming before the next tick cancels further throttling.
    window.dispatchEvent(new CustomEvent("mkt:appResumed"));
    vi.advanceTimersByTime(120_000);
    expect(events).toEqual(["throttle"]);
    vi.useRealTimers();
  });
});
