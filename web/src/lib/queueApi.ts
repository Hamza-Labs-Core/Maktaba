// Typed client for the queue graph API (plan 06-11 §8). The types are a
// verbatim copy of §8.2 / §7.2 so the UI is developed against the
// contract; the Go side (workstream B) is built in parallel.
import { api } from "./api";

export type SubjectKind = "video" | "library" | "series" | "season" | "catalog";
export type JobState =
  | "pending"
  | "claimed"
  | "running"
  | "paused"
  | "resuming"
  | "done"
  | "failed"
  | "cancelled";
export type Lane = "sources" | "episodes" | "speech" | "seasons" | "channels" | "media";

export interface JobRef {
  id: number;
  stage: string;
  state: JobState;
  subject_kind: SubjectKind;
  subject_id: string;
  subject_label: string;
  library_id: string | null;
  priority: number;
  attempts: number;
  max_attempts: number;
  outcome: string | null;
  error_cause: string | null;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
  progress: {
    step: string;
    steps: string[];
    done_units: number;
    total_units: number;
    message?: string;
  } | null;
}

export interface Pool {
  pool: string;
  label: string;
  scope: "global" | "per_worker";
  size: number | null;
  effective_size: number;
  busy: number;
  stages: string[];
  updated_at: string;
  updated_by: string | null;
}

export interface StageEdge {
  to: string;
  map: string;
  when: string;
  legacy: boolean;
}

export interface StageInfo {
  stage: string;
  label: string;
  lane: Lane;
  lane_order: number;
  subject_kinds: string[];
  pool: string;
  version: number;
  legacy: boolean;
  hidden: boolean;
  external_write: boolean;
  write_mode: "auto" | "review";
  enabled: boolean;
  disabled_reason: string | null;
  paused: boolean;
  paused_at: string | null;
  paused_by: string | null;
  debounce_sec: number;
  edges: StageEdge[];
  may_request: string[];
  entry_for: string[];
  fed_by: string[];
  /** Extension that owns this queue (e.g. "plex", "dizquetv"); null/absent = core. */
  extension?: string | null;
  graph_synced_at: string | null;
}

export type WaitingReason =
  | "paused"
  | "paused_all"
  | "pool_full"
  | "debouncing"
  | "backoff"
  | "service_retrying"
  | "upstream"
  | "disabled"
  | "not_configured"
  | "awaiting_review"
  | "no_worker"
  | "idle"
  | null;

export interface QueueSummary {
  stage: string;
  counts: {
    pending: number;
    ready: number;
    running: number;
    paused: number;
    failed_open: number;
    done_24h: number;
    skipped_24h: number;
    awaiting_review: number;
  };
  wave: { since: string | null; done: number; total: number };
  rate_per_min: number;
  eta_sec: number | null;
  oldest_waiting_sec: number | null;
  waiting_reason: WaitingReason;
  running_jobs: JobRef[];
  /** Hosts currently able to run this queue (GET /api/workers is the source). */
  served_by?: string[];
  /** true when work is waiting but no live worker serves this stage. */
  no_worker?: boolean;
  /** External service ("plex", "dizquetv") whose failures put waiting rows into backoff. */
  retrying_service?: string | null;
}

export interface WorkerHost {
  worker_id: string;
  /** Short host name, e.g. "This PC" or "Mac". */
  host: string;
  online: boolean;
  last_seen_at: string | null;
  /** Stages this worker serves. */
  serves: string[];
  /** Human summary, e.g. "NAS, detection, Plex". */
  serves_label: string;
  /** Epic 31: the joined machine this worker runs on (null / absent: the home). */
  node_id?: string | null;
  node_name?: string | null;
  role?: "home" | "node" | null;
}

export interface ExtensionSettingField {
  key: string;
  label: string;
  type: "text" | "url" | "secret" | "number" | "boolean" | "select";
  required?: boolean;
  help?: string;
  options?: { value: string; label: string }[];
}

/** GET /api/extensions. Secret values are never returned; secrets_set says which are stored. */
export interface Extension {
  name: string;
  label: string;
  description: string;
  version: string;
  enabled: boolean;
  configured: boolean;
  settings_schema: ExtensionSettingField[];
  settings: Record<string, string | number | boolean | null>;
  secrets_set: string[];
}

export interface Flow {
  from: string;
  to: string;
  per_min: number;
}

export interface HealthNow {
  nas: {
    reads_busy: number;
    reads_limit: number;
    open_ms_p50: number | null;
    open_ms_p95: number | null;
    timeouts_10m: number;
  };
  cpu: { pct: number | null };
  network: { plex_ms: number | null; tv_ms: number | null; youtube_ms: number | null };
  backlog: { ready: number; oldest_waiting_sec: number | null };
  spark: Record<"nas_open_ms" | "nas_timeouts" | "cpu_pct" | "backlog_ready", number[]>;
}

export interface QueuesSnapshot {
  at: string;
  paused_all: boolean;
  stages: StageInfo[];
  queues: QueueSummary[];
  pools: Pool[];
  flows: Flow[];
  health: HealthNow;
  issues_count: number;
}

export interface RatePoint {
  t: string;
  done: number;
  failed: number;
  skipped: number;
  nas_timeouts: number;
}

export interface FailureGroup {
  stage: string;
  cause: string;
  label: string;
  count: number;
  first_at: string;
  last_at: string;
  sample_message: string;
  sample_subjects: { kind: string; id: string; label: string }[];
  action: "retry" | "apply" | "open_settings" | "show_workers";
}

export interface LogLine {
  id: number;
  at: string;
  job_id: number;
  stage: string;
  subject_kind: string;
  subject_id: string;
  subject_label: string;
  level: "debug" | "info" | "warn" | "error";
  kind: string;
  message: string;
  data: Record<string, unknown> | null;
}

export interface QueueDetailData {
  info: StageInfo;
  summary: QueueSummary;
  kpis: {
    done_24h: number;
    failed_24h: number;
    median_duration_ms: number | null;
    p95_duration_ms: number | null;
    skipped_share_24h: number;
    changed_share_24h: number;
  };
  running: JobRef[];
  next_up: JobRef[];
  failures: FailureGroup[];
  log: LogLine[];
}

export type RetriggerMode = "queue" | "force" | "force_cascade";

export interface RetriggerRequest {
  items: { subject_kind: SubjectKind; subject_id: string; stage?: string }[];
  mode?: RetriggerMode;
  priority?: number;
}
export type EnqueueOutcome =
  | "inserted"
  | "coalesced"
  | "rerun_requested"
  | "disabled"
  | "unknown_stage"
  | "invalid_subject";
export interface RetriggerResponse {
  results: {
    subject_kind: string;
    subject_id: string;
    stage: string;
    job_id: number | null;
    outcome: EnqueueOutcome;
  }[];
}

/** One thing an external write changed (from the job_events data of plex_collection / dizquetv_show / write_channels). */
export interface WriteChange {
  action: "added" | "removed" | "reordered" | "created" | "deleted" | "updated";
  what: "episodes" | "collection" | "show" | "channel" | "slot";
  target: string;
  count?: number;
  items?: string[];
}

export interface TimelineEntry {
  job: JobRef;
  changes?: WriteChange[];
  duration_ms: number | null;
  triggered: { stage: string; subject_label: string; outcome: string }[];
  reasons: { at: string; why: string; by: string }[];
}

export interface AppearsInEntry {
  target:
    | "plex_collection"
    | "dizquetv_show"
    | "maktaba_channel"
    | "dizquetv_channel"
    | "channel_slot";
  title: string;
  external_id: string;
  detail?: string;
}

export interface ItemHistoryData {
  subject: {
    kind: string;
    id: string;
    label: string;
    library_id: string | null;
    library_label: string | null;
  };
  kinds: string[];
  queues: { stage: string; last_job: JobRef | null; can_requeue: boolean }[];
  seasons?: { id: string; label: string; season: number; kind: string; episode_count: number }[];
  appears_in: AppearsInEntry[];
  timeline: TimelineEntry[];
  next: string | null;
}

export interface LibraryQueueRow {
  library_id: string;
  name: string;
  videos: number;
  seasons: number;
  strip: {
    stage: string;
    done: number;
    total: number;
    running: number;
    pending: number;
    failed: number;
  }[];
  last_activity_at: string | null;
}

export interface HealthSeries {
  metric: string;
  points: { t: string; v: number }[];
}

// ---- SSE payloads (§7.2) -------------------------------------------------

export interface JobFinishedPayload {
  job: JobRef;
  duration_ms: number;
  triggered: { stage: string; count: number }[];
}
export interface JobFailedPayload {
  job: JobRef;
  message: string;
  will_retry: boolean;
  not_before: string | null;
}
export interface JobLogPayload {
  job_id: number;
  stage: string;
  subject_label: string;
  level: "info" | "warn" | "error";
  kind: string;
  message: string;
  data?: Record<string, unknown>;
}

// ---- calls ---------------------------------------------------------------

const q = (s: string) => encodeURIComponent(s);

function qs(params: Record<string, string | number | undefined | null>): string {
  const p = Object.entries(params)
    .filter(([, v]) => v !== undefined && v !== null && v !== "")
    .map(([k, v]) => `${k}=${q(String(v))}`);
  return p.length ? `?${p.join("&")}` : "";
}

export const queueApi = {
  snapshot: () => api.get<QueuesSnapshot>("/api/queues"),
  detail: (stage: string) => api.get<QueueDetailData>(`/api/queues/${q(stage)}`),
  rate: (stage: string, hours = 6) =>
    api.get<{ bucket_sec: number; points: RatePoint[] }>(
      `/api/queues/${q(stage)}/rate${qs({ hours })}`
    ),
  failures: (stage: string) =>
    api.get<{ groups: FailureGroup[] }>(`/api/queues/${q(stage)}/failures`),
  pause: (stage: string, interrupt = false) =>
    api.post<void>(`/api/queues/${q(stage)}/pause${interrupt ? "?interrupt=true" : ""}`),
  resume: (stage: string) => api.post<void>(`/api/queues/${q(stage)}/resume`),
  pauseAll: () => api.post<void>("/api/queues/pause-all"),
  resumeAll: () => api.post<void>("/api/queues/resume-all"),
  patchStage: (stage: string, body: { enabled?: boolean; write_mode?: "auto" | "review" }) =>
    api.patch<StageInfo>(`/api/queues/${q(stage)}`, body),
  retry: (stage: string, body: { cause?: string; job_ids?: number[] }) =>
    api.post<{ enqueued: number; coalesced: number }>(`/api/queues/${q(stage)}/retry`, body),
  apply: (stage: string, body: { job_ids?: number[]; all?: boolean }) =>
    api.post<{ enqueued: number }>(`/api/queues/${q(stage)}/apply`, body),
  setPool: (pool: string, size: number | null) => api.put<Pool>(`/api/pools/${q(pool)}`, { size }),
  retrigger: (req: RetriggerRequest) => api.post<RetriggerResponse>("/api/queue/retrigger", req),
  issues: () => api.get<{ groups: FailureGroup[] }>("/api/issues"),
  activity: (p: {
    stage?: string;
    library_id?: string;
    level?: string;
    cursor?: string | null;
    limit?: number;
  }) =>
    api.get<{ items: LogLine[]; next: string | null }>(
      `/api/activity${qs({
        stage: p.stage,
        library_id: p.library_id,
        level: p.level,
        cursor: p.cursor,
        limit: p.limit,
      })}`
    ),
  history: (kind: string, id: string, cursor?: string | null, limit = 50) =>
    api.get<ItemHistoryData>(`/api/items/${q(kind)}/${q(id)}/history${qs({ cursor, limit })}`),
  extensions: () => api.get<{ extensions: Extension[] }>("/api/extensions"),
  saveExtension: (
    name: string,
    body: { enabled?: boolean; settings?: Record<string, string | number | boolean | null> }
  ) => api.put<Extension>(`/api/extensions/${q(name)}`, body),
  testExtension: (name: string) =>
    api.post<{ ok: boolean; message: string }>(`/api/extensions/${q(name)}/test`),
  workers: () => api.get<{ workers: WorkerHost[] }>("/api/workers"),
  libraries: () => api.get<{ items: LibraryQueueRow[] }>("/api/queues/libraries"),
  health: (hours = 4) => api.get<{ series: HealthSeries[] }>(`/api/queues/health${qs({ hours })}`),
};
