// Story 58.11 T01 — LG webOS with a fake <video> and a fake `webOS` / PalmServiceBridge:
// the device caps against 58.02's vectors, luna, the player's call sequence (play, start,
// tracks, subtitles drawn by Maktaba, the rate held at 1), Back and leaving the app.
import vectors from "../../../../../shared/playback/caps-vectors.json";
import { commonKeyAction } from "./types";
import {
  DV_GOOD_YEARS,
  PROBE_TYPES,
  TYPES,
  nativeHls,
  probeTypes,
  webosCaps,
  yearFromSdk,
  type WebOSAnswers,
} from "./webos-caps";
import { LUNA_TIMEOUT_MS, luna, readDeviceInfo } from "./webos-luna";
import { WebOSPlayer } from "./webos-player";
import { WEBOS_KEYS, webosPlatform } from "./webos";
import { clearLog, logText } from "../log";

type Vector = { id: string; profile: string; answers: unknown; device_caps: unknown };
const WEBOS = (vectors.vectors as Vector[]).filter((v) => v.profile === "webos");

describe("webOS device caps (58.02 vectors)", () => {
  it("has a webOS vector to test against", () => {
    expect(WEBOS.length).toBeGreaterThan(0);
  });
  for (const v of WEBOS) {
    it(`builds ${v.id}'s report from its answers`, () => {
      const a = v.answers as WebOSAnswers;
      // the vector asks exactly the codec strings the reader asks
      expect(Object.keys(a.is_type_supported).sort()).toEqual([...PROBE_TYPES].sort());
      expect(webosCaps(a)).toEqual(v.device_caps);
    });
  }

  const c2 = WEBOS[0]!.answers as WebOSAnswers;
  it("reports Dolby Vision only for a year the device checklist marked good", () => {
    expect(DV_GOOD_YEARS).toContain(2022);
    expect(webosCaps(c2, []).hdr).toEqual(["hdr10", "hlg"]);
    const noProbe = { ...c2, is_type_supported: { ...c2.is_type_supported, [TYPES.dvP8]: false } };
    expect(webosCaps(noProbe).hdr).not.toContain("dv-p8");
    const noDv = { ...c2, device_info: { ...c2.device_info, dolbyVision: false } };
    expect(webosCaps(noDv).hdr).not.toContain("dv-p8");
  });
  it("reports a 1080p TV with no HDR, HEVC or Dolby audio", () => {
    const a: WebOSAnswers = {
      device_info: { modelName: "32LM630", sdkVersion: "5.0.0", uhd: false },
      is_type_supported: { [TYPES.h264]: true, [TYPES.aac]: true },
      native_hls: nativeHls("5.0.0"),
    };
    expect(webosCaps(a)).toEqual({
      containers: ["mp4", "mkv", "ts"],
      video: [{ codec: "h264", max_height: 1080, max_fps: 60 }],
      hdr: [],
      audio: ["aac"],
      passthrough: [],
      max_audio_channels: 2,
      hls_segments: ["ts", "fmp4"],
      dash: false,
    });
  });
  it("names the model year and the native HLS player from the SDK version", () => {
    expect(yearFromSdk("5.0.0")).toBe(2020);
    expect(yearFromSdk("6.3.1")).toBe(2021);
    expect(yearFromSdk("7.1.0")).toBe(2022);
    expect(yearFromSdk("10.0.0")).toBe(2025);
    expect(yearFromSdk(undefined)).toBe(0);
    expect(nativeHls("4.4.0")).toEqual({ version: 3, fmp4: false });
    expect(nativeHls("6.0.0")).toEqual({ version: 7, fmp4: true });
  });
  it("asks MediaSource, else the <video>", () => {
    const asked: string[] = [];
    const w = {
      MediaSource: { isTypeSupported: (t: string) => (asked.push(t), t.includes("avc1")) },
      document,
    } as unknown as Window;
    const r = probeTypes(w);
    expect(asked).toEqual([...PROBE_TYPES]);
    expect(r[TYPES.h264]).toBe(true);
    expect(r[TYPES.hevc]).toBe(false);
    const noMs = probeTypes({ document } as unknown as Window);
    expect(Object.keys(noMs)).toHaveLength(PROBE_TYPES.length);
  });
});

/** A PalmServiceBridge that answers each luna URI from a table. */
function palmWindow(answers: Record<string, unknown>, extra: Record<string, unknown> = {}) {
  const calls: string[] = [];
  class Bridge {
    onservicecallback: ((m: string) => void) | null = null;
    call(uri: string, params: string) {
      calls.push(`${uri} ${params}`);
      const a = answers[uri];
      setTimeout(() =>
        this.onservicecallback?.(
          a === undefined
            ? JSON.stringify({ returnValue: false, errorText: "no" })
            : JSON.stringify(a)
        )
      );
    }
  }
  const w = {
    PalmSystem: {},
    PalmServiceBridge: Bridge,
    navigator: { userAgent: "Mozilla/5.0 (Web0S; Linux/SmartTV) Chrome/87.0.4280.88" },
    document,
    fetch: async () => new Response(""),
    close: vi.fn(),
    ...extra,
  } as unknown as Window;
  return { w, calls };
}

describe("luna", () => {
  beforeEach(() => clearLog());
  it("speaks to PalmServiceBridge when webOSTV.js is absent", async () => {
    const { w, calls } = palmWindow({
      "luna://com.webos.service.connectionmanager/getStatus": {
        returnValue: true,
        wired: { state: "disconnected" },
        wifi: { state: "connected", ipAddress: "192.168.1.40" },
      },
    });
    const p = webosPlatform(w);
    expect(await p.localIp()).toBe("192.168.1.40");
    expect(calls[0]).toBe("luna://com.webos.service.connectionmanager/getStatus {}");
  });
  it("reads the device from the system properties and config", async () => {
    const { w } = palmWindow({
      "luna://com.webos.service.tv.systemproperty/getSystemInfo": {
        returnValue: true,
        modelName: "OLED65G1",
        sdkVersion: "6.3.0",
        UHD: "true",
      },
      "luna://com.webos.service.config/getConfigs": {
        returnValue: true,
        configs: { "tv.model.supportHDR": true, "tv.config.supportDolbyHDRContents": true },
      },
    });
    expect(await readDeviceInfo(w)).toEqual({
      modelName: "OLED65G1",
      sdkVersion: "6.3.0",
      uhd: true,
      hdr10: true,
      dolbyVision: true,
    });
    const p = webosPlatform(w);
    expect(await p.device()).toEqual({ model: "OLED65G1", year: 2021, engine: "Chromium 87" });
    // 2021 is not a year the checklist marked good for Dolby Vision
    expect(p.deviceCaps().hdr).toEqual(["hdr10", "hlg"]);
  });
  it("fails closed: a refused call or no answer is null, logged", async () => {
    vi.useFakeTimers();
    try {
      const { w } = palmWindow({});
      const refused = luna(w, "luna://x", "y");
      await vi.runAllTimersAsync();
      expect(await refused).toBeNull();
      const silent = luna(
        { webOS: { service: { request: () => undefined } } } as unknown as Window,
        "luna://s",
        "m"
      );
      await vi.advanceTimersByTimeAsync(LUNA_TIMEOUT_MS + 1);
      expect(await silent).toBeNull();
      expect(logText()).toContain("luna://s/m failed: timeout");
    } finally {
      vi.useRealTimers();
    }
  });
  it("describes an unknown TV as an LG TV that plays H.264", async () => {
    const { w } = palmWindow({});
    const p = webosPlatform(w);
    const d = await p.device();
    expect(d.model).toBe("LG TV");
    expect(d.year).toBe(2022); // from the engine (Chromium 87)
    expect(p.deviceCaps().video[0]!.codec).toBe("h264");
  });
});

describe("keys and leaving", () => {
  const key = (keyCode: number, k = "") => new KeyboardEvent("keydown", { keyCode, key: k });
  it("maps Back (461) and the media and red keys", () => {
    expect(commonKeyAction(key(461))).toBe("back");
    expect(commonKeyAction(key(415))).toBe("play");
    expect(commonKeyAction(key(19))).toBe("pause");
    expect(commonKeyAction(key(413))).toBe("pause");
    expect(commonKeyAction(key(417))).toBe("seekForward");
    expect(commonKeyAction(key(412))).toBe("seekBack");
    expect(commonKeyAction(key(403))).toBe("menu");
    const { w } = palmWindow({});
    expect(webosPlatform(w).registerKeys()).toEqual(WEBOS_KEYS);
  });
  it("leaves by webOS.platformBack, else PalmSystem's, else closing", () => {
    const back = vi.fn();
    webosPlatform(palmWindow({}, { webOS: { platformBack: back } }).w).exit();
    expect(back).toHaveBeenCalledOnce();
    const palmBack = vi.fn();
    webosPlatform(palmWindow({}, { PalmSystem: { platformBack: palmBack } }).w).exit();
    expect(palmBack).toHaveBeenCalledOnce();
    const { w } = palmWindow({});
    webosPlatform(w).exit();
    expect(w.close).toHaveBeenCalledOnce();
  });
});

/** A <video> whose media calls are recorded (jsdom plays nothing). */
function fakeVideo() {
  const stage = document.createElement("div");
  document.body.replaceChildren(stage);
  const v = document.createElement("video");
  stage.appendChild(v);
  const calls: string[] = [];
  let ready = 0;
  let time = 0;
  const audio = [{ enabled: true }, { enabled: false }];
  const textTracks: (TextTrack & { _fire(): void })[] = [];
  Object.defineProperties(v, {
    play: { value: async () => void calls.push("play") },
    pause: { value: () => void calls.push("pause") },
    load: { value: () => void calls.push("load") },
    readyState: { get: () => ready },
    duration: { get: () => 600 },
    currentTime: {
      get: () => time,
      set: (t: number) => {
        calls.push(`currentTime=${t}`);
        time = t;
      },
    },
    audioTracks: { value: audio },
    textTracks: { value: Object.assign(textTracks, { addEventListener: () => undefined }) },
  });
  const meta = () => {
    ready = 1;
    v.dispatchEvent(new Event("loadedmetadata"));
  };
  const at = (t: number) => {
    time = t;
    v.dispatchEvent(new Event("timeupdate"));
  };
  const addTextTrack = (language: string, cueTexts: () => string[]) => {
    const listeners: (() => void)[] = [];
    const t = {
      id: "",
      label: language,
      language,
      mode: "showing" as TextTrackMode,
      get activeCues() {
        return cueTexts().map((text) => ({ text }));
      },
      addEventListener: (_: string, f: () => void) => listeners.push(f),
      removeEventListener: () => undefined,
      _fire: () => listeners.forEach((f) => f()),
    } as unknown as TextTrack & { _fire(): void };
    textTracks.push(t);
    return t;
  };
  return { v, stage, calls, audio, meta, at, addTextTrack };
}

const VTT_AR = "WEBVTT\n\n00:00:01.000 --> 00:00:04.000\nمرحبًا بكم في مكتبة\n";

describe("WebOSPlayer", () => {
  beforeEach(() => clearLog());

  it("sets the signed URL on the <video>, then the start once the metadata is in", async () => {
    const f = fakeVideo();
    const p = new WebOSPlayer(f.v, async () => new Response(""));
    await p.play({ url: "https://h/stream/s/master.m3u8?sig=abc", start_sec: 125, audio: 1 });
    expect(f.v.getAttribute("src")).toBe("https://h/stream/s/master.m3u8?sig=abc");
    expect(f.calls).toEqual(["play"]); // no currentTime before the metadata
    f.meta();
    expect(f.calls).toEqual(["play", "currentTime=125"]);
    expect(f.audio.map((a) => a.enabled)).toEqual([false, true]);
    expect(p.active()).toBe(true);
    expect(p.state()).toMatchObject({ position: 125, duration: 600, ended: false, error: null });
    // the signature never reaches the log
    expect(logText()).not.toContain("abc");
  });

  it("refuses anything but http(s) media", async () => {
    const f = fakeVideo();
    const p = new WebOSPlayer(f.v, async () => new Response(""));
    await expect(p.play({ url: "javascript:alert(1)" })).rejects.toThrow();
    expect(f.v.getAttribute("src")).toBeNull();
  });

  it("holds the rate at 1.0 (webOS plays no other speed)", async () => {
    const f = fakeVideo();
    const p = new WebOSPlayer(f.v, async () => new Response(""));
    await p.play({ url: "https://h/a.mp4?sig=x" });
    f.v.playbackRate = 2;
    f.v.dispatchEvent(new Event("ratechange"));
    expect(f.v.playbackRate).toBe(1);
  });

  it("pauses, resumes, seeks and stops", async () => {
    const f = fakeVideo();
    const p = new WebOSPlayer(f.v, async () => new Response(""));
    await p.play({ url: "https://h/a.mkv?sig=x" });
    f.meta();
    p.pause();
    p.resume();
    p.seek(42);
    p.seek(-1);
    p.stop();
    expect(f.calls).toEqual(["play", "pause", "play", "currentTime=42", "pause", "load"]);
    expect(f.v.getAttribute("src")).toBeNull();
    expect(p.active()).toBe(false);
  });

  it("draws a sidecar file's Arabic cues right to left, at the person's size", async () => {
    const f = fakeVideo();
    const fetched: string[] = [];
    const p = new WebOSPlayer(f.v, async (u) => (fetched.push(u), new Response(VTT_AR)));
    await p.play({
      url: "https://h/a.m3u8?sig=x",
      subtitles: [
        { id: "s-ar", url: "https://h/stream/s/ar.vtt?sig=y", lang: "ar", label: "العربية" },
      ],
      subtitle_scale: 1.5,
    });
    p.subtitle("s-ar");
    await new Promise((r) => setTimeout(r));
    f.at(2);
    const cue = f.stage.querySelector(".ml-cue") as HTMLElement;
    expect(fetched).toEqual(["https://h/stream/s/ar.vtt?sig=y"]);
    expect(cue.dir).toBe("rtl");
    expect(cue.textContent).toBe("مرحبًا بكم في مكتبة");
    expect(p.layer.element.style.fontSize).toBe("72px");
    p.subtitleScale(2);
    expect(p.layer.element.style.fontSize).toBe("96px");
    f.at(5);
    expect(p.layer.text()).toBe("");
    p.subtitle(null);
    f.at(2);
    expect(p.layer.text()).toBe("");
  });

  it("draws a manifest rendition's cues itself, the TV's own renderer hidden", async () => {
    const f = fakeVideo();
    let active: string[] = [];
    const track = f.addTextTrack("ar", () => active);
    const p = new WebOSPlayer(f.v, async () => new Response(""));
    await p.play({ url: "https://h/a.m3u8?sig=x" });
    f.meta();
    expect(track.mode).toBe("disabled");
    p.subtitle("ar");
    expect(track.mode).toBe("hidden");
    active = ["<i>سلام</i>"];
    track._fire();
    const cue = f.stage.querySelector(".ml-cue") as HTMLElement;
    expect(cue.dir).toBe("rtl");
    expect(cue.textContent).toBe("سلام");
    p.subtitle(null);
    expect(track.mode).toBe("disabled");
    expect(p.layer.text()).toBe("");
  });

  it("says a media error in its state", async () => {
    const f = fakeVideo();
    const p = new WebOSPlayer(f.v, async () => new Response(""));
    await p.play({ url: "https://h/a.m3u8?sig=x" });
    Object.defineProperty(f.v, "error", { value: { code: 4 } });
    f.v.dispatchEvent(new Event("error"));
    expect(p.state().error).toBe("webos media error 4");
  });
});
