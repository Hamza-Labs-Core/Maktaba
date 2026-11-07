import { describe, expect, it } from "vitest";
import { fakeConfig, registry } from "../testing/harness.js";

describe("the provider registry", () => {
  it("chooses by the buyer's country and currency, in the admin's order", () => {
    const r = registry([
      fakeConfig({ priority: 20 }),
      { ...fakeConfig({ id: "adyen", priority: 10, regions: ["SA", "AE", "EG"] }), settings: { merchantAccount: "MaktabaStore" } },
    ]);
    expect(r.issues).toEqual([]);
    const sa = r.choose({ country: "SA", currency: "SAR" });
    expect(typeof sa !== "string" && sa.config.id).toBe("adyen");
    const de = r.choose({ country: "DE", currency: "EUR" });
    expect(typeof de !== "string" && de.config.id).toBe("fake");
    expect(r.list().map((c) => c.config.id)).toEqual(["adyen", "fake"]);
  });

  it("says why none takes a sale", () => {
    const r = registry([fakeConfig({ regions: ["SA"], currencies: ["SAR"] })]);
    expect(r.choose({ country: "US", currency: "USD" })).toBe("no_provider_for_region");
    expect(r.choose({ country: "SA", currency: "USD" })).toBe("no_provider_for_currency");
    expect(registry([]).choose({ country: "SA", currency: "SAR" })).toBe("no_provider");
  });

  it("skips a marketplace provider the developer has no account with", () => {
    const r = registry([fakeConfig()]);
    expect(r.choose({ country: "SA", currency: "SAR", developerProviders: [] })).toBe("no_provider_for_currency");
    const ok = r.choose({ country: "SA", currency: "SAR", developerProviders: ["fake"] });
    expect(typeof ok).not.toBe("string");
  });

  it("uses a live provider only with its resale confirmation on record, and with its secrets", () => {
    const live = registry([fakeConfig({ mode: "live" })]);
    expect(live.list()).toEqual([]);
    expect(live.issues[0]).toMatch(/resale confirmation/);
    expect(registry([fakeConfig({ mode: "live", resaleConfirmedAt: "2026-11-01" })]).list()).toHaveLength(1);
    const noKeys = registry([{ ...fakeConfig({ id: "adyen" }), settings: { merchantAccount: "M" } }]);
    expect(noKeys.list().map((c) => c.config.id)).toEqual(["adyen"]);
    expect(registry([fakeConfig({ id: "nope" })]).issues[0]).toMatch(/no such provider module/);
    expect(registry([fakeConfig({ enabled: false })]).list()).toEqual([]);
  });
});
