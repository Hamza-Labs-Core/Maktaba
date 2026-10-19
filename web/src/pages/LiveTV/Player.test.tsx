// Story 27.7 — live channel player tests; Epic 34.6 channel switching and
// Epic 32.9 LAN-or-remote through the shared TV player.
//
// LAN: the player plays the channel's HLS playlist through the web port
// (/stream/channel/{id}/index.m3u8 — tuning is lazy, there is no tune call).
// Remote (GET /api/tv/info says via_relay): POST /api/tv/remote/tune, a
// heartbeat every 60 s, the session ended on a switch and on unmount.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Routes, Route, useLocation } from "react-router-dom";

const { get, post, del } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), del: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, delete: del } };
});

// hls.js (audit P1-3): browsers without native HLS get a lazily-imported
// hls.js. The fake records instances so tests can drive its error events.
const { hlsInstances, hlsSupported } = vi.hoisted(() => ({
  hlsInstances: [] as Array<{
    loadSource: ReturnType<typeof vi.fn>;
    attachMedia: ReturnType<typeof vi.fn>;
    destroy: ReturnType<typeof vi.fn>;
    emit: (evt: string, data: unknown) => void;
  }>,
  hlsSupported: { value: true },
}));
vi.mock("hls.js", () => {
  class FakeHls {
    static Events = { ERROR: "hlsError" };
    static ErrorTypes = { NETWORK_ERROR: "networkError", MEDIA_ERROR: "mediaError" };
    static isSupported = () => hlsSupported.value;
    handlers: Record<string, (evt: string, data: unknown) => void> = {};
    loadSource = vi.fn();
    attachMedia = vi.fn();
    startLoad = vi.fn();
    recoverMediaError = vi.fn();
    destroy = vi.fn();
    liveSyncPosition = null;
    constructor() {
      hlsInstances.push(this);
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

// jsdom has no PointerEvent: a MouseEvent with a pointerId stands in
if (typeof window.PointerEvent === "undefined") {
  class PointerEventShim extends MouseEvent {
    pointerId: number;
    constructor(type: string, init: PointerEventInit = {}) {
      super(type, init);
      this.pointerId = init.pointerId ?? 0;
    }
  }
  (window as unknown as { PointerEvent: unknown }).PointerEvent = PointerEventShim;
}

let nativeHls = true;
const realCanPlayType = HTMLMediaElement.prototype.canPlayType;
const realPlay = HTMLMediaElement.prototype.play;
const realPause = HTMLMediaElement.prototype.pause;
const realLoad = HTMLMediaElement.prototype.load;

import { LIVE_RETRIES, LIVE_RETRY_MS, Player } from "./Player";
import { I18nProvider } from "../../lib/i18n";
import { ApiError } from "../../lib/api";

const NOW = Date.now();
const iso = (ms: number) => new Date(NOW + ms).toISOString();

const lineup = {
  items: [
    {
      id: "c1",
      number: 1,
      name: "Movies",
      mode: "shuffle",
      enabled: true,
      sort_order: 0,
      logo_url: "/api/channels/c1/logo",
    },
    { id: "c2", number: 2, name: "Kids", mode: "shuffle", enabled: true, sort_order: 1 },
    { id: "c3", number: 3, name: "News", mode: "shuffle", enabled: true, sort_order: 2 },
  ],
};

const nowPayload = {
  server_time: new Date(NOW).toISOString(),
  channels: [
    {
      channel: lineup.items[0],
      on_air: true,
      current: {
        channel_id: "c1",
        seq: 0,
        kind: "program",
        video_id: "v1",
        title: "Now on One",
        start: iso(-5 * 60_000),
        stop: iso(25 * 60_000),
      },
      next: {
        channel_id: "c1",
        seq: 1,
        kind: "program",
        title: "Next on One",
        start: iso(25 * 60_000),
        stop: iso(85 * 60_000),
      },
    },
    {
      channel: lineup.items[1],
      on_air: true,
      current: {
        channel_id: "c2",
        seq: 0,
        kind: "program",
        title: "Cartoons",
        start: iso(-60_000),
        stop: iso(60_000),
      },
      next: null,
    },
    { channel: lineup.items[2], on_air: false, current: null, next: null },
  ],
};

const lanInfo = {
  remote: {
    available: false,
    reason: "off",
    via_relay: false,
    max_streams: 2,
    token_ttl_minutes: 60,
  },
};
let infoPayload: unknown = lanInfo;

function mockGet() {
  get.mockImplementation((url: string) => {
    if (url.includes("/api/tv/info")) return Promise.resolve(infoPayload);
    if (url.includes("/now")) return Promise.resolve(nowPayload);
    return Promise.resolve(lineup);
  });
}

function Where() {
  const loc = useLocation();
  return <div data-testid="where">{loc.pathname}</div>;
}

function renderPlayer(num = "1") {
  return render(
    <MemoryRouter initialEntries={[`/live/${num}`]}>
      <I18nProvider>
        <Routes>
          <Route
            path="/live/:number"
            element={
              <>
                <Player />
                <Where />
              </>
            }
          />
          <Route path="/guide" element={<div>GUIDE</div>} />
        </Routes>
      </I18nProvider>
    </MemoryRouter>
  );
}

const active = () => screen.getByTestId("live-video") as HTMLVideoElement;
const standby = () => screen.getByTestId("live-video-standby") as HTMLVideoElement;
const stage = () => screen.getByTestId("live-player");

/** The first frame arrived on the element (the pipeline then swaps). */
function firstFrame(el: HTMLVideoElement) {
  act(() => {
    fireEvent.playing(el);
  });
}

async function flushMicrotasks() {
  for (let i = 0; i < 8; i++) await Promise.resolve();
}

/** Plays channel `num` to its first frame. */
async function startOn(num = "1") {
  renderPlayer(num);
  await waitFor(() => expect(active().getAttribute("src")).toMatch(/m3u8/));
  firstFrame(active());
  await screen.findByTestId("tune-banner");
}

describe("Player (live channel)", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    del.mockReset();
    del.mockResolvedValue(undefined);
    hlsInstances.length = 0;
    hlsSupported.value = true;
    nativeHls = true;
    infoPayload = lanInfo;
    HTMLMediaElement.prototype.canPlayType = function (type: string) {
      return nativeHls && type === "application/vnd.apple.mpegurl" ? "maybe" : "";
    } as HTMLMediaElement["canPlayType"];
    HTMLMediaElement.prototype.play = vi.fn(() => Promise.resolve());
    HTMLMediaElement.prototype.pause = vi.fn();
    HTMLMediaElement.prototype.load = vi.fn();
    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.resolve(new Response("#EXTM3U\n", { status: 200 })))
    );
  });
  afterEach(() => {
    HTMLMediaElement.prototype.canPlayType = realCanPlayType;
    HTMLMediaElement.prototype.play = realPlay;
    HTMLMediaElement.prototype.pause = realPause;
    HTMLMediaElement.prototype.load = realLoad;
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  // TC1 — distinct live mode: a LIVE indicator + a video element playing
  // the channel's HLS through the web port, with no tune/session calls.
  it("shows a LIVE indicator and plays the channel HLS through the web port", async () => {
    mockGet();
    renderPlayer("1");
    expect(await screen.findByText(/LIVE/)).toBeInTheDocument();
    await waitFor(() => expect(active().getAttribute("src")).toBe("/stream/channel/c1/index.m3u8"));
    expect(post).not.toHaveBeenCalled();
    expect(del).not.toHaveBeenCalled();
  });

  it("shows the tuning card at once, then the channel logo in the tune banner", async () => {
    mockGet();
    renderPlayer("1");
    const card = await screen.findByTestId("tuning-card");
    expect(card).toHaveTextContent("Movies");
    expect(card).toHaveTextContent("Now on One");
    await waitFor(() => expect(active().getAttribute("src")).toBeTruthy());
    firstFrame(active());
    const banner = await screen.findByTestId("tune-banner");
    expect(banner.querySelector("img")?.getAttribute("src")).toBe("/api/channels/c1/logo");
    expect(screen.queryByTestId("tuning-card")).toBeNull();
  });

  // Lazy tuning: the first playlist request can fail while the encoder
  // starts, so the player retries before giving up with a slate + link.
  it("retries a failing stream, then shows a slate with a direct link", async () => {
    mockGet();
    renderPlayer("1");
    await waitFor(() => expect(active().getAttribute("src")).toBeTruthy());
    vi.useFakeTimers();
    try {
      for (let i = 1; i < LIVE_RETRIES; i++) {
        fireEvent.error(active());
        await act(async () => {
          await flushMicrotasks();
          vi.advanceTimersByTime(LIVE_RETRY_MS);
          await flushMicrotasks();
        });
        expect(active().getAttribute("src")).toBe(`/stream/channel/c1/index.m3u8?retry=${i}`);
      }
      fireEvent.error(active());
      await act(async () => {
        await flushMicrotasks();
      });
    } finally {
      vi.useRealTimers();
    }
    expect(await screen.findByTestId("live-slate")).toBeInTheDocument();
    expect(screen.getByTestId("live-direct-link").getAttribute("href")).toBe(
      "/stream/channel/c1/index.m3u8"
    );
  });

  // P1-3 — without native HLS the player lazy-loads hls.js and feeds it
  // the channel playlist instead of setting <video src>.
  it("plays through hls.js when the browser has no native HLS", async () => {
    nativeHls = false;
    mockGet();
    const { unmount } = renderPlayer("1");
    await vi.waitFor(() => expect(hlsInstances).toHaveLength(1));
    const video = active();
    expect(hlsInstances[0].attachMedia).toHaveBeenCalledWith(video);
    expect(hlsInstances[0].loadSource).toHaveBeenCalledWith("/stream/channel/c1/index.m3u8");
    expect(video.getAttribute("src")).toBeNull();
    firstFrame(video);
    // A fatal network error shows the reconnecting indicator.
    act(() => hlsInstances[0].emit("hlsError", { fatal: true, type: "networkError" }));
    expect(await screen.findByTestId("live-reconnecting")).toHaveTextContent(/reconnecting/i);
    unmount();
    expect(hlsInstances[0].destroy).toHaveBeenCalled();
  });

  it("shows a clear slate when neither native HLS nor hls.js can play", async () => {
    nativeHls = false;
    hlsSupported.value = false;
    mockGet();
    renderPlayer("1");
    const slate = await screen.findByTestId("live-slate");
    expect(slate).toHaveTextContent(/can't play live streams/i);
    expect(screen.getByTestId("live-direct-link").getAttribute("href")).toBe(
      "/stream/channel/c1/index.m3u8"
    );
  });

  // TC4 — the tune banner shows the current + next program.
  it("renders a tune banner with current and next program", async () => {
    mockGet();
    await startOn("1");
    const banner = screen.getByTestId("tune-banner");
    expect(banner).toHaveTextContent("Now on One");
    expect(banner).toHaveTextContent("Next on One");
  });

  // TC5 — the mini-guide overlay lists channels over the playing channel.
  it("opens a mini-guide listing channels with now and next", async () => {
    mockGet();
    renderPlayer("1");
    const guideBtn = await screen.findByRole("button", { name: /mini-guide/i });
    guideBtn.click();
    const mg = await screen.findByTestId("mini-guide");
    expect(mg).toHaveTextContent("Movies");
    expect(mg).toHaveTextContent("Kids");
    expect(mg).toHaveTextContent("Next on One");
  });

  // EC6 — an unknown channel number resolves to a not-found state.
  it("shows a not-found state for an unknown channel number", async () => {
    mockGet();
    renderPlayer("9");
    expect(await screen.findByText(/channel not found/i)).toBeInTheDocument();
  });

  // 34.6 AC3/AC4 — make before break: the card at once, the old channel
  // playing until the new one has its first segment, then the swap.
  it("keeps the old channel playing until the new one can play (ArrowUp)", async () => {
    mockGet();
    await startOn("1");
    const oldEl = active();
    fireEvent.keyDown(stage(), { key: "ArrowUp" });
    // the card is there synchronously, the old picture still playing
    expect(screen.getByTestId("tuning-card")).toHaveTextContent("Kids");
    expect(oldEl.getAttribute("src")).toBe("/stream/channel/c1/index.m3u8");
    // the tune commits 300 ms after the last press, on the hidden element
    await waitFor(() =>
      expect(standby().getAttribute("src")).toBe("/stream/channel/c2/index.m3u8")
    );
    expect(oldEl.getAttribute("src")).toBe("/stream/channel/c1/index.m3u8");
    expect(active()).toBe(oldEl);
    firstFrame(standby());
    await waitFor(() => expect(active().getAttribute("src")).toBe("/stream/channel/c2/index.m3u8"));
    expect(oldEl.getAttribute("src")).toBeNull();
    expect(screen.getByTestId("where")).toHaveTextContent("/live/2");
    expect(screen.queryByTestId("tuning-card")).toBeNull();
  });

  // 34.6 AC6 — rapid surfing moves the card and tunes once.
  it("debounces rapid surfing to one tune (PageUp twice → channel 3)", async () => {
    mockGet();
    await startOn("1");
    fireEvent.keyDown(stage(), { key: "PageUp" });
    fireEvent.keyDown(stage(), { key: "PageUp" });
    expect(screen.getByTestId("tuning-card")).toHaveTextContent("News");
    await waitFor(() =>
      expect(standby().getAttribute("src")).toBe("/stream/channel/c3/index.m3u8")
    );
    // channel 2 was never loaded: the old picture is still channel 1
    expect(active().getAttribute("src")).toBe("/stream/channel/c1/index.m3u8");
  });

  it("wraps down from the first channel (PageDown → the last)", async () => {
    mockGet();
    await startOn("1");
    fireEvent.keyDown(stage(), { key: "PageDown" });
    expect(screen.getByTestId("tuning-card")).toHaveTextContent("News");
  });

  // 34.6 AC1 — digits + Enter, "No channel N", last channel.
  it("tunes by number with Enter and goes back with L", async () => {
    mockGet();
    await startOn("1");
    fireEvent.keyDown(stage(), { key: "3" });
    expect(screen.getByTestId("number-entry")).toHaveTextContent("3");
    fireEvent.keyDown(stage(), { key: "Enter" });
    await waitFor(() =>
      expect(standby().getAttribute("src")).toBe("/stream/channel/c3/index.m3u8")
    );
    firstFrame(standby());
    await waitFor(() => expect(screen.getByTestId("where")).toHaveTextContent("/live/3"));
    fireEvent.keyDown(stage(), { key: "l" });
    expect(screen.getByTestId("tuning-card")).toHaveTextContent("Movies");
    await waitFor(() =>
      expect(standby().getAttribute("src")).toBe("/stream/channel/c1/index.m3u8")
    );
  });

  it("says there is no such channel for an unknown number", async () => {
    mockGet();
    await startOn("1");
    fireEvent.keyDown(stage(), { key: "4" });
    fireEvent.keyDown(stage(), { key: "7" });
    fireEvent.keyDown(stage(), { key: "Enter" });
    expect(await screen.findByTestId("number-entry")).toHaveTextContent("No channel 47");
    expect(standby().getAttribute("src")).toBeNull();
  });

  it("tunes from the mini-guide (I, then a tap)", async () => {
    mockGet();
    await startOn("1");
    fireEvent.keyDown(stage(), { key: "i" });
    const mg = await screen.findByTestId("mini-guide");
    const kids = Array.from(mg.querySelectorAll("button")).find((b) =>
      b.textContent?.includes("Kids")
    );
    fireEvent.click(kids!);
    await waitFor(() =>
      expect(standby().getAttribute("src")).toBe("/stream/channel/c2/index.m3u8")
    );
  });

  it("opens the full guide with G", async () => {
    mockGet();
    await startOn("1");
    fireEvent.keyDown(stage(), { key: "g" });
    expect(await screen.findByText("GUIDE")).toBeInTheDocument();
  });

  it("surfs with a vertical swipe on the picture", async () => {
    mockGet();
    await startOn("1");
    const layer = document.querySelector(".mkt-live__touch") as HTMLElement;
    fireEvent.pointerDown(layer, { clientX: 100, clientY: 300, pointerId: 1, button: 0 });
    fireEvent.pointerUp(layer, { clientX: 104, clientY: 180, pointerId: 1, button: 0 });
    expect(screen.getByTestId("tuning-card")).toHaveTextContent("Kids");
  });

  // 34.6 AC5 — the neighbours' playlists (on air only), never a segment.
  it("prefetches the playlists of on-air neighbours only", async () => {
    mockGet();
    await startOn("1");
    const f = fetch as unknown as ReturnType<typeof vi.fn>;
    await waitFor(() => expect(f).toHaveBeenCalled());
    const urls = f.mock.calls.map((c) => String(c[0]));
    expect(urls).toContain("/stream/channel/c2/index.m3u8"); // next, on air
    expect(urls).not.toContain("/stream/channel/c3/index.m3u8"); // prev, not on air
    expect(urls.every((u) => u.endsWith(".m3u8"))).toBe(true);
  });

  // 32.9 AC3/AC5 — through the relay the player tunes remotely.
  describe("remote (through the relay)", () => {
    const HLS = (tok: string) =>
      `https://customer-abc.cloudflarestream.com/${tok}/manifest/video.m3u8`;
    beforeEach(() => {
      infoPayload = {
        remote: {
          available: true,
          reason: null,
          via_relay: true,
          max_streams: 2,
          token_ttl_minutes: 60,
        },
      };
    });

    it("tunes remotely and ends the session on a switch and on unmount", async () => {
      mockGet();
      let n = 0;
      post.mockImplementation((url: string) => {
        if (url === "/api/tv/remote/tune") {
          n += 1;
          return Promise.resolve({
            session_id: `s${n}`,
            state: "live",
            hls_url: HLS(`tok${n}`),
            expires_at: iso(3600_000),
          });
        }
        return Promise.resolve({ state: "live" });
      });
      const { unmount } = renderPlayer("1");
      await waitFor(() => expect(active().getAttribute("src")).toBe(HLS("tok1")));
      expect(post).toHaveBeenCalledWith(
        "/api/tv/remote/tune",
        expect.objectContaining({ channel_id: "c1" })
      );
      expect(screen.getByTestId("live-player").dataset.path).toBe("remote");
      firstFrame(active());
      fireEvent.keyDown(stage(), { key: "ArrowUp" });
      await waitFor(() => expect(standby().getAttribute("src")).toBe(HLS("tok2")));
      expect(del).not.toHaveBeenCalled(); // channel 1 plays until channel 2 can
      firstFrame(standby());
      await waitFor(() => expect(del).toHaveBeenCalledWith("/api/tv/remote/sessions/s1"));
      // remote neighbours are never prefetched (each would need a tune)
      expect(fetch).not.toHaveBeenCalled();
      unmount();
      expect(del).toHaveBeenCalledWith("/api/tv/remote/sessions/s2");
    });

    it("shows Starting remote stream while the push starts (202)", async () => {
      mockGet();
      post.mockResolvedValue({ session_id: "s1", state: "starting", retry_after: 3 });
      renderPlayer("1");
      expect(await screen.findByText(/Starting remote stream/)).toBeInTheDocument();
    });

    it("says the monthly budget is reached (402)", async () => {
      mockGet();
      post.mockRejectedValue(
        new ApiError({
          type: "x",
          title: "Payment Required",
          status: 402,
          code: "budget_exhausted",
        } as never)
      );
      renderPlayer("1");
      expect(await screen.findByTestId("live-slate")).toHaveTextContent(
        /monthly budget is reached/
      );
    });

    it("says which limit when the remote channels are full (429)", async () => {
      mockGet();
      post.mockRejectedValue(
        new ApiError({
          type: "x",
          title: "Too Many",
          status: 429,
          code: "remote_channels_full",
        } as never)
      );
      renderPlayer("1");
      expect(await screen.findByTestId("live-slate")).toHaveTextContent(
        /All remote channels are in use/
      );
    });
  });
});
