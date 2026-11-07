// C12, the flow check at verification (story 54.16 §1, README §4.12, story 54.39), with the
// store's catalog as the source of what a flow depends on. The check itself is the SDK's
// (tools/plugin-sdk/js/src/flow.ts, the same algorithm as the home's shared/plugins/
// pluginflow, on one corpus); this file only feeds it:
//
//   - a flow (flow.toml, byte-identical in every member package) goes through checkFlow:
//     its schema, each uploaded manifest matching its member entry exactly, every member not
//     uploaded already in the store at a version the flow accepts;
//   - a plugin alone (no flow.toml) is a flow of one: its manifest's graph;
//   - either way, the graph is checked again with every plugin it subscribes to, and
//     theirs, transitively, newest approved version first (walking upstream finds every
//     cycle through the flow): the leaf rule (no edge into a core stage or another plugin's
//     stage), events that exist, subjects that agree, no cycle (Tarjan's SCC, the path from
//     the smallest stage), the 16-hop cap. Payload schemas are C3's (the home's validator).
//
// Every refusal carries its code and the cycle path or the bad edge, in English and Arabic.

import {
  type Finding,
  type Graph,
  type Member,
  checkMembers,
  fromManifest,
  hasRefusal,
  inRange,
  message,
  parseFlow,
} from "../../tools/plugin-sdk/js/src/flow";
import { checkFlow } from "../checks/flow";
import type { Env } from "./env";

type Obj = Record<string, unknown>;

export interface C12 {
  ok: boolean;
  findings: (Finding & { en: string; ar: string })[];
  graph: Graph;
  /** the flow's published events (plugin:<id>/<event>) and subscriptions (from -> to) */
  events: string[];
  subscriptions: string[];
  members: string[];
}

/** The plugins a manifest or flow member subscribes to (plugin:<id>/<event> triggers). */
function publishers(m: Member): string[] {
  const out: string[] = [];
  for (const s of m.stages)
    for (const t of s.triggers) if (t.startsWith("plugin:")) out.push(t.slice("plugin:".length).split("/")[0] ?? "");
  return out.filter(Boolean);
}

/** The store's approved manifests by plugin, newest first, for the ids asked and every
 * plugin they subscribe to, transitively. */
async function upstream(env: Env, start: string[], skip: Set<string>): Promise<Map<string, { version: string; manifest: Obj }[]>> {
  const out = new Map<string, { version: string; manifest: Obj }[]>();
  let todo = [...new Set(start)].filter((id) => !skip.has(id));
  while (todo.length && out.size < 500) {
    const next: string[] = [];
    for (const id of todo) {
      if (out.has(id)) continue;
      const rows = await env.DB_CATALOG.prepare(
        "SELECT version, manifest FROM versions WHERE plugin = ? AND status = 'approved' ORDER BY released DESC",
      )
        .bind(id)
        .all<{ version: string; manifest: string }>();
      const list = rows.results.map((r) => ({ version: r.version, manifest: JSON.parse(r.manifest) as Obj }));
      out.set(id, list);
      if (list[0]) for (const p of publishers(fromManifest(list[0].manifest))) if (!skip.has(p) && !out.has(p)) next.push(p);
    }
    todo = next;
  }
  return out;
}

const key = (f: Finding) => JSON.stringify([f.code, f.severity, f.plugin ?? "", f.detail ?? "", f.path ?? []]);

function summary(graph: Graph): Pick<C12, "events" | "subscriptions"> {
  const display = (id: string) => (id.startsWith("event:") ? `plugin:${id.slice("event:".length)}` : id.replace(/^stage:/, ""));
  return {
    events: graph.nodes.filter((n) => n.kind === "event").map((n) => `plugin:${n.plugin}/${n.name}`),
    subscriptions: graph.edges.filter((e) => e.kind === "subscribe").map((e) => `${display(e.from)} -> ${display(e.to)}`),
  };
}

/**
 * C12 for one flow: flowToml (null for a plugin alone) and the manifests uploaded with it
 * (normalised, as the home's validator answers them).
 */
export async function checkFlowAtVerification(env: Env, flowToml: Uint8Array | null, uploaded: Obj[]): Promise<C12> {
  const own = uploaded.map((m) => fromManifest(m));
  const ownIds = new Set(own.map((m) => m.id));
  let findings: Finding[] = [];
  let flowMembers: Member[] = own;
  let memberIds = [...ownIds];

  // the store's side of the flow: members not uploaded, and everything upstream
  const flowIds: string[] = [];
  if (flowToml) for (const m of parseFlow(flowToml).flow?.members ?? []) flowIds.push(m.id, ...publishers(m));
  const store = await upstream(env, [...flowIds, ...own.flatMap(publishers)], ownIds);
  const newest = (id: string, range: string) => store.get(id)?.find((v) => inRange(v.version, range))?.manifest;

  let graph: Graph;
  if (flowToml) {
    const r = checkFlow(flowToml, uploaded, newest);
    findings = r.findings.map(({ en: _en, ar: _ar, ...f }) => f);
    graph = r.graph ?? { nodes: [], edges: [] };
    memberIds = [...new Set(graph.nodes.filter((n) => n.kind === "stage").map((n) => n.plugin))];
    flowMembers = memberIds.map((id) => own.find((m) => m.id === id) ?? fromManifest(store.get(id)?.[0]?.manifest ?? { id }));
  } else {
    graph = checkMembers(own, { strict: true }).graph;
  }

  // the flow with everything upstream of it, transitively: cycles through published flows
  const inGraph = new Set(flowMembers.map((m) => m.id));
  const extra: Member[] = [];
  for (const [id, versions] of store) if (!inGraph.has(id) && versions[0]) extra.push(fromManifest(versions[0].manifest));
  const whole = checkMembers([...flowMembers, ...extra], { strict: true });
  const seen = new Set(findings.map(key));
  for (const f of whole.findings) {
    // the flow's own findings are checkFlow's; here only what the wider graph adds (a cycle
    // through other flows), not complaints about plugins outside the flow
    const touches = (f.path ?? []).some((p) => [...inGraph].some((id) => p.includes(id))) || (f.plugin && inGraph.has(f.plugin));
    if (!touches || seen.has(key(f))) continue;
    if (!flowToml || f.code === "flow_cycle" || f.code === "flow_too_deep") {
      findings.push(f);
      seen.add(key(f));
    }
  }
  return {
    ok: !hasRefusal(findings),
    findings: findings.map((f) => ({ ...f, en: message(f, "en"), ar: message(f, "ar") })),
    graph,
    ...summary(graph),
    members: memberIds,
  };
}
