import { describe, expect, it } from "vitest";
import { keyAction } from "./keys";

const key = (code: string, extra: Partial<KeyboardEventInit> = {}) =>
  new KeyboardEvent("keydown", { code, key: "x", ...extra });

// Epic 52 (story 52.11 §3): physical keys, off in the edit field.
describe("review keys", () => {
  it("maps the physical keys whatever the layout types", () => {
    // an Arabic layout: the N key types «ى» but its code is still KeyN
    expect(keyAction(new KeyboardEvent("keydown", { code: "KeyN", key: "ى" }), false)).toEqual({
      kind: "name",
    });
    expect(keyAction(key("KeyJ"), false)).toEqual({ kind: "next" });
    expect(keyAction(key("ArrowUp"), false)).toEqual({ kind: "prev" });
    expect(keyAction(key("Digit2"), false)).toEqual({ kind: "candidate", n: 1 });
    expect(keyAction(key("Space", { shiftKey: true }), false)).toEqual({
      kind: "play",
      slow: true,
    });
    expect(keyAction(key("KeyZ", { ctrlKey: true }), false)).toEqual({ kind: "undo" });
    expect(keyAction(key("Slash", { shiftKey: true }), false)).toEqual({ kind: "help" });
  });
  it("types letters in the edit field and saves with Enter", () => {
    expect(keyAction(key("KeyN"), true)).toBeNull();
    expect(keyAction(key("Enter"), true)).toEqual({ kind: "accept" });
    expect(keyAction(key("Escape"), true)).toEqual({ kind: "leave" });
  });
});
