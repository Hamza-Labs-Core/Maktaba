// Names the cluster views show: a process's machine, a queue's label.
import type { LiveProcess } from "../../lib/clusterStream";
import type { QueuesSnapshot } from "../../lib/queueApi";

/** The machine a process runs on: the node's name, else its host, else its IP. */
export function machineName(p: Pick<LiveProcess, "node_name" | "host" | "ip"> | undefined): string {
  if (!p) return "";
  return p.node_name || p.host || p.ip;
}

/** "Studio Mac · worker-2" */
export function processName(p: LiveProcess | undefined): string {
  if (!p) return "";
  return `${machineName(p)} · ${p.worker_id}`;
}

/** Where a job or request ran: the process's machine, else the IP. */
export function whereName(
  processes: Record<string, LiveProcess>,
  session: string | null | undefined,
  ip?: string | null
): string {
  const p = session ? processes[session] : undefined;
  return p ? machineName(p) : (ip ?? session ?? "");
}

/** The queue's label from the Processing page's snapshot, else the stage id. */
export function queueLabeller(
  snapshot: QueuesSnapshot | null | undefined
): (stage: string) => string {
  const m = new Map((snapshot?.stages ?? []).map((s) => [s.stage, s.label]));
  return (stage) => m.get(stage) ?? stage;
}
