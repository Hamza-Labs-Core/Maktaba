import { describe, expect, it } from "vitest";
import { STRINGS } from "./strings";

describe("the install page's strings", () => {
  it("has every key in English and Arabic", () => {
    expect(Object.keys(STRINGS.ar).sort()).toEqual(Object.keys(STRINGS.en).sort());
  });

  it("has Arabic text in every Arabic string", () => {
    for (const [k, v] of Object.entries(STRINGS.ar)) {
      if (k === "langLink") continue;
      expect(/[؀-ۿ]/.test(v), k).toBe(true);
    }
  });
});
