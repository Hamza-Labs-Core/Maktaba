// Story 59.09: the Synology guide's text in both languages.
import { describe, expect, it } from "vitest";
import { st, SYN } from "./synologyStrings";

describe("the Synology guide's strings", () => {
  it("has every key in English and Arabic", () => {
    expect(Object.keys(SYN.ar).sort()).toEqual(Object.keys(SYN.en).sort());
  });

  it("has Arabic text in every Arabic string, and the same placeholders as the English", () => {
    const vars = (s: string) => (s.match(/\{\w+\}/g) ?? []).sort();
    for (const [k, v] of Object.entries(SYN.ar)) {
      if (k !== "langLink") expect(/[؀-ۿ]/.test(v), k).toBe(true);
      expect(vars(v), k).toEqual(vars(SYN.en[k as keyof typeof SYN.en]));
    }
  });

  it("fills in placeholders and leaves unknown ones", () => {
    expect(st("en", "fits", { tier: "Light" })).toBe("Fits: Light");
    expect(st("en", "memory", { ram: 2 })).toBe("2, up to {max}");
  });
});

describe("paths in the Arabic text", () => {
  it("are isolated left to right, the trailing full stop left outside", () => {
    expect(st("ar", "field.volume.help", { volume: "volume2" })).toContain(
      "⁦/volume2/docker/maktaba⁩."
    );
    expect(st("ar", "field.gpu")).toContain("⁦/dev/dri⁩)");
    expect(st("en", "field.gpu")).not.toContain("⁦");
  });
});
