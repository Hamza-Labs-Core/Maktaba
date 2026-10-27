// Epic 45 Phase 2 (story 45.10) — Playlists (`/playlists`) and one playlist
// (`/playlists/:id`): the list, creating one, the page's order, removing,
// renaming, deleting with a confirmation, Play all / Shuffle / Play from here
// into the play queue, a 404, and Arabic.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route } from "react-router-dom";
import { ApiError } from "../../lib/api";
import { renderPage, memberUser } from "../../test/pageHarness";
import { readQueue } from "../../lib/playQueue";
import ar from "../../i18n/ar.json";

const { get, post, put, patch, del } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  patch: vi.fn(),
  del: vi.fn(),
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put, patch, delete: del } };
});

import { Playlists } from "./Playlists";
import { PlaylistPage } from "./PlaylistPage";

const P1 = {
  id: "p1",
  name: "رمضان",
  description: "",
  videos: 3,
  duration_sec: 3600,
  poster_video_ids: ["a"],
};
const card = (id: string, extra: Record<string, unknown> = {}) => ({
  id,
  title: `Lesson ${id}`,
  filename: `${id}.mp4`,
  library_id: "L1",
  duration_sec: 1200,
  state: "ready",
  ...extra,
});
const DETAIL = {
  playlist: P1,
  items: [card("a", { completed: true }), card("b", { position_sec: 600 }), card("c")],
};

const playerRoute = <Route path="/videos/:videoId/watch" element={<p>player</p>} />;

describe("Playlists", () => {
  beforeEach(() => {
    for (const f of [get, post, put, patch, del]) f.mockReset();
    sessionStorage.clear();
    localStorage.clear();
  });

  it("lists the playlists with their counts", async () => {
    get.mockResolvedValue({ items: [P1] });
    renderPage(<Playlists />, { path: "/playlists", user: memberUser });
    const link = await screen.findByRole("link", { name: /رمضان/ });
    expect(link).toHaveAttribute("href", "/playlists/p1");
    expect(link).toHaveTextContent("3 videos");
    expect(link).toHaveTextContent("1:00:00");
  });

  it("shows the empty state and creates a playlist, then opens it", async () => {
    get.mockImplementation((url: string) =>
      Promise.resolve(url === "/api/me/playlists" ? { items: [] } : DETAIL)
    );
    post.mockResolvedValue({ ...P1, id: "new1", name: "Study" });
    renderPage(<Playlists />, {
      path: "/playlists",
      user: memberUser,
      extraRoutes: <Route path="/playlists/:playlistId" element={<p>opened</p>} />,
    });
    expect(await screen.findByText("No playlists yet")).toBeInTheDocument();
    const user = userEvent.setup();
    const create = screen.getByRole("button", { name: "Create playlist" });
    expect(create).toBeDisabled();
    await user.type(screen.getByLabelText("New playlist name"), "Study");
    await user.click(create);
    expect(post).toHaveBeenCalledWith("/api/me/playlists", { name: "Study" });
    expect(await screen.findByText("opened")).toBeInTheDocument();
  });

  it("an error offers Try again", async () => {
    get.mockRejectedValueOnce(new ApiError({ type: "about:blank", title: "boom", status: 500 }));
    get.mockResolvedValueOnce({ items: [P1] });
    renderPage(<Playlists />, { path: "/playlists", user: memberUser });
    await userEvent.setup().click(await screen.findByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("link", { name: /رمضان/ })).toBeInTheDocument();
  });
});

describe("PlaylistPage", () => {
  beforeEach(() => {
    for (const f of [get, post, put, patch, del]) f.mockReset();
    sessionStorage.clear();
  });

  function open() {
    return renderPage(<PlaylistPage />, {
      path: "/playlists/p1",
      route: "/playlists/:playlistId",
      user: memberUser,
      extraRoutes: (
        <>
          {playerRoute}
          <Route path="/playlists" element={<p>all playlists</p>} />
        </>
      ),
    });
  }

  it("lists the videos in order with their state", async () => {
    get.mockResolvedValue(DETAIL);
    open();
    expect(await screen.findByRole("heading", { name: "رمضان" })).toBeInTheDocument();
    const items = screen.getAllByRole("listitem");
    expect(items).toHaveLength(3);
    expect(items[0]).toHaveTextContent("Lesson a");
    expect(items[0]).toHaveTextContent("Watched");
    expect(items[1]).toHaveTextContent("50%");
    expect(within(items[0]!).getByRole("button", { name: "Move Lesson a up" })).toBeDisabled();
    expect(within(items[2]!).getByRole("button", { name: "Move Lesson c down" })).toBeDisabled();
  });

  it("moves a video down and saves the new order", async () => {
    get.mockResolvedValue(DETAIL);
    put.mockResolvedValue({
      ...DETAIL,
      items: [DETAIL.items[1], DETAIL.items[0], DETAIL.items[2]],
    });
    open();
    await screen.findByRole("heading", { name: "رمضان" });
    await userEvent.setup().click(screen.getByRole("button", { name: "Move Lesson a down" }));
    expect(put).toHaveBeenCalledWith("/api/me/playlists/p1/order", { video_ids: ["b", "a", "c"] });
    await waitFor(() => expect(screen.getAllByRole("listitem")[0]).toHaveTextContent("Lesson b"));
  });

  it("removes a video", async () => {
    get.mockResolvedValue(DETAIL);
    del.mockResolvedValue(undefined);
    open();
    await screen.findByRole("heading", { name: "رمضان" });
    await userEvent
      .setup()
      .click(screen.getByRole("button", { name: "Remove Lesson b from the playlist" }));
    expect(del).toHaveBeenCalledWith("/api/me/playlists/p1/items/b");
    await waitFor(() => expect(screen.getAllByRole("listitem")).toHaveLength(2));
  });

  it("Play from here fills the queue from that video and opens the player", async () => {
    get.mockResolvedValue(DETAIL);
    open();
    await screen.findByRole("heading", { name: "رمضان" });
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Play from Lesson b" }));
    expect(await screen.findByText("player")).toBeInTheDocument();
    expect(readQueue()).toMatchObject({ ids: ["a", "b", "c"], index: 1 });
  });

  it("Shuffle queues every video", async () => {
    get.mockResolvedValue(DETAIL);
    open();
    await screen.findByRole("heading", { name: "رمضان" });
    await userEvent.setup().click(screen.getByRole("button", { name: /Shuffle/ }));
    expect(await screen.findByText("player")).toBeInTheDocument();
    expect([...(readQueue()?.ids ?? [])].sort()).toEqual(["a", "b", "c"]);
  });

  it("renames and deletes after confirming", async () => {
    get.mockResolvedValue(DETAIL);
    patch.mockResolvedValue({ ...P1, name: "New name" });
    del.mockResolvedValue(undefined);
    open();
    await screen.findByRole("heading", { name: "رمضان" });
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Rename" }));
    const field = screen.getByLabelText("Name");
    await user.clear(field);
    await user.type(field, "New name");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(patch).toHaveBeenCalledWith("/api/me/playlists/p1", { name: "New name" });
    expect(await screen.findByRole("heading", { name: "New name" })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Delete playlist" }));
    const dialog = await screen.findByRole("dialog");
    expect(del).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole("button", { name: "Delete playlist" }));
    expect(del).toHaveBeenCalledWith("/api/me/playlists/p1");
    expect(await screen.findByText("all playlists")).toBeInTheDocument();
  });

  it("another user's playlist is not found", async () => {
    get.mockRejectedValue(new ApiError({ type: "about:blank", title: "not found", status: 404 }));
    open();
    expect(await screen.findByText("Playlist not found")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Back to playlists" })).toHaveAttribute(
      "href",
      "/playlists"
    );
  });

  it("speaks Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    get.mockResolvedValue(DETAIL);
    open();
    expect(
      await screen.findByRole("button", { name: new RegExp(ar["browse.playAll"]) })
    ).toBeInTheDocument();
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    localStorage.clear();
  });
});
