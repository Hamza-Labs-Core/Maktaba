// The status page's tests (Epic 50 story 50.11, T01–T04 and T11): node --test status/test/
import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { applyRound, lastMonthDays, loadPosts, roundState, update, worst } from "../lib.mjs";
import { build } from "../build.mjs";
import { esc } from "../render.mjs";
import strings from "../strings.json" with { type: "json" };

const config = { site: "https://status.maktaba.tv", target: 99.9, components: ["relay", "api", "push"], probed: ["relay", "api", "push"], payments_status_url: null, plugins_listed: false, days: 90 };
const tmp = () => fs.mkdtempSync(path.join(os.tmpdir(), "status-"));
const put = (dir, rel, v) => {
  const f = path.join(dir, rel);
  fs.mkdirSync(path.dirname(f), { recursive: true });
  fs.writeFileSync(f, typeof v === "string" ? v : JSON.stringify(v));
};
const post = (fm, en = "What happened.", ar = "ما حدث.") =>
  `---\n${Object.entries(fm).map(([k, v]) => `${k}: ${v}`).join("\n")}\n---\n\n## English\n\n${en}\n\n## العربية\n\n${ar}\n`;
const read = (dir, rel) => fs.readFileSync(path.join(dir, rel), "utf8");
// the page's one inline stylesheet or script cut out (the builder writes them exactly so)
const withoutBlock = (html, open, close) => {
  const i = html.indexOf(open), j = html.indexOf(close, i);
  return i < 0 || j < 0 ? html : html.slice(0, i) + html.slice(j + close.length);
};

test("T02 the worst component sets the headline", () => {
  assert.equal(worst(["degraded", "outage"]), "outage");
  assert.equal(worst(["operational", "maintenance"]), "maintenance");
  assert.equal(worst(["maintenance", "degraded"]), "degraded");
  assert.equal(worst([null, "operational"]), "operational");
});

test("a round's state: both networks failing is an outage, one is degraded", () => {
  assert.equal(roundState("relay", { relay: { actions: 0, witness: 0 } }), "outage");
  assert.equal(roundState("relay", { relay: { actions: 0, witness: 1 } }), "degraded");
  assert.equal(roundState("relay", { relay: { actions: 1, witness: 1 } }), "operational");
  // the witness unreachable (Hetzner down): the outside network alone decides
  assert.equal(roundState("relay", { relay: { actions: 0, witness: null } }), "outage");
  assert.equal(roundState("relay", { relay: { actions: 1, witness: 1, held_ratio: 0.08 } }), "degraded");
  assert.equal(roundState("push", { push: { lag: 301 } }), "degraded");
  assert.equal(roundState("push", { push: { lag: null } }), null);
  assert.equal(roundState("api", {}), null);
});

test("a state changes only after two rounds in a row (no flapping)", () => {
  let r = applyRound(undefined, "operational", "t0");
  r = applyRound(r, "outage", "t1");
  assert.equal(r.state, "operational", "one failed round changes nothing");
  r = applyRound(r, "operational", "t2");
  r = applyRound(r, "outage", "t3");
  assert.equal(r.state, "operational");
  r = applyRound(r, "outage", "t4");
  assert.equal(r.state, "outage");
  assert.equal(r.since, "t4");
  r = applyRound(r, "operational", "t5");
  assert.equal(r.state, "outage", "recovery also takes two passing rounds");
  r = applyRound(r, "operational", "t6");
  assert.equal(r.state, "operational");
});

test("update folds rounds into day files and skips rounds it has seen", () => {
  const d = tmp();
  const rounds = [];
  for (let i = 0; i < 4; i++) rounds.push({ at: `2026-11-03T10:0${i}:00Z`, relay: { actions: i < 2 ? 1 : 0, witness: i < 2 ? 1 : 0 }, api: { actions: 1, witness: 1 }, push: { lag: 1 } });
  let s = update(d, rounds, config.probed);
  assert.equal(s.components.relay.state, "outage");
  update(d, rounds, config.probed); // the same rounds again: no double count
  const day = JSON.parse(read(d, "checks/relay/2026-11-03.json"));
  assert.deepEqual(day, { date: "2026-11-03", rounds: 4, operational: 3, degraded: 0, outage: 1 });
  s = JSON.parse(read(d, "checks/state.json"));
  assert.equal(s.updated_at, "2026-11-03T10:03:00Z");
});

test("T01 the uptime bars follow the fixture's per-day uptime", () => {
  const d = tmp(), out = tmp(), inc = tmp();
  put(d, "checks/state.json", { updated_at: "2026-11-03T10:00:00Z", components: { relay: { state: "operational" }, api: { state: "operational" }, push: { state: "operational" } } });
  put(d, "checks/relay/2026-11-01.json", { date: "2026-11-01", rounds: 1000, operational: 1000, degraded: 0, outage: 0 });
  put(d, "checks/relay/2026-11-02.json", { date: "2026-11-02", rounds: 1000, operational: 995, degraded: 0, outage: 5 });
  put(d, "checks/relay/2026-11-03.json", { date: "2026-11-03", rounds: 100, operational: 50, degraded: 0, outage: 50 });
  const m = build({ checksDir: d, incidentsDir: inc, out, now: new Date("2026-11-03T12:00:00Z"), config });
  const days = Object.fromEntries(m.components.relay.days.map((x) => [x.date, x.uptime]));
  assert.equal(days["2026-11-01"], 1);
  assert.equal(days["2026-11-02"], 0.995);
  assert.equal(days["2026-11-03"], 0.5);
  assert.equal(days["2026-10-31"], null);
  assert.equal(m.components.relay.days.length, 90);
  const html = read(out, "index.html");
  assert.match(html, /class="ok" title="2026-11-01: 100\.00%"/);
  assert.match(html, /class="minor" title="2026-11-02: 99\.50%"/);
  assert.match(html, /class="major" title="2026-11-03: 50\.00%"/);
  assert.match(html, /class="" title="2026-10-31: No data"/);
});

test("T03 an incident without title_ar fails the build with the file name", () => {
  const inc = tmp();
  put(inc, "2026-11-03-relay-slow.md", post({ component: "relay", state: "investigating", started: "2026-11-03T10:00:00Z", title_en: "Remote access is slow" }));
  assert.throws(() => loadPosts(inc), /2026-11-03-relay-slow\.md: title_ar is missing/);
});

test("a post's mistakes fail with the file name", () => {
  const cases = [
    [{ component: "relay", state: "investigating", started: "2026-11-03 10:00", title_en: "a", title_ar: "ب" }, /started must be UTC in ISO 8601/],
    [{ component: "relay", state: "investigating", started: "2026-11-03T10:00:00+02:00", title_en: "a", title_ar: "ب" }, /started must be UTC/],
    [{ component: "nope", state: "investigating", started: "2026-11-03T10:00:00Z", title_en: "a", title_ar: "ب" }, /unknown component nope/],
    [{ component: "relay", state: "resolved", started: "2026-11-03T10:00:00Z", title_en: "a", title_ar: "ب" }, /needs resolved/],
    [{ kind: "maintenance", component: "relay", state: "scheduled", starts: "2026-11-05T02:00:00Z", ends: "2026-11-05T01:00:00Z", title_en: "a", title_ar: "ب" }, /ends must come after starts/],
  ];
  for (const [fm, re] of cases) {
    const inc = tmp();
    put(inc, "2026-11-03-x.md", post(fm));
    assert.throws(() => loadPosts(inc), (e) => re.test(e.message) && e.message.startsWith("2026-11-03-x.md"), String(re));
  }
  const inc = tmp();
  put(inc, "2026-11-03-x.md", `---\ncomponent: relay\nstate: investigating\nstarted: 2026-11-03T10:00:00Z\ntitle_en: a\ntitle_ar: ب\n---\n\n## English\n\nOnly English.\n`);
  assert.throws(() => loadPosts(inc), /2026-11-03-x\.md: no "## العربية"/);
});

test("T04 the Arabic build is right to left, with no English left in the template strings", () => {
  const d = tmp(), out = tmp(), inc = tmp();
  put(d, "checks/state.json", { updated_at: "2026-11-03T10:00:00Z", components: { relay: { state: "degraded" }, api: { state: "operational" }, push: { state: "operational" } } });
  put(inc, "2026-11-03-relay-slow.md", post({ component: "relay", state: "identified", started: "2026-11-03T09:40:00Z", title_en: "Remote access is slow", title_ar: "الوصول عن بُعد بطيء" }));
  put(inc, "2026-11-04-db-upgrade.md", post({ kind: "maintenance", component: "relay, api", state: "scheduled", starts: "2026-11-05T02:00:00Z", ends: "2026-11-05T02:30:00Z", title_en: "Database upgrade", title_ar: "ترقية قاعدة البيانات" }));
  build({ checksDir: d, incidentsDir: inc, out, now: new Date("2026-11-03T12:00:00Z"), config });
  for (const f of ["ar/index.html", "ar/i/2026-11-03-relay-slow/index.html", "ar/i/2026-11-04-db-upgrade/index.html"]) {
    const html = read(out, f);
    assert.match(html, /<html lang="ar" dir="rtl">/, f);
    const text = withoutBlock(withoutBlock(html, "<style>", "</style>"), "<script>", "</script>");
    for (const [k, en] of Object.entries(strings.en)) {
      if (en === strings.ar[k] || !/[A-Za-z]{3}/.test(en.replace(/\{\w+\}/g, ""))) continue;
      assert.ok(!text.includes(en.replace(/\{\w+\}.*$/, "")), `${f}: English ${k} left: ${en}`);
    }
    assert.ok(!text.includes("Remote access is slow"), `${f}: the English title`);
  }
  const ar = read(out, "ar/index.html");
  for (const k of ["status.component.relay", "status.component.api", "status.component.push", "status.state.degraded", "status.overall.degraded"]) {
    assert.ok(ar.includes(strings.ar[k]), `the Arabic page has ${k}`);
  }
  assert.ok(ar.includes("الوصول عن بُعد بطيء") && ar.includes("ترقية قاعدة البيانات"));
  assert.match(read(out, "index.html"), /<html lang="en" dir="ltr">/);
});

test("every English string has Arabic, and the reverse", () => {
  assert.deepEqual(Object.keys(strings.ar).sort(), Object.keys(strings.en).sort());
  for (const [k, v] of Object.entries(strings.ar)) {
    assert.ok(v.trim(), `${k} is empty`);
    const holes = (s) => (s.match(/\{\w+\}/g) || []).sort().join();
    assert.equal(holes(v), holes(strings.en[k]), `${k}: the same placeholders`);
  }
});

test("T11 a month at 99.7 % shows the target, the figure and uptime.policy; no credit is offered", () => {
  const d = tmp(), out = tmp(), inc = tmp();
  put(d, "checks/state.json", { updated_at: "2026-11-03T10:00:00Z", components: { relay: { state: "operational" } } });
  for (const day of lastMonthDays(new Date("2026-11-03T12:00:00Z"))) {
    put(d, `checks/relay/${day}.json`, { date: day, rounds: 1000, operational: 997, degraded: 0, outage: 3 });
  }
  const m = build({ checksDir: d, incidentsDir: inc, out, now: new Date("2026-11-03T12:00:00Z"), config });
  assert.equal(m.lastMonth, 0.997);
  for (const lang of ["en", "ar"]) {
    const html = read(out, lang === "en" ? "index.html" : "ar/index.html");
    const target = strings[lang]["status.target"].replace("{target}", "99.9%").replace("{percent}", "99.70%");
    assert.ok(html.includes(target), `${lang}: ${target}`);
    assert.ok(html.includes(esc(strings[lang]["uptime.policy"])), `${lang}: uptime.policy`);
    const rest = html.replace(esc(strings[lang]["uptime.policy"]), "");
    assert.doesNotMatch(rest, /credit|refund|owed|compensat|رصيد|استرداد|تعويض/i, `${lang}: no credit wording outside uptime.policy`);
  }
});

test("summary.json: states, the open incident, maintenance ahead; none for nothing measured", () => {
  const d = tmp(), out = tmp(), inc = tmp();
  put(d, "checks/state.json", { updated_at: "2026-11-03T10:15:00Z", components: { relay: { state: "outage" }, api: { state: "operational" }, push: { state: "degraded" } } });
  put(inc, "2026-11-03-relay-down.md", post({ component: "relay", state: "investigating", started: "2026-11-03T10:05:00Z", title_en: "Remote access is down", title_ar: "الوصول عن بُعد متوقف" }));
  put(inc, "2026-11-01-old.md", post({ component: "api", state: "resolved", started: "2026-11-01T10:05:00Z", resolved: "2026-11-01T10:35:00Z", title_en: "Sign-in failed", title_ar: "فشل تسجيل الدخول" }));
  put(inc, "2026-11-02-db.md", post({ kind: "maintenance", component: "relay", state: "scheduled", starts: "2026-11-05T02:00:00Z", ends: "2026-11-05T02:30:00Z", title_en: "Database upgrade", title_ar: "ترقية قاعدة البيانات" }));
  build({ checksDir: d, incidentsDir: inc, out, now: new Date("2026-11-03T12:00:00Z"), config });
  const s = JSON.parse(read(out, "summary.json"));
  assert.equal(s.overall, "outage");
  assert.deepEqual(s.components, { relay: "outage", api: "operational", push: "degraded" });
  assert.equal(s.incident.id, "2026-11-03-relay-down");
  assert.equal(s.incident.url, "https://status.maktaba.tv/i/2026-11-03-relay-down");
  assert.deepEqual(s.incident.title, { en: "Remote access is down", ar: "الوصول عن بُعد متوقف" });
  assert.equal(s.maintenance.length, 1);
  assert.equal(s.maintenance[0].starts_at, "2026-11-05T02:00:00Z");
  assert.ok(read(out, "index.html").includes("Sign-in failed"), "the past incident is listed");
  assert.equal(read(out, "CNAME").trim(), "status.maktaba.tv");

  // during the window the component is under maintenance
  const out2 = tmp();
  put(d, "checks/state.json", { updated_at: "2026-11-05T02:10:00Z", components: { relay: { state: "operational" }, api: { state: "operational" }, push: { state: "operational" } } });
  fs.rmSync(path.join(inc, "2026-11-03-relay-down.md"));
  build({ checksDir: d, incidentsDir: inc, out: out2, now: new Date("2026-11-05T02:10:00Z"), config });
  const s2 = JSON.parse(read(out2, "summary.json"));
  assert.equal(s2.components.relay, "maintenance");
  assert.equal(s2.overall, "maintenance");
  assert.equal(s2.incident, null);

  // after the window it is a past notice, and not in summary.json
  const out3 = tmp();
  build({ checksDir: d, incidentsDir: inc, out: out3, now: new Date("2026-11-05T03:00:00Z"), config });
  assert.deepEqual(JSON.parse(read(out3, "summary.json")).maintenance, []);
});

test("an incident's body is escaped: a post cannot inject HTML", () => {
  const out = tmp(), inc = tmp(), d = tmp();
  put(inc, "2026-11-03-x.md", post({ component: "relay", state: "investigating", started: "2026-11-03T10:00:00Z", title_en: "<b>t</b>", title_ar: "ب" }, "<script>alert(1)</script> [ok](https://example.com) [no](javascript:alert(1))"));
  build({ checksDir: d, incidentsDir: inc, out, now: new Date("2026-11-03T12:00:00Z"), config });
  const html = read(out, "i/2026-11-03-x/index.html");
  assert.ok(!html.includes("<script>alert"));
  assert.ok(!html.includes("<b>t</b>"));
  assert.ok(html.includes('<a href="https://example.com">ok</a>'));
  assert.ok(!html.includes('href="javascript'));
});

test("no measurement yet: the page says so instead of claiming all is well", () => {
  const out = tmp();
  build({ checksDir: tmp(), incidentsDir: tmp(), out, now: new Date("2026-11-03T12:00:00Z"), config });
  assert.ok(read(out, "index.html").includes(strings.en["status.noDataYet"]));
});

test("publish-data.sh commits and pushes the data, and publishes at once only on a change", async () => {
  const { execFileSync } = await import("node:child_process");
  const here = path.dirname(new URL(import.meta.url).pathname);
  const script = path.join(here, "..", "publish-data.sh");
  const root = tmp();
  const git = (cwd, ...a) => execFileSync("git", a, { cwd, encoding: "utf8" }).trim();
  git(root, "init", "-q", "--bare", "origin.git");
  git(root, "clone", "-q", path.join(root, "origin.git"), "data");
  const data = path.join(root, "data");
  git(data, "checkout", "-q", "-b", "status-data");
  const rounds = path.join(root, "rounds");
  const marker = path.join(root, "published");
  const run = (...flags) => execFileSync("bash", [script, data, rounds, ...flags], {
    encoding: "utf8", env: { ...process.env, PUBLISH_CMD: `echo x >> ${marker}` },
  });
  const round = (at, up) => JSON.stringify({ at, relay: { actions: up, witness: null }, api: { actions: 1, witness: null }, push: { lag: null } }) + "\n";
  fs.writeFileSync(rounds, round("2026-11-03T10:00:00Z", 1));
  run("--if-changed"); // the first measurement is a change
  const published = () => (fs.existsSync(marker) ? fs.readFileSync(marker, "utf8").split("\n").filter(Boolean).length : 0);
  assert.equal(published(), 1);
  fs.appendFileSync(rounds, round("2026-11-03T10:00:30Z", 1));
  run("--if-changed");
  assert.equal(published(), 1, "no change: nothing published mid-run");
  fs.appendFileSync(rounds, round("2026-11-03T10:01:00Z", 0) + round("2026-11-03T10:01:30Z", 0));
  run("--if-changed");
  assert.equal(published(), 2, "relay went down: published at once");
  run(); // the run's end: everything, even with no change
  const log = git(path.join(root, "origin.git"), "log", "--format=%s", "status-data");
  assert.match(log.split("\n")[0], /"relay":"outage"/);
  const pushed = git(path.join(root, "origin.git"), "show", "status-data:checks/relay/2026-11-03.json");
  assert.deepEqual(JSON.parse(pushed), { date: "2026-11-03", rounds: 4, operational: 3, degraded: 0, outage: 1 });
});
