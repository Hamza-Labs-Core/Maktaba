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

  // found by following the guide: This machine shows no "runs as" user, setup's folders are
  // added as libraries in Manage, and admin is the sentinel account's name
  it("finishes setup as the container does", () => {
    expect(SYN.en["step5.body"]).not.toContain("runs as");
    expect(SYN.en["step5.body"]).toContain("Manage → Manage libraries → Add library");
    expect(SYN.ar["step5.body"]).toContain("الإدارة ← إدارة المكتبات ← إضافة مكتبة");
    expect(SYN.en["step5.body"]).toContain("not admin");
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

  it("leaves a slash between words, a command and a URL each in one piece", () => {
    // "Read/Write" is no path
    expect(st("ar", "step2.body")).not.toMatch(/Read⁦/);
    // a whole command stays one run: "stat -c %g /dev/dri/renderD128", in that order
    expect(st("ar", "field.renderGroup.help")).toMatch(/⁦stat -c %g ⁦\/dev\/dri\/renderD128⁩⁩$/);
    // a URL is never split at its slashes
    expect(st("ar", "trouble.port")).toContain("⁦http://<your NAS>:8780⁩");
  });
});
