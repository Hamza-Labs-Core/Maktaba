// R4.2 — VideoPlayer must perform the real streaming handshake:
//   POST /api/stream/sessions { video_id }
//   -> { session_id, mode, manifest_url, direct_url, expires_at }
//   then play manifest_url|direct_url, then
//   POST /api/stream/sessions/{session_id}/progress { position_sec }
// and must NOT call the unmounted GET /api/videos/{id}/stream.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Routes, Route, useNavigate } from "react-router-dom";

const { post, get, del } = vi.hoisted(() => ({
  post: vi.fn(),
  get: vi.fn(),
  del: vi.fn(),
}));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, post, get, delete: del } };
});

import { VideoPlayer } from "./VideoPlayer";
import { I18nProvider } from "../lib/i18n";

function renderPlayer(videoId = "11111111-1111-1111-1111-111111111111") {
  return render(
    <MemoryRouter initialEntries={[`/videos/${videoId}/watch`]}>
      <I18nProvider>
        <Routes>
          <Route path="/videos/:videoId/watch" element={<VideoPlayer />} />
        </Routes>
      </I18nProvider>
    </MemoryRouter>
  );
}

describe("VideoPlayer", () => {
  beforeEach(() => {
    post.mockReset();
    get.mockReset();
    del.mockReset();
    del.mockResolvedValue(undefined);
  });

  it("opens a stream session via POST /api/stream/sessions and plays manifest_url", async () => {
    post.mockResolvedValue({
      session_id: "sess-abc",
      mode: "transcode",
      manifest_url: "/stream/sess-abc/manifest.m3u8",
      direct_url: "",
      expires_at: "2026-05-17T12:00:00Z",
    });

    renderPlayer("vid-uuid-1");
    await waitFor(() => {
      expect(post).toHaveBeenCalled();
    });
    const openCall = post.mock.calls.find((c) => c[0] === "/api/stream/sessions");
    expect(openCall).toBeTruthy();
    expect(openCall![1]).toMatchObject({ video_id: "vid-uuid-1" });

    // Never hits the old, unmounted route.
    expect(get).not.toHaveBeenCalledWith(
      expect.stringContaining("/api/videos/vid-uuid-1/stream")
    );

    const video = await screen.findByTestId("mkt-video");
    expect(video).toHaveAttribute("src", "/stream/sess-abc/manifest.m3u8");
  });

  it("uses direct_url when the server picks direct mode", async () => {
    post.mockResolvedValue({
      session_id: "sess-direct",
      mode: "direct",
      manifest_url: "",
      direct_url: "/files/original.mp4",
      expires_at: "2026-05-17T12:00:00Z",
    });

    renderPlayer();
    const video = await screen.findByTestId("mkt-video");
    expect(video).toHaveAttribute("src", "/files/original.mp4");
  });

  it("posts progress to /api/stream/sessions/{id}/progress", async () => {
    post.mockResolvedValue({
      session_id: "sess-prog",
      mode: "transcode",
      manifest_url: "/stream/sess-prog/manifest.m3u8",
      direct_url: "",
      expires_at: "2026-05-17T12:00:00Z",
    });

    renderPlayer();
    const video = (await screen.findByTestId("mkt-video")) as HTMLVideoElement;

    // Simulate playback time advancing.
    Object.defineProperty(video, "currentTime", { value: 30, configurable: true });
    video.dispatchEvent(new Event("timeupdate"));

    await waitFor(() => {
      const progressCall = post.mock.calls.find((c) =>
        String(c[0]).endsWith("/progress")
      );
      expect(progressCall).toBeTruthy();
      expect(progressCall![0]).toBe("/api/stream/sessions/sess-prog/progress");
      expect(progressCall![1]).toMatchObject({ position_sec: 30 });
    });
  });

  it("closes the streaming session via DELETE on unmount", async () => {
    post.mockResolvedValue({
      session_id: "sess-close",
      mode: "transcode",
      manifest_url: "/stream/sess-close/manifest.m3u8",
      direct_url: "",
      expires_at: "2026-05-17T12:00:00Z",
    });

    const { unmount } = renderPlayer();
    // Wait until the session has resolved (and the ref is set).
    await screen.findByTestId("mkt-video");

    unmount();

    await waitFor(() => {
      expect(del).toHaveBeenCalledWith("/api/stream/sessions/sess-close");
    });
  });

  it("closes the prior session via DELETE when videoId changes", async () => {
    post
      .mockResolvedValueOnce({
        session_id: "sess-first",
        mode: "transcode",
        manifest_url: "/stream/sess-first/manifest.m3u8",
        direct_url: "",
        expires_at: "2026-05-17T12:00:00Z",
      })
      .mockResolvedValueOnce({
        session_id: "sess-second",
        mode: "transcode",
        manifest_url: "/stream/sess-second/manifest.m3u8",
        direct_url: "",
        expires_at: "2026-05-17T12:00:00Z",
      });

    // Navigate within the same router so the route element stays mounted and
    // the open-session effect re-runs (cleaning up the prior session) on the
    // videoId param change, mirroring real in-app navigation.
    function Nav() {
      const navigate = useNavigate();
      return (
        <button type="button" data-testid="go-next" onClick={() => navigate("/videos/vid-2/watch")}>
          next
        </button>
      );
    }
    render(
      <MemoryRouter initialEntries={["/videos/vid-1/watch"]}>
        <I18nProvider>
          <Nav />
          <Routes>
            <Route path="/videos/:videoId/watch" element={<VideoPlayer />} />
          </Routes>
        </I18nProvider>
      </MemoryRouter>
    );

    await waitFor(() => {
      const video = screen.getByTestId("mkt-video");
      expect(video).toHaveAttribute("src", "/stream/sess-first/manifest.m3u8");
    });

    screen.getByTestId("go-next").click();

    await waitFor(() => {
      expect(del).toHaveBeenCalledWith("/api/stream/sessions/sess-first");
    });
  });

  it("does NOT call DELETE when no session was ever opened", async () => {
    // Session open never resolves -> sessionIdRef stays null.
    post.mockReturnValue(new Promise(() => {}));

    const { unmount } = renderPlayer();
    // The loading state renders while the open request is pending.
    await screen.findByText(/loading/i);

    unmount();

    expect(del).not.toHaveBeenCalled();
  });
});
