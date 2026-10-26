// attachVod helpers: sig carrying, HTTP status extraction, and the native
// path's expiry handling (no HTTP status there).
import { describe, it, expect, vi } from "vitest";
import {
  VOD_STALL_MS,
  attachVod,
  carrySig,
  hlsErrorOf,
  httpStatusOf,
  sigOf,
  type VodError,
} from "./vod";

const M = "/stream/s1/manifest.m3u8?sig=a.b%2Bc";

describe("carrySig", () => {
  it("reads the manifest sig", () => {
    expect(sigOf(M)).toBe("a.b+c");
    expect(sigOf("/stream/s1/manifest.m3u8")).toBe("");
  });
  it("adds the sig to a bare same-session URL, keeping query and fragment", () => {
    expect(carrySig("/stream/s1/720p/seg.ts", M)).toBe("/stream/s1/720p/seg.ts?sig=a.b%2Bc");
    expect(carrySig("/stream/s1/subs/en.vtt?x=1#t", M)).toBe(
      "/stream/s1/subs/en.vtt?x=1&sig=a.b%2Bc#t"
    );
  });
  it("leaves signed, foreign-origin and other-session URLs untouched", () => {
    expect(carrySig("/stream/s1/seg.ts?sig=own", M)).toBe("/stream/s1/seg.ts?sig=own");
    expect(carrySig("https://cdn.example.com/stream/s1/seg.ts", M)).toBe(
      "https://cdn.example.com/stream/s1/seg.ts"
    );
    expect(carrySig("/stream/s2/seg.ts", M)).toBe("/stream/s2/seg.ts");
    expect(carrySig("/stream/s1/seg.ts", "/stream/s1/manifest.m3u8")).toBe("/stream/s1/seg.ts");
  });
});

describe("httpStatusOf", () => {
  it("prefers the loader response code, then the XHR status", () => {
    expect(httpStatusOf({ response: { code: 403 } })).toBe(403);
    expect(httpStatusOf({ networkDetails: { status: 401 } })).toBe(401);
    expect(httpStatusOf({})).toBe(0);
  });
});

describe("attachVod native path", () => {
  function nativeVideo() {
    const v = document.createElement("video");
    v.canPlayType = vi.fn(() => "maybe") as HTMLVideoElement["canPlayType"];
    return v;
  }
  it("sets the signed URL on the element and never loads hls.js", () => {
    const v = nativeVideo();
    const loadHls = vi.fn();
    const h = attachVod(v, M, { loadHls });
    expect(h.mode).toBe("native");
    expect(v.getAttribute("src")).toBe(M);
    expect(loadHls).not.toHaveBeenCalled();
    h.detach();
  });
  it("treats a media error past expiry as an auth error without probing", () => {
    const v = nativeVideo();
    const onAuthError = vi.fn();
    const probe = vi.fn();
    const h = attachVod(v, M, { onAuthError, isExpired: () => true, probe });
    v.dispatchEvent(new Event("error"));
    v.dispatchEvent(new Event("error"));
    expect(onAuthError).toHaveBeenCalledTimes(1);
    expect(probe).not.toHaveBeenCalled();
    h.detach();
  });
  it.each([
    [403, "auth"],
    [401, "auth"],
    [500, "fatal"],
  ])("probes the manifest on a media error: %i is %s", async (status, kind) => {
    const v = nativeVideo();
    const onAuthError = vi.fn();
    const onFatal = vi.fn();
    const probe = vi.fn(() => Promise.resolve(status));
    const h = attachVod(v, M, { onAuthError, onFatal, isExpired: () => false, probe });
    v.dispatchEvent(new Event("error"));
    await vi.waitFor(() =>
      expect(onAuthError.mock.calls.length + onFatal.mock.calls.length).toBe(1)
    );
    expect(probe).toHaveBeenCalledWith(M);
    expect(kind === "auth" ? onAuthError : onFatal).toHaveBeenCalledTimes(1);
    h.detach();
  });
  it.each([404, 410])(
    "probes the manifest on a media error: %i is a stream gone",
    async (status) => {
      const v = nativeVideo();
      Object.defineProperty(v, "currentTime", { value: 1234, configurable: true, writable: true });
      const onGone = vi.fn();
      const onFatal = vi.fn();
      const probe = vi.fn(() => Promise.resolve(status));
      const h = attachVod(v, M, { onGone, onFatal, isExpired: () => false, probe });
      v.dispatchEvent(new Event("error"));
      v.dispatchEvent(new Event("error"));
      await vi.waitFor(() => expect(onGone).toHaveBeenCalledTimes(1));
      expect(onGone).toHaveBeenCalledWith(1234, false);
      expect(onFatal).not.toHaveBeenCalled();
      h.detach();
    }
  );
  it("without onGone, a stream gone is fatal", async () => {
    const v = nativeVideo();
    const onFatal = vi.fn();
    const h = attachVod(v, M, {
      onFatal,
      isExpired: () => false,
      probe: () => Promise.resolve(410),
    });
    v.dispatchEvent(new Event("error"));
    await vi.waitFor(() => expect(onFatal).toHaveBeenCalledTimes(1));
    h.detach();
  });
  it("probes when native playback stalls and refreshes on 403", async () => {
    vi.useFakeTimers();
    try {
      const v = nativeVideo();
      Object.defineProperty(v, "paused", { value: false, configurable: true });
      Object.defineProperty(v, "currentTime", { value: 12, configurable: true, writable: true });
      const onAuthError = vi.fn();
      const probe = vi.fn().mockResolvedValueOnce(200).mockResolvedValueOnce(403);
      const h = attachVod(v, M, { onAuthError, probe });
      v.dispatchEvent(new Event("playing"));
      await vi.advanceTimersByTimeAsync(VOD_STALL_MS - 1);
      expect(probe).not.toHaveBeenCalled();
      // progress re-arms the watchdog
      v.currentTime = 13;
      v.dispatchEvent(new Event("timeupdate"));
      await vi.advanceTimersByTimeAsync(VOD_STALL_MS - 1);
      expect(probe).not.toHaveBeenCalled();
      await vi.advanceTimersByTimeAsync(1);
      expect(probe).toHaveBeenCalledTimes(1);
      expect(onAuthError).not.toHaveBeenCalled();
      // 200: keep watching; the next stall finds the token revoked
      await vi.advanceTimersByTimeAsync(VOD_STALL_MS);
      expect(probe).toHaveBeenCalledTimes(2);
      expect(onAuthError).toHaveBeenCalledWith(13, true);
      h.detach();
    } finally {
      vi.useRealTimers();
    }
  });
  it("ignores media errors after detach", () => {
    const v = nativeVideo();
    const probe = vi.fn(() => Promise.resolve(403));
    const h = attachVod(v, M, { probe });
    h.detach();
    v.dispatchEvent(new Event("error"));
    expect(probe).not.toHaveBeenCalled();
  });
  it("falls back to the element when MSE is unavailable", async () => {
    const v = document.createElement("video");
    v.canPlayType = vi.fn(() => "") as HTMLVideoElement["canPlayType"];
    const Fake = { isSupported: () => false };
    const h = attachVod(v, M, {
      loadHls: () => Promise.resolve(Fake as never),
    });
    await Promise.resolve();
    await Promise.resolve();
    expect(h.mode).toBe("unsupported");
    expect(v.getAttribute("src")).toBe(M);
    h.detach();
  });
});

describe("attachVod onError", () => {
  function nativeVideo() {
    const v = document.createElement("video");
    v.canPlayType = vi.fn(() => "maybe") as HTMLVideoElement["canPlayType"];
    return v;
  }
  it("describes a native media error that is fatal", async () => {
    const v = nativeVideo();
    Object.defineProperty(v, "error", {
      value: { code: 3, message: "PIPELINE_ERROR_DECODE" },
      configurable: true,
    });
    const onError = vi.fn<(e: VodError) => void>();
    const onFatal = vi.fn();
    const h = attachVod(v, M, {
      onError,
      onFatal,
      isExpired: () => false,
      probe: () => Promise.resolve(200),
    });
    v.dispatchEvent(new Event("error"));
    await vi.waitFor(() => expect(onFatal).toHaveBeenCalledTimes(1));
    expect(onError).toHaveBeenCalledWith({
      engine: "native",
      outcome: "fatal",
      code: "media_err_3",
      message: "PIPELINE_ERROR_DECODE",
      httpStatus: 200,
      url: M,
    });
    h.detach();
  });
  it("describes a native stream gone before the reopen", async () => {
    const v = nativeVideo();
    const onError = vi.fn<(e: VodError) => void>();
    const onGone = vi.fn();
    const h = attachVod(v, M, {
      onError,
      onGone,
      isExpired: () => false,
      probe: () => Promise.resolve(404),
    });
    v.dispatchEvent(new Event("error"));
    await vi.waitFor(() => expect(onGone).toHaveBeenCalledTimes(1));
    expect(onError).toHaveBeenCalledWith(
      expect.objectContaining({ outcome: "reopen", code: "manifest_gone", httpStatus: 404 })
    );
    h.detach();
  });
  it("does not describe an expired signature (the owner just refreshes it)", () => {
    const v = nativeVideo();
    const onError = vi.fn();
    const h = attachVod(v, M, { onError, onAuthError: vi.fn(), isExpired: () => true });
    v.dispatchEvent(new Event("error"));
    expect(onError).not.toHaveBeenCalled();
    h.detach();
  });

  // A stand-in for hls.js: records its error handler so a test can fire it.
  function fakeHls() {
    let onErr: ((evt: string, data: unknown) => void) | null = null;
    class FakeHls {
      static Events = { ERROR: "hlsError" };
      static ErrorTypes = { NETWORK_ERROR: "networkError", MEDIA_ERROR: "mediaError" };
      static isSupported = () => true;
      on(_e: string, fn: (evt: string, data: unknown) => void) {
        onErr = fn;
      }
      attachMedia() {}
      loadSource() {}
      startLoad() {}
      recoverMediaError() {}
      destroy() {}
    }
    return { Hls: FakeHls, fire: (data: unknown) => onErr?.("hlsError", data) };
  }
  async function attachHls(opts: Parameters<typeof attachVod>[2]) {
    const v = document.createElement("video");
    v.canPlayType = vi.fn(() => "") as HTMLVideoElement["canPlayType"];
    const fake = fakeHls();
    const h = attachVod(v, M, { ...opts, loadHls: () => Promise.resolve(fake.Hls as never) });
    await vi.waitFor(() => expect(h.mode).toBe("hls"));
    return { h, fire: fake.fire };
  }
  it("describes a fatal hls.js media error with its details", async () => {
    const onError = vi.fn<(e: VodError) => void>();
    const onFatal = vi.fn();
    const { h, fire } = await attachHls({ onError, onFatal });
    const err = {
      fatal: true,
      type: "mediaError",
      details: "bufferAppendError",
      error: { message: "decode" },
    };
    fire(err); // recovery 1
    fire(err); // recovery 2
    expect(onError).not.toHaveBeenCalled();
    fire(err);
    expect(onFatal).toHaveBeenCalledTimes(1);
    expect(onError).toHaveBeenCalledWith({
      engine: "hls.js",
      outcome: "fatal",
      code: "bufferAppendError",
      message: "decode",
      httpStatus: 0,
      url: undefined,
    });
    h.detach();
  });
  it("describes a segment 410 before the reopen", async () => {
    const onError = vi.fn<(e: VodError) => void>();
    const onGone = vi.fn();
    const { h, fire } = await attachHls({ onError, onGone });
    fire({
      fatal: false,
      type: "networkError",
      details: "fragLoadError",
      frag: { url: "/stream/s1/seg9.ts?sig=x" },
      response: { code: 410, text: "Gone" },
    });
    expect(onGone).toHaveBeenCalledTimes(1);
    expect(onError).toHaveBeenCalledWith(
      expect.objectContaining({
        engine: "hls.js",
        outcome: "reopen",
        code: "fragLoadError",
        httpStatus: 410,
        url: "/stream/s1/seg9.ts?sig=x",
      })
    );
    h.detach();
  });
  it("hlsErrorOf falls back to the error type", () => {
    expect(hlsErrorOf({ type: "otherError" })).toEqual({
      code: "otherError",
      message: "",
      httpStatus: 0,
      url: undefined,
    });
  });
});
