// The shared cluster hub socket: topic ref-counting, reconnect with
// re-subscribe, ping, message routing, commands.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const { post } = vi.hoisted(() => ({ post: vi.fn() }));
vi.mock("./api", async () => {
  const actual = await vi.importActual<typeof import("./api")>("./api");
  return { ...actual, api: { ...actual.api, post } };
});

import { FakeSocket } from "../test/fakeSocket";
import {
  BACKOFF_START_MS,
  clusterSocket,
  logsTopic,
  PING_MS,
  socketFallback,
  socketUrl,
} from "./clusterSocket";

describe("clusterSocket", () => {
  beforeEach(() => {
    FakeSocket.all = [];
    vi.stubGlobal("WebSocket", FakeSocket);
    clusterSocket.reset();
    post.mockReset();
  });
  afterEach(() => {
    clusterSocket.reset();
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it("connects to /api/cluster/ws on the page's host", () => {
    expect(socketUrl()).toBe(`ws://${window.location.host}/api/cluster/ws`);
    clusterSocket.subscribe("queues", vi.fn());
    expect(FakeSocket.all).toHaveLength(1);
    expect(FakeSocket.last.url).toBe(socketUrl());
    expect(clusterSocket.getStatus()).toBe("connecting");
  });

  it("subscribes each topic once and unsubscribes after the last listener", () => {
    const a = vi.fn();
    const b = vi.fn();
    const offA = clusterSocket.subscribe("queues", a);
    const ws = FakeSocket.last;
    ws.open();
    expect(ws.subscribed()).toEqual(["queues"]);
    const offB = clusterSocket.subscribe("queues", b);
    clusterSocket.subscribe("jobs", vi.fn());
    expect(ws.subscribed()).toEqual(["queues", "jobs"]);
    ws.recv({ type: "queues", data: { queues: [] } });
    expect(a).toHaveBeenCalledTimes(1);
    expect(b).toHaveBeenCalledTimes(1);
    offA();
    expect(ws.unsubscribed()).toEqual([]);
    offB();
    expect(ws.unsubscribed()).toEqual(["queues"]);
    expect(FakeSocket.all).toHaveLength(1);
  });

  it("routes snapshots by topic, job events to jobs and log lines to the logs topic", () => {
    const queues = vi.fn();
    const jobs = vi.fn();
    const logs = vi.fn();
    clusterSocket.subscribe("queues", queues);
    clusterSocket.subscribe("jobs", jobs);
    clusterSocket.subscribe(logsTopic("10.0.0.5", "info"), logs);
    const ws = FakeSocket.last;
    ws.open();
    ws.recv({ type: "snapshot", topic: "jobs", data: { jobs: [] } });
    ws.recv({ type: "job", data: { session: "s1", job: { job_id: 1 } } });
    ws.recv({ type: "log", data: { session: "s1", record: { event: "x" } } });
    ws.recv({ type: "pong" });
    expect(queues).not.toHaveBeenCalled();
    expect(jobs).toHaveBeenCalledTimes(2);
    expect(logs).toHaveBeenCalledTimes(1);
    expect(logs.mock.calls[0][0].type).toBe("log");
  });

  it("reconnects with backoff and re-subscribes every live topic", () => {
    vi.useFakeTimers();
    clusterSocket.subscribe("queues", vi.fn());
    clusterSocket.subscribe("workers", vi.fn());
    FakeSocket.last.open();
    FakeSocket.last.drop();
    expect(clusterSocket.getStatus()).toBe("reconnecting");
    expect(socketFallback(clusterSocket.getStatus())).toBe(true);
    vi.advanceTimersByTime(BACKOFF_START_MS - 1);
    expect(FakeSocket.all).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(FakeSocket.all).toHaveLength(2);
    // a second failure waits twice as long
    FakeSocket.last.drop();
    vi.advanceTimersByTime(BACKOFF_START_MS);
    expect(FakeSocket.all).toHaveLength(2);
    vi.advanceTimersByTime(BACKOFF_START_MS);
    expect(FakeSocket.all).toHaveLength(3);
    FakeSocket.last.open();
    expect(clusterSocket.getStatus()).toBe("open");
    expect(FakeSocket.last.subscribed()).toEqual(["queues", "workers"]);
  });

  it("pings every 20 seconds while open", () => {
    vi.useFakeTimers();
    clusterSocket.subscribe("queues", vi.fn());
    const ws = FakeSocket.last;
    ws.open();
    vi.advanceTimersByTime(PING_MS);
    expect(ws.sent.filter((m) => m.op === "ping")).toHaveLength(1);
    vi.advanceTimersByTime(PING_MS);
    expect(ws.sent.filter((m) => m.op === "ping")).toHaveLength(2);
  });

  it("closes a while after the last topic is gone", () => {
    vi.useFakeTimers();
    const off = clusterSocket.subscribe("queues", vi.fn());
    const ws = FakeSocket.last;
    ws.open();
    off();
    vi.advanceTimersByTime(10_000);
    expect(ws.readyState).toBe(3);
    expect(clusterSocket.getStatus()).toBe("idle");
  });

  it("is unavailable without WebSocket", () => {
    vi.stubGlobal("WebSocket", undefined);
    clusterSocket.subscribe("queues", vi.fn());
    expect(clusterSocket.getStatus()).toBe("unavailable");
    expect(socketFallback("unavailable")).toBe(true);
  });

  it("sends a command over the socket and resolves with its result", async () => {
    clusterSocket.subscribe("processes", vi.fn());
    const ws = FakeSocket.last;
    ws.open();
    const p = clusterSocket.cmd("s1", "drain");
    const sent = ws.sent.find((m) => m.op === "cmd")!;
    expect(sent).toMatchObject({ op: "cmd", session: "s1", name: "drain", args: {} });
    ws.recv({ type: "cmd.result", data: { id: sent.id, ok: true, detail: "draining" } });
    await expect(p).resolves.toEqual({ ok: true, detail: "draining" });
    expect(post).not.toHaveBeenCalled();
  });

  it("falls back to the REST command while the socket is down", async () => {
    post.mockResolvedValue({ ok: true, detail: null });
    await expect(clusterSocket.cmd("s 1", "log_level", { level: "debug" })).resolves.toEqual({
      ok: true,
      detail: null,
    });
    expect(post).toHaveBeenCalledWith("/api/cluster/processes/s%201/cmd", {
      name: "log_level",
      args: { level: "debug" },
    });
  });
});
