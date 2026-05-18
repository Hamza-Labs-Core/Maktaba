// Story 11.8 — theme controller: system mode, OS resolution, corrupted
// key → "system" fallback (NOT light), live matchMedia tracking.
import { describe, it, expect, beforeEach, vi, afterEach } from "vitest";
import { readMode, resolveTheme, applyResolvedTheme, setMode, watchSystemTheme } from "./theme";

function mockMatchMedia(dark: boolean) {
  const listeners = new Set<() => void>();
  const mql = {
    matches: dark,
    media: "(prefers-color-scheme: dark)",
    addEventListener: (_: string, fn: () => void) => listeners.add(fn),
    removeEventListener: (_: string, fn: () => void) => listeners.delete(fn),
  };
  vi.stubGlobal(
    "matchMedia",
    vi.fn(() => mql)
  );
  return { fire: () => listeners.forEach((f) => f()), set: (v: boolean) => (mql.matches = v) };
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
    mockMatchMedia(true);
    expect(resolveTheme("system")).toBe("dark");
    expect(applyResolvedTheme("system")).toBe("dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
  });

  it("an explicit choice wins over the OS preference", () => {
    mockMatchMedia(true);
    setMode("light");
    expect(readMode()).toBe("light");
    expect(document.documentElement.dataset.theme).toBe("light");
  });

  it("watchSystemTheme re-applies on OS change while in system mode", () => {
    const mm = mockMatchMedia(false);
    setMode("system");
    const onChange = vi.fn();
    const off = watchSystemTheme(onChange);
    mm.set(true);
    mm.fire();
    expect(onChange).toHaveBeenCalledWith("dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
    off();
  });
});
