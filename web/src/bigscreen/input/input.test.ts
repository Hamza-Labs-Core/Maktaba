import { describe, expect, it } from "vitest";
import { keyAction } from "./keys";
import {
  BUTTON_ACTIONS,
  PadReader,
  REPEAT_DELAY_MS,
  REPEAT_EVERY_MS,
  heldActions,
  padGlyphs,
  padLayout,
  type PadSnapshot,
} from "./gamepad";

describe("keyAction", () => {
  it("maps arrows, Enter and the back keys", () => {
    expect(keyAction({ key: "ArrowUp" })).toBe("up");
    expect(keyAction({ key: "ArrowDown" })).toBe("down");
    expect(keyAction({ key: "ArrowLeft" })).toBe("left");
    expect(keyAction({ key: "ArrowRight" })).toBe("right");
    expect(keyAction({ key: "Enter" })).toBe("select");
    expect(keyAction({ key: "Enter", code: "NumpadEnter" })).toBe("select");
    for (const k of ["Escape", "Backspace", "BrowserBack", "GoBack"])
      expect(keyAction({ key: k }), k).toBe("back");
  });

  it("maps the media keys", () => {
    expect(keyAction({ key: "MediaPlayPause" })).toBe("playPause");
    expect(keyAction({ key: "MediaPlay" })).toBe("play");
    expect(keyAction({ key: "MediaPause" })).toBe("pause");
    expect(keyAction({ key: "MediaTrackNext" })).toBe("next");
    expect(keyAction({ key: "MediaTrackPrevious" })).toBe("previous");
    expect(keyAction({ key: "MediaFastForward" })).toBe("seekForward");
    expect(keyAction({ key: "MediaRewind" })).toBe("seekBack");
  });

  it("Space plays in the player and is the browser's elsewhere", () => {
    expect(keyAction({ key: " " }, { player: true })).toBe("playPause");
    expect(keyAction({ key: " " })).toBeNull();
  });

  it("leaves a text field its keys, but up / down and Escape still leave it", () => {
    expect(keyAction({ key: "Backspace" }, { typing: true })).toBeNull();
    expect(keyAction({ key: "ArrowLeft" }, { typing: true })).toBeNull();
    expect(keyAction({ key: "a" }, { typing: true })).toBeNull();
    expect(keyAction({ key: "/" }, { typing: true })).toBeNull();
    expect(keyAction({ key: "ArrowDown" }, { typing: true })).toBe("down");
    expect(keyAction({ key: "Escape" }, { typing: true })).toBe("back");
  });

  it("ignores shortcuts with modifiers and IME composition", () => {
    expect(keyAction({ key: "ArrowLeft", metaKey: true })).toBeNull();
    expect(keyAction({ key: "Enter", ctrlKey: true })).toBeNull();
    expect(keyAction({ key: "Enter", isComposing: true })).toBeNull();
    expect(keyAction({ key: "Process", keyCode: 229 })).toBeNull();
  });

  it("/ is search and ContextMenu is the menu; other keys are nothing", () => {
    expect(keyAction({ key: "/" })).toBe("search");
    expect(keyAction({ key: "ContextMenu" })).toBe("menu");
    expect(keyAction({ key: "a" })).toBeNull();
    expect(keyAction({ key: "Tab" })).toBeNull();
  });
});

function pad(pressedIdx: number[] = [], axes: number[] = [0, 0, 0, 0]): PadSnapshot {
  return {
    id: "Xbox Wireless Controller (STANDARD GAMEPAD Vendor: 045e Product: 0b13)",
    mapping: "standard",
    buttons: Array.from({ length: 17 }, (_, i) => ({
      pressed: pressedIdx.includes(i),
      value: pressedIdx.includes(i) ? 1 : 0,
    })),
    axes,
  };
}

describe("gamepad", () => {
  it("every standard button has its action", () => {
    const want: Record<number, string> = {
      0: "select",
      1: "back",
      2: "playPause",
      3: "search",
      4: "previous",
      5: "next",
      6: "seekBack",
      7: "seekForward",
      8: "back",
      9: "menu",
      12: "up",
      13: "down",
      14: "left",
      15: "right",
    };
    for (const [i, a] of BUTTON_ACTIONS) expect(a, `button ${i}`).toBe(want[i]);
    for (const i of Object.keys(want).map(Number))
      expect([...heldActions(pad([i]))], `button ${i}`).toEqual([want[i]]);
    // Home / PS is the system's
    expect(heldActions(pad([16])).size).toBe(0);
  });

  it("reads the left stick past the dead zone, its stronger axis winning", () => {
    expect([...heldActions(pad([], [0.3, -0.4]))]).toEqual([]);
    expect([...heldActions(pad([], [0.9, 0.2]))]).toEqual(["right"]);
    expect([...heldActions(pad([], [-0.7, 0]))]).toEqual(["left"]);
    expect([...heldActions(pad([], [0.6, -0.8]))]).toEqual(["up"]);
    expect([...heldActions(pad([], [0.1, 0.95]))]).toEqual(["down"]);
  });

  it("counts an analog trigger past half way, and plain numbers as values", () => {
    const p = pad();
    const buttons = [...p.buttons] as Array<{ pressed: boolean; value: number }>;
    buttons[7] = { pressed: false, value: 0.7 };
    expect([...heldActions({ ...p, buttons })]).toEqual(["seekForward"]);
    expect([...heldActions({ buttons: [1, 0, 0.2], axes: [] })]).toEqual(["select"]);
  });

  it("a press acts once; a held direction repeats after the delay", () => {
    const r = new PadReader();
    expect(r.step(pad([0]), 0)).toEqual(["select"]);
    expect(r.step(pad([0]), 50)).toEqual([]);
    expect(r.step(pad([0]), 1000)).toEqual([]); // buttons never repeat
    expect(r.step(pad(), 1010)).toEqual([]);
    expect(r.step(pad([0]), 1020)).toEqual(["select"]);

    const d = new PadReader();
    expect(d.step(pad([15]), 0)).toEqual(["right"]);
    expect(d.step(pad([15]), REPEAT_DELAY_MS - 1)).toEqual([]);
    expect(d.step(pad([15]), REPEAT_DELAY_MS)).toEqual(["right"]);
    expect(d.step(pad([15]), REPEAT_DELAY_MS + REPEAT_EVERY_MS - 1)).toEqual([]);
    expect(d.step(pad([15]), REPEAT_DELAY_MS + REPEAT_EVERY_MS)).toEqual(["right"]);
    // let go and press again: at once
    expect(d.step(pad(), 2000)).toEqual([]);
    expect(d.step(pad([], [0, -1]), 2001)).toEqual(["up"]);
    d.reset();
    expect(d.step(pad([], [0, -1]), 2002)).toEqual(["up"]);
  });

  it("names the layout from the pad's id", () => {
    expect(padLayout("Xbox Wireless Controller (Vendor: 045e Product: 0b13)")).toBe("xbox");
    expect(padLayout("DualSense Wireless Controller (Vendor: 054c Product: 0ce6)")).toBe(
      "playstation"
    );
    expect(padLayout("054c-09cc-Wireless Controller")).toBe("playstation");
    expect(padLayout("8BitDo SN30 Pro")).toBe("generic");
    expect(padLayout(undefined)).toBe("generic");
    expect(padGlyphs("playstation")).toEqual({ select: "✕", back: "○" });
    expect(padGlyphs("xbox")).toEqual({ select: "A", back: "B" });
  });
});
