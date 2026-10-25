import { describe, expect, it } from "vitest";

import swSource from "../../public/sw.js?raw";
import { serviceWorkerBuild, stampServiceWorker } from "./swStamp";

describe("the service worker's build stamp", () => {
  it("stamps public/sw.js, so each release is a new worker", () => {
    const src = swSource;
    const a = stampServiceWorker(src, serviceWorkerBuild("0.3.2", "abcdef0123456789"));
    const b = stampServiceWorker(src, serviceWorkerBuild("0.3.2", "fedcba9876543210"));
    expect(a).toContain('const BUILD = "0.3.2-abcdef012345";');
    expect(a).not.toEqual(b);
    expect(a).toContain('const SHELL_CACHE = "mkt-shell-" + BUILD;');
  });

  it("refuses a worker it cannot stamp", () => {
    expect(() => stampServiceWorker("self.addEventListener('fetch', () => {});", "x")).toThrow();
  });

  it("keeps the stamp to safe characters", () => {
    expect(serviceWorkerBuild('v0.3"2', "00112233445566")).toBe("v0.32-001122334455");
    expect(serviceWorkerBuild("", "0011223344556677")).toBe("dev-001122334455");
  });
});
