// C12, the flow check at verification (story 54.16 §1, README §4.12, story 54.39), with the
// store's catalog as the source of what a flow depends on. The check is the shared one:
// checks/flow.ts's checkFlow for a flow, and for a plugin alone (a flow of one) the SDK's
// resolveDepends and checkMembers, the same algorithm as the home's pluginflow, on one
// corpus. Both walk upstream transitively (every plugin subscribed to, and theirs) and check
// the schema, each manifest against its member, the leaf rule, events and subjects, members
// and upstreams in the store at an accepted version, cycles (Tarjan's SCC) and the 16-hop cap.
// Payload schemas are C3's (the home's validator).
//
// This file only feeds the checker: its lookup is synchronous, the store's manifests are in
// D1, so the checker runs, says which plugins it asked for that are not loaded yet, and runs
// again once they are, until it asks for nothing new.

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
  resolveDepends,
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

/** The store's approved manifests of a plugin, newest first. */
async function approved(env: Env, id: string): Promise<{ version: string; manifest: Obj }[]> {
  const rows = await env.DB_CATALOG.prepare("SELECT version, manifest FROM versions WHERE plugin = ? AND status = 'approved' ORDER BY released DESC")
    .bind(id)
    .all<{ version: string; manifest: string }>();
  return rows.results.map((r) => ({ version: r.version, manifest: JSON.parse(r.manifest) as Obj }));
}

/** The flow's own part of the graph (the checker's also holds the upstreams it pulled in). */
function summary(graph: Graph, members: string[]): Pick<C12, "events" | "subscriptions" | "members"> {
  const display = (id: string) => (id.startsWith("event:") ? `plugin:${id.slice("event:".length)}` : id.replace(/^stage:/, ""));
  const ours = new Set(members);
  const pluginOf = (id: string) => graph.nodes.find((n) => n.id === id)?.plugin ?? "";
  return {
    events: graph.nodes.filter((n) => n.kind === "event" && ours.has(n.plugin)).map((n) => `plugin:${n.plugin}/${n.name}`),
    subscriptions: graph.edges
      .filter((e) => e.kind === "subscribe" && ours.has(pluginOf(e.to)))
      .map((e) => `${display(e.from)} -> ${display(e.to)}`),
    members,
  };
}

/**
 * C12 for one flow: flowToml (null for a plugin alone) and the manifests uploaded with it
 * (normalised, as the home's validator answers them).
 */
export async function checkFlowAtVerification(env: Env, flowToml: Uint8Array | null, uploaded: Obj[]): Promise<C12> {
  const own = new Set(uploaded.map((m) => String(m.id)));
  const store = new Map<string, { version: string; manifest: Obj }[]>();
  const newest = (id: string, range: string) => store.get(id)?.find((v) => !range || inRange(v.version, range))?.manifest;

  let findings: Finding[] = [];
  let graph: Graph = { nodes: [], edges: [] };
  for (let round = 0; round < 64; round++) {
    const missing = new Set<string>();
    const lookup = (id: string, range: string) => {
      if (own.has(id)) return undefined;
      if (!store.has(id)) missing.add(id);
      return newest(id, range);
    };
    if (flowToml) {
      const r = checkFlow(flowToml, uploaded, lookup);
      findings = r.findings.map(({ en: _en, ar: _ar, ...f }) => f);
      graph = r.graph ?? { nodes: [], edges: [] };
    } else {
      const members: Member[] = uploaded.map((m) => fromManifest(m));
      const resolved = resolveDepends(members, (id, range) => {
        const m = lookup(id, range);
        return m ? fromManifest(m) : undefined;
      });
      const r = checkMembers(resolved, { strict: true });
      findings = r.findings;
      graph = r.graph;
    }
    if (missing.size === 0) break;
    for (const id of missing) store.set(id, await approved(env, id));
  }
  return {
    ok: !hasRefusal(findings),
    findings: findings.map((f) => ({ ...f, en: message(f, "en"), ar: message(f, "ar") })),
    graph,
    ...summary(graph, flowToml ? (parseFlow(flowToml).flow?.members ?? []).map((m) => m.id) : [...own]),
  };
}
