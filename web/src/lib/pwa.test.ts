import { describe, expect, it, vi } from "vitest";

import { reloadOnStaleChunks } from "./pwa";

describe("reloadOnStaleChunks", () => {
  it("reloads once when an older release's chunk is gone, not in a loop", () => {
    const target = new EventTarget();
    const reload = vi.fn();
    const store = new Map<string, string>();
    let clock = 1_000_000;
    reloadOnStaleChunks(
      {
        addEventListener: target.addEventListener.bind(target),
        location: { reload } as unknown as Location,
      } as unknown as Window,
      { getItem: (k) => store.get(k) ?? null, setItem: (k, v) => void store.set(k, v) },
      () => clock
    );
    target.dispatchEvent(new Event("vite:preloadError", { cancelable: true }));
    expect(reload).toHaveBeenCalledTimes(1);
    clock += 5_000;
    target.dispatchEvent(new Event("vite:preloadError", { cancelable: true }));
    expect(reload).toHaveBeenCalledTimes(1);
    clock += 120_000;
    target.dispatchEvent(new Event("vite:preloadError", { cancelable: true }));
    expect(reload).toHaveBeenCalledTimes(2);
  });
});
