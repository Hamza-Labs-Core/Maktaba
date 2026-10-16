// JSON fixtures for every plan 06-11 §8.2 type and §7.2 event. Used by the
// unit tests and by the dev-time mock server (web/mocks/queue-dev).
// Numbers are chosen to cover every card state: running (probe,
// fingerprint, detect), done (thumb_hash), waiting (tag, plex_collection),
// failing (link), disabled (dizquetv_show), review (write_channels).
import type {
  Extension,
  FailureGroup,
  Flow,
  HealthSeries,
  ItemHistoryData,
  JobRef,
  LibraryQueueRow,
  LogLine,
  Pool,
  QueueDetailData,
  QueueSummary,
  QueuesSnapshot,
  RatePoint,
  StageInfo,
  WaitingReason,
  WorkerHost,
} from "../../lib/queueApi";

export const NOW = "2026-05-18T12:00:00Z";
const ago = (sec: number) => new Date(Date.parse(NOW) - sec * 1000).toISOString();

export function jobRef(over: Partial<JobRef> = {}): JobRef {
  return {
    id: 1001,
    stage: "probe",
    state: "running",
    subject_kind: "video",
    subject_id: "00000000-0000-4000-8000-000000000001",
    subject_label: "قصة وآية - الحلقة 12",
    library_id: "lib-1",
    priority: 100,
    attempts: 1,
    max_attempts: 3,
    outcome: null,
    error_cause: null,
    created_at: ago(300),
    started_at: ago(20),
    finished_at: null,
    progress: null,
    ...over,
  };
}

interface StageSeed {
  stage: string;
  label: string;
  lane: StageInfo["lane"];
  order: number;
  pool: string;
  kinds: string[];
  to?: string[];
  hidden?: boolean;
  legacy?: boolean;
  entry?: string[];
  external_write?: boolean;
  fed_by?: string[];
  extension?: string;
}

const SEEDS: StageSeed[] = [
  {
    stage: "youtube_sync",
    label: "YouTube listing",
    lane: "sources",
    order: 0,
    pool: "network_yt",
    kinds: ["library"],
    to: ["link", "detect"],
    entry: ["library"],
  },
  {
    stage: "probe",
    label: "Length",
    lane: "episodes",
    order: 1,
    pool: "nas_read",
    kinds: ["video"],
    to: ["thumb_hash", "link", "detect"],
    legacy: true,
    entry: ["video"],
  },
  {
    stage: "thumb_hash",
    label: "Thumbnail",
    lane: "episodes",
    order: 2,
    pool: "nas_read",
    kinds: ["video"],
    to: ["detect"],
    fed_by: ["probe"],
  },
  {
    stage: "link",
    label: "Link",
    lane: "episodes",
    order: 3,
    pool: "cpu",
    kinds: ["video"],
    to: ["detect"],
    fed_by: ["probe", "youtube_sync"],
  },
  {
    stage: "fingerprint",
    label: "Fingerprint",
    lane: "episodes",
    order: 4,
    pool: "nas_read",
    kinds: ["video"],
    to: ["detect", "fp_gate"],
    fed_by: ["detect", "fp_gate"],
  },
  {
    stage: "style_sample",
    label: "Style sample",
    lane: "episodes",
    order: 5,
    pool: "nas_read",
    kinds: ["video"],
    to: ["rate_style"],
    fed_by: ["rate_style"],
  },
  {
    stage: "detect",
    label: "Place episodes",
    lane: "seasons",
    order: 1,
    pool: "cpu",
    kinds: ["library"],
    to: ["tag", "rate_style", "plex_collection", "dizquetv_show", "plan_channels", "fingerprint"],
    entry: ["library"],
    fed_by: ["probe", "thumb_hash", "link", "fingerprint"],
  },
  {
    stage: "fp_gate",
    label: "Samples to candidates",
    lane: "seasons",
    order: 1,
    pool: "cpu",
    kinds: ["season"],
    hidden: true,
    to: ["fingerprint"],
    fed_by: ["detect", "fingerprint"],
  },
  {
    stage: "tag",
    label: "Tag topics",
    lane: "seasons",
    order: 2,
    pool: "cpu",
    kinds: ["season", "library"],
    to: ["plan_channels"],
    entry: ["season"],
    fed_by: ["detect"],
  },
  {
    stage: "rate_style",
    label: "Rate style",
    lane: "seasons",
    order: 3,
    pool: "cpu",
    kinds: ["season"],
    to: ["plan_channels", "style_sample"],
    entry: ["season"],
    fed_by: ["detect", "style_sample"],
  },
  {
    stage: "plex_collection",
    label: "Plex collection",
    lane: "seasons",
    order: 4,
    pool: "network_plex",
    kinds: ["season"],
    to: ["dizquetv_show"],
    entry: ["season"],
    external_write: true,
    extension: "plex",
    fed_by: ["detect"],
  },
  {
    stage: "dizquetv_show",
    label: "dizqueTV show",
    lane: "seasons",
    order: 5,
    pool: "network_tv",
    kinds: ["season"],
    external_write: true,
    extension: "dizquetv",
    fed_by: ["detect", "plex_collection"],
  },
  {
    stage: "plan_channels",
    label: "Plan channels",
    lane: "channels",
    order: 1,
    pool: "cpu",
    kinds: ["catalog"],
    to: ["write_channels"],
    entry: ["catalog"],
    fed_by: ["detect", "tag", "rate_style"],
  },
  {
    stage: "write_channels",
    label: "Write channels",
    lane: "channels",
    order: 2,
    pool: "network_tv",
    kinds: ["catalog"],
    external_write: true,
    fed_by: ["plan_channels"],
  },
  {
    stage: "scan",
    label: "Scan folders",
    lane: "media",
    order: 0,
    pool: "nas_read",
    kinds: ["library"],
    to: ["probe"],
    legacy: true,
  },
  {
    stage: "extract",
    label: "Extract audio",
    lane: "media",
    order: 1,
    pool: "nas_read",
    kinds: ["video"],
    legacy: true,
  },
  {
    stage: "transcribe",
    label: "Transcribe",
    lane: "media",
    order: 2,
    pool: "gpu",
    kinds: ["video"],
    legacy: true,
  },
  {
    stage: "subtitle_gen",
    label: "Subtitles",
    lane: "media",
    order: 3,
    pool: "cpu",
    kinds: ["video"],
    legacy: true,
  },
  {
    stage: "index",
    label: "Index",
    lane: "media",
    order: 4,
    pool: "cpu",
    kinds: ["video"],
    legacy: true,
  },
  {
    stage: "thumbnail",
    label: "Poster frames",
    lane: "media",
    order: 5,
    pool: "nas_read",
    kinds: ["video"],
    legacy: true,
  },
];

export function stageInfo(over: Partial<StageInfo> & { stage: string }): StageInfo {
  const seed = SEEDS.find((s) => s.stage === over.stage);
  return {
    label: seed?.label ?? over.stage,
    lane: seed?.lane ?? "media",
    lane_order: seed?.order ?? 0,
    subject_kinds: seed?.kinds ?? ["video"],
    pool: seed?.pool ?? "cpu",
    version: 1,
    legacy: seed?.legacy ?? false,
    hidden: seed?.hidden ?? false,
    external_write: seed?.external_write ?? false,
    write_mode: "auto", // external writes are automatic by default
    enabled: true,
    disabled_reason: null,
    paused: false,
    paused_at: null,
    paused_by: null,
    debounce_sec: 0,
    edges: (seed?.to ?? []).map((to) => ({
      to,
      map: "same",
      when: "output_changed",
      legacy: false,
    })),
    may_request: [],
    entry_for: seed?.entry ?? [],
    fed_by: seed?.fed_by ?? [],
    extension: seed?.extension ?? null,
    graph_synced_at: ago(3600),
    ...over,
  };
}

export const stagesFixture: StageInfo[] = SEEDS.map((s) =>
  s.stage === "dizquetv_show"
    ? stageInfo({ stage: s.stage, enabled: false, disabled_reason: "not_configured" })
    : stageInfo({ stage: s.stage })
);

const ZERO = {
  pending: 0,
  ready: 0,
  running: 0,
  paused: 0,
  failed_open: 0,
  done_24h: 0,
  skipped_24h: 0,
  awaiting_review: 0,
};

export function queueSummary(
  stage: string,
  over: {
    counts?: Partial<QueueSummary["counts"]>;
    wave?: Partial<QueueSummary["wave"]>;
    rate_per_min?: number;
    eta_sec?: number | null;
    oldest_waiting_sec?: number | null;
    waiting_reason?: WaitingReason;
    running_jobs?: JobRef[];
    served_by?: string[];
    no_worker?: boolean;
  } = {}
): QueueSummary {
  return {
    stage,
    counts: { ...ZERO, ...over.counts },
    wave: { since: ago(7200), done: 0, total: 0, ...over.wave },
    rate_per_min: over.rate_per_min ?? 0,
    eta_sec: over.eta_sec ?? null,
    oldest_waiting_sec: over.oldest_waiting_sec ?? null,
    waiting_reason: over.waiting_reason ?? "idle",
    running_jobs: over.running_jobs ?? [],
    served_by: over.served_by ?? ["This PC"],
    no_worker: over.no_worker ?? false,
  };
}

const runningProbe = jobRef({ id: 2001, stage: "probe", subject_label: "حلقة 12 - قصة وآية" });
const runningFp = jobRef({
  id: 2002,
  stage: "fingerprint",
  subject_label: "Lecture 04 · fingerprint",
  progress: {
    step: "tail window",
    steps: ["head window", "tail window", "frames"],
    done_units: 1,
    total_units: 3,
  },
});

export const summariesFixture: QueueSummary[] = [
  queueSummary("youtube_sync", {
    counts: { done_24h: 3 },
    wave: { done: 3, total: 3 },
    waiting_reason: "idle",
  }),
  queueSummary("probe", {
    counts: { pending: 340, ready: 340, running: 2, done_24h: 2410, skipped_24h: 100 },
    wave: { done: 2410, total: 2752 },
    rate_per_min: 41,
    eta_sec: 500,
    oldest_waiting_sec: 420,
    running_jobs: [runningProbe],
  }),
  queueSummary("thumb_hash", {
    counts: { done_24h: 2380 },
    wave: { done: 2380, total: 2380 },
    waiting_reason: "idle",
  }),
  queueSummary("link", {
    counts: { pending: 12, ready: 12, failed_open: 7, done_24h: 2100 },
    wave: { done: 2100, total: 2119 },
    waiting_reason: "idle",
    rate_per_min: 0,
    oldest_waiting_sec: 90,
  }),
  queueSummary("fingerprint", {
    counts: { pending: 120, ready: 118, running: 1, failed_open: 2, done_24h: 300 },
    wave: { done: 300, total: 423 },
    rate_per_min: 3.5,
    eta_sec: 2040,
    oldest_waiting_sec: 3100,
    running_jobs: [runningFp],
  }),
  queueSummary("style_sample", {
    counts: { pending: 0, done_24h: 40 },
    wave: { done: 40, total: 40 },
  }),
  queueSummary("detect", {
    counts: { pending: 2, ready: 0, running: 1, done_24h: 14 },
    wave: { done: 14, total: 17 },
    rate_per_min: 0.4,
    eta_sec: 450,
    waiting_reason: "debouncing",
    running_jobs: [
      jobRef({
        id: 2003,
        stage: "detect",
        subject_kind: "library",
        subject_label: "Islamic lectures",
      }),
    ],
  }),
  queueSummary("fp_gate", { counts: { pending: 3, ready: 3 }, wave: { done: 5, total: 8 } }),
  queueSummary("tag", {
    counts: { pending: 9, ready: 0 },
    wave: { done: 31, total: 40 },
    waiting_reason: "upstream",
  }),
  queueSummary("rate_style", {
    counts: { pending: 4, ready: 0 },
    wave: { done: 12, total: 16 },
    waiting_reason: "upstream",
  }),
  queueSummary("plex_collection", {
    counts: { pending: 5, ready: 0 },
    wave: { done: 20, total: 25 },
    waiting_reason: "upstream",
  }),
  queueSummary("dizquetv_show", { wave: { done: 0, total: 0 }, waiting_reason: "not_configured" }),
  queueSummary("plan_channels", {
    counts: { done_24h: 2 },
    wave: { done: 2, total: 2 },
    waiting_reason: "idle",
  }),
  queueSummary("write_channels", { counts: { done_24h: 1 }, wave: { done: 1, total: 1 } }),
  queueSummary("scan", { counts: { done_24h: 6 }, wave: { done: 6, total: 6 } }),
  queueSummary("extract", {
    counts: { pending: 10, ready: 10, paused: 0 },
    wave: { done: 90, total: 100 },
    waiting_reason: "pool_full",
  }),
  queueSummary("transcribe", {
    counts: { pending: 40, ready: 40, running: 1 },
    wave: { done: 8, total: 49 },
    rate_per_min: 0.2,
    eta_sec: 12000,
    served_by: ["Mac"],
  }),
  queueSummary("subtitle_gen"),
  queueSummary("index"),
  queueSummary("thumbnail"),
];

export const poolsFixture: Pool[] = [
  {
    pool: "nas_read",
    label: "NAS reads",
    scope: "global",
    size: 2,
    effective_size: 2,
    busy: 2,
    stages: ["probe", "thumb_hash", "fingerprint", "style_sample", "scan", "extract", "thumbnail"],
    updated_at: ago(9000),
    updated_by: "admin",
  },
  {
    pool: "cpu",
    label: "CPU",
    scope: "per_worker",
    size: null,
    effective_size: 3,
    busy: 1,
    stages: [
      "link",
      "detect",
      "fp_gate",
      "tag",
      "rate_style",
      "plan_channels",
      "subtitle_gen",
      "index",
    ],
    updated_at: ago(9000),
    updated_by: null,
  },
  {
    pool: "gpu",
    label: "GPU",
    scope: "per_worker",
    size: 1,
    effective_size: 1,
    busy: 1,
    stages: ["transcribe"],
    updated_at: ago(9000),
    updated_by: null,
  },
  {
    pool: "network_plex",
    label: "Plex",
    scope: "global",
    size: 2,
    effective_size: 2,
    busy: 0,
    stages: ["plex_collection"],
    updated_at: ago(9000),
    updated_by: null,
  },
  {
    pool: "network_tv",
    label: "dizqueTV",
    scope: "global",
    size: 1,
    effective_size: 1,
    busy: 0,
    stages: ["dizquetv_show", "write_channels"],
    updated_at: ago(9000),
    updated_by: null,
  },
  {
    pool: "network_yt",
    label: "YouTube",
    scope: "global",
    size: 1,
    effective_size: 1,
    busy: 0,
    stages: ["youtube_sync"],
    updated_at: ago(9000),
    updated_by: null,
  },
];

export const extensionsFixture: Extension[] = [
  {
    name: "plex",
    label: "Plex",
    description: "Keeps Plex collections in step with your seasons.",
    version: "1.2.0",
    enabled: true,
    configured: true,
    settings_schema: [
      {
        key: "url",
        label: "Server address",
        type: "url",
        required: true,
        help: "For example http://192.168.1.10:32400",
      },
      { key: "token", label: "Token", type: "secret", required: true },
      {
        key: "path_map",
        label: "Path map",
        type: "text",
        help: "PLEX=LOCAL pairs, comma separated",
      },
    ],
    settings: { url: "http://192.168.1.10:32400", path_map: "/data=Z:/" },
    secrets_set: ["token"],
  },
  {
    name: "dizquetv",
    label: "dizqueTV",
    description: "Mirrors seasons and channels into a dizqueTV server.",
    version: "0.4.1",
    enabled: false,
    configured: false,
    settings_schema: [
      { key: "url", label: "Server address", type: "url", required: true },
      { key: "api_key", label: "API key", type: "secret" },
      { key: "mirror_channels", label: "Also mirror channels", type: "boolean" },
    ],
    settings: { url: "", mirror_channels: false },
    secrets_set: [],
  },
];

export const workersFixture: WorkerHost[] = [
  {
    worker_id: "pc-1",
    host: "This PC",
    online: true,
    last_seen_at: ago(4),
    serves: [
      "scan",
      "probe",
      "thumb_hash",
      "link",
      "fingerprint",
      "detect",
      "plex_collection",
      "write_channels",
    ],
    serves_label: "NAS, detection, Plex",
  },
  {
    worker_id: "mac-1",
    host: "Mac",
    online: true,
    last_seen_at: ago(9),
    serves: ["transcribe"],
    serves_label: "transcription",
  },
];

export const flowsFixture: Flow[] = [
  { from: "probe", to: "thumb_hash", per_min: 38 },
  { from: "thumb_hash", to: "link", per_min: 0 },
  { from: "probe", to: "link", per_min: 36 },
  { from: "link", to: "fingerprint", per_min: 0 },
  { from: "fingerprint", to: "style_sample", per_min: 0 },
  { from: "detect", to: "tag", per_min: 0.3 },
  { from: "tag", to: "rate_style", per_min: 0 },
  { from: "detect", to: "rate_style", per_min: 0.2 },
  { from: "rate_style", to: "plex_collection", per_min: 0 },
  { from: "plex_collection", to: "dizquetv_show", per_min: 0.1 },
  { from: "plan_channels", to: "write_channels", per_min: 0 },
];

export const snapshotFixture: QueuesSnapshot = {
  at: NOW,
  paused_all: false,
  stages: stagesFixture,
  queues: summariesFixture,
  pools: poolsFixture,
  flows: flowsFixture,
  health: {
    nas: { reads_busy: 2, reads_limit: 2, open_ms_p50: 180, open_ms_p95: 940, timeouts_10m: 3 },
    cpu: { pct: 47 },
    network: { plex_ms: 38, tv_ms: 22, youtube_ms: -1 },
    backlog: { ready: 523, oldest_waiting_sec: 3100 },
    spark: {
      nas_open_ms: [120, 140, 180, 210, 190, 260, 240, 180],
      nas_timeouts: [0, 0, 1, 0, 0, 2, 0, 0],
      cpu_pct: [30, 40, 35, 55, 60, 52, 47, 47],
      backlog_ready: [200, 260, 340, 400, 480, 510, 523, 523],
    },
  },
  issues_count: 4,
};

export const failureGroupsFixture: FailureGroup[] = [
  {
    stage: "fingerprint",
    cause: "nas_timeout",
    label: "NAS read timed out",
    count: 2,
    first_at: ago(1800),
    last_at: ago(120),
    sample_message: "ffmpeg: Connection timed out (WinError 121)",
    sample_subjects: [{ kind: "video", id: "v-1", label: "Lecture 04" }],
    action: "retry",
  },
  {
    stage: "link",
    cause: "youtube_bot_check",
    label: "YouTube asked to confirm we are not a bot",
    count: 7,
    first_at: ago(7200),
    last_at: ago(600),
    sample_message: "Sign in to confirm you're not a bot",
    sample_subjects: [{ kind: "video", id: "v-2", label: "حلقة 3" }],
    action: "retry",
  },
];

let logId = 9000;
export function logLine(over: Partial<LogLine> = {}): LogLine {
  logId += 1;
  return {
    id: logId,
    at: ago(60),
    job_id: 2001,
    stage: "probe",
    subject_kind: "video",
    subject_id: "v-1",
    subject_label: "Lecture 04",
    level: "info",
    kind: "done",
    message: "Length read: 54 min",
    data: null,
    ...over,
  };
}

export const logFixture: LogLine[] = [
  logLine({
    id: 8001,
    at: ago(20),
    stage: "detect",
    subject_kind: "library",
    subject_label: "Islamic lectures",
    kind: "triggered",
    message: "Placed 118 episodes into 7 seasons; 2 seasons changed",
  }),
  logLine({
    id: 8002,
    at: ago(45),
    stage: "fingerprint",
    level: "warn",
    kind: "retry_scheduled",
    message: "NAS read timed out, retrying in 2 min",
  }),
  logLine({
    id: 8003,
    at: ago(70),
    stage: "link",
    level: "error",
    kind: "failed",
    subject_label: "حلقة 3",
    message: "YouTube asked to confirm we are not a bot",
  }),
  logLine({ id: 8004, at: ago(90), stage: "probe", message: "Length read: 54 min" }),
];

export const healthSeriesFixture: HealthSeries[] = [
  {
    metric: "nas.open_ms_p95",
    points: Array.from({ length: 24 }, (_, i) => ({
      t: ago((24 - i) * 600),
      v: 300 + ((i * 97) % 700),
    })),
  },
  {
    metric: "cpu.pct",
    points: Array.from({ length: 24 }, (_, i) => ({
      t: ago((24 - i) * 600),
      v: 30 + ((i * 13) % 50),
    })),
  },
  {
    metric: "net.plex_ms",
    points: Array.from({ length: 24 }, (_, i) => ({ t: ago((24 - i) * 600), v: 30 + (i % 9) })),
  },
  {
    metric: "nas.timeouts",
    points: Array.from({ length: 24 }, (_, i) => ({
      t: ago((24 - i) * 600),
      v: i % 7 === 0 ? 2 : 0,
    })),
  },
];

export const rateFixture: { bucket_sec: number; points: RatePoint[] } = {
  bucket_sec: 600,
  points: Array.from({ length: 36 }, (_, i) => ({
    t: ago((36 - i) * 600),
    done: 20 + ((i * 7) % 30),
    failed: i % 6 === 0 ? 3 : 0,
    skipped: 5,
    nas_timeouts: i % 9 === 0 ? 2 : 0,
  })),
};

export const librariesFixture: LibraryQueueRow[] = [
  {
    library_id: "lib-1",
    name: "Islamic lectures",
    videos: 1840,
    seasons: 22,
    strip: [
      { stage: "probe", done: 1800, total: 1840, running: 2, pending: 38, failed: 0 },
      { stage: "thumb_hash", done: 1840, total: 1840, running: 0, pending: 0, failed: 0 },
      { stage: "link", done: 1700, total: 1840, running: 0, pending: 133, failed: 7 },
      { stage: "fingerprint", done: 120, total: 300, running: 1, pending: 178, failed: 1 },
      { stage: "detect", done: 1, total: 1, running: 0, pending: 0, failed: 0 },
    ],
    last_activity_at: ago(30),
  },
  {
    library_id: "lib-2",
    name: "قصة وآية",
    videos: 912,
    seasons: 3,
    strip: [
      { stage: "probe", done: 912, total: 912, running: 0, pending: 0, failed: 0 },
      { stage: "thumb_hash", done: 912, total: 912, running: 0, pending: 0, failed: 0 },
      { stage: "link", done: 912, total: 912, running: 0, pending: 0, failed: 0 },
      { stage: "fingerprint", done: 0, total: 0, running: 0, pending: 0, failed: 0 },
      { stage: "detect", done: 1, total: 1, running: 0, pending: 0, failed: 0 },
    ],
    last_activity_at: ago(86400 * 2),
  },
];

export function detailFixture(stage: string): QueueDetailData {
  const info = stagesFixture.find((s) => s.stage === stage) ?? stageInfo({ stage });
  const summary = summariesFixture.find((s) => s.stage === stage) ?? queueSummary(stage);
  const running = summary.running_jobs.length ? summary.running_jobs : [];
  return {
    info,
    summary,
    kpis: {
      done_24h: summary.counts.done_24h,
      failed_24h: 9,
      median_duration_ms: 3200,
      p95_duration_ms: 18400,
      skipped_share_24h: 0.04,
      changed_share_24h: 0.61,
    },
    running,
    next_up: [
      jobRef({ id: 3001, state: "pending", stage, subject_label: "Lecture 05", started_at: null }),
      jobRef({ id: 3002, state: "pending", stage, subject_label: "Lecture 06", started_at: null }),
    ],
    failures: failureGroupsFixture.filter((g) => g.stage === stage),
    log: logFixture.filter((l) => l.stage === stage),
  };
}

const done = (over: Partial<JobRef>) =>
  jobRef({
    state: "done",
    started_at: ago(500),
    finished_at: ago(480),
    outcome: "changed",
    ...over,
  });

const plexWrite = done({
  id: 13,
  stage: "plex_collection",
  subject_kind: "season",
  subject_label: "Fiqh of worship",
});
export const seasonWriteChanges: NonNullable<ItemHistoryData["timeline"][number]["changes"]> = [
  { action: "created", what: "collection", target: "Fiqh of worship", count: 1 },
  {
    action: "added",
    what: "episodes",
    target: "Fiqh of worship",
    count: 3,
    items: ["Lecture 22", "Lecture 23", "Lecture 24"],
  },
  {
    action: "removed",
    what: "episodes",
    target: "Fiqh of worship",
    count: 1,
    items: ["Lecture 02 (duplicate)"],
  },
  { action: "reordered", what: "episodes", target: "Fiqh of worship", count: 24 },
];

export const libraryHistoryFixture: ItemHistoryData = {
  subject: {
    kind: "library",
    id: "lib-1",
    label: "Islamic lectures",
    library_id: "lib-1",
    library_label: "Islamic lectures",
  },
  kinds: ["talk", "ongoing"],
  queues: [
    {
      stage: "youtube_sync",
      last_job: done({ id: 11, stage: "youtube_sync", subject_kind: "library" }),
      can_requeue: true,
    },
    {
      stage: "detect",
      last_job: done({ id: 12, stage: "detect", subject_kind: "library" }),
      can_requeue: true,
    },
    { stage: "tag", last_job: null, can_requeue: true },
    { stage: "plan_channels", last_job: null, can_requeue: false },
  ],
  seasons: [
    { id: "s-1", label: "Fiqh of worship", season: 1, kind: "episodic", episode_count: 24 },
    { id: "s-2", label: "2023", season: 2023, kind: "ongoing", episode_count: 52 },
    { id: "s-3", label: "2024", season: 2024, kind: "ongoing", episode_count: 48 },
  ],
  appears_in: [
    { target: "plex_collection", title: "Fiqh of worship", external_id: "plex-991" },
    { target: "dizquetv_show", title: "Fiqh of worship", external_id: "dz-12" },
    {
      target: "channel_slot",
      title: "Wisdom channel",
      external_id: "ch-4",
      detail: "Tuesdays 20:00",
    },
  ],
  timeline: [
    {
      job: plexWrite,
      changes: seasonWriteChanges,
      duration_ms: 1900,
      triggered: [],
      reasons: [{ at: ago(470), why: "detect output changed", by: "job:12" }],
    },
    {
      job: done({
        id: 12,
        stage: "detect",
        subject_kind: "library",
        subject_label: "Islamic lectures",
      }),
      duration_ms: 4200,
      triggered: [
        { stage: "tag", subject_label: "Fiqh of worship", outcome: "inserted" },
        { stage: "plex_collection", subject_label: "Fiqh of worship", outcome: "coalesced" },
      ],
      reasons: [{ at: ago(520), why: "link output changed", by: "job:11" }],
    },
    {
      job: jobRef({
        id: 10,
        stage: "link",
        state: "failed",
        outcome: null,
        error_cause: "youtube_bot_check",
        finished_at: ago(900),
        subject_label: "Islamic lectures",
      }),
      duration_ms: 800,
      triggered: [],
      reasons: [],
    },
    {
      job: done({ id: 9, stage: "tag", outcome: "unchanged", subject_label: "Islamic lectures" }),
      duration_ms: 120,
      triggered: [],
      reasons: [{ at: ago(2000), why: "user re-queue", by: "user:1" }],
    },
  ],
  next: null,
};

export const videoHistoryFixture: ItemHistoryData = {
  subject: {
    kind: "video",
    id: "v-1",
    label: "Lecture 04",
    library_id: "lib-1",
    library_label: "Islamic lectures",
  },
  kinds: ["episode"],
  queues: [
    { stage: "probe", last_job: done({ id: 31, stage: "probe" }), can_requeue: true },
    { stage: "fingerprint", last_job: null, can_requeue: true },
  ],
  appears_in: [],
  timeline: [],
  next: null,
};

// ---- SSE events (§7.2) ---------------------------------------------------

export const events = {
  jobStarted: (job: JobRef) => ({ type: "job.started", at: NOW, payload: { job } }),
  jobProgress: (job: JobRef) => ({ type: "job.progress", at: NOW, payload: { job } }),
  jobFinished: (job: JobRef, triggered: { stage: string; count: number }[] = []) => ({
    type: "job.finished",
    at: NOW,
    payload: { job: { ...job, state: "done", outcome: "changed" }, duration_ms: 2100, triggered },
  }),
  jobFailed: (job: JobRef, message = "boom", will_retry = true) => ({
    type: "job.failed",
    at: NOW,
    payload: { job: { ...job, state: "failed" }, message, will_retry, not_before: null },
  }),
  jobLog: (
    over: Partial<{
      stage: string;
      subject_label: string;
      level: "info" | "warn" | "error";
      kind: string;
      message: string;
      job_id: number;
    }> = {}
  ) => ({
    type: "job.log",
    at: NOW,
    payload: {
      job_id: 2001,
      stage: "probe",
      subject_label: "Lecture 09",
      level: "info" as const,
      kind: "done",
      message: "Length read: 41 min",
      ...over,
    },
  }),
  snapshot: (s: QueuesSnapshot) => ({ type: "queues.snapshot", at: s.at, payload: s }),
  queueControl: (stage: string | null, paused: boolean) => ({
    type: "queue.control",
    at: NOW,
    payload: { stage, paused, by: "admin" },
  }),
};
