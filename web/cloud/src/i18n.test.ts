import { describe, expect, it } from "vitest";
import en from "./i18n/en.json";
import ar from "./i18n/ar.json";

describe("cloud catalogs", () => {
  it("Arabic has every English key and no extras", () => {
    expect(Object.keys(ar).sort()).toEqual(Object.keys(en).sort());
  });

  it("keeps the same {placeholders} in both languages", () => {
    const vars = (s: string) => (s.match(/\{\w+\}/g) ?? []).sort();
    const arMap = ar as Record<string, string>;
    for (const [k, v] of Object.entries(en as Record<string, string>)) {
      expect([k, vars(arMap[k])]).toEqual([k, vars(v)]);
    }
  });
});
