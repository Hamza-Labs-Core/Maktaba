// Story 16.5 EC: the queue keeps at most 1,000 events (the oldest go), sends 100
// a request, backs off exponentially on a 5xx or a dropped network, and drops
// a batch the server refused.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { TelemetryQueue, type TelemetryEvent } from "./queue";

const ev = (i: number): TelemetryEvent => ({
  event_kind: "app.open",
  payload: { n: i },
  ts: "2026-10-26T00:00:00Z",
});

describe("TelemetryQueue", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    localStorage.clear();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("keeps 1,000 events, dropping the oldest", () => {
    const q = new TelemetryQueue({ send: vi.fn() });
    for (let i = 0; i < 1001; i++) q.enqueue(ev(i));
    expect(q.size).toBe(1000);
  });

  it("sends at most 100 a request, the oldest first; the rest on the next tick", async () => {
    const sent: TelemetryEvent[][] = [];
    const q = new TelemetryQueue({ send: async (b) => void sent.push(b), flushMs: 1000 });
    for (let i = 0; i < 200; i++) q.enqueue(ev(i));
    await vi.advanceTimersByTimeAsync(1000);
    expect(sent).toHaveLength(1);
    expect(sent[0]).toHaveLength(100);
    expect(sent[0][0].payload.n).toBe(0);
    await vi.advanceTimersByTimeAsync(1000);
    expect(sent).toHaveLength(2);
    expect(q.size).toBe(0);
  });

  it("backs off 2 s, 4 s ... up to 60 s on a 5xx, then sends", async () => {
    let fail = 3;
    const times: number[] = [];
    const send = vi.fn(async () => {
      times.push(Date.now());
      if (fail-- > 0) throw Object.assign(new Error("bad gateway"), { status: 502 });
    });
    const q = new TelemetryQueue({ send, flushMs: 1000 });
    q.enqueue(ev(1));
    await vi.advanceTimersByTimeAsync(1000); // first attempt
    await vi.advanceTimersByTimeAsync(2000); // retry after 2 s
    await vi.advanceTimersByTimeAsync(4000); // after 4 s
    await vi.advanceTimersByTimeAsync(8000); // after 8 s: succeeds
    expect(send).toHaveBeenCalledTimes(4);
    expect(times.slice(1).map((t, i) => t - times[i])).toEqual([2000, 4000, 8000]);
    expect(q.size).toBe(0);
    expect(q.backoffMs(10)).toBe(60_000);
  });

  it("keeps the events through a dropped network", async () => {
    const send = vi.fn().mockRejectedValue(new TypeError("Failed to fetch"));
    const q = new TelemetryQueue({ send, flushMs: 1000, storageKey: "k" });
    q.enqueue(ev(1));
    await vi.advanceTimersByTimeAsync(1000);
    expect(q.size).toBe(1);
    // persisted: a new launch picks them up
    expect(new TelemetryQueue({ send: vi.fn(), storageKey: "k" }).size).toBe(1);
  });

  it("drops a batch the server refused (4xx)", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const send = vi.fn().mockRejectedValue(Object.assign(new Error("x"), { status: 400 }));
    const q = new TelemetryQueue({ send, flushMs: 1000 });
    q.enqueue(ev(1));
    await vi.advanceTimersByTimeAsync(1000);
    expect(q.size).toBe(0);
    await vi.advanceTimersByTimeAsync(120_000);
    expect(send).toHaveBeenCalledTimes(1);
    warn.mockRestore();
  });

  it("stop drops what waits and queues nothing more", async () => {
    const send = vi.fn();
    const q = new TelemetryQueue({ send, flushMs: 1000, storageKey: "k" });
    q.enqueue(ev(1));
    q.stop();
    q.enqueue(ev(2));
    await vi.advanceTimersByTimeAsync(5000);
    expect(send).not.toHaveBeenCalled();
    expect(q.size).toBe(0);
    expect(localStorage.getItem("k")).toBeNull();
  });
});
