// The play queue: on the server since Epic 56 (story 56.13, T01): a library's
// grid makes one POST /api/me/play-queues and the window it answers is used;
// a grid with no library keeps the in-tab queue of Epic 45 (story 45.13), read
// on past its first page with the cursor GET /api/videos handed back.
import { describe, it, expect, vi, beforeEach } from "vitest";

const { get, post, put } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }));
vi.mock("./api", async () => {
  const actual = await vi.importActual<typeof import("./api")>("./api");
  return { ...actual, api: { ...actual.api, get, post, put } };
});

import { ApiError } from "./api";
import {
  addToQueue,
  advanceTo,
  clearQueue,
  fetchCurrentQueue,
  playSource,
  readServerQueue,
  reshuffle,
  queueHas,
  queueNext,
  readQueue,
  shuffled,
  startQueue,
  startQueueFromQuery,
  watchPath,
} from "./playQueue";

const page = (ids: string[], next: string | null = null) => ({
  items: ids.map((id) => ({ id })),
  next,
});

const win = (cur: number, ids: string[], extra: Partial<Record<string, unknown>> = {}) => ({
  id: "Q",
  source: { kind: "library", id: "L" },
  shuffle: false,
  current_pos: cur,
  version: 1,
  source_done: false,
  total_estimate: ids.length,
  items: ids.map((id, pos) => ({ pos, origin: "source", id, title: id })),
  ...extra,
});

describe("playQueue", () => {
  beforeEach(() => {
    sessionStorage.clear();
    get.mockReset();
    post.mockReset();
    put.mockReset();
  });

  it("a library's Shuffle is one POST and the window it answers (T01)", async () => {
    post.mockResolvedValueOnce(win(0, ["s1", "s2", "s3"], { shuffle: true }));
    expect(await startQueueFromQuery("library=L&watched=unwatched&sort=title&order=asc&limit=60", true)).toBe("s1");
    expect(post).toHaveBeenCalledTimes(1);
    const [path, body] = post.mock.calls[0]!;
    expect(path).toBe("/api/me/play-queues");
    expect(body).toMatchObject({
      source: { kind: "library", id: "L", filters: { watched: "unwatched", sort: "title", order: "asc" } },
      shuffle: true,
    });
    expect(get).not.toHaveBeenCalled();
    expect(await queueNext("s1")).toBe("s2");
    expect(queueHas("s1")).toBe(true);
    // moving on is a PUT with the queue's version
    put.mockResolvedValueOnce(win(1, ["s1", "s2", "s3"], { version: 2 }));
    await advanceTo("s2");
    expect(put).toHaveBeenCalledWith("/api/me/play-queues/Q/current", { pos: 1, version: 1 });
    expect(readServerQueue()?.version).toBe(2);
  });

  it("past the window's end, the next window is read", async () => {
    post.mockResolvedValueOnce(win(0, ["a"]));
    await playSource({ kind: "series", id: "S" });
    get.mockResolvedValueOnce({ ...win(0, ["a", "b"]) });
    expect(await queueNext("a")).toBe("b");
    expect(get.mock.calls[0]![0]).toBe("/api/me/play-queues/Q?around=0&before=0&after=30");
  });

  it("an empty source plays nothing", async () => {
    post.mockRejectedValueOnce(new ApiError({ type: "t", title: "empty_source", status: 422 }));
    expect(await playSource({ kind: "library", id: "L", filters: { q: "zz" } })).toBeNull();
    expect(readServerQueue()).toBeNull();
  });

  it("a stale move keeps the latest state and says so", async () => {
    post.mockResolvedValueOnce(win(0, ["a", "b"]));
    await playSource({ kind: "library", id: "L" });
    const latest = win(1, ["a", "b"], { version: 5 });
    put.mockRejectedValueOnce(
      new ApiError({ type: "t", title: "stale_queue", status: 409, queue: latest } as never)
    );
    const heard = vi.fn();
    window.addEventListener("mkt:queue-stale", heard);
    await advanceTo("b");
    window.removeEventListener("mkt:queue-stale", heard);
    expect(heard).toHaveBeenCalled();
    expect(readServerQueue()?.version).toBe(5);
  });

  it("Play next and Add to queue post to the queue; reshuffle; the current queue", async () => {
    post.mockResolvedValueOnce(win(0, ["a", "b"], { shuffle: true }));
    await playSource({ kind: "library", id: "L" }, { shuffle: true });
    post.mockResolvedValueOnce(win(0, ["a", "n", "b"], { shuffle: true }));
    expect(await addToQueue(["n"], "next")).toBe(true);
    expect(post.mock.calls[1]).toEqual(["/api/me/play-queues/Q/items", { video_ids: ["n"], where: "next" }]);
    post.mockResolvedValueOnce(win(0, ["a", "x"], { shuffle: true }));
    await reshuffle();
    expect(post.mock.calls[2]![0]).toBe("/api/me/play-queues/Q/reshuffle");
    get.mockResolvedValueOnce(undefined); // 204: no queue
    expect(await fetchCurrentQueue()).toBeNull();
  });

  it("an old in-tab queue from before is ignored", () => {
    sessionStorage.setItem("mkt:playQueue", JSON.stringify({ ids: ["a"], index: 0 }));
    expect(readQueue()).toBeNull();
    expect(readServerQueue()).toBeNull();
  });

  it("starts from ids at an index and walks to the end", async () => {
    expect(startQueue(["a", "b", "c"], 1)).toBe("b");
    expect(queueHas("b")).toBe(true);
    expect(await queueNext("b")).toBe("c");
    await advanceTo("c");
    expect(readQueue()?.index).toBe(2);
    expect(await queueNext("c")).toBeNull();
    // a video the queue does not hold has no next
    expect(await queueNext("zzz")).toBeNull();
    clearQueue();
    expect(readQueue()).toBeNull();
  });

  it("an empty list clears the queue", () => {
    startQueue(["a"]);
    expect(startQueue([])).toBeNull();
    expect(readQueue()).toBeNull();
  });

  it("every library's Play all reads the first page, then the next one with the cursor", async () => {
    get.mockResolvedValueOnce(page(["v1", "v2"], "CUR1")).mockResolvedValueOnce(page(["v2", "v3"]));
    const first = await startQueueFromQuery("sort=title&order=asc&cursor=x&limit=60");
    expect(first).toBe("v1");
    const url1 = new URL(get.mock.calls[0]![0] as string, "http://x");
    expect(url1.pathname).toBe("/api/videos");
    expect(url1.searchParams.get("sort")).toBe("title");
    expect(url1.searchParams.get("library")).toBeNull();
    expect(url1.searchParams.get("limit")).toBe("200");
    expect(url1.searchParams.get("cursor")).toBeNull();
    expect(await queueNext("v1")).toBe("v2");
    // the end of what is loaded: the next page, without the repeat
    expect(await queueNext("v2")).toBe("v3");
    const url2 = new URL(get.mock.calls[1]![0] as string, "http://x");
    expect(url2.searchParams.get("cursor")).toBe("CUR1");
    expect(readQueue()?.ids).toEqual(["v1", "v2", "v3"]);
    expect(readQueue()?.source).toBeNull();
    expect(await queueNext("v3")).toBeNull();
  });

  it("Shuffle asks for one random page and keeps no cursor", async () => {
    get.mockResolvedValueOnce(page(["r2", "r1"], "IGNORED"));
    expect(await startQueueFromQuery("sort=title&order=asc", true)).toBe("r2");
    const url = new URL(get.mock.calls[0]![0] as string, "http://x");
    expect(url.searchParams.get("sort")).toBe("random");
    expect(url.searchParams.get("order")).toBeNull();
    expect(readQueue()?.source).toBeNull();
  });

  it("a failed next page ends the queue quietly", async () => {
    get.mockResolvedValueOnce(page(["v1"], "C")).mockRejectedValueOnce(new Error("down"));
    await startQueueFromQuery("sort=title");
    expect(await queueNext("v1")).toBeNull();
  });

  it("shuffled keeps every item", () => {
    const xs = ["a", "b", "c", "d"];
    const out = shuffled(xs, () => 0);
    expect([...out].sort()).toEqual(xs);
    expect(out).not.toBe(xs);
  });

  it("blocked storage does not throw", () => {
    const spy = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(startQueue(["a"])).toBe("a");
    spy.mockRestore();
  });

  it("watchPath is the player's route", () => {
    expect(watchPath("a b")).toBe("/videos/a%20b/watch");
  });
});
