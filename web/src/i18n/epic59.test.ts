// Epic 59 (Maktaba on a NAS), story 59.14, T01: every string an Epic 59 story adds is in
// English and Arabic. The source is each story's Strings table
// (specs/epics/59-nas/story-*.md): a key there is in both web catalogs, the Arabic one in
// Arabic; a `container.*` key is printed by the container before the web is up, so it is in
// the server's own pair (cmd/maktaba-server/container_preflight.go, English then Arabic,
// checked by its Go test).
import { describe, expect, it } from "vitest";
import en from "./en.json";
import ar from "./ar.json";

// Files outside web/ are read at run time (the web image builds from web/ alone); vitest runs
// in web/.
const FS = "node:fs";
const { readFileSync, readdirSync } = (await import(/* @vite-ignore */ FS)) as {
  readFileSync: (path: string, encoding: "utf8") => string;
  readdirSync: (path: string) => string[];
};
const DIR = "../specs/epics/59-nas";
const STORIES: Record<string, string> = Object.fromEntries(
  readdirSync(DIR)
    .filter((n) => /^story-59-\d+-.*\.md$/.test(n))
    .map((n) => [n, readFileSync(`${DIR}/${n}`, "utf8")])
);
const preflight = readFileSync("../cmd/maktaba-server/container_preflight.go", "utf8");

// Stories whose strings land with the story itself and are not on main yet: their keys are
// checked once the story merges (remove it from here in that pull request).
const PENDING: Record<string, string> = {};

// Keys a story's code named differently from its spec's table, spec key → catalog key. Story
// 59.12 put its texts under the Updates page's own prefixes (updates.code.*, as every update
// failure code; updates.container.*, the page's Container block).
const ALIASES: Record<string, string> = {
  "update.disk_short": "updates.code.disk_short",
  "update.image_older": "updates.container.why.imageOlder",
  "update.runtime_vs_image": "updates.container.versions",
};

interface Row {
  story: string;
  key: string;
}

function rows(): Row[] {
  const out: Row[] = [];
  for (const [file, text] of Object.entries(STORIES)) {
    const story = `59.${file.match(/story-59-(\d+)/)![1]}`;
    const section = text.split(/^## Strings\s*$/m)[1]?.split(/^## /m)[0] ?? "";
    for (const m of section.matchAll(/^\| `([a-z0-9_.]+)` \|/gm)) out.push({ story, key: m[1]! });
  }
  return out;
}

const catalogEn = en as Record<string, string>;
const catalogAr = ar as Record<string, string>;
const ARABIC = /[؀-ۿ]/;

describe("Epic 59's strings", () => {
  const all = rows();

  it("reads the stories' Strings tables", () => {
    expect(all.length).toBeGreaterThan(50);
  });

  it.each(all.filter((r) => !PENDING[r.story]))("$story $key is in English and Arabic", (r) => {
    if (r.key.startsWith("container.")) {
      expect(preflight).toContain(`"${r.key}": {`);
      return;
    }
    const key = ALIASES[r.key] ?? r.key;
    expect(catalogEn[key], `${key} in en.json`).toBeTruthy();
    expect(catalogAr[key], `${key} in ar.json`).toBeTruthy();
    expect(catalogAr[key]).toMatch(ARABIC);
  });

  it("aliases only keys the stories name", () => {
    for (const k of Object.keys(ALIASES)) expect(all.some((r) => r.key === k)).toBe(true);
  });

  it("names only stories that still have strings to land", () => {
    for (const s of Object.keys(PENDING)) expect(all.some((r) => r.story === s)).toBe(true);
  });
});
