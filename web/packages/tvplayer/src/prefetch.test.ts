// 34.6 TC3 — neighbour playlists are fetched, never a segment; refreshes
// pause in a background tab (EC4).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Prefetcher, firstVariant, isPlaylist } from "./prefetch";

const MASTER = `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=6500000,RESOLUTION=1920x1080
720p/index.m3u8
`;
const MEDIA = `#EXTM3U
#EXT-X-TARGETDURATION:2
#EXTINF:2.0,
seg-100.ts
#EXTINF:2.0,
seg-101.ts
`;

const flush = async () => {
  for (let i = 0; i < 10; i++) await Promise.resolve();
};

describe("Prefetcher", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("fetches the playlists (master and first variant) and never a segment", async () => {
    const fetchFn = vi.fn((url: string) =>
      Promise.resolve(new Response(url.includes("720p") ? MEDIA : MASTER, { status: 200 }))
    );
    const p = new Prefetcher({ fetch: fetchFn, visible: () => true });
    p.set(["/stream/channel/b/index.m3u8", "/stream/channel/b/seg-1.ts"]);
    await flush();
    const urls = fetchFn.mock.calls.map((c) => c[0]);
    expect(urls).toEqual(["/stream/channel/b/index.m3u8", "/stream/channel/b/720p/index.m3u8"]);
    expect(urls.some((u) => u.endsWith(".ts"))).toBe(false);
    p.stop();
  });

  it("refreshes every 10 s while visible, pauses in the background", async () => {
    let visible = true;
    const fetchFn = vi.fn(() => Promise.resolve(new Response(MEDIA, { status: 200 })));
    const p = new Prefetcher({ fetch: fetchFn, visible: () => visible, every: 10_000 });
    p.set(["/a.m3u8"]);
    await flush();
    expect(fetchFn).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(10_000);
    await flush();
    expect(fetchFn).toHaveBeenCalledTimes(2);
    visible = false;
    vi.advanceTimersByTime(30_000);
    await flush();
    expect(fetchFn).toHaveBeenCalledTimes(2);
    p.stop();
    visible = true;
    vi.advanceTimersByTime(30_000);
    await flush();
    expect(fetchFn).toHaveBeenCalledTimes(2);
  });

  it("rewrites URLs before fetching (the remote token)", async () => {
    const fetchFn = vi.fn((_url: string) => Promise.resolve(new Response(MEDIA, { status: 200 })));
    const p = new Prefetcher({ fetch: fetchFn, visible: () => true, rewrite: (u) => u + "?t=1" });
    p.set(["/a.m3u8"]);
    await flush();
    expect(fetchFn.mock.calls[0][0]).toBe("/a.m3u8?t=1");
    expect(p.has("/a.m3u8")).toBe(true);
    p.stop();
  });
});

describe("helpers", () => {
  it("knows a playlist from a segment", () => {
    expect(isPlaylist("/x/index.m3u8?retry=1")).toBe(true);
    expect(isPlaylist("https://c.cloudflarestream.com/t/manifest/video.m3u8")).toBe(true);
    expect(isPlaylist("/x/seg-1.ts")).toBe(false);
  });
  it("finds the first variant of a master playlist", () => {
    expect(firstVariant(MASTER, "/stream/channel/b/index.m3u8")).toBe(
      "/stream/channel/b/720p/index.m3u8"
    );
    expect(firstVariant(MEDIA, "/x.m3u8")).toBeNull();
    expect(
      firstVariant(MASTER, "https://customer-a.cloudflarestream.com/tok/manifest/video.m3u8")
    ).toBe("https://customer-a.cloudflarestream.com/tok/manifest/720p/index.m3u8");
  });
});
