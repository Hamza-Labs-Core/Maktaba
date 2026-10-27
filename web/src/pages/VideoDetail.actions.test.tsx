// Epic 45 Phase 2 (story 45.14) — the video page's actions and series panel:
// Resume where you stopped, Mark watched / unwatched, Favorite, Add to
// playlist, and for a lesson "Lesson n of m" with the series' progress and the
// previous / next lessons. A server without GET /api/me/videos/{id} shows none
// of it.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderPage, memberUser } from "../test/pageHarness";
import ar from "../i18n/ar.json";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});
vi.mock("../lib/ws", () => ({ subscribe: () => () => {} }));
vi.mock("../components/MediaContextCard", () => ({ MediaContextCard: () => null }));
vi.mock("../components/EnrichmentPanel", () => ({ EnrichmentPanel: () => null }));

import { VideoDetail } from "./VideoDetail";

const VIDEO = { id: "v2", filename: "2.mp4", title: "الدرس الثاني", state: "ready" };
const MINE = {
  video_id: "v2",
  position_sec: 754,
  completed: false,
  favorite: false,
  playlists: [{ id: "p1", name: "Study" }],
  series: { id: "s1", name: "شرح الأربعين" },
};
const EPISODES = {
  seasons: [
    {
      season: 1,
      episodes: [
        { video_id: "v1", title: "الدرس الأول", watched: true, progress_pct: 100 },
        { video_id: "v2", title: "الدرس الثاني", watched: false, progress_pct: 40 },
        { video_id: "v3", title: "الدرس الثالث", watched: false, progress_pct: 0 },
      ],
    },
  ],
};

function serve(mine: unknown = MINE) {
  get.mockImplementation((url: string) => {
    if (url === "/api/videos/v2") return Promise.resolve(VIDEO);
    if (url === "/api/me/videos/v2")
      return mine instanceof Error ? Promise.reject(mine) : Promise.resolve(mine);
    if (url === "/api/series/s1/episodes") return Promise.resolve(EPISODES);
    if (url === "/api/me/playlists") return Promise.resolve({ items: [] });
    return Promise.resolve({ items: [] });
  });
}

function open() {
  return renderPage(<VideoDetail />, {
    path: "/videos/v2",
    route: "/videos/:videoId",
    user: memberUser,
  });
}

describe("VideoDetail actions", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    localStorage.clear();
  });

  it("offers Resume at the saved position and the lesson's place in its series", async () => {
    serve();
    open();
    const resume = await screen.findByRole("link", { name: /Resume from 12:34/ });
    expect(resume).toHaveAttribute("href", "/videos/v2/watch?t=754");
    const nav = await screen.findByRole("navigation", { name: "Lessons of the series" });
    expect(nav).toHaveTextContent("Lesson 2 of 3 in");
    expect(within(nav).getByRole("link", { name: "شرح الأربعين" })).toHaveAttribute(
      "href",
      "/series/s1"
    );
    expect(nav).toHaveTextContent("1 of 3 lessons watched");
    expect(within(nav).getByRole("link", { name: /Previous: الدرس الأول/ })).toHaveAttribute(
      "href",
      "/videos/v1"
    );
    expect(within(nav).getByRole("link", { name: /Next: الدرس الثالث/ })).toHaveAttribute(
      "href",
      "/videos/v3"
    );
    expect(screen.getByRole("link", { name: "Study" })).toHaveAttribute("href", "/playlists/p1");
  });

  it("marks watched and back, and toggles the favorite", async () => {
    serve();
    post.mockResolvedValue({ updated: 1 });
    open();
    const user = userEvent.setup();
    const mark = await screen.findByRole("button", { name: "Mark watched" });
    await user.click(mark);
    expect(post).toHaveBeenCalledWith("/api/me/watched", { video_ids: ["v2"], watched: true });
    const watched = await screen.findByRole("button", { name: /Watched/ });
    expect(watched).toHaveAttribute("aria-pressed", "true");
    // a watched video has nothing to resume
    expect(screen.queryByRole("link", { name: /Resume/ })).toBeNull();
    await user.click(watched);
    expect(post).toHaveBeenLastCalledWith("/api/me/watched", { video_ids: ["v2"], watched: false });

    const fav = screen.getByRole("button", { name: /Favorite/ });
    expect(fav).toHaveAttribute("aria-pressed", "false");
    await user.click(fav);
    expect(post).toHaveBeenLastCalledWith("/api/me/favorites", {
      video_ids: ["v2"],
      favorite: true,
    });
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /In favorites/ })).toHaveAttribute(
        "aria-pressed",
        "true"
      )
    );
  });

  it("adds the video to a new playlist", async () => {
    serve();
    post.mockResolvedValue({ id: "p9", name: "Ramadan" });
    open();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: /Add to playlist/ }));
    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByText(/no playlists yet/i)).toBeInTheDocument();
    await user.type(within(dialog).getByLabelText("New playlist name"), "Ramadan");
    await user.click(within(dialog).getByRole("button", { name: "Create and add" }));
    expect(post).toHaveBeenCalledWith("/api/me/playlists", {
      name: "Ramadan",
      video_ids: ["v2"],
    });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(screen.getByRole("link", { name: "Ramadan" })).toHaveAttribute("href", "/playlists/p9");
  });

  it("a server without the route shows the page as before", async () => {
    serve(new Error("404"));
    open();
    expect(await screen.findByRole("heading", { name: "الدرس الثاني" })).toBeInTheDocument();
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.queryByRole("button", { name: "Mark watched" })).toBeNull();
  });

  it("speaks Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    serve();
    open();
    expect(
      await screen.findByRole("button", { name: ar["video.markWatched"] })
    ).toBeInTheDocument();
    expect(
      await screen.findByRole("navigation", { name: ar["video.series.nav"] })
    ).toHaveTextContent("الدرس ٢ من ٣ في");
  });
});
