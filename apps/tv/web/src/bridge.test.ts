// Story 58.09 T02 — the launcher's bridge with a fake iframe: messages from the loaded home
// origin and its frame are handled; any other origin or window is dropped (no playback, no
// exit). T06's launcher half: every message it sends validates against bridge.schema.json.
import { ACTIONS, LauncherBridge, STATE_EVERY_MS, UI_MESSAGES, type HelloInfo } from "./bridge";
import { IDLE, type PlayerAdapter, type PlayerState } from "./player";
import { checkMessage, messageTypes } from "./schema";
import { clearLog, logText } from "./log";

const HOME = "https://home.example:8443";

const INFO: HelloInfo = {
  platform: "tizen",
  model: "QE55Q80T",
  year: 2020,
  engine: "Chromium 69",
  version: "1.0.0",
  device_caps: { video: ["h264"], audio: ["aac"], hdr: [], max_height: 2160 },
  keys: ["MediaPlayPause", "ColorF0Red"],
};

function fakePlayer() {
  let st: PlayerState = { ...IDLE };
  let on = false;
  const calls: string[] = [];
  const p: PlayerAdapter = {
    name: "fake",
    async play(req) {
      calls.push(`play ${req.url} ${req.start_sec}`);
      on = true;
      st = { ...st, duration: 100 };
    },
    pause: () => calls.push("pause"),
    resume: () => calls.push("resume"),
    seek: (s) => calls.push(`seek ${s}`),
    stop: () => {
      calls.push("stop");
      on = false;
    },
    audioTrack: (i) => calls.push(`audio ${i}`),
    subtitle: (id) => calls.push(`subtitle ${id}`),
    active: () => on,
    state: () => st,
  };
  return { p, calls, set: (s: Partial<PlayerState>) => (st = { ...st, ...s }) };
}

function setup(origins = [HOME]) {
  const sent: { msg: Record<string, unknown>; target: string }[] = [];
  const frameWin = { postMessage: (msg: Record<string, unknown>, target: string) => sent.push({ msg, target }) };
  const other = { postMessage: () => {} };
  const player = fakePlayer();
  const handlers = { exit: vi.fn(), setLanguage: vi.fn(), openHomePicker: vi.fn(), ready: vi.fn() };
  let tick: (() => void) | null = null;
  const bridge = new LauncherBridge({
    frame: () => frameWin as unknown as Window,
    origins,
    targetOrigin: origins[0]!,
    info: INFO,
    player: player.p,
    handlers,
    every: (fn) => {
      tick = fn;
      return 1;
    },
    stopEvery: () => {
      tick = null;
    },
  });
  const fromHome = (data: unknown) => bridge.handle({ origin: HOME, source: frameWin, data });
  return { bridge, sent, frameWin, other, player, handlers, fromHome, tick: () => tick?.(), ticking: () => tick !== null };
}

beforeEach(() => clearLog());

describe("the launcher's bridge (T02)", () => {
  it("answers hello with what the launcher is and the messages it takes", () => {
    const s = setup();
    expect(s.fromHome({ type: "maktaba:hello", id: 7 })).toBe(true);
    expect(s.sent).toHaveLength(1);
    const { msg, target } = s.sent[0]!;
    expect(target).toBe(HOME);
    expect(msg).toMatchObject({ type: "maktaba:hello", re: 7, bridge_level: 1, platform: "tizen", year: 2020 });
    expect(msg.messages).toEqual(UI_MESSAGES.slice());
  });

  it("drops a message from another origin, even from the frame: no playback, no exit", () => {
    const s = setup();
    for (const data of [
      { type: "maktaba:exit" },
      { type: "maktaba:play", url: "https://home.example:8443/m.m3u8" },
      { type: "maktaba:hello", id: 1 },
    ]) {
      expect(s.bridge.handle({ origin: "https://evil.example", source: s.frameWin, data })).toBe(false);
      expect(s.bridge.handle({ origin: "null", source: s.frameWin, data })).toBe(false);
    }
    expect(s.handlers.exit).not.toHaveBeenCalled();
    expect(s.player.calls).toEqual([]);
    expect(s.sent).toEqual([]);
    expect(logText()).toContain("dropped: origin https://evil.example");
  });

  it("drops a message from another window, even with the home's origin", () => {
    const s = setup();
    expect(s.bridge.handle({ origin: HOME, source: s.other, data: { type: "maktaba:exit" } })).toBe(false);
    expect(s.bridge.handle({ origin: HOME, source: null, data: { type: "maktaba:exit" } })).toBe(false);
    expect(s.handlers.exit).not.toHaveBeenCalled();
  });

  it("ignores what is not a bridge message", () => {
    const s = setup();
    expect(s.fromHome("maktaba:exit")).toBe(false);
    expect(s.fromHome({ type: "exit" })).toBe(false);
    expect(s.fromHome(null)).toBe(false);
    expect(s.handlers.exit).not.toHaveBeenCalled();
  });

  it("exits after the UI's confirm, stopping playback", () => {
    const s = setup();
    s.fromHome({ type: "maktaba:exit" });
    expect(s.player.calls).toContain("stop");
    expect(s.handlers.exit).toHaveBeenCalledTimes(1);
  });

  it("plays, reports state every 500 ms while playing, and stops reporting", async () => {
    const s = setup();
    expect(STATE_EVERY_MS).toBe(500);
    s.fromHome({ type: "maktaba:play", url: `${HOME}/stream/v1/master.m3u8?sig=abc`, start_sec: 30, mode: "vod" });
    await Promise.resolve();
    expect(s.player.calls[0]).toBe(`play ${HOME}/stream/v1/master.m3u8?sig=abc 30`);
    expect(s.ticking()).toBe(true);
    s.player.set({ position: 31 });
    s.tick();
    const state = s.sent.filter((x) => x.msg.type === "maktaba:state").pop()!.msg;
    expect(state).toMatchObject({ position: 31, duration: 100, ended: false, error: null });
    s.fromHome({ type: "maktaba:pause" });
    s.fromHome({ type: "maktaba:seek", sec: 90 });
    s.fromHome({ type: "maktaba:audioTrack", i: 1 });
    s.fromHome({ type: "maktaba:subtitle", id: "ar" });
    s.fromHome({ type: "maktaba:subtitle", id: null });
    s.fromHome({ type: "maktaba:stop" });
    expect(s.player.calls.slice(1)).toEqual(["pause", "seek 90", "audio 1", "subtitle ar", "subtitle null", "stop"]);
    expect(s.ticking()).toBe(false);
    // the media URL's sig never reaches the log
    expect(logText()).not.toContain("sig=abc");
    expect(logText()).toContain("sig=[REDACTED]");
  });

  it("refuses a play of anything but http(s) media", () => {
    const s = setup();
    s.fromHome({ type: "maktaba:play", url: "javascript:alert(1)" });
    s.fromHome({ type: "maktaba:play", url: "file:///etc/passwd" });
    expect(s.player.calls).toEqual([]);
    expect(s.sent.map((x) => x.msg)).toEqual([
      { type: "maktaba:error", re_type: "play", error: "bad_message" },
      { type: "maktaba:error", re_type: "play", error: "bad_message" },
    ]);
  });

  it("answers a message it does not know with error unknown", () => {
    const s = setup();
    s.fromHome({ type: "maktaba:launchRockets" });
    expect(s.sent[0]!.msg).toEqual({ type: "maktaba:error", re_type: "launchRockets", error: "unknown" });
  });

  it("passes setLanguage, openHomePicker and ready to the launcher", () => {
    const s = setup();
    s.fromHome({ type: "maktaba:ready" });
    s.fromHome({ type: "maktaba:setLanguage", lang: "ar" });
    s.fromHome({ type: "maktaba:setLanguage", lang: "fr" });
    s.fromHome({ type: "maktaba:openHomePicker" });
    expect(s.handlers.ready).toHaveBeenCalled();
    expect(s.handlers.setLanguage).toHaveBeenCalledWith("ar");
    expect(s.handlers.setLanguage).toHaveBeenCalledTimes(1);
    expect(s.handlers.openHomePicker).toHaveBeenCalled();
  });

  it("sends keys, visibility and its logs", () => {
    const s = setup();
    s.bridge.sendKey("menu");
    s.bridge.sendVisibility(true);
    s.fromHome({ type: "maktaba:logs", id: 3 });
    expect(s.sent[0]!.msg).toEqual({ type: "maktaba:action", action: "menu" });
    expect(s.sent[1]!.msg).toEqual({ type: "maktaba:visibility", hidden: true });
    expect(s.player.calls).toContain("pause");
    expect(s.sent[2]!.msg).toMatchObject({ type: "maktaba:logs", re: 3 });
  });

  it("takes the bundled UI's file:// frame by its own origins", () => {
    const s = setup(["file://", "null"]);
    expect(s.bridge.handle({ origin: "null", source: s.frameWin, data: { type: "maktaba:ready" } })).toBe(true);
    expect(s.bridge.handle({ origin: HOME, source: s.frameWin, data: { type: "maktaba:ready" } })).toBe(false);
  });
});

describe("the launcher's messages match bridge.schema.json (T06)", () => {
  it("every message it sends validates", async () => {
    const s = setup();
    s.fromHome({ type: "maktaba:hello", id: 1 });
    s.fromHome({ type: "maktaba:play", url: `${HOME}/m.m3u8` });
    await Promise.resolve();
    s.tick();
    s.bridge.sendKey("back");
    s.bridge.sendVisibility(false);
    s.fromHome({ type: "maktaba:logs", id: 2 });
    s.fromHome({ type: "maktaba:nope" });
    s.fromHome({ type: "maktaba:seek", sec: "x" });
    s.player.set({ error: "media error 4" });
    s.bridge.sendState();
    const types = new Set<string>();
    for (const { msg } of s.sent) {
      types.add(msg.type as string);
      expect(checkMessage("launcherToUi", msg)).toEqual([]);
    }
    expect([...types].sort()).toEqual(messageTypes("launcherToUi").sort());
  });

  it("takes exactly the UI messages and actions the schema names", () => {
    expect(messageTypes("uiToLauncher").map((t) => t.replace("maktaba:", "")).sort()).toEqual(
      UI_MESSAGES.slice().sort()
    );
    const schemaActions = (checkMessage("launcherToUi", { type: "maktaba:action", action: "nope" }) ?? [])[0];
    expect(schemaActions).toContain(JSON.stringify(ACTIONS.slice()));
  });

  it("refuses a message the schema does not allow", () => {
    expect(checkMessage("launcherToUi", { type: "maktaba:state", position: "1" })).not.toEqual([]);
    expect(checkMessage("uiToLauncher", { type: "maktaba:play" })).toEqual(["$.url: is missing"]);
    expect(checkMessage("uiToLauncher", { type: "maktaba:exit", extra: 1 })).toEqual(["$.extra: is not in the schema"]);
  });
});
