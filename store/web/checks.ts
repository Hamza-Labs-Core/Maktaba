// The automated checks (story 54.16 §1): the `store-checks` queue's consumer. The static
// checks run here, in the Worker, with the home's own rules compiled to WebAssembly (the
// package reader, the manifest validator and the host's import audit: crates/storewasm and
// crates/audit); the sandbox run (C8), the ClamAV scan (C7's other half) and the
// reproducible build (C10) run in the isolated runner (54.16 §1: the CI fallback,
// store-check-runner.yml), which posts its report back on a single-use URL. Then the version
// goes to a person (§2) or is approved automatically and countersigned `automated`.
import CAPS from "../checks/capabilities.json";
import RULES from "../checks/rules.json";
import { parseJSON } from "../signing/canonical";
import { Root, emptyState } from "../signing/verify";
import committedRoot from "../../shared/plugins/plugins-root.json";
import { sha256hex, token } from "./crypto";
import { type Env, iso, now } from "./env";
import { sendMail } from "./mail";
import { countersign } from "./publish";
import { PackageRefused, extract, inspect, type Inspected } from "./wasm";
import { type C12, checkFlowAtVerification } from "./flowcheck";
import { graphJson, type Graph } from "../../tools/plugin-sdk/js/src/flow";

export type Result = "pass" | "warn" | "block" | "skip" | "pending";

export interface Check {
  id: string;
  result: Result;
  code?: string;
  detail?: string;
  /** C12: the refusal in Arabic too, and the cycle path or the bad edge */
  detail_ar?: string;
  path?: string[];
}

export interface Report {
  submission: string;
  plugin: string;
  version: string;
  started: string;
  finished?: string;
  checks: Check[];
  /** Why a person reviews it (§2); empty when it is approved automatically. */
  route_reasons: string[];
  rating: number;
  capabilities: { required: string[]; optional: string[] };
  diff?: { new_capabilities: string[]; new_panels: string[]; new_stages: string[]; size_ratio: number };
  imports: string[];
  runner?: RunnerReport;
  /** C12: the flow this version is checked in (54.16 §1) */
  flow?: FlowSummary;
}

export interface FlowSummary {
  /** the flow's id and version, or the plugin's own for a plugin alone (a flow of one) */
  id: string;
  version: string;
  members: string[];
  events: string[];
  subscriptions: string[];
  graph: Graph;
  findings: C12["findings"];
  /** the flow submission it came in, if any */
  group?: string;
}

/** What the isolated runner posts (store/check-runner/run.sh). */
export interface RunnerReport {
  c7: { result: Result; detail?: string };
  c8: { result: Result; code?: string; detail?: string; peak_memory_mb?: number; calls?: Record<string, number>; generated_only?: boolean };
  c10: { result: Result; reproducible?: boolean; detail?: string };
}

interface Cap {
  name: string;
  risk: string;
  interface: string;
  param: string;
}
const capList = (CAPS as { capabilities: Cap[] }).capabilities;
const capOf = (name: string): Cap | undefined => capList.find((c) => c.name === name.split(":", 1)[0]);

const RESERVED = [/maktaba/i, /مكتبة/, /\bofficial\b/i];
const URL_LIKE = /(https?:\/\/|www\.|[a-z0-9-]+\.(com|net|org|io|dev|tv|app|me|ly)\b)/i;
const EMAIL = /[^\s@]+@[^\s@]+\.[a-z]{2,}/i;
const PHONE = /(\+?\d[\d\s().-]{7,}\d)/;
/** Arabic letters without their diacritics (a reserved word written with tashkeel is still it). */
const plainArabic = (s: string) => s.replace(/[ً-ٰٟـ]/g, "");

/** The rating (54.08 §2), the same function as the home's (shared/plugins/testdata/ratings.json). */
export function rating(capabilities: string[], level: string): number {
  let r = 6;
  for (const name of new Set(capabilities)) {
    const c = capOf(name);
    if (!c) continue;
    if (c.risk === "medium") r--;
    if (c.risk === "high") r -= 2;
    if (name.startsWith("plugin-data.read:")) r--;
  }
  if (level !== "store" && level !== "automated" && level !== "reviewed") r--;
  return Math.max(r, 1);
}

/** C6 on the plugin's own strings: every key in both languages; nothing to fetch; no reserved word in the name. */
export function stringChecks(strings: Record<string, unknown>, nameKey: string): Check {
  const en = (strings.en ?? {}) as Record<string, unknown>;
  const ar = (strings.ar ?? {}) as Record<string, unknown>;
  const missing = [...Object.keys(en).filter((k) => !(k in ar)), ...Object.keys(ar).filter((k) => !(k in en))];
  if (missing.length) return { id: "C6", result: "block", code: "strings_missing", detail: missing.slice(0, 10).join(", ") };
  const names = [String(en[nameKey] ?? ""), plainArabic(String(ar[nameKey] ?? ""))];
  if (names.some((n) => RESERVED.some((re) => re.test(n))))
    return { id: "C6", result: "block", code: "name_reserved", detail: names.join(" / ") };
  const linky = Object.entries({ ...en, ...Object.fromEntries(Object.entries(ar).map(([k, v]) => [`ar:${k}`, v])) }).filter(
    ([, v]) => typeof v === "string" && (URL_LIKE.test(v) || EMAIL.test(v) || PHONE.test(v)),
  );
  if (linky.length)
    return {
      id: "C6",
      result: "warn",
      code: "strings_link",
      detail: linky
        .map(([k]) => k)
        .slice(0, 10)
        .join(", "),
    };
  return { id: "C6", result: "pass" };
}

/** C7's static rules on the component's bytes. */
export function malwareRules(wasm: Uint8Array): Check {
  const text = new TextDecoder("latin1").decode(wasm).toLowerCase();
  for (const r of (RULES as { rules: { id: string; patterns: string[]; all: boolean }[] }).rules) {
    const hits = r.patterns.filter((p) => text.includes(p.toLowerCase()));
    if (r.all ? hits.length === r.patterns.length : hits.length > 0) return { id: "C7", result: "block", code: "malware", detail: r.id };
  }
  return { id: "C7", result: "pass", detail: "static rules; ClamAV in the isolated runner" };
}

interface Manifest {
  id: string;
  version: string;
  maktaba: string;
  capabilities: { required: string[]; optional: string[] };
  panel?: { name?: string; id?: string }[];
  stage?: { name: string }[];
  strings: { name: string; summary: string };
}

interface Sub {
  id: string;
  plugin: string;
  version: string;
  uploaded_by: string;
  package_key: string;
  source_key: string;
  package_size: number;
  flow_submission: string | null;
  flow_sha256: string;
}

async function saveReport(env: Env, r: Report): Promise<string> {
  const key = `reports/${r.submission}.json`;
  await env.PRIVATE.put(key, JSON.stringify(r));
  return key;
}

/** The static checks (C1-C7's static half, C9, C11), then the isolated runner or the route. */
export async function runChecks(env: Env, submissionId: string): Promise<Report> {
  const s = await env.DB_CATALOG.prepare("SELECT * FROM submissions WHERE id = ?").bind(submissionId).first<Sub>();
  if (!s) throw new Error(`no submission ${submissionId}`);
  const report: Report = {
    submission: s.id,
    plugin: s.plugin,
    version: s.version,
    started: iso(now()),
    checks: [],
    route_reasons: [],
    rating: 0,
    capabilities: { required: [], optional: [] },
    imports: [],
  };
  const block = async (c: Check) => {
    report.checks.push(c);
    return finish(env, s, report);
  };
  const obj = await env.PRIVATE.get(s.package_key);
  if (!obj) return block({ id: "C1", result: "block", code: "package_missing" });
  const pkg = new Uint8Array(await obj.arrayBuffer());
  let i: Inspected;
  try {
    i = await inspect(pkg);
  } catch (e) {
    if (e instanceof PackageRefused) {
      // a manifest refusal is C3's, any other C1's
      return block({ id: e.code.startsWith("manifest_") ? "C3" : "C1", result: "block", code: e.code, detail: e.detail });
    }
    throw e;
  }
  report.checks.push({ id: "C1", result: "pass" });
  const m = JSON.parse(i.manifest) as Manifest;
  report.capabilities = m.capabilities;
  report.imports = i.imports;

  // C2: the developer's signature, by a key registered for this account and active, and the
  // id under a verified prefix of the account
  if (m.id !== s.plugin || m.version !== s.version)
    return block({ id: "C3", result: "block", code: "package_mismatch", detail: `${m.id} ${m.version}` });
  if (!i.developer_sig) return block({ id: "C2", result: "block", code: "developer_sig_missing" });
  const dev = parseJSON(i.developer_sig) as { key_id?: string };
  const key = await env.DB_CATALOG.prepare(
    "SELECT public_key FROM developer_keys WHERE id = ? AND account = ? AND revoked_at IS NULL AND suspended_at IS NULL",
  )
    .bind(String(dev.key_id ?? ""), s.uploaded_by)
    .first<{ public_key: string }>();
  if (!key) return block({ id: "C2", result: "block", code: "developer_key_inactive", detail: String(dev.key_id ?? "") });
  try {
    const keys = await (
      await Root.parse(env.PLUGINS_ROOT ?? JSON.stringify(committedRoot))
    ).verifyStoreKeys(await (await env.BUCKET.get("store-keys.json"))!.text(), emptyState(), now());
    const { capabilitiesSHA256 } = await import("../signing/sig");
    await keys.verifyPackage({
      filesJSON: i.files_json,
      developerSig: i.developer_sig,
      pluginId: m.id,
      version: m.version,
      capabilitiesSHA256: await capabilitiesSHA256(m.capabilities),
    });
  } catch (e) {
    return block({
      id: "C2",
      result: "block",
      code: (e as { code?: string }).code ?? "plugin_signature_invalid",
      detail: (e as Error).message,
    });
  }
  const prefix = await env.DB_CATALOG.prepare(
    "SELECT prefix FROM developer_prefixes WHERE account = ? AND verified_at IS NOT NULL AND lapsed_at IS NULL",
  )
    .bind(s.uploaded_by)
    .all<{ prefix: string }>();
  if (!prefix.results.some((p) => m.id.startsWith(p.prefix + "."))) return block({ id: "C2", result: "block", code: "prefix_unverified" });
  report.checks.push({ id: "C2", result: "pass", detail: String(dev.key_id) });

  // C3: the manifest (the home's validator, in inspect) and an upper bound on maktaba
  if (!/</.test(m.maktaba)) return block({ id: "C3", result: "block", code: "maktaba_upper_bound", detail: m.maktaba });
  const flagged = await env.DB_CATALOG.prepare("SELECT word FROM flagged_words").all<{ word: string }>();
  const squat = flagged.results.filter((w) => m.id.toLowerCase().includes(w.word.toLowerCase()));
  report.checks.push(
    squat.length
      ? { id: "C3", result: "warn", code: "id_flagged", detail: squat.map((w) => w.word).join(", ") }
      : { id: "C3", result: "pass" },
  );

  // C4: the host's import audit
  if (i.audit) return block({ id: "C4", result: "block", code: i.audit.code, detail: i.audit.detail });
  report.checks.push({ id: "C4", result: "pass" });

  // C5: declared but not imported; medium capabilities without a stated reason
  const declared = [...m.capabilities.required, ...m.capabilities.optional];
  const imported = new Set(i.imports.map((n) => n.replace(/^maktaba:plugin\//, "").split("@")[0]));
  const unused = declared.filter((c) => {
    const cap = capOf(c);
    return cap && !imported.has(cap.interface) && !(cap.name === "library.read" && imported.has("media"));
  });
  const ver = await env.DB_CATALOG.prepare("SELECT notes_en FROM versions WHERE plugin = ? AND version = ?")
    .bind(s.plugin, s.version)
    .first<{ notes_en: string }>();
  const unexplained = declared.filter((c) => capOf(c)?.risk !== "low" && !(ver?.notes_en ?? "").includes(c.split(":", 1)[0] ?? c));
  const c5: string[] = [];
  if (unused.length) c5.push(`declared, not imported: ${unused.join(", ")}`);
  if (unexplained.length) c5.push(`no reason given: ${unexplained.join(", ")}`);
  report.checks.push(
    c5.length ? { id: "C5", result: "warn", code: "capability_audit", detail: c5.join("; ") } : { id: "C5", result: "pass" },
  );

  // C6: strings
  const c6 = stringChecks(i.strings, m.strings.name);
  if (c6.result === "block") return block(c6);
  report.checks.push(c6);

  // C7 (static rules here; ClamAV in the runner)
  const wasmBytes = await componentBytes(pkg);
  const c7 = malwareRules(wasmBytes);
  if (c7.result === "block") return block(c7);
  report.checks.push(c7);

  // C9: the diff against the newest approved version
  const prev = await env.DB_CATALOG.prepare(
    "SELECT manifest, size FROM versions WHERE plugin = ? AND status = 'approved' ORDER BY released DESC LIMIT 1",
  )
    .bind(s.plugin)
    .first<{ manifest: string; size: number }>();
  if (prev) {
    const pm = JSON.parse(prev.manifest) as Manifest;
    const pDeclared = [...pm.capabilities.required, ...pm.capabilities.optional];
    report.diff = {
      new_capabilities: declared.filter((c) => !pDeclared.includes(c)),
      new_panels: (m.panel ?? []).map((p) => p.id ?? p.name ?? "").filter((n) => !(pm.panel ?? []).some((q) => (q.id ?? q.name) === n)),
      new_stages: (m.stage ?? []).map((x) => x.name).filter((n) => !(pm.stage ?? []).some((q) => q.name === n)),
      size_ratio: prev.size ? Math.round((s.package_size / prev.size) * 100) / 100 : 1,
    };
    const flags = [
      ...(report.diff.new_stages.length ? [`new exports: ${report.diff.new_stages.join(", ")}`] : []),
      ...(report.diff.size_ratio > 3 ? [`size ×${report.diff.size_ratio}`] : []),
    ];
    report.checks.push(flags.length ? { id: "C9", result: "warn", code: "diff", detail: flags.join("; ") } : { id: "C9", result: "pass" });
  } else {
    report.checks.push({ id: "C9", result: "skip", detail: "a first version" });
  }
  report.rating = rating(declared, "automated");
  report.checks.push({ id: "C11", result: "pass", detail: `rating ${report.rating}` });

  // C12's input: the flow.toml the package carries (byte-identical in every member; the
  // signatures cover it, as every file in files.json). The check runs once the flow's
  // members have all passed C1 to C11 (finish).
  const flowFile = i.files.find((f) => f.path === "flow.toml");
  if (s.flow_submission && !flowFile)
    return block({ id: "C12", result: "block", code: "flow_invalid", detail: "a flow submission's package carries no flow.toml" });
  if (flowFile) {
    s.flow_sha256 = flowFile.sha256;
    await env.DB_CATALOG.prepare("UPDATE submissions SET flow_sha256 = ? WHERE id = ?").bind(flowFile.sha256, s.id).run();
  }

  // the isolated runner: C7 (ClamAV), C8 and C10
  if (env.RUNNER === "github" && env.RUNNER_DISPATCH_TOKEN) {
    await dispatchRunner(env, s);
    report.checks.push({ id: "C8", result: "pending" }, { id: "C10", result: "pending" });
    await env.DB_CATALOG.prepare("UPDATE submissions SET runner_state = 'dispatched', report_key = ? WHERE id = ?")
      .bind(await saveReport(env, report), s.id)
      .run();
    return report;
  }
  report.checks.push(
    { id: "C8", result: "skip", detail: "no isolated runner is configured: a person reviews" },
    { id: "C10", result: "skip", detail: "no isolated runner is configured" },
  );
  await env.DB_CATALOG.prepare("UPDATE submissions SET runner_state = 'skipped' WHERE id = ?").bind(s.id).run();
  return finish(env, s, report);
}

/** plugin.wasm of a package (the checks scan the component's bytes). */
async function componentBytes(pkg: Uint8Array): Promise<Uint8Array> {
  const { extract } = await import("./wasm");
  return extract(pkg, "plugin.wasm");
}

/** Why a person must review it (§2). */
async function routeReasons(env: Env, r: Report, first: boolean): Promise<string[]> {
  const reasons: string[] = [];
  if (first) reasons.push("first_version");
  if (r.diff?.new_capabilities.length) reasons.push("new_capability");
  if (r.diff?.new_panels.length) reasons.push("new_panel");
  if (
    [...r.capabilities.required, ...r.capabilities.optional].some((c) => c.startsWith("plugin-data.read")) &&
    (first || r.diff?.new_capabilities.some((c) => c.startsWith("plugin-data.read")))
  ) {
    reasons.push("plugin_data_read");
  }
  for (const c of r.checks)
    if (c.result === "warn" && ["C3", "C5", "C6", "C9", "C10"].includes(c.id)) reasons.push(`flagged_${c.id.toLowerCase()}`);
  if (Math.random() < Number(env.REVIEW_SAMPLE ?? "0.05")) reasons.push("sampled");
  r.route_reasons = [...new Set(reasons)];
  return r.route_reasons;
}

/**
 * The end of the checks: blocked (the developer is told), to a person, or approved
 * automatically and countersigned `automated` (the catalog regenerates).
 */
async function finish(env: Env, s: Sub, r: Report): Promise<Report> {
  r.finished = iso(now());
  const blocked = r.checks.find((c) => c.result === "block");
  if (blocked) {
    await settle(env, s, r, []);
    // a flow is refused as a whole: the members that passed their own checks are refused too
    if (s.flow_submission) await refuseFlow(env, s.flow_submission, `${s.plugin} ${s.version}: ${blocked.code ?? blocked.id}`);
    return r;
  }
  return flowGate(env, s, r);
}

/**
 * C12 (54.16 §1): once every member of the submission's flow has passed C1 to C11 (a plugin
 * alone at once), the flow check runs over all of them with the store's catalog; then the
 * members are refused together, go to a person together, or are approved together.
 */
async function flowGate(env: Env, s: Sub, r: Report): Promise<Report> {
  type Member = { s: Sub; r: Report };
  let members: Member[] = [{ s, r }];
  let flowId = s.plugin;
  let flowVersion = s.version;
  if (s.flow_submission) {
    const fs = await env.DB_CATALOG.prepare("SELECT * FROM flow_submissions WHERE id = ?")
      .bind(s.flow_submission)
      .first<{ status: string; members: string; flow_id: string; flow_version: string }>();
    r.checks.push({ id: "C12", result: "pending", detail: "waiting for the flow's other members" });
    await env.DB_CATALOG.prepare("UPDATE submissions SET flow_state = 'waiting', report_key = ? WHERE id = ?")
      .bind(await saveReport(env, r), s.id)
      .run();
    if (!fs || fs.status !== "open") return r;
    const want = JSON.parse(fs.members) as string[];
    const subs = await env.DB_CATALOG.prepare("SELECT * FROM submissions WHERE flow_submission = ? AND status = 'checking'")
      .bind(s.flow_submission)
      .all<Sub & { flow_state: string; report_key: string }>();
    const waiting = subs.results.filter((x) => x.flow_state === "waiting");
    if (!want.every((id) => waiting.some((x) => x.plugin === id))) return r;
    // the last member in: this run checks the flow (a conditional update, so only one does)
    const claim = await env.DB_CATALOG.prepare("UPDATE flow_submissions SET status = 'in_review' WHERE id = ? AND status = 'open'")
      .bind(s.flow_submission)
      .run();
    if (!claim.meta.changes) return r;
    members = [];
    for (const x of waiting) {
      const rep = x.id === s.id ? r : (JSON.parse((await (await env.PRIVATE.get(x.report_key))?.text()) ?? "null") as Report);
      rep.checks = rep.checks.filter((c) => c.id !== "C12");
      members.push({ s: x, r: rep });
    }
    flowId = fs.flow_id;
    flowVersion = fs.flow_version;
  }
  // the flow.toml, the same bytes in every member
  const sums = new Set(members.map((m) => m.s.flow_sha256));
  let flowToml: Uint8Array | null = null;
  const manifests: Record<string, unknown>[] = [];
  for (const m of members) {
    const pkg = new Uint8Array(await (await env.PRIVATE.get(m.s.package_key))!.arrayBuffer());
    manifests.push(JSON.parse((await inspect(pkg)).manifest) as Record<string, unknown>);
    if (m.s.flow_sha256 && !flowToml) flowToml = await extract(pkg, "flow.toml");
  }
  let c12: C12;
  if (sums.size > 1) {
    const f = { code: "flow_manifest_mismatch", severity: "refuse" as const, detail: "the members carry different flow.toml files" };
    c12 = { ok: false, findings: [{ ...f, en: "The members carry different flow.toml files.", ar: "تحمل الأعضاء ملفات flow.toml مختلفة." }], graph: { nodes: [], edges: [] }, events: [], subscriptions: [], members: [] };
  } else {
    c12 = await checkFlowAtVerification(env, flowToml, manifests);
  }
  if (flowToml && !s.flow_submission) {
    // a flow of several submitted one member at a time: its id and version are the flow's own
    const head = new TextDecoder().decode(flowToml);
    flowId = /^id\s*=\s*"([^"]+)"/m.exec(head)?.[1] ?? flowId;
    flowVersion = /^version\s*=\s*"([^"]+)"/m.exec(head)?.[1] ?? flowVersion;
  }
  const refusal = c12.findings.find((f) => f.severity === "refuse");
  const warns = c12.findings.filter((f) => f.severity === "warn");
  const check: Check = refusal
    ? { id: "C12", result: "block", code: refusal.code, detail: refusal.en, detail_ar: refusal.ar, ...(refusal.path ? { path: refusal.path } : {}) }
    : warns.length
      ? { id: "C12", result: "warn", code: warns[0]!.code, detail: warns.map((w) => w.en).join("; "), detail_ar: warns.map((w) => w.ar).join("؛ ") }
      : { id: "C12", result: "pass", detail: `${c12.members.length} plugin(s), ${c12.events.length} published event(s), ${c12.subscriptions.length} subscription(s)` };
  const summary: FlowSummary = {
    id: flowId,
    version: flowVersion,
    members: c12.members,
    events: c12.events,
    subscriptions: c12.subscriptions,
    graph: c12.graph,
    findings: c12.findings,
    ...(s.flow_submission ? { group: s.flow_submission } : {}),
  };
  for (const m of members) {
    m.r.checks.push(check);
    m.r.flow = summary;
    m.r.finished = iso(now());
  }
  if (s.flow_submission) {
    await env.PRIVATE.put(`reports/flow-${s.flow_submission}.json`, JSON.stringify(summary));
    await env.DB_CATALOG.prepare("UPDATE flow_submissions SET report_key = ? WHERE id = ?")
      .bind(`reports/flow-${s.flow_submission}.json`, s.flow_submission)
      .run();
  }
  if (refusal) {
    for (const m of members) await settle(env, m.s, m.r, []);
    if (s.flow_submission) await env.DB_CATALOG.prepare("UPDATE flow_submissions SET status = 'rejected', decided_at = ? WHERE id = ?").bind(iso(now()), s.flow_submission).run();
    return r;
  }
  // the route is the flow's: a person reviews all of it when any member or the flow needs one
  const extra = flowToml ? await flowReasons(env, summary) : [];
  const reasons = new Set(extra);
  for (const m of members) {
    for (const x of await routeReasons(env, m.r, !m.r.diff)) reasons.add(x);
    const runnerSkipped = !m.r.runner && m.r.checks.some((c) => c.id === "C8" && c.result === "skip");
    if (runnerSkipped) reasons.add("runner_unavailable");
  }
  for (const m of members) await settle(env, m.s, m.r, [...reasons]);
  if (reasons.size === 0) {
    await recordFlow(env, summary, [...sums][0] ?? "");
    if (s.flow_submission) await env.DB_CATALOG.prepare("UPDATE flow_submissions SET status = 'approved', decided_at = ? WHERE id = ?").bind(iso(now()), s.flow_submission).run();
    await env.CATALOG_WRITER.get(env.CATALOG_WRITER.idFromName("main")).regenerate();
  }
  return r;
}

/** Why a flow goes to a person (54.16 §2): a new flow, or a version adding a member, an
 * event or a subscription to the newest approved one. */
async function flowReasons(env: Env, f: FlowSummary): Promise<string[]> {
  const prev = await env.DB_CATALOG.prepare("SELECT members, events, subscriptions FROM flows WHERE id = ? ORDER BY approved_at DESC LIMIT 1")
    .bind(f.id)
    .first<{ members: string; events: string; subscriptions: string }>();
  if (!prev) return ["new_flow"];
  const grew = (now: string[], before: string) => now.some((x) => !(JSON.parse(before) as string[]).includes(x));
  const out: string[] = [];
  if (grew(f.members, prev.members)) out.push("flow_new_member");
  if (grew(f.events, prev.events)) out.push("flow_new_event");
  if (grew(f.subscriptions, prev.subscriptions)) out.push("flow_new_subscription");
  return out;
}

/** An approved flow, for the next version's comparison and the listing's chart. */
export async function recordFlow(env: Env, f: FlowSummary, sha: string): Promise<void> {
  const at = iso(now());
  await env.DB_CATALOG.batch([
    env.DB_CATALOG.prepare(
      "INSERT OR REPLACE INTO flows (id, version, members, events, subscriptions, graph, flow_sha256, approved_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
    ).bind(f.id, f.version, JSON.stringify(f.members), JSON.stringify(f.events), JSON.stringify(f.subscriptions), graphJson(f.graph), sha, at),
    ...f.members.map((m) =>
      env.DB_CATALOG.prepare("INSERT OR REPLACE INTO flow_members (plugin, flow_id, version) VALUES (?, ?, ?)").bind(m, f.id, f.version),
    ),
  ]);
}

/** A member refused: every other member of its flow submission waiting on C12 is refused too. */
async function refuseFlow(env: Env, group: string, why: string): Promise<void> {
  await env.DB_CATALOG.prepare("UPDATE flow_submissions SET status = 'rejected', decided_at = ? WHERE id = ? AND status = 'open'")
    .bind(iso(now()), group)
    .run();
  const waiting = await env.DB_CATALOG.prepare("SELECT * FROM submissions WHERE flow_submission = ? AND status = 'checking' AND flow_state = 'waiting'")
    .bind(group)
    .all<Sub & { report_key: string }>();
  for (const x of waiting.results) {
    const rep = JSON.parse((await (await env.PRIVATE.get(x.report_key))?.text()) ?? "null") as Report;
    rep.checks = rep.checks.filter((c) => c.id !== "C12");
    rep.checks.push({
      id: "C12",
      result: "block",
      code: "flow_member_refused",
      detail: `Another member of the flow was refused (${why}).`,
      detail_ar: `رُفض عضو آخر في التدفق (${why}).`,
    });
    rep.finished = iso(now());
    await settle(env, x, rep, []);
  }
}

/**
 * One version's outcome: blocked (the developer is told), to a person (reasons), or approved
 * automatically and countersigned `automated`.
 */
async function settle(env: Env, s: Sub, r: Report, reasons: string[]): Promise<Report> {
  const blocked = r.checks.find((c) => c.result === "block");
  r.route_reasons = blocked ? [] : reasons;
  await env.DB_CATALOG.prepare("UPDATE submissions SET flow_state = 'checked' WHERE id = ?").bind(s.id).run();
  const key = await saveReport(env, r);
  const route = blocked ? "blocked" : r.route_reasons.length ? "human" : "auto";
  const status = blocked ? "rejected" : route === "human" ? "in_review" : "approved";
  await env.DB_CATALOG.batch([
    env.DB_CATALOG.prepare(
      "UPDATE submissions SET status = ?, route = ?, route_reasons = ?, report_key = ?, flags = ?, checked_at = ?, decision = ?, reasons = ?, reproducible = ? WHERE id = ?",
    ).bind(
      status,
      route,
      JSON.stringify(r.route_reasons),
      key,
      JSON.stringify(r.checks.filter((c) => c.result === "warn").map((c) => `${c.id}:${c.code}`)),
      iso(now()),
      blocked ? "blocked" : route === "auto" ? "approved" : "",
      JSON.stringify(blocked ? [blocked.code ?? blocked.id] : []),
      r.runner?.c10.reproducible === true ? "yes" : r.runner ? "no" : "",
      s.id,
    ),
    env.DB_CATALOG.prepare("UPDATE versions SET status = ? WHERE plugin = ? AND version = ?").bind(
      blocked ? "rejected" : route === "human" ? "in_review" : "checking",
      s.plugin,
      s.version,
    ),
  ]);
  if (route === "auto") await countersign(env, s.id, "automated");
  if (route !== "human") {
    const a = await env.DB_ACCOUNTS.prepare("SELECT email FROM accounts WHERE id = ?").bind(s.uploaded_by).first<{ email: string }>();
    if (a)
      await sendMail(env, {
        to: a.email,
        template: "submission_decided",
        params: { plugin: s.plugin, version: s.version, decision: blocked ? `blocked (${blocked.code})` : "approved" },
      });
  }
  return r;
}

// ---- the isolated runner (54.16 §1: the CI fallback) --------------------------------------

/** Single-use URLs for the runner: the package and source to read, the report to post. */
async function runnerURL(env: Env, submission: string, kind: "package" | "source" | "report"): Promise<string> {
  const t = token();
  await env.PRIVATE.put(
    `runner-tokens/${await sha256hex(t)}`,
    JSON.stringify({ submission, kind, expires: now().getTime() + 6 * 3600_000 }),
  );
  return `${env.STORE_ORIGIN}/v1/runner/${t}`;
}

async function dispatchRunner(env: Env, s: Sub): Promise<void> {
  const payload = {
    submission: s.id,
    package_url: await runnerURL(env, s.id, "package"),
    source_url: await runnerURL(env, s.id, "source"),
    report_url: await runnerURL(env, s.id, "report"),
  };
  // workflow_dispatch of the one runner workflow: the token needs only Actions write on this
  // repository, and names the workflow file it starts
  const r = await fetch(`https://api.github.com/repos/${env.RUNNER_REPOSITORY}/actions/workflows/store-check-runner.yml/dispatches`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${env.RUNNER_DISPATCH_TOKEN}`,
      Accept: "application/vnd.github+json",
      "User-Agent": "maktaba-plugin-store",
    },
    body: JSON.stringify({ ref: "main", inputs: payload }),
  });
  if (!r.ok) throw new Error(`the runner dispatch answered ${r.status}`);
}

/** GET (package, source) and POST (report) /v1/runner/{token}: each once. */
export async function runnerRoute(env: Env, req: Request, t: string): Promise<Response> {
  const key = `runner-tokens/${await sha256hex(t)}`;
  const meta = await env.PRIVATE.get(key);
  if (!meta) return Response.json({ error: "gone" }, { status: 410 });
  const m = JSON.parse(await meta.text()) as { submission: string; kind: string; expires: number };
  await env.PRIVATE.delete(key);
  if (m.expires < now().getTime()) return Response.json({ error: "gone" }, { status: 410 });
  const s = await env.DB_CATALOG.prepare("SELECT * FROM submissions WHERE id = ?").bind(m.submission).first<Sub>();
  if (!s) return Response.json({ error: "gone" }, { status: 410 });
  if (m.kind !== "report" && req.method === "GET") {
    const obj = await env.PRIVATE.get(m.kind === "package" ? s.package_key : s.source_key);
    return obj ? new Response(obj.body) : Response.json({ error: "gone" }, { status: 410 });
  }
  if (m.kind === "report" && req.method === "POST") {
    const text = await req.text();
    if (text.length > 1 << 20) return Response.json({ error: "too_large" }, { status: 413 });
    await acceptRunnerReport(env, s, JSON.parse(text) as RunnerReport);
    return Response.json({ status: "accepted" });
  }
  return Response.json({ error: "method" }, { status: 405 });
}

/** The runner's report joins the static one; the route is decided. */
export async function acceptRunnerReport(env: Env, s: Sub, runner: RunnerReport): Promise<Report> {
  const sub = await env.DB_CATALOG.prepare("SELECT report_key, runner_state FROM submissions WHERE id = ?")
    .bind(s.id)
    .first<{ report_key: string; runner_state: string }>();
  if (!sub || sub.runner_state !== "dispatched") throw new Error("no runner report is awaited");
  const r = JSON.parse((await (await env.PRIVATE.get(sub.report_key))!.text()) as string) as Report;
  r.checks = r.checks.filter((c) => c.result !== "pending");
  const fit = (x: { result: Result; code?: string; detail?: string }, id: string): Check => ({
    id,
    result: (["pass", "warn", "block", "skip"] as Result[]).includes(x.result) ? x.result : "block",
    code: x.code,
    detail: x.detail,
  });
  r.checks.push(
    fit(runner.c7, "C7"),
    fit(runner.c8, "C8"),
    fit({ ...runner.c10, result: runner.c10.result === "block" ? "warn" : runner.c10.result }, "C10"),
  );
  r.runner = runner;
  await env.DB_CATALOG.prepare("UPDATE submissions SET runner_state = 'reported' WHERE id = ?").bind(s.id).run();
  return finish(env, s, r);
}
