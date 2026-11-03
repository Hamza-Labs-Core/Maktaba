// T13 (Epic 51, story 51.06): a stage stopped by its component reads in the page's language,
// with the component's own title.
import { describe, expect, it } from "vitest";
import { lookupMessage, type Locale } from "../../lib/i18n";
import { componentTitle, reasonWords } from "./format";

function tFor(locale: Locale) {
  return (key: string, vars?: Record<string, string | number>) => {
    const hit = lookupMessage(locale, key);
    if (hit === undefined) return key;
    return hit.replace(/\{(\w+)\}/g, (m, k) => (vars && k in vars ? String(vars[k]) : m));
  };
}

describe("reasonWords", () => {
  it("names the component in Arabic", () => {
    const t = tFor("ar");
    const title = t("components.transcription.title");
    expect(title).not.toBe("components.transcription.title");
    expect(reasonWords("component_off:transcription", t)).toBe(
      t("machines.reason.component_off", { component: title })
    );
    expect(reasonWords("component_off:transcription", t)).toContain(title);
    expect(reasonWords("component_held:transcription", t)).toContain(title);
    expect(reasonWords("component_held:transcription", t)).not.toBe(
      reasonWords("component_off:transcription", t)
    );
  });

  it("names the component in English", () => {
    const t = tFor("en");
    expect(reasonWords("component_off:media.thumbnails", t)).toBe(
      `${t("components.media.thumbnails.title")} is off`
    );
  });

  it("keeps an unknown component's id and the other reasons as before", () => {
    const t = tFor("en");
    expect(componentTitle("from.the.future", t)).toBe("from.the.future");
    expect(reasonWords("component_off:from.the.future", t)).toBe("from.the.future is off");
    expect(reasonWords("unavailable: no GPU", t)).toBe("cannot run here: no GPU");
    expect(reasonWords("something_new", t)).toBe("something_new");
  });
});
