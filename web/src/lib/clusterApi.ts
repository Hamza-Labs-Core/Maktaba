// Typed client for the multi-instance admin API (Epic 31, design §8):
// this home and the machines that joined it or ask to.
import { api, ApiError } from "./api";

export interface ClusterHome {
  instance_id: string;
  name: string;
  accept_joins: boolean;
  accept_joins_until: string | null;
  /** false on a home that cannot take machines (e.g. SQLite) */
  roles_supported: boolean;
  roles_reason: string | null;
}

export type NodeState = "pending" | "approved" | "rejected" | "revoked";

export interface NodeStorage {
  read: "ok" | "error" | string;
  write: "ok" | "error" | "skipped" | string;
  checked_at: string | null;
  detail: string | null;
}

export interface NodeWorker {
  id: string;
  host: string;
  last_seen_at: string | null;
  /** the stages this worker claims */
  stages: string[];
  /** {stage: reason} for the stages it does not claim */
  stages_off: Record<string, string>;
}

export interface ClusterNode {
  id: string;
  name: string;
  state: NodeState;
  os: string;
  arch: string;
  versions: Record<string, unknown>;
  capabilities: Record<string, unknown>;
  storage: NodeStorage | null;
  /** db prefix -> local prefix; null until worked out */
  path_map: Record<string, string> | null;
  path_map_source: "inferred" | "admin" | null;
  drain: boolean;
  expires_at: string | null;
  created_at: string;
  approved_at: string | null;
  revoked_at: string | null;
  last_seen_at: string | null;
  online: boolean;
  workers: NodeWorker[];
  /** the admin's switches; an absent stage is automatic */
  stage_overrides: Record<string, boolean>;
}

export interface ClusterView {
  home: ClusterHome;
  nodes: ClusterNode[];
}

export interface NodeUpdate {
  name?: string;
  drain?: boolean;
  /** null: back to the map the machine works out itself */
  path_map?: Record<string, string> | null;
  /** true / false: switched on / off; null: automatic */
  stages?: Record<string, boolean | null>;
}

const q = encodeURIComponent;

export const clusterApi = {
  getCluster: () => api.get<ClusterView>("/api/cluster"),
  putSettings: (body: { accept_joins?: boolean; name?: string }) =>
    api.put<ClusterHome>("/api/cluster/settings", body),
  approve: (id: string, code: string) =>
    api.post<ClusterNode>(`/api/cluster/nodes/${q(id)}/approve`, { code }),
  reject: (id: string) => api.post<ClusterNode>(`/api/cluster/nodes/${q(id)}/reject`),
  revoke: (id: string) => api.post<ClusterNode>(`/api/cluster/nodes/${q(id)}/revoke`),
  updateNode: (id: string, body: NodeUpdate) =>
    api.put<ClusterNode>(`/api/cluster/nodes/${q(id)}`, body),
  removeNode: (id: string) => api.delete<void>(`/api/cluster/nodes/${q(id)}`),
};

/** "123 456", "123-456" -> "123456"; anything else -> null. */
export function normalizePairingCode(raw: string): string | null {
  const digits = raw.replace(/[\s-]/g, "");
  return /^\d{6}$/.test(digits) ? digits : null;
}

export type ApproveFailure =
  | { kind: "wrong_code"; attemptsLeft: number | null }
  | { kind: "expired" }
  | { kind: "other"; message: string };

function problemCode(e: ApiError): string {
  const p = e.problem as unknown as { code?: unknown; type?: unknown };
  if (typeof p.code === "string") return p.code;
  // a type URI such as ".../problems/wrong_code" names the code too
  const type = typeof p.type === "string" ? p.type : "";
  return type.split(/[/#:]/).pop() ?? "";
}

/** Reads an approve error: a wrong code (with the tries left) or an expired request. */
export function approveFailure(e: unknown): ApproveFailure {
  if (e instanceof ApiError) {
    const code = problemCode(e);
    if (code === "wrong_code") {
      const left = (e.problem as unknown as { attempts_left?: unknown }).attempts_left;
      return { kind: "wrong_code", attemptsLeft: typeof left === "number" ? left : null };
    }
    if (code === "expired" || e.status === 410) return { kind: "expired" };
    return { kind: "other", message: e.problem.detail || e.problem.title };
  }
  return { kind: "other", message: e instanceof Error ? e.message : String(e) };
}
