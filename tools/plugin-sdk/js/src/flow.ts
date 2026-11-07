// The flow check of Epic 54 (README §4.12, story 54.39) for the SDK, the store's
// verification (54.16 C12) and the web's chart: the same algorithm as the Go checker
// (shared/plugins/pluginflow), giving the same codes, paths, words and normalised graph on
// every case of shared/plugins/testdata/flows.

import { flowMessages } from "./data.generated.js";
import { events as coreEvents, validateFlowDoc } from "./manifest.js";

export const FLOW_CODES = {
  invalid: "flow_invalid",
  limit: "flow_limit",
  duplicate: "flow_duplicate",
  notMember: "flow_not_member",
  manifestMismatch: "flow_manifest_mismatch",
  coreEdge: "flow_core_edge",
  foreignStage: "flow_foreign_stage",
  foreignEvent: "flow_foreign_event",
  unknownEvent: "flow_unknown_event",
  subjectMismatch: "flow_subject_mismatch",
  missingPlugin: "flow_missing_plugin",
  cycle: "flow_cycle",
  tooDeep: "flow_too_deep",
} as const;

/** The lineage hop cap (README §4.7): the longest declared chain of stages. */
export const MAX_HOPS = 16;
/** The largest combined graph a home checks. */
export const MAX_STAGES = 2000;

export interface FlowStage {
  name: string;
  subject: string;
  /** "audio.ready", "stage:<own>", "plugin:<id>/<event>" */
  triggers: string[];
  emits: string[];
  mayRequest: string[];
}

export interface FlowEvent {
  name: string;
  subject: string;
  payload: string;
}

/** One plugin as the graph sees it: from its manifest (version exact, depends with
 * ranges) or from a flow (version a range, depends undefined). */
export interface Member {
  id: string;
  version: string;
  stages: FlowStage[];
  events: FlowEvent[];
  depends?: Record<string, string>;
  /** "" or enabled: in the graph; installed or missing: shown only */
  state?: string;
}

export interface Finding {
  code: string;
  severity: "refuse" | "warn";
  plugin?: string;
  detail?: string;
  path?: string[];
}

export interface GraphNode {
  id: string;
  kind: "core" | "stage" | "event";
  name: string;
  plugin: string;
  state: string;
  subject: string;
}

export interface GraphEdge {
  from: string;
  kind: string;
  to: string;
}

export interface Graph {
  edges: GraphEdge[];
  nodes: GraphNode[];
}

export interface Flow {
  id: string;
  version: string;
  name: string;
  members: Member[];
}

type Obj = Record<string, unknown>;

const active = (m: Member) => !m.state || m.state === "enabled";
const str = (v: unknown) => (typeof v === "string" ? v : "");
const list = (v: unknown) => (Array.isArray(v) ? (v as unknown[]) : []);
const strs = (v: unknown) => list(v).map(str);
const cmp = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0);

/** The graph's one text: sorted keys, no spaces (Go's encoder, HTML unescaped). */
export function graphJson(g: Graph): string {
  const node = (n: GraphNode) =>
    `{"id":${JSON.stringify(n.id)},"kind":${JSON.stringify(n.kind)},"name":${JSON.stringify(n.name)},"plugin":${JSON.stringify(n.plugin)},"state":${JSON.stringify(n.state)},"subject":${JSON.stringify(n.subject)}}`;
  const edge = (e: GraphEdge) => `{"from":${JSON.stringify(e.from)},"kind":${JSON.stringify(e.kind)},"to":${JSON.stringify(e.to)}}`;
  return `{"edges":[${g.edges.map(edge).join(",")}],"nodes":[${g.nodes.map(node).join(",")}]}`;
}

export function hasRefusal(fs: Finding[]): boolean {
  return fs.some((f) => f.severity === "refuse");
}

/** A normalised manifest (validate()) as a member. */
export function fromManifest(m: Obj, state = ""): Member {
  const out: Member = { id: str(m.id), version: str(m.version), stages: [], events: [], depends: {}, state };
  for (const s of list(m.stage) as Obj[]) {
    out.stages.push({
      name: str(s.name),
      subject: str(s.subject),
      triggers: (list(s.trigger) as Obj[]).map((t) => (typeof t.event === "string" ? t.event : `stage:${str(t.stage)}`)),
      emits: strs(s.emits),
      mayRequest: strs(s.may_request),
    });
  }
  for (const e of list(m.event) as Obj[]) out.events.push({ name: str(e.name), subject: str(e.subject), payload: str(e.payload) });
  for (const d of list(m.depends) as Obj[]) (out.depends as Record<string, string>)[str(d.plugin)] = str(d.version);
  return out;
}

/** Read and check a flow.toml's schema. */
export function parseFlow(src: Uint8Array | string): { flow?: Flow; findings: Finding[] } {
  const { doc, errors } = validateFlowDoc(src);
  if (!doc) {
    return { findings: (errors ?? []).map((e) => ({ code: e.code, severity: "refuse" as const, detail: e.detail })) };
  }
  const flow: Flow = { id: str(doc.id), version: str(doc.version), name: str((doc.strings as Obj).name), members: [] };
  for (const p of list(doc.plugin) as Obj[]) {
    flow.members.push({
      id: str(p.id),
      version: normaliseRange(str(p.version)),
      state: "enabled",
      stages: (list(p.stage) as Obj[]).map((s) => ({
        name: str(s.name),
        subject: str(s.subject),
        triggers: strs(s.triggers),
        emits: strs(s.emits),
        mayRequest: strs(s.may_request),
      })),
      events: (list(p.event) as Obj[]).map((e) => ({ name: str(e.name), subject: str(e.subject), payload: str(e.payload) })),
    });
  }
  return { flow, findings: [] };
}

/** The package's manifest is a member of its flow and matches its entry exactly (54.39 §4,
 * checks 2 and 3). */
export function matchManifest(flow: Flow, manifest: Obj): Finding[] {
  const m = fromManifest(manifest);
  const entry = flow.members.find((x) => x.id === m.id);
  if (!entry || !inRange(m.version, entry.version)) return [{ code: FLOW_CODES.notMember, severity: "refuse", plugin: m.id }];
  const field = mismatch(m, entry);
  return field ? [{ code: FLOW_CODES.manifestMismatch, severity: "refuse", plugin: m.id, detail: field }] : [];
}

function sameSet(a: string[], b: string[]): boolean {
  const x = [...a].sort(cmp);
  const y = [...b].sort(cmp);
  return x.length === y.length && x.every((v, i) => v === y[i]);
}

function mismatch(m: Member, e: Member): string {
  const ms = new Map(m.stages.map((s) => [s.name, s]));
  const es = new Map(e.stages.map((s) => [s.name, s]));
  if (!sameSet([...ms.keys()], [...es.keys()])) return "stages";
  for (const name of [...ms.keys()].sort(cmp)) {
    const a = ms.get(name) as FlowStage;
    const b = es.get(name) as FlowStage;
    if (a.subject !== b.subject) return `stage ${name} subject`;
    if (!sameSet(a.triggers, b.triggers)) return `stage ${name} triggers`;
    if (!sameSet(a.emits, b.emits)) return `stage ${name} emits`;
    if (!sameSet(a.mayRequest, b.mayRequest)) return `stage ${name} may_request`;
  }
  const ev = (es: FlowEvent[]) => es.map((x) => `${x.name}|${x.subject}|${x.payload}`);
  if (!sameSet(ev(m.events), ev(e.events))) return "events";
  if (!sameSet(Object.keys(m.depends ?? {}), publishers(e))) return "depends";
  return "";
}

function publishers(m: Member): string[] {
  const seen = new Set<string>();
  for (const s of m.stages) for (const t of s.triggers) {
    const sub = subscription(t);
    if (sub) seen.add(sub.publisher);
  }
  return [...seen].sort(cmp);
}

function subscription(t: string): { publisher: string; event: string } | undefined {
  if (!t.startsWith("plugin:")) return undefined;
  const rest = t.slice("plugin:".length);
  const i = rest.indexOf("/");
  if (i < 0) return undefined;
  return { publisher: rest.slice(0, i), event: rest.slice(i + 1) };
}

const stageId = (p: string, s: string) => `stage:${p}/${s}`;
const eventId = (p: string, e: string) => `event:${p}/${e}`;

/** Build the combined graph of the active members and check it (54.39 §4, checks 4 to 8).
 * strict (the store) refuses a missing upstream; the home only warns. */
export function checkMembers(members: Member[], opts: { strict?: boolean } = {}): { graph: Graph; findings: Finding[] } {
  const nodes = new Map<string, GraphNode>();
  const edges = new Map<string, GraphEdge>();
  const findings: Finding[] = [];
  const node = (n: GraphNode) => {
    if (!nodes.has(n.id)) nodes.set(n.id, n);
  };
  const edge = (from: string, kind: string, to: string) => edges.set(`${from}\u0000${to}\u0000${kind}`, { from, kind, to });
  const find = (f: Finding) => findings.push(f);
  const sorted = [...members].sort((a, b) => cmp(a.id, b.id));
  const byId = new Map<string, Member>();
  const live: Member[] = [];
  let stages = 0;
  for (const m of sorted) {
    const prev = byId.get(m.id);
    if (active(m) && prev && active(prev)) find({ code: FLOW_CODES.duplicate, severity: "refuse", plugin: m.id, detail: m.id });
    else if (active(m)) {
      byId.set(m.id, m);
      live.push(m);
    } else if (!prev) byId.set(m.id, m);
  }
  const evs = new Map(coreEvents().map((e) => [e.name, e]));
  const core = new Set(coreEvents().map((e) => e.stage).filter((s) => s !== ""));
  const missing = opts.strict ? "refuse" : "warn";
  for (const m of live) {
    const own = new Map(m.stages.map((s) => [s.name, s]));
    const pub = new Map<string, FlowEvent>();
    for (const e of m.events) {
      pub.set(e.name, e);
      node({ id: eventId(m.id, e.name), kind: "event", name: e.name, plugin: m.id, state: "enabled", subject: e.subject });
    }
    for (const s of m.stages) {
      stages++;
      const to = stageId(m.id, s.name);
      node({ id: to, kind: "stage", name: s.name, plugin: m.id, state: "enabled", subject: s.subject });
      const where = `${m.id}/${s.name}`;
      for (const t of s.triggers) {
        if (t.startsWith("stage:")) {
          const name = t.slice("stage:".length);
          const from = own.get(name);
          if (from && from.subject !== s.subject) find({ code: FLOW_CODES.subjectMismatch, severity: "refuse", plugin: m.id, detail: `${where}: ${t}` });
          else if (from) edge(stageId(m.id, name), "after", to);
          else if (core.has(name)) find({ code: FLOW_CODES.coreEdge, severity: "refuse", plugin: m.id, detail: `${where}: ${t}` });
          else find({ code: FLOW_CODES.foreignStage, severity: "refuse", plugin: m.id, detail: `${where}: ${t}` });
          continue;
        }
        const sub = subscription(t);
        if (sub) {
          const up = byId.get(sub.publisher);
          const rng = m.depends?.[sub.publisher] ?? "";
          if (sub.publisher === m.id) find({ code: FLOW_CODES.invalid, severity: "refuse", plugin: m.id, detail: `${where}: ${t}` });
          else if (!up || !active(up) || (rng !== "" && !isRange(up.version) && !inRange(up.version, rng))) {
            find({ code: FLOW_CODES.missingPlugin, severity: missing, plugin: m.id, detail: rng ? `${sub.publisher} ${rng}` : sub.publisher });
          } else {
            const e = up.events.find((x) => x.name === sub.event);
            if (!e) find({ code: FLOW_CODES.unknownEvent, severity: "refuse", plugin: m.id, detail: t });
            else if (e.subject !== s.subject) find({ code: FLOW_CODES.subjectMismatch, severity: "refuse", plugin: m.id, detail: `${where}: ${t}` });
            else edge(eventId(sub.publisher, sub.event), "subscribe", to);
          }
          continue;
        }
        const e = evs.get(t);
        const triggerable = e !== undefined && e.trigger !== false;
        if (triggerable && e.subject !== s.subject) find({ code: FLOW_CODES.subjectMismatch, severity: "refuse", plugin: m.id, detail: `${where}: ${t}` });
        else if (triggerable) {
          node({ id: `core:${t}`, kind: "core", name: t, plugin: "", state: "enabled", subject: e.subject });
          edge(`core:${t}`, "trigger", to);
        } else if (core.has(t)) find({ code: FLOW_CODES.coreEdge, severity: "refuse", plugin: m.id, detail: `${where}: ${t}` });
        else find({ code: FLOW_CODES.unknownEvent, severity: "refuse", plugin: m.id, detail: t });
      }
      for (const r of s.mayRequest) {
        if (own.has(r)) edge(to, "request", stageId(m.id, r));
        else if (core.has(r)) find({ code: FLOW_CODES.coreEdge, severity: "refuse", plugin: m.id, detail: `${where}: ${r}` });
        else find({ code: FLOW_CODES.foreignStage, severity: "refuse", plugin: m.id, detail: `${where}: ${r}` });
      }
      for (const e of s.emits) {
        if (pub.has(e)) edge(to, "emit", eventId(m.id, e));
        else find({ code: FLOW_CODES.foreignEvent, severity: "refuse", plugin: m.id, detail: `${where}: ${e}` });
      }
    }
  }
  if (stages > MAX_STAGES) find({ code: FLOW_CODES.limit, severity: "refuse", detail: `stages ${stages}` });
  const graph: Graph = {
    nodes: [...nodes.values()].sort((a, b) => cmp(a.id, b.id)),
    edges: [...edges.values()].sort((a, b) => cmp(a.from, b.from) || cmp(a.to, b.to) || cmp(a.kind, b.kind)),
  };
  const adj = adjacency(graph);
  const cycles = cyclicComponents(graph, adj);
  for (const comp of cycles) {
    find({ code: FLOW_CODES.cycle, severity: "refuse", plugin: pluginOf(comp[0] as string), path: display(shortestCycle(comp, adj)) });
  }
  if (cycles.length === 0) {
    const path = longest(graph, adj);
    if (stageCount(path) > MAX_HOPS) {
      find({ code: FLOW_CODES.tooDeep, severity: "refuse", plugin: pluginOf(path[path.length - 1] as string), path: display(path) });
    }
  }
  return { graph, findings };
}

function adjacency(g: Graph): Map<string, string[]> {
  const adj = new Map<string, string[]>();
  for (const e of g.edges) {
    const s = adj.get(e.from) ?? [];
    if (s.length === 0 || s[s.length - 1] !== e.to) s.push(e.to);
    adj.set(e.from, s);
  }
  return adj;
}

function stageFirst(comp: string[]): string[] {
  comp.sort(cmp);
  const i = comp.findIndex((c) => c.startsWith("stage:"));
  if (i < 0) return comp;
  return [comp[i] as string, ...comp.slice(0, i), ...comp.slice(i + 1)];
}

/** Tarjan's strongly connected components that hold a cycle, each with its smallest stage
 * first, ordered by that stage. */
function cyclicComponents(g: Graph, adj: Map<string, string[]>): string[][] {
  const index = new Map<string, number>();
  const low = new Map<string, number>();
  const onStack = new Set<string>();
  const stack: string[] = [];
  const out: string[][] = [];
  let next = 0;
  const strong = (v: string) => {
    index.set(v, next);
    low.set(v, next);
    next++;
    stack.push(v);
    onStack.add(v);
    for (const w of adj.get(v) ?? []) {
      if (!index.has(w)) {
        strong(w);
        low.set(v, Math.min(low.get(v) as number, low.get(w) as number));
      } else if (onStack.has(w)) {
        low.set(v, Math.min(low.get(v) as number, index.get(w) as number));
      }
    }
    if (low.get(v) === index.get(v)) {
      const comp: string[] = [];
      for (;;) {
        const w = stack.pop() as string;
        onStack.delete(w);
        comp.push(w);
        if (w === v) break;
      }
      const self = (adj.get(v) ?? []).includes(v);
      if (comp.length > 1 || self) out.push(stageFirst(comp));
    }
  };
  for (const n of g.nodes) if (!index.has(n.id)) strong(n.id);
  return out.sort((a, b) => cmp(a[0] as string, b[0] as string));
}

/** From the component's smallest stage, the shortest way back to it inside the component,
 * by breadth-first search with successors in sorted order. */
function shortestCycle(comp: string[], adj: Map<string, string[]>): string[] {
  const inside = new Set(comp);
  const start = comp[0] as string;
  const parent = new Map<string, string>();
  const seen = new Set([start]);
  const queue = [start];
  for (let qi = 0; qi < queue.length; qi++) {
    const u = queue[qi] as string;
    for (const v of adj.get(u) ?? []) {
      if (!inside.has(v)) continue;
      if (v === start) {
        const back: string[] = [];
        for (let x = u; x !== start; x = parent.get(x) as string) back.push(x);
        return [start, ...back.reverse(), start];
      }
      if (!seen.has(v)) {
        seen.add(v);
        parent.set(v, u);
        queue.push(v);
      }
    }
  }
  return [start, start];
}

/** A min-heap of node ids: the smallest ready node first. */
class IdHeap {
  private h: string[] = [];
  get size() {
    return this.h.length;
  }
  push(x: string) {
    const h = this.h;
    h.push(x);
    let i = h.length - 1;
    while (i > 0) {
      const p = (i - 1) >> 1;
      if ((h[p] as string) <= x) break;
      h[i] = h[p] as string;
      i = p;
    }
    h[i] = x;
  }
  pop(): string {
    const h = this.h;
    const top = h[0] as string;
    const last = h.pop() as string;
    if (h.length > 0) {
      let i = 0;
      for (;;) {
        const l = 2 * i + 1;
        const r = l + 1;
        let m = i;
        let mv = last;
        if (l < h.length && (h[l] as string) < mv) {
          m = l;
          mv = h[l] as string;
        }
        if (r < h.length && (h[r] as string) < mv) {
          m = r;
          mv = h[r] as string;
        }
        if (m === i) break;
        h[i] = mv;
        i = m;
      }
      h[i] = last;
    }
    return top;
  }
}

/** The path with the most stages, in topological order taking the smallest ready node
 * first; ties go to the smallest predecessor and the smallest end node. */
function longest(g: Graph, adj: Map<string, string[]>): string[] {
  const indeg = new Map<string, number>();
  const preds = new Map<string, string[]>();
  for (const n of g.nodes) indeg.set(n.id, 0);
  for (const [from, tos] of adj) {
    for (const to of tos) {
      indeg.set(to, (indeg.get(to) ?? 0) + 1);
      const ps = preds.get(to);
      if (ps) ps.push(from);
      else preds.set(to, [from]);
    }
  }
  const ready = new IdHeap();
  for (const n of g.nodes) if (indeg.get(n.id) === 0) ready.push(n.id);
  const best = new Map<string, number>();
  const pred = new Map<string, string>();
  let end = "";
  while (ready.size > 0) {
    const v = ready.pop();
    const ps = [...(preds.get(v) ?? [])].sort(cmp);
    let b = 0;
    let p = "";
    for (const u of ps) {
      const bu = best.get(u) ?? 0;
      if (bu > b || p === "") {
        b = bu;
        p = u;
      }
    }
    if (v.startsWith("stage:")) b++;
    best.set(v, b);
    pred.set(v, p);
    const be = best.get(end) ?? 0;
    if (end === "" || b > be || (b === be && v < end)) end = v;
    for (const w of adj.get(v) ?? []) {
      indeg.set(w, (indeg.get(w) as number) - 1);
      if (indeg.get(w) === 0) ready.push(w);
    }
  }
  const path: string[] = [];
  for (let x = end; x !== ""; x = pred.get(x) ?? "") path.push(x);
  return path.reverse();
}

const stageCount = (path: string[]) => path.filter((p) => p.startsWith("stage:")).length;

/** Node ids as people read them: audio.ready, <plugin>/<stage>, plugin:<plugin>/<event>. */
export function display(ids: string[]): string[] {
  return ids.map((id) => {
    const i = id.indexOf(":");
    const kind = id.slice(0, i);
    const rest = id.slice(i + 1);
    return kind === "core" || kind === "stage" ? rest : `plugin:${rest}`;
  });
}

function pluginOf(id: string): string {
  if (id.startsWith("core:")) return "";
  const rest = id.slice(id.indexOf(":") + 1);
  return rest.split("/")[0] ?? "";
}

let messages: Record<string, Record<string, string>> | null = null;

/** A finding in English ("en") or Arabic ("ar"), from flow-messages.json. */
export function message(f: Finding, lang: "en" | "ar"): string {
  messages ??= JSON.parse(flowMessages) as Record<string, Record<string, string>>;
  const t = messages[f.code]?.[lang] || messages[f.code]?.en || "";
  return t
    .replaceAll("{plugin}", f.plugin ?? "")
    .replaceAll("{detail}", f.detail ?? "")
    .replaceAll("{path}", (f.path ?? []).join(" → "));
}

// ---- semver ranges ------------------------------------------------------------------

const isRange = (v: string) => /[<>=]/.test(v);

function normaliseRange(r: string): string {
  return r
    .split(",")
    .map((p) => p.replaceAll(" ", ""))
    .join(", ");
}

/** Whether version meets every comparator of rng (">=1.2.0, <2.0.0"). A pre-release sorts
 * before its release; two pre-releases compare as text. */
export function inRange(version: string, rng: string): boolean {
  for (const c of normaliseRange(rng).split(", ")) {
    const op = [">=", "<=", ">", "<", "="].find((o) => c.startsWith(o)) ?? "";
    const d = compare(version, c.slice(op.length));
    const ok = { ">=": d >= 0, ">": d > 0, "<=": d <= 0, "<": d < 0, "=": d === 0 }[op as ">="];
    if (!ok) return false;
  }
  return true;
}

function compare(a: string, b: string): number {
  const [ac = "", ...ap] = a.split("-");
  const [bc = "", ...bp] = b.split("-");
  const apre = ap.join("-");
  const bpre = bp.join("-");
  const as = ac.split(".");
  const bs = bc.split(".");
  for (let i = 0; i < 3; i++) {
    const x = Number.parseInt(as[i] ?? "0", 10) || 0;
    const y = Number.parseInt(bs[i] ?? "0", 10) || 0;
    if (x !== y) return x < y ? -1 : 1;
  }
  if (apre === bpre) return 0;
  if (apre === "") return 1;
  if (bpre === "") return -1;
  return apre < bpre ? -1 : 1;
}
