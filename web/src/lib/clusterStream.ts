// Live state of the cluster for the Machines views (design-agent.md §5).
//
//   useClusterStream()  the hub's topics over the shared cluster socket
//                       (clusterSocket.ts): every connected process by
//                       session, a rolling 5-minute history per process
//                       for the sparklines, the LLM request feed, the job
//                       feed, queue and library samples, and — when
//                       subscribed — a live log tail
//                       (`logs:<ip|node_id|session>:<level>`).
//
// The state is a pure reducer (clusterReducer) so it is tested without a
// socket; the hook stamps every action with the receive time.
import { useCallback, useEffect, useReducer, useRef, useState } from "react";
import {
  tsMs,
  type ClusterLogRecord,
  type ClusterMetrics,
  type ClusterProcess,
  type JobEvent,
  type LibrarySample,
  type LibraryStats,
  type LogLevel,
} from "./clusterApi";
import {
  clusterSocket,
  logsTopic,
  useSocketTopic,
  type HubMessage,
  type SocketStatus,
} from "./clusterSocket";
import type { QueuesSnapshot } from "./queueApi";

export const HISTORY_MS = 5 * 60_000;
export const MAX_SAMPLES = 200;
export const MAX_LIVE_LOGS = 2_000;
export const MAX_LLM_FEED = 100;
export const MAX_JOB_EVENTS = 1_000;
export const QUEUE_WINDOW_MS = 60 * 60_000;
export const MAX_QUEUE_POINTS = 1_800;
export const MAX_LIBRARY_POINTS = 720;

// ---------------------------------------------------------------- types

/** The series the sparklines draw. */
export type SeriesKey = "cpu" | "mem" | "gpu" | "rx" | "tx" | "stt" | "tok" | "pcpu";

export interface MetricSample {
  t: number;
  v: Partial<Record<SeriesKey, number>>;
}

export interface LiveProcess extends ClusterProcess {
  online: boolean;
  /** receive time of the last metrics frame (or the connect) */
  seenAt: number;
  disconnectedAt: number | null;
}

export interface LiveLog extends ClusterLogRecord {
  key: string;
  session: string;
  ip: string;
}

export interface LlmRequest {
  id: string;
  model: string;
  op: string;
  from_session: string | null;
  to_session: string | null;
  state: "started" | "done" | "failed";
  usage: { prompt_tokens: number; completion_tokens: number; ms: number } | null;
  error: string | null;
  startedAt: number;
  updatedAt: number;
}

export interface LiveJob extends JobEvent {
  key: string;
  session: string;
  ip: string;
  /** receive time */
  at: number;
}

export interface QueueStagePoint {
  waiting: number;
  running: number;
  /** done per minute */
  rate: number;
}

export interface QueuePoint {
  t: number;
  stages: Record<string, QueueStagePoint>;
}

export interface LibraryPoint {
  t: number;
  totals: LibraryStats;
}

export interface ClusterStreamState {
  /** true once the processes snapshot arrived */
  ready: boolean;
  processes: Record<string, LiveProcess>;
  history: Record<string, MetricSample[]>;
  llm: LlmRequest[];
  logs: LiveLog[];
  jobs: LiveJob[];
  queues: QueuePoint[];
  latestQueues: QueuesSnapshot | null;
  library: LibrarySample | null;
  libraryHistory: LibraryPoint[];
  /** frames the hub dropped for this (slow) viewer */
  dropped: number;
  lastRefused: { reason: string | null; at: number } | null;
}

export const initialClusterState: ClusterStreamState = {
  ready: false,
  processes: {},
  history: {},
  llm: [],
  logs: [],
  jobs: [],
  queues: [],
  latestQueues: null,
  library: null,
  libraryHistory: [],
  dropped: 0,
  lastRefused: null,
};

export type ClusterAction =
  | { type: "event"; name: string; data: unknown; at: number }
  | { type: "batch"; events: { name: string; data: unknown; at: number }[] }
  | { type: "clearLogs" };

/**
 * How often the hook hands the hub's frames to React. Every machine sends
 * a metrics frame every second or two, so one React update per frame made
 * the whole Machines page re-render several times a second; the frames
 * are buffered and applied together, at most once per flushMs. Tests set
 * it to 0 (every frame applied at once).
 */
export const clusterStreamConfig = { flushMs: 1_000 };

// ---------------------------------------------------------------- derive

function fin(v: unknown): number | undefined {
  return typeof v === "number" && Number.isFinite(v) ? v : undefined;
}

/** The sparkline values one metrics frame contributes. */
export function seriesValues(
  m: ClusterMetrics | null | undefined
): Partial<Record<SeriesKey, number>> {
  const out: Partial<Record<SeriesKey, number>> = {};
  if (!m) return out;
  const sys = m.system;
  const cpu = fin(sys?.cpu_pct);
  if (cpu !== undefined) out.cpu = cpu;
  const total = fin(sys?.mem_total);
  const avail = fin(sys?.mem_available);
  if (total !== undefined && avail !== undefined && total > 0)
    out.mem = ((total - avail) / total) * 100;
  const nvidia = m.gpu?.nvidia ?? [];
  if (nvidia.length > 0) {
    out.gpu = Math.max(...nvidia.map((g) => fin(g.util_pct) ?? 0));
  } else {
    const active = fin(m.gpu?.apple?.active_gb);
    if (active !== undefined) out.gpu = active;
  }
  const rx = fin(sys?.net?.rx_bps);
  if (rx !== undefined) out.rx = rx;
  const tx = fin(sys?.net?.tx_bps);
  if (tx !== undefined) out.tx = tx;
  const stt = fin(m.pipeline?.stt?.audio_sec_per_s);
  if (stt !== undefined) out.stt = stt;
  const tok = fin(m.llm?.tokens_per_s);
  if (tok !== undefined) out.tok = tok;
  const pcpu = fin(m.process?.cpu_pct);
  if (pcpu !== undefined) out.pcpu = pcpu;
  return out;
}

/** One series of a process's history, oldest first. */
export function series(samples: MetricSample[] | undefined, key: SeriesKey): number[] {
  if (!samples) return [];
  const out: number[] = [];
  for (const s of samples) {
    const v = s.v[key];
    if (v !== undefined) out.push(v);
  }
  return out;
}

/** A queue snapshot reduced to what the charts draw. */
export function queuePoint(snapshot: QueuesSnapshot, t: number): QueuePoint {
  const stages: Record<string, QueueStagePoint> = {};
  for (const q of snapshot?.queues ?? []) {
    stages[q.stage] = {
      waiting: q.counts?.pending ?? 0,
      running: q.counts?.running ?? 0,
      rate: fin(q.rate_per_min) ?? 0,
    };
  }
  return { t, stages };
}

const LIBRARY_KEYS: (keyof LibraryStats)[] = [
  "videos",
  "hours",
  "bytes",
  "transcribed_videos",
  "transcribed_hours",
  "series",
  "seasons",
  "tagged",
  "indexed",
];

/** The sample's totals, or the sum of its libraries when the totals are missing. */
export function libraryTotals(s: Pick<LibrarySample, "libraries" | "totals">): LibraryStats {
  if (s.totals && Object.keys(s.totals).length > 0) return s.totals;
  const sum: Record<string, number> = {};
  for (const lib of s.libraries ?? []) {
    for (const k of LIBRARY_KEYS) {
      const v = fin(lib[k]);
      if (v !== undefined) sum[k] = (sum[k] ?? 0) + v;
    }
  }
  return sum as LibraryStats;
}

const LEVEL_RANK: Record<string, number> = {
  debug: 10,
  info: 20,
  warning: 30,
  warn: 30,
  error: 40,
  critical: 50,
};

/** true when the record's level is at or above `min`. */
export function atLeast(level: string, min: LogLevel | "" | undefined): boolean {
  if (!min) return true;
  return (LEVEL_RANK[String(level).toLowerCase()] ?? 20) >= LEVEL_RANK[min];
}

/** Every process grouped by machine: the home first, then nodes by name. */
export interface MachineGroup {
  key: string;
  kind: "home" | "node";
  nodeId: string | null;
  name: string;
  ip: string;
  processes: LiveProcess[];
}

export function groupByMachine(processes: Record<string, LiveProcess>): MachineGroup[] {
  const groups = new Map<string, MachineGroup>();
  for (const p of Object.values(processes)) {
    const key = p.kind === "home" ? "home" : `node:${p.node_id ?? p.ip}`;
    let g = groups.get(key);
    if (!g) {
      g = {
        key,
        kind: p.kind,
        nodeId: p.node_id,
        name: p.node_name || p.host || p.ip,
        ip: p.ip,
        processes: [],
      };
      groups.set(key, g);
    }
    g.processes.push(p);
  }
  const all = [...groups.values()];
  for (const g of all) g.processes.sort((a, b) => a.worker_id.localeCompare(b.worker_id));
  return all.sort((a, b) =>
    a.kind === b.kind ? a.name.localeCompare(b.name) : a.kind === "home" ? -1 : 1
  );
}

// ---------------------------------------------------------------- reducer

let seq = 0;

function obj(v: unknown): Record<string, unknown> | null {
  return v && typeof v === "object" && !Array.isArray(v) ? (v as Record<string, unknown>) : null;
}

function asProcess(v: unknown, at: number): LiveProcess | null {
  const p = obj(v);
  if (!p || typeof p.session !== "string") return null;
  const proc = p as unknown as ClusterProcess;
  return { ...proc, online: true, seenAt: at, disconnectedAt: null };
}

function pushSample(
  history: Record<string, MetricSample[]>,
  session: string,
  m: ClusterMetrics | null | undefined,
  at: number
): Record<string, MetricSample[]> {
  const v = seriesValues(m);
  if (Object.keys(v).length === 0) return history;
  const prev = history[session] ?? [];
  const cutoff = at - HISTORY_MS;
  const kept = prev.filter((s) => s.t >= cutoff);
  return { ...history, [session]: [...kept, { t: at, v }].slice(-MAX_SAMPLES) };
}

function capped<T>(list: T[], item: T, max: number): T[] {
  return list.length >= max ? [...list.slice(list.length - max + 1), item] : [...list, item];
}

function onEvent(
  s: ClusterStreamState,
  name: string,
  data: unknown,
  at: number
): ClusterStreamState {
  const d = obj(data);
  switch (name) {
    case "snapshot": {
      const items = Array.isArray(d?.processes) ? d.processes : [];
      const processes: Record<string, LiveProcess> = {};
      let history: Record<string, MetricSample[]> = {};
      for (const raw of items) {
        const p = asProcess(raw, at);
        if (!p) continue;
        processes[p.session] = p;
        history[p.session] = s.history[p.session] ?? [];
        history = pushSample(history, p.session, p.metrics, at);
      }
      return { ...s, ready: true, processes, history };
    }
    case "metricsSnapshot": {
      // the metrics topic's snapshot: the latest frame per process
      const items = Array.isArray(d?.processes) ? d.processes : [];
      let next = s;
      for (const raw of items) {
        const item = obj(raw);
        if (!item || typeof item.session !== "string") continue;
        if (!next.processes[item.session] && typeof item.worker_id === "string") {
          const p = asProcess(item, at);
          if (p) next = { ...next, processes: { ...next.processes, [p.session]: p } };
        }
        next = onEvent(next, "metrics", { session: item.session, metrics: item.metrics }, at);
      }
      return next;
    }
    case "metrics": {
      if (!d || typeof d.session !== "string") return s;
      const cur = s.processes[d.session];
      const m = (obj(d.metrics) ?? null) as ClusterMetrics | null;
      const history = pushSample(s.history, d.session, m, at);
      if (!cur) return { ...s, history };
      return {
        ...s,
        history,
        processes: { ...s.processes, [d.session]: { ...cur, metrics: m, seenAt: at } },
      };
    }
    case "process": {
      if (!d) return s;
      if (d.event === "refused") {
        return {
          ...s,
          lastRefused: { reason: typeof d.reason === "string" ? d.reason : null, at },
        };
      }
      const session = typeof d.session === "string" ? d.session : null;
      if (d.event === "connected") {
        const p = asProcess(d.process ?? (session ? { session } : null), at);
        if (!p) return s;
        return { ...s, processes: { ...s.processes, [p.session]: p } };
      }
      if (d.event === "disconnected" && session && s.processes[session]) {
        return {
          ...s,
          processes: {
            ...s.processes,
            [session]: { ...s.processes[session], online: false, disconnectedAt: at },
          },
        };
      }
      return s;
    }
    case "log": {
      const rec = obj(d?.record);
      if (!d || !rec) return s;
      const log: LiveLog = {
        ...(rec as unknown as ClusterLogRecord),
        key: `l${++seq}`,
        session: typeof d.session === "string" ? d.session : "",
        ip: typeof d.ip === "string" ? d.ip : "",
      };
      return { ...s, logs: capped(s.logs, log, MAX_LIVE_LOGS) };
    }
    case "llm": {
      if (!d || typeof d.id !== "string") return s;
      const i = s.llm.findIndex((r) => r.id === d.id);
      const prev = i >= 0 ? s.llm[i] : null;
      const state = d.state === "done" || d.state === "failed" ? d.state : "started";
      const next: LlmRequest = {
        id: d.id,
        model: typeof d.model === "string" ? d.model : (prev?.model ?? ""),
        op: typeof d.op === "string" ? d.op : (prev?.op ?? ""),
        from_session:
          typeof d.from_session === "string" ? d.from_session : (prev?.from_session ?? null),
        to_session: typeof d.to_session === "string" ? d.to_session : (prev?.to_session ?? null),
        state,
        usage: (obj(d.usage) as LlmRequest["usage"]) ?? prev?.usage ?? null,
        error: typeof d.error === "string" ? d.error : null,
        startedAt: prev?.startedAt ?? at,
        updatedAt: at,
      };
      const rest = i >= 0 ? [...s.llm.slice(0, i), ...s.llm.slice(i + 1)] : s.llm;
      return { ...s, llm: [next, ...rest].slice(0, MAX_LLM_FEED) };
    }
    case "job": {
      const job = obj(d?.job);
      if (!d || !job) return s;
      const live: LiveJob = {
        ...(job as unknown as JobEvent),
        key: `j${++seq}`,
        session: typeof d.session === "string" ? d.session : "",
        ip: typeof d.ip === "string" ? d.ip : "",
        at,
      };
      return { ...s, jobs: capped(s.jobs, live, MAX_JOB_EVENTS) };
    }
    case "queues": {
      // the snapshot itself, or {ts, snapshot}
      const snap = (Array.isArray(d?.queues) ? d : obj(d?.snapshot)) as QueuesSnapshot | null;
      if (!d || !snap) return s;
      const stamp = (d.ts ?? (snap as { at?: string }).at) as string | number | undefined;
      const t = Number.isFinite(tsMs(stamp)) ? tsMs(stamp) : at;
      const cutoff = t - QUEUE_WINDOW_MS;
      const queues = [...s.queues.filter((p) => p.t >= cutoff), queuePoint(snap, t)].slice(
        -MAX_QUEUE_POINTS
      );
      return { ...s, queues, latestQueues: snap };
    }
    case "library": {
      if (!d || !Array.isArray(d.libraries)) return s;
      const sample: LibrarySample = {
        ts: (d.ts as string | number) ?? at,
        libraries: d.libraries as LibraryStats[],
        totals: (obj(d.totals) as LibraryStats) ?? null,
      };
      const t = Number.isFinite(tsMs(sample.ts)) ? tsMs(sample.ts) : at;
      const libraryHistory = [...s.libraryHistory, { t, totals: libraryTotals(sample) }].slice(
        -MAX_LIBRARY_POINTS
      );
      return { ...s, library: sample, libraryHistory };
    }
    case "dropped":
      return { ...s, dropped: s.dropped + (fin(d?.count) ?? 0) };
    default:
      return s;
  }
}

export function clusterReducer(s: ClusterStreamState, a: ClusterAction): ClusterStreamState {
  switch (a.type) {
    case "clearLogs":
      return { ...s, logs: [] };
    case "event":
      return onEvent(s, a.name, a.data, a.at);
    case "batch":
      return a.events.reduce((acc, e) => onEvent(acc, e.name, e.data, e.at), s);
  }
}

// ---------------------------------------------------------------- hook

export interface LogSubscription {
  /** an IP (a machine), a node id or a session (one process) */
  target: string;
  level: LogLevel;
}

/** The topics the Machines views listen to (the logs topic joins when subscribed). */
export const CLUSTER_TOPICS = ["processes", "metrics", "llm", "jobs", "queues", "library"];

function listOf(data: unknown, key: string): unknown[] {
  if (Array.isArray(data)) return data;
  const o = obj(data);
  return o && Array.isArray(o[key]) ? (o[key] as unknown[]) : [];
}

/** A hub message as reducer events (a snapshot can carry many). */
export function hubEvents(msg: HubMessage): { name: string; data: unknown }[] {
  if (msg.type !== "snapshot") return [{ name: msg.type, data: msg.data }];
  const topic = msg.topic ?? "";
  const data = msg.data;
  if (topic === "processes")
    return [{ name: "snapshot", data: { processes: listOf(data, "processes") } }];
  if (topic === "metrics")
    return [{ name: "metricsSnapshot", data: { processes: listOf(data, "processes") } }];
  if (topic === "queues" || topic === "library") return data ? [{ name: topic, data }] : [];
  if (topic === "llm") return listOf(data, "requests").map((r) => ({ name: "llm", data: r }));
  if (topic === "jobs")
    return listOf(data, "jobs").map((j) => ({ name: "job", data: obj(j)?.job ? j : { job: j } }));
  if (topic.startsWith("logs:"))
    return listOf(data, "records").map((r) => ({
      name: "log",
      data: obj(r)?.record ? r : { record: r },
    }));
  return [];
}

export interface ClusterStream extends ClusterStreamState {
  status: SocketStatus;
  logSubscription: LogSubscription | null;
  /** Moves the logs topic to the new subscription (null: no logs); the tail starts empty. */
  subscribeLogs: (sub: LogSubscription | null) => void;
  clearLogs: () => void;
}

export function useClusterStream(): ClusterStream {
  const [state, dispatch] = useReducer(clusterReducer, initialClusterState);
  const [sub, setSub] = useState<LogSubscription | null>(null);
  const topics = sub ? [...CLUSTER_TOPICS, logsTopic(sub.target, sub.level)] : CLUSTER_TOPICS;

  // frames wait here until the next flush: one React update per flushMs
  const buf = useRef<{ name: string; data: unknown; at: number }[]>([]);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const flush = useCallback(() => {
    timer.current = null;
    const events = buf.current;
    buf.current = [];
    if (events.length) dispatch({ type: "batch", events });
  }, []);
  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current);
      timer.current = null;
    },
    []
  );

  const status = useSocketTopic(topics, (msg) => {
    const at = Date.now();
    for (const ev of hubEvents(msg)) buf.current.push({ name: ev.name, data: ev.data, at });
    const ms = clusterStreamConfig.flushMs;
    if (ms <= 0) flush();
    else if (!timer.current) timer.current = setTimeout(flush, ms);
  });

  // a new logs topic starts with an empty tail
  useEffect(() => {
    buf.current = buf.current.filter((e) => e.name !== "log");
    dispatch({ type: "clearLogs" });
  }, [sub?.target, sub?.level]);

  const subscribeLogs = useCallback((next: LogSubscription | null) => {
    setSub((prev) => (prev?.target === next?.target && prev?.level === next?.level ? prev : next));
  }, []);
  const clearLogs = useCallback(() => dispatch({ type: "clearLogs" }), []);

  return { ...state, status, logSubscription: sub, subscribeLogs, clearLogs };
}

/** Sends a per-process command over the hub (REST while the socket is down). */
export function runCommand(
  session: string,
  name: Parameters<typeof clusterSocket.cmd>[1],
  args?: Record<string, unknown>
) {
  return clusterSocket.cmd(session, name, args);
}
