// Story 27.7 — live channel player tests. The player plays the channel's
// HLS playlist through the web port (/stream/channel/{id}/index.m3u8 —
// tuning is lazy, there is no tune call), shows a LIVE indicator + tune
// banner, a mini-guide overlay, retries a failing stream before showing a
// slate, and resolves an unknown channel number to a not-found state.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";

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

let nativeHls = true;
const realCanPlayType = HTMLMediaElement.prototype.canPlayType;

import { LIVE_RETRIES, LIVE_RETRY_MS, Player } from "./Player";
import { I18nProvider } from "../../lib/i18n";

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
  ],
};

const nowPayload = {
  server_time: new Date(NOW).toISOString(),
  channels: [
    {
      channel: lineup.items[0],
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
    { channel: lineup.items[1], current: null, next: null },
  ],
};

function mockGet() {
  get.mockImplementation((url: string) =>
    url.includes("/now") ? Promise.resolve(nowPayload) : Promise.resolve(lineup)
  );
}

function renderPlayer(num = "1") {
  return render(
    <MemoryRouter initialEntries={[`/live/${num}`]}>
      <I18nProvider>
        <Routes>
          <Route path="/live/:number" element={<Player />} />
          <Route path="/guide" element={<div>GUIDE</div>} />
        </Routes>
      </I18nProvider>
    </MemoryRouter>
  );
}

describe("Player (live channel)", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    del.mockReset();
    hlsInstances.length = 0;
    hlsSupported.value = true;
    nativeHls = true;
    HTMLMediaElement.prototype.canPlayType = function (type: string) {
      return nativeHls && type === "application/vnd.apple.mpegurl" ? "maybe" : "";
    } as HTMLMediaElement["canPlayType"];
  });
  afterEach(() => {
    HTMLMediaElement.prototype.canPlayType = realCanPlayType;
  });

  // TC1 — distinct live mode: a LIVE indicator + a video element playing
  // the channel's HLS through the web port, with no tune/session calls.
  it("shows a LIVE indicator and plays the channel HLS through the web port", async () => {
    mockGet();
    renderPlayer("1");
    expect(await screen.findByText(/LIVE/)).toBeInTheDocument();
    const video = (await screen.findByTestId("live-video")) as HTMLVideoElement;
    expect(video.getAttribute("src")).toBe("/stream/channel/c1/index.m3u8");
    expect(post).not.toHaveBeenCalled();
    expect(del).not.toHaveBeenCalled();
  });

  it("shows the channel logo in the tune banner", async () => {
    mockGet();
    renderPlayer("1");
    const banner = await screen.findByTestId("tune-banner");
    expect(banner.querySelector("img")?.getAttribute("src")).toBe("/api/channels/c1/logo");
  });

  // Lazy tuning: the first playlist request can fail while the encoder
  // starts, so the player retries before giving up with a slate + link.
  it("retries a failing stream, then shows a slate with a direct link", async () => {
    mockGet();
    renderPlayer("1");
    let video = (await screen.findByTestId("live-video")) as HTMLVideoElement;
    vi.useFakeTimers();
    try {
      for (let i = 1; i < LIVE_RETRIES; i++) {
        fireEvent.error(video);
        await act(async () => {
          vi.advanceTimersByTime(LIVE_RETRY_MS);
        });
        video = screen.getByTestId("live-video") as HTMLVideoElement;
        expect(video.getAttribute("src")).toBe(`/stream/channel/c1/index.m3u8?retry=${i}`);
      }
      fireEvent.error(video);
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
    const video = (await screen.findByTestId("live-video")) as HTMLVideoElement;
    await vi.waitFor(() => expect(hlsInstances).toHaveLength(1));
    expect(hlsInstances[0].attachMedia).toHaveBeenCalledWith(video);
    expect(hlsInstances[0].loadSource).toHaveBeenCalledWith("/stream/channel/c1/index.m3u8");
    expect(video.getAttribute("src")).toBeNull();
    // A fatal network error shows the reconnecting indicator.
    act(() => hlsInstances[0].emit("hlsError", { fatal: true, type: "networkError" }));
    fireEvent.playing(video); // first frame arrived earlier; tuning is over
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
    renderPlayer("1");
    const banner = await screen.findByTestId("tune-banner");
    expect(banner).toHaveTextContent("Now on One");
    expect(banner).toHaveTextContent("Next on One");
  });

  // TC5 — the mini-guide overlay lists channels over the playing channel.
  it("opens a mini-guide listing channels", async () => {
    mockGet();
    renderPlayer("1");
    const guideBtn = await screen.findByRole("button", { name: /mini-guide/i });
    guideBtn.click();
    const mg = await screen.findByTestId("mini-guide");
    expect(mg).toHaveTextContent("Movies");
    expect(mg).toHaveTextContent("Kids");
  });

  // EC6 — an unknown channel number resolves to a not-found state.
  it("shows a not-found state for an unknown channel number", async () => {
    mockGet();
    renderPlayer("9");
    expect(await screen.findByText(/channel not found/i)).toBeInTheDocument();
  });
});
