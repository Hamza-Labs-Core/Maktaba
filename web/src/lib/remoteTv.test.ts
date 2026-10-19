// Epic 32.9 TC2 — the remote session: the token rewrite (only the token
// segment, only on the Cloudflare host), the 202 retry with the same session,
// the heartbeat timer and its token swap, ending on release and on unload.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { post, del } = vi.hoisted(() => ({ post: vi.fn(), del: vi.fn() }));
vi.mock("./api", async () => {
  const actual = await vi.importActual<typeof import("./api")>("./api");
  return { ...actual, api: { ...actual.api, post, delete: del } };
});

import { ApiError } from "./api";
import {
  HEARTBEAT_MS,
  RemoteSession,
  deviceLabel,
  remoteCode,
  rewriteToken,
  tokenOf,
} from "./remoteTv";

const URL1 = "https://customer-abc.cloudflarestream.com/OLDTOK/manifest/video.m3u8";

const ctx = () => ({
  signal: new AbortController().signal,
  status: vi.fn(),
  freeSlot: vi.fn(),
});
const flush = async () => {
  for (let i = 0; i < 10; i++) await Promise.resolve();
};

describe("rewriteToken", () => {
  it("replaces only the token path segment on the Cloudflare host", () => {
    expect(rewriteToken(URL1, "NEW")).toBe(
      "https://customer-abc.cloudflarestream.com/NEW/manifest/video.m3u8"
    );
    expect(
      rewriteToken("https://customer-abc.cloudflarestream.com/OLDTOK/seg/OLDTOK_3.ts?x=1", "NEW")
    ).toBe("https://customer-abc.cloudflarestream.com/NEW/seg/OLDTOK_3.ts?x=1");
    // other hosts and LAN paths are left alone
    expect(rewriteToken("/stream/channel/c1/index.m3u8", "NEW")).toBe(
      "/stream/channel/c1/index.m3u8"
    );
    expect(rewriteToken("https://example.com/OLDTOK/manifest/video.m3u8", "NEW")).toBe(
      "https://example.com/OLDTOK/manifest/video.m3u8"
    );
    expect(tokenOf(URL1)).toBe("OLDTOK");
    expect(tokenOf("/stream/x.m3u8")).toBeNull();
  });
});

describe("RemoteSession", () => {
  beforeEach(() => {
    post.mockReset();
    del.mockReset();
    del.mockResolvedValue(undefined);
  });
  afterEach(() => vi.useRealTimers());

  it("retries a 202 with the same session until the push is live", async () => {
    vi.useFakeTimers();
    post
      .mockResolvedValueOnce({ session_id: "s1", state: "starting", retry_after: 3 })
      .mockResolvedValueOnce({ session_id: "s1", state: "live", hls_url: URL1 });
    const c = ctx();
    const p = RemoteSession.open("c1", c, { device: "test" });
    await flush();
    expect(c.status).toHaveBeenCalledWith("remote_starting");
    vi.advanceTimersByTime(3000);
    await flush();
    const s = await p;
    expect(s.id).toBe("s1");
    expect(s.url).toBe(URL1);
    expect(post).toHaveBeenCalledTimes(2);
    expect(post.mock.calls[0]).toEqual([
      "/api/tv/remote/tune",
      { channel_id: "c1", device: "test" },
    ]);
    s.release();
  });

  it("beats every 60 s and swaps the token without a new URL for hls.js", async () => {
    vi.useFakeTimers();
    post.mockResolvedValueOnce({ session_id: "s1", state: "live", hls_url: URL1 });
    const s = await RemoteSession.open("c1", ctx(), { device: "t" });
    const urls: string[] = [];
    s.onUrl((u) => urls.push(u));
    post.mockResolvedValueOnce({ state: "live" }); // no new token yet
    vi.advanceTimersByTime(HEARTBEAT_MS);
    await flush();
    expect(post).toHaveBeenLastCalledWith("/api/tv/remote/sessions/s1/heartbeat");
    expect(s.rewrite(URL1)).toBe(URL1);
    post.mockResolvedValueOnce({
      state: "live",
      token: "NEWTOK",
      hls_url: "https://customer-abc.cloudflarestream.com/NEWTOK/manifest/video.m3u8",
    });
    vi.advanceTimersByTime(HEARTBEAT_MS);
    await flush();
    // a request still carrying the old token goes out with the new one
    expect(s.rewrite("https://customer-abc.cloudflarestream.com/OLDTOK/seg_9.ts")).toBe(
      "https://customer-abc.cloudflarestream.com/NEWTOK/seg_9.ts"
    );
    expect(urls).toEqual(["https://customer-abc.cloudflarestream.com/NEWTOK/manifest/video.m3u8"]);
    s.release();
    expect(del).toHaveBeenCalledWith("/api/tv/remote/sessions/s1");
    // no beat after release
    const n = post.mock.calls.length;
    vi.advanceTimersByTime(HEARTBEAT_MS * 2);
    await flush();
    expect(post.mock.calls.length).toBe(n);
  });

  it("ends the session with a keepalive DELETE on page unload", async () => {
    const fetchSpy = vi.fn(() => Promise.resolve(new Response(null, { status: 204 })));
    vi.stubGlobal("fetch", fetchSpy);
    try {
      post.mockResolvedValueOnce({ session_id: "s9", state: "live", hls_url: URL1 });
      await RemoteSession.open("c1", ctx(), { device: "t" });
      window.dispatchEvent(new Event("pagehide"));
      expect(fetchSpy).toHaveBeenCalledWith(
        "/api/tv/remote/sessions/s9",
        expect.objectContaining({ method: "DELETE", keepalive: true })
      );
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("frees the tab's own slot once on user_streams_full, then tunes again", async () => {
    post
      .mockRejectedValueOnce(
        new ApiError({ type: "x", title: "t", status: 429, code: "user_streams_full" } as never)
      )
      .mockResolvedValueOnce({ session_id: "s2", state: "live", hls_url: URL1 });
    const c = ctx();
    const s = await RemoteSession.open("c2", c, { device: "t" });
    expect(c.freeSlot).toHaveBeenCalledTimes(1);
    expect(s.id).toBe("s2");
    s.release();
  });

  it("maps refusals to their codes (402, 429, 503)", async () => {
    for (const [status, code] of [
      [402, "budget_exhausted"],
      [429, "remote_channels_full"],
      [503, "remote_unavailable"],
    ] as const) {
      post.mockRejectedValueOnce(new ApiError({ type: "x", title: "t", status, code } as never));
      await expect(RemoteSession.open("c1", ctx(), { device: "t" })).rejects.toMatchObject({
        code,
      });
    }
    expect(remoteCode(new ApiError({ type: "x", title: "t", status: 402 }))).toBe(
      "budget_exhausted"
    );
    expect(remoteCode(new Error("offline"))).toBe("network");
  });

  it("labels the device without personal data", () => {
    expect(
      deviceLabel(
        "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"
      )
    ).toBe("Chrome on Windows");
    expect(deviceLabel("Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Safari/604.1")).toBe(
      "Safari on iOS"
    );
  });
});
