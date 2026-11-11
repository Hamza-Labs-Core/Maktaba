// The maktaba.tv link check's tests (story 60.08, T01-T06), on fixture repositories.
// Run: node --test web/packages/website/scripts/
import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync, execFileSync } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { findLinks, excluded, storyFor, resolvePage } from "./links.mjs";

const SCRIPT = path.join(path.dirname(fileURLToPath(import.meta.url)), "links.mjs");
const PKG = "web/packages/website";

// fixture: a git repository of `files` (path -> text), with the site's data files given.
function fixture(t, files, { manifest, baseline, allow, problems } = {}) {
  const root = mkdtempSync(path.join(tmpdir(), "links-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const all = { ...files };
  if (manifest) all[`${PKG}/dist-manifest.json`] = JSON.stringify(manifest);
  if (baseline) all[`${PKG}/links-baseline.json`] = JSON.stringify(baseline);
  if (allow) all[`${PKG}/links-allow.json`] = JSON.stringify(allow);
  if (problems) all["shared/api/problems.json"] = JSON.stringify(problems);
  for (const [f, text] of Object.entries(all)) {
    mkdirSync(path.dirname(path.join(root, f)), { recursive: true });
    writeFileSync(path.join(root, f), text);
  }
  execFileSync("git", ["init", "-q", root]);
  execFileSync("git", ["-C", root, "add", "-A"]);
  return root;
}

function run(root, ...args) {
  const r = spawnSync(process.execPath, [SCRIPT, "--root", root, ...args], { encoding: "utf8" });
  return { code: r.status, out: r.stdout + r.stderr };
}

const SITE = {
  files: [
    "/index.html",
    "/install/synology/index.html",
    "/install/docker/index.html",
    "/ar/install/docker/index.html",
  ],
  ids: { "/install/docker/index.html": ["compose", "ports"] },
  redirects: { "/docs": "/" },
};
const PROBLEMS = [
  { slug: "seat-limit", status: 403 },
  { slug: "not-found", status: 404 },
];

test("T01: a dead link in a .go file fails with its file and line", (t) => {
  const root = fixture(
    t,
    { "api/x.go": "package x\n\n// see https://maktaba.tv/nope\n" },
    { manifest: SITE }
  );
  const r = run(root);
  assert.equal(r.code, 1, r.out);
  assert.match(r.out, /api\/x\.go:3: https:\/\/maktaba\.tv\/nope: no such page/);
  assert.match(r.out, /::error file=api\/x\.go,line=3::/);
});

test("T02: the same link in a _test.go file is ignored", (t) => {
  const root = fixture(
    t,
    { "api/x_test.go": "package x\n\n// see https://maktaba.tv/nope\n" },
    { manifest: SITE }
  );
  const r = run(root);
  assert.equal(r.code, 0, r.out);
  assert.match(r.out, /0 links checked/);
});

test("T02: every test path, the specs and the trail are left out", (t) => {
  for (const f of [
    "a/b_test.go",
    "web/src/x.test.ts",
    "web/src/x.spec.ts",
    "apps/android/app/src/test/java/X.kt",
    "apps/android/FooTest.kt",
    "apps/apple/FooTests.swift",
    "web/src/__tests__/x.ts",
    "api/test-cases/x.json",
    "tools/ci/tests/test_x.py",
    "tools/lint/x-test.sh",
    "specs/epics/x.md",
    "docs/trail/events/0001-x.md",
    "docs/claude-trail.md",
    "docs/wiki/db/x.json",
    "web/wiki-app/public/db/x.json",
  ]) {
    assert.ok(excluded(f), f);
  }
  for (const f of [
    "api/x.go",
    "README.md",
    "docs/developers/contributing/nas.md",
    "web/packages/devportal/scripts/apps.mjs",
  ]) {
    assert.ok(!excluded(f), f);
  }
  const r = spawnSync(process.execPath, [SCRIPT, "--paths"], { encoding: "utf8" });
  assert.equal(r.status, 0);
  assert.ok(r.stdout.split("\n").includes("specs/**"));
  assert.ok(r.stdout.split("\n").includes("**/*_test.go"));
});

test("T03: a scheme-less link in a template is checked, and resolves", (t) => {
  const root = fixture(
    t,
    { "tools/release/notes.md.tmpl": "maktaba.tv/install/synology fills it in.\n" },
    { manifest: SITE }
  );
  const r = run(root);
  assert.equal(r.code, 0, r.out);
  assert.match(r.out, /1 links checked/);

  const dead = fixture(
    t,
    { "tools/release/notes.md.tmpl": "as maktaba.tv/install/truenas shows.\n" },
    { manifest: SITE }
  );
  const d = run(dead);
  assert.equal(d.code, 1, d.out);
  assert.match(d.out, /notes\.md\.tmpl:1: maktaba\.tv\/install\/truenas: no such page/);
});

test("T03: auto-trailing-slash: /x, /x/ and /x/index.html are one page; the home page; a redirect", (t) => {
  for (const p of ["/install/docker", "/install/docker/", "/install/docker/index.html"]) {
    assert.equal(resolvePage(SITE, p), "/install/docker/index.html", p);
  }
  assert.equal(resolvePage(SITE, "/"), "/index.html");
  const root = fixture(
    t,
    {
      "a.md":
        "https://maktaba.tv\nhttps://maktaba.tv/\nhttps://www.maktaba.tv/install/docker\nhttps://maktaba.tv/docs\n",
    },
    { manifest: SITE }
  );
  const r = run(root);
  assert.equal(r.code, 0, r.out);
  assert.match(r.out, /4 links checked/);
});

test("T04: a problem link is checked against the registry, not the site", (t) => {
  const root = fixture(
    t,
    {
      "apps/android/X.kt":
        'val a = "https://maktaba.tv/problems/seat-limit"\nval b = "https://maktaba.tv/problems/not-registered"\n',
    },
    { manifest: { files: [] }, problems: PROBLEMS }
  );
  const r = run(root);
  assert.equal(r.code, 1, r.out);
  assert.match(
    r.out,
    /X\.kt:2: https:\/\/maktaba\.tv\/problems\/not-registered: no problem type "not-registered" in shared\/api\/problems\.json/
  );
  assert.doesNotMatch(r.out, /X\.kt:1:/);
});

test("T04: with no registry, no slug is registered", (t) => {
  const root = fixture(
    t,
    { "a.kt": '"https://maktaba.tv/problems/seat-limit/"\n' },
    { manifest: SITE }
  );
  const r = run(root);
  assert.equal(r.code, 1, r.out);
  assert.match(r.out, /no problem type "seat-limit"/);
});

test("T04: the registry's shape is read loosely (an object holding the entries)", (t) => {
  const root = fixture(
    t,
    { "a.kt": '"https://maktaba.tv/ar/problems/seat-limit/"\n' },
    { problems: { problems: PROBLEMS } }
  );
  assert.equal(run(root).code, 0);
});

test("T05: a fragment must name an id on its page", (t) => {
  const root = fixture(
    t,
    {
      "a.md":
        "https://maktaba.tv/install/docker/#compose\nhttps://maktaba.tv/install/docker/#missing-id\n",
    },
    { manifest: SITE }
  );
  const r = run(root);
  assert.equal(r.code, 1, r.out);
  assert.match(
    r.out,
    /a\.md:2: .*#missing-id: no id "missing-id" on \/install\/docker\/index\.html/
  );
  assert.doesNotMatch(r.out, /a\.md:1:/);
});

test("T06: a typo suggests the close page", (t) => {
  const root = fixture(
    t,
    { "a.md": "see https://maktaba.tv/install/synolgy/\n" },
    { manifest: SITE, problems: PROBLEMS }
  );
  const r = run(root);
  assert.equal(r.code, 1, r.out);
  assert.match(r.out, /did you mean https:\/\/maktaba\.tv\/install\/synology\/\?/);

  const p = fixture(
    t,
    { "a.go": '"https://maktaba.tv/problems/seat-limt"\n' },
    { problems: PROBLEMS }
  );
  assert.match(run(p).out, /did you mean https:\/\/maktaba\.tv\/problems\/seat-limit\/\?/);

  const far = fixture(
    t,
    { "a.md": "https://maktaba.tv/completely-elsewhere\n" },
    { manifest: SITE }
  );
  assert.doesNotMatch(run(far).out, /did you mean/);
});

test("not a link: a subdomain, a mail address, the bare name; sentence punctuation is dropped", () => {
  const found = findLinks(
    "api.maktaba.tv/v1 privacy@maktaba.tv maktaba.tv is ours; https://status.maktaba.tv/x\n" +
      "see maktaba.tv/install/nas. Or (https://maktaba.tv/install/docker/), https://maktaba.tv/a?b=1#c\n"
  );
  assert.deepEqual(
    found.map((l) => [l.line, l.link, l.path, l.fragment]),
    [
      [2, "maktaba.tv/install/nas", "/install/nas", ""],
      [2, "https://maktaba.tv/install/docker/", "/install/docker/", ""],
      [2, "https://maktaba.tv/a?b=1#c", "/a", "c"],
    ]
  );
});

test("the allow list: an allowed link passes; one no longer in its file fails; one with no reason fails", (t) => {
  const allow = [
    {
      file: "web/src/lib/native.ts",
      link: "https://maktaba.tv/library/abc",
      reason: "a parser comment",
    },
  ];
  const ok = fixture(
    t,
    { "web/src/lib/native.ts": "// https://maktaba.tv/library/abc → /library/abc\n" },
    { allow }
  );
  assert.equal(run(ok).code, 0, run(ok).out);

  const stale = fixture(t, { "web/src/lib/native.ts": "// nothing here\n" }, { allow });
  const s = run(stale);
  assert.equal(s.code, 1);
  assert.match(s.out, /is no longer in the file: remove its entry/);

  const noReason = fixture(
    t,
    { "web/src/lib/native.ts": "// https://maktaba.tv/library/abc\n" },
    { allow: [{ ...allow[0], reason: " " }] }
  );
  const n = run(noReason);
  assert.equal(n.code, 1);
  assert.match(n.out, /an allowed link needs a reason/);
});

test("the baseline only shrinks: a listed dead link passes, a new one fails, a live or gone entry fails", (t) => {
  const baseline = [
    { file: "a.md", link: "https://maktaba.tv/install/nas/", story: "60.05" },
    { file: "b.md", link: "https://maktaba.tv/install/docker/", story: "60.05" },
    { file: "c.md", link: "https://maktaba.tv/problems/gone", story: "60.06" },
  ];
  const root = fixture(
    t,
    {
      "a.md": "https://maktaba.tv/install/nas/\nhttps://maktaba.tv/install/qnap/\n",
      "b.md": "https://maktaba.tv/install/docker/\n",
      "c.md": "nothing\n",
    },
    { manifest: SITE, baseline }
  );
  const r = run(root);
  assert.equal(r.code, 1, r.out);
  assert.match(r.out, /a\.md:2: https:\/\/maktaba\.tv\/install\/qnap\/: no such page/);
  assert.doesNotMatch(r.out, /a\.md:1:/);
  assert.match(
    r.out,
    /b\.md: https:\/\/maktaba\.tv\/install\/docker\/ now opens a page: remove it from links-baseline\.json/
  );
  assert.match(r.out, /c\.md: https:\/\/maktaba\.tv\/problems\/gone no longer in the file/);
  assert.match(r.out, /must be empty by the end of Epic 60's Phase 1/);

  // --prune-baseline drops the live and gone entries, and never adds the new dead link
  assert.equal(run(root, "--prune-baseline").code, 0);
  const left = JSON.parse(readFileSync(path.join(root, PKG, "links-baseline.json"), "utf8"));
  assert.deepEqual(left, [baseline[0]]);
});

test("the baseline's stories", () => {
  assert.equal(storyFor("/problems/seat-limit"), "60.06");
  assert.equal(storyFor("/ar/install/nas/"), "60.05");
  assert.equal(storyFor("/"), "60.03");
  assert.equal(storyFor("/docs"), "60.03");
  assert.equal(storyFor("/schema/log-line.json"), "60.03");
  assert.equal(storyFor("/legal/copyright/"), "60.07");
});

test("--write-manifest lists dist/'s files and each page's ids, and keeps the Worker's redirects", (t) => {
  const root = fixture(
    t,
    { "x.md": "nothing\n" },
    { manifest: { files: [], redirects: { "/docs": "/" } } }
  );
  const dist = path.join(root, "dist");
  mkdirSync(path.join(dist, "ar/install/nas"), { recursive: true });
  writeFileSync(path.join(dist, "index.html"), '<h1 id="top">x</h1><a name="old">');
  writeFileSync(
    path.join(dist, "ar/install/nas/index.html"),
    '<section id="ports"></section><p id = "ports">'
  );
  writeFileSync(path.join(dist, "robots.txt"), "User-agent: *\n");
  assert.equal(run(root, "--write-manifest", dist).code, 0);
  const m = JSON.parse(readFileSync(path.join(root, PKG, "dist-manifest.json"), "utf8"));
  assert.deepEqual(m, {
    files: ["/ar/install/nas/index.html", "/index.html", "/robots.txt"],
    ids: { "/ar/install/nas/index.html": ["ports"], "/index.html": ["old", "top"] },
    redirects: { "/docs": "/" },
  });
});

test("--dist checks against a built dist/ instead of the committed manifest", (t) => {
  const root = fixture(
    t,
    { "a.md": "https://maktaba.tv/install/nas/#ports\n" },
    { manifest: { files: [] } }
  );
  assert.equal(run(root).code, 1);
  const dist = path.join(root, "dist");
  mkdirSync(path.join(dist, "install/nas"), { recursive: true });
  writeFileSync(path.join(dist, "install/nas/index.html"), '<h2 id="ports">');
  const r = run(root, "--dist", dist);
  assert.equal(r.code, 0, r.out);
});
