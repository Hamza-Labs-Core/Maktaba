// Live data hooks for the queue control centre (plan 06-11 §9.3), fed by
// the cluster hub's WebSocket (design-agent.md §5) with the older SSE
// stream as the fallback.
//
//   useQueuesSnapshot()  GET /api/queues once, then the hub's `queues`
//                        topic (pushed as it changes). While the socket is
//                        down or missing: the 2 s `queues.snapshot` SSE
//                        stream, and only if that is silent for 10 s we
//                        poll /api/queues every 5 s, until a snapshot
//                        arrives.
//   useJobStream(filter) the hub's `jobs` topic (every job event from every
//                        machine), or the `jobs` SSE channel; batched to
//                        one React update per second; keeps the newest 500
//                        log lines, the latest JobRef per job id and the
//                        jobs running now with the machine they run on.
//   useWorkers()         the hub's `workers` topic, else GET /api/workers
//                        every 30 s.
import { useEffect, useRef, useState } from "react";
import type { JobEvent } from "./clusterApi";
import { socketFallback, useSocketTopic, type HubMessage } from "./clusterSocket";
import { queueApi } from "./queueApi";
import type { JobRef, JobState, LogLine, QueuesSnapshot, WorkerHost } from "./queueApi";
import { subscribe, type ServerEvent } from "./ws";

export const SNAPSHOT_SILENCE_MS = 10_000;
export const SNAPSHOT_POLL_MS = 5_000;
export const STREAM_FLUSH_MS = 1_000;
export const MAX_LOG_LINES = 500;
export const WORKERS_POLL_MS = 30_000;

export interface SnapshotState {
  snapshot: QueuesSnapshot | null;
  error: boolean;
  /** true while the live streams are silent and we are polling instead. */
  polling: boolean;
  reload: () => void;
}

function isSnapshot(v: unknown): v is QueuesSnapshot {
  return !!v && typeof v === "object" && Array.isArray((v as QueuesSnapshot).queues);
}

/** The queue snapshot a hub message carries, if any. */
function hubSnapshot(msg: HubMessage): QueuesSnapshot | null {
  if (msg.type !== "queues" && msg.type !== "snapshot") return null;
  const d = msg.data as { snapshot?: unknown } | undefined;
  if (isSnapshot(d)) return d;
  return isSnapshot(d?.snapshot) ? d.snapshot : null;
}

export function useQueuesSnapshot(): SnapshotState {
  const [snapshot, setSnapshot] = useState<QueuesSnapshot | null>(null);
  const [error, setError] = useState(false);
  const [polling, setPolling] = useState(false);
  const alive = useRef(true);

  const fetchOnce = useRef(() =>
    queueApi
      .snapshot()
      .then((s) => {
        if (!alive.current) return;
        setSnapshot(s);
        setError(false);
      })
      .catch(() => {
        if (alive.current) setError(true);
      })
  );

  useEffect(() => {
    alive.current = true;
    void fetchOnce.current();
    return () => {
      alive.current = false;
    };
  }, []);

  const status = useSocketTopic("queues", (msg) => {
    const s = hubSnapshot(msg);
    if (!s) return;
    setSnapshot(s);
    setError(false);
  });
  const sse = socketFallback(status);

  useEffect(() => {
    if (!sse) {
      setPolling(false);
      return;
    }
    let silence: ReturnType<typeof setTimeout> | null = null;
    let poll: ReturnType<typeof setInterval> | null = null;

    const stopPolling = () => {
      if (poll) clearInterval(poll);
      poll = null;
      setPolling(false);
    };
    const armSilence = () => {
      if (silence) clearTimeout(silence);
      silence = setTimeout(() => {
        if (!alive.current || poll) return;
        setPolling(true);
        poll = setInterval(() => void fetchOnce.current(), SNAPSHOT_POLL_MS);
      }, SNAPSHOT_SILENCE_MS);
    };

    armSilence();
    const off = subscribe("queues", (ev: ServerEvent) => {
      if (ev.type !== "queues.snapshot" || !ev.payload) return;
      stopPolling();
      armSilence();
      setSnapshot(ev.payload as unknown as QueuesSnapshot);
      setError(false);
    });
    return () => {
      off();
      if (silence) clearTimeout(silence);
      if (poll) clearInterval(poll);
    };
  }, [sse]);

  return { snapshot, error, polling, reload: () => void fetchOnce.current() };
}

// ---- job stream ----------------------------------------------------------

export interface LiveLine extends Omit<LogLine, "id"> {
  /** Client-side key; the live payloads carry no row id. */
  id: string;
}

/** A job running now, as the hub reports it, with where it runs. */
export interface LiveRun {
  job: JobRef;
  session: string | null;
  ip: string | null;
  worker_id: string | null;
  /** the machine's name when the hub sends it, else its IP */
  machine: string | null;
}

export interface JobStreamFilter {
  stage?: string;
  subjectId?: string;
  /** Test hook; production uses STREAM_FLUSH_MS. */
  flushMs?: number;
}

export interface JobStreamState {
  /** Newest first, capped at MAX_LOG_LINES. */
  lines: LiveLine[];
  /** Latest JobRef per job id seen on the stream. */
  jobs: ReadonlyMap<number, JobRef>;
  /** Jobs running now (hub only), by job id. */
  running: ReadonlyMap<number, LiveRun>;
  /** Bumps once per flush that contained a finished or failed job. */
  finishedTick: number;
}

let lineSeq = 0;

const EVENT_STATE: Record<JobEvent["event"], JobState> = {
  claimed: "running",
  progress: "running",
  done: "done",
  failed: "failed",
  skipped: "cancelled",
  paused: "paused",
  deferred: "pending",
};

const TERMINAL = new Set(["done", "failed", "skipped", "paused", "deferred"]);

function str(v: unknown): string | null {
  return typeof v === "string" && v !== "" ? v : null;
}

function isoOf(ts: unknown): string {
  if (typeof ts === "number") return new Date(ts < 1e12 ? ts * 1000 : ts).toISOString();
  return typeof ts === "string" && ts ? ts : new Date().toISOString();
}

/** A hub job event as the JobRef the queue views draw, merged onto the last one. */
export function jobRefOf(ev: JobEvent, prev?: JobRef): JobRef {
  const id = Number(ev.job_id);
  const state = EVENT_STATE[ev.event] ?? prev?.state ?? "running";
  const p = ev.progress;
  return {
    id,
    stage: ev.stage ?? prev?.stage ?? "",
    state,
    subject_kind: (ev.subject_kind ?? prev?.subject_kind ?? "video") as JobRef["subject_kind"],
    subject_id: String(ev.subject_id ?? prev?.subject_id ?? ""),
    subject_label: ev.title ?? prev?.subject_label ?? String(ev.subject_id ?? ""),
    library_id: prev?.library_id ?? null,
    priority: prev?.priority ?? 0,
    attempts: ev.attempt ?? prev?.attempts ?? 1,
    max_attempts: prev?.max_attempts ?? Math.max(1, ev.attempt ?? 1),
    outcome: ev.outcome?.kind ?? prev?.outcome ?? null,
    error_cause: ev.outcome?.error ?? prev?.error_cause ?? null,
    created_at: prev?.created_at ?? ev.started_at ?? isoOf(ev.ts),
    started_at: ev.started_at ?? prev?.started_at ?? null,
    finished_at: TERMINAL.has(ev.event) ? isoOf(ev.ts) : null,
    progress: p
      ? {
          step: p.step,
          steps: prev?.progress?.steps ?? [],
          done_units: p.done,
          total_units: p.total,
        }
      : (prev?.progress ?? null),
  };
}

interface HubJob {
  ev: JobEvent;
  session: string | null;
  ip: string | null;
  worker_id: string | null;
  machine: string | null;
  /** from the snapshot's `running` (a job running now, not an event): it seeds
   * the running map and writes no activity line */
  seed?: boolean;
}

/** A jobs snapshot starts the running map afresh. */
interface ResetRunning {
  reset: true;
}

function hubJobs(msg: HubMessage): HubJob[] {
  const wrap = (raw: unknown): HubJob | null => {
    if (!raw || typeof raw !== "object") return null;
    const d = raw as Record<string, unknown>;
    const ev = (d.job && typeof d.job === "object" ? d.job : d) as JobEvent;
    if (ev.job_id === undefined || ev.job_id === null) return null;
    const e = ev as unknown as Record<string, unknown>;
    return {
      ev,
      session: str(d.session),
      ip: str(d.ip),
      worker_id: str(d.worker_id) ?? str(e.worker_id),
      machine: str(d.node_name) ?? str(d.host) ?? str(e.node_name) ?? str(e.host) ?? str(d.ip),
    };
  };
  if (msg.type === "job") {
    const j = wrap(msg.data);
    return j ? [j] : [];
  }
  if (msg.type === "snapshot") {
    const d = msg.data as { jobs?: unknown[]; running?: unknown[] } | unknown[] | undefined;
    const list = Array.isArray(d) ? d : Array.isArray(d?.jobs) ? d.jobs : [];
    const events = list.map(wrap).filter((j): j is HubJob => j !== null);
    // the jobs running now on every machine (from each process's latest
    // metrics): a page that loads mid-job shows it even when its claimed
    // event is no longer among the recent events
    const running = !Array.isArray(d) && Array.isArray(d?.running) ? d.running : [];
    const seeds = running
      .map(wrap)
      .filter((j): j is HubJob => j !== null)
      .map((j) => ({ ...j, seed: true }));
    return [...events, ...seeds];
  }
  return [];
}

export function useJobStream(filter: JobStreamFilter = {}): JobStreamState {
  const { stage, subjectId, flushMs = STREAM_FLUSH_MS } = filter;
  const [state, setState] = useState<JobStreamState>({
    lines: [],
    jobs: new Map(),
    running: new Map(),
    finishedTick: 0,
  });
  const buf = useRef<(ServerEvent | HubJob | ResetRunning)[]>([]);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const flushRef = useRef<() => void>(() => {});

  // The flush reads the filter of the current render.
  flushRef.current = () => {
    timer.current = null;
    const batch = buf.current;
    buf.current = [];
    setState((prev) => {
      const jobs = new Map(prev.jobs);
      const running = new Map(prev.running);
      const fresh: LiveLine[] = [];
      let finished = false;
      for (const item of batch) {
        if ("reset" in item) {
          running.clear();
          continue;
        }
        if ("ev" in item) {
          const { ev } = item;
          if (stage && ev.stage !== stage) continue;
          if (subjectId && String(ev.subject_id) !== subjectId) continue;
          const id = Number(ev.job_id);
          const ref = jobRefOf(ev, jobs.get(id));
          jobs.set(id, ref);
          if (item.seed) {
            running.set(id, {
              job: ref,
              session: item.session,
              ip: item.ip,
              worker_id: item.worker_id,
              machine: item.machine,
            });
            continue;
          }
          if (TERMINAL.has(ev.event)) running.delete(id);
          else
            running.set(id, {
              job: ref,
              session: item.session,
              ip: item.ip,
              worker_id: item.worker_id,
              machine: item.machine,
            });
          if (ev.event === "done" || ev.event === "failed") finished = true;
          if (ev.event !== "progress") {
            fresh.push({
              id: `live-${++lineSeq}`,
              at: isoOf(ev.ts),
              job_id: id,
              stage: ev.stage,
              subject_kind: ev.subject_kind ?? "",
              subject_id: String(ev.subject_id ?? ""),
              subject_label: ev.title ?? "",
              level: ev.event === "failed" ? "error" : ev.event === "deferred" ? "warn" : "info",
              kind: `hubjob:${ev.event}`,
              message: ev.outcome?.error ?? "",
              data: { machine: item.machine, duration_ms: ev.duration_ms ?? null },
            });
          }
          continue;
        }
        const ev = item;
        const p = ev.payload as Record<string, unknown> | undefined;
        if (!p) continue;
        if (ev.type === "job.log") {
          if (stage && p.stage !== stage) continue;
          fresh.push({
            id: `live-${++lineSeq}`,
            at: ev.at,
            job_id: p.job_id as number,
            stage: p.stage as string,
            subject_kind: "",
            subject_id: "",
            subject_label: (p.subject_label as string) ?? "",
            level: p.level as LogLine["level"],
            kind: p.kind as string,
            message: p.message as string,
            data: (p.data as Record<string, unknown> | undefined) ?? null,
          });
          continue;
        }
        const job = p.job as JobRef | undefined;
        if (!job) continue;
        if (stage && job.stage !== stage) continue;
        if (subjectId && job.subject_id !== subjectId) continue;
        jobs.set(job.id, job);
        if (ev.type === "job.finished" || ev.type === "job.failed") finished = true;
      }
      while (jobs.size > 500) jobs.delete(jobs.keys().next().value as number);
      // batch is oldest-first; the list is newest-first.
      const lines = fresh.length
        ? [...fresh.reverse(), ...prev.lines].slice(0, MAX_LOG_LINES)
        : prev.lines;
      return {
        lines,
        jobs,
        running,
        finishedTick: prev.finishedTick + (finished ? 1 : 0),
      };
    });
  };

  const push = (item: ServerEvent | HubJob | ResetRunning) => {
    buf.current.push(item);
    if (!timer.current) timer.current = setTimeout(() => flushRef.current(), flushMs);
  };

  const status = useSocketTopic("jobs", (msg) => {
    if (msg.type === "snapshot") push({ reset: true });
    hubJobs(msg).forEach(push);
  });
  const sse = socketFallback(status);

  useEffect(() => {
    if (!sse) return;
    return subscribe("jobs", (ev) => {
      if (ev.type.startsWith("job.")) push(ev);
    });
    // push only touches refs
  }, [sse, stage, subjectId]);

  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current);
      timer.current = null;
    },
    []
  );

  return state;
}

// ---- workers -------------------------------------------------------------

/** The machines' workers: the hub's `workers` topic, else GET /api/workers every 30 s. */
export function useWorkers(): WorkerHost[] | null {
  const [workers, setWorkers] = useState<WorkerHost[] | null>(null);
  // once the hub has spoken, a slower REST answer must not overwrite it
  const fromHub = useRef(false);
  const status = useSocketTopic("workers", (msg) => {
    if (msg.type !== "workers" && msg.type !== "snapshot") return;
    const d = msg.data as { workers?: unknown } | unknown[] | undefined;
    const list = Array.isArray(d) ? d : Array.isArray(d?.workers) ? d.workers : null;
    if (!list) return;
    fromHub.current = true;
    setWorkers(list as WorkerHost[]);
  });
  const viaSocket = status === "open";

  useEffect(() => {
    if (viaSocket) return;
    fromHub.current = false;
    let alive = true;
    const load = () =>
      queueApi
        .workers()
        .then((r) => alive && !fromHub.current && setWorkers(r.workers))
        .catch(() => {});
    load();
    const id = setInterval(load, WORKERS_POLL_MS);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, [viaSocket]);

  return workers;
}

/** A running job's machine: the worker's machine name when known, else what the hub sent. */
export function runMachine(run: LiveRun, workers: WorkerHost[] | null): string | null {
  const w = run.worker_id ? workers?.find((x) => x.worker_id === run.worker_id) : undefined;
  return (w && (w.node_name || w.host)) || run.machine;
}
