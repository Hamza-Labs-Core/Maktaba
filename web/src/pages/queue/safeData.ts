// Defensive shapes for what the queue pages get from the API. A Go handler
// that returns a nil slice sends `null`, an older server may leave a key
// out, and a proxy's HTML fallback page arrives as a string. These helpers
// turn every list into an array (or reject a payload that is not an object)
// so the pages show their empty states instead of crashing.
import type {
  FailureGroup,
  HealthNow,
  ItemHistoryData,
  JobRef,
  LibraryQueueRow,
  QueueDetailData,
  QueueSummary,
  QueuesSnapshot,
  StageInfo,
  TimelineEntry,
} from "../../lib/queueApi";

export const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);

const isObject = (v: unknown): v is Record<string, unknown> => !!v && typeof v === "object";

function safeJob(j: JobRef): JobRef {
  return j.progress ? { ...j, progress: { ...j.progress, steps: arr(j.progress.steps) } } : j;
}

export function safeStage(s: StageInfo): StageInfo {
  return {
    ...s,
    edges: arr(s.edges),
    subject_kinds: arr(s.subject_kinds),
    fed_by: arr(s.fed_by),
    may_request: arr(s.may_request),
    entry_for: arr(s.entry_for),
  };
}

export function safeSummary(q: QueueSummary): QueueSummary {
  return { ...q, running_jobs: arr(q.running_jobs).map(safeJob) };
}

function safeHealth(h: HealthNow): HealthNow {
  if (!isObject(h)) return h;
  const spark = isObject(h.spark) ? h.spark : ({} as HealthNow["spark"]);
  return {
    ...h,
    spark: {
      nas_open_ms: arr(spark.nas_open_ms),
      nas_timeouts: arr(spark.nas_timeouts),
      cpu_pct: arr(spark.cpu_pct),
      backlog_ready: arr(spark.backlog_ready),
    },
  };
}

/** A usable snapshot, or null when the payload is not one (e.g. an HTML page). */
export function safeSnapshot(s: unknown): QueuesSnapshot | null {
  if (!isObject(s)) return null;
  const x = s as unknown as QueuesSnapshot;
  return {
    ...x,
    stages: arr(x.stages).map(safeStage),
    queues: arr(x.queues).map(safeSummary),
    pools: arr(x.pools).map((p) => ({ ...p, stages: arr(p.stages) })),
    flows: arr(x.flows),
    health: safeHealth(x.health),
  };
}

export function safeGroups(groups: FailureGroup[] | null | undefined): FailureGroup[] {
  return arr(groups).map((g) => ({ ...g, sample_subjects: arr(g.sample_subjects) }));
}

export function safeLibraries(rows: LibraryQueueRow[] | null | undefined): LibraryQueueRow[] {
  return arr(rows).map((r) => ({ ...r, strip: arr(r.strip) }));
}

const NO_KPIS: QueueDetailData["kpis"] = {
  done_24h: 0,
  failed_24h: 0,
  median_duration_ms: null,
  p95_duration_ms: null,
  skipped_share_24h: 0,
  changed_share_24h: 0,
};

/** A usable stage detail, or null when the payload is not one. */
export function safeDetail(d: unknown): QueueDetailData | null {
  if (!isObject(d) || !isObject(d.info) || !isObject(d.summary)) return null;
  const x = d as unknown as QueueDetailData;
  return {
    ...x,
    info: safeStage(x.info),
    summary: safeSummary(x.summary),
    kpis: { ...NO_KPIS, ...(isObject(x.kpis) ? x.kpis : {}) },
    running: arr(x.running).map(safeJob),
    next_up: arr(x.next_up),
    failures: safeGroups(x.failures),
    log: arr(x.log),
  };
}

function safeEntry(e: TimelineEntry): TimelineEntry {
  return {
    ...e,
    job: safeJob(e.job),
    triggered: arr(e.triggered),
    reasons: arr(e.reasons),
    changes: e.changes ? arr(e.changes) : e.changes,
  };
}

export function safeTimeline(entries: TimelineEntry[] | null | undefined): TimelineEntry[] {
  return arr(entries).map(safeEntry);
}

/** A usable item history, or null when the payload is not one. */
export function safeHistory(h: unknown): ItemHistoryData | null {
  if (!isObject(h) || !isObject(h.subject)) return null;
  const x = h as unknown as ItemHistoryData;
  return {
    ...x,
    kinds: arr(x.kinds),
    queues: arr(x.queues),
    seasons: x.seasons ? arr(x.seasons) : x.seasons,
    appears_in: arr(x.appears_in),
    timeline: safeTimeline(x.timeline),
    next: x.next ?? null,
  };
}
