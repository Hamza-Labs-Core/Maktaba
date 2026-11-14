// Epic 58 (stories 58.10, 58.11): the player inside a Samsung or LG launcher. The TV's own
// player plays the stream (launcherMedia.ts, tested on its own), and the page:
//   - opens the session with the launcher's client_profile and device_caps;
//   - hands the launcher the signed URL, the start position and the sidecar subtitles;
//   - on the TV player's first error, reports it and opens the session again once with
//     force_transcode, from where it stopped; a second error is the error screen.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";

const { post, get, del, attach, report } = vi.hoisted(() => ({
  post: vi.fn(),
  get: vi.fn(),
  del: vi.fn(),
  attach: vi.fn(),
  report: vi.fn(),
}));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, post, get, delete: del } };
});
const CAPS = { containers: ["mp4"], video: [], hdr: [], audio: ["aac"], dash: false };
vi.mock("../bigscreen/input/launcherMedia", () => ({
  launcherPlays: () => true,
  launcherSessionFields: () => ({ client_profile: "webos", device_caps: CAPS }),
  attachLauncher: attach,
}));
vi.mock("../lib/playbackReport", async () => {
  const actual =
    await vi.importActual<typeof import("../lib/playbackReport")>("../lib/playbackReport");
  return { ...actual, reportPlaybackFailure: report };
});

import { VideoPlayer } from "./VideoPlayer";
import { I18nProvider } from "../lib/i18n";

type OnError = (error: string) => void;

function session(id: string) {
  return {
    session_id: id,
    mode: "direct",
    manifest_url: `/stream/${id}/file.mkv?sig=s1`,
    direct_url: `/stream/${id}/file.mkv?sig=s1`,
    expires_at: "2099-01-01T00:00:00Z",
    subtitles: [{ id: "ar", language: "ar", label: "العربية", url: `/stream/${id}/ar.vtt` }],
  };
}

function renderPlayer(at = 0) {
  localStorage.setItem("mkt:locale", "en");
  return render(
    <MemoryRouter initialEntries={[`/videos/v1/watch${at ? `?t=${at}` : ""}`]}>
      <I18nProvider>
        <Routes>
          <Route path="/videos/:videoId/watch" element={<VideoPlayer />} />
        </Routes>
      </I18nProvider>
    </MemoryRouter>
  );
}

const opens = () => post.mock.calls.filter((c) => c[0] === "/api/stream/sessions");

beforeEach(() => {
  post.mockReset();
  get.mockReset();
  del.mockReset();
  attach.mockReset();
  report.mockReset();
  localStorage.clear();
  del.mockResolvedValue(undefined);
  get.mockResolvedValue({ items: [] });
  let n = 0;
  post.mockImplementation((url: string) =>
    Promise.resolve(url === "/api/stream/sessions" ? session(`s${++n}`) : undefined)
  );
  attach.mockImplementation(() => ({ detach: vi.fn() }));
  vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockReturnValue("maybe");
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
});
afterEach(() => {
  vi.restoreAllMocks();
});

describe("VideoPlayer in a TV launcher (58.10, 58.11)", () => {
  it("opens the session with the TV's profile and caps, and hands the launcher the stream", async () => {
    renderPlayer(30);
    await waitFor(() => expect(attach).toHaveBeenCalledTimes(1));
    expect(opens()[0][1]).toEqual({
      video_id: "v1",
      start_sec: 30,
      client_profile: "webos",
      device_caps: CAPS,
    });
    const [el, media] = attach.mock.calls[0];
    expect(el).toBe(screen.getByTestId("mkt-video"));
    expect(media.url).toBe("/stream/s1/file.mkv?sig=s1");
    expect(media.startAt).toBe(30);
    // the sidecar carries the stream's signature
    expect(media.subtitles).toHaveLength(1);
    expect(media.subtitles[0]).toMatchObject({ id: "ar", language: "ar" });
    expect(media.subtitles[0].url).toContain("sig=s1");
    // the page's own element never gets the stream
    expect(screen.getByTestId("mkt-video").getAttribute("src")).toBeNull();
  });

  it("a TV player error reopens once as a transcode from there; a second one is the error screen", async () => {
    renderPlayer();
    await waitFor(() => expect(attach).toHaveBeenCalledTimes(1));
    const el = screen.getByTestId("mkt-video") as HTMLVideoElement;
    Object.defineProperty(el, "currentTime", { value: 75.4, configurable: true });

    act(() => (attach.mock.calls[0][2] as OnError)("webos media error 4"));
    await waitFor(() => expect(attach).toHaveBeenCalledTimes(2));
    expect(report).toHaveBeenCalledTimes(1);
    expect(report.mock.calls[0][1]).toMatchObject({
      outcome: "reopen",
      code: "launcher_player",
      message: "webos media error 4",
    });
    expect(opens()).toHaveLength(2);
    expect(opens()[1][1]).toMatchObject({
      video_id: "v1",
      start_sec: 75,
      force_transcode: true,
      client_profile: "webos",
    });
    expect(attach.mock.calls[1][1].url).toBe("/stream/s2/file.mkv?sig=s1");
    expect(screen.queryByRole("alert")).toBeNull();

    act(() => (attach.mock.calls[1][2] as OnError)("webos media error 3"));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("This video can't play right now");
    expect(report.mock.calls[1][1]).toMatchObject({ outcome: "fatal", code: "launcher_player" });
    // no third session
    expect(opens()).toHaveLength(2);
  });
});
