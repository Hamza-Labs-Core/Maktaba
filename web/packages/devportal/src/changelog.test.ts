// @vitest-environment node
// The changelog page (scripts/changelog.mjs): the file and the fragments in changelog.d/, each
// in the release whose tag first has it, in English and Arabic.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import {
  changelogPage,
  checkFragments,
  fragmentReleases,
  readFragments,
} from "../scripts/changelog.mjs";
import { changelogOutline, checkChangelog } from "../scripts/parity.mjs";
import { REPO, checkSources } from "../scripts/build.mjs";

const DIST = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../dist");
const EN =
  "# Changelog\n\n## [Unreleased]\n\n### Fixed\n\n- frozen\n\n## [0.2.0] - 2026-10-19\n\n### Added\n\n- old\n";
const AR =
  "# سجل التغييرات\n\n## [لم يصدر بعد]\n\n### أُصلح\n\n- مجمّد\n\n## [0.2.0] - 2026-10-19\n\n### أُضيف\n\n- قديم\n";
const frag = (name: string, en: string | null, ar: string | null) => ({ name, en, ar });
const A = frag("2026-11-25-a.md", "### Fixed\n\n- a\n", "### أُصلح\n\n- أ\n");
const B = frag(
  "2026-11-26-b.md",
  "### Fixed\n\n- b\n  more\n\n  its second paragraph\n\n### Added\n\n- b2\n",
  "### أُصلح\n\n- ب\n  تتمة\n\n  فقرته الثانية\n\n### أُضيف\n\n- ب2\n"
);
const none = new Map();
const rel = (o: Record<string, [string, string]>) =>
  new Map(Object.entries(o).map(([k, [version, date]]) => [k, { version, date }]));

describe("the changelog page", () => {
  it("is the file, unchanged, when there is no fragment", () => {
    for (const [f, lang] of [
      ["CHANGELOG.md", "en"],
      ["CHANGELOG.ar.md", "ar"],
    ]) {
      const md = fs.readFileSync(path.join(REPO, f), "utf8");
      expect(changelogPage(md, lang, [], none)).toBe(md);
    }
  });

  it("lists unreleased fragments above the frozen entries, newest first, by section", () => {
    const en = changelogPage(EN, "en", [B, A], none);
    expect(en).toBe(
      "# Changelog\n\n## [Unreleased]\n\n### Added\n\n- b2\n\n### Fixed\n\n" +
        "- b\n  more\n\n  its second paragraph\n\n- a\n\n### Fixed\n\n- frozen\n\n" +
        "## [0.2.0] - 2026-10-19\n\n### Added\n\n- old\n"
    );
    expect(checkChangelog(en, changelogPage(AR, "ar", [B, A], none))).toEqual([]);
  });

  it("puts a released fragment in its release, and the frozen entries after them", () => {
    const releases = rel({
      "2026-11-25-a.md": ["0.3.9", "2026-11-25"],
      "2026-11-26-b.md": ["0.3.10", "2026-11-26"],
    });
    const en = changelogPage(EN, "en", [B, A], releases);
    expect(changelogOutline(en, "en").map((r) => r.release)).toEqual([
      "Unreleased",
      "[0.3.10] - 2026-11-26",
      "[0.3.9] - 2026-11-25",
      "Earlier entries, not listed by release",
      "[0.2.0] - 2026-10-19",
    ]);
    expect(changelogOutline(en, "en").map((r) => r.sections.length)).toEqual([0, 2, 1, 1, 1]);
    const ar = changelogPage(AR, "ar", [B, A], releases);
    expect(ar).toContain("## [0.3.10] - 2026-11-26\n\n### أُضيف\n\n- ب2\n");
    expect(ar).toContain("## مُدخلات أسبق، غير موزّعة على الإصدارات\n\n### أُصلح\n\n- مجمّد\n");
  });

  it("loses no entry, whatever is released", () => {
    for (const releases of [none, rel({ "2026-11-25-a.md": ["0.3.9", "2026-11-25"] })])
      for (const s of [
        "- a\n",
        "- b\n  more\n\n  its second paragraph\n",
        "- b2\n",
        "- frozen\n",
        "- old\n",
      ])
        expect(changelogPage(EN, "en", [B, A], releases)).toContain(s);
  });

  it("a file without its [Unreleased] section fails", () => {
    expect(() => changelogPage("# Changelog\n", "en", [], none)).toThrow(/no "## \[Unreleased\]"/);
  });

  it("the built page has every fragment's entries, in both languages", () => {
    const plain = (s: string) => s.replace(/<[^>]+>|&[a-z#0-9]+;|[`*"'<>&_\s]/g, "");
    const pages = { en: "changelog/index.html", ar: "ar/changelog/index.html" };
    for (const lang of ["en", "ar"] as const) {
      const html = plain(fs.readFileSync(path.join(DIST, pages[lang]), "utf8"));
      for (const f of readFragments(REPO)) {
        const first = f[lang]!.match(/^- (?:\*\*)?([^*\n]{5,40})/m)![1];
        expect(html, `${f.name} (${lang})`).toContain(plain(first));
      }
    }
  });
});

describe("the fragments' parity", () => {
  it("the repository's own pass, and are part of the build's checks", () => {
    expect(checkFragments(readFragments(REPO))).toEqual([]);
    expect(checkSources()).toEqual([]);
  });

  it.each([
    [
      "no Arabic",
      frag(A.name, A.en, null),
      /no Arabic fragment \(changelog\.d\/2026-11-25-a\.ar\.md\)/,
    ],
    ["no English", frag(A.name, null, A.ar), /no English fragment/],
    ["a bad name", frag("fix.md", A.en, A.ar), /not a fragment/],
    ["one entry fewer in Arabic", frag(A.name, "### Fixed\n\n- a\n- b\n", A.ar), /both languages/],
    ["another section in Arabic", frag(A.name, A.en, "### أُضيف\n\n- أ\n"), /both languages/],
    ["an unknown section", frag(A.name, "### Fixes\n\n- a\n", A.ar), /not only sections/],
    ["English sections in Arabic", frag(A.name, A.en, "### Fixed\n\n- أ\n"), /not only sections/],
    [
      "a section with no entry",
      frag(A.name, "### Fixed\n\n", "### أُصلح\n\n"),
      /not only sections/,
    ],
    ["text outside a section", frag(A.name, "hello\n" + A.en, A.ar), /not only sections/],
    ["an empty file", frag(A.name, "", ""), /not only sections/],
    ["an untranslated entry", frag(A.name, A.en, "### أُصلح\n\n- left in English\n"), /Latin/],
  ])("%s fails", (_what, f, want) => {
    expect(checkFragments([f]).join("\n")).toMatch(want);
  });

  it("a good pair passes", () => {
    expect(checkFragments([A, B])).toEqual([]);
  });
});

describe("the release of a fragment", () => {
  let dir = "";
  const git = (...args: string[]) =>
    execFileSync("git", ["-C", dir, ...args], {
      encoding: "utf8",
      env: { ...process.env, GIT_CONFIG_GLOBAL: "/dev/null", GIT_CONFIG_SYSTEM: "/dev/null" },
    });
  const add = (f: string) => {
    fs.mkdirSync(path.dirname(path.join(dir, f)), { recursive: true });
    fs.writeFileSync(path.join(dir, f), f);
    git("add", "-A");
    git("commit", "-qm", f);
  };
  beforeAll(() => {
    dir = fs.mkdtempSync(path.join(os.tmpdir(), "maktaba-changelog-"));
    git("init", "-q", "-b", "main");
    git("config", "user.email", "test@example.invalid");
    git("config", "user.name", "test");
    git("config", "commit.gpgsign", "false");
    git("config", "tag.gpgsign", "false");
    add("README.md");
    git("tag", "v0.3.8");
    add("changelog.d/README.md");
    add("changelog.d/2026-11-25-a.md");
    add("changelog.d/2026-11-25-a.ar.md");
    git("tag", "-a", "-m", "v0.3.9", "v0.3.9");
    add("other.md");
    git("tag", "v0.3.10");
    git("tag", "v0.4.0-rc1");
    add("changelog.d/2026-11-26-b.md");
    git("tag", "nightly");
    git("tag", "v0.3.11");
    add("changelog.d/2026-11-27-c.md");
  });
  afterAll(() => fs.rmSync(dir, { recursive: true, force: true }));

  it("is the lowest version tag whose tree has it; none yet is unreleased", () => {
    const got = fragmentReleases(dir);
    expect([...got].map(([name, r]) => [name, r.version])).toEqual([
      ["2026-11-25-a.md", "0.3.9"],
      ["2026-11-26-b.md", "0.3.11"],
    ]);
    for (const r of got.values()) expect(r.date).toMatch(/^\d{4}-\d\d-\d\d$/);
  });

  it("reads the same from a shallow, blobless fetch of the tags (as CI does)", () => {
    const shallow = fs.mkdtempSync(path.join(os.tmpdir(), "maktaba-shallow-"));
    const run = (...a: string[]) => execFileSync("git", ["-C", shallow, ...a], { stdio: "pipe" });
    git("config", "uploadpack.allowFilter", "true");
    run("init", "-q");
    run("remote", "add", "origin", `file://${dir}`);
    run("fetch", "-q", "--depth=1", "--filter=blob:none", "origin", "+refs/tags/v*:refs/tags/v*");
    expect([...fragmentReleases(shallow)]).toEqual([...fragmentReleases(dir)]);
    fs.rmSync(shallow, { recursive: true, force: true });
  });

  it("with no tags, or no git, nothing is released", () => {
    const bare = fs.mkdtempSync(path.join(os.tmpdir(), "maktaba-nogit-"));
    expect(fragmentReleases(bare).size).toBe(0);
    execFileSync("git", ["-C", bare, "init", "-q"]);
    expect(fragmentReleases(bare).size).toBe(0);
    fs.rmSync(bare, { recursive: true, force: true });
  });
});
