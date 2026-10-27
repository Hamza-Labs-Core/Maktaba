// Epic 42.9: the cloud's editions live under /play/ or /manage/; the home's at the root.
import { describe, it, expect, vi, afterEach } from "vitest";

afterEach(() => {
  vi.unstubAllEnvs();
  vi.resetModules();
});

describe("the base path", () => {
  it("is the root on the home", async () => {
    vi.stubEnv("BASE_URL", "/");
    const m = await import("./basePath");
    expect([m.BASE, m.ROUTER_BASENAME, m.IN_CLOUD]).toEqual(["", undefined, false]);
  });

  it("is the edition's under the cloud", async () => {
    vi.stubEnv("BASE_URL", "/play/");
    const m = await import("./basePath");
    expect([m.BASE, m.ROUTER_BASENAME, m.IN_CLOUD]).toEqual(["/play", "/play", true]);
  });
});
