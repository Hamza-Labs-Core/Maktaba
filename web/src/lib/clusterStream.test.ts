// The cluster views' live state, as a pure reducer over hub events.
import { describe, it, expect } from "vitest";
import {
  atLeast,
  clusterReducer,
  groupByMachine,
  hubEvents,
  HISTORY_MS,
  initialClusterState,
  libraryTotals,
  MAX_LIVE_LOGS,
  series,
  seriesValues,
  type ClusterStreamState,
} from "./clusterStream";
import type { ClusterProcess } from "./clusterApi";

const proc = (over: Partial<ClusterProcess> = {}): ClusterProcess => ({
  session: "s1",
  kind: "node",
  node_id: "n1",
  node_name: "Studio Mac",
  ip: "10.0.0.5",
  host: "studio",
  worker_id: "node-n1-1",
  pid: 42,
  connected_at: new Date().toISOString(),
  metrics: { system: { cpu_pct: 10, mem_total: 100, mem_available: 75 } },
  llm: null,
  ...over,
});

function run(events: { name: string; data: unknown; at?: number }[]): ClusterStreamState {
  return events.reduce(
    (s, e) => clusterReducer(s, { type: "event", name: e.name, data: e.data, at: e.at ?? 1_000 }),
    initialClusterState
  );
}

describe("clusterReducer", () => {
  it("takes the processes from the snapshot with a first sample", () => {
    const s = run([
      { name: "snapshot", data: { processes: [proc(), proc({ session: "s2", kind: "home" })] } },
    ]);
    expect(s.ready).toBe(true);
    expect(Object.keys(s.processes)).toEqual(["s1", "s2"]);
    expect(s.processes.s1.online).toBe(true);
    expect(series(s.history.s1, "cpu")).toEqual([10]);
    expect(series(s.history.s1, "mem")).toEqual([25]);
  });

  it("adds metrics to the rolling history and drops samples older than 5 minutes", () => {
    const s = run([
      { name: "snapshot", data: { processes: [proc()] }, at: 0 },
      { name: "metrics", data: { session: "s1", metrics: { system: { cpu_pct: 50 } } }, at: 2_000 },
      {
        name: "metrics",
        data: { session: "s1", metrics: { system: { cpu_pct: 70 }, llm: { tokens_per_s: 12 } } },
        at: HISTORY_MS + 1_000,
      },
    ]);
    expect(series(s.history.s1, "cpu")).toEqual([50, 70]);
    expect(series(s.history.s1, "tok")).toEqual([12]);
    expect(s.processes.s1.metrics?.system?.cpu_pct).toBe(70);
  });

  it("marks a process disconnected and adds a newly connected one", () => {
    const s = run([
      { name: "snapshot", data: { processes: [proc()] } },
      { name: "process", data: { event: "disconnected", session: "s1" }, at: 5_000 },
      {
        name: "process",
        data: { event: "connected", session: "s3", process: proc({ session: "s3" }) },
      },
      { name: "process", data: { event: "refused", session: null, reason: "bad key" } },
    ]);
    expect(s.processes.s1.online).toBe(false);
    expect(s.processes.s1.disconnectedAt).toBe(5_000);
    expect(s.processes.s3.online).toBe(true);
    expect(s.lastRefused?.reason).toBe("bad key");
  });

  it("keeps at most MAX_LIVE_LOGS log lines", () => {
    let s = initialClusterState;
    for (let i = 0; i < MAX_LIVE_LOGS + 5; i++)
      s = clusterReducer(s, {
        type: "event",
        name: "log",
        data: { session: "s1", ip: "10.0.0.5", record: { ts: i, level: "info", event: `e${i}` } },
        at: i,
      });
    expect(s.logs).toHaveLength(MAX_LIVE_LOGS);
    expect(s.logs[0].event).toBe("e5");
    expect(clusterReducer(s, { type: "clearLogs" }).logs).toEqual([]);
  });

  it("merges an LLM request's start and finish", () => {
    const s = run([
      {
        name: "llm",
        data: {
          id: "r1",
          model: "qwen",
          op: "chat",
          from_session: "s1",
          to_session: "s2",
          state: "started",
        },
      },
      {
        name: "llm",
        data: {
          id: "r1",
          state: "done",
          usage: { prompt_tokens: 5, completion_tokens: 9, ms: 400 },
        },
      },
    ]);
    expect(s.llm).toHaveLength(1);
    expect(s.llm[0]).toMatchObject({
      model: "qwen",
      state: "done",
      usage: { completion_tokens: 9 },
    });
  });

  it("keeps job events, queue points and library samples", () => {
    const s = run([
      {
        name: "job",
        data: {
          session: "s1",
          ip: "10.0.0.5",
          job: { event: "done", job_id: 7, stage: "transcribe", duration_ms: 900 },
        },
      },
      {
        name: "queues",
        data: {
          at: "2026-10-19T10:00:00Z",
          queues: [{ stage: "transcribe", counts: { pending: 4, running: 1 }, rate_per_min: 2 }],
        },
      },
      {
        name: "library",
        data: {
          ts: "2026-10-19T10:00:00Z",
          libraries: [{ name: "A", hours: 10, transcribed_hours: 4 }],
          totals: null,
        },
      },
    ]);
    expect(s.jobs[0]).toMatchObject({ job_id: 7, session: "s1" });
    expect(s.queues[0].stages.transcribe).toEqual({ waiting: 4, running: 1, rate: 2 });
    expect(s.libraryHistory[0].totals).toMatchObject({ hours: 10, transcribed_hours: 4 });
  });

  it("counts dropped frames", () => {
    expect(
      run([
        { name: "dropped", data: { count: 3 } },
        { name: "dropped", data: { count: 2 } },
      ]).dropped
    ).toBe(5);
  });
});

describe("helpers", () => {
  it("reads the sparkline values from a metrics frame", () => {
    expect(
      seriesValues({
        system: { cpu_pct: 5, net: { rx_bps: 10, tx_bps: 20 } },
        gpu: { nvidia: [{ name: "a", util_pct: 30, mem_used: 0, mem_total: 1, temp_c: 50 }] },
        pipeline: { stt: { audio_sec_per_s: 8 } },
      })
    ).toEqual({ cpu: 5, rx: 10, tx: 20, gpu: 30, stt: 8 });
  });

  it("groups processes by machine with the home first", () => {
    const s = run([
      {
        name: "snapshot",
        data: {
          processes: [
            proc(),
            proc({ session: "s2", worker_id: "node-n1-0" }),
            proc({ session: "h", kind: "home", node_id: null, node_name: null, host: "nas" }),
          ],
        },
      },
    ]);
    const g = groupByMachine(s.processes);
    expect(g.map((x) => x.name)).toEqual(["nas", "Studio Mac"]);
    expect(g[1].processes.map((p) => p.worker_id)).toEqual(["node-n1-0", "node-n1-1"]);
  });

  it("orders log levels", () => {
    expect(atLeast("debug", "info")).toBe(false);
    expect(atLeast("WARNING", "info")).toBe(true);
    expect(atLeast("error", "warning")).toBe(true);
  });

  it("sums library totals when the hub sends none", () => {
    expect(libraryTotals({ libraries: [{ videos: 2 }, { videos: 3 }], totals: null })).toEqual({
      videos: 5,
    });
  });

  it("turns hub snapshots into reducer events", () => {
    expect(hubEvents({ type: "snapshot", topic: "processes", data: [proc()] })[0].name).toBe(
      "snapshot"
    );
    expect(
      hubEvents({ type: "snapshot", topic: "llm", data: { requests: [{ id: "a" }, { id: "b" }] } })
    ).toHaveLength(2);
    expect(
      hubEvents({ type: "snapshot", topic: "logs:s1:info", data: { records: [{ event: "x" }] } })[0]
    ).toEqual({
      name: "log",
      data: { record: { event: "x" } },
    });
    expect(hubEvents({ type: "metrics", data: { session: "s1" } })).toEqual([
      { name: "metrics", data: { session: "s1" } },
    ]);
  });
});
