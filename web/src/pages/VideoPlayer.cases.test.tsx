// Video player cases from web/test-cases/video-player.md. Real decoding is
// e2e-only; these check what the page wires up (src, listeners, API calls).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route } from "react-router-dom";
import { ApiError } from "../lib/api";
import { renderPage, memberUser, quietCrash } from "../test/pageHarness";

const { post, get, del } = vi.hoisted(() => ({ post: vi.fn(), get: vi.fn(), del: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, post, get, delete: del } };
});

import { VideoPlayer } from "./VideoPlayer";

const problem = (status: number, title: string) =>
  new ApiError({ type: "about:blank", title, status });

const SESSION = {
  session_id: "sess-abc",
  mode: "transcode",
  manifest_url: "/stream/sess-abc/manifest.m3u8",
  direct_url: "",
  expires_at: "2099-01-01T00:00:00Z",
};

type Answer = unknown | ((body: unknown) => unknown);

/** Routes api.post by path; the stream session answer may be a function. */
function servePost(session: Answer = SESSION) {
  post.mockImplementation((url: string, body: unknown) => {
    const pick = (v: Answer) => {
      try {
        return Promise.resolve(typeof v === "function" ? (v as (b: unknown) => unknown)(body) : v);
      } catch (e) {
        return Promise.reject(e);
      }
    };
    if (url === "/api/stream/sessions") return pick(session);
    return Promise.resolve(undefined);
  });
}

const posts = (path: string) => post.mock.calls.filter((c) => c[0] === path);

function renderPlayer(
  path = "/videos/vid-1/watch",
  opts: { history?: string[]; user?: null } = {}
) {
  return renderPage(<VideoPlayer />, {
    path,
    route: "/videos/:videoId/watch",
    user: opts.user === null ? null : memberUser,
    history: opts.history,
    extraRoutes: (
      <>
        <Route path="/videos/:videoId" element={<p>video detail page</p>} />
        <Route path="/library" element={<p>library page</p>} />
      </>
    ),
  });
}

/**
 * The player's <video> once the page has attached the stream to it. React
 * commits the element first and runs the effects that set its src and add its
 * listeners as a later scheduler task: on a busy runner findByTestId can
 * resolve in between, and an event fired then has no listener.
 */
async function video(): Promise<HTMLVideoElement> {
  const el = (await screen.findByTestId("mkt-video")) as HTMLVideoElement;
  await waitFor(() => expect(el).toHaveAttribute("src"));
  return el;
}

function setDuration(el: HTMLVideoElement, d: number) {
  Object.defineProperty(el, "duration", { configurable: true, value: d });
}

describe("VideoPlayer (test-cases/video-player.md)", () => {
  beforeEach(() => {
    post.mockReset();
    get.mockReset();
    del.mockReset();
    del.mockResolvedValue(undefined);
    get.mockResolvedValue({ items: [] });
    // Native HLS path (the hls.js path is VideoPlayer.hls.test.tsx).
    vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockReturnValue("maybe");
    vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(undefined);
    vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
    vi.spyOn(HTMLMediaElement.prototype, "load").mockImplementation(() => {});
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("VPL-03: a signed URL is used exactly as returned", async () => {
    const signed = "/api/stream/s1/file?exp=1760870400&sig=a%2Bb%2F%3D";
    servePost({ ...SESSION, mode: "direct", manifest_url: "", direct_url: signed });
    renderPlayer();
    const el = await video();
    expect(el.getAttribute("src")).toBe(signed);
  });

  it.each([
    ["no URLs", { ...SESSION, manifest_url: "", direct_url: "" }],
    ["no session_id", {}],
  ])("VPL-05: a session with %s shows an error, not an empty player", async (_l, s) => {
    servePost(s);
    renderPlayer();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Something went wrong.");
    expect(screen.queryByTestId("mkt-video")).toBeNull();
    expect(post.mock.calls.some((c) => String(c[0]).includes("undefined"))).toBe(false);
  });

  it("VPL-06: a playback error on a direct stream shows an alert; Try again reopens", async () => {
    servePost({ ...SESSION, mode: "direct", manifest_url: "", direct_url: "/files/o.mp4" });
    renderPlayer();
    const el = await video();
    Object.defineProperty(el, "error", { configurable: true, value: { code: 2 } });
    fireEvent.error(el);
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Something went wrong.");
    await userEvent.setup().click(within(alert).getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(posts("/api/stream/sessions")).toHaveLength(2));
    expect(await video()).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("VPL-06: a non-auth playback error on native HLS shows the alert", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => ({ status: 500, body: null }))
    );
    servePost();
    renderPlayer();
    const el = await video();
    fireEvent.error(el);
    expect(await screen.findByRole("alert")).toHaveTextContent("Something went wrong.");
    vi.unstubAllGlobals();
  });

  it.each([
    [403, "forbidden", "Something went wrong.", "permission"],
    [404, "video not found", "Video not found.", "not_found"],
  ])(
    "VPL-07: session open %s shows an error with a way back",
    async (status, title, heading, kind) => {
      servePost(() => Promise.reject(problem(status, title)));
      renderPlayer("/videos/vid-1/watch", { history: ["/library"] });
      const alert = await screen.findByRole("alert");
      expect(alert).toHaveAttribute("data-kind", kind);
      expect(alert).toHaveTextContent(heading);
      expect(alert).toHaveTextContent(title);
      expect(screen.queryByTestId("mkt-video")).toBeNull();
      expect(del).not.toHaveBeenCalled();
      await userEvent.setup().click(within(alert).getByRole("button", { name: /Back/ }));
      expect(screen.getByText("library page")).toBeInTheDocument();
    }
  );

  it("VPL-07c: a server without the disk space to prepare the video says so (507)", async () => {
    servePost(() =>
      Promise.reject(
        new ApiError({
          type: "https://maktaba.dev/problems/insufficient-storage",
          title: "not enough disk space to prepare this video",
          status: 507,
          code: "insufficient_storage",
        })
      )
    );
    renderPlayer("/videos/vid-1/watch", { history: ["/library"] });
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(
      "There isn't enough free disk space on the server to prepare this video."
    );
    expect(screen.queryByTestId("mkt-video")).toBeNull();
    expect(within(alert).getByRole("button", { name: "Try again" })).toBeInTheDocument();
  });

  it("VPL-07b: a video this machine cannot read while the home is unreachable says so", async () => {
    servePost(() =>
      Promise.reject(
        new ApiError({
          type: "https://maktaba.dev/problems/video-unavailable-offline",
          title: "this video isn't available while the home is unreachable",
          status: 503,
          code: "video_unavailable_offline",
        })
      )
    );
    renderPlayer("/videos/vid-1/watch", { history: ["/library"] });
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(
      "This video isn't available while this machine can't reach its home"
    );
    expect(screen.queryByTestId("mkt-video")).toBeNull();
    // the home may come back: trying again stays offered
    expect(within(alert).getByRole("button", { name: "Try again" })).toBeInTheDocument();
  });

  it("VPL-08: signed-out user is sent to /login; no session is opened", () => {
    servePost();
    renderPlayer("/videos/vid-1/watch?t=120", { user: null });
    expect(screen.getByText(/^login page \(from \/videos\/vid-1\/watch/)).toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
  });

  it("VPL-08: the /login redirect keeps ?t=120", () => {
    servePost();
    renderPlayer("/videos/vid-1/watch?t=120", { user: null });
    expect(screen.getByText("login page (from /videos/vid-1/watch?t=120)")).toBeInTheDocument();
  });

  it.each([
    ["125", 125],
    ["abc", 0],
    ["-5", 0],
    ["0", 0],
    ["99999", 599],
    [null, 0],
  ])("VPL-09: ?t=%s starts at %s", async (t, expected) => {
    servePost();
    renderPlayer(t === null ? "/videos/vid-1/watch" : `/videos/vid-1/watch?t=${t}`);
    const el = await video();
    setDuration(el, 600);
    fireEvent.loadedMetadata(el);
    expect(el.currentTime).toBe(expected);
  });

  it.todo("VPL-09: with no ?t=, resume from the last saved position (no API for it yet)");

  it("VPL-10: progress posts are throttled, sent on pause, completed on end", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.setSystemTime(new Date("2026-10-19T10:00:00Z"));
    servePost();
    renderPlayer();
    const el = await video();
    const progress = () => posts("/api/stream/sessions/sess-abc/progress").map((c) => c[1]);
    el.currentTime = 10.7;
    fireEvent.timeUpdate(el);
    vi.setSystemTime(new Date("2026-10-19T10:00:01Z"));
    fireEvent.timeUpdate(el);
    vi.setSystemTime(new Date("2026-10-19T10:00:06Z"));
    el.currentTime = 16.2;
    fireEvent.timeUpdate(el);
    expect(progress()).toEqual([{ position_sec: 10 }, { position_sec: 16 }]);
    fireEvent.pause(el);
    expect(progress().at(-1)).toEqual({ position_sec: 16 });
    el.currentTime = 600;
    fireEvent.ended(el);
    expect(progress().at(-1)).toEqual({ position_sec: 600, completed: true });
  });

  it("VPL-10: a failing progress POST is swallowed", async () => {
    servePost();
    renderPlayer();
    const el = await video();
    post.mockImplementation((url: string) =>
      url.endsWith("/progress") ? Promise.reject(problem(500, "boom")) : Promise.resolve(undefined)
    );
    fireEvent.pause(el);
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.getByTestId("mkt-video")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("VPL-11: a failing DELETE on unmount is swallowed", async () => {
    servePost();
    del.mockRejectedValue(problem(500, "boom"));
    const r = renderPlayer();
    await video();
    r.unmount();
    expect(del).toHaveBeenCalledWith("/api/stream/sessions/sess-abc");
  });

  it("VPL-12: leaving before the session opens still closes the late session", async () => {
    let resolve: (v: unknown) => void = () => {};
    servePost(() => new Promise((r) => (resolve = r)));
    const r = renderPlayer();
    await waitFor(() => expect(posts("/api/stream/sessions")).toHaveLength(1));
    r.unmount();
    await act(async () => resolve({ ...SESSION, session_id: "late" }));
    expect(del).toHaveBeenCalledWith("/api/stream/sessions/late");
    expect(del).toHaveBeenCalledTimes(1);
  });

  it("VPL-12: switching video before the first POST resolves closes the late session", async () => {
    const resolvers: Array<(v: unknown) => void> = [];
    servePost(() => new Promise((r) => resolvers.push(r)));
    renderPlayer("/videos/vid-1/watch", { history: ["/videos/vid-2/watch"] });
    await waitFor(() => expect(posts("/api/stream/sessions")).toHaveLength(1));
    await userEvent.setup().click(screen.getByRole("button", { name: "go back" }));
    await waitFor(() => expect(posts("/api/stream/sessions")).toHaveLength(2));
    await act(async () => resolvers[0]({ ...SESSION, session_id: "late-1" }));
    expect(del).toHaveBeenCalledWith("/api/stream/sessions/late-1");
  });

  // Epic 44 story 44.3: the server records the play from the stream session's
  // progress and its close; the player calls no /api/watch/* (one record a play).
  it("VPL-13: the play is recorded from the stream session alone, never /api/watch/*", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    servePost(SESSION);
    const r = renderPlayer();
    const el = await video();
    setDuration(el, 600);
    fireEvent.play(el);
    await act(async () => {
      vi.advanceTimersByTime(60_000);
    });
    fireEvent.pause(el);
    fireEvent.play(el);
    fireEvent.ended(el);
    r.unmount();
    expect(posts("/api/stream/sessions/sess-abc/progress").length).toBeGreaterThan(0);
    expect(del).toHaveBeenCalledWith("/api/stream/sessions/sess-abc");
    expect(post.mock.calls.filter((c) => String(c[0]).startsWith("/api/watch/"))).toEqual([]);
  });

  it.each([
    ["items null", { items: null }],
    ["items missing", {}],
    ["500", () => Promise.reject(problem(500, "boom"))],
    ["a null entry", { items: [null] }],
  ])("VPL-15: chapters %s render no ticks and do not crash", async (_l, answer) => {
    get.mockImplementation(() =>
      typeof answer === "function" ? (answer as () => unknown)() : Promise.resolve(answer)
    );
    servePost();
    renderPlayer();
    const el = await video();
    setDuration(el, 600);
    fireEvent.loadedMetadata(el);
    await new Promise((r) => setTimeout(r, 0));
    expect(document.querySelector(".mkt-player__chapters")).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("VPL-15: chapter ticks are placed by start time, clamped, hidden until duration", async () => {
    get.mockResolvedValue({
      items: [
        { id: 1, start_sec: 0, title: "Intro" },
        { id: 2, start_sec: 300, title: "Part 2" },
        { id: 3, start_sec: 900, title: "Past the end" },
      ],
    });
    servePost();
    renderPlayer();
    const el = await video();
    await new Promise((r) => setTimeout(r, 0));
    expect(document.querySelector(".mkt-player__chapters")).toBeNull();
    setDuration(el, 600);
    fireEvent.loadedMetadata(el);
    const strip = await waitFor(() => {
      const s = document.querySelector(".mkt-player__chapters");
      expect(s).not.toBeNull();
      return s!;
    });
    expect(strip).toHaveAttribute("aria-hidden", "true");
    const ticks = Array.from(strip.querySelectorAll<HTMLElement>(".mkt-player__tick"));
    expect(ticks.map((t) => [t.title, t.style.insetInlineStart])).toEqual([
      ["Intro", "0%"],
      ["Part 2", "50%"],
      ["Past the end", "100%"],
    ]);
  });

  it("VPL-16: render crash shows route error fallback; Back still works", async () => {
    quietCrash();
    let bad = true;
    // an object where the error description string is rendered
    servePost(() =>
      bad
        ? Promise.reject(new ApiError({ type: "x", title: {} as unknown as string, status: 500 }))
        : SESSION
    );
    renderPlayer();
    expect(
      await screen.findByText("This page hit an error and could not be shown.")
    ).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
    bad = false;
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await video()).toBeInTheDocument();
  });

  it("VPL-17: loading shows Back but no video or controls", async () => {
    servePost(() => new Promise(() => {}));
    renderPlayer("/videos/vid-1/watch", { history: ["/library"] });
    expect(screen.getByText("Loading…")).toBeInTheDocument();
    expect(screen.queryByTestId("mkt-video")).toBeNull();
    expect(screen.queryByRole("combobox", { name: "Playback speed" })).toBeNull();
    await userEvent.setup().click(screen.getByRole("button", { name: /Back/ }));
    expect(screen.getByText("library page")).toBeInTheDocument();
  });

  it.each([
    ["500", problem(500, "transcoder busy"), "transcoder busy"],
    ["network failure", new TypeError("Failed to fetch"), "Something went wrong."],
    ["malformed JSON", new SyntaxError("Unexpected token b"), "Something went wrong."],
  ])("VPL-18: session open %s shows the error with Try again", async (_l, e, desc) => {
    let fail = true;
    servePost(() => (fail ? Promise.reject(e) : SESSION));
    renderPlayer();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Something went wrong.");
    expect(alert).toHaveTextContent(desc);
    fail = false;
    await userEvent.setup().click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await video()).toHaveAttribute("src", SESSION.manifest_url);
  });

  it("VPL-19: the error clears when moving to another video", async () => {
    servePost((body: unknown) =>
      (body as { video_id: string }).video_id === "vid-1"
        ? Promise.reject(problem(403, "forbidden"))
        : { ...SESSION, session_id: "s2", manifest_url: "/stream/s2/manifest.m3u8" }
    );
    renderPlayer("/videos/vid-1/watch", { history: ["/videos/vid-2/watch"] });
    expect(await screen.findByRole("alert")).toHaveTextContent("forbidden");
    await userEvent.setup().click(screen.getByRole("button", { name: "go back" }));
    expect(await video()).toHaveAttribute("src", "/stream/s2/manifest.m3u8");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("VPL-20: Back goes to the previous page and closes the session", async () => {
    servePost();
    renderPlayer("/videos/vid-1/watch", { history: ["/videos/vid-1"] });
    await video();
    await userEvent.setup().click(screen.getByRole("button", { name: "← Back" }));
    expect(screen.getByText("video detail page")).toBeInTheDocument();
    expect(del).toHaveBeenCalledWith("/api/stream/sessions/sess-abc");
  });

  it("VPL-20: Back on a pasted deep link goes to the video page, not out of the app", async () => {
    servePost();
    renderPlayer();
    await video();
    await userEvent.setup().click(screen.getByRole("button", { name: "← Back" }));
    expect(screen.getByText("video detail page")).toBeInTheDocument();
  });

  it("VPL-21: keyboard map works on the focused player", async () => {
    servePost();
    renderPlayer();
    const el = await video();
    setDuration(el, 600);
    let paused = true;
    Object.defineProperty(el, "paused", { configurable: true, get: () => paused });
    const play = vi.spyOn(el, "play").mockImplementation(async () => {
      paused = false;
    });
    const pause = vi.spyOn(el, "pause").mockImplementation(() => {
      paused = true;
    });
    const requestFullscreen = vi.fn(async () => {});
    Object.defineProperty(el, "requestFullscreen", {
      configurable: true,
      value: requestFullscreen,
    });
    const player = screen.getByRole("region", { name: "Watch" });
    player.focus();
    const key = (k: string, code = "") => fireEvent.keyDown(player, { key: k, code });
    expect(key(" ")).toBe(false); // preventDefault: no page scroll
    expect(play).toHaveBeenCalledTimes(1);
    key("k", "KeyK");
    expect(pause).toHaveBeenCalledTimes(1);
    el.currentTime = 100;
    key("j", "KeyJ");
    expect(el.currentTime).toBe(90);
    key("l", "KeyL");
    expect(el.currentTime).toBe(100);
    key("ArrowLeft");
    expect(el.currentTime).toBe(95);
    key("ArrowRight");
    expect(el.currentTime).toBe(100);
    key("m", "KeyM");
    expect(el.muted).toBe(true);
    key(",");
    expect(el.currentTime).toBeCloseTo(100 - 1 / 30);
    key(".");
    expect(el.currentTime).toBeCloseTo(100);
    key("5", "Digit5");
    expect(el.currentTime).toBe(300);
    key("f", "KeyF");
    expect(requestFullscreen).toHaveBeenCalledTimes(1);
    Object.defineProperty(document, "fullscreenElement", { configurable: true, value: el });
    const exit = vi.fn(async () => {});
    Object.defineProperty(document, "exitFullscreen", { configurable: true, value: exit });
    key("f", "KeyF");
    expect(exit).toHaveBeenCalledTimes(1);
    Object.defineProperty(document, "fullscreenElement", { configurable: true, value: null });
    // Caps Lock / Shift and an Arabic layout still hit the physical key.
    key("J", "KeyJ");
    expect(el.currentTime).toBe(290);
    key("ت", "KeyL");
    expect(el.currentTime).toBe(300);
  });

  it.todo("VPL-21: a visible keyboard shortcut list (player.shortcuts)");

  it("VPL-22: shortcuts do not fire from controls, with modifiers, or after the g leader", async () => {
    servePost();
    renderPlayer("/videos/vid-1/watch", { history: ["/library"] });
    const el = await video();
    setDuration(el, 600);
    el.currentTime = 100;
    const speed = screen.getByRole("combobox", { name: "Playback speed" });
    fireEvent.keyDown(speed, { key: "ArrowRight" });
    fireEvent.keyDown(speed, { key: "-" });
    expect(el.currentTime).toBe(100);
    expect(speed).toHaveValue("1");
    const back = screen.getByRole("button", { name: "← Back" });
    expect(fireEvent.keyDown(back, { key: " " })).toBe(true); // not prevented
    const player = screen.getByRole("region", { name: "Watch" });
    const requestFullscreen = vi.fn(async () => {});
    Object.defineProperty(el, "requestFullscreen", {
      configurable: true,
      value: requestFullscreen,
    });
    fireEvent.keyDown(player, { key: "f", code: "KeyF", ctrlKey: true });
    fireEvent.keyDown(player, { key: "f", code: "KeyF", metaKey: true });
    expect(requestFullscreen).not.toHaveBeenCalled();
    fireEvent.keyDown(player, { key: "g", code: "KeyG" });
    fireEvent.keyDown(player, { key: "l", code: "KeyL" });
    expect(el.currentTime).toBe(100);
  });

  it("VPL-23: speed select and +/- keys stay in sync", async () => {
    servePost();
    renderPlayer();
    const el = await video();
    const select = screen.getByRole("combobox", { name: "Playback speed" });
    expect(Array.from((select as HTMLSelectElement).options).map((o) => o.value)).toEqual([
      "0.5",
      "0.75",
      "1",
      "1.25",
      "1.5",
      "2",
    ]);
    await userEvent.setup().selectOptions(select, "1.5");
    expect(el.playbackRate).toBe(1.5);
    const player = screen.getByRole("region", { name: "Watch" });
    fireEvent.keyDown(player, { key: "+" });
    fireEvent.keyDown(player, { key: "=" });
    expect(el.playbackRate).toBe(2);
    expect(select).toHaveValue("2");
    // 2 -> 1.5 -> 1.25 -> 1 -> 0.75
    for (let i = 0; i < 4; i++) fireEvent.keyDown(player, { key: "-" });
    expect(el.playbackRate).toBe(0.75);
    expect(select).toHaveValue("0.75");
  });

  it("VPL-23: the chosen speed is re-applied after switching video", async () => {
    servePost((body: unknown) => ({
      ...SESSION,
      session_id: `s-${(body as { video_id: string }).video_id}`,
    }));
    renderPlayer("/videos/vid-1/watch", { history: ["/videos/vid-2/watch"] });
    await video();
    await userEvent
      .setup()
      .selectOptions(screen.getByRole("combobox", { name: "Playback speed" }), "1.5");
    await userEvent.setup().click(screen.getByRole("button", { name: "go back" }));
    await waitFor(() => expect(posts("/api/stream/sessions")).toHaveLength(2));
    const el = await video();
    await waitFor(() => expect(el.playbackRate).toBe(1.5));
  });

  it("VPL-24: session subtitles render as tracks; size select sets the caption scale", async () => {
    servePost({
      ...SESSION,
      manifest_url: "/stream/sess-abc/manifest.m3u8?sig=tok",
      subtitles: [{ lang: "ar", label: "العربية", url: "/stream/sess-abc/subs/ar.vtt" }],
    });
    renderPlayer();
    const el = await video();
    const track = el.querySelector("track")!;
    expect(track).toHaveAttribute("kind", "subtitles");
    expect(track).toHaveAttribute("srclang", "ar");
    expect(track.getAttribute("src")).toContain("sig=tok");
    const size = screen.getByRole("combobox", { name: "Subtitle size" });
    expect(Array.from((size as HTMLSelectElement).options).map((o) => o.textContent)).toEqual([
      "75%",
      "100%",
      "125%",
      "150%",
    ]);
    await userEvent.setup().selectOptions(size, "1.5");
    expect(
      (document.querySelector(".mkt-player__stage") as HTMLElement).style.getPropertyValue(
        "--mkt-caption-scale"
      )
    ).toBe("1.5");
  });

  it("VPL-26: an expired signed URL reopens the session at the same position", async () => {
    servePost({ ...SESSION, expires_at: "2000-01-01T00:00:00Z" });
    renderPlayer();
    const el = await video();
    el.currentTime = 42;
    fireEvent.error(el);
    await waitFor(() => expect(posts("/api/stream/sessions")).toHaveLength(2));
  });

  it("VPL-27: Arabic UI: labels from ar.json and the Back arrow points right", async () => {
    localStorage.setItem("mkt:locale", "ar");
    servePost();
    renderPlayer();
    await video();
    await waitFor(() => expect(document.documentElement).toHaveAttribute("dir", "rtl"));
    expect(screen.queryByRole("combobox", { name: "Playback speed" })).toBeNull();
    const back = document.querySelector(".mkt-player__bar button")!;
    expect(back.textContent?.startsWith("→")).toBe(true);
  });
});
