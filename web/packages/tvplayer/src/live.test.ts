// Which player plays a live channel: Safari's own HLS, or hls.js everywhere
// else. Chrome 142+ answers canPlayType "maybe" but cannot parse our MPEG-TS
// live segments, so it must get hls.js.
import { afterEach, describe, expect, it, vi } from "vitest";
import { hasNativeHls } from "./live";

function video(answer: string) {
  const v = document.createElement("video");
  v.canPlayType = (() => answer) as HTMLVideoElement["canPlayType"];
  return v;
}

function browser(vendor: string, mse: boolean) {
  vi.spyOn(navigator, "vendor", "get").mockReturnValue(vendor);
  vi.stubGlobal("MediaSource", mse ? function MediaSource() {} : undefined);
}

describe("hasNativeHls", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("Chrome that says maybe still gets hls.js", () => {
    browser("Google Inc.", true);
    expect(hasNativeHls(video("maybe"))).toBe(false);
  });

  it("Safari plays it natively", () => {
    browser("Apple Computer, Inc.", true);
    expect(hasNativeHls(video("maybe"))).toBe(true);
  });

  it("a browser without Media Source Extensions plays it natively", () => {
    browser("Google Inc.", false);
    expect(hasNativeHls(video("probably"))).toBe(true);
  });

  it("a browser that cannot play HLS at all never goes native", () => {
    browser("Apple Computer, Inc.", false);
    expect(hasNativeHls(video(""))).toBe(false);
  });
});
