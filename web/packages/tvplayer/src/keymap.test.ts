// 34.6 TC1 — the key map: keyboard, TV remote codes, RTL.
import { describe, expect, it } from "vitest";
import { fromTextField, keyToAction } from "./keymap";

describe("keyToAction", () => {
  it.each([
    ["ArrowUp", "next"],
    ["PageUp", "next"],
    ["ChannelUp", "next"],
    ["ArrowDown", "prev"],
    ["PageDown", "prev"],
    ["ChannelDown", "prev"],
    ["Enter", "enter"],
    ["Backspace", "last"],
    ["l", "last"],
    ["L", "last"],
    ["g", "guide"],
    ["G", "guide"],
    ["i", "info"],
    ["I", "info"],
    ["Escape", "escape"],
  ])("%s → %s", (key, type) => {
    expect(keyToAction({ key })?.type).toBe(type);
  });

  it("maps digits, numpad digits and remote number codes", () => {
    expect(keyToAction({ key: "7" })).toEqual({ type: "digit", digit: "7" });
    expect(keyToAction({ key: "Unidentified", code: "Numpad4" })).toEqual({
      type: "digit",
      digit: "4",
    });
    expect(keyToAction({ key: "Unidentified", keyCode: 53 })).toEqual({
      type: "digit",
      digit: "5",
    });
  });

  it("maps TV remote key codes (Tizen, Android TV, webOS)", () => {
    expect(keyToAction({ key: "Unidentified", keyCode: 427 })?.type).toBe("next");
    expect(keyToAction({ key: "Unidentified", keyCode: 428 })?.type).toBe("prev");
    expect(keyToAction({ key: "Unidentified", keyCode: 166 })?.type).toBe("next");
    expect(keyToAction({ key: "Unidentified", keyCode: 167 })?.type).toBe("prev");
    expect(keyToAction({ key: "Unidentified", keyCode: 10009 })?.type).toBe("escape");
    expect(keyToAction({ key: "Unidentified", keyCode: 461 })?.type).toBe("escape");
  });

  it("keeps up = next channel in RTL (nothing is mirrored)", () => {
    document.documentElement.dir = "rtl";
    try {
      expect(keyToAction({ key: "ArrowUp" })?.type).toBe("next");
      expect(keyToAction({ key: "ArrowDown" })?.type).toBe("prev");
      // left/right stay the browser's (seek bar, RTL scrolling)
      expect(keyToAction({ key: "ArrowLeft" })).toBeNull();
      expect(keyToAction({ key: "ArrowRight" })).toBeNull();
    } finally {
      document.documentElement.dir = "";
    }
  });

  it("leaves browser shortcuts and unknown keys alone", () => {
    expect(keyToAction({ key: "l", ctrlKey: true })).toBeNull();
    expect(keyToAction({ key: "r", metaKey: true })).toBeNull();
    expect(keyToAction({ key: "x" })).toBeNull();
  });

  it("recognises text fields", () => {
    expect(fromTextField(document.createElement("input"))).toBe(true);
    expect(fromTextField(document.createElement("textarea"))).toBe(true);
    expect(fromTextField(document.createElement("div"))).toBe(false);
    expect(fromTextField(null)).toBe(false);
  });
});
