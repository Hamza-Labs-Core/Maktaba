// Live data hooks for the queue control centre (plan 06-11 §9.3).
//
//   useQueuesSnapshot()  GET /api/queues once, then the 2 s
//                        `queues.snapshot` SSE stream. Only if the
//                        stream is silent for 10 s do we poll
//                        /api/queues every 5 s, until a snapshot arrives.
//   useJobStream(filter) the `jobs` SSE channel, batched to one React
//                        update per second; keeps the newest 500 log
//                        lines and the latest JobRef per job id.
import { useEffect, useRef, useState } from "react";
import { queueApi } from "./queueApi";
import type { JobRef, LogLine, QueuesSnapshot } from "./queueApi";
import { subscribe, type ServerEvent } from "./ws";

export const SNAPSHOT_SILENCE_MS = 10_000;
export const SNAPSHOT_POLL_MS = 5_000;
export const STREAM_FLUSH_MS = 1_000;
export const MAX_LOG_LINES = 500;

export interface SnapshotState {
  snapshot: QueuesSnapshot | null;
  error: boolean;
  /** true while the SSE stream is silent and we are polling instead. */
  polling: boolean;
  reload: () => void;
}

export function useQueuesSnapshot(): SnapshotState {
  const [snapshot, setSnapshot] = useState<QueuesSnapshot | null>(null);
  const [error, setError] = useState(false);
  const [polling, setPolling] = useState(false);
  const reloadRef = useRef<() => void>(() => {});

  useEffect(() => {
    let alive = true;
    let silence: ReturnType<typeof setTimeout> | null = null;
    let poll: ReturnType<typeof setInterval> | null = null;

    const fetchOnce = () =>
      queueApi
        .snapshot()
        .then((s) => {
          if (!alive) return;
          setSnapshot(s);
          setError(false);
        })
        .catch(() => {
          if (alive) setError(true);
        });
    reloadRef.current = () => void fetchOnce();

    const stopPolling = () => {
      if (poll) clearInterval(poll);
      poll = null;
      setPolling(false);
    };
    const armSilence = () => {
      if (silence) clearTimeout(silence);
      silence = setTimeout(() => {
        if (!alive || poll) return;
        setPolling(true);
        poll = setInterval(() => void fetchOnce(), SNAPSHOT_POLL_MS);
      }, SNAPSHOT_SILENCE_MS);
    };

    void fetchOnce();
    armSilence();
    const off = subscribe("queues", (ev: ServerEvent) => {
      if (ev.type !== "queues.snapshot" || !ev.payload) return;
      stopPolling();
      armSilence();
      setSnapshot(ev.payload as unknown as QueuesSnapshot);
      setError(false);
    });
    return () => {
      alive = false;
      off();
      if (silence) clearTimeout(silence);
      if (poll) clearInterval(poll);
    };
  }, []);

  return { snapshot, error, polling, reload: () => reloadRef.current() };
}

// ---- job stream ----------------------------------------------------------

export interface LiveLine extends Omit<LogLine, "id"> {
  /** Client-side key; the SSE job.log payload carries no row id. */
  id: string;
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
  /** Bumps once per flush that contained a job.finished / job.failed. */
  finishedTick: number;
}

let lineSeq = 0;

export function useJobStream(filter: JobStreamFilter = {}): JobStreamState {
  const { stage, subjectId, flushMs = STREAM_FLUSH_MS } = filter;
  const [state, setState] = useState<JobStreamState>({
    lines: [],
    jobs: new Map(),
    finishedTick: 0,
  });

  useEffect(() => {
    let buf: ServerEvent[] = [];
    let timer: ReturnType<typeof setTimeout> | null = null;

    const flush = () => {
      timer = null;
      const batch = buf;
      buf = [];
      setState((prev) => {
        const jobs = new Map(prev.jobs);
        const fresh: LiveLine[] = [];
        let finished = false;
        for (const ev of batch) {
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
        return { lines, jobs, finishedTick: prev.finishedTick + (finished ? 1 : 0) };
      });
    };

    const off = subscribe("jobs", (ev) => {
      if (!ev.type.startsWith("job.")) return;
      buf.push(ev);
      if (!timer) timer = setTimeout(flush, flushMs);
    });
    return () => {
      off();
      if (timer) clearTimeout(timer);
    };
  }, [stage, subjectId, flushMs]);

  return state;
}
