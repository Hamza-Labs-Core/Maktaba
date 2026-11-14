// The receiver's strings: English and Arabic, the sender's language picked, LTR times inside
// Arabic text.

import { describe, expect, it } from "vitest";
import ar from "../i18n/ar.json";
import en from "../i18n/en.json";
import { clock, dirOf, langOf, ltr, position, t } from "../src/receiver/i18n";

describe("the catalogs", () => {
  it("hold the same keys", () => expect(Object.keys(ar).sort()).toEqual(Object.keys(en).sort()));

  it("hold the story's strings", () => {
    expect(en["receiver.idle"]).toBe("Ready to play from Maktaba");
    expect(ar["receiver.upNext"]).toBe("التالي");
    expect(ar["receiver.remote"]).toBe("عبر Maktaba Cloud");
    expect(en["receiver.error.grant"]).toBe("This cast has ended. Cast again from your phone.");
  });
});

describe("t", () => {
  it("fills the placeholders", () => {
    expect(t("en", "receiver.loading", "Dune")).toBe("Getting Dune ready…");
    expect(t("ar", "receiver.loading", "Dune")).toBe("جارٍ تجهيز Dune…");
  });

  it("picks Arabic for ar and its regions, English otherwise", () => {
    expect(langOf("ar")).toBe("ar");
    expect(langOf("ar-SA")).toBe("ar");
    expect(langOf("en-GB")).toBe("en");
    expect(langOf(undefined)).toBe("en");
    expect(langOf("arm")).toBe("en");
    expect(dirOf("ar")).toBe("rtl");
  });
});

describe("times", () => {
  it("read h:mm:ss or m:ss", () => {
    expect(clock(0)).toBe("0:00");
    expect(clock(754)).toBe("12:34");
    expect(clock(5400)).toBe("1:30:00");
    expect(clock(Number.NaN)).toBe("0:00");
  });

  it("stay left to right inside Arabic text", () => {
    expect(ltr("1:05")).toBe("‎1:05‎");
    expect(position("ar", 65, 5400)).toBe("‎1:05‎ من ‎1:30:00‎");
    expect(position("en", 65, 5400)).toBe("‎1:05‎ of ‎1:30:00‎");
  });
});
