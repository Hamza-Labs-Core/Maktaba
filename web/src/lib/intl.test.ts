// lib/intl (#697, #1199): one formatter per locale and options, with Western digits in Arabic.
import { describe, it, expect } from "vitest";
import { dateTimeFormat, intlLocale, numberFormat, relativeTimeFormat } from "./intl";

const EASTERN_ARABIC_DIGITS = /[٠-٩]/;

describe("kept Intl formatters", () => {
  it("normalizes Arabic locales to Western digits while preserving explicit numbering systems", () => {
    expect(intlLocale("en")).toBe("en");
    expect(intlLocale("ar")).toBe("ar-u-nu-latn");
    expect(intlLocale("ar-EG")).toBe("ar-EG-u-nu-latn");
    expect(intlLocale("ar_SA")).toBe("ar-SA-u-nu-latn");
    expect(intlLocale("ar-u-nu-latn")).toBe("ar-u-nu-latn");
    expect(intlLocale("ar-EG-u-nu-arab")).toBe("ar-EG-u-nu-arab");
  });

  it("makes a number formatter once per locale and options, with Western digits in Arabic", () => {
    const a = numberFormat("en", { maximumFractionDigits: 1 });
    expect(numberFormat("en", { maximumFractionDigits: 1 })).toBe(a);
    expect(numberFormat("en", { maximumFractionDigits: 0 })).not.toBe(a);
    expect(numberFormat("ar", { maximumFractionDigits: 1 })).not.toBe(a);
    expect(numberFormat("en")).toBe(numberFormat("en"));
    expect(a.format(12.34)).toBe(
      new Intl.NumberFormat("en", { maximumFractionDigits: 1 }).format(12.34)
    );
    for (const tag of ["ar", "ar-EG", "ar-SA"]) {
      const num = numberFormat(tag).format(12345.6);
      expect(num).toContain("12");
      expect(num).not.toMatch(EASTERN_ARABIC_DIGITS);
      const year = numberFormat(tag, { useGrouping: false }).format(2024);
      expect(year).toBe("2024");
      expect(year).not.toMatch(EASTERN_ARABIC_DIGITS);
    }
  });

  it("makes a date formatter once per locale and options, with Western digits in Arabic", () => {
    const opts = { hour: "2-digit", minute: "2-digit", timeZone: "UTC" } as const;
    const a = dateTimeFormat("en", opts);
    expect(dateTimeFormat("en", { ...opts })).toBe(a);
    expect(dateTimeFormat("ar", opts)).not.toBe(a);
    const d = new Date(Date.UTC(2026, 10, 6, 21, 5));
    expect(a.format(d)).toBe(new Intl.DateTimeFormat("en", opts).format(d));
    for (const tag of ["ar", "ar-EG", "ar-SA"]) {
      const formatted = dateTimeFormat(tag, {
        dateStyle: "medium",
        timeStyle: "short",
        timeZone: "UTC",
      }).format(d);
      expect(formatted).toMatch(/[0-9]/);
      expect(formatted).not.toMatch(EASTERN_ARABIC_DIGITS);
    }
  });

  it("formats Arabic relative times with Western digits", () => {
    const a = relativeTimeFormat("ar", { numeric: "auto" });
    expect(relativeTimeFormat("ar-u-nu-latn", { numeric: "auto" })).toBe(a);
    for (const tag of ["ar", "ar-EG"]) {
      const rel = relativeTimeFormat(tag, { numeric: "auto" }).format(-5, "minute");
      expect(rel).toContain("5");
      expect(rel).not.toMatch(EASTERN_ARABIC_DIGITS);
    }
  });
});
