// Story 51.11 follow-up: a feature that is off reads as its own sentence, so the verb
// agrees with the name in English (plural names) and Arabic (gender); a feature this
// build has no sentence for reads through the grammar-safe frame.
import { describe, expect, it } from "vitest";
import en from "../i18n/en.json";
import ar from "../i18n/ar.json";
import { featureOffText, featureUnavailableText } from "./featureWords";

const words =
  (dict: Record<string, string>) => (k: string, vars?: Record<string, string | number>) =>
    (dict[k] ?? k).replace(/\{(\w+)\}/g, (_, n: string) => String(vars?.[n] ?? `{${n}}`));
const enT = words(en as Record<string, string>);
const arT = words(ar as Record<string, string>);

describe("featureOffText", () => {
  it("agrees with a plural name in English", () => {
    expect(featureOffText("transcripts", enT)).toBe("Transcripts are off on this home.");
    expect(featureOffText("live_tv", enT)).toBe("Live TV is off on this home.");
    expect(featureUnavailableText("chapters", enT)).toBe("Chapters aren't available on this home.");
  });

  it("agrees with a masculine and a feminine name in Arabic", () => {
    expect(featureOffText("live_tv", arT)).toBe("البث المباشر متوقف على الجهاز الرئيسي.");
    expect(featureOffText("transcripts", arT)).toBe("نصوص الفيديو متوقفة على الجهاز الرئيسي.");
    expect(featureUnavailableText("live_tv", arT)).toBe(
      "البث المباشر غير متاح على الجهاز الرئيسي."
    );
  });

  it("frames a feature it has no sentence for", () => {
    expect(featureOffText("from_the_future", enT)).toBe(
      "The from_the_future feature is off on this home."
    );
    expect(featureOffText("from_the_future", arT)).toBe(
      "ميزة from_the_future متوقفة على الجهاز الرئيسي."
    );
    expect(featureUnavailableText("from_the_future", enT)).toBe(
      "The from_the_future feature isn't available on this home."
    );
  });
});
