// Epic 58 (stories 58.10, 58.11) — /tv/ playing through the launcher: the stand-in <video>
// turns play, pause, seek, audio and subtitle choices into bridge messages (each one
// bridge.schema.json allows), fires the element's events from the launcher's state, makes
// the page transparent while it plays, and stops the launcher's player when it goes.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { checkMessage } from "../../../../apps/tv/web/src/schema";
import {
  resetBridgeForTest,
  setLauncherHelloForTest,
  startBridge,
  type LauncherHello,
  type UiMessage,
} from "./bridge";
import {
  LAUNCHER_VIDEO_CLASS,
  attachLauncher,
  launcherPlays,
  launcherSessionFields,
} from "./launcherMedia";

let frame: HTMLIFrameElement;
let parent: Window;
let sent: UiMessage[];

const HELLO: LauncherHello = {
  bridge_level: 1,
  platform: "webos",
  model: "OLED55C2",
  year: 2022,
  engine: "Chromium 87",
  version: "1.0.0",
  device_caps: {
    containers: ["mp4", "mkv", "ts"],
    video: [{ codec: "hevc", max_height: 2160, max_fps: 60, bit_depth: 10 }],
    hdr: ["hdr10", "hlg"],
    audio: ["aac", "eac3"],
    passthrough: ["eac3"],
    hls_segments: ["ts", "fmp4"],
    dash: false,
  },
  keys: [],
  messages: ["play", "pause", "resume", "seek", "stop", "audioTrack", "subtitle", "logs"],
};

function state(s: Record<string, unknown>) {
  window.dispatchEvent(
    new MessageEvent("message", {
      data: {
        type: "maktaba:state",
        position: 0,
        duration: 0,
        buffering: false,
        ended: false,
        error: null,
        ...s,
      },
      origin: "null",
      source: parent,
    })
  );
}

beforeEach(async () => {
  resetBridgeForTest();
  frame = document.createElement("iframe");
  document.body.appendChild(frame);
  parent = frame.contentWindow!;
  sent = [];
  vi.spyOn(parent, "postMessage").mockImplementation((m: unknown) => {
    sent.push(m as UiMessage);
  });
  Object.defineProperty(window, "parent", { value: parent, configurable: true });
  void startBridge(10);
  setLauncherHelloForTest(HELLO, "null");
  sent = [];
});

afterEach(() => {
  Object.defineProperty(window, "parent", { value: window, configurable: true });
  frame.remove();
  resetBridgeForTest();
  vi.restoreAllMocks();
  document.documentElement.classList.remove(LAUNCHER_VIDEO_CLASS);
});

function video(withTracks = 0) {
  const el = document.createElement("video");
  el.autoplay = true;
  // jsdom has no text tracks: a list like the browser's, one per <track>
  const list = Object.assign(new EventTarget(), { length: withTracks }) as unknown as Record<
    number,
    { mode: string }
  > &
    EventTarget;
  for (let i = 0; i < withTracks; i++) list[i] = { mode: "disabled" };
  Object.defineProperty(el, "textTracks", { value: list });
  document.body.appendChild(el);
  const events: string[] = [];
  for (const e of [
    "loadedmetadata",
    "durationchange",
    "play",
    "pause",
    "waiting",
    "playing",
    "seeking",
    "seeked",
    "ended",
  ])
    el.addEventListener(e, () => events.push(e));
  return { el, events };
}

const SRC = {
  url: "/stream/s1/master.m3u8?sig=abc&rid=r1",
  startAt: 30,
  subtitles: [
    { lang: "ar", label: "العربية", url: "/stream/s1/subs/ar.vtt?sig=abc" },
    { lang: "en", url: "/stream/s1/subs/en.vtt?sig=abc" },
  ],
};

describe("in a launcher that plays", () => {
  it("says the TV's profile and caps to the session", () => {
    expect(launcherPlays()).toBe(true);
    expect(launcherSessionFields()).toEqual({
      client_profile: "webos",
      device_caps: HELLO.device_caps,
    });
  });

  it("starts the launcher's player with absolute URLs, and every message fits the schema", () => {
    const { el } = video();
    const h = attachLauncher(el, SRC, () => undefined);
    expect(sent).toHaveLength(1);
    const play = sent[0] as Extract<UiMessage, { type: "maktaba:play" }>;
    expect(play).toMatchObject({
      type: "maktaba:play",
      url: `${location.origin}/stream/s1/master.m3u8?sig=abc&rid=r1`,
      start_sec: 30,
      mode: "vod",
      subtitle_scale: 1,
    });
    expect(play.subtitles).toEqual([
      {
        id: "sub-0",
        url: `${location.origin}/stream/s1/subs/ar.vtt?sig=abc`,
        lang: "ar",
        label: "العربية",
      },
      { id: "sub-1", url: `${location.origin}/stream/s1/subs/en.vtt?sig=abc`, lang: "en" },
    ]);
    expect(document.documentElement.classList.contains(LAUNCHER_VIDEO_CLASS)).toBe(true);
    el.pause();
    void el.play();
    el.currentTime = 90;
    h.detach();
    expect(sent.map((m) => m.type)).toEqual([
      "maktaba:play",
      "maktaba:pause",
      "maktaba:resume",
      "maktaba:seek",
      "maktaba:stop",
    ]);
    expect(sent[3]).toEqual({ type: "maktaba:seek", sec: 90 });
    for (const m of sent) expect(checkMessage("uiToLauncher", m)).toEqual([]);
    expect(document.documentElement.classList.contains(LAUNCHER_VIDEO_CLASS)).toBe(false);
  });

  it("mirrors the launcher's state as the element's own events and values", () => {
    const { el, events } = video();
    attachLauncher(el, SRC, () => undefined);
    expect(el.paused).toBe(false);
    expect(Number.isNaN(el.duration)).toBe(true);
    state({ position: 31, duration: 600, buffering: true });
    expect(el.duration).toBe(600);
    expect(el.currentTime).toBe(31);
    state({ position: 32, duration: 600 });
    el.currentTime = 100;
    state({ position: 100, duration: 600 });
    state({ position: 600, duration: 600, ended: true });
    expect(el.ended).toBe(true);
    expect(el.paused).toBe(true);
    expect(events).toEqual([
      "play",
      "loadedmetadata",
      "durationchange",
      // buffering from the start: playing once the first frames are in
      "playing",
      "seeking",
      "seeked",
      "pause",
      "ended",
    ]);
  });

  it("lists the launcher's audio tracks and switches them by index", () => {
    const { el } = video();
    attachLauncher(el, SRC, () => undefined);
    state({
      position: 1,
      duration: 10,
      audio_tracks: [
        { enabled: true, lang: "ar", label: "Arabic" },
        { enabled: false, lang: "en" },
      ],
    });
    const tracks = (el as unknown as { audioTracks: { language: string; enabled: boolean }[] })
      .audioTracks;
    expect(tracks.map((t) => [t.language, t.enabled])).toEqual([
      ["ar", true],
      ["en", false],
    ]);
    tracks[1]!.enabled = true;
    expect(sent.at(-1)).toEqual({ type: "maktaba:audioTrack", i: 1 });
    expect(checkMessage("uiToLauncher", sent.at(-1))).toEqual([]);
  });

  it("hands the subtitle shown to the launcher, with the person's size", () => {
    const { el } = video(2);
    el.style.setProperty("--mkt-caption-scale", "1.5");
    attachLauncher(el, SRC, () => undefined);
    el.textTracks[1]!.mode = "showing";
    el.textTracks.dispatchEvent(new Event("change"));
    expect(sent.at(-1)).toEqual({ type: "maktaba:subtitle", id: "sub-1", subtitle_scale: 1.5 });
    el.textTracks[1]!.mode = "disabled";
    el.textTracks.dispatchEvent(new Event("change"));
    expect(sent.at(-1)).toEqual({ type: "maktaba:subtitle", id: null, subtitle_scale: 1.5 });
    for (const m of sent) expect(checkMessage("uiToLauncher", m)).toEqual([]);
  });

  it("names the subtitle chosen before playback right after play", () => {
    const { el } = video(2);
    el.autoplay = false;
    attachLauncher(el, SRC, () => undefined);
    el.textTracks[0]!.mode = "showing";
    el.textTracks.dispatchEvent(new Event("change"));
    expect(sent).toEqual([]);
    void el.play();
    expect(sent.map((m) => m.type)).toEqual(["maktaba:play", "maktaba:subtitle"]);
  });

  it("reports the player's error once", () => {
    const { el } = video();
    const errors: string[] = [];
    attachLauncher(el, SRC, (e) => errors.push(e));
    state({ error: "webos media error 4" });
    state({ error: "webos media error 4" });
    expect(errors).toEqual(["webos media error 4"]);
  });
});

describe("outside a launcher, or one that does not play", () => {
  it("adds nothing to the session", () => {
    setLauncherHelloForTest({ ...HELLO, messages: ["logs"] });
    expect(launcherPlays()).toBe(false);
    expect(launcherSessionFields()).toEqual({});
  });
});
