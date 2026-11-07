// lib/intl (#697): one formatter per locale and options, formatting as a new one would.
import { describe, it, expect } from "vitest";
import { dateTimeFormat, numberFormat } from "./intl";

describe("kept Intl formatters", () => {
  it("makes a number formatter once per locale and options", () => {
    const a = numberFormat("en", { maximumFractionDigits: 1 });
    expect(numberFormat("en", { maximumFractionDigits: 1 })).toBe(a);
    expect(numberFormat("en", { maximumFractionDigits: 0 })).not.toBe(a);
    expect(numberFormat("ar-EG", { maximumFractionDigits: 1 })).not.toBe(a);
    expect(numberFormat("en")).toBe(numberFormat("en"));
    expect(a.format(12.34)).toBe(
      new Intl.NumberFormat("en", { maximumFractionDigits: 1 }).format(12.34)
    );
    expect(numberFormat("ar-EG").format(42)).toBe(new Intl.NumberFormat("ar-EG").format(42));
  });

  it("makes a date formatter once per locale and options", () => {
    const opts = { hour: "2-digit", minute: "2-digit", timeZone: "UTC" } as const;
    const a = dateTimeFormat("en", opts);
    expect(dateTimeFormat("en", { ...opts })).toBe(a);
    expect(dateTimeFormat("ar-EG", opts)).not.toBe(a);
    const d = new Date(Date.UTC(2026, 10, 6, 21, 5));
    expect(a.format(d)).toBe(new Intl.DateTimeFormat("en", opts).format(d));
  });
});
