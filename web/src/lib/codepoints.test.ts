import { describe, expect, it } from "vitest";
import { cpSlice, fromCodePoints, toCodePoints } from "./codepoints";

// Epic 52 (story 52.12): the edit API counts code points; JavaScript counts UTF-16 units.
describe("code points", () => {
  it("counts an emoji as one and a combining mark as one", () => {
    const text = "😀 بِسْمِ word";
    const at = text.indexOf("word");
    expect(at).toBe(10); // 2 units for the emoji
    expect(toCodePoints(text, at)).toBe(9);
    expect(fromCodePoints(text, 9)).toBe(at);
    expect(cpSlice(text, 9, 4)).toBe("word");
  });
  it("keeps tatweel and a ZWJ as their own code points", () => {
    const text = "كـتاب ‍x";
    expect(toCodePoints(text, text.length)).toBe(Array.from(text).length);
    expect(cpSlice(text, 0, 5)).toBe("كـتاب");
  });
});
