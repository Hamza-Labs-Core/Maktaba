// VOD playback without native HLS (Chrome/Firefox desktop): the watch page
// lazy-loads hls.js and hands it the signed manifest URL untouched; an
// expired token (401/403) reopens the session and resumes where playback was.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";

const { post, get, del, instances, imported } = vi.hoisted(() => ({
  post: vi.fn(),
  get: vi.fn(),
  del: vi.fn(),
  instances: [] as FakeHlsLike[],
  imported: { count: 0 },
}));

interface FakeHlsLike {
  config: Record<string, unknown>;
  handlers: Record<string, (evt: string, data: unknown) => void>;
  loadSource: ReturnType<typeof vi.fn>;
  attachMedia: ReturnType<typeof vi.fn>;
  startLoad: ReturnType<typeof vi.fn>;
  recoverMediaError: ReturnType<typeof vi.fn>;
  destroy: ReturnType<typeof vi.fn>;
  emit: (evt: string, data: unknown) => void;
}

vi.mock("hls.js", () => {
  imported.count += 1;
  class BaseLoader {
    static urls: string[] = [];
    load(context: { url: string }) {
      BaseLoader.urls.push(context.url);
    }
  }
  class FakeHls implements FakeHlsLike {
    static Events = { ERROR: "hlsError" };
    static ErrorTypes = { NETWORK_ERROR: "networkError", MEDIA_ERROR: "mediaError" };
    static DefaultConfig = { loader: BaseLoader };
    static isSupported = () => true;
    handlers: Record<string, (evt: string, data: unknown) => void> = {};
    loadSource = vi.fn();
    attachMedia = vi.fn();
    startLoad = vi.fn();
    recoverMediaError = vi.fn();
    destroy = vi.fn();
    constructor(public config: Record<string, unknown>) {
      instances.push(this);
    }
    on(evt: string, fn: (evt: string, data: unknown) => void) {
      this.handlers[evt] = fn;
    }
    emit(evt: string, data: unknown) {
      this.handlers[evt]?.(evt, data);
    }
  }
  return { default: FakeHls, BaseLoader };
});

vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, post, get, delete: del } };
});

import { VideoPlayer } from "./VideoPlayer";
import { I18nProvider } from "../lib/i18n";

const SID = "sess-hls";
const SIGNED = `/stream/${SID}/manifest.m3u8?sig=tok.one`;
const RESIGNED = `/stream/${SID}/manifest.m3u8?sig=tok.two`;

function session(over: Record<string, unknown> = {}) {
  return {
    session_id: SID,
    mode: "transcode",
    manifest_url: SIGNED,
    direct_url: "",
    expires_at: "2099-01-01T00:00:00Z",
    ...over,
  };
}

function renderPlayer(path = "/videos/vid-1/watch") {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <I18nProvider>
        <Routes>
          <Route path="/videos/:videoId/watch" element={<VideoPlayer />} />
        </Routes>
      </I18nProvider>
    </MemoryRouter>
  );
}

async function hlsReady(n = 1) {
  await waitFor(() => expect(instances.length).toBe(n));
  return instances[n - 1];
}

function opens() {
  return post.mock.calls.filter((c) => c[0] === "/api/stream/sessions");
}

describe("VideoPlayer — hls.js (no native HLS)", () => {
  beforeEach(() => {
    instances.length = 0;
    imported.count = 0;
    post.mockReset();
    get.mockReset();
    del.mockReset();
    del.mockResolvedValue(undefined);
    get.mockResolvedValue({ items: [] });
    // Chrome / Firefox desktop: no native HLS.
    vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockReturnValue("");
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("plays the signed manifest through hls.js with the query intact", async () => {
    post.mockResolvedValue(session());
    renderPlayer();
    const hls = await hlsReady();
    const video = screen.getByTestId("mkt-video");
    expect(hls.loadSource).toHaveBeenCalledWith(SIGNED);
    expect(hls.attachMedia).toHaveBeenCalledWith(video);
    // hls.js owns the element: no src attribute to fight it.
    expect(video.getAttribute("src")).toBeNull();
    expect(hls.config.startPosition).toBe(-1);
  });

  it("starts at the ?t= deep-link offset", async () => {
    post.mockResolvedValue(session());
    renderPlayer("/videos/vid-1/watch?t=42");
    const hls = await hlsReady();
    expect(hls.config.startPosition).toBe(42);
  });

  it("requests child URIs exactly as given and carries the sig only onto bare same-session ones", async () => {
    post.mockResolvedValue(session());
    renderPlayer();
    const hls = await hlsReady();
    const Loader = hls.config.loader as new (c: unknown) => { load(ctx: { url: string }): void };
    const { BaseLoader } = (await import("hls.js")) as unknown as {
      BaseLoader: { urls: string[] };
    };
    BaseLoader.urls.length = 0;
    const l = new Loader({});
    const signedChild = `${location.origin}/stream/${SID}/720p/index.m3u8?sig=tok.one&x=1`;
    const bareChild = `${location.origin}/stream/${SID}/720p/seg_0001.ts`;
    const other = "https://cdn.example.com/seg.ts?a=b";
    for (const url of [signedChild, bareChild, other]) l.load({ url });
    expect(BaseLoader.urls).toEqual([signedChild, `${bareChild}?sig=tok.one`, other]);
  });

  it("does not re-attach on seek", async () => {
    post.mockResolvedValue(session());
    renderPlayer();
    const hls = await hlsReady();
    const video = screen.getByTestId("mkt-video") as HTMLVideoElement;
    Object.defineProperty(video, "currentTime", { value: 300, configurable: true, writable: true });
    video.dispatchEvent(new Event("seeking"));
    video.dispatchEvent(new Event("seeked"));
    expect(instances.length).toBe(1);
    expect(hls.loadSource).toHaveBeenCalledTimes(1);
    expect(hls.destroy).not.toHaveBeenCalled();
  });

  it.each([401, 403])(
    "reopens the session on HTTP %i and resumes at the current position",
    async (code) => {
      post
        .mockResolvedValueOnce(session())
        .mockResolvedValueOnce(session({ manifest_url: RESIGNED }));
      renderPlayer();
      const first = await hlsReady();
      const video = screen.getByTestId("mkt-video") as HTMLVideoElement;
      Object.defineProperty(video, "currentTime", { value: 1234.5, configurable: true });

      act(() => {
        first.emit("hlsError", {
          type: "networkError",
          details: "fragLoadError",
          fatal: false,
          response: { code, text: "" },
        });
      });

      const second = await hlsReady(2);
      expect(opens()).toHaveLength(2);
      expect(opens()[1][1]).toMatchObject({ video_id: "vid-1" });
      expect(first.destroy).toHaveBeenCalled();
      expect(second.loadSource).toHaveBeenCalledWith(RESIGNED);
      expect(second.config.startPosition).toBe(1234.5);
      // Same session handed back: nothing to close.
      expect(del).not.toHaveBeenCalled();
    }
  );

  it("reopens once even when several requests fail with 403", async () => {
    post
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(session({ manifest_url: RESIGNED }));
    renderPlayer();
    const first = await hlsReady();
    act(() => {
      for (let i = 0; i < 3; i++) {
        first.emit("hlsError", { type: "networkError", fatal: i === 2, response: { code: 403 } });
      }
    });
    await hlsReady(2);
    expect(opens()).toHaveLength(2);
  });

  it("closes the old session when the reopen hands back a different one", async () => {
    post
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(
        session({ session_id: "sess-new", manifest_url: "/stream/sess-new/manifest.m3u8?sig=n" })
      );
    renderPlayer();
    const first = await hlsReady();
    act(() => {
      first.emit("hlsError", { type: "networkError", fatal: true, response: { code: 401 } });
    });
    await hlsReady(2);
    await waitFor(() => expect(del).toHaveBeenCalledWith(`/api/stream/sessions/${SID}`));
  });

  it("retries a fatal network error that is not an auth failure", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      post.mockResolvedValue(session());
      renderPlayer();
      const hls = await hlsReady();
      act(() => {
        hls.emit("hlsError", { type: "networkError", fatal: true, response: { code: 503 } });
      });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1500);
      });
      expect(hls.startLoad).toHaveBeenCalled();
      expect(opens()).toHaveLength(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it("opens the video again where it was when the stream is gone (410)", async () => {
    post
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(
        session({ session_id: "sess-new", manifest_url: "/stream/sess-new/manifest.m3u8?sig=n" })
      );
    renderPlayer();
    const first = await hlsReady();
    const video = screen.getByTestId("mkt-video") as HTMLVideoElement;
    Object.defineProperty(video, "currentTime", { value: 1234.5, configurable: true });
    act(() => {
      first.emit("hlsError", {
        type: "networkError",
        details: "levelLoadError",
        fatal: false,
        response: { code: 410 },
      });
    });
    const second = await hlsReady(2);
    expect(opens()[1][1]).toEqual({ video_id: "vid-1", start_sec: 1234 });
    expect(second.config.startPosition).toBe(1234.5);
    await waitFor(() => expect(del).toHaveBeenCalledWith(`/api/stream/sessions/${SID}`));
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("opens the video again when its loads still fail after the retries (404)", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      post.mockImplementation(() => Promise.resolve(session()));
      renderPlayer();
      const hls = await hlsReady();
      const fail = () =>
        act(() => {
          hls.emit("hlsError", {
            type: "networkError",
            details: "fragLoadError",
            fatal: true,
            response: { code: 404 },
          });
        });
      // the network retries first (a playlist 404s until its first segment)
      for (let i = 0; i < 3; i++) {
        fail();
        await act(async () => {
          await vi.advanceTimersByTimeAsync(8000);
        });
      }
      expect(hls.startLoad).toHaveBeenCalledTimes(3);
      expect(opens()).toHaveLength(1);
      fail();
      await hlsReady(2);
      expect(opens()).toHaveLength(2);
    } finally {
      vi.useRealTimers();
    }
  });

  it("shows the error instead of opening the video again and again", async () => {
    post.mockImplementation(() => Promise.resolve(session()));
    renderPlayer();
    for (let n = 1; n <= 4; n++) {
      const hls = await hlsReady(n);
      act(() => {
        hls.emit("hlsError", { type: "networkError", fatal: true, response: { code: 410 } });
      });
      if (n < 4) await hlsReady(n + 1);
    }
    // three reopens, then the error screen instead of a fourth
    await screen.findByRole("alert");
    expect(opens()).toHaveLength(4);
    expect(instances.length).toBe(4);
  });

  it("renders sidecar subtitle tracks with the session sig", async () => {
    post.mockResolvedValue(
      session({
        subtitles: [
          { lang: "en", label: "English", url: `/stream/${SID}/subs/sidecar/en.vtt` },
          { lang: "ar", url: `/stream/${SID}/subs/auto.vtt?sig=own` },
        ],
      })
    );
    const { container } = renderPlayer();
    await hlsReady();
    const tracks = Array.from(container.querySelectorAll("track"));
    expect(tracks.map((t) => t.getAttribute("src"))).toEqual([
      `/stream/${SID}/subs/sidecar/en.vtt?sig=tok.one`,
      `/stream/${SID}/subs/auto.vtt?sig=own`,
    ]);
    expect(tracks[0].getAttribute("label")).toBe("English");
    expect(tracks[1].getAttribute("srclang")).toBe("ar");
  });

  it("leaves direct-play sessions alone (no hls.js)", async () => {
    post.mockResolvedValue(
      session({ mode: "direct", manifest_url: "", direct_url: "/files/a.mp4?sig=d" })
    );
    renderPlayer();
    const video = await screen.findByTestId("mkt-video");
    await waitFor(() => expect(video).toHaveAttribute("src", "/files/a.mp4?sig=d"));
    expect(instances.length).toBe(0);
  });

  it("uses native HLS when the browser has it (hls.js never constructed)", async () => {
    vi.mocked(HTMLMediaElement.prototype.canPlayType).mockReturnValue("maybe");
    post.mockResolvedValue(session());
    renderPlayer();
    const video = await screen.findByTestId("mkt-video");
    await waitFor(() => expect(video).toHaveAttribute("src", SIGNED));
    expect(instances.length).toBe(0);
  });
});
