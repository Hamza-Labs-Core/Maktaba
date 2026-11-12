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
  measure,
  pick,
  RUNTIME_RETRIES,
  runtimeErrorOf,
  sample,
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
    // the problem index (60.06), a family's own page
    "/problems/",
    "/ar/problems/",
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

test("a large changed family is sampled evenly, both languages; a small one runs whole", () => {
  const big = Array.from({ length: 288 }, (_, i) => `/problems/p${String(i).padStart(3, "0")}/`);
  const urls = [...big, ...big.map((u) => `/ar${u}`)];
  const s = sample(urls, 16);
  assert.equal(s.length, 16);
  assert.ok(s.includes("/problems/p000/") && s.includes("/problems/p287/"), "first and last");
  for (const u of s.filter((u) => !u.startsWith("/ar/"))) assert.ok(s.includes(`/ar${u}`), u);
  assert.deepEqual(sample(["/b/", "/a/"], 16), ["/a/", "/b/"]);
  const fam = urls.map((url) => ({ url, family: "problems" }));
  const r = pick({
    set: "pr",
    pages: fam,
    reps: { problems: [] },
    hand,
    changed: ["shared/api/problems.json"],
  });
  assert.equal(r.single.length, 16);
  // the nightly still sweeps every page
  assert.equal(pick({ set: "all", pages: fam, reps: {}, hand }).single.length, hand.length + 576);
});

// ---- a run Lighthouse could not finish is measured again; a low score is not ---------------

const lhr = (url, extra = {}) =>
  JSON.stringify({
    requestedUrl: `http://127.0.0.1:8787${url}`,
    categories: { performance: { score: 1 } },
    audits: {},
    ...extra,
  });

/** A fake lhci collect: each call answers the next scripted result and writes its lhrs. */
function fakeCollect(work, script) {
  const calls = [];
  let n = 0;
  const run = async (args) => {
    calls.push(args);
    const step = script[Math.min(n++, script.length - 1)];
    const dir = path.join(work, ".lighthouseci");
    if (!args.includes("--additive")) fs.rmSync(dir, { recursive: true, force: true });
    fs.mkdirSync(dir, { recursive: true });
    for (const [url, extra] of step.write ?? [])
      fs.writeFileSync(path.join(dir, `lhr-${n}-${Math.random()}.json`), lhr(url, extra));
    return { status: step.status, out: step.out ?? "" };
  };
  return { run, calls };
}

const NAVSTART =
  "Runtime error encountered: Something went wrong with recording the trace over your page load. Please run Lighthouse again. (NO_NAVSTART)";

function tmpWork(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "lh-retry-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return { work: path.join(dir, "work"), log: path.join(dir, "lhci.log") };
}

test("runtimeErrorOf: the code of a run Lighthouse could not finish, else null", () => {
  assert.equal(runtimeErrorOf(NAVSTART), "NO_NAVSTART");
  assert.equal(runtimeErrorOf('{"runtimeError": {\n "code": "NO_FCP", "message": "x"}}'), "NO_FCP");
  assert.equal(runtimeErrorOf("Error: connect ECONNREFUSED 127.0.0.1:8787"), null);
  assert.equal(runtimeErrorOf("Assertion failed: categories:performance 0.81 < 0.95"), null);
});

test("a run Lighthouse could not finish is measured again, only the unmeasured URLs, logged", async (t) => {
  const { work, log } = tmpWork(t);
  const urls = ["/a/", "/b/", "/c/"];
  const { run, calls } = fakeCollect(work, [
    // /a/ measured 3 times; /b/ stopped after 1 run on NO_NAVSTART; /c/ never reached
    { status: 1, out: NAVSTART, write: [["/a/"], ["/a/"], ["/a/"], ["/b/"]] },
    { status: 0, write: [["/b/"], ["/b/"], ["/b/"], ["/c/"], ["/c/"], ["/c/"]] },
  ]);
  const said = [];
  await measure({
    base: "http://127.0.0.1:8787",
    urls,
    runs: 3,
    work,
    rcFile: "rc",
    chrome: "c",
    log,
    run,
    say: (l) => said.push(l),
  });
  assert.equal(calls.length, 2);
  const retried = calls[1].filter((a) => a.startsWith("--url=")).map((a) => a.slice(27));
  assert.deepEqual(retried, ["/b/", "/c/"]);
  assert.ok(calls[1].includes("--additive"), "the retry keeps /a/'s runs");
  assert.match(said[0], /NO_NAVSTART.*2 unmeasured URL\(s\).*retry 1 of 2/);
  assert.match(fs.readFileSync(log, "utf8"), /retry 1 of 2/);
  // /b/'s partial run was dropped: every URL ends with exactly 3 runs
  const lhrs = fs.readdirSync(path.join(work, ".lighthouseci"));
  assert.equal(lhrs.length, 9);
});

test("a kept lhr with a runtimeError is measured again too", async (t) => {
  const { work, log } = tmpWork(t);
  const { run, calls } = fakeCollect(work, [
    { status: 0, write: [["/a/"], ["/a/", { runtimeError: { code: "NO_FCP" } }], ["/a/"]] },
    { status: 0, write: [["/a/"], ["/a/"], ["/a/"]] },
  ]);
  await measure({
    base: "http://127.0.0.1:8787",
    urls: ["/a/"],
    runs: 3,
    work,
    rcFile: "rc",
    chrome: "c",
    log,
    run,
    say: () => {},
  });
  assert.equal(calls.length, 2);
  assert.equal(fs.readdirSync(path.join(work, ".lighthouseci")).length, 3);
});

test("Lighthouse failing three times in a row fails the run, after two retries", async (t) => {
  const { work, log } = tmpWork(t);
  const { run, calls } = fakeCollect(work, [{ status: 1, out: NAVSTART, write: [] }]);
  await assert.rejects(
    measure({
      base: "http://x",
      urls: ["/a/"],
      runs: 3,
      work,
      rcFile: "rc",
      chrome: "c",
      log,
      run,
      say: () => {},
    }),
    /NO_NAVSTART after 3 attempts/
  );
  assert.equal(calls.length, 1 + RUNTIME_RETRIES);
});

test("a collect that failed for another reason is not retried", async (t) => {
  const { work, log } = tmpWork(t);
  const { run, calls } = fakeCollect(work, [
    { status: 1, out: "Error: connect ECONNREFUSED", write: [] },
  ]);
  await assert.rejects(
    measure({
      base: "http://x",
      urls: ["/a/"],
      runs: 3,
      work,
      rcFile: "rc",
      chrome: "c",
      log,
      run,
      say: () => {},
    }),
    /lhci collect failed \(1\)/
  );
  assert.equal(calls.length, 1);
});

test("a low score is a measurement: collected once, never retried", async (t) => {
  const { work, log } = tmpWork(t);
  const low = { categories: { performance: { score: 0.41 } } };
  const { run, calls } = fakeCollect(work, [
    {
      status: 0,
      write: [
        ["/a/", low],
        ["/a/", low],
        ["/a/", low],
      ],
    },
  ]);
  await measure({
    base: "http://x",
    urls: ["/a/"],
    runs: 3,
    work,
    rcFile: "rc",
    chrome: "c",
    log,
    run,
    say: () => {},
  });
  assert.equal(calls.length, 1, "no second collect for a score under the bar");
  assert.equal(fs.readdirSync(path.join(work, ".lighthouseci")).length, 3);
});
