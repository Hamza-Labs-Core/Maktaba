import { describe, expect, it } from "vitest";
import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";
import { updateCodeHelp } from "../../help/registry";

// Epic 51 story 51.18 (T09): the update's packs step and every reason it fails with have
// English and Arabic words, and a help entry of their own.
const PACK_CODES = [
  "pack_verify_failed",
  "pack_download_failed",
  "index_missing",
  "index_expired",
  "index_wrong_version",
  "index_replayed",
  "pack_missing",
  "components_busy",
  "packs_disabled",
  "packs_failed",
];

describe("the update's packs step words", () => {
  const words = [en, ar] as unknown as Record<string, string>[];

  it("names the step in English and Arabic", () => {
    for (const w of words) expect(w["updates.step.packs"]?.trim()).toBeTruthy();
    expect(en["updates.step.packs" as keyof typeof en]).not.toBe(
      ar["updates.step.packs" as keyof typeof ar]
    );
  });

  it("says each failure in English and Arabic, with its help entry", () => {
    for (const c of PACK_CODES) {
      for (const w of words) expect(w[`updates.code.${c}`]?.trim(), c).toBeTruthy();
      expect(updateCodeHelp(c), c).toEqual({ topic: "updates", anchor: "code-packs" });
    }
  });
});
