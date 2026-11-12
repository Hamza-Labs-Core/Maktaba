// @vitest-environment node
// Story 60.06 T11's logic: what the problem index's filter matches, and the count line in each
// language's plural forms. The page itself (typing narrows the list, no request) is
// e2e/problems.spec.ts's.
import { describe, expect, it } from "vitest";
import { countText, fold, matches } from "./problems-filter";

describe("the problem filter", () => {
  it("folds case, Arabic short vowels, alef forms and slug separators", () => {
    expect(fold("Seat_Limit  seat-limit")).toBe("seat limit seat limit");
    expect(fold("إعادةُ المحاولة")).toBe("اعاده المحاوله");
    expect(fold("مستخدَم")).toBe("مستخدم");
  });

  it("matches every word of the query, in any order; an empty query matches all", () => {
    const text = "This home has no free seats seat-limit";
    expect(matches("", text)).toBe(true);
    expect(matches("  ", text)).toBe(true);
    expect(matches("SEATS home", text)).toBe(true);
    expect(matches("seat limit", text)).toBe(true);
    expect(matches("seat_limit", text)).toBe(true);
    expect(matches("password", text)).toBe(false);
    expect(matches("مقاعد", "لا مقاعد شاغرة في هذا البيت")).toBe(true);
    expect(matches("المقاعد", "لا مقاعد شاغرة")).toBe(false);
  });

  it("writes the count in the page's language and plural form", () => {
    const en = {
      lang: "en",
      dataset: { countOne: "1 message", countOther: "{n} messages" } as DOMStringMap,
    };
    expect(countText(en, 1)).toBe("1 message");
    expect(countText(en, 0)).toBe("0 messages");
    expect(countText(en, 288)).toBe("288 messages");
    const ar = {
      lang: "ar",
      dataset: {
        countZero: "لا رسالة",
        countOne: "رسالة واحدة",
        countTwo: "رسالتان",
        countFew: "{n} رسائل",
        countMany: "{n} رسالة",
        countOther: "{n} رسالة",
      } as DOMStringMap,
    };
    expect([0, 1, 2, 5, 11, 288].map((n) => countText(ar, n))).toEqual([
      "لا رسالة",
      "رسالة واحدة",
      "رسالتان",
      "5 رسائل",
      "11 رسالة",
      "288 رسالة",
    ]);
  });
});
