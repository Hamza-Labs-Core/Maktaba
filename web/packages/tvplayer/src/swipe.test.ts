// 34.6 TC1 — vertical swipes and taps.
import { describe, expect, it, vi } from "vitest";
import { attachGestures, classify } from "./gestures";
import { ChannelRing } from "./ring";
import { NumberEntry } from "./numberEntry";

describe("classify", () => {
  it("reads a quick short stroke as a tap", () => {
    expect(classify(2, 3, 120)).toBe("tap");
  });
  it("reads a vertical stroke as a swipe (up = next)", () => {
    expect(classify(5, -120, 250)).toBe("swipe_up");
    expect(classify(-5, 120, 250)).toBe("swipe_down");
  });
  it("leaves horizontal, short or slow strokes alone", () => {
    expect(classify(150, -60, 250)).toBeNull(); // mostly horizontal
    expect(classify(0, -30, 250)).toBeNull(); // too short
    expect(classify(0, -200, 2000)).toBeNull(); // too slow
    expect(classify(2, 2, 900)).toBeNull(); // a long press is no tap
  });
});

describe("attachGestures", () => {
  it("reports a swipe from pointer events and detaches", () => {
    const el = document.createElement("div");
    let t = 0;
    const seen = vi.fn();
    const off = attachGestures(el, seen, () => t);
    const ev = (type: string, y: number) => {
      const e = new MouseEvent(type, { clientX: 10, clientY: y }) as MouseEvent & {
        pointerId: number;
      };
      Object.defineProperty(e, "pointerId", { value: 1 });
      el.dispatchEvent(e);
    };
    ev("pointerdown", 300);
    t = 200;
    ev("pointerup", 150);
    expect(seen).toHaveBeenCalledWith("swipe_up");
    off();
    ev("pointerdown", 300);
    ev("pointerup", 150);
    expect(seen).toHaveBeenCalledTimes(1);
  });
});

describe("ChannelRing", () => {
  const ring = new ChannelRing([
    { id: "c", number: 30 },
    { id: "a", number: 1 },
    { id: "b", number: 7 },
  ]);
  it("orders by number and wraps at both ends", () => {
    expect(ring.step("a", 1)?.id).toBe("b");
    expect(ring.step("c", 1)?.id).toBe("a");
    expect(ring.step("a", -1)?.id).toBe("c");
    expect(ring.step(null, 1)?.id).toBe("a");
    expect(ring.byNumber(7)?.id).toBe("b");
    expect(ring.byNumber(8)).toBeNull();
    expect(ring.neighbours("a").map((c) => c.id)).toEqual(["c", "b"]);
  });
});

describe("NumberEntry", () => {
  it("commits on Enter or after the idle window, at most 4 digits", () => {
    vi.useFakeTimers();
    try {
      const changes: string[] = [];
      const commits: number[] = [];
      const ne = new NumberEntry(
        (b) => changes.push(b),
        (n) => commits.push(n)
      );
      ne.digit("1");
      ne.digit("0");
      ne.digit("5");
      expect(ne.buffer).toBe("105");
      vi.advanceTimersByTime(1499);
      expect(commits).toEqual([]);
      vi.advanceTimersByTime(1);
      expect(commits).toEqual([105]);
      ne.digit("4");
      ne.digit("7");
      expect(ne.commit()).toBe(true);
      expect(commits).toEqual([105, 47]);
      expect(ne.commit()).toBe(false);
      for (const d of "123456") ne.digit(d);
      expect(ne.buffer).toBe("3456");
      ne.cancel();
      expect(ne.buffer).toBe("");
    } finally {
      vi.useRealTimers();
    }
  });
});
