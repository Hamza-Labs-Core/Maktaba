// maktaba.tv's NAS model table (Epic 59 story 59.14): nas-models.json is data in one shape, and
// each model's fit follows the server's tier rule (internal/hostinfo/tier.go) at the memory it
// ships with, or at the most it takes.
import { describe, expect, it } from "vitest";
import data from "./nas-models.json";
import { CHECKED, MODELS, detectedTier, fitOf, modelsOf } from "./nasModels";
import { MODELS as SYNOLOGY } from "./synology";

const byName = (n: string) => MODELS.find((m) => m.name === n)!;

describe("the NAS model table", () => {
  it("is data in one shape, each model named once", () => {
    expect(CHECKED).toMatch(/^\d{4}-\d{2}-\d{2}$/);
    const names = new Set<string>();
    for (const m of data.models) {
      expect(Object.keys(m).sort()).toEqual(
        ["arch", "cpu", "gpu", "maxRam", "name", "ram", "vendor"].sort()
      );
      expect(["synology", "qnap", "ugreen", "asustor", "other"]).toContain(m.vendor);
      expect(["amd64", "arm64"]).toContain(m.arch);
      expect(m.maxRam).toBeGreaterThanOrEqual(m.ram);
      expect(names.has(m.name)).toBe(false);
      names.add(m.name);
    }
  });

  it("covers each NAS the guides name", () => {
    for (const v of ["synology", "qnap", "ugreen", "asustor"] as const)
      expect(modelsOf(v).length).toBeGreaterThan(0);
    expect(SYNOLOGY).toEqual(modelsOf("synology"));
  });

  it("follows the server's tiers: Light, Standard, Strong, and storage under 3.5 GiB", () => {
    expect(detectedTier(4, "amd64")).toBe("light");
    expect(detectedTier(2, "amd64")).toBe("storage");
    expect(detectedTier(8, "amd64")).toBe("standard");
    expect(detectedTier(8, "arm64")).toBe("light");
    expect(detectedTier(32, "amd64")).toBe("strong");
  });

  it("says fits, fits with more memory, or storage", () => {
    expect(fitOf(byName("DS923+"))).toEqual({ kind: "fits", tier: "light" });
    expect(fitOf(byName("DXP2800"))).toEqual({ kind: "fits", tier: "standard" });
    expect(fitOf(byName("DS224+"))).toEqual({ kind: "more", tier: "light", maxRam: 6 });
    expect(fitOf(byName("DS223j"))).toEqual({ kind: "storage" });
    expect(fitOf(byName("TS-233"))).toEqual({ kind: "storage" });
  });
});
