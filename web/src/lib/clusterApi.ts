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
  /** story 31.3: the home's own storage check, per share */
  storage?: NodeStorage | null;
  /** the home's "share -> local mount" override; absent: its own mounts */
  path_map?: Record<string, string> | null;
  /** story 31.3: the automatic conversion of the stored paths to smb://server/share/... */
  canonicalize?: CanonStatus | null;
  /** Epic 35: the home's own web address (null: not known) */
  web_url?: string | null;
  /** Epic 35: the version the home runs */
  version?: string;
}

/** What the home's pipeline reports of the automatic path conversion. */
export interface CanonState {
  phase?:
    | "idle"
    | "done"
    | "blocked"
    | "failed"
    | "off"
    | "held"
    | "backing_up"
    | "pausing"
    | "applying"
    | "verifying"
    | "reverting";
  /** why it waits or failed: unmounted, missing, unresolved_mount, no_shares, error, ... */
  problems?: { code: string; detail?: string }[];
  checked_at?: string;
  held?: boolean;
  /** the run in progress (or cut short, resumed at the next look) */
  run?: { batch: string; done?: number; total?: number; started_at?: string } | null;
  last?: { batch: string; changes: number; finished_at?: string; backup?: string } | null;
  reverted?: { batch: string; rows: number; at: string } | null;
  plan?: {
    changes: number;
    collisions: number;
    unmatched: number;
    checked: number;
    missing: number;
  };
  servers_added?: { name: string; addresses: string[] }[];
  plex_added?: string[];
}

export type CanonAction = "run" | "revert" | "resume";

export interface CanonStatus {
  state: CanonState;
  request: CanonAction | null;
  requested_at: string | null;
  requested_by: string | null;
}

export type NodeState = "pending" | "approved" | "rejected" | "revoked";

/** One share a machine checked (story 31.3): reachable, writable, through which mount. */
export interface ShareCheck {
  /** canonical share, smb://nas/Originals */
  share: string;
  /** this machine's mount of it; null: not mounted here */
  local: string | null;
  source: string | null;
  read: "ok" | "error" | string;
  write: "ok" | "error" | "skipped" | string;
  detail: string | null;
}

export interface NodeStorage {
  read: "ok" | "error" | string;
  write: "ok" | "error" | "skipped" | string;
  checked_at: string | null;
  detail: string | null;
  shares?: ShareCheck[];
  /** the whole map the machine resolves with: share (or legacy prefix) -> local */
  path_map?: Record<string, string>;
}

/** A storage server (story 31.3): what smb://<name>/... names, and its addresses. */
export interface StorageServer {
  id: string;
  name: string;
  addresses: string[];
  /** none | fullwidth | sfm | 0x22:0xff02,... */
  char_map: string;
  exports: Record<string, string>;
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
  /** Epic 35: the version it runs (null: not reported yet) */
  version?: string | null;
  /** Epic 35: "windows-amd64", "darwin-arm64", ... */
  platform?: string | null;
  /** Epic 35 (story 35.11): its own web address */
  web_url?: string | null;
  /** Epic 35 (story 35.8): a full machine (web, API, streaming); false: a worker only */
  full_node?: boolean;
  /** Epic 35 (design §11.5): VOD served by another machine when the share is not readable here */
  vod?: NodeVod;
  /** Epic 35 (story 35.12): the update rollout's view of this machine */
  update_state?: Record<string, unknown>;
  /** Epic 37: the machine's copy of this home's database; {} before it reported one */
  copy?: NodeCopy;
}

/** A full machine's copy of its home's database (Epic 37), as its supervisor keeps it. */
export interface NodeCopy {
  state?: NodeCopyState;
  detail?: string;
  /** the home's time of the last change the copy applied */
  applied_at?: string;
  lag_ms?: number;
  built?: string;
  home_major?: string;
  checked?: string;
}

export type NodeCopyState =
  | "preparing"
  | "waiting"
  | "copying"
  | "rebuilding"
  | "following"
  | "behind"
  | "starting"
  | "incompatible"
  | "unavailable"
  | "error"
  | "revoked";

/** A machine's VOD fallback (design §11.5); {} when it serves its own. */
export interface NodeVod {
  fallback?: boolean;
  served_by?: string;
  since?: string;
  detail?: string;
  checked_at?: string;
}

/** GET /api/cluster/self (public, every machine): who this machine is and who its home is. */
export interface ClusterSelf {
  role: "home" | "node";
  id: string;
  name?: string;
  version: string;
  schema_rev: number;
  web_url?: string;
  home?: {
    instance_id?: string;
    name?: string;
    web_url?: string;
    version?: string;
    reachable: boolean;
    failing_since?: string;
  };
  /** the schema guard's reason; non-empty: this node is paused */
  paused?: string;
  /** cluster sign-in is on */
  sso: boolean;
  agent_connected?: boolean;
  vod?: NodeVod & { checked_at: string };
  /** a full node's copy of its home's database (Epic 37) */
  copy?: NodeCopy;
  machines?: { role: "home" | "node"; id: string; name: string; web_url?: string }[];
  accept_joins?: boolean;
  served_at: string;
}

export interface ClusterView {
  home: ClusterHome;
  nodes: ClusterNode[];
  /** the cluster link (story 35.16); absent on a home without a cluster CA */
  link?: ClusterLink;
}

/** The cluster link: its mode, the CA, each machine's link and certificate. */
export interface ClusterLink {
  mode: "off" | "prefer" | "require";
  auto_require: boolean;
  ca_sha256: string;
  ca_url: string;
  web_https: boolean;
  /** the approved machines that held their link for a day, of all of them */
  ready: number;
  machines: number;
  nodes?: Record<
    string,
    { connected: boolean; since?: string; cert_not_after?: string; has_cert: boolean }
  >;
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
  getSelf: () => api.get<ClusterSelf>("/api/cluster/self"),
  putSettings: (body: { accept_joins?: boolean; name?: string }) =>
    api.put<ClusterHome>("/api/cluster/settings", body),
  approve: (id: string, code: string) =>
    api.post<ClusterNode>(`/api/cluster/nodes/${q(id)}/approve`, { code }),
  reject: (id: string) => api.post<ClusterNode>(`/api/cluster/nodes/${q(id)}/reject`),
  revoke: (id: string) => api.post<ClusterNode>(`/api/cluster/nodes/${q(id)}/revoke`),
  updateNode: (id: string, body: NodeUpdate) =>
    api.put<ClusterNode>(`/api/cluster/nodes/${q(id)}`, body),
  removeNode: (id: string) => api.delete<void>(`/api/cluster/nodes/${q(id)}`),
  setHomePathMap: (path_map: Record<string, string> | null) =>
    api.put<ClusterHome>("/api/cluster/home/path-map", { path_map }),
  listServers: () => api.get<{ servers: StorageServer[] }>("/api/cluster/storage-servers"),
  putServer: (
    name: string,
    body: { addresses: string[]; char_map?: string; exports?: Record<string, string> }
  ) => api.put<StorageServer>(`/api/cluster/storage-servers/${q(name)}`, body),
  deleteServer: (name: string) => api.delete<void>(`/api/cluster/storage-servers/${q(name)}`),
  canonicalize: (action: CanonAction) =>
    api.post<CanonStatus>("/api/cluster/storage/canonicalize", { action }),
  // Epic 37: switching the home to another machine
  switchover: () => api.get<SwitchStatus>("/api/cluster/switchover"),
  startSwitch: (to_node: string) =>
    api.post<{ id: string }>("/api/cluster/switchover", { to_node }),
  /** Calls a switch under way off; `force` with the machine's name typed when it does
   * not answer (409 switchover-no-answer). */
  cancelSwitch: (id: string, force = false, confirm = "") =>
    api.post<{ state: "calling_off" | "called_off" }>(`/api/cluster/switchover/${q(id)}/cancel`, {
      force,
      confirm,
    }),
  setStandby: (node_id: string, standby: boolean) =>
    api.put<{ standbys: StandbyHome[] }>("/api/cluster/switchover/standby", { node_id, standby }),
  checklistDone: (id: string, item: string, done: boolean) =>
    api.post<{ done: boolean }>(`/api/cluster/switchover/${q(id)}/checklist`, { item, done }),
  keptPlan: (id: string) => api.get<KeptPlan>(`/api/cluster/kept-aside/${q(id)}/plan`),
  keptApply: (id: string, plan: string) =>
    api.post<{ plan: KeptPlan; changed: boolean }>(`/api/cluster/kept-aside/${q(id)}/apply`, {
      plan,
    }),
  keptDismiss: (id: string) =>
    api.post<{ dismissed: boolean }>(`/api/cluster/kept-aside/${q(id)}/dismiss`, {}),
  keptDownloadUrl: (id: string) => `/api/cluster/kept-aside/${q(id)}/download`,
  // a standby home taking over when its home is gone (this machine's own)
  emergency: () => api.get<EmergencyState>("/api/cluster/emergency"),
  startEmergency: (confirm: string) =>
    api.post<{ accepted: boolean; id: string }>("/api/cluster/emergency", { confirm }),
};

/** One step of a switch of the home, as the old home runs it. */
export interface SwitchStep {
  name: string;
  state: "running" | "done" | "failed";
  detail?: string;
  at: string;
}

/** A machine's part in a switch. */
export interface SwitchMachine {
  name: string;
  state: string;
  detail?: string;
}

/** An outside service after a switch: moved and checked, or what it needs (done:
 * a person said they did it). */
export interface SwitchIntegration {
  state: "moved" | "pending" | "failed" | "needs_you" | "not_used";
  detail?: string;
  done?: boolean;
}

/** A switch of the home (GET /api/cluster/switchover). */
export interface SwitchProgress {
  id: string;
  term: number;
  kind: "planned" | "emergency";
  to_node: string;
  to_name: string;
  from_name: string;
  state: string;
  detail?: string;
  steps?: SwitchStep[] | null;
  machines?: Record<string, SwitchMachine> | null;
  integrations?: Record<string, SwitchIntegration> | null;
  started?: string;
  /** the promotion was asked: from then only the machine taking over can call it off */
  promote_sent?: boolean;
  /** where the new home answers, once it is the home */
  new_home?: { api_url: string; web_url: string; name: string } | null;
}

/** A machine that could take over as the home, and why it cannot now. */
export interface SwitchCandidate {
  node_id: string;
  name: string;
  ready: boolean;
  reasons?: string[] | null;
  lag_ms?: number | null;
}

export interface SwitchStatus {
  term: { term: number; home_name: string; reason: string; started_at: string } | null;
  current: SwitchProgress | null;
  recent: SwitchProgress[] | null;
  candidates: SwitchCandidate[] | null;
  standbys?: StandbyHome[] | null;
  reports?: KeptReport[] | null;
}

/** A machine as a standby home: it may take over if the home is gone for good, and
 * keeps the home's kit (its secrets and keys) sealed to it. */
export interface StandbyHome {
  node_id: string;
  name: string;
  standby: boolean;
  /** an approved full machine: it may be one */
  can: boolean;
  /** when it last confirmed the kit it holds */
  kit_at?: string | null;
  kit_term?: number | null;
}

/** What an old home wrote after an emergency took the home from it, as it reported it. */
export interface KeptReport {
  id: string;
  term: number;
  from_name: string;
  state: "reported" | "applied" | "dismissed";
  lost: boolean;
  received_at: string;
  applied_at?: string | null;
  changes: number;
  transactions: number;
  counts?: Record<string, number> | null;
  applied_transactions?: number | null;
  /** the old home wrote more than a report carries: `total` changes, `changes` of them here */
  truncated?: boolean;
  total?: number;
}

/** One of the old home's transactions: applied whole, or not, with why. */
export interface KeptTransaction {
  commit_lsn: string;
  committed_at: string;
  tables: string[];
  changes: number;
  apply: boolean;
  reason?: string;
  detail?: string;
}

/** What applying a report would do now. */
export interface KeptPlan {
  report: string;
  hash: string;
  transactions: KeptTransaction[] | null;
  apply: number;
  skip: number;
  unknown?: boolean;
}

/** Whether this machine (a standby home) can take over as the home now. */
export interface EmergencyState {
  standby: boolean;
  kit_term?: number;
  kit_made_at?: string | null;
  home: string;
  home_url?: string;
  home_answers?: boolean;
  copy?: NodeCopy | null;
  ready: boolean;
  reasons?: string[] | null;
}

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

/** Days on record per kind (YYYY-MM-DD). */
export interface FileDates {
  logs?: string[];
  metrics?: string[];
  llm?: string[];
  events?: string[];
  jobs?: string[];
  queues?: string[];
  library?: string[];
}

export interface FileNode {
  ip: string;
  processes: FileProcess[];
  /** the days the share holds */
  dates: FileDates;
  /** the days the home's long-term archive holds (design-agent.md §7.10); the
   * history endpoints read them from the archive once the share has let them go */
  archived_dates?: FileDates;
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
    api.get<{
      records: (JobEvent & { worker_id?: string })[];
      next_before: string | number | null;
    }>(`/api/cluster/files/${q(ip)}/jobs${query(p)}`),
  queues: (ip: string, p: { date?: string; from?: string; to?: string } = {}) =>
    api.get<{ samples: { ts: string | number; snapshot: QueuesSnapshot }[] }>(
      `/api/cluster/files/${q(ip)}/queues${query(p)}`
    ),
  library: (ip: string, p: { date?: string } = {}) =>
    api.get<{ samples: LibrarySample[] }>(`/api/cluster/files/${q(ip)}/library${query(p)}`),
  cmd: (session: string, name: ProcessCommand, args: Record<string, unknown> = {}) =>
    api.post<CommandResult>(`/api/cluster/processes/${q(session)}/cmd`, { name, args }),
  relay: () => api.get<RelayStatus>("/api/cluster/relay"),
};

/** The extension relay's settings (rules section "relay"). */
export interface RelaySettings {
  enabled: boolean;
  requests_per_minute: number;
  burst: number;
  max_in_flight: number;
  max_response_mb: number;
  timeout_sec: number;
}

/** One relayed call as the home audited it (never the token, never the query). */
export interface RelayCall {
  ts: string;
  kind: string;
  node_id?: string;
  node_name?: string;
  worker_id?: string;
  ip?: string;
  ext: string;
  method: string;
  path: string;
  status: number;
  bytes: number;
  ms: number;
  error?: string;
  scrubbed?: number;
}

/** GET /api/cluster/relay: the home's service door (story 31.R). */
export interface RelayStatus {
  available: boolean;
  enabled: boolean;
  settings: RelaySettings;
  extensions: {
    name: string;
    configured: boolean;
    enabled: boolean;
    upstream?: string;
    reason?: string;
    allow: { method: string; path: string; use: string }[];
  }[];
  home_only: { name: string; reason: string }[];
  calls: RelayCall[];
  totals: { caller: string; calls: number; refused: number; failed: number; bytes: number }[];
}

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
