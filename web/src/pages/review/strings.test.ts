import { describe, expect, it } from "vitest";
import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";
import { REASONS, SOURCES } from "../../lib/reviewApi";

const PREFIXES = [
  "review.",
  "vocabulary.",
  "wordReview.",
  "transcript.edit.",
  "transcription.flags.",
  "queue.backfill.",
];
const SAME = new Set(["review.source.whisper"]);

// Epic 52: every word review string has its Arabic, and it is not a copy of the English.
describe("word review strings", () => {
  const E = en as Record<string, string>;
  const A = ar as Record<string, string>;
  const keys = Object.keys(E).filter((k) => PREFIXES.some((p) => k.startsWith(p)));
  it("has them", () => expect(keys.length).toBeGreaterThan(150));
  it.each(keys)("%s has Arabic", (k) => {
    expect(A[k], k).toBeTruthy();
    if (!SAME.has(k)) expect(A[k], k).not.toBe(E[k]);
  });
  it("names every reason and source", () => {
    for (const r of REASONS) expect(E[`review.reason.${r}`], r).toBeTruthy();
    for (const s of SOURCES) expect(E[`review.source.${s}`], s).toBeTruthy();
  });
  it("has the six Arabic plural categories", () => {
    for (const c of ["zero", "one", "two", "few", "many", "other"])
      expect(A[`review.otherPlaces.${c}`], c).toBeTruthy();
  });
});
