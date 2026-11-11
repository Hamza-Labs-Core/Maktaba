// Epic 59 (Maktaba on a NAS), story 59.14, T01: every string an Epic 59 story adds is in
// English and Arabic. The source is each story's Strings table
// (specs/epics/59-nas/story-*.md): a key there is in both web catalogs, the Arabic one in
// Arabic; a `container.*` key is printed by the container before the web is up, so it is in
// the server's own pair (cmd/maktaba-server/container_preflight.go, English then Arabic,
// checked by its Go test).
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import en from "./en.json";
import ar from "./ar.json";

const repo = path.resolve(__dirname, "../../..");
const dir = path.join(repo, "specs/epics/59-nas");

// Stories whose strings land with the story itself and are not on main yet: their keys are
// checked once the story merges (remove it from here in that pull request).
const PENDING: Record<string, string> = {
  "59.12": "updates on a NAS: in flight",
  "59.13": "a NAS in a cluster: in flight",
  "59.16": "OpenAPI and role tests: its one string lands with it",
};

interface Row {
  story: string;
  key: string;
}

function rows(): Row[] {
  const out: Row[] = [];
  for (const f of fs.readdirSync(dir).filter((n) => /^story-59-\d+-.*\.md$/.test(n))) {
    const story = `59.${f.match(/^story-59-(\d+)/)![1]}`;
    const text = fs.readFileSync(path.join(dir, f), "utf8");
    const section = text.split(/^## Strings\s*$/m)[1]?.split(/^## /m)[0] ?? "";
    for (const m of section.matchAll(/^\| `([a-z0-9_.]+)` \|/gm)) out.push({ story, key: m[1]! });
  }
  return out;
}

const catalogEn = en as Record<string, string>;
const catalogAr = ar as Record<string, string>;
const ARABIC = /[؀-ۿ]/;
const preflight = fs.readFileSync(
  path.join(repo, "cmd/maktaba-server/container_preflight.go"),
  "utf8"
);

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
    expect(catalogEn[r.key], `${r.key} in en.json`).toBeTruthy();
    expect(catalogAr[r.key], `${r.key} in ar.json`).toBeTruthy();
    expect(catalogAr[r.key]).toMatch(ARABIC);
  });

  it("names only stories that still have strings to land", () => {
    for (const s of Object.keys(PENDING)) expect(all.some((r) => r.story === s)).toBe(true);
  });
});
