import { describe, it, expect, vi, afterEach } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { useDelayedFlag } from "./useDelayedFlag";

describe("useDelayedFlag", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("is true only once the flag has held for the delay", () => {
    vi.useFakeTimers();
    const { result } = renderHook(({ on }) => useDelayedFlag(on, 400), {
      initialProps: { on: true },
    });
    expect(result.current).toBe(false);
    act(() => {
      vi.advanceTimersByTime(399);
    });
    expect(result.current).toBe(false);
    act(() => {
      vi.advanceTimersByTime(1);
    });
    expect(result.current).toBe(true);
  });

  it("stays false when the flag drops first, and drops at once with it", () => {
    vi.useFakeTimers();
    const { result, rerender } = renderHook(({ on }) => useDelayedFlag(on, 400), {
      initialProps: { on: true },
    });
    act(() => {
      vi.advanceTimersByTime(200);
    });
    rerender({ on: false });
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(result.current).toBe(false);

    rerender({ on: true });
    act(() => {
      vi.advanceTimersByTime(400);
    });
    expect(result.current).toBe(true);
    rerender({ on: false });
    expect(result.current).toBe(false);
  });
});
