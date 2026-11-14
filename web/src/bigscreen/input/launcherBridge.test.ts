// Epic 58 story 58.09 — /tv/'s side of the launcher bridge: ready and hello at start, only
// the parent at a launcher's origin is heard, the hello answer decides what /tv/ sends,
// and (T06, /tv/'s half) every message /tv/ sends is one bridge.schema.json allows.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { checkMessage, messageTypes } from "../../../../apps/tv/web/src/schema";
import {
  BRIDGE_LEVEL,
  launcherHello,
  launcherLogs,
  launcherOrigin,
  launcherTakes,
  listenToLauncher,
  onLauncherState,
  onLauncherVisibility,
  postToLauncher,
  resetBridgeForTest,
  startBridge,
  tellLauncher,
  type UiMessage,
} from "./bridge";

let frame: HTMLIFrameElement;
let parent: Window;
let sent: UiMessage[];

const hello = (re: number) => ({
  type: "maktaba:hello",
  re,
  bridge_level: 1,
  platform: "tizen",
  model: "QE55Q80T",
  year: 2020,
  engine: "Chromium 69",
  version: "1.2.3",
  device_caps: { video: ["h264"], audio: ["aac"], hdr: [], max_height: 2160 },
  keys: ["MediaPlayPause"],
  messages: ["play", "pause", "logs"],
});

function fromParent(data: unknown, origin = "file://", source: Window | null = parent) {
  window.dispatchEvent(new MessageEvent("message", { data, origin, source }));
}

beforeEach(() => {
  resetBridgeForTest();
  frame = document.createElement("iframe");
  document.body.appendChild(frame);
  parent = frame.contentWindow!;
  sent = [];
  vi.spyOn(parent, "postMessage").mockImplementation((m: unknown) => {
    sent.push(m as UiMessage);
  });
  Object.defineProperty(window, "parent", { value: parent, configurable: true });
});

afterEach(() => {
  Object.defineProperty(window, "parent", { value: window, configurable: true });
  frame.remove();
  resetBridgeForTest();
  vi.restoreAllMocks();
});

describe("the bridge in the launcher", () => {
  it("says ready, asks hello at bridge_level 1, and keeps the answer", async () => {
    const started = startBridge(1000);
    expect(sent.map((m) => m.type)).toEqual(["maktaba:ready", "maktaba:hello"]);
    const ask = sent[1] as Extract<UiMessage, { type: "maktaba:hello" }>;
    expect(ask.bridge_level).toBe(BRIDGE_LEVEL);
    fromParent(hello(ask.id), "null");
    const h = await started;
    expect(h?.platform).toBe("tizen");
    expect(launcherHello()?.model).toBe("QE55Q80T");
    expect(launcherTakes("play")).toBe(true);
    expect(launcherTakes("subtitle")).toBe(false);
    // asked again: the kept answer, no new message
    await startBridge();
    expect(sent).toHaveLength(2);
  });

  it("hears only the parent, only at a launcher's origin", async () => {
    const started = startBridge(50);
    fromParent(hello(1), "https://evil.example");
    fromParent(hello(1), "file://", window);
    expect(await started).toBeNull();
    expect(launcherHello()).toBeNull();
    expect(launcherOrigin("null")).toBe(true);
    expect(launcherOrigin("app://abc")).toBe(true);
    expect(launcherOrigin(window.location.origin)).toBe(true);
    expect(launcherOrigin("https://evil.example")).toBe(false);
  });

  it("takes key presses, the player's state and the app switcher from the launcher", async () => {
    const actions: string[] = [];
    const stop = listenToLauncher((a) => actions.push(a));
    expect(sent.at(-1)?.type).toBe("maktaba:ready");
    fromParent({ type: "maktaba:action", action: "back" });
    fromParent({ type: "maktaba:action", action: "exec" });
    fromParent({ type: "maktaba:action", action: "select" }, "https://evil.example");
    expect(actions).toEqual(["back"]);
    stop();

    const started = startBridge(1000);
    fromParent(hello(1));
    await started;
    const states: number[] = [];
    const hidden: boolean[] = [];
    onLauncherState((s) => states.push(s.position));
    onLauncherVisibility((h) => hidden.push(h));
    const st = {
      type: "maktaba:state",
      position: 12,
      duration: 60,
      buffering: false,
      ended: false,
      error: null,
    };
    expect(checkMessage("launcherToUi", st)).toEqual([]);
    fromParent(st);
    fromParent({ type: "maktaba:visibility", hidden: true });
    fromParent({ type: "maktaba:visibility", hidden: "yes" });
    expect(states).toEqual([12]);
    expect(hidden).toEqual([true]);
  });

  it("asks the launcher's log lines when hello listed logs", async () => {
    const started = startBridge(1000);
    fromParent(hello(1));
    await started;
    const lines = launcherLogs(1000);
    const ask = sent.at(-1) as Extract<UiMessage, { type: "maktaba:logs" }>;
    expect(ask.type).toBe("maktaba:logs");
    const answer = { type: "maktaba:logs", re: ask.id, lines: "boot\nready" };
    expect(checkMessage("launcherToUi", answer)).toEqual([]);
    fromParent(answer);
    expect(await lines).toBe("boot\nready");
  });

  it("T06: every message /tv/ sends is one the schema allows, and each kind is covered", async () => {
    void startBridge(10);
    tellLauncher("maktaba:exit");
    tellLauncher("maktaba:openHomePicker");
    tellLauncher("maktaba:pause");
    tellLauncher("maktaba:resume");
    tellLauncher("maktaba:stop");
    postToLauncher({ type: "maktaba:setLanguage", lang: "ar" });
    postToLauncher({
      type: "maktaba:play",
      url: "https://h.example/stream/1/master.m3u8?sig=x",
      start_sec: 30,
      subtitles: [{ id: "s1", url: "https://h.example/sub/1.vtt", lang: "ar", label: "عربي" }],
      audio: 1,
      mode: "vod",
    });
    postToLauncher({ type: "maktaba:seek", sec: 90 });
    postToLauncher({ type: "maktaba:audioTrack", i: 0 });
    postToLauncher({ type: "maktaba:subtitle", id: null });
    postToLauncher({ type: "maktaba:logs", id: 9 });
    for (const m of sent) expect(checkMessage("uiToLauncher", m), m.type).toEqual([]);
    expect(new Set(sent.map((m) => m.type))).toEqual(new Set(messageTypes("uiToLauncher")));
    // and the schema refuses what /tv/ must not send
    expect(checkMessage("uiToLauncher", { type: "maktaba:seek", sec: "90" })).not.toEqual([]);
    expect(checkMessage("uiToLauncher", { type: "maktaba:eval", code: "x" })).not.toEqual([]);
  });

  it("outside a launcher: nothing is sent and hello is null", async () => {
    Object.defineProperty(window, "parent", { value: window, configurable: true });
    postToLauncher({ type: "maktaba:exit" });
    expect(await startBridge(10)).toBeNull();
    expect(sent).toEqual([]);
  });
});
