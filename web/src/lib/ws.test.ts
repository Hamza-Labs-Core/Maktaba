import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";

class FakeES {
  static all: FakeES[] = [];
  static CLOSED = 2;
  readyState = 1;
  onopen: (() => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onerror: (() => void) | null = null;
  closed = false;
  constructor(
    public url: string,
    public init?: unknown
  ) {
    FakeES.all.push(this);
  }
  close() {
    this.closed = true;
    this.readyState = 2;
  }
  emit(data: unknown) {
    this.onmessage?.({
      data: typeof data === "string" ? data : JSON.stringify(data),
    } as MessageEvent);
  }
}

async function load() {
  vi.resetModules();
  return import("./ws");
}

describe("ws.subscribe", () => {
  beforeEach(() => {
    FakeES.all = [];
    vi.stubGlobal("EventSource", FakeES);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it("shares one EventSource per channel and fans out to every listener", async () => {
    const { subscribe } = await load();
    const a = vi.fn();
    const b = vi.fn();
    const offA = subscribe("jobs", a);
    const offB = subscribe("jobs", b);
    subscribe("queues", vi.fn());
    expect(FakeES.all.map((e) => e.url)).toEqual(["/ws/jobs", "/ws/queues"]);
    FakeES.all[0].emit({ type: "job.started", at: "x", payload: {} });
    expect(a).toHaveBeenCalledTimes(1);
    expect(b).toHaveBeenCalledTimes(1);
    offA();
    expect(FakeES.all[0].closed).toBe(false);
    offB();
    expect(FakeES.all[0].closed).toBe(true);
  });

  it("opens a fresh stream after the last listener left", async () => {
    const { subscribe } = await load();
    subscribe("jobs", vi.fn())();
    subscribe("jobs", vi.fn());
    expect(FakeES.all).toHaveLength(2);
    expect(FakeES.all[1].closed).toBe(false);
  });

  it("ignores non-JSON frames and survives a listener unsubscribing mid-dispatch", async () => {
    const { subscribe } = await load();
    const seen: string[] = [];
    let offFirst = () => {};
    offFirst = subscribe("jobs", () => {
      seen.push("first");
      offFirst();
    });
    subscribe("jobs", () => seen.push("second"));
    FakeES.all[0].emit(": ping");
    expect(seen).toEqual([]);
    FakeES.all[0].emit({ type: "t", at: "x" });
    expect(seen).toEqual(["first", "second"]);
  });

  it("reopens with last_event_id after the browser gave up on the stream", async () => {
    vi.useFakeTimers();
    const { subscribe } = await load();
    subscribe("jobs", vi.fn());
    FakeES.all[0].emit({ type: "job.log", at: "x", payload: { _event_id: 42 } });
    FakeES.all[0].readyState = 2;
    FakeES.all[0].onerror?.();
    vi.advanceTimersByTime(1000);
    expect(FakeES.all).toHaveLength(2);
    expect(FakeES.all[1].url).toBe("/ws/jobs?last_event_id=42");
  });
});
