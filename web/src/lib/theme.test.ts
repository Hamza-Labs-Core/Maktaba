// Story 11.8 — theme controller: system mode (the default), OS resolution
// with dark as the fallback when the OS states no preference, corrupted
// key → "system", live matchMedia tracking, and the no-flash boot script
// (public/theme-boot.js) that index.html loads before first paint.
import { describe, it, expect, beforeEach, vi, afterEach } from "vitest";
import { readMode, resolveTheme, applyResolvedTheme, setMode, watchSystemTheme } from "./theme";
import BOOT from "../../public/theme-boot.js?raw";
import INDEX from "../../index.html?raw";

type OSPref = "light" | "dark" | "none";

// matchMedia answering per query, like a browser: "none" matches neither
// (prefers-color-scheme: light) nor (... dark).
function mockMatchMedia(pref: OSPref) {
  const listeners = new Set<() => void>();
  const state = { pref };
  const mm = vi.fn((query: string) => ({
    get matches() {
      if (query.includes("light")) return state.pref === "light";
      if (query.includes("dark")) return state.pref === "dark";
      return false;
    },
    media: query,
    addEventListener: (_: string, fn: () => void) => listeners.add(fn),
    removeEventListener: (_: string, fn: () => void) => listeners.delete(fn),
  }));
  vi.stubGlobal("matchMedia", mm);
  return {
    fire: () => listeners.forEach((f) => f()),
    set: (p: OSPref) => (state.pref = p),
  };
}

function runBoot() {
  new Function(BOOT)();
  return document.documentElement.dataset.theme;
}

describe("theme", () => {
  beforeEach(() => {
    delete document.documentElement.dataset.theme;
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("defaults to system when no key is stored", () => {
    expect(readMode()).toBe("system");
  });

  it("EC: a corrupted stored value falls back to system, not light", () => {
    localStorage.setItem("mkt:theme", "chartreuse");
    expect(readMode()).toBe("system");
  });

  it("resolves system → dark when the OS prefers dark", () => {
    mockMatchMedia("dark");
    expect(resolveTheme("system")).toBe("dark");
    expect(applyResolvedTheme("system")).toBe("dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
  });

  it("resolves system → light only when the OS asks for light", () => {
    mockMatchMedia("light");
    expect(resolveTheme("system")).toBe("light");
  });

  it("falls back to dark when the OS states no preference", () => {
    mockMatchMedia("none");
    expect(resolveTheme()).toBe("dark");
  });

  it("falls back to dark when matchMedia is unavailable", () => {
    vi.stubGlobal("matchMedia", undefined);
    expect(resolveTheme("system")).toBe("dark");
  });

  it("an explicit choice wins over the OS preference", () => {
    mockMatchMedia("dark");
    setMode("light");
    expect(readMode()).toBe("light");
    expect(document.documentElement.dataset.theme).toBe("light");
  });

  it("watchSystemTheme re-applies on OS change while in system mode", () => {
    const mm = mockMatchMedia("light");
    setMode("system");
    expect(document.documentElement.dataset.theme).toBe("light");
    const onChange = vi.fn();
    const off = watchSystemTheme(onChange);
    mm.set("dark");
    mm.fire();
    expect(onChange).toHaveBeenCalledWith("dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
    off();
  });

  it("watchSystemTheme leaves an explicit choice alone", () => {
    const mm = mockMatchMedia("light");
    setMode("light");
    const off = watchSystemTheme();
    mm.set("dark");
    mm.fire();
    expect(document.documentElement.dataset.theme).toBe("light");
    off();
  });
});

describe("no-flash boot (public/theme-boot.js)", () => {
  beforeEach(() => {
    delete document.documentElement.dataset.theme;
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("index.html loads it as a blocking same-origin script in <head>, before the app", () => {
    const head = INDEX.slice(0, INDEX.indexOf("</head>"));
    // blocking: a classic script (no defer/async/module) inside <head>
    expect(head).toMatch(/<script src="\/theme-boot\.js"><\/script>/);
    // the CSP (default-src 'self') forbids inline script: none may remain
    const doc = new DOMParser().parseFromString(INDEX, "text/html");
    const scripts = [...doc.querySelectorAll("script")];
    expect(scripts.length).toBeGreaterThan(0);
    expect(scripts.every((el) => el.hasAttribute("src"))).toBe(true);
    expect(INDEX.indexOf("/theme-boot.js")).toBeLessThan(INDEX.indexOf("/src/main.tsx"));
    // the UA canvas is dark before any CSS when the OS has no preference
    expect(head).toMatch(/<meta name="color-scheme" content="dark light"/);
  });

  it.each([
    ["no stored choice, OS light", null, "light", "light"],
    ["no stored choice, OS dark", null, "dark", "dark"],
    ["no stored choice, no OS preference", null, "none", "dark"],
    ["stored system, OS light", "system", "light", "light"],
    ["stored dark, OS light", "dark", "light", "dark"],
    ["stored light, OS dark", "light", "dark", "light"],
    ["corrupted value, no OS preference", "chartreuse", "none", "dark"],
  ] as const)("%s → %s", (_name, stored, os, want) => {
    if (stored !== null) localStorage.setItem("mkt:theme", stored);
    mockMatchMedia(os);
    expect(runBoot()).toBe(want);
    // and the runtime controller agrees with the boot script
    expect(resolveTheme()).toBe(want);
  });

  it("resolves dark when matchMedia is missing", () => {
    vi.stubGlobal("matchMedia", undefined);
    expect(runBoot()).toBe("dark");
  });

  it("resolves dark when storage throws", () => {
    mockMatchMedia("none");
    const spy = vi.spyOn(localStorage, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(runBoot()).toBe("dark");
    spy.mockRestore();
  });
});
