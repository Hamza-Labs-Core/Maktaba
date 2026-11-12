#!/usr/bin/env node
// maktaba.tv's Lighthouse runs (story 60.04), with @lhci/cli and lighthouserc.json's bar, against
// a built dist/ served by the Worker (wrangler dev), so its headers count.
//
//   node scripts/lighthouse.mjs urls --set pr|all [--changed FILE] [--shard I/N] [--extra JSON]
//       prints the URLs a run measures, as {"repeated": [...], "single": [...]}
//   node scripts/lighthouse.mjs run --base URL --out DIR --set pr|all [--changed FILE]
//       [--shard I/N] [--extra JSON] [--remeasure 3]
//       measures them, judges them, writes DIR/reports (HTML and JSON), DIR/scores.json,
//       DIR/failures.json and DIR/summary.md (also to $GITHUB_STEP_SUMMARY); exits 1 on a failure
//   node scripts/lighthouse.mjs verdict --shards N --in DIR --out FILE
//       the nightly's verdict over its shards' results (DIR/shard-<i>/failures.json): the pages
//       under the bar, plus every page of a shard that left no result (it crashed or timed out),
//       written to FILE and $GITHUB_STEP_SUMMARY; exits 1 when there is any
//   node scripts/lighthouse.mjs control --out DIR
//       T02, the positive control: a fixture page whose image arrives unsized must fail the
//       layout-shift assertion; exits 1 when it does not
//
// The sets:
//   - pr (pull requests and the deploy run): every hand-made page (with the families' own
//     indexes, /problems/) and each family's representatives (e2e/representatives.json), both
//     languages, three runs each, the median run judged; and, when --changed lists a file a
//     family is generated from (its generator, its source, or the shell it is rendered in), the
//     other pages of that family, one run each: all of them up to FAMILY_SAMPLE, and beyond that
//     FAMILY_SAMPLE spread evenly over it (the problem pages are one template over 288 entries,
//     which the nightly sweeps whole; README §11 decision 11); --extra adds URLs measured three
//     times (the deploy: the pages the last nightly failed).
//   - all (the nightly sweep): every page in both languages, one run each, split into --shard I/N
//     (every Nth page). With --remeasure 3, a URL that fails is measured three more times and
//     judged on their median: a page under the bar on that median fails, with no other retry.
//
// A run that Lighthouse itself could not finish (a runtimeError: NO_NAVSTART, NO_FCP and the
// like, "Please run Lighthouse again") makes lhci collect stop. That is no measurement, so the
// pages the collect left unmeasured (the one it stopped on, and those after it) are measured
// again, up to RUNTIME_RETRIES more times, each retry logged. A score under the bar is a
// measurement: it is never retried here and fails on its first judgement.

import { spawn } from "node:child_process";
import fs from "node:fs";
import http from "node:http";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { DIST, PAGES, sitePages } from "./build.mjs";
import { inLang } from "./head.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const PKG = path.resolve(HERE, "..");
const LHCI = path.join(PKG, "node_modules/@lhci/cli/src/cli.js");

/** The files each family is generated from (repository paths; a prefix ending in / is a
 * folder). A change to one runs the whole family. */
export const FAMILY_SOURCES = {
  install: ["web/packages/install/", "web/vite.install.config.ts"],
  problems: ["web/packages/website/scripts/problems.mjs", "shared/api/problems.json"],
  help: ["web/packages/website/scripts/help.mjs", "web/src/help/content/"],
};
/** The shell the hand-made pages and the problem and help pages are rendered in: a change to it
 * runs those families whole (the hand-made pages run on every pull request anyway). */
export const SHELL_SOURCES = [
  "web/packages/website/scripts/shell.mjs",
  "web/packages/website/scripts/head.mjs",
  "web/packages/website/static/assets/",
];
const SHELL_FAMILIES = ["problems", "help"];

const touches = (changed, sources) =>
  changed.some((f) => sources.some((s) => (s.endsWith("/") ? f.startsWith(s) : f === s)));

/** The families a list of changed files renders anew. */
export function familiesChanged(changed) {
  const out = new Set();
  for (const [family, sources] of Object.entries(FAMILY_SOURCES))
    if (touches(changed, sources)) out.add(family);
  if (touches(changed, SHELL_SOURCES)) for (const f of SHELL_FAMILIES) out.add(f);
  return [...out].sort();
}

/** The generated families' own indexes: one page each, measured as the hand-made pages are. */
export const FAMILY_INDEXES = ["/problems/"];

/** The most pages of a changed family a pull request measures once each (beyond its
 * representatives): a family larger than this is sampled evenly, both languages. */
export const FAMILY_SAMPLE = 16;

/** The hand-made pages' URLs: their own path, or /404 for the 404 page (the Worker answers a
 * missing path with it and status 404, which Lighthouse refuses to measure; /404 is the same
 * page with 200); then the families' indexes, in both languages. */
export function handMadeUrls(pages = PAGES, indexes = FAMILY_INDEXES) {
  return [
    ...pages.map((p) =>
      p.path === null ? `/${p.out.replace(/\.html$/, "")}` : inLang(p.lang, p.path)
    ),
    ...indexes.flatMap((u) => [u, inLang("ar", u)]),
  ];
}

/** `max` URLs spread evenly over a family's pages (each English page with its Arabic twin, when
 * it has one), or all of them when there are no more than `max`. */
export function sample(urls, max = FAMILY_SAMPLE) {
  const sorted = [...new Set(urls)].sort();
  if (sorted.length <= max) return sorted;
  const has = new Set(sorted);
  const en = sorted.filter((u) => !u.startsWith("/ar/"));
  const k = Math.max(1, Math.floor(max / 2));
  const picked = new Set();
  for (let i = 0; i < k; i++) {
    const u = en[Math.round((i * (en.length - 1)) / Math.max(1, k - 1))];
    picked.add(u);
    if (has.has(inLang("ar", u))) picked.add(inLang("ar", u));
  }
  return [...picked].sort();
}

/** lighthouserc's pattern for the hand-made pages (the byte-weight bar). */
export function handMadePattern(urls = handMadeUrls()) {
  const alts = urls.map((u) => u.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join("|");
  return `^https?://[^/]+(${alts})$`;
}

/** Every generated page (not hand-made), by family: [{url, family}]. */
export function generatedPages(dist = DIST) {
  const files = [];
  const walk = (dir) => {
    for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
      const p = path.join(dir, e.name);
      if (e.isDirectory()) walk(p);
      else files.push(path.relative(dist, p).split(path.sep).join("/"));
    }
  };
  walk(dist);
  const hand = new Set(handMadeUrls());
  return sitePages(files)
    .filter((p) => !hand.has(p.own))
    .map((p) => ({ url: p.own, family: p.path.match(/^\/([^/]+)\//)?.[1] ?? "other" }));
}

export function readRepresentatives(file = path.join(PKG, "e2e/representatives.json")) {
  const reps = JSON.parse(fs.readFileSync(file, "utf8"));
  const out = {};
  for (const [family, claims] of Object.entries(reps))
    if (!family.startsWith("$")) out[family] = [...new Set(Object.values(claims))];
  return out;
}

/** The URLs a run measures: {repeated (3 runs each), single (1 run each)}. */
export function pick({
  set,
  changed = [],
  shard = null,
  extra = [],
  pages = generatedPages(),
  reps = readRepresentatives(),
  hand = handMadeUrls(),
}) {
  const both = (u) => [u, inLang("ar", u)];
  if (set === "all") {
    let all = [...new Set([...hand, ...pages.map((p) => p.url)])].sort();
    if (shard) {
      const [i, n] = shard;
      all = all.filter((_, k) => k % n === i - 1);
    }
    return { repeated: [], single: all };
  }
  if (set !== "pr") throw new Error(`lighthouse.mjs: unknown set ${set}`);
  const repeated = new Set([...hand, ...Object.values(reps).flat().flatMap(both), ...extra]);
  const single = familiesChanged(changed).flatMap((family) =>
    sample(pages.filter((p) => p.family === family && !repeated.has(p.url)).map((p) => p.url))
  );
  return { repeated: [...repeated].sort(), single: [...new Set(single)].sort() };
}

// ---- running lhci ---------------------------------------------------------------------------

async function chromePath() {
  if (process.env.CHROME_PATH) return process.env.CHROME_PATH;
  const { chromium } = await import("@playwright/test");
  return chromium.executablePath();
}

/** The effective lighthouserc.json, with the hand-made pages' pattern filled in. */
export function config() {
  const rc = JSON.parse(fs.readFileSync(path.join(PKG, "lighthouserc.json"), "utf8"));
  for (const m of rc.ci.assert.assertMatrix)
    if (m.matchingUrlPattern === "HAND_MADE") m.matchingUrlPattern = handMadePattern();
  return rc;
}

/** Runs lhci asynchronously (the control's fixture server answers from this process meanwhile),
 * its output appended to the log. */
function lhci(args, cwd, log) {
  return new Promise((resolve, reject) => {
    const child = spawn(process.execPath, [LHCI, ...args], { cwd });
    let out = "";
    child.stdout.on("data", (d) => (out += d));
    child.stderr.on("data", (d) => (out += d));
    child.on("error", reject);
    child.on("close", (status) => {
      fs.appendFileSync(log, `$ lhci ${args.join(" ")}\n${out}\n`);
      resolve({ status, out });
    });
  });
}

/** How many more times a collect that Lighthouse could not finish is run. */
export const RUNTIME_RETRIES = 2;

/** The Lighthouse runtimeError code a failed collect's output names, or null when it failed for
 * another reason (which is never retried). */
export function runtimeErrorOf(output = "") {
  return (
    output.match(/Runtime error encountered:[^\n]*\(([A-Z][A-Z_]+)\)/)?.[1] ??
    output.match(/"runtimeError":\s*\{\s*"code":\s*"([A-Z][A-Z_]+)"/)?.[1] ??
    null
  );
}

/** The lhr files in a work folder, by the path of the URL they measured. */
function lhrsByUrl(work) {
  const dir = path.join(work, ".lighthouseci");
  const out = {};
  if (!fs.existsSync(dir)) return out;
  for (const f of fs.readdirSync(dir).filter((f) => /^lhr-.*\.json$/.test(f))) {
    const file = path.join(dir, f);
    let lhr;
    try {
      lhr = JSON.parse(fs.readFileSync(file, "utf8"));
    } catch {
      continue;
    }
    const url = new URL(lhr.requestedUrl ?? lhr.finalDisplayedUrl ?? lhr.finalUrl).pathname;
    (out[url] ??= []).push({ file, failed: lhr.runtimeError?.code ?? null });
  }
  return out;
}

/** After a stopped collect: the URLs without `runs` good measurements. Their partial or failed
 * lhrs are removed, so each is measured afresh and judged on `runs` runs, as the others. */
export function unfinished(work, urls, runs) {
  const have = lhrsByUrl(work);
  const out = [];
  for (const u of urls) {
    const lhrs = have[u] ?? [];
    if (lhrs.length >= runs && !lhrs.some((l) => l.failed)) continue;
    for (const l of lhrs) fs.rmSync(l.file, { force: true });
    out.push(u);
  }
  return out;
}

/** Measures `urls` (each `runs` times) in a work folder of its own. A collect that Lighthouse
 * could not finish (a runtimeError) is run again for the URLs it left unmeasured, up to
 * RUNTIME_RETRIES more times; any other failure stops at once. */
export async function measure({
  base,
  urls,
  runs,
  work,
  rcFile,
  chrome,
  log,
  additive = false,
  run = lhci,
  say = console.log,
}) {
  fs.mkdirSync(work, { recursive: true });
  if (!urls.length) return;
  let todo = urls;
  for (let attempt = 0; ; attempt++) {
    const args = [
      "collect",
      `--config=${rcFile}`,
      `--chromePath=${chrome}`,
      `--numberOfRuns=${runs}`,
      ...todo.map((u) => `--url=${base}${u}`),
    ];
    if (additive || attempt > 0) args.push("--additive");
    const r = await run(args, work, log);
    // a collect that finished can still have kept a run Lighthouse could not finish
    const kept = Object.values(lhrsByUrl(work))
      .flat()
      .find((l) => l.failed)?.failed;
    if (r.status === 0 && !kept) return;
    const code = r.status === 0 ? kept : runtimeErrorOf(r.out);
    if (!code || attempt >= RUNTIME_RETRIES)
      throw new Error(
        `lhci collect failed (${r.status}${code ? `, Lighthouse's ${code} after ${attempt + 1} attempts` : ""}): see ${log}`
      );
    todo = unfinished(work, todo, runs);
    const line = `lighthouse: Lighthouse could not finish a run (${code}); measuring the ${todo.length} unmeasured URL(s) again (retry ${attempt + 1} of ${RUNTIME_RETRIES})`;
    say(line);
    fs.appendFileSync(log, `${line}\n`);
    if (!todo.length) return;
  }
}

async function judge({ work, rcFile, log }) {
  const r = await lhci(["assert", `--config=${rcFile}`], work, log);
  const file = path.join(work, ".lighthouseci/assertion-results.json");
  const results = fs.existsSync(file) ? JSON.parse(fs.readFileSync(file, "utf8")) : [];
  const failing = results.filter((a) => !a.passed && a.level === "error");
  return { status: r.status, failing };
}

/** The median run's scores of each URL (median by performance, as lhci's median-run). */
function scores(work) {
  const dir = path.join(work, ".lighthouseci");
  const byUrl = {};
  if (!fs.existsSync(dir)) return byUrl;
  for (const f of fs.readdirSync(dir).filter((f) => /^lhr-.*\.json$/.test(f))) {
    const lhr = JSON.parse(fs.readFileSync(path.join(dir, f), "utf8"));
    const url = new URL(lhr.finalDisplayedUrl ?? lhr.finalUrl ?? lhr.requestedUrl).pathname;
    (byUrl[url] ??= []).push(lhr);
  }
  const out = {};
  for (const [url, lhrs] of Object.entries(byUrl)) {
    lhrs.sort(
      (a, b) => (a.categories.performance?.score ?? 0) - (b.categories.performance?.score ?? 0)
    );
    const m = lhrs[Math.floor(lhrs.length / 2)];
    const cat = (k) => m.categories[k]?.score ?? null;
    out[url] = {
      runs: lhrs.length,
      performance: cat("performance"),
      accessibility: cat("accessibility"),
      "best-practices": cat("best-practices"),
      seo: cat("seo"),
      cls: m.audits["cumulative-layout-shift"]?.numericValue ?? null,
      bytes: m.audits["total-byte-weight"]?.numericValue ?? null,
    };
  }
  return out;
}

const pathOf = (u) => new URL(u).pathname;

function summary(table, failures, title) {
  const pct = (v) => (v === null || v === undefined ? "–" : String(Math.round(v * 100)));
  const lines = [
    `### ${title}`,
    "",
    "| Page | Runs | Performance | Accessibility | Best practices | SEO | CLS | KB |",
    "|---|---|---|---|---|---|---|---|",
  ];
  for (const [url, s] of Object.entries(table).sort())
    lines.push(
      `| ${failures.includes(url) ? "❌ " : ""}\`${url}\` | ${s.runs} | ${pct(s.performance)} | ${pct(s.accessibility)} | ${pct(s["best-practices"])} | ${pct(s.seo)} | ${s.cls === null ? "–" : s.cls.toFixed(3)} | ${s.bytes === null ? "–" : Math.round(s.bytes / 1024)} |`
    );
  lines.push(
    "",
    failures.length
      ? `**Under the bar:** ${failures.map((u) => `\`${u}\``).join(", ")}`
      : "Every page meets the bar."
  );
  return lines.join("\n") + "\n";
}

async function upload({ work, rcFile, out, log }) {
  await lhci(
    ["upload", `--config=${rcFile}`, "--target=filesystem", `--outputDir=${out}`],
    work,
    log
  );
}

async function run(opts) {
  const out = path.resolve(opts.out);
  fs.rmSync(out, { recursive: true, force: true });
  fs.mkdirSync(out, { recursive: true });
  const log = path.join(out, "lhci.log");
  const rcFile = path.join(out, "lighthouserc.json");
  fs.writeFileSync(rcFile, JSON.stringify(config(), null, 2));
  const chrome = await chromePath();
  const { repeated, single } = pick(opts);
  fs.writeFileSync(path.join(out, "urls.json"), JSON.stringify({ repeated, single }, null, 2));
  console.log(`lighthouse: ${repeated.length} URL(s) × 3 runs, ${single.length} × 1 run`);

  const work = path.join(out, "work");
  await measure({ base: opts.base, urls: repeated, runs: 3, work, rcFile, chrome, log });
  await measure({
    base: opts.base,
    urls: single,
    runs: 1,
    work,
    rcFile,
    chrome,
    log,
    additive: repeated.length > 0,
  });
  let last = await judge({ work, rcFile, log });
  await upload({ work, rcFile, out: path.join(out, "reports"), log });
  const table = scores(work);
  let failures = [...new Set(last.failing.map((a) => pathOf(a.url)))].sort();

  // the nightly's single runs: a failing URL is measured again, and judged on that median
  if (opts.remeasure && failures.length) {
    console.log(
      `lighthouse: re-measuring ${failures.length} failing URL(s) ${opts.remeasure} times`
    );
    const again = path.join(out, "remeasure");
    await measure({
      base: opts.base,
      urls: failures,
      runs: opts.remeasure,
      work: again,
      rcFile,
      chrome,
      log,
    });
    last = await judge({ work: again, rcFile, log });
    await upload({ work: again, rcFile, out: path.join(out, "reports-remeasured"), log });
    Object.assign(table, scores(again));
    failures = [...new Set(last.failing.map((a) => pathOf(a.url)))].sort();
  }

  fs.writeFileSync(path.join(out, "scores.json"), JSON.stringify(table, null, 2) + "\n");
  fs.writeFileSync(path.join(out, "failures.json"), JSON.stringify(failures, null, 2) + "\n");
  const md = summary(table, failures, opts.title ?? "maktaba.tv: Lighthouse (mobile, median run)");
  fs.writeFileSync(path.join(out, "summary.md"), md);
  if (process.env.GITHUB_STEP_SUMMARY) fs.appendFileSync(process.env.GITHUB_STEP_SUMMARY, md);
  console.log(md);
  for (const a of last.failing)
    console.log(
      `::error::${pathOf(a.url)}: ${a.auditProperty ? `${a.auditId}.${a.auditProperty}` : a.auditId} is ${a.actual}, the bar is ${a.operator} ${a.expected}`
    );
  return failures.length ? 1 : 0;
}

// ---- T02: the positive control ---------------------------------------------------------------

const PNG_1x1 = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+M8AAAMBAQAYDS5GAAAAAElFTkSuQmCC",
  "base64"
);

/** A page whose image arrives late and unsized, pushing the text below it down. */
const FIXTURE = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>An unsized image</title>
<meta name="description" content="Story 60.04 T02: a page whose image has no width or height."></head>
<body><h1>An unsized image</h1><img src="/late.png" alt="A late image" style="width:100%">
<p>This paragraph moves down when the image above it arrives, a layout shift Lighthouse must
see.</p></body></html>`;

async function control(opts) {
  const out = path.resolve(opts.out);
  fs.rmSync(out, { recursive: true, force: true });
  fs.mkdirSync(out, { recursive: true });
  const log = path.join(out, "lhci.log");
  const rcFile = path.join(out, "lighthouserc.json");
  fs.writeFileSync(rcFile, JSON.stringify(config(), null, 2));
  const server = http.createServer((req, res) => {
    if (req.url === "/late.png") {
      setTimeout(() => res.writeHead(200, { "Content-Type": "image/png" }).end(PNG_1x1), 1500);
      return;
    }
    res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" }).end(FIXTURE);
  });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const base = `http://127.0.0.1:${server.address().port}`;
  try {
    const work = path.join(out, "work");
    await measure({
      base,
      urls: ["/unsized"],
      runs: 1,
      work,
      rcFile,
      chrome: await chromePath(),
      log,
    });
    const { failing } = await judge({ work, rcFile, log });
    await upload({ work, rcFile, out: path.join(out, "reports"), log });
    const cls = failing.find((a) => a.auditId === "cumulative-layout-shift");
    if (!cls) {
      console.log("::error::T02: the page with an unsized image passed the layout-shift bar");
      return 1;
    }
    console.log(
      `lighthouse control: the unsized image's CLS is ${cls.actual}: the bar refuses it, as it must`
    );
    return 0;
  } finally {
    server.close();
  }
}

// ---- the nightly's verdict --------------------------------------------------------------------

/** The pages the sweep failed: each shard's failures, and all of a shard's pages when it left no
 * readable result. */
export function verdict({
  shards,
  dir,
  shardUrls = (i) => pick({ set: "all", shard: [i, shards] }).single,
}) {
  const failed = new Set();
  const notes = [];
  for (let i = 1; i <= shards; i++) {
    const file = path.join(dir, `shard-${i}`, "failures.json");
    let list = null;
    try {
      list = JSON.parse(fs.readFileSync(file, "utf8"));
      if (!Array.isArray(list)) list = null;
    } catch {
      list = null;
    }
    if (list === null) {
      const urls = shardUrls(i);
      notes.push(`shard ${i}/${shards} left no result: its ${urls.length} page(s) count as failed`);
      for (const u of urls) failed.add(u);
    } else for (const u of list) failed.add(u);
  }
  return { failed: [...failed].sort(), notes };
}

// ---- the command line ------------------------------------------------------------------------

function parse(argv) {
  const o = { set: "pr", changed: [], shard: null, extra: [], remeasure: 0 };
  for (let i = 1; i < argv.length; i++) {
    const a = argv[i];
    const v = () => argv[++i];
    if (a === "--set") o.set = v();
    else if (a === "--base") o.base = v().replace(/\/$/, "");
    else if (a === "--out") o.out = v();
    else if (a === "--title") o.title = v();
    else if (a === "--changed") {
      const f = v();
      o.changed = fs.existsSync(f) ? fs.readFileSync(f, "utf8").split("\n").filter(Boolean) : [];
    } else if (a === "--shard") {
      const m = v().match(/^(\d+)\/(\d+)$/);
      if (!m || +m[1] < 1 || +m[1] > +m[2]) throw new Error("--shard I/N, 1 ≤ I ≤ N");
      o.shard = [+m[1], +m[2]];
    } else if (a === "--extra") {
      const x = v();
      o.extra = x ? JSON.parse(x) : [];
      if (
        !Array.isArray(o.extra) ||
        o.extra.some((u) => typeof u !== "string" || !u.startsWith("/"))
      )
        throw new Error("--extra: a JSON list of site paths");
    } else if (a === "--remeasure") o.remeasure = +v();
    else if (a === "--shards") o.shards = +v();
    else if (a === "--in") o.in = v();
    else throw new Error(`lighthouse.mjs: unknown argument ${a}`);
  }
  return o;
}

async function main(argv) {
  const cmd = argv[0];
  const o = parse(argv);
  if (cmd === "urls") {
    console.log(JSON.stringify(pick(o), null, 2));
    return 0;
  }
  if (cmd === "run") {
    if (!o.base || !o.out) throw new Error("run: --base and --out are needed");
    return run(o);
  }
  if (cmd === "verdict") {
    if (!o.shards || !o.in || !o.out)
      throw new Error("verdict: --shards, --in and --out are needed");
    const { failed, notes } = verdict({ shards: o.shards, dir: o.in });
    fs.writeFileSync(o.out, JSON.stringify(failed, null, 2) + "\n");
    const md = [
      "### maktaba.tv: the nightly sweep",
      "",
      ...notes.map((n) => `- ${n}`),
      failed.length
        ? `**${failed.length} page(s) under the bar** (the next deploy measures them again and refuses while they fail): ${failed.map((u) => `\`${u}\``).join(", ")}`
        : "Every page in both languages meets the bar.",
      "",
    ].join("\n");
    if (process.env.GITHUB_STEP_SUMMARY) fs.appendFileSync(process.env.GITHUB_STEP_SUMMARY, md);
    console.log(md);
    for (const u of failed) console.log(`::error title=maktaba.tv nightly::${u} is under the bar`);
    return failed.length ? 1 : 0;
  }
  if (cmd === "control") {
    if (!o.out) throw new Error("control: --out is needed");
    return control(o);
  }
  throw new Error("usage: lighthouse.mjs urls|run|control ...");
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).then(
    (code) => (process.exitCode = code),
    (e) => {
      console.error(e.message);
      process.exitCode = 2;
    }
  );
}
