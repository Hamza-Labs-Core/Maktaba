// Story 60.04: which URLs each Lighthouse run measures (scripts/lighthouse.mjs).
// Run: node --test web/packages/website/scripts/lighthouse.test.mjs
import assert from "node:assert/strict";
import test from "node:test";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import {
  config,
  familiesChanged,
  handMadePattern,
  handMadeUrls,
  pick,
  verdict,
} from "./lighthouse.mjs";

const pages = [
  { url: "/install/nas/", family: "install" },
  { url: "/install/qnap/", family: "install" },
  { url: "/ar/install/nas/", family: "install" },
  { url: "/ar/install/qnap/", family: "install" },
  { url: "/problems/x/", family: "problems" },
  { url: "/ar/problems/x/", family: "problems" },
];
const reps = { install: ["/install/nas/"], problems: [] };
const hand = ["/", "/ar/", "/404", "/ar/404"];

test("the hand-made pages: their paths, the 404 at /404 (status 200, which Lighthouse measures)", () => {
  // the front door, the 404, the install chooser (60.05) and the legal pages (60.07)
  const all = [
    ...hand,
    "/install/",
    "/ar/install/",
    ...["privacy", "terms", "copyright"].flatMap((n) => [`/legal/${n}/`, `/ar/legal/${n}/`]),
  ];
  assert.deepEqual(handMadeUrls(), all);
  const re = new RegExp(handMadePattern());
  for (const u of all) assert.ok(re.test(`http://127.0.0.1:8787${u}`), u);
  assert.ok(!re.test("http://127.0.0.1:8787/install/nas/"));
  // the config the runs use carries the pattern, never the placeholder
  assert.ok(!JSON.stringify(config()).includes("HAND_MADE"));
});

test("a pull request: the hand-made pages and the representatives in both languages, 3 runs", () => {
  const r = pick({ set: "pr", pages, reps, hand });
  assert.deepEqual(r.repeated, [
    "/",
    "/404",
    "/ar/",
    "/ar/404",
    "/ar/install/nas/",
    "/install/nas/",
  ]);
  assert.deepEqual(r.single, []);
});

test("a change to a family's generator or source runs the whole family, one run per page", () => {
  assert.deepEqual(familiesChanged(["web/packages/install/site/install/qnap/index.html"]), [
    "install",
  ]);
  assert.deepEqual(familiesChanged(["web/vite.install.config.ts"]), ["install"]);
  assert.deepEqual(familiesChanged(["shared/api/problems.json"]), ["problems"]);
  assert.deepEqual(familiesChanged(["web/src/help/content/en/a.md"]), ["help"]);
  // the shell renders the problem and help pages too
  assert.deepEqual(familiesChanged(["web/packages/website/scripts/shell.mjs"]), [
    "help",
    "problems",
  ]);
  assert.deepEqual(familiesChanged(["README.md", "web/packages/installer.ts"]), []);
  const r = pick({ set: "pr", pages, reps, hand, changed: ["web/packages/install/src/page.ts"] });
  assert.deepEqual(r.single, ["/ar/install/qnap/", "/install/qnap/"]);
  assert.ok(!r.single.some((u) => r.repeated.includes(u)), "no URL twice");
});

test("the deploy adds the pages the last nightly failed, measured 3 times", () => {
  const r = pick({ set: "pr", pages, reps, hand, extra: ["/install/qnap/"] });
  assert.ok(r.repeated.includes("/install/qnap/"));
});

test("the nightly sweep: every page once, split into shards that cover it exactly", () => {
  const all = pick({ set: "all", pages, reps, hand }).single;
  assert.equal(all.length, hand.length + pages.length);
  const shards = [1, 2, 3].map(
    (i) => pick({ set: "all", pages, reps, hand, shard: [i, 3] }).single
  );
  assert.deepEqual(shards.flat().sort(), [...all].sort());
  for (const s of shards) assert.ok(s.length >= 3 && s.length <= 4);
});

test("the nightly's verdict: each shard's failures, and every page of a shard with no result", (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "verdict-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const put = (i, v) => {
    fs.mkdirSync(path.join(dir, `shard-${i}`), { recursive: true });
    fs.writeFileSync(path.join(dir, `shard-${i}`, "failures.json"), v);
  };
  put(1, "[]");
  put(2, '["/install/qnap/"]');
  put(3, "{not json");
  const urls = { 3: ["/ar/", "/x/"], 4: ["/y/"] };
  const v = verdict({ shards: 4, dir, shardUrls: (i) => urls[i] ?? [] });
  assert.deepEqual(v.failed, ["/ar/", "/install/qnap/", "/x/", "/y/"]);
  assert.equal(v.notes.length, 2);
  put(3, "[]");
  put(4, "[]");
  put(2, "[]");
  assert.deepEqual(verdict({ shards: 4, dir, shardUrls: () => ["/z/"] }).failed, []);
});
