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
  /** the worker (workers.id) running it; absent on older servers, null while it waits */
  claimed_by?: string | null;
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
  /** Epic 40: the tile's state (red only for a dominant new error) and the Needs-you count. */
  health?: StageHealth | null;
}

export type StageHealthState = "ok" | "waiting" | "failing";

export interface StageHealth {
  state: StageHealthState;
  window_hours: number;
  failed_window: number;
  finished_window: number;
  failure_share: number;
  /** What jobs of the stage are parked on (dependency:model:tagcheck, resource:disk ...). */
  waiting: {
    on: string;
    count: number;
    healthy: boolean | null;
    message: string | null;
    since: string | null;
  }[];
  needs_you: number;
  ignored: number;
  failing: {
    signature: string;
    class: string;
    count: number;
    example: string;
    subject: string | null;
  } | null;
}

export type FailureStatus = "needs_you" | "failing" | "waiting" | "retrying" | "ignored";

export interface FailureFilter {
  stage?: string;
  class?: string;
  signature?: string;
  library?: string;
  path_prefix?: string;
  status?: FailureStatus | "all" | "";
}

export interface FailureItem {
  job_id: number;
  stage: string;
  subject_kind: string;
  subject_id: string;
  label: string;
  path: string | null;
  library_id: string | null;
  library_name: string | null;
  state: string;
  status: FailureStatus;
  message: string;
  signature: string | null;
  class: string | null;
  parked_on: string | null;
  /** missing_confirmed, missing_unconfirmed, permission, damaged, waiting:<on> ... */
  reason: string;
  attempts: number;
  max_attempts: number;
  first_failed_at: string | null;
  last_failed_at: string | null;
  missing_confirmed_at: string | null;
  youtube_id: string | null;
  access_check: {
    ok: boolean;
    error: string | null;
    host: string | null;
    checked_at: string;
  } | null;
  /** Epic 47: the ledger row the failed job wrote (absent on older servers) */
  file_action?: { id: string; kind: string; state: string; restorable: boolean } | null;
}

// ---------- Epic 47: offline, MP4, no duplicates ----------

export interface LibraryActionResult {
  library_id: string;
  job_id: number | null;
  outcome: EnqueueOutcome;
}

export interface LibraryActionResponse {
  stage: string;
  results: LibraryActionResult[];
  enqueued: number;
  coalesced: number;
  skipped: number;
}

export interface SourceProblem {
  source_id: string;
  kind: string;
  url: string;
  status: string;
  error: string | null;
}

export interface OfflineSummary {
  library_id: string;
  name: string;
  youtube: {
    sources: number;
    sources_on: number;
    wanted: number;
    backfill: number;
    downloading: number;
    downloaded: number;
    failed: number;
    skipped: number;
    gone: number;
    last_refresh: string | null;
    last_fetched: string | null;
    problems: SourceProblem[];
  };
  not_mp4: number;
  converting: number;
  duplicates: number;
  trash: { entries: number; bytes: number; next_purge_at: string | null };
}

export type SourceMode = "off" | "new_and_backfill" | "new_only";
export const SOURCE_MODES: SourceMode[] = ["new_and_backfill", "new_only", "off"];

export interface SourceOptions {
  max_height?: number;
  sub_langs?: string;
  auto_subs?: boolean;
  audio_only?: boolean;
  cookies_from_browser?: string;
  limit_rate?: string;
}

export interface YouTubeSource {
  id: string;
  library_id: string;
  kind: "channel" | "playlist";
  url: string;
  external_id: string;
  folder: string | null;
  mode: SourceMode;
  added_at: string | null;
  options: SourceOptions;
  status: string | null;
  error: string | null;
  last_checked_at: string | null;
  last_fetched_at: string | null;
  downloads: {
    wanted: number;
    backfill: number;
    downloading: number;
    done: number;
    failed: number;
    skipped: number;
    gone: number;
  };
}

export interface SourceWriteResponse {
  source: YouTubeSource;
  refresh: LibraryActionResult | null;
}

export type DownloadState = "wanted" | "downloading" | "done" | "skipped" | "gone" | "failed";

export interface YouTubeDownload {
  library_id: string;
  library_name: string;
  youtube_id: string;
  source_id: string | null;
  state: DownloadState;
  backfill: boolean;
  reason: string | null;
  failure_class: string | null;
  error: string | null;
  attempts: number;
  video_id: string | null;
  upload_date: string | null;
  requested_at: string | null;
  done_at: string | null;
  not_before: string | null;
}

export type FileActionKind = "convert" | "dedup" | "youtube_fetch" | "purge" | "restore";
export const FILE_ACTION_KINDS: FileActionKind[] = [
  "convert",
  "dedup",
  "youtube_fetch",
  "restore",
  "purge",
];
export type FileActionState =
  | "planned"
  | "staged"
  | "verified"
  | "swapped"
  | "done"
  | "rolled_back"
  | "failed";
export const FILE_ACTION_STATES: FileActionState[] = [
  "done",
  "failed",
  "rolled_back",
  "planned",
  "staged",
  "verified",
  "swapped",
];

export interface FileAction {
  id: string;
  library_id: string;
  library_name: string;
  video_id: string | null;
  video_title: string | null;
  job_id: number | null;
  kind: FileActionKind;
  state: FileActionState;
  old_path: string | null;
  new_path: string | null;
  trash_path: string | null;
  old_size: number | null;
  new_size: number | null;
  verify: Record<string, unknown> | null;
  error: string | null;
  purge_after: string | null;
  purged_at: string | null;
  created_at: string;
  updated_at: string;
  restorable: boolean;
  restore_queued: boolean;
}

export interface LibraryTrash {
  library_id: string;
  name: string;
  entries: number;
  bytes: number;
  next_purge_at: string | null;
  last_purge_at: string | null;
}

export interface FileActionFilter {
  library_id?: string;
  kind?: string;
  state?: string;
  restorable?: boolean;
}

export type OfflineStep = "download" | "convert" | "dedup" | "restore" | "purge";
export const OFFLINE_STEPS: OfflineStep[] = ["download", "convert", "dedup", "restore", "purge"];

export interface OfflineItem extends JobRef {
  step: OfflineStep;
  waiting_for_conversion: boolean;
  downloading: string | null;
  error: string | null;
}

export interface DuplicateMember {
  video_id: string;
  path: string;
  is_copy: boolean;
  score: Record<string, unknown> | null;
  title: string | null;
  library_id: string;
  library_name: string;
  size_bytes: number | null;
  duration_sec: number | null;
  width: number | null;
  height: number | null;
  video_codec: string | null;
  container: string | null;
  bitrate_kbps: number | null;
  viewers: number;
  completed: number;
  last_watched: string | null;
}

export type DuplicateStatus = "open" | "applied" | "dismissed" | "failed";

export interface DuplicateGroup {
  id: string;
  library_id: string | null;
  library_name: string | null;
  tier: "exact" | "same_video";
  status: DuplicateStatus;
  keeper_video_id: string | null;
  reason: string | null;
  created_at: string;
  decided_by: string | null;
  decided_at: string | null;
  job: JobRef | null;
  members: DuplicateMember[];
}

export interface FacetCount {
  value: string;
  label?: string;
  count: number;
}

export interface FailureList {
  items: FailureItem[];
  next: string | null;
  facets: Record<"stage" | "class" | "signature" | "library" | "status", FacetCount[]>;
}

export type FailureAction =
  | "retry"
  | "ignore"
  | "unignore"
  | "rescan"
  | "remove"
  | "redownload"
  | "fix_path"
  | "path_map"
  | "check_access";

export interface FailureActionRequest {
  action: FailureAction;
  job_ids?: number[];
  filter?: FailureFilter;
  path?: string;
  path_map?: { from: string; to: string };
}

export interface FailureActionResult {
  action: FailureAction;
  matched: number;
  affected: number;
  skipped: number;
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

/** POST /api/extensions/plex/signin. */
export interface PlexSignin {
  id: string;
  code: string;
  auth_url: string;
}

/** A Plex server found after sign-in (its token stays on the server). */
export interface PlexServerChoice {
  name: string;
  uri: string;
}

/** GET /api/extensions/plex/signin/{id}. */
export interface PlexSigninStatus {
  state: "waiting" | "done";
  servers?: PlexServerChoice[];
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

// ---- explain / style / insights / pulse (control-centre parity) ----------

export interface ExplainVerdict {
  kind: "episode" | "extra" | "duplicate" | "short" | "clip_group" | "promo" | "unassigned";
  label: string;
  season_id: string | null;
  series_id: string | null;
  season: number | null;
  episode: number | null;
  attached: boolean;
}
export interface ExplainPlaylist {
  title: string;
  position: number | null;
}
export interface ExplainVideo {
  id: string;
  title: string;
  file: string;
  library_id: string;
  library: string;
  aired: string | null;
  date_quality: string | null;
  duration_sec: number | null;
  width: number | null;
  height: number | null;
  youtube_id: string | null;
  link_method: string | null;
  tab: string | null;
  playlists: ExplainPlaylist[];
  plex_rating_key: string | null;
  verdict: ExplainVerdict;
}
export interface ExplainRemote {
  youtube_id: string;
  title: string;
  aired: string | null;
  date_quality: string | null;
  duration_sec: number | null;
  tab: string | null;
  playlists: ExplainPlaylist[];
}
export interface ExplainResponse {
  q: string;
  videos: ExplainVideo[];
  remote: ExplainRemote[];
  limit: number;
}

export type ProductionStyle = "static" | "talk" | "produced" | "cinematic";
export const PRODUCTION_STYLES: ProductionStyle[] = ["cinematic", "produced", "talk", "static"];

export interface SeasonStyle {
  season_id: string;
  series_id: string;
  series: string;
  name: string;
  season: number;
  library_id: string | null;
  library: string | null;
  style: ProductionStyle | null;
  tier: number | null;
  confidence: number | null;
  override_style: ProductionStyle | null;
}
export interface StyleResult {
  style: ProductionStyle | null;
  seasons: { season_id: string; outcome: string; job_id: number | null }[];
}

export interface InsightTotals {
  libraries: number;
  videos: number;
  videos_with_length: number;
  series: number;
  seasons: number;
  plex_collections: number;
  dizquetv_shows: number;
  channels: number;
  style_pinned: number;
  youtube_videos: number;
  videos_without_exact_date: number;
}
export interface Insights {
  totals: InsightTotals;
  styles: Partial<Record<ProductionStyle, number>>;
  genres: { genre: string; seasons: number }[];
}

export type PulseState = "idle" | "working" | "paused" | "stalled";
export interface StuckJob {
  job_id: number;
  stage: string;
  label: string;
  subject_kind: SubjectKind;
  subject_id: string;
  heartbeat_age_sec: number;
  claimed_by: string | null;
  stalled_after_sec: number;
  running_for_sec: number;
}
export interface Pulse {
  state: PulseState;
  running: number;
  waiting: number;
  paused_all: boolean;
  last_activity_at: string | null;
  age_sec: number | null;
  /** The stalled jobs: admins only (empty for everyone else). */
  stuck: StuckJob[];
  /** How many jobs stalled (shown to everyone). */
  stuck_count?: number;
}

export interface LibraryCollections {
  topics: { id: string; name: string; items: number; collection: string | null }[];
  clip_groups: {
    id: string;
    kind: "clips" | "standalone";
    basis: "playlist" | "topic" | "rest" | "promo";
    title: string;
    items: number;
    collection: string | null;
  }[];
  promos: {
    video_id: string;
    title: string;
    show: string;
    season_id: string | null;
    season: number | null;
    evidence: string;
  }[];
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
  /** "Sign in with Plex" (plex.tv PIN). Tokens never reach the browser. */
  plexSignin: () => api.post<PlexSignin>("/api/extensions/plex/signin"),
  plexSigninStatus: (id: string) =>
    api.get<PlexSigninStatus>(`/api/extensions/plex/signin/${q(id)}`),
  plexSigninUse: (id: string, name: string) =>
    api.post<Extension>(`/api/extensions/plex/signin/${q(id)}/use`, { name }),
  workers: () => api.get<{ workers: WorkerHost[] }>("/api/workers"),
  /** Jobs running now, oldest first; host: "home" or a joined machine's id (Epic 31). */
  running: (host?: string) =>
    api.get<{ host: string; jobs: JobRef[] }>(`/api/queues/running${qs({ host })}`),
  libraries: () => api.get<{ items: LibraryQueueRow[] }>("/api/queues/libraries"),
  health: (hours = 4) => api.get<{ series: HealthSeries[] }>(`/api/queues/health${qs({ hours })}`),
  explain: (text: string, libraryId?: string, limit?: number) =>
    api.get<ExplainResponse>(`/api/explain${qs({ q: text, library_id: libraryId, limit })}`),
  seasonStyles: (p: { libraryId?: string; q?: string; pinned?: boolean; limit?: number }) =>
    api.get<{ items: SeasonStyle[] }>(
      `/api/seasons/styles${qs({
        library_id: p.libraryId,
        q: p.q,
        pinned: p.pinned ? "true" : undefined,
        limit: p.limit,
      })}`
    ),
  /** Pin (or, with null, clear) one season's production style; queues its re-rating. */
  putSeasonStyle: (seasonId: string, style: ProductionStyle | null) =>
    api.put<StyleResult>(`/api/seasons/${q(seasonId)}/style`, { style }),
  /** The same for every season of a series. */
  putSeriesStyle: (seriesId: string, style: ProductionStyle | null) =>
    api.put<StyleResult>(`/api/series/${q(seriesId)}/style`, { style }),
  libraryCollections: (libraryId: string) =>
    api.get<LibraryCollections>(`/api/items/library/${q(libraryId)}/collections`),
  insights: () => api.get<Insights>("/api/queues/insights"),
  pulse: () => api.get<Pulse>("/api/queues/pulse"),
  /** Epic 40: every failing item, filtered (admin). */
  failureItems: (f: FailureFilter, before?: string | null, limit = 50) =>
    api.get<FailureList>(
      `/api/queues/failures${qs({
        stage: f.stage || undefined,
        class: f.class || undefined,
        signature: f.signature || undefined,
        library: f.library || undefined,
        path_prefix: f.path_prefix || undefined,
        status: f.status || undefined,
        before: before ?? undefined,
        limit,
      })}`
    ),
  failureAction: (req: FailureActionRequest) =>
    api.post<FailureActionResult>("/api/queues/failures/actions", req),

  // ---- Epic 47 (admin; every call only queues work) ----
  /** a job's own controls (the jobs API) */
  cancelJob: (id: number) => api.post<unknown>(`/api/jobs/${id}/cancel`),
  retryJob: (id: number) => api.post<unknown>(`/api/jobs/${id}/retry`),
  offlineSummaries: () =>
    api.get<{ items: OfflineSummary[]; cross_library_duplicates: number }>(
      "/api/queues/offline-summary"
    ),
  /** id "all" with all: true runs it for every library */
  convertLibrary: (id: string, body: { dry_run?: boolean; all?: boolean }) =>
    api.post<LibraryActionResponse>(`/api/libraries/${q(id)}/convert`, body),
  dedupScanLibrary: (id: string, body: { dry_run?: boolean; all?: boolean }) =>
    api.post<LibraryActionResponse>(`/api/libraries/${q(id)}/dedup-scan`, body),
  refreshYouTube: (
    id: string,
    body: { all?: boolean; full_listing?: boolean; refresh?: boolean }
  ) => api.post<LibraryActionResponse>(`/api/libraries/${q(id)}/youtube/refresh`, body),
  resumeBackfill: (id: string) =>
    api.post<LibraryActionResponse>(`/api/libraries/${q(id)}/youtube/backfill`),
  retryDownloads: (id: string, body: { states?: DownloadState[]; source_id?: string }) =>
    api.post<{ wanted: number; new: number; backfill: number; results: LibraryActionResult[] }>(
      `/api/libraries/${q(id)}/youtube/retry`,
      body
    ),
  importTartube: (id: string, body: { path: string; database?: string; channels?: string[] }) =>
    api.post<LibraryActionResponse>(`/api/libraries/${q(id)}/youtube/tartube`, body),
  sources: (id: string) =>
    api.get<{ items: YouTubeSource[] }>(`/api/libraries/${q(id)}/youtube/sources`),
  addSource: (
    id: string,
    body: { url: string; mode?: SourceMode; folder?: string; options?: SourceOptions }
  ) => api.post<SourceWriteResponse>(`/api/libraries/${q(id)}/youtube/sources`, body),
  patchSource: (
    id: string,
    sid: string,
    body: { mode?: SourceMode; folder?: string; options?: Record<string, unknown> }
  ) => api.patch<SourceWriteResponse>(`/api/libraries/${q(id)}/youtube/sources/${q(sid)}`, body),
  deleteSource: (id: string, sid: string) =>
    api.delete<void>(`/api/libraries/${q(id)}/youtube/sources/${q(sid)}`),
  youtubeDownloads: (p: {
    library_id?: string;
    source_id?: string;
    state?: string;
    cursor?: string | null;
    limit?: number;
  }) =>
    api.get<{ items: YouTubeDownload[]; next: string | null }>(
      `/api/queues/youtube-downloads${qs({ ...p })}`
    ),
  offlineItems: (p: {
    library_id?: string;
    step?: OfflineStep;
    before?: string | null;
    limit?: number;
  }) =>
    api.get<{ items: OfflineItem[]; next: string | null }>(
      `/api/queues/offline-items${qs({ ...p })}`
    ),
  fileActions: (f: FileActionFilter, cursor?: string | null, limit = 50) =>
    api.get<{ items: FileAction[]; next: string | null; trash: LibraryTrash[] }>(
      `/api/queues/file-actions${qs({
        library_id: f.library_id || undefined,
        kind: f.kind || undefined,
        state: f.state || undefined,
        restorable: f.restorable ? "true" : undefined,
        cursor: cursor ?? undefined,
        limit,
      })}`
    ),
  duplicates: (
    p: { status?: string; tier?: string; library_id?: string; limit?: number },
    cursor?: string | null
  ) =>
    api.get<{ items: DuplicateGroup[]; next: string | null }>(
      `/api/queues/duplicates${qs({ ...p, cursor: cursor ?? undefined })}`
    ),
  applyDuplicate: (gid: string, keeper?: string) =>
    api.post<{ group_id: string; job_id: number | null; outcome: EnqueueOutcome }>(
      `/api/queues/duplicates/${q(gid)}/apply`,
      keeper ? { keeper_video_id: keeper } : {}
    ),
  dismissDuplicate: (gid: string) =>
    api.post<{ group_id: string; status: string }>(`/api/queues/duplicates/${q(gid)}/dismiss`),
  restoreFileAction: (id: string) =>
    api.post<{ action_id: string; job_id: number | null; outcome: EnqueueOutcome }>(
      `/api/queues/file-actions/${q(id)}/restore`
    ),
};
