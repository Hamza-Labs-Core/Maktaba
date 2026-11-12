// Epic 57 (parental controls), story 57.15 T06: every string the web shows for parental
// controls is in English and Arabic. The keys the stories name for the web (57.15's time
// strings, 57.10's live TV strings, 57.01's tier labels) are read from their Strings tables;
// every other `parental.*` and `live.parental.*` key of the English catalog has its Arabic too.
import { describe, expect, it } from "vitest";
import en from "./en.json";
import ar from "./ar.json";

// Files outside web/ are read at run time (the web image builds from web/ alone); vitest runs
// in web/.
const FS = "node:fs";
const { readFileSync } = (await import(/* @vite-ignore */ FS)) as {
  readFileSync: (path: string, encoding: "utf8") => string;
};
const DIR = "../specs/epics/57-parental-controls";
// the stories whose Strings tables name strings the web shows
const STORIES = [
  "story-57-01-age-tiers.md",
  "story-57-10-live-tv.md",
  "story-57-12-existing-homes.md",
  "story-57-15-time-on-clients.md",
];

const catalogEn = en as Record<string, string>;
const catalogAr = ar as Record<string, string>;
const ARABIC = /[؀-ۿ]/;
// the age tiers read the same in both languages, but for the mark that keeps the plus on
// the correct side
const TIER = /^parental\.tier\.(9|13|16|18)$/;
const LRM = "‎";

function specKeys(): string[] {
  const out: string[] = [];
  for (const file of STORIES) {
    const text = readFileSync(`${DIR}/${file}`, "utf8");
    const section = text.split(/^#{2,3} (?:\d+\. )?Strings\s*$/m)[1]?.split(/^## /m)[0] ?? "";
    for (const m of section.matchAll(/^\| `([A-Za-z0-9_.]+)` \|/gm)) out.push(m[1]!);
  }
  return out;
}

describe("Epic 57's strings", () => {
  const fromSpec = specKeys();

  it("reads the stories' Strings tables", () => {
    expect(fromSpec).toContain("parental.time.left");
    expect(fromSpec).toContain("parental.time.up.title");
    expect(fromSpec).toContain("live.parental.slate");
    expect(fromSpec).toContain("live.admin.hdhrAllowed");
    expect(fromSpec).toContain("parental.tier.none");
    expect(fromSpec.length).toBeGreaterThanOrEqual(19);
  });

  it("every key the stories name is in both catalogs", () => {
    for (const key of fromSpec) {
      expect(catalogEn[key], `${key} has no English`).toBeTruthy();
      expect(catalogAr[key], `${key} has no Arabic`).toBeTruthy();
    }
  });

  it("every parental string has its Arabic, in Arabic, with the same placeholders", () => {
    const keys = Object.keys(catalogEn).filter(
      (k) =>
        k.startsWith("parental.") ||
        k.startsWith("live.parental.") ||
        k.startsWith("live.admin.hdhrAllowed") ||
        k === "nav.family" ||
        k === "nav.admin.tierReview"
    );
    expect(keys.length).toBeGreaterThan(100);
    for (const key of keys) {
      const a = catalogAr[key];
      expect(a, `${key} has no Arabic`).toBeTruthy();
      if (TIER.test(key)) {
        expect(a!.startsWith(LRM), `${key} needs the left-to-right mark`).toBe(true);
        continue;
      }
      expect(ARABIC.test(a!), `${key} is not Arabic: ${a}`).toBe(true);
      const holes = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort();
      // a plural form that names its number in words ("one minute") needs no {count}
      if (/\.(one|two)$/.test(key)) continue;
      expect(holes(a!), `${key}: placeholders differ`).toEqual(holes(catalogEn[key]!));
    }
  });

  it("no Arabic parental key is missing from English", () => {
    for (const key of Object.keys(catalogAr)) {
      if (key.startsWith("parental.") || key.startsWith("live.parental."))
        expect(catalogEn[key], `${key} has no English`).toBeTruthy();
    }
  });
});
