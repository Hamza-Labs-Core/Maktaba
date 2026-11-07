// C12, the flow check of the store's verification (Epic 54, README §4.12, story 54.16 §1;
// the check itself is story 54.39's, shared with the home through the SDK's checker).
//
// A submission is one flow.toml plus the manifests of the members uploaded with it. Every
// member not in the upload, and every plugin the flow depends on, must already be in the
// store at a version the flow accepts: `catalog` answers the newest compatible manifest
// the store has. The flow is refused as a whole on any refusal, with the code and the
// cycle path or the bad edge in English and Arabic; nothing in it is countersigned.

import {
  checkMembers,
  fromManifest,
  hasRefusal,
  matchManifest,
  message,
  parseFlow,
  resolveDepends,
  type Finding,
  type Graph,
  type Member,
} from "../../tools/plugin-sdk/js/src/flow.js";

export interface FlowReport {
  ok: boolean;
  findings: (Finding & { en: string; ar: string })[];
  /** the normalised graph review draws (54.41) */
  graph?: Graph;
}

/** The newest manifest the store has for a plugin id inside a version range, normalised. */
export type Catalog = (pluginId: string, range: string) => Record<string, unknown> | undefined;

export function checkFlow(
  flowToml: Uint8Array | string,
  uploaded: Record<string, unknown>[],
  catalog: Catalog,
): FlowReport {
  const { flow, findings } = parseFlow(flowToml);
  const all: Finding[] = [...findings];
  let graph: Graph | undefined;
  if (flow) {
    const members: Member[] = [...flow.members];
    const inUpload = new Set<string>();
    for (const m of uploaded) {
      inUpload.add(String(m.id));
      all.push(...matchManifest(flow, m));
    }
    // a member not uploaded must be in the store at a version its range accepts
    for (const m of flow.members) {
      if (!inUpload.has(m.id) && !catalog(m.id, m.version)) {
        all.push({ code: "flow_missing_plugin", severity: "refuse", plugin: flow.id, detail: `${m.id} ${m.version}` });
      }
    }
    // the plugins the flow subscribes to outside itself, from the store, and the ones
    // those subscribe to, all the way: a loop or a missing plugin further away is refused
    const resolved = resolveDepends(members, (id, range) => {
      const found = catalog(id, range || ">=0.0.0");
      return found ? fromManifest(found) : undefined;
    });
    const checked = checkMembers(resolved, { strict: true });
    graph = checked.graph;
    all.push(...checked.findings);
  }
  return {
    ok: !hasRefusal(all),
    findings: all.map((f) => ({ ...f, en: message(f, "en"), ar: message(f, "ar") })),
    graph,
  };
}
