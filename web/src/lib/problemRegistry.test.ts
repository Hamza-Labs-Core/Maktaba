// Epic 60 story 60.06 (T07): every problem type and code the web app compares against is in
// the registry (shared/api/problems.json), so each has its page on maktaba.tv and the API
// sends it.
//
// Types: every `maktaba.tv/problems/<slug>` in the app's sources, every literal compared with
// a `slug`, and every member of a `…_SLUGS` set. Codes: every literal compared with `code` or
// `reason` in the problem classifiers (CLASSIFIERS) and every `problem.code === "…"` anywhere.
import { describe, it, expect } from "vitest";

// read from disk (vitest runs in web/), as stageWords.test.ts does
const FS = "node:fs";
const { readFileSync, readdirSync, statSync } = (await import(/* @vite-ignore */ FS)) as {
  readFileSync: (path: string, encoding: "utf8") => string;
  readdirSync: (path: string) => string[];
  statSync: (path: string) => { isDirectory(): boolean };
};

interface Entry {
  slug: string;
  codes: string[];
}
const registry = JSON.parse(readFileSync("../shared/api/problems.json", "utf8")) as {
  problems: Entry[];
};
const slugs = new Set(registry.problems.map((e) => e.slug));
const codes = new Set(registry.problems.flatMap((e) => e.codes));

/** The files that sort the home's problems into what a person reads. */
const CLASSIFIERS = new Set([
  "src/lib/errorKind.ts",
  "src/lib/errorText.ts",
  "src/lib/api.ts",
  "src/lib/clusterApi.ts",
]);

/** Codes compared there that are not the API's problem codes, each with why. */
const NOT_PROBLEM_CODES: Record<string, string> = {
  relay_needs_plan:
    "Maktaba Cloud's relay names it in its own `error` field (story 50.19), not an API problem",
  stream_limit:
    "Maktaba Cloud's relay names it in its own `error` field (story 50.19), not an API problem",
  home_reconnecting:
    "Maktaba Cloud's relay names it in its own `error` field (story 50.13), not an API problem",
  media_err_2: "a playback fault's code (the browser's MediaError), classified in errorKind.ts",
};

function sources(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const path = `${dir}/${name}`;
    if (statSync(path).isDirectory()) {
      if (name !== "test" && name !== "node_modules") sources(path, out);
    } else if (/\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name)) {
      out.push(path);
    }
  }
  return out;
}

const URI = /maktaba\.tv\/problems\/([a-z0-9_-]+)/g;
const SLUG_COMPARE = /\b[sS]lug\s*===?\s*"([a-z0-9_-]+)"/g;
const SLUG_SET = /[A-Z_]*SLUGS\s*=\s*new Set\(\[([^\]]*)\]/g;
const CODE_COMPARE = /(?<!typeof\s[\w.?]*)\b(?:code|reason)\s*===?\s*"([a-z0-9_]+)"/g;
const PROBLEM_CODE = /problem\??\.code\s*===?\s*"([a-z0-9_]+)"/g;
const QUOTED = /"([a-z0-9_-]+)"/g;

describe("the problem registry", () => {
  it("lists every problem type and code the web app compares", () => {
    const files = sources("src");
    expect(files.length, "the scan found no sources").toBeGreaterThan(200);
    const types = new Map<string, string>();
    const found = new Map<string, string>();
    const add = (m: Map<string, string>, k: string, at: string) => {
      if (!m.has(k)) m.set(k, at);
    };
    for (const at of files) {
      const text = readFileSync(at, "utf8");
      for (const re of [URI, SLUG_COMPARE]) for (const m of text.matchAll(re)) add(types, m[1], at);
      for (const m of text.matchAll(SLUG_SET))
        for (const q of m[1].matchAll(QUOTED)) add(types, q[1], at);
      for (const m of text.matchAll(PROBLEM_CODE)) add(found, m[1], at);
      if (CLASSIFIERS.has(at)) for (const m of text.matchAll(CODE_COMPARE)) add(found, m[1], at);
    }
    expect(types.size, "found no problem type: the scan is broken").toBeGreaterThan(0);
    expect(found.size, "found no problem code: the scan is broken").toBeGreaterThan(0);

    const bad = [
      ...[...types].filter(([s]) => !slugs.has(s)).map(([s, at]) => `type ${s} (${at})`),
      ...[...found]
        .filter(([c]) => !codes.has(c) && !(c in NOT_PROBLEM_CODES))
        .map(([c, at]) => `code ${c} (${at})`),
    ];
    expect(
      bad,
      "not in shared/api/problems.json (add the entry there, or, for a code that is no API problem's, to NOT_PROBLEM_CODES with why)"
    ).toEqual([]);
    const stale = Object.keys(NOT_PROBLEM_CODES).filter((c) => !found.has(c) || codes.has(c));
    expect(stale, "NOT_PROBLEM_CODES lists codes no longer compared, or registered").toEqual([]);
  });
});
