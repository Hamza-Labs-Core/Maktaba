// Epic 45 Phase 2 — lib/me: the marks are sent in chunks of 500, the readers
// are defensive (missing lists are empty), and a lesson's place in its series
// comes from the series' episodes in order.
import { describe, it, expect, vi, beforeEach } from "vitest";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("./api", async () => {
  const actual = await vi.importActual<typeof import("./api")>("./api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import {
  createPlaylist,
  fetchFacets,
  fetchMyVideo,
  fetchPlaylists,
  markWatched,
  seriesPosition,
  setFavorite,
} from "./me";

describe("lib/me", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
  });

  it("marks 1,201 videos in three calls of at most 500", async () => {
    post.mockImplementation((_p: string, b: { video_ids: string[] }) =>
      Promise.resolve({ updated: b.video_ids.length })
    );
    const ids = Array.from({ length: 1201 }, (_, i) => `v${i}`);
    expect(await markWatched(ids, true)).toBe(1201);
    expect(post).toHaveBeenCalledTimes(3);
    expect(post.mock.calls[0]![0]).toBe("/api/me/watched");
    expect(post.mock.calls[0]![1]).toMatchObject({ watched: true });
    expect(post.mock.calls.map((c) => (c[1] as { video_ids: string[] }).video_ids.length)).toEqual([
      500, 500, 201,
    ]);
    post.mockClear();
    await setFavorite(["a"], false);
    expect(post).toHaveBeenCalledWith("/api/me/favorites", { video_ids: ["a"], favorite: false });
  });

  it("a new playlist with many videos adds the rest after creating it", async () => {
    post.mockImplementation((path: string) =>
      Promise.resolve(path === "/api/me/playlists" ? { id: "p1", name: "N" } : { added: 1 })
    );
    const ids = Array.from({ length: 501 }, (_, i) => `v${i}`);
    const p = await createPlaylist("N", "", ids);
    expect(p.id).toBe("p1");
    expect((post.mock.calls[0]![1] as { video_ids: string[] }).video_ids).toHaveLength(500);
    expect(post.mock.calls[1]![0]).toBe("/api/me/playlists/p1/items");
  });

  it("readers survive missing fields", async () => {
    get.mockResolvedValue({});
    expect(await fetchFacets("L")).toEqual({ languages: [], tags: [], speakers: [], letters: [] });
    expect(get).toHaveBeenLastCalledWith("/api/videos/facets?library=L");
    expect(await fetchPlaylists()).toEqual([]);
    const mine = await fetchMyVideo("v1");
    expect(mine).toMatchObject({ video_id: "v1", favorite: false, completed: false, series: null });
  });

  it("finds a lesson's place, previous and next across seasons", async () => {
    get.mockResolvedValue({
      seasons: [
        { season: 1, episodes: [{ video_id: "a", title: "A", watched: true, progress_pct: 100 }] },
        {
          season: 2,
          episodes: [
            { video_id: "b", title: "B", watched: false, progress_pct: 10 },
            { video_id: "c", title: "C", watched: false, progress_pct: 0 },
          ],
        },
      ],
    });
    const pos = await seriesPosition({ id: "s1", name: "S" }, "b");
    expect(pos).toMatchObject({ index: 1, total: 3, watched: 1 });
    expect(pos?.previous?.video_id).toBe("a");
    expect(pos?.next?.video_id).toBe("c");
    expect(await seriesPosition({ id: "s1", name: "S" }, "zzz")).toBeNull();
  });
});
