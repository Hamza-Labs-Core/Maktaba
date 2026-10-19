// Pure helpers behind the Machines page's static layout: a fixed order for
// the machines (never by a changing metric), node objects kept as they
// were when a poll brings the same data (so memoised cards skip the
// render), the per-machine "jobs running" count and the link to the
// Processing page filtered by machine.
import type { ClusterNode, ClusterView } from "../../lib/clusterApi";
import type { LiveProcess } from "../../lib/clusterStream";

/** The `host` the Processing page filters by for the home's own workers. */
export const HOME_HOST = "home";

/**
 * /queue?host=<node id | home>&name=<its name>: the Processing page showing
 * one machine's jobs (the name titles the page until the workers list
 * arrives; the home's needs none).
 */
export function queueHref(host: string, name?: string): string {
  const q = `/queue?host=${encodeURIComponent(host)}`;
  return name && host !== HOME_HOST ? `${q}&name=${encodeURIComponent(name)}` : q;
}

const STATE_ORDER: Record<string, number> = { pending: 0, approved: 1, revoked: 2, rejected: 3 };

/** By state group, then name, then id: a poll that reorders the API's list moves nothing. */
export function sortNodes(nodes: readonly ClusterNode[]): ClusterNode[] {
  return [...nodes].sort(
    (a, b) =>
      (STATE_ORDER[a.state] ?? 9) - (STATE_ORDER[b.state] ?? 9) ||
      a.name.localeCompare(b.name) ||
      a.id.localeCompare(b.id)
  );
}

function same(a: unknown, b: unknown): boolean {
  return a === b || JSON.stringify(a) === JSON.stringify(b);
}

/**
 * The new view with every unchanged piece replaced by the previous one, so
 * React.memo'd cards see the same object and do not re-render; the very
 * same view object when nothing changed at all.
 */
export function reconcileView(prev: ClusterView | null, next: ClusterView): ClusterView {
  if (!prev) return { ...next, nodes: sortNodes(next.nodes) };
  const old = new Map(prev.nodes.map((n) => [n.id, n]));
  const nodes = sortNodes(next.nodes).map((n) => {
    const o = old.get(n.id);
    return o && same(o, n) ? o : n;
  });
  const home = same(prev.home, next.home) ? prev.home : next.home;
  const unchanged =
    home === prev.home &&
    nodes.length === prev.nodes.length &&
    nodes.every((n, i) => n === prev.nodes[i]);
  return unchanged ? prev : { home, nodes };
}

/** The key a process's machine is counted under: HOME_HOST or the node id. */
export function machineKey(p: Pick<LiveProcess, "kind" | "node_id">): string | null {
  if (p.kind === "home") return HOME_HOST;
  return p.node_id ?? null;
}

/** Jobs running now per machine (online processes only), from the hub's metrics. */
export function jobCounts(processes: Record<string, LiveProcess>): Record<string, number> {
  const out: Record<string, number> = {};
  for (const p of Object.values(processes)) {
    const key = machineKey(p);
    if (!key) continue;
    out[key] = (out[key] ?? 0) + (p.online ? (p.metrics?.pipeline?.jobs?.length ?? 0) : 0);
  }
  return out;
}

/** A stable string for a counts record (the memo key: equal counts, same object). */
export function countsKey(counts: Record<string, number>): string {
  return Object.keys(counts)
    .sort()
    .map((k) => `${k}=${counts[k]}`)
    .join(",");
}
