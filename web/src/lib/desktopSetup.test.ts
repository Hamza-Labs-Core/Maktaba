// Epic 36: the Mac app's setup bridge helpers.
import { describe, it, expect, afterEach, vi } from "vitest";
import { formatBytes, isBundledDesktop, isStepError, progressPercent } from "./desktopSetup";

describe("isBundledDesktop", () => {
  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).__TAURI_INTERNALS__;
    vi.unstubAllGlobals();
  });

  it("is false in a browser and on the local server's web app inside the shell", () => {
    expect(isBundledDesktop()).toBe(false);
    (window as unknown as Record<string, unknown>).__TAURI_INTERNALS__ = {};
    // jsdom serves http://localhost:3000, like the server's own web app
    expect(isBundledDesktop()).toBe(false);
  });

  it("is true on the app's own pages", () => {
    (window as unknown as Record<string, unknown>).__TAURI_INTERNALS__ = {};
    vi.stubGlobal("location", { protocol: "tauri:", hostname: "localhost" });
    expect(isBundledDesktop()).toBe(true);
    vi.stubGlobal("location", { protocol: "http:", hostname: "tauri.localhost" });
    expect(isBundledDesktop()).toBe(true);
  });
});

describe("formatBytes / progressPercent", () => {
  it("formats sizes", () => {
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1536)).toBe("1.5 KB");
    expect(formatBytes(96 * 2 ** 30)).toBe("96.0 GB");
    expect(formatBytes(-1)).toBe("?");
  });

  it("gives a percent only with a total", () => {
    expect(progressPercent(null)).toBeNull();
    expect(progressPercent({ stage: "venv", message: "" })).toBeNull();
    expect(progressPercent({ stage: "venv", message: "", done: 5, total: 0 })).toBeNull();
    expect(progressPercent({ stage: "venv", message: "", done: 5, total: 20 })).toBe(25);
    expect(progressPercent({ stage: "venv", message: "", done: 30, total: 20 })).toBe(100);
  });

  it("recognises a step error", () => {
    expect(isStepError({ code: "x", message: "y", details: [] })).toBe(true);
    expect(isStepError(new Error("x"))).toBe(false);
    expect(isStepError("x")).toBe(false);
  });
});
