// Epic 57 story 57.15 (T04): the web player under the home's time rules.
//
// The home refuses with 403 `parental-time` when a restricted account's time is over:
// at the session's open, and at a progress post while the video plays. The player
// then stops, shows the time's-up screen with when watching opens again, and asks
// the home nothing more until the viewer taps Check again. While it plays it posts
// progress at least every 60 seconds, and shows the time the home says is left.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Routes, Route } from "react-router-dom";

const { post, get, del } = vi.hoisted(() => ({ post: vi.fn(), get: vi.fn(), del: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, post, get, delete: del } };
});

import { ApiError } from "../lib/api";
import { VideoPlayer } from "./VideoPlayer";
import { I18nProvider } from "../lib/i18n";
import { ParentalFixture } from "../lib/parental";
import { withRid } from "../test/ridMatcher";

const SESSION = {
  session_id: "sess-1",
  mode: "transcode",
  manifest_url: "/stream/sess-1/manifest.m3u8",
  direct_url: "",
  expires_at: "2099-01-01T00:00:00Z",
};
const NEXT = (() => {
  const at = new Date();
  at.setHours(23, 15, 0, 0);
  return at;
})();

function timeOver(reason = "limit_reached", next: string | null = NEXT.toISOString()) {
  return new ApiError({
    type: "https://maktaba.tv/problems/parental-time",
    title: "watching time is over",
    status: 403,
    code: "parental_time",
    reason,
    ...(next ? { next_allowed_at: next } : {}),
  } as never);
}

/** What GET /api/me/parental/time answers; the other reads (chapters) answer an empty list. */
let myTime: Record<string, unknown> | null = null;

function renderPlayer(opts: { restricted?: boolean; locale?: "en" | "ar"; at?: number } = {}) {
  localStorage.setItem("mkt:locale", opts.locale ?? "en");
  const q = opts.at ? `?t=${opts.at}` : "";
  return render(
    <MemoryRouter initialEntries={[`/videos/v1/watch${q}`]}>
      <I18nProvider>
        <ParentalFixture value={{ restricted: opts.restricted ?? true }}>
          <Routes>
            <Route path="/videos/:videoId/watch" element={<VideoPlayer />} />
          </Routes>
        </ParentalFixture>
      </I18nProvider>
    </MemoryRouter>
  );
}

const opens = () => post.mock.calls.filter((c) => c[0] === "/api/stream/sessions");
const progress = () => post.mock.calls.filter((c) => String(c[0]).endsWith("/progress"));

beforeEach(() => {
  post.mockReset();
  get.mockReset();
  del.mockReset();
  del.mockResolvedValue(undefined);
  localStorage.clear();
  myTime = { allowed: true, limited: false, remaining_seconds: null };
  get.mockImplementation((path: string) =>
    Promise.resolve(path.startsWith("/api/me/parental/time") ? myTime : { items: [] })
  );
  vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockReturnValue("maybe");
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
});
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("VideoPlayer under the home's time rules (57.15)", () => {
  it("shows the time's-up screen when the home refuses the session, and does not retry", async () => {
    post.mockRejectedValue(timeOver("limit_reached"));
    renderPlayer();
    expect(await screen.findByRole("heading", { name: "Time's up for today" })).toBeInTheDocument();
    const up = screen.getByTestId("parental-times-up");
    expect(up).toHaveTextContent(/You can watch again at 11:15/);
    expect(up).toHaveTextContent("Ask a parent for more time");
    // no player, no generic error with its Try again
    expect(screen.queryByTestId("mkt-video")).toBeNull();
    expect(screen.queryByText("This video can't play right now")).toBeNull();
    expect(screen.queryByRole("button", { name: "Try again" })).toBeNull();
    // and the home is not asked again by itself
    await act(async () => {
      await new Promise((r) => setTimeout(r, 50));
    });
    expect(opens()).toHaveLength(1);
  });

  it("outside the watching hours says so", async () => {
    post.mockRejectedValue(timeOver("outside_slot"));
    renderPlayer();
    expect(
      await screen.findByRole("heading", { name: "Not watching time yet" })
    ).toBeInTheDocument();
    expect(screen.getByTestId("parental-times-up")).toHaveAttribute("data-reason", "outside_slot");
  });

  it("is Arabic in Arabic", async () => {
    post.mockRejectedValue(timeOver("limit_reached", null));
    renderPlayer({ locale: "ar" });
    expect(await screen.findByRole("heading", { name: "انتهى وقت اليوم" })).toBeInTheDocument();
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    expect(screen.getByRole("button", { name: "تحقّق مجددًا" })).toBeInTheDocument();
  });

  it("stops mid-video when a progress post is refused: the session closes, the place is shown", async () => {
    post.mockImplementation((path: string) =>
      path === "/api/stream/sessions"
        ? Promise.resolve(SESSION)
        : path.endsWith("/progress")
          ? Promise.reject(timeOver("limit_reached"))
          : Promise.resolve(undefined)
    );
    renderPlayer();
    const video = (await screen.findByTestId("mkt-video")) as HTMLVideoElement;
    await waitFor(() =>
      expect(video).toHaveAttribute("src", withRid("/stream/sess-1/manifest.m3u8"))
    );
    Object.defineProperty(video, "currentTime", { value: 754, configurable: true });
    video.dispatchEvent(new Event("timeupdate"));

    expect(await screen.findByRole("heading", { name: "Time's up for today" })).toBeInTheDocument();
    expect(screen.getByTestId("parental-times-up")).toHaveTextContent(
      "Your place in this video is saved at 12:34."
    );
    expect(HTMLMediaElement.prototype.pause).toHaveBeenCalled();
    expect(screen.queryByTestId("mkt-video")).toBeNull();
    await waitFor(() => expect(del).toHaveBeenCalledWith("/api/stream/sessions/sess-1"));
    // playback stopped: one open, one refused post, nothing after
    await act(async () => {
      await new Promise((r) => setTimeout(r, 50));
    });
    expect(opens()).toHaveLength(1);
    expect(progress()).toHaveLength(1);
  });

  it("goes on at the viewer's tap once a parent gave more time", async () => {
    post.mockRejectedValueOnce(timeOver("limit_reached"));
    renderPlayer();
    await screen.findByTestId("parental-times-up");
    post.mockResolvedValue(SESSION);
    await userEvent.click(screen.getByRole("button", { name: "Check again" }));
    const video = await screen.findByTestId("mkt-video");
    await waitFor(() =>
      expect(video).toHaveAttribute("src", withRid("/stream/sess-1/manifest.m3u8"))
    );
    expect(screen.queryByTestId("parental-times-up")).toBeNull();
    expect(opens()).toHaveLength(2);
  });

  it("stops when the home's own answer says the time is over", async () => {
    post.mockResolvedValue(SESSION);
    myTime = {
      allowed: false,
      limited: true,
      reason: "outside_slot",
      remaining_seconds: 0,
      next_allowed_at: NEXT.toISOString(),
    };
    renderPlayer();
    expect(
      await screen.findByRole("heading", { name: "Not watching time yet" })
    ).toBeInTheDocument();
    await waitFor(() => expect(del).toHaveBeenCalledWith("/api/stream/sessions/sess-1"));
  });

  it("shows the time left once 15 minutes or fewer remain", async () => {
    post.mockResolvedValue(SESSION);
    myTime = { allowed: true, limited: true, remaining_seconds: 14 * 60 };
    renderPlayer();
    await screen.findByTestId("mkt-video");
    expect(await screen.findByTestId("parental-time-left")).toHaveTextContent(
      "14 minutes left today"
    );
    expect(screen.queryByTestId("parental-time-warning")).toBeNull();
  });

  it("warns five minutes before the end", async () => {
    post.mockResolvedValue(SESSION);
    myTime = { allowed: true, limited: true, remaining_seconds: 300 };
    renderPlayer();
    expect(await screen.findByTestId("parental-time-warning")).toHaveTextContent(
      "Watching ends in 5 minutes."
    );
  });

  it("shows no time while more than 15 minutes remain", async () => {
    post.mockResolvedValue(SESSION);
    myTime = { allowed: true, limited: true, remaining_seconds: 3000 };
    renderPlayer();
    await screen.findByTestId("mkt-video");
    await waitFor(() => expect(get).toHaveBeenCalledWith("/api/me/parental/time"));
    expect(screen.queryByTestId("parental-time-left")).toBeNull();
  });

  it("asks the home for no time for an account that is not restricted", async () => {
    post.mockResolvedValue(SESSION);
    renderPlayer({ restricted: false });
    await screen.findByTestId("mkt-video");
    await act(async () => {});
    expect(get).not.toHaveBeenCalledWith("/api/me/parental/time");
  });

  it("posts progress at least every 60 seconds while it plays, and at once when it plays again", async () => {
    post.mockImplementation((path: string) =>
      Promise.resolve(path === "/api/stream/sessions" ? SESSION : undefined)
    );
    renderPlayer();
    const video = (await screen.findByTestId("mkt-video")) as HTMLVideoElement;
    await waitFor(() =>
      expect(video).toHaveAttribute("src", withRid("/stream/sess-1/manifest.m3u8"))
    );
    const now = vi.spyOn(Date, "now");
    const t0 = 1_800_000_000_000;
    let at = 0;
    // a minute of playback: the element reports its time about four times a second
    for (let ms = 0; ms <= 60_000; ms += 250) {
      now.mockReturnValue(t0 + ms);
      at = ms / 1000;
      Object.defineProperty(video, "currentTime", { value: at, configurable: true });
      video.dispatchEvent(new Event("timeupdate"));
    }
    const sent = progress();
    // every 5 s: far inside the 60 s the home serves a restricted stream for
    expect(sent.length).toBeGreaterThanOrEqual(12);
    expect(sent[0]![0]).toBe("/api/stream/sessions/sess-1/progress");

    // paused for longer than a minute, then playing again: a post goes out at once,
    // which is what opens the stream again on the home
    const before = progress().length;
    now.mockReturnValue(t0 + 200_000);
    video.dispatchEvent(new Event("play"));
    expect(progress().length).toBe(before + 1);
  });
});
