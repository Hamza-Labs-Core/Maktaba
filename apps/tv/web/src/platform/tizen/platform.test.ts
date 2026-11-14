// Story 58.10 — the Samsung platform: the keys it registers (media and color), Back (10009)
// as the bridge's back, leaving through tizen.application, the caps from webapis, and the
// player: AVPlay in the stage when webapis.avplay exists, the <video> otherwise.
import { TIZEN_KEYS, isTizen, tizenPlatform, NOTICE_MS } from "../tizen";
import { AvplayPlayer } from "./avplay";
import { VideoElementPlayer } from "../../player";
import { setLang } from "../../i18n";

function stub(over: Record<string, unknown> = {}) {
  const registered: string[][] = [];
  let exited = 0;
  const w = {
    document,
    navigator: {
      userAgent:
        "Mozilla/5.0 (SMART-TV; LINUX; Tizen 6.5) AppleWebKit/537.36 (KHTML, like Gecko) 85.0.4183.93/6.5 TV Safari/537.36",
    },
    MediaSource: { isTypeSupported: (t: string) => t.indexOf("avc1") >= 0 || t.indexOf("hvc1") >= 0 },
    fetch: async () => new Response("WEBVTT"),
    tizen: {
      tvinputdevice: { registerKeyBatch: (k: string[]) => registered.push(k) },
      application: { getCurrentApplication: () => ({ exit: () => exited++ }) },
      tvaudiocontrol: { getOutputMode: () => "DOLBY" },
    },
    webapis: {
      productinfo: { getRealModel: () => "QN90B", isUdPanelSupported: () => true },
      avinfo: { isHdrTvSupport: () => true },
      ...over,
    },
  };
  return { w: w as unknown as Window, registered, exited: () => exited };
}

function key(keyCode: number, k = ""): KeyboardEvent {
  return new KeyboardEvent("keydown", { keyCode, key: k } as KeyboardEventInit);
}

describe("the Samsung platform (58.10)", () => {
  beforeEach(() => {
    document.body.innerHTML = '<div id="stage"></div>';
  });

  it("is Tizen only with both tizen and webapis", () => {
    expect(isTizen(stub().w)).toBe(true);
    expect(isTizen({ tizen: {} } as unknown as Window)).toBe(false);
  });

  it("registers the media and color keys at start", () => {
    const s = stub();
    expect(tizenPlatform(s.w).registerKeys()).toEqual(TIZEN_KEYS);
    expect(s.registered).toEqual([TIZEN_KEYS]);
    for (const k of [
      "MediaPlay",
      "MediaPause",
      "MediaPlayPause",
      "MediaFastForward",
      "MediaRewind",
      "MediaStop",
      "ColorF0Red",
      "ColorF1Green",
      "ColorF2Yellow",
      "ColorF3Blue",
    ])
      expect(TIZEN_KEYS).toContain(k);
  });

  it("maps Back (10009), red and Play/Pause to the bridge's actions", () => {
    const p = tizenPlatform(stub().w);
    expect(p.keyAction(key(10009, "XF86Back"))).toBe("back");
    expect(p.keyAction(key(403, "ColorF0Red"))).toBe("menu");
    expect(p.keyAction(key(10252, "MediaPlayPause"))).toBe("playPause");
    expect(p.keyAction(key(417))).toBe("seekForward");
    expect(p.keyAction(key(412))).toBe("seekBack");
  });

  it("leaves through tizen.application", () => {
    const s = stub();
    tizenPlatform(s.w).exit();
    expect(s.exited()).toBe(1);
  });

  it("reports caps from webapis: HDR10 and HLG, AC3 passthrough behind a Dolby Digital soundbar", () => {
    const caps = tizenPlatform(stub().w).deviceCaps();
    expect(caps.video.map((v) => [v.codec, v.max_height])).toEqual([
      ["h264", 2160],
      ["hevc", 2160],
    ]);
    expect(caps.hdr).toEqual(["hdr10", "hlg"]);
    expect(caps.passthrough).toEqual(["ac3"]);
    expect(caps.hls_segments).toEqual(["ts", "fmp4"]);
  });

  it("plays with AVPlay in the stage, under the subtitles and the notice", async () => {
    let listener: { onerror?: (t: string) => void } = {};
    const avplay = {
      open: () => {},
      close: () => {},
      setListener: (l: typeof listener) => (listener = l),
      setDisplayRect: () => {},
      setDisplayMethod: () => {},
      setStreamingProperty: () => {},
      prepareAsync: (ok: () => void) => ok(),
      play: () => {},
      pause: () => {},
      stop: () => {},
      seekTo: () => {},
      getState: () => "PLAYING",
      getDuration: () => 1000,
      getCurrentTime: () => 0,
      getTotalTrackInfo: () => [],
      setSelectTrack: () => {},
    };
    const stage = document.getElementById("stage")!;
    const video = document.createElement("video");
    stage.appendChild(video);
    const player = tizenPlatform(stub({ avplay }).w).player!(video);
    expect(player).toBeInstanceOf(AvplayPlayer);
    const plane = stage.querySelector('object[type="application/avplayer"]');
    expect(plane).not.toBeNull();
    expect(stage.firstElementChild).toBe(plane);
    expect(stage.querySelector('[data-testid="subtitles"]')).not.toBeNull();
    vi.useFakeTimers();
    try {
      setLang("ar");
      await player.play({ url: "https://h/a.m3u8" });
      listener.onerror!("PLAYER_ERROR_NOT_SUPPORTED_FILE");
      const notice = stage.querySelector<HTMLElement>('[data-testid="player-notice"]')!;
      expect(notice.hidden).toBe(false);
      expect(notice.textContent).toBe("تعذّر على التلفاز تشغيل هذا. جارٍ تجربة نسخة محوّلة…");
      vi.advanceTimersByTime(NOTICE_MS);
      expect(notice.hidden).toBe(true);
    } finally {
      setLang("en");
      vi.useRealTimers();
    }
  });

  it("plays in the <video> where AVPlay is missing (the emulator)", () => {
    const video = document.createElement("video");
    document.getElementById("stage")!.appendChild(video);
    expect(tizenPlatform(stub().w).player!(video)).toBeInstanceOf(VideoElementPlayer);
  });
});
