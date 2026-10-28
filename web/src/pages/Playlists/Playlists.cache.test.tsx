// A change drops the kept answers it makes stale (lib/pageCache): a playlist
// renamed elsewhere never comes back under its old name, and marking a video
// watched drops Home's kept rows.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderPage, memberUser } from "../../test/pageHarness";

const { get, patch, post } = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, patch, post } };
});

import { Playlists } from "./Playlists";
import { markWatched, updatePlaylist } from "../../lib/me";
import { cachedAnswer, keepAnswer } from "../../lib/pageCache";

const summary = (name: string) => ({
  items: [{ id: "p1", name, videos: 2, duration_sec: 0, poster_video_ids: [] }],
});

describe("kept answers after a change", () => {
  beforeEach(() => {
    get.mockReset();
    patch.mockReset();
    post.mockReset();
  });

  it("a renamed playlist never comes back under its old name", async () => {
    get.mockResolvedValue(summary("Old name"));
    renderPage(<Playlists />, { path: "/playlists", user: memberUser });
    expect(await screen.findByText("Old name")).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));

    // renamed (the playlist's own page does this)
    patch.mockResolvedValue({ id: "p1", name: "New name" });
    await updatePlaylist("p1", { name: "New name" });

    // the re-read is still on its way: nothing stale shows meanwhile
    get.mockReturnValue(new Promise(() => {}));
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(screen.queryByText("Old name")).toBeNull();
    expect(document.querySelector('[aria-busy="true"]')).not.toBeNull();
  });

  it("marking a video watched drops every kept list of videos", async () => {
    keepAnswer(memberUser.id, "home", { rows: 1 });
    keepAnswer(memberUser.id, "videos:sort=title", { videos: [] });
    keepAnswer(memberUser.id, "playlists", []);
    keepAnswer(memberUser.id, "libraries", []);
    post.mockResolvedValue({ updated: 1 });
    await markWatched(["v1"], true);
    expect(cachedAnswer(memberUser.id, "home")).toBeUndefined();
    expect(cachedAnswer(memberUser.id, "videos:sort=title")).toBeUndefined();
    expect(cachedAnswer(memberUser.id, "playlists")).toBeUndefined();
    // a list of libraries shows no video's state
    expect(cachedAnswer(memberUser.id, "libraries")).toEqual([]);
  });
});
