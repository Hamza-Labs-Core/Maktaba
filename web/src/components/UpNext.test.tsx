// Epic 45 Phase 2 (story 45.13) — the player's Up next: at the end of a video,
// the play queue's next one (else the series' next lesson), a 10-second
// countdown that starts it, Play now and Cancel; nothing when there is no next.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { Route, useLocation } from "react-router-dom";
import { renderPage, memberUser } from "../test/pageHarness";
import { readQueue, startQueue } from "../lib/playQueue";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { UpNext, UP_NEXT_SEC } from "./UpNext";

function Player() {
  const [el, setEl] = useState<HTMLVideoElement | null>(null);
  return (
    <>
      <video data-testid="v" ref={setEl} />
      <UpNext videoId="a" el={el} />
    </>
  );
}

function Watch() {
  const loc = useLocation();
  return (
    <p>
      watching {loc.pathname} {String((loc.state as { autoplay?: boolean } | null)?.autoplay)}
    </p>
  );
}

function open() {
  renderPage(<Player />, {
    path: "/videos/a/watch",
    route: "/videos/a/watch",
    user: memberUser,
    extraRoutes: <Route path="/videos/:videoId/watch" element={<Watch />} />,
  });
  return screen.getByTestId("v") as HTMLVideoElement;
}

describe("UpNext", () => {
  beforeEach(() => {
    get.mockReset();
    sessionStorage.clear();
  });
  afterEach(() => vi.useRealTimers());

  it("offers the queue's next video at the end and Play now opens it playing", async () => {
    startQueue(["a", "b"]);
    get.mockImplementation((url: string) =>
      url === "/api/videos/b"
        ? Promise.resolve({ id: "b", title: "الدرس الثاني" })
        : Promise.reject(new Error(url))
    );
    const v = open();
    expect(screen.queryByRole("dialog")).toBeNull();
    fireEvent(v, new Event("ended"));
    const card = await screen.findByRole("dialog");
    expect(card).toHaveTextContent(`Up next in ${UP_NEXT_SEC} s`);
    expect(card).toHaveTextContent("الدرس الثاني");
    fireEvent.click(screen.getByRole("button", { name: /Play now/ }));
    expect(await screen.findByText("watching /videos/b/watch true")).toBeInTheDocument();
    expect(readQueue()?.index).toBe(1);
  });

  it("counts down and starts the next lesson of the series by itself", async () => {
    get.mockImplementation((url: string) => {
      if (url === "/api/me/videos/a")
        return Promise.resolve({ video_id: "a", series: { id: "s1", name: "S" } });
      // the lesson order, whatever order the series page lists them in
      if (url === "/api/series/s1/episodes?sort=episode")
        return Promise.resolve({
          seasons: [
            {
              season: 1,
              episodes: [
                { video_id: "a", title: "One" },
                { video_id: "c", title: "Two" },
              ],
            },
          ],
        });
      return Promise.reject(new Error(url));
    });
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const v = open();
    fireEvent(v, new Event("ended"));
    expect(await screen.findByRole("dialog")).toHaveTextContent("Two");
    for (let i = 0; i < UP_NEXT_SEC; i++) {
      await act(async () => {
        vi.advanceTimersByTime(1000);
      });
    }
    expect(await screen.findByText("watching /videos/c/watch true")).toBeInTheDocument();
  });

  it("Cancel stays on the video", async () => {
    startQueue(["a", "b"]);
    get.mockResolvedValue({ id: "b", title: "B" });
    const v = open();
    fireEvent(v, new Event("ended"));
    await screen.findByRole("dialog");
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByText(/watching/)).toBeNull();
  });

  it("shows nothing when there is no next video", async () => {
    get.mockRejectedValue(new Error("no series route"));
    const v = open();
    fireEvent(v, new Event("ended"));
    await waitFor(() => expect(get).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});
