// Story 58.10 T01 — the AVPlay adapter against a fake webapis.avplay: the call sequence for
// play, tracks, subtitles, seek, the app switcher and errors (as state.error).
import { AvplayPlayer, audioInfo, type Avplay, type AvplayListener, type SubtitleSink } from "./avplay";
import type { Cue } from "../../subtitles";
import { clearLog, logText } from "../../log";

function fakeAvplay() {
  const calls: string[] = [];
  let listener: AvplayListener = {};
  let state = "NONE";
  let prepare: { ok: () => void; fail: (e: unknown) => void } | null = null;
  const av: Avplay = {
    open: (url) => {
      calls.push(`open ${url}`);
      state = "IDLE";
    },
    close: () => {
      calls.push("close");
      state = "NONE";
    },
    setListener: (l) => {
      calls.push("setListener");
      listener = l;
    },
    setDisplayRect: (x, y, w, h) => calls.push(`setDisplayRect ${x},${y},${w},${h}`),
    setDisplayMethod: (m) => calls.push(`setDisplayMethod ${m}`),
    setStreamingProperty: (n, v) => calls.push(`setStreamingProperty ${n} ${v}`),
    prepareAsync: (ok, fail) => {
      calls.push("prepareAsync");
      prepare = { ok, fail };
    },
    play: () => {
      calls.push("play");
      state = "PLAYING";
    },
    pause: () => {
      calls.push("pause");
      state = "PAUSED";
    },
    stop: () => {
      calls.push("stop");
      state = "IDLE";
    },
    seekTo: (ms, ok) => {
      calls.push(`seekTo ${ms}`);
      ok?.();
    },
    getState: () => state,
    getDuration: () => 5_400_000,
    getCurrentTime: () => 0,
    getTotalTrackInfo: () => [
      { index: 0, type: "VIDEO" },
      { index: 1, type: "AUDIO", extra_info: '{"language":"eng","channels":"2","fourCC":"AAC"}' },
      { index: 2, type: "AUDIO", extra_info: '{"language":"ara","channels":"6","fourCC":"EAC3"}' },
      { index: 3, type: "TEXT", extra_info: '{"language":"ar"}' },
    ],
    setSelectTrack: (t, i) => calls.push(`setSelectTrack ${t} ${i}`),
    setSilentSubtitle: (s) => calls.push(`setSilentSubtitle ${s}`),
    suspend: () => calls.push("suspend"),
    restore: () => calls.push("restore"),
  };
  return {
    av,
    calls,
    listener: () => listener,
    prepared: () => {
      state = "READY";
      prepare!.ok();
    },
    prepareFails: (e: unknown) => prepare!.fail(e),
  };
}

function fakeSubs() {
  const calls: string[] = [];
  const sink: SubtitleSink = {
    setCues: (cues, lang) => calls.push(`setCues ${cues.length} ${lang ?? "-"}`),
    render: (t) => calls.push(`render ${t}`),
    showText: (text) => calls.push(`showText ${text}`),
    clear: () => calls.push("clear"),
    setScale: (n) => calls.push(`setScale ${n}`),
  };
  return { sink, calls };
}

const CUES: Cue[] = [{ start: 1, end: 3, text: "مرحبا" } as Cue];

function setup(o: { uhd?: boolean } = {}) {
  const f = fakeAvplay();
  const s = fakeSubs();
  const plane = document.createElement("object");
  plane.hidden = true;
  document.body.appendChild(plane);
  const notices: string[] = [];
  const fetched: string[] = [];
  const player = new AvplayPlayer({
    avplay: f.av,
    doc: document,
    plane,
    subtitles: s.sink,
    parseVtt: () => CUES,
    fetchText: async (url) => {
      fetched.push(url);
      return "WEBVTT";
    },
    uhd: o.uhd ?? false,
    notice: (k) => notices.push(k),
  });
  return { f, s, plane, player, notices, fetched };
}

const flush = () => new Promise((r) => setTimeout(r, 0));

describe("the AVPlay adapter (58.10 T01)", () => {
  beforeEach(() => {
    clearLog();
    document.body.className = "";
  });

  it("plays: open, listener, the whole screen letterboxed, prepare, seek to the start, play", async () => {
    const t = setup();
    const done = t.player.play({
      url: "https://home.example/s/1/master.m3u8?sig=abc",
      start_sec: 42.5,
    });
    expect(t.player.active()).toBe(true);
    expect(t.player.state().buffering).toBe(true);
    expect(t.plane.hidden).toBe(false);
    expect(document.body.classList.contains("ml-avplay")).toBe(true);
    t.f.prepared();
    await done;
    expect(t.f.calls).toEqual([
      "open https://home.example/s/1/master.m3u8?sig=abc",
      "setListener",
      "setDisplayRect 0,0,1920,1080",
      "setDisplayMethod PLAYER_DISPLAY_MODE_LETTER_BOX",
      "prepareAsync",
      "seekTo 42500",
      "setSilentSubtitle true",
      "play",
    ]);
    expect(t.player.state()).toEqual({
      position: 42.5,
      duration: 5400,
      buffering: false,
      ended: false,
      error: null,
      audio_tracks: [
        { enabled: true, lang: "eng", label: "AAC 2ch" },
        { enabled: false, lang: "ara", label: "EAC3 6ch" },
      ],
    });
    // the signed URL's sig never reaches the log
    expect(logText()).not.toContain("sig=abc");
  });

  it("turns on the 4K mode for a 2160p source, before prepare, and not for 1080p", async () => {
    const a = setup();
    const p = a.player.play({ url: "https://h/x.mkv?sig=1", height: 2160 });
    a.f.prepared();
    await p;
    const i4k = a.f.calls.indexOf(
      "setStreamingProperty ADAPTIVE_INFO FIXED_MAX_RESOLUTION=3840X2160"
    );
    expect(i4k).toBeGreaterThan(0);
    expect(i4k).toBeLessThan(a.f.calls.indexOf("prepareAsync"));

    const b = setup({ uhd: true });
    const q = b.player.play({ url: "https://h/x.mkv?sig=1", height: 1080 });
    b.f.prepared();
    await q;
    expect(b.f.calls.join("\n")).not.toContain("3840X2160");

    // an adaptive source of unknown height on a UHD panel gets it; a file does not
    const c = setup({ uhd: true });
    void c.player.play({ url: "https://h/master.m3u8?sig=1" });
    expect(c.f.calls).toContain(
      "setStreamingProperty ADAPTIVE_INFO FIXED_MAX_RESOLUTION=3840X2160"
    );
    const d = setup({ uhd: true });
    void d.player.play({ url: "https://h/movie.mp4?sig=1" });
    expect(d.f.calls.join("\n")).not.toContain("3840X2160");

    // a firmware that refuses ADAPTIVE_INFO gets the older SET_MODE_4K
    const e = setup();
    const set = e.f.av.setStreamingProperty;
    e.f.av.setStreamingProperty = (n, v) => {
      if (n === "ADAPTIVE_INFO") throw { name: "NotSupportedError", message: "no" };
      set(n, v);
    };
    void e.player.play({ url: "https://h/x.mkv?sig=1", height: 2160 });
    expect(e.f.calls).toContain("setStreamingProperty SET_MODE_4K TRUE");
  });

  it("picks the UI's audio track i among the stream's audio tracks", async () => {
    const t = setup();
    const p = t.player.play({ url: "https://h/a.m3u8", audio: 1 });
    t.f.prepared();
    await p;
    expect(t.f.calls).toContain("setSelectTrack AUDIO 2");
    expect(t.player.state().audio_tracks!.map((a) => a.enabled)).toEqual([false, true]);
    t.player.audioTrack(0);
    expect(t.f.calls[t.f.calls.length - 1]).toBe("setSelectTrack AUDIO 1");
    const n = t.f.calls.length;
    t.player.audioTrack(5);
    expect(t.f.calls.length).toBe(n);
    expect(logText()).toContain("no audio track 5");
  });

  it("draws a sidecar WebVTT itself, timed by AVPlay's play time, at the person's size", async () => {
    const t = setup();
    const p = t.player.play({
      url: "https://h/a.m3u8",
      subtitles: [{ id: "ar", url: "https://h/subs/ar.vtt?sig=2", lang: "ar" }],
      subtitle_scale: 1.5,
    });
    t.f.prepared();
    await p;
    expect(t.s.calls).toContain("setScale 1.5");
    t.player.subtitle("ar");
    await flush();
    expect(t.fetched).toEqual(["https://h/subs/ar.vtt?sig=2"]);
    expect(t.s.calls).toContain("setCues 1 ar");
    t.s.calls.length = 0;
    t.f.listener().oncurrentplaytime!(2000);
    expect(t.s.calls).toEqual(["render 2"]);
    // embedded text is ignored while a sidecar is chosen
    t.f.listener().onsubtitlechange!(1000, "embedded line");
    expect(t.s.calls).toEqual(["render 2"]);
    t.player.subtitleScale(2);
    expect(t.s.calls).toContain("setScale 2");
    // off: the layer is emptied and stops drawing
    t.player.subtitle(null);
    t.s.calls.length = 0;
    t.f.listener().oncurrentplaytime!(2500);
    expect(t.s.calls).toEqual([]);
  });

  it("draws an embedded track's text from onsubtitlechange (embedded:<n>)", async () => {
    const t = setup();
    const p = t.player.play({ url: "https://h/movie.mkv?sig=1" });
    t.f.prepared();
    await p;
    t.player.subtitle("embedded:3");
    expect(t.f.calls.slice(-2)).toEqual(["setSilentSubtitle false", "setSelectTrack TEXT 3"]);
    t.f.listener().onsubtitlechange!(2000, "مرحبا بالعالم");
    t.f.listener().onsubtitlechange!(0, "");
    expect(t.s.calls.slice(-2)).toEqual(["showText مرحبا بالعالم", "showText null"]);
  });

  it("a sidecar chosen before a newer one finishes loading is dropped", async () => {
    const t = setup();
    let release: (s: string) => void = () => {};
    const slow = new Promise<string>((r) => (release = r));
    const deps = (t.player as unknown as { d: { fetchText: (u: string) => Promise<string> } }).d;
    deps.fetchText = (u) => (u.includes("en") ? slow : Promise.resolve("WEBVTT"));
    const p = t.player.play({
      url: "https://h/a.m3u8",
      subtitles: [
        { id: "en", url: "https://h/en.vtt", lang: "en" },
        { id: "ar", url: "https://h/ar.vtt", lang: "ar" },
      ],
    });
    t.f.prepared();
    await p;
    t.player.subtitle("en");
    t.player.subtitle("ar");
    await flush();
    release("WEBVTT");
    await flush();
    expect(t.s.calls.filter((c) => c.startsWith("setCues 1"))).toEqual(["setCues 1 ar"]);
  });

  it("seeks with seekTo in ms, buffering until AVPlay answers", async () => {
    const t = setup();
    const p = t.player.play({ url: "https://h/a.m3u8" });
    t.f.prepared();
    await p;
    t.player.seek(600);
    expect(t.f.calls[t.f.calls.length - 1]).toBe("seekTo 600000");
    expect(t.player.state().position).toBe(600);
    expect(t.player.state().buffering).toBe(false);
    t.player.seek(-1);
    t.player.seek(Number.NaN);
    expect(t.f.calls.filter((c) => c.startsWith("seekTo")).length).toBe(1);
  });

  it("pauses, resumes, reports buffering and the end", async () => {
    const t = setup();
    const p = t.player.play({ url: "https://h/a.m3u8" });
    t.f.prepared();
    await p;
    t.player.pause();
    t.player.resume();
    expect(t.f.calls.slice(-2)).toEqual(["pause", "play"]);
    t.f.listener().onbufferingstart!();
    expect(t.player.state().buffering).toBe(true);
    t.f.listener().onbufferingcomplete!();
    t.f.listener().onstreamcompleted!();
    expect(t.player.state()).toMatchObject({ ended: true, buffering: false });
  });

  it("reports onerror as state.error and says a converted version follows", async () => {
    const t = setup();
    const p = t.player.play({ url: "https://h/a.m3u8" });
    t.f.prepared();
    await p;
    t.f.listener().onerror!("PLAYER_ERROR_NOT_SUPPORTED_FILE");
    expect(t.player.state().error).toBe("avplay PLAYER_ERROR_NOT_SUPPORTED_FILE");
    expect(t.notices).toEqual(["playback-error"]);
  });

  it("reports a failed prepare as state.error", async () => {
    const t = setup();
    const p = t.player.play({ url: "https://h/a.m3u8" });
    t.f.prepareFails({ name: "PLAYER_ERROR_CONNECTION_FAILED", message: "no route" });
    await p;
    expect(t.player.state().error).toBe("avplay prepare: PLAYER_ERROR_CONNECTION_FAILED no route");
    expect(t.f.calls).not.toContain("play");
  });

  it("ignores a late callback of an earlier play", async () => {
    const t = setup();
    void t.player.play({ url: "https://h/one.m3u8" });
    const first = t.f.listener();
    const p = t.player.play({ url: "https://h/two.m3u8" });
    expect(t.f.calls).toContain("close");
    first.onerror!("OLD");
    first.oncurrentplaytime!(99_000);
    t.f.prepared();
    await p;
    expect(t.player.state().error).toBeNull();
    expect(t.player.state().position).toBe(0);
  });

  it("stops: stop and close, the plane hidden and the page opaque again", async () => {
    const t = setup();
    const p = t.player.play({ url: "https://h/a.m3u8" });
    t.f.prepared();
    await p;
    t.player.stop();
    expect(t.f.calls.slice(-2)).toEqual(["stop", "close"]);
    expect(t.player.active()).toBe(false);
    expect(t.plane.hidden).toBe(true);
    expect(document.body.classList.contains("ml-avplay")).toBe(false);
    const n = t.f.calls.length;
    t.player.stop();
    expect(t.f.calls.length).toBe(n);
  });

  it("suspends AVPlay when the TV's app switcher hides the app, and restores it", async () => {
    const t = setup();
    const p = t.player.play({ url: "https://h/a.m3u8" });
    t.f.prepared();
    await p;
    const hidden = vi.spyOn(document, "hidden", "get").mockReturnValue(true);
    document.dispatchEvent(new Event("visibilitychange"));
    hidden.mockReturnValue(false);
    document.dispatchEvent(new Event("visibilitychange"));
    hidden.mockRestore();
    expect(t.f.calls.slice(-2)).toEqual(["suspend", "restore"]);
  });

  it("lists a track's language and codec, leaving out what AVPlay does not say", () => {
    expect(audioInfo({ index: 1, type: "AUDIO", extra_info: '{"language":"und"}' }, false)).toEqual({
      enabled: false,
    });
    expect(audioInfo({ index: 1, type: "AUDIO", extra_info: "not json" }, true)).toEqual({
      enabled: true,
    });
    expect(
      audioInfo({ index: 1, type: "AUDIO", extra_info: '{"fourCC":"AC3"}' }, true)
    ).toEqual({ enabled: true, label: "AC3" });
  });

  it("refuses anything but http(s) media", async () => {
    const t = setup();
    await expect(t.player.play({ url: "file:///etc/passwd" })).rejects.toThrow();
    expect(t.f.calls).toEqual([]);
  });
});
