import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { split } from "./split.js";

// T01: Maktaba's 15 % rounded down, the developer the rest, the two sum to the price
describe("split", () => {
  const { cases } = JSON.parse(readFileSync(join(import.meta.dirname, "testdata", "split.json"), "utf8")) as {
    cases: { price: number; currency: string; maktaba: number; developer: number }[];
  };
  it.each(cases)("$price $currency", (c) => {
    const s = split({ minor: c.price, currency: c.currency });
    expect([s.maktaba.minor, s.developer.minor]).toEqual([c.maktaba, c.developer]);
    expect(s.maktaba.minor + s.developer.minor).toBe(c.price);
    expect(s.developer.currency).toBe(c.currency);
  });
  it("refuses a fraction", () => {
    expect(() => split({ minor: 9.5, currency: "USD" })).toThrow();
  });
});
