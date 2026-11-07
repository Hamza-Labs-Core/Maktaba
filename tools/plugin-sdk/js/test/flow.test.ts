// Story 54.39 T01, T08, T09 (ts): the flow corpus through the TypeScript checker gives the
// same findings, words and graph as the Go checker, byte for byte; N22.
import { strict as assert } from "node:assert";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

import {
  FLOW_CODES,
  checkMembers,
  fromManifest,
  graphJson,
  hasRefusal,
  inRange,
  matchManifest,
  resolveDepends,
  message,
  parseFlow,
  type Finding,
  type Member,
} from "../src/flow.js";
import { validate } from "../src/manifest.js";

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, "..", "..", "..", "..", "..");
const corpus = join(root, "shared", "plugins", "testdata", "flows");

function runCase(dir: string): string {
  const meta = JSON.parse(readFileSync(join(dir, "case.json"), "utf8")) as { mode: string; states: Record<string, string> };
  const manifests = new Map<string, Record<string, unknown>>();
  const pdir = join(dir, "plugins");
  const names = existsSync(pdir) ? readdirSync(pdir).filter((f) => f.endsWith(".toml")).sort() : [];
  const ids: string[] = [];
  for (const n of names) {
    const m = validate(readFileSync(join(pdir, n))) as Record<string, unknown>;
    manifests.set(m.id as string, m);
    ids.push(m.id as string);
  }
  const findings: Finding[] = [];
  const members: Member[] = [];
  if (meta.mode === "store") {
    const { flow, findings: fs } = parseFlow(readFileSync(join(dir, "flow.toml")));
    findings.push(...fs);
    const inFlow = new Set((flow?.members ?? []).map((m) => m.id));
    members.push(...(flow?.members ?? []));
    for (const id of ids) {
      const m = manifests.get(id) as Record<string, unknown>;
      if (flow && inFlow.has(id)) findings.push(...matchManifest(flow, m));
      else members.push(fromManifest(m));
    }
    // the store's other plugins, reached only through the flow's publishers
    const cdir = join(dir, "catalog");
    const catalog = new Map<string, Record<string, unknown>>();
    for (const n of existsSync(cdir) ? readdirSync(cdir).filter((f) => f.endsWith(".toml")) : []) {
      const m = validate(readFileSync(join(cdir, n))) as Record<string, unknown>;
      catalog.set(m.id as string, m);
    }
    const resolved = resolveDepends(members, (id) => {
      const m = catalog.get(id);
      return m ? fromManifest(m) : undefined;
    });
    members.length = 0;
    members.push(...resolved);
  } else {
    for (const id of ids) members.push(fromManifest(manifests.get(id) as Record<string, unknown>, meta.states[id] ?? ""));
  }
  const { graph, findings: fs } = checkMembers(members, { strict: meta.mode === "store" });
  findings.push(...fs);
  const ordered = findings.map((f) => {
    const o: Record<string, unknown> = { code: f.code, severity: f.severity };
    if (f.plugin) o.plugin = f.plugin;
    if (f.detail) o.detail = f.detail;
    if (f.path && f.path.length) o.path = f.path;
    return o;
  });
  const out = {
    findings: ordered,
    graph: JSON.parse(graphJson(graph)),
    messages: { ar: findings.map((f) => message(f, "ar")), en: findings.map((f) => message(f, "en")) },
  };
  return JSON.stringify(out, null, 2) + "\n";
}

const cases = readdirSync(corpus).filter((d) => existsSync(join(corpus, d, "case.json")));
test("the corpus has its cases", () => assert.ok(cases.length >= 20));
for (const name of cases) {
  test(`flow/${name}`, () => {
    assert.equal(runCase(join(corpus, name)), readFileSync(join(corpus, name, "expect.json"), "utf8"));
  });
}

test("T08: every code has English and Arabic words", () => {
  for (const code of [...Object.values(FLOW_CODES), "flow_unknown_key"]) {
    const f: Finding = { code, severity: "refuse", plugin: "dev.example.a", detail: "x", path: ["a/b", "a/b"] };
    const en = message(f, "en");
    const ar = message(f, "ar");
    assert.ok(en && !en.includes("{"), `${code} en`);
    assert.ok(ar && !ar.includes("{") && ar !== en, `${code} ar`);
  }
});

test("T09: N22, 100 plugins of 20 stages in at most 50 ms", () => {
  const members: Member[] = [];
  for (let p = 0; p < 100; p++) {
    const id = `dev.example.p${String(p).padStart(3, "0")}`;
    const prev = `dev.example.p${String(p - 1).padStart(3, "0")}`;
    const m: Member = { id, version: "1.0.0", stages: [], events: [{ name: "p.done", subject: "video", payload: "events/p.json" }], depends: {} };
    for (let s = 0; s < 20; s++) {
      const triggers = s === 0 ? ["audio.ready", ...(p > 0 ? [`plugin:${prev}/p.done`] : [])] : [`stage:s${String(s - 1).padStart(2, "0")}`];
      if (s === 0 && p > 0) (m.depends as Record<string, string>)[prev] = ">=1.0.0";
      m.stages.push({ name: `s${String(s).padStart(2, "0")}`, subject: "video", triggers, emits: s === 19 ? ["p.done"] : [], mayRequest: [] });
    }
    members.push(m);
  }
  // N22 is the check's own time on a home: warm (not V8's first compile of it), the best
  // of five, and counted as this process's CPU time, so a runner busy with other jobs does
  // not time its own queue
  checkMembers(members);
  let took = Infinity;
  let findings: Finding[] = [];
  for (let i = 0; i < 5; i++) {
    const start = process.cpuUsage();
    findings = checkMembers(members).findings;
    const used = process.cpuUsage(start);
    took = Math.min(took, (used.user + used.system) / 1000);
  }
  assert.ok(took <= 50, `took ${took} ms`);
  assert.ok(hasRefusal(findings));
});

test("version ranges", () => {
  for (const [v, r, ok] of [
    ["1.4.0", ">=1.2.0, <2.0.0", true],
    ["2.0.0", ">=1.2.0, <2.0.0", false],
    ["2.0.0-beta.1", "<2.0.0", true],
    ["1.0.0", "=1.0.0", true],
    ["1.0.1", ">1.0.0", true],
    ["1.0.0", ">1.0.0", false],
    ["10.0.0", ">=9.0.0", true],
  ] as const) {
    assert.equal(inRange(v, r), ok, `${v} in ${r}`);
  }
});
