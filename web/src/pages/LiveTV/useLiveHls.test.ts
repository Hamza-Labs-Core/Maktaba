// Audit P1-3 — live HLS attachment: native on Safari, lazy hls.js
// elsewhere, recovery from fatal errors and a stall watchdog that rejoins
// the live edge.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, renderHook } from "@testing-library/react";

const { instances, isSupported, imported } = vi.hoisted(() => ({
  instances: [] as FakeHlsLike[],
  isSupported: { value: true },
  imported: { count: 0 },
}));

interface FakeHlsLike {
  config: unknown;
  handlers: Record<string, (evt: string, data: unknown) => void>;
  loadSource: ReturnType<typeof vi.fn>;
  attachMedia: ReturnType<typeof vi.fn>;
  startLoad: ReturnType<typeof vi.fn>;
  recoverMediaError: ReturnType<typeof vi.fn>;
  destroy: ReturnType<typeof vi.fn>;
  liveSyncPosition: number | null;
  emit: (evt: string, data: unknown) => void;
}

vi.mock("hls.js", () => {
  imported.count += 1;
  class FakeHls implements FakeHlsLike {
    static Events = { ERROR: "hlsError" };
    static ErrorTypes = {
      NETWORK_ERROR: "networkError",
      MEDIA_ERROR: "mediaError",
      OTHER_ERROR: "otherError",
    };
    static isSupported = () => isSupported.value;
    handlers: Record<string, (evt: string, data: unknown) => void> = {};
    loadSource = vi.fn();
    attachMedia = vi.fn();
    startLoad = vi.fn();
    recoverMediaError = vi.fn();
    destroy = vi.fn();
    liveSyncPosition: number | null = null;
    constructor(public config: unknown) {
      instances.push(this);
    }
    on(evt: string, fn: (evt: string, data: unknown) => void) {
      this.handlers[evt] = fn;
    }
    emit(evt: string, data: unknown) {
      this.handlers[evt]?.(evt, data);
    }
  }
  return { default: FakeHls };
});

import { MAX_REJOINS, NET_RETRY_BASE_MS, STALL_MS, useLiveHls } from "./useLiveHls";

const URL1 = "/stream/channel/c1/index.m3u8";
const URL2 = "/stream/channel/c2/index.m3u8";

function makeVideo(native: boolean) {
  const v = document.createElement("video");
  v.canPlayType = vi.fn(() => (native ? "maybe" : "")) as HTMLVideoElement["canPlayType"];
  return v;
}

async function flush() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

describe("useLiveHls", () => {
  beforeEach(() => {
    instances.length = 0;
    isSupported.value = true;
    imported.count = 0;
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("uses the native path on Safari: sets src and never loads hls.js", async () => {
    const video = makeVideo(true);
    const { result } = renderHook(() => useLiveHls(video, URL1, vi.fn()));
    await flush();
    expect(result.current.mode).toBe("native");
    expect(video.getAttribute("src")).toBe(URL1);
    expect(imported.count).toBe(0);
    expect(instances).toHaveLength(0);
  });

  it("loads hls.js without native HLS and attaches the channel URL", async () => {
    const video = makeVideo(false);
    const { result } = renderHook(() => useLiveHls(video, URL1, vi.fn()));
    await flush();
    expect(result.current.mode).toBe("hls");
    expect(instances).toHaveLength(1);
    const hls = instances[0];
    expect(hls.attachMedia).toHaveBeenCalledWith(video);
    expect(hls.loadSource).toHaveBeenCalledWith(URL1);
    expect(hls.config).toMatchObject({ lowLatencyMode: false, enableWorker: true });
  });

  it("reports unsupported when neither native HLS nor MSE is available", async () => {
    isSupported.value = false;
    const video = makeVideo(false);
    const { result } = renderHook(() => useLiveHls(video, URL1, vi.fn()));
    await flush();
    expect(result.current.mode).toBe("unsupported");
    expect(instances).toHaveLength(0);
  });

  it("retries a fatal network error with startLoad, then rejoins, then gives up", async () => {
    vi.useFakeTimers();
    const onFatal = vi.fn();
    const video = makeVideo(false);
    const { result } = renderHook(() => useLiveHls(video, URL1, onFatal));
    await flush();
    const hls = instances[0];
    act(() => hls.emit("hlsError", { fatal: true, type: "networkError" }));
    expect(result.current.reconnecting).toBe(true);
    expect(hls.startLoad).not.toHaveBeenCalled();
    act(() => {
      vi.advanceTimersByTime(NET_RETRY_BASE_MS);
    });
    expect(hls.startLoad).toHaveBeenCalledTimes(1);
    // Exhaust the network retries → destroy + re-create.
    act(() => hls.emit("hlsError", { fatal: true, type: "networkError" }));
    act(() => hls.emit("hlsError", { fatal: true, type: "networkError" }));
    act(() => hls.emit("hlsError", { fatal: true, type: "networkError" }));
    expect(hls.destroy).toHaveBeenCalled();
    expect(instances).toHaveLength(2);
    expect(instances[1].loadSource).toHaveBeenCalledWith(URL1);
    // Non-recoverable errors keep rejoining until the bound, then onFatal.
    for (let i = 1; i < MAX_REJOINS; i++) {
      act(() => instances[i].emit("hlsError", { fatal: true, type: "otherError" }));
    }
    expect(onFatal).not.toHaveBeenCalled();
    act(() => instances[MAX_REJOINS].emit("hlsError", { fatal: true, type: "otherError" }));
    expect(onFatal).toHaveBeenCalledTimes(1);
  });

  it("recovers a fatal media error with recoverMediaError", async () => {
    const video = makeVideo(false);
    renderHook(() => useLiveHls(video, URL1, vi.fn()));
    await flush();
    act(() => instances[0].emit("hlsError", { fatal: true, type: "mediaError" }));
    expect(instances[0].recoverMediaError).toHaveBeenCalledTimes(1);
    expect(instances).toHaveLength(1);
  });

  it("ignores non-fatal errors", async () => {
    const video = makeVideo(false);
    renderHook(() => useLiveHls(video, URL1, vi.fn()));
    await flush();
    act(() => instances[0].emit("hlsError", { fatal: false, type: "networkError" }));
    expect(instances[0].startLoad).not.toHaveBeenCalled();
    expect(instances).toHaveLength(1);
  });

  it("destroys the instance on channel change and on unmount", async () => {
    const video = makeVideo(false);
    const { rerender, unmount } = renderHook(({ src }) => useLiveHls(video, src, vi.fn()), {
      initialProps: { src: URL1 },
    });
    await flush();
    rerender({ src: URL2 });
    await flush();
    expect(instances[0].destroy).toHaveBeenCalled();
    expect(instances[1].loadSource).toHaveBeenCalledWith(URL2);
    unmount();
    expect(instances[1].destroy).toHaveBeenCalled();
  });

  it("watchdog: a stall seeks to the live edge, then re-creates hls", async () => {
    vi.useFakeTimers();
    const video = makeVideo(false);
    const { result } = renderHook(() => useLiveHls(video, URL1, vi.fn()));
    await flush();
    const hls = instances[0];
    hls.liveSyncPosition = 120;
    act(() => {
      video.dispatchEvent(new Event("waiting"));
    });
    act(() => {
      vi.advanceTimersByTime(STALL_MS - 1);
    });
    expect(hls.startLoad).not.toHaveBeenCalled();
    act(() => {
      vi.advanceTimersByTime(1);
    });
    expect(result.current.reconnecting).toBe(true);
    expect(hls.startLoad).toHaveBeenCalledTimes(1);
    expect(video.currentTime).toBe(120);
    // Still no progress → full rejoin.
    act(() => {
      vi.advanceTimersByTime(STALL_MS);
    });
    expect(hls.destroy).toHaveBeenCalled();
    expect(instances).toHaveLength(2);
    // Playback resumes → indicator clears and the watchdog disarms.
    act(() => {
      video.dispatchEvent(new Event("playing"));
    });
    expect(result.current.reconnecting).toBe(false);
    act(() => {
      vi.advanceTimersByTime(STALL_MS * 3);
    });
    expect(instances).toHaveLength(2);
  });

  it("watchdog: progress before the deadline cancels the rejoin", async () => {
    vi.useFakeTimers();
    const video = makeVideo(true);
    const setSrc = vi.spyOn(video, "src", "set");
    renderHook(() => useLiveHls(video, URL1, vi.fn()));
    await flush();
    expect(setSrc).toHaveBeenCalledTimes(1);
    act(() => {
      video.dispatchEvent(new Event("stalled"));
    });
    act(() => {
      vi.advanceTimersByTime(STALL_MS / 2);
      video.currentTime = 5;
      video.dispatchEvent(new Event("timeupdate"));
      vi.advanceTimersByTime(STALL_MS);
    });
    expect(setSrc).toHaveBeenCalledTimes(1);
  });

  it("watchdog on the native path reloads the source", async () => {
    vi.useFakeTimers();
    const video = makeVideo(true);
    const setSrc = vi.spyOn(video, "src", "set");
    const { result } = renderHook(() => useLiveHls(video, URL1, vi.fn()));
    await flush();
    act(() => {
      video.dispatchEvent(new Event("waiting"));
      vi.advanceTimersByTime(STALL_MS);
    });
    expect(setSrc).toHaveBeenCalledTimes(2);
    expect(setSrc).toHaveBeenLastCalledWith(URL1);
    expect(result.current.reconnecting).toBe(true);
  });

  it("watchdog stays quiet while the viewer has paused", async () => {
    vi.useFakeTimers();
    const video = makeVideo(true);
    const setSrc = vi.spyOn(video, "src", "set");
    renderHook(() => useLiveHls(video, URL1, vi.fn()));
    await flush();
    act(() => {
      video.dispatchEvent(new Event("pause"));
      video.dispatchEvent(new Event("waiting"));
      vi.advanceTimersByTime(STALL_MS * 2);
    });
    expect(setSrc).toHaveBeenCalledTimes(1);
  });
});
