// Epic 45 Phase 2 (story 45.13) — the play queue: kept in sessionStorage,
// started from ids or from a grid's query, read on past its first page with
// the cursor GET /api/videos handed back, and Shuffle asks for sort=random.
import { describe, it, expect, vi, beforeEach } from "vitest";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("./api", async () => {
  const actual = await vi.importActual<typeof import("./api")>("./api");
  return { ...actual, api: { ...actual.api, get } };
});

import {
  advanceTo,
  clearQueue,
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

describe("playQueue", () => {
  beforeEach(() => {
    sessionStorage.clear();
    get.mockReset();
  });

  it("starts from ids at an index and walks to the end", async () => {
    expect(startQueue(["a", "b", "c"], 1)).toBe("b");
    expect(queueHas("b")).toBe(true);
    expect(await queueNext("b")).toBe("c");
    advanceTo("c");
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

  it("Play all reads the grid's first page, then the next one with the cursor", async () => {
    get.mockResolvedValueOnce(page(["v1", "v2"], "CUR1")).mockResolvedValueOnce(page(["v2", "v3"]));
    const first = await startQueueFromQuery("sort=title&order=asc&library=L&cursor=x&limit=60");
    expect(first).toBe("v1");
    const url1 = new URL(get.mock.calls[0]![0] as string, "http://x");
    expect(url1.pathname).toBe("/api/videos");
    expect(url1.searchParams.get("sort")).toBe("title");
    expect(url1.searchParams.get("library")).toBe("L");
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
