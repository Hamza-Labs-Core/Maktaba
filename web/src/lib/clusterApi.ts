// Typed client for the multi-instance admin API (Epic 31, design §8):
// this home and the machines that joined it or ask to.
import { api, ApiError } from "./api";
import type { QueuesSnapshot } from "./queueApi";

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

// ---------------------------------------------------------------- phase 2
// The cluster channel (design-agent.md §3–§6): live processes, their
// metrics and models, the files they write on the share, commands.

export interface DiskMetric {
  path: string;
  total: number;
  free: number;
}

export interface NvidiaGpu {
  name: string;
  util_pct: number;
  mem_used: number;
  mem_total: number;
  temp_c: number;
}

export interface PipelineJob {
  job_id: number | string;
  stage: string;
  subject: string;
  since: string;
  /** a fraction (0..1) or a percentage; see jobProgress */
  progress: number | null;
}

/** Where a process saves its files (§4): on the share, or locally and why. */
export interface FilesTarget {
  dir: string;
  on_share: boolean;
  reason: string | null;
}

/** One metrics frame (§3). Any sub-object may be missing. */
export interface ClusterMetrics {
  ts?: string | number;
  system?: {
    cpu_pct?: number;
    load1?: number;
    mem_total?: number;
    mem_available?: number;
    swap_used?: number;
    disks?: DiskMetric[];
    net?: { rx_bps?: number; tx_bps?: number };
  };
  gpu?: {
    nvidia?: NvidiaGpu[];
    apple?: { active_gb?: number; cache_gb?: number; peak_gb?: number } | null;
  };
  process?: { rss?: number; cpu_pct?: number; threads?: number; uptime_s?: number };
  pipeline?: {
    role?: string;
    stages?: string[];
    stages_off?: Record<string, string>;
    jobs?: PipelineJob[];
    claims_per_min?: number;
    stt?: { audio_sec_per_s?: number; files_in_flight?: number };
  };
  llm?: { in_flight?: number; tokens_per_s?: number };
  files?: FilesTarget;
}

export interface LlmModel {
  name: string;
  size: number | null;
  family: string | null;
  quant: string | null;
  loaded: boolean;
}

export interface LlmRuntime {
  provider: string;
  base_url: string;
  models: LlmModel[];
  max_parallel: number;
}

export interface ClusterProcess {
  session: string;
  kind: "home" | "node";
  node_id: string | null;
  node_name: string | null;
  ip: string;
  host: string;
  worker_id: string;
  pid: number;
  connected_at: string;
  metrics: ClusterMetrics | null;
  llm: { runtimes: LlmRuntime[] } | null;
}

/** A job event (§3 `job`). */
export interface JobEvent {
  event: "claimed" | "progress" | "done" | "failed" | "skipped" | "paused" | "deferred";
  job_id: number | string;
  stage: string;
  subject_kind: string;
  subject_id: string;
  title: string | null;
  attempt: number;
  started_at: string | null;
  ts: string | number;
  duration_ms: number | null;
  progress: { step: string; done: number; total: number } | null;
  outcome: { kind: string; retryable: boolean; error: string | null } | null;
  output: Record<string, unknown> | null;
}

export interface LibraryStats {
  library_id?: string | number;
  name?: string;
  videos?: number;
  hours?: number;
  bytes?: number;
  transcribed_videos?: number;
  transcribed_hours?: number;
  series?: number;
  seasons?: number;
  tagged?: number;
  indexed?: number;
}

export interface LibrarySample {
  ts: string | number;
  libraries: LibraryStats[];
  totals: LibraryStats | null;
}

export type LogLevel = "debug" | "info" | "warning" | "error";
export const LOG_LEVELS: LogLevel[] = ["debug", "info", "warning", "error"];

export interface ClusterLogRecord {
  ts: string | number;
  level: string;
  event: string;
  logger?: string;
  fields?: Record<string, unknown>;
  worker_id?: string;
}

export interface FileProcess {
  worker_id: string;
  node_id: string | null;
  name: string | null;
  host: string;
  os: string | null;
  pid: number | null;
  role: string | null;
  started_at: string | null;
}

export interface FileNode {
  ip: string;
  processes: FileProcess[];
  dates: {
    logs?: string[];
    metrics?: string[];
    llm?: string[];
    events?: string[];
    jobs?: string[];
    queues?: string[];
    library?: string[];
  };
}

export interface ClusterFiles {
  files: FilesTarget | null;
  nodes: FileNode[];
}

export interface LogPage {
  records: ClusterLogRecord[];
  next_before: string | number | null;
}

export interface LogQuery {
  date?: string;
  level?: LogLevel;
  q?: string;
  limit?: number;
  before?: string | number | null;
}

export type ProcessCommand = "drain" | "undrain" | "reload_stages" | "log_level" | "disconnect";

export interface CommandResult {
  ok: boolean;
  detail: string | null;
}

function query(params: Record<string, string | number | null | undefined>): string {
  const parts = Object.entries(params)
    .filter(([, v]) => v !== undefined && v !== null && v !== "")
    .map(([k, v]) => `${q(k)}=${q(String(v))}`);
  return parts.length ? `?${parts.join("&")}` : "";
}

export const clusterChannelApi = {
  files: () => api.get<ClusterFiles>("/api/cluster/files"),
  logs: (ip: string, p: LogQuery = {}) =>
    api.get<LogPage>(
      `/api/cluster/files/${q(ip)}/logs${query({
        date: p.date,
        level: p.level,
        q: p.q,
        limit: p.limit,
        before: p.before,
      })}`
    ),
  metrics: (ip: string, p: { date?: string; from?: string; to?: string } = {}) =>
    api.get<{ samples: { ts: string | number; worker_id: string; metrics: ClusterMetrics }[] }>(
      `/api/cluster/files/${q(ip)}/metrics${query(p)}`
    ),
  jobs: (
    ip: string,
    p: { date?: string; stage?: string; event?: string; limit?: number; before?: string | number }
  ) =>
    api.get<{ records: (JobEvent & { worker_id?: string })[]; next_before: string | number | null }>(
      `/api/cluster/files/${q(ip)}/jobs${query(p)}`
    ),
  queues: (ip: string, p: { date?: string; from?: string; to?: string } = {}) =>
    api.get<{ samples: { ts: string | number; snapshot: QueuesSnapshot }[] }>(
      `/api/cluster/files/${q(ip)}/queues${query(p)}`
    ),
  library: (ip: string, p: { date?: string } = {}) =>
    api.get<{ samples: LibrarySample[] }>(`/api/cluster/files/${q(ip)}/library${query(p)}`),
  cmd: (session: string, name: ProcessCommand, args: Record<string, unknown> = {}) =>
    api.post<CommandResult>(`/api/cluster/processes/${q(session)}/cmd`, { name, args }),
};

/** A timestamp (ISO string, epoch seconds or epoch ms) as epoch ms; NaN when unreadable. */
export function tsMs(ts: string | number | null | undefined): number {
  if (typeof ts === "number") return ts < 1e12 ? ts * 1000 : ts;
  if (typeof ts === "string") {
    const asNum = Number(ts);
    if (ts.trim() !== "" && Number.isFinite(asNum)) return tsMs(asNum);
    return Date.parse(ts);
  }
  return NaN;
}

/** Job progress as 0..100, accepting either a fraction or a percentage. */
export function jobProgress(p: number | null | undefined): number | null {
  if (typeof p !== "number" || !Number.isFinite(p)) return null;
  const pct = p <= 1 ? p * 100 : p;
  return Math.max(0, Math.min(100, pct));
}
