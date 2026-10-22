// The two-element pipeline and the hls.js token loader (32.9 AC2/AC4, 34.6 D2).
import { describe, expect, it, vi } from "vitest";
import type HlsType from "hls.js";
import { attachLive } from "./live";
import { VideoPipeline } from "./pipeline";

const loads: string[] = [];
class BaseLoader {
  constructor(public config: unknown) {}
  load(context: { url: string }) {
    loads.push(context.url);
  }
}
const instances: Array<{ config: Record<string, unknown>; loadSource: ReturnType<typeof vi.fn> }> =
  [];
class FakeHls {
  static DefaultConfig = { loader: BaseLoader };
  static Events = { ERROR: "hlsError" };
  static ErrorTypes = { NETWORK_ERROR: "networkError", MEDIA_ERROR: "mediaError" };
  static isSupported = () => true;
  loadSource = vi.fn();
  attachMedia = vi.fn();
  destroy = vi.fn();
  startLoad = vi.fn();
  recoverMediaError = vi.fn();
  liveSyncPosition = null;
  constructor(public config: Record<string, unknown>) {
    instances.push(this);
  }
  on() {}
}
const loadHls = () => Promise.resolve(FakeHls as unknown as typeof HlsType);
const flush = async () => {
  for (let i = 0; i < 10; i++) await Promise.resolve();
};

function video(native = false) {
  const v = document.createElement("video");
  v.canPlayType = (() => (native ? "maybe" : "")) as HTMLVideoElement["canPlayType"];
  v.play = vi.fn(() => Promise.resolve());
  v.pause = vi.fn();
  v.load = vi.fn();
  return v;
}

describe("attachLive with a token rewrite", () => {
  it("rewrites every playlist and segment request, without reloading the media", async () => {
    let token = "T1";
    const v = video();
    const emptied = vi.fn();
    v.addEventListener("emptied", emptied);
    const h = attachLive(v, "https://customer-a.cloudflarestream.com/T1/manifest/video.m3u8", {
      loadHls,
      rewrite: (u) => u.replace(/(cloudflarestream\.com\/)[^/]+/, `$1${token}`),
    });
    await flush();
    const inst = instances.at(-1)!;
    const Loader = inst.config.loader as new (c: unknown) => BaseLoader;
    expect(Loader).toBeTruthy();
    new Loader({}).load({ url: "https://customer-a.cloudflarestream.com/T1/seg_1.ts" });
    token = "T2"; // a heartbeat brought a new token
    new Loader({}).load({ url: "https://customer-a.cloudflarestream.com/T1/seg_2.ts" });
    expect(loads.slice(-2)).toEqual([
      "https://customer-a.cloudflarestream.com/T1/seg_1.ts",
      "https://customer-a.cloudflarestream.com/T2/seg_2.ts",
    ]);
    expect(v.load).not.toHaveBeenCalled();
    expect(emptied).not.toHaveBeenCalled();
    h.detach();
  });
});

describe("VideoPipeline", () => {
  it("loads the first channel on the visible element, the next on the hidden one", async () => {
    const a = video(true);
    const b = video(true);
    const active: number[] = [];
    const p = new VideoPipeline([a, b], { onActive: (i) => active.push(i) });
    const ac = new AbortController();
    const first = p.prepare({ url: "/one.m3u8", release() {} }, ac.signal);
    expect(a.getAttribute("src")).toBe("/one.m3u8");
    a.dispatchEvent(new Event("playing"));
    await first;
    p.promote();
    expect(p.activeIndex).toBe(0);
    const second = p.prepare({ url: "/two.m3u8", release() {} }, ac.signal);
    expect(b.getAttribute("src")).toBe("/two.m3u8");
    expect(b.muted).toBe(true); // unheard while it loads
    expect(a.getAttribute("src")).toBe("/one.m3u8"); // the old channel keeps playing
    b.dispatchEvent(new Event("playing"));
    await second;
    p.promote();
    expect(p.activeIndex).toBe(1);
    expect(active).toEqual([0, 1]);
    expect(a.getAttribute("src")).toBeNull(); // stopped after the swap
    expect(b.muted).toBe(false);
  });

  it("a mute the browser forced is not carried to the next channel", async () => {
    const a = video(true);
    const b = video(true);
    // the browser refuses sound on the first channel
    a.play = vi.fn(() => (a.muted ? Promise.resolve() : Promise.reject(new Error("NotAllowed"))));
    const policy: boolean[] = [];
    const p = new VideoPipeline([a, b], { onMutedByPolicy: (m) => policy.push(m) });
    const ac = new AbortController();
    const first = p.prepare({ url: "/one.m3u8", release() {} }, ac.signal);
    a.dispatchEvent(new Event("playing"));
    await first;
    p.promote();
    await flush();
    expect(a.muted).toBe(true);
    expect(policy).toEqual([true]);
    // the next channel asks for sound again, and gets it
    const second = p.prepare({ url: "/two.m3u8", release() {} }, ac.signal);
    b.dispatchEvent(new Event("playing"));
    await second;
    p.promote();
    await flush();
    expect(b.muted).toBe(false);
    expect(policy).toEqual([true, false]);
  });

  it("keeps a mute the viewer chose on the next channel", async () => {
    const a = video(true);
    const b = video(true);
    const p = new VideoPipeline([a, b]);
    const ac = new AbortController();
    const first = p.prepare({ url: "/one.m3u8", release() {} }, ac.signal);
    a.dispatchEvent(new Event("playing"));
    await first;
    p.promote();
    a.muted = true; // the viewer muted
    const second = p.prepare({ url: "/two.m3u8", release() {} }, ac.signal);
    b.dispatchEvent(new Event("playing"));
    await second;
    p.promote();
    expect(b.muted).toBe(true);
  });

  it("drops a hidden load that is aborted or fails", async () => {
    const a = video(true);
    const b = video(true);
    const p = new VideoPipeline([a, b]);
    const ac = new AbortController();
    const first = p.prepare({ url: "/one.m3u8", release() {} }, ac.signal);
    a.dispatchEvent(new Event("playing"));
    await first;
    p.promote();
    const ac2 = new AbortController();
    const pending = p.prepare({ url: "/two.m3u8", release() {} }, ac2.signal);
    ac2.abort();
    await expect(pending).rejects.toMatchObject({ code: "aborted" });
    expect(b.getAttribute("src")).toBeNull();
    expect(a.getAttribute("src")).toBe("/one.m3u8");
    const failing = p.prepare({ url: "/three.m3u8", release() {} }, new AbortController().signal);
    b.dispatchEvent(new Event("error"));
    await expect(failing).rejects.toMatchObject({ code: "unavailable" });
    expect(a.getAttribute("src")).toBe("/one.m3u8");
  });
});
