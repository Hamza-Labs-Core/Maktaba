// Live player cases from web/test-cases/live-player.md (LPL-*). Same mocks as
// Player.test.tsx (api.get/post/delete, hls.js, HTMLMediaElement); the page
// is mounted with the shared page harness (RequireAuth + RouteErrorBoundary +
// a signed-in user). Real HLS playback and true phone layout are e2e-only.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, useLocation } from "react-router-dom";

const { get, post, del } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), del: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, delete: del } };
});

vi.mock("hls.js", () => {
  class FakeHls {
    static Events = { ERROR: "hlsError" };
    static ErrorTypes = { NETWORK_ERROR: "networkError", MEDIA_ERROR: "mediaError" };
    static isSupported = () => true;
    loadSource = vi.fn();
    attachMedia = vi.fn();
    startLoad = vi.fn();
    recoverMediaError = vi.fn();
    destroy = vi.fn();
    liveSyncPosition = null;
    on() {}
  }
  return { default: FakeHls };
});

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

const realCanPlayType = HTMLMediaElement.prototype.canPlayType;
const realPlay = HTMLMediaElement.prototype.play;
const realPause = HTMLMediaElement.prototype.pause;
const realLoad = HTMLMediaElement.prototype.load;

import { LIVE_RETRIES, LIVE_RETRY_MS, Player } from "./Player";
import { ApiError } from "../../lib/api";
import { memberUser, quietCrash, renderPage } from "../../test/pageHarness";

const NOW = Date.now();
const iso = (ms: number) => new Date(NOW + ms).toISOString();

const ch = (id: string, number: number, name: unknown) => ({
  id,
  number,
  name,
  mode: "shuffle",
  enabled: true,
  sort_order: number,
});
const baseLineup = () => ({
  items: [ch("c1", 1, "One"), ch("c2", 2, "Two"), ch("c3", 3, "Three")],
});

const nowFor = (items: ReturnType<typeof ch>[]) => ({
  server_time: iso(0),
  channels: [
    {
      channel: items[0],
      on_air: true,
      current: {
        channel_id: "c1",
        kind: "program",
        video_id: "v1",
        title: "Now on One",
        start: iso(-5 * 60_000),
        stop: iso(25 * 60_000),
      },
      next: {
        channel_id: "c1",
        kind: "program",
        title: "Next on One",
        start: iso(25 * 60_000),
        stop: iso(85 * 60_000),
      },
    },
    {
      channel: items[1],
      on_air: true,
      current: {
        channel_id: "c2",
        kind: "program",
        title: "Cartoons",
        start: iso(-60_000),
        stop: iso(60_000),
      },
      next: null,
    },
    { channel: items[2], on_air: false, current: null, next: null },
  ],
});

const lanInfo = {
  remote: {
    available: false,
    reason: "off",
    via_relay: false,
    max_streams: 2,
    token_ttl_minutes: 60,
  },
};
const remoteInfo = {
  remote: { available: true, reason: null, via_relay: true, max_streams: 2, token_ttl_minutes: 60 },
};

let lineupPayload: unknown;
let nowPayload: unknown;
let infoPayload: unknown;
let listImpl: (() => Promise<unknown>) | null;

function mockGet() {
  get.mockImplementation((url: string) => {
    if (url.includes("/api/tv/info")) return Promise.resolve(infoPayload);
    if (url.includes("/api/channels/now")) return Promise.resolve(nowPayload);
    if (listImpl) return listImpl();
    return Promise.resolve(lineupPayload);
  });
}

function Where() {
  const loc = useLocation();
  return <div data-testid="where">{loc.pathname + loc.search}</div>;
}

const extraRoutes = (
  <>
    <Route path="/guide" element={<p>GUIDE</p>} />
    <Route
      path="/videos/:id/watch"
      element={
        <>
          <p>VOD WATCH</p>
          <Where />
        </>
      }
    />
  </>
);

function openPlayer(num = "1", opts: { user?: typeof memberUser | null; history?: string[] } = {}) {
  return renderPage(
    <>
      <Player />
      <Where />
    </>,
    { path: `/live/${num}`, route: "/live/:number", extraRoutes, ...opts }
  );
}

const active = () => screen.getByTestId("live-video") as HTMLVideoElement;
const standby = () => screen.getByTestId("live-video-standby") as HTMLVideoElement;
const stage = () => screen.getByTestId("live-player");
const where = () => screen.getByTestId("where").textContent;
const streamFetches = () =>
  (fetch as unknown as ReturnType<typeof vi.fn>).mock.calls.filter((c) =>
    String(c[0]).includes("/stream/")
  );

function firstFrame(el: HTMLVideoElement) {
  act(() => {
    fireEvent.playing(el);
  });
}

async function flushMicrotasks() {
  for (let i = 0; i < 8; i++) await Promise.resolve();
}

async function startOn(num = "1") {
  const r = openPlayer(num);
  await waitFor(() => expect(active().getAttribute("src")).toMatch(/m3u8/));
  firstFrame(active());
  await screen.findByTestId("tune-banner");
  return r;
}

/** The video element that is loading a stream (fails it through every retry). */
async function failAllRetries() {
  const loading = () =>
    (screen.queryAllByTestId(/live-video/) as HTMLVideoElement[]).find((v) =>
      v.getAttribute("src")
    )!;
  for (let i = 1; i < LIVE_RETRIES; i++) {
    fireEvent.error(loading());
    await act(async () => {
      await flushMicrotasks();
      vi.advanceTimersByTime(LIVE_RETRY_MS);
      await flushMicrotasks();
    });
  }
  fireEvent.error(loading());
  await act(async () => {
    await flushMicrotasks();
  });
}

const crashText = "This page hit an error and could not be shown.";

describe("Player (test-cases/live-player.md)", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    del.mockReset();
    del.mockResolvedValue(undefined);
    lineupPayload = baseLineup();
    nowPayload = nowFor(baseLineup().items);
    infoPayload = lanInfo;
    listImpl = null;
    HTMLMediaElement.prototype.canPlayType = function (type: string) {
      return type === "application/vnd.apple.mpegurl" ? "maybe" : "";
    } as HTMLMediaElement["canPlayType"];
    HTMLMediaElement.prototype.play = vi.fn(() => Promise.resolve());
    HTMLMediaElement.prototype.pause = vi.fn();
    HTMLMediaElement.prototype.load = vi.fn();
    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.resolve(new Response("#EXTM3U\n", { status: 200 })))
    );
    try {
      localStorage.removeItem("mkt:locale");
    } catch {
      /* storage blocked */
    }
  });
  afterEach(() => {
    HTMLMediaElement.prototype.canPlayType = realCanPlayType;
    HTMLMediaElement.prototype.play = realPlay;
    HTMLMediaElement.prototype.pause = realPause;
    HTMLMediaElement.prototype.load = realLoad;
    vi.unstubAllGlobals();
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  // ── P1 ──────────────────────────────────────────────────────────────

  it("LPL-04: an unknown number shows Channel not found; Open guide goes to /guide", async () => {
    mockGet();
    const user = userEvent.setup();
    openPlayer("9");
    expect(await screen.findByText("Channel not found")).toBeInTheDocument();
    expect(
      screen.getByText("That channel does not exist, is disabled, or you cannot access it.")
    ).toBeInTheDocument();
    expect(screen.queryByTestId("live-video")).toBeNull();
    expect(streamFetches()).toHaveLength(0);
    await user.click(screen.getByRole("button", { name: "Open guide" }));
    expect(await screen.findByText("GUIDE")).toBeInTheDocument();
  });

  it.each(["abc", "0", "-1", "1.5", "%20"])(
    "LPL-05: /live/%s is not found, with no stream request",
    async (num) => {
      mockGet();
      openPlayer(num);
      expect(await screen.findByText("Channel not found")).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Open guide" })).toBeInTheDocument();
      expect(screen.queryByTestId("live-video")).toBeNull();
      expect(streamFetches()).toHaveLength(0);
      expect(screen.queryByText(crashText)).toBeNull();
    }
  );

  it.each(["1e0", "01"])("LPL-05: /live/%s resolves to channel 1 (Number parsing)", async (num) => {
    mockGet();
    openPlayer(num);
    await waitFor(() => expect(active().getAttribute("src")).toBe("/stream/channel/c1/index.m3u8"));
  });

  it("LPL-05: /live/0 plays when a channel really has number 0", async () => {
    lineupPayload = { items: [ch("c0", 0, "Zero"), ...baseLineup().items] };
    mockGet();
    openPlayer("0");
    await waitFor(() => expect(active().getAttribute("src")).toBe("/stream/channel/c0/index.m3u8"));
  });

  // Bug: Number(" ") is 0, so /live/%20 (and an empty param) matches a
  // channel numbered 0. The fix (accept only /^\d+$/ before Number) belongs
  // in Player.tsx (targetNumber), which this group may not edit.
  it.fails("LPL-05: /live/%20 is not found even when a channel has number 0", async () => {
    lineupPayload = { items: [ch("c0", 0, "Zero"), ...baseLineup().items] };
    mockGet();
    openPlayer("%20");
    expect(await screen.findByText("Channel not found")).toBeInTheDocument();
  });

  // Bug: Player.tsx loadMeta's .catch sets lineup=[] so any list failure
  // reads "Channel not found". Fix in Player.tsx (an error state with retry).
  it.fails(
    "LPL-06: a failing channel list shows an error state, not Channel not found",
    async () => {
      listImpl = () =>
        Promise.reject(new ApiError({ type: "about:blank", title: "Internal", status: 500 }));
      mockGet();
      openPlayer("1");
      expect(await screen.findByText("Something went wrong.")).toBeInTheDocument();
      expect(screen.queryByText("Channel not found")).toBeNull();
    }
  );

  it.each([
    ["500", () => Promise.reject(new ApiError({ type: "x", title: "x", status: 500 }))],
    ["401", () => Promise.reject(new ApiError({ type: "x", title: "x", status: 401 }))],
    ["403", () => Promise.reject(new ApiError({ type: "x", title: "x", status: 403 }))],
    ["network", () => Promise.reject(new TypeError("Failed to fetch"))],
  ])(
    "LPL-06: list failure (%s) is not a blank page; the 30 s refresh recovers",
    async (_l, fail) => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      listImpl = fail;
      mockGet();
      openPlayer("1");
      expect(await screen.findByText("Channel not found")).toBeInTheDocument(); // today's words
      expect(screen.queryByText(crashText)).toBeNull();
      listImpl = null;
      await act(async () => {
        await vi.advanceTimersByTimeAsync(30_000);
      });
      await waitFor(() =>
        expect(active().getAttribute("src")).toBe("/stream/channel/c1/index.m3u8")
      );
    }
  );

  it.each([
    ["items null", { items: null }],
    ["items missing", {}],
  ])("LPL-07: lineup %s shows Channel not found, not a crash", async (_l, payload) => {
    lineupPayload = payload;
    mockGet();
    openPlayer("1");
    expect(await screen.findByText("Channel not found")).toBeInTheDocument();
    expect(screen.queryByText(crashText)).toBeNull();
  });

  it.each([
    ["channels null", { server_time: iso(0), channels: null }],
    ["empty object", {}],
  ])("LPL-07: now %s: plays, banner says No content, mini-guide empty", async (_l, payload) => {
    nowPayload = payload;
    mockGet();
    await startOn("1");
    const banner = screen.getByTestId("tune-banner");
    expect(banner).toHaveTextContent("No content");
    expect(banner).not.toHaveTextContent("Next:");
    expect(screen.queryByRole("button", { name: "Watch from beginning" })).toBeNull();
    fireEvent.keyDown(stage(), { key: "i" });
    const mg = await screen.findByTestId("mini-guide");
    expect(within(mg).queryAllByRole("listitem")).toHaveLength(0);
    expect(screen.queryByText(crashText)).toBeNull();
  });

  it("LPL-07: a now entry with current null and next missing reads No content", async () => {
    nowPayload = {
      server_time: iso(0),
      channels: [{ channel: ch("c1", 1, "One"), current: null }],
    };
    mockGet();
    await startOn("1");
    const banner = screen.getByTestId("tune-banner");
    expect(banner).toHaveTextContent("No content");
    expect(banner).not.toHaveTextContent("Next:");
    fireEvent.keyDown(stage(), { key: "i" });
    expect(await screen.findByTestId("mini-guide")).toHaveTextContent("No content");
  });

  it.each([
    ["{}", {}],
    ["remote null", { remote: null }],
  ])("LPL-07: info %s takes the LAN path", async (_l, payload) => {
    infoPayload = payload;
    mockGet();
    openPlayer("1");
    await waitFor(() => expect(stage().dataset.path).toBe("lan"));
    await waitFor(() => expect(active().getAttribute("src")).toBe("/stream/channel/c1/index.m3u8"));
    expect(post).not.toHaveBeenCalled();
  });

  it("LPL-12: the on-screen Channel up / Channel down buttons surf and wrap", async () => {
    mockGet();
    const user = userEvent.setup();
    await startOn("1");
    await user.click(screen.getByRole("button", { name: "Channel up" }));
    expect(screen.getByTestId("tuning-card")).toHaveTextContent("Two");
    await waitFor(() =>
      expect(standby().getAttribute("src")).toBe("/stream/channel/c2/index.m3u8")
    );
    firstFrame(standby());
    await waitFor(() => expect(where()).toBe("/live/2"));

    await user.click(screen.getByRole("button", { name: "Channel down" }));
    await user.click(screen.getByRole("button", { name: "Channel down" }));
    // 2 → 1 → 3 (wraps): one tune, to channel 3
    expect(screen.getByTestId("tuning-card")).toHaveTextContent("Three");
    await waitFor(() =>
      expect(standby().getAttribute("src")).toBe("/stream/channel/c3/index.m3u8")
    );
  });

  it.each([
    ["ChannelUp", { key: "ChannelUp" }],
    ["MediaTrackNext", { key: "MediaTrackNext" }],
    ["keyCode 427 (Tizen)", { key: "Unidentified", keyCode: 427 }],
  ])("LPL-12: %s surfs up", async (_l, init) => {
    mockGet();
    await startOn("1");
    fireEvent.keyDown(stage(), init);
    expect(screen.getByTestId("tuning-card")).toHaveTextContent("Two");
  });

  it("LPL-12: ArrowUp from channel 3 wraps to 1", async () => {
    mockGet();
    await startOn("3");
    fireEvent.keyDown(stage(), { key: "ArrowUp" });
    expect(screen.getByTestId("tuning-card")).toHaveTextContent("One");
  });

  it("LPL-12: a single-channel lineup stays on its channel", async () => {
    lineupPayload = { items: [ch("c1", 1, "One")] };
    mockGet();
    await startOn("1");
    fireEvent.keyDown(stage(), { key: "ArrowUp" });
    fireEvent.keyDown(stage(), { key: "ArrowDown" });
    await act(async () => {
      await new Promise((r) => setTimeout(r, 350));
    });
    expect(active().getAttribute("src")).toBe("/stream/channel/c1/index.m3u8");
    expect(standby().getAttribute("src")).toBeNull();
    expect(where()).toBe("/live/1");
    expect(screen.queryByText(crashText)).toBeNull();
  });

  it("LPL-26: signed out goes to /login with from=/live/1 and makes no requests", async () => {
    mockGet();
    openPlayer("1", { user: null });
    expect(await screen.findByText("login page (from /live/1)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
    expect(post).not.toHaveBeenCalled();
    expect(fetch).not.toHaveBeenCalled();
  });

  it("LPL-26: a normal (non-admin) user plays the channel", async () => {
    mockGet();
    openPlayer("1", { user: memberUser });
    await waitFor(() => expect(active().getAttribute("src")).toBe("/stream/channel/c1/index.m3u8"));
    expect(screen.queryByText(/admins only/i)).toBeNull();
  });

  it("LPL-27: a render crash shows the route error fallback; a nav link works; no requests after", async () => {
    const spy = quietCrash();
    lineupPayload = { items: [ch("c1", 1, { bad: "name" }), ch("c2", 2, "Two")] };
    mockGet();
    const user = userEvent.setup();
    openPlayer("1");
    expect(await screen.findByRole("alert")).toHaveTextContent(crashText);
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();
    expect(screen.queryByText(crashText)).toBeNull();
    const calls = get.mock.calls.length;
    await act(async () => {
      await new Promise((r) => setTimeout(r, 50));
    });
    expect(get.mock.calls.length).toBe(calls);
    spy.mockRestore();
  });

  it("LPL-27: Back from a crashed player shows the previous page", async () => {
    const spy = quietCrash();
    lineupPayload = { items: [ch("c1", 1, { bad: "name" })] };
    mockGet();
    const user = userEvent.setup();
    openPlayer("1", { history: ["/guide"] });
    expect(await screen.findByRole("alert")).toHaveTextContent(crashText);
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByText("GUIDE")).toBeInTheDocument();
    expect(screen.queryByText(crashText)).toBeNull();
    spy.mockRestore();
  });

  // ── P2 ──────────────────────────────────────────────────────────────

  it("LPL-03: the banner shows now/progress/next, hides after 5 s, Enter toggles it", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    mockGet();
    await startOn("1");
    const banner = screen.getByTestId("tune-banner");
    expect(banner).toHaveTextContent("Now on One");
    expect(within(banner).getByRole("progressbar", { name: "Duration" })).toBeInTheDocument();
    expect(banner).toHaveTextContent("Next: Next on One");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(screen.queryByTestId("tune-banner")).toBeNull();
    fireEvent.keyDown(stage(), { key: "Enter" });
    expect(screen.getByTestId("tune-banner")).toBeInTheDocument();
    fireEvent.keyDown(stage(), { key: "Enter" });
    expect(screen.queryByTestId("tune-banner")).toBeNull();
  });

  it("LPL-03: with the mini-guide open the banner is hidden and its timer does not run", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    mockGet();
    await startOn("1");
    fireEvent.keyDown(stage(), { key: "i" });
    expect(screen.getByTestId("mini-guide")).toBeInTheDocument();
    expect(screen.queryByTestId("tune-banner")).toBeNull();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    fireEvent.keyDown(stage(), { key: "i" });
    // the hide timer was suspended: the banner comes back when the guide closes
    expect(screen.getByTestId("tune-banner")).toBeInTheDocument();
  });

  it("LPL-11: a stall shows Reconnecting… until playing again", async () => {
    mockGet();
    await startOn("1");
    vi.useFakeTimers({ shouldAdvanceTime: true });
    fireEvent.waiting(active());
    await act(async () => {
      await vi.advanceTimersByTimeAsync(8100);
    });
    const r = await screen.findByTestId("live-reconnecting");
    expect(r).toHaveAttribute("role", "status");
    expect(r).toHaveTextContent("Reconnecting…");
    firstFrame(active());
    await waitFor(() => expect(screen.queryByTestId("live-reconnecting")).toBeNull());
  });

  it("LPL-14: Backspace goes back; a lone digit commits after 1.5 s; the Last channel button; Numpad3", async () => {
    mockGet();
    const user = userEvent.setup();
    await startOn("1");
    expect(screen.queryByRole("button", { name: "Last channel" })).toBeNull();

    fireEvent.keyDown(stage(), { key: "3" });
    expect(screen.getByTestId("number-entry")).toHaveTextContent("3");
    fireEvent.keyDown(stage(), { key: "Enter" });
    await waitFor(() =>
      expect(standby().getAttribute("src")).toBe("/stream/channel/c3/index.m3u8")
    );
    firstFrame(standby());
    await waitFor(() => expect(where()).toBe("/live/3"));

    fireEvent.keyDown(stage(), { key: "Backspace" });
    expect(screen.getByTestId("tuning-card")).toHaveTextContent("One");
    await waitFor(() =>
      expect(standby().getAttribute("src")).toBe("/stream/channel/c1/index.m3u8")
    );
    firstFrame(standby());
    await waitFor(() => expect(where()).toBe("/live/1"));

    vi.useFakeTimers({ shouldAdvanceTime: true });
    fireEvent.keyDown(stage(), { key: "2" });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1600);
    });
    await waitFor(() =>
      expect(standby().getAttribute("src")).toBe("/stream/channel/c2/index.m3u8")
    );
    firstFrame(standby());
    await waitFor(() => expect(where()).toBe("/live/2"));
    vi.useRealTimers();

    await user.click(screen.getByRole("button", { name: "Last channel" }));
    expect(screen.getByTestId("tuning-card")).toHaveTextContent("One");
    await waitFor(() =>
      expect(standby().getAttribute("src")).toBe("/stream/channel/c1/index.m3u8")
    );
    firstFrame(standby());
    await waitFor(() => expect(where()).toBe("/live/1"));

    fireEvent.keyDown(stage(), { key: "Unidentified", code: "Numpad3" });
    fireEvent.keyDown(stage(), { key: "Enter" });
    await waitFor(() =>
      expect(standby().getAttribute("src")).toBe("/stream/channel/c3/index.m3u8")
    );
  });

  it("LPL-16: Ctrl/Cmd/Alt shortcuts are left to the browser", async () => {
    mockGet();
    await startOn("1");
    for (const init of [
      { key: "l", ctrlKey: true },
      { key: "g", metaKey: true },
      { key: "ArrowUp", altKey: true },
    ]) {
      const ev = fireEvent.keyDown(stage(), init);
      expect(ev).toBe(true); // not preventDefault-ed
    }
    expect(screen.queryByTestId("tuning-card")).toBeNull();
    expect(screen.queryByText("GUIDE")).toBeNull();
    expect(where()).toBe("/live/1");
  });

  it("LPL-16: Backspace in a text field does not switch channel", async () => {
    mockGet();
    await startOn("1");
    fireEvent.keyDown(stage(), { key: "2" });
    fireEvent.keyDown(stage(), { key: "Enter" });
    await waitFor(() =>
      expect(standby().getAttribute("src")).toBe("/stream/channel/c2/index.m3u8")
    );
    firstFrame(standby());
    await waitFor(() => expect(where()).toBe("/live/2"));
    // a text field inside the player (events bubble to its key handler)
    const input = document.createElement("input");
    stage().appendChild(input);
    const ev = fireEvent.keyDown(input, { key: "Backspace" });
    expect(ev).toBe(true);
    expect(screen.queryByTestId("tuning-card")).toBeNull();
    input.remove();
  });

  it("LPL-17: the mini-guide lists rows, marks the current one, closes with Escape and Close, picks tune", async () => {
    mockGet();
    const user = userEvent.setup();
    await startOn("1");
    await user.click(screen.getByRole("button", { name: "Mini-guide" }));
    const mg = screen.getByRole("dialog", { name: "What's on now" });
    const rows = within(mg).getAllByRole("listitem");
    expect(rows).toHaveLength(3);
    expect(rows[0]).toHaveTextContent(/1\s*One\s*Now on One\s*Next: Next on One/);
    expect(rows[1]).toHaveTextContent(/2\s*Two\s*Cartoons/);
    expect(rows[2]).toHaveTextContent(/3\s*Three\s*No content/);
    expect(within(rows[0]).getByRole("button")).toHaveAttribute("aria-current", "true");

    fireEvent.keyDown(stage(), { key: "Escape" });
    expect(screen.queryByRole("dialog")).toBeNull();

    fireEvent.keyDown(stage(), { key: "i" });
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Close" }));
    expect(screen.queryByRole("dialog")).toBeNull();

    fireEvent.keyDown(stage(), { key: "i" });
    const two = within(screen.getByRole("dialog"))
      .getAllByRole("button")
      .find((b) => b.textContent?.includes("Two"))!;
    await user.click(two);
    expect(screen.queryByRole("dialog")).toBeNull();
    await waitFor(() =>
      expect(standby().getAttribute("src")).toBe("/stream/channel/c2/index.m3u8")
    );
  });

  it("LPL-18: the ← Open guide button goes to /guide", async () => {
    mockGet();
    const user = userEvent.setup();
    await startOn("1");
    await user.click(screen.getByRole("button", { name: /Open guide/ }));
    expect(await screen.findByText("GUIDE")).toBeInTheDocument();
  });

  it("LPL-18: G on the remote path ends the remote session", async () => {
    infoPayload = remoteInfo;
    post.mockResolvedValue({
      session_id: "s1",
      state: "live",
      hls_url: "https://customer-abc.cloudflarestream.com/tok/manifest/video.m3u8",
      expires_at: iso(3600_000),
    });
    mockGet();
    openPlayer("1");
    await waitFor(() => expect(active().getAttribute("src")).toMatch(/cloudflarestream/));
    firstFrame(active());
    fireEvent.keyDown(stage(), { key: "g" });
    expect(await screen.findByText("GUIDE")).toBeInTheDocument();
    await waitFor(() => expect(del).toHaveBeenCalledWith("/api/tv/remote/sessions/s1"));
  });

  it("LPL-19: surfing within 5 s replaces the history entry (Back leaves the player)", async () => {
    mockGet();
    const user = userEvent.setup();
    renderPage(
      <>
        <Player />
        <Where />
      </>,
      { path: "/live/1", route: "/live/:number", extraRoutes, history: ["/guide"] }
    );
    await waitFor(() => expect(active().getAttribute("src")).toMatch(/c1/));
    firstFrame(active());
    fireEvent.keyDown(stage(), { key: "ArrowUp" });
    await waitFor(() => expect(standby().getAttribute("src")).toMatch(/c2/));
    firstFrame(standby());
    await waitFor(() => expect(where()).toBe("/live/2"));
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByText("GUIDE")).toBeInTheDocument();
  });

  it("LPL-19: a channel watched 5 s gets its own entry (Back retunes it)", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    mockGet();
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    renderPage(
      <>
        <Player />
        <Where />
      </>,
      { path: "/live/1", route: "/live/:number", extraRoutes, history: ["/guide"] }
    );
    await waitFor(() => expect(active().getAttribute("src")).toMatch(/c1/));
    firstFrame(active());
    await act(async () => {
      await vi.advanceTimersByTimeAsync(6000);
    });
    fireEvent.keyDown(stage(), { key: "ArrowUp" });
    await waitFor(() => expect(standby().getAttribute("src")).toMatch(/c2/));
    firstFrame(standby());
    await waitFor(() => expect(where()).toBe("/live/2"));
    await user.click(screen.getByRole("button", { name: "go back" }));
    await waitFor(() => expect(where()).toBe("/live/1"));
    await waitFor(() => expect(standby().getAttribute("src")).toMatch(/c1/));
  });

  describe("remote refusals (LPL-21)", () => {
    beforeEach(() => {
      infoPayload = remoteInfo;
    });
    it.each([
      ["user_streams_full", undefined, "Too many remote streams"],
      [
        "remote_storage_full",
        undefined,
        "All remote channels are in use (the Cloudflare storage is full)",
      ],
      ["remote_unavailable", "no inputs", "Remote viewing is not set up (no inputs)"],
      ["remote_not_allowed", undefined, "You may not watch away from home"],
      ["remote_timeout", undefined, "The remote stream did not start. Try again in a moment."],
      ["no_such_channel", undefined, "Channel not found"],
      ["something_new", undefined, "Can't reach the remote stream host (*.cloudflarestream.com)"],
    ])("LPL-21: %s reads the right words, with no direct link", async (code, detail, words) => {
      post.mockRejectedValue(
        new ApiError({ type: "x", title: "Refused", status: 409, code, detail } as never)
      );
      mockGet();
      openPlayer("1");
      const slate = await screen.findByTestId("live-slate");
      expect(slate).toHaveTextContent(words);
      expect(screen.queryByTestId("live-direct-link")).toBeNull();
    });
  });

  // Bug: after a first-tune LAN failure re-reads /api/tv/info and switches
  // the path to remote, nothing tunes: the switcher is re-created for the new
  // pathKey but useTvSwitcher's deep-link effect ([want, ready]) does not
  // re-run (ready goes false→true in one batch), so the player sits blank
  // with no card and no slate. Fix in packages/tvplayer/src/react.tsx (re-run
  // the tune on a new switcher) or Player.tsx (retune after setPath).
  it.fails(
    "LPL-22: a LAN failure moves to the remote path when the device is now away",
    async () => {
      let infoReads = 0;
      mockGet();
      get.mockImplementation((url: string) => {
        if (url.includes("/api/tv/info")) {
          infoReads += 1;
          return Promise.resolve(
            infoReads === 1 ? { remote: { ...remoteInfo.remote, via_relay: false } } : remoteInfo
          );
        }
        if (url.includes("/api/channels/now")) return Promise.resolve(nowPayload);
        return Promise.resolve(lineupPayload);
      });
      post.mockResolvedValue({
        session_id: "s1",
        state: "live",
        hls_url: "https://customer-abc.cloudflarestream.com/tok/manifest/video.m3u8",
        expires_at: iso(3600_000),
      });
      openPlayer("1");
      await waitFor(() => expect(active().getAttribute("src")).toMatch(/c1/));
      vi.useFakeTimers();
      try {
        await failAllRetries();
      } finally {
        vi.useRealTimers();
      }
      await waitFor(() => expect(stage().dataset.path).toBe("remote"));
      await waitFor(() =>
        expect(post).toHaveBeenCalledWith(
          "/api/tv/remote/tune",
          expect.objectContaining({ channel_id: "c1" })
        )
      );
    }
  );

  it("LPL-22: three LAN failures offer Watch remotely; clicking it tunes remotely", async () => {
    infoPayload = { remote: { ...remoteInfo.remote, via_relay: false } };
    post.mockResolvedValue({
      session_id: "s1",
      state: "live",
      hls_url: "https://customer-abc.cloudflarestream.com/tok/manifest/video.m3u8",
      expires_at: iso(3600_000),
    });
    mockGet();
    const user = userEvent.setup();
    openPlayer("1");
    await waitFor(() => expect(active().getAttribute("src")).toMatch(/c1/));
    vi.useFakeTimers();
    try {
      await failAllRetries();
      expect(screen.getByTestId("live-slate")).toBeInTheDocument();
      expect(screen.queryByTestId("watch-remote")).toBeNull();
      for (let n = 2; n <= 3; n++) {
        fireEvent.keyDown(stage(), { key: "ArrowUp" });
        await act(async () => {
          vi.advanceTimersByTime(350);
          await flushMicrotasks();
        });
        await failAllRetries();
      }
    } finally {
      vi.useRealTimers();
    }
    const btn = await screen.findByTestId("watch-remote");
    expect(btn).toHaveTextContent("Watch remotely");
    await user.click(btn);
    await waitFor(() => expect(stage().dataset.path).toBe("remote"));
  });

  it("LPL-22: with remote unavailable, LAN failures never offer Watch remotely", async () => {
    mockGet();
    openPlayer("1");
    await waitFor(() => expect(active().getAttribute("src")).toMatch(/c1/));
    vi.useFakeTimers();
    try {
      await failAllRetries();
    } finally {
      vi.useRealTimers();
    }
    expect(await screen.findByTestId("live-slate")).toBeInTheDocument();
    expect(screen.queryByTestId("watch-remote")).toBeNull();
  });

  // Bug: fromBlock() in src/lib/channels.ts never copies video_id (WireBlock
  // has no such field), so current.video_id is always undefined and the
  // button never shows. Fix in channels.ts.
  it.fails("LPL-23: Watch from beginning opens the VOD at t=0", async () => {
    mockGet();
    const user = userEvent.setup();
    await startOn("1");
    await user.click(await screen.findByRole("button", { name: "Watch from beginning" }));
    expect(await screen.findByText("VOD WATCH")).toBeInTheDocument();
    expect(where()).toBe("/videos/v1/watch?t=0");
  });

  it("LPL-29: Arabic: RTL, Arabic labels, ArrowUp is still the next channel", async () => {
    localStorage.setItem("mkt:locale", "ar");
    mockGet();
    await startOn("1");
    await waitFor(() => expect(document.documentElement.dir).toBe("rtl"));
    expect(screen.getByRole("toolbar", { name: "عناصر تحكم المشغّل" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "القناة التالية" })).toBeInTheDocument();
    expect(stage()).toHaveTextContent("مباشر");
    fireEvent.keyDown(stage(), { key: "ArrowUp" });
    expect(screen.getByTestId("tuning-card")).toHaveTextContent("Two");
    await waitFor(() =>
      expect(standby().getAttribute("src")).toBe("/stream/channel/c2/index.m3u8")
    );
    fireEvent.keyDown(stage(), { key: "i" });
    expect(screen.getByRole("dialog", { name: "ما يُعرض الآن" })).toBeInTheDocument();
  });

  it("LPL-31: labels, focusable stage, hidden standby video, status regions", async () => {
    mockGet();
    await startOn("1");
    const s = screen.getByRole("region", { name: "Live channel" });
    expect(s).toBe(stage());
    expect(s).toHaveAttribute("tabindex", "0");
    expect(screen.getByRole("toolbar", { name: "Player controls" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Channel up" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Channel down" })).toBeInTheDocument();
    const sb = standby();
    expect(sb).toHaveAttribute("aria-hidden", "true");
    expect(sb).toHaveAttribute("tabindex", "-1");
    expect(sb.controls).toBe(false);
    expect(active().controls).toBe(true);
  });

  // Gap: NumberOverlay (packages/tvplayer/src/overlay/index.tsx) is a plain
  // <div> with no role="status" / aria-live, so typed digits and "No channel
  // N" are not announced. Fix in the tvplayer overlay.
  it.fails("LPL-31: the number overlay is a polite status region", async () => {
    mockGet();
    await startOn("1");
    fireEvent.keyDown(stage(), { key: "4" });
    const entry = screen.getByTestId("number-entry");
    expect(entry).toHaveAttribute("role", "status");
    expect(entry).toHaveAttribute("aria-live", "polite");
  });

  // Gap: MiniGuide (packages/tvplayer overlay) has no focus management, so
  // focus stays on the trigger and is not returned to the player. Fix in the
  // tvplayer MiniGuide / Player.tsx.
  it.fails("LPL-31: opening the mini-guide moves focus into it", async () => {
    mockGet();
    const user = userEvent.setup();
    await startOn("1");
    await user.click(screen.getByRole("button", { name: "Mini-guide" }));
    const mg = screen.getByRole("dialog");
    await waitFor(() => expect(mg.contains(document.activeElement)).toBe(true));
  });
});
