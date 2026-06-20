import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const { start, heartbeat, stop, stopBeacon } = vi.hoisted(() => ({
  start: vi.fn(),
  heartbeat: vi.fn(),
  stop: vi.fn(),
  stopBeacon: vi.fn(),
}));

vi.mock("./analytics", () => ({
  analyticsApi: { start, heartbeat, stop, stopBeacon },
  detectClient: () => ({ device_type: "web", platform: "browser" }),
}));

import { createWatchSession, HEARTBEAT_MS } from "./watchSession";

const VIDEO = "11111111-1111-1111-1111-111111111111";

describe("createWatchSession", () => {
  beforeEach(() => {
    start.mockReset();
    heartbeat.mockReset();
    stop.mockReset();
    stopBeacon.mockReset();
    start.mockResolvedValue({ tracking: true, session_id: "sess-1" });
    heartbeat.mockResolvedValue({});
    stop.mockResolvedValue({});
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("opens a session on start() with the detected client + position", async () => {
    let t = 0;
    const ws = createWatchSession({ videoId: VIDEO, getPosition: () => t, quality: "auto" });
    ws.start();
    await vi.waitFor(() => expect(start).toHaveBeenCalledTimes(1));
    expect(start).toHaveBeenCalledWith({
      video_id: VIDEO,
      device_type: "web",
      platform: "browser",
      quality: "auto",
    });
    expect(ws.sessionId()).toBe("sess-1");
    void t;
  });

  it("beats every 30s with the current floored position", async () => {
    let t = 0;
    const ws = createWatchSession({ videoId: VIDEO, getPosition: () => t });
    ws.start();
    await vi.waitFor(() => expect(ws.sessionId()).toBe("sess-1"));

    t = 42.9;
    await vi.advanceTimersByTimeAsync(HEARTBEAT_MS);
    expect(heartbeat).toHaveBeenCalledWith({ session_id: "sess-1", position_sec: 42 });

    t = 90.2;
    await vi.advanceTimersByTimeAsync(HEARTBEAT_MS);
    expect(heartbeat).toHaveBeenLastCalledWith({ session_id: "sess-1", position_sec: 90 });
  });

  it("start() is a no-op while a session is open, but reopens after stop()", async () => {
    const ws = createWatchSession({ videoId: VIDEO, getPosition: () => 0 });
    ws.start();
    await vi.waitFor(() => expect(ws.sessionId()).toBe("sess-1"));
    ws.start(); // already open → ignored
    expect(start).toHaveBeenCalledTimes(1);

    ws.stop();
    expect(stop).toHaveBeenCalledTimes(1);
    expect(ws.sessionId()).toBeNull();

    start.mockResolvedValue({ tracking: true, session_id: "sess-2" });
    ws.start(); // fresh run → new session
    await vi.waitFor(() => expect(ws.sessionId()).toBe("sess-2"));
    expect(start).toHaveBeenCalledTimes(2);
  });

  it("stop() closes the session and stops beating", async () => {
    let t = 10;
    const ws = createWatchSession({ videoId: VIDEO, getPosition: () => t });
    ws.start();
    await vi.waitFor(() => expect(ws.sessionId()).toBe("sess-1"));

    t = 55;
    ws.stop();
    expect(stop).toHaveBeenCalledWith({ session_id: "sess-1", position_sec: 55 });

    heartbeat.mockClear();
    await vi.advanceTimersByTimeAsync(HEARTBEAT_MS * 2);
    expect(heartbeat).not.toHaveBeenCalled();
  });

  it("stays idle when tracking is paused (no session, no beats)", async () => {
    start.mockResolvedValue({ tracking: false });
    const ws = createWatchSession({ videoId: VIDEO, getPosition: () => 0 });
    ws.start();
    await vi.waitFor(() => expect(start).toHaveBeenCalledTimes(1));
    expect(ws.sessionId()).toBeNull();
    await vi.advanceTimersByTimeAsync(HEARTBEAT_MS * 3);
    expect(heartbeat).not.toHaveBeenCalled();
    ws.stop(); // no session → no network call
    expect(stop).not.toHaveBeenCalled();
  });

  it("dispose() fires a keepalive beacon stop on pagehide", async () => {
    let t = 0;
    const ws = createWatchSession({ videoId: VIDEO, getPosition: () => t });
    ws.start();
    await vi.waitFor(() => expect(ws.sessionId()).toBe("sess-1"));

    t = 120;
    window.dispatchEvent(new Event("pagehide"));
    expect(stopBeacon).toHaveBeenCalledWith({ session_id: "sess-1", position_sec: 120 });
    // beacon path closed the session, so dispose()'s stop() is a no-op
    ws.dispose();
    expect(stop).not.toHaveBeenCalled();
  });

  it("swallows a start() rejection without throwing", async () => {
    start.mockRejectedValue(new Error("boom"));
    const ws = createWatchSession({ videoId: VIDEO, getPosition: () => 0 });
    expect(() => ws.start()).not.toThrow();
    await vi.waitFor(() => expect(start).toHaveBeenCalled());
    expect(ws.sessionId()).toBeNull();
  });
});
