// Story 58.09 — the launcher's side of the bridge to the home's /tv/ (bridge_level 1; the
// contract is apps/tv/web/bridge.schema.json, the UI's side web/src/bigscreen/input/bridge.ts).
//
// Every message is an object whose `type` starts with "maktaba:". The launcher takes a
// message only when it comes from the iframe it loaded (its window) AND from the home origin
// it loaded there; anything else is dropped and logged, never answered. It answers a known
// message it cannot act on, or one it does not know, with `maktaba:error`, so the UI can hide
// the feature; the UI uses only what `hello` lists.
//
//   UI → launcher   ready · hello · exit (after the UI's confirm) · setLanguage{lang} ·
//                   openHomePicker · play{url,…} · pause · resume · seek{sec} · stop ·
//                   audioTrack{i} · subtitle{id|null} · logs
//   launcher → UI   hello{bridge_level,platform,model,year,engine,version,device_caps,keys,
//                   messages} · action{action} · state{…} every 500 ms while playing ·
//                   visibility{hidden} · logs{lines} · error{re_type,error}

import { log, logText } from "./log";
import { playableUrl, type PlayerAdapter } from "./player";
import type { DeviceCaps, PlatformName } from "./platform/types";

export const BRIDGE_LEVEL = 1;
export const PREFIX = "maktaba:";

/** The remote's actions, as /tv/ takes them (web/src/bigscreen/input/actions.ts). */
export const ACTIONS = [
  "up",
  "down",
  "left",
  "right",
  "select",
  "back",
  "playPause",
  "play",
  "pause",
  "seekBack",
  "seekForward",
  "next",
  "previous",
  "search",
  "menu",
] as const;
export type BridgeAction = (typeof ACTIONS)[number];

/** The UI → launcher messages this launcher takes (hello lists them). */
export const UI_MESSAGES = [
  "ready",
  "hello",
  "exit",
  "setLanguage",
  "openHomePicker",
  "play",
  "pause",
  "resume",
  "seek",
  "stop",
  "audioTrack",
  "subtitle",
  "logs",
] as const;
export type UiMessage = (typeof UI_MESSAGES)[number];

export interface HelloInfo {
  platform: PlatformName;
  model: string;
  year: number;
  engine: string;
  version: string;
  device_caps: DeviceCaps;
  keys: string[];
}

export interface BridgeHandlers {
  exit(): void;
  setLanguage(lang: "en" | "ar"): void;
  openHomePicker(): void;
  /** The UI listens (its `ready`): the launcher hands it focus. */
  ready?(): void;
}

export interface BridgeOptions {
  /** The iframe's window, the only source taken. */
  frame: () => Window | null;
  /**
   * The origin messages must come from: the home's (https://home…), or for the bundled UI
   * the package's own, which a file:// page reports as "null" or "file://".
   */
  origins: string[];
  /** Where the launcher's messages go ("*" only for the bundled UI's file:// frame). */
  targetOrigin: string;
  info: HelloInfo;
  player: PlayerAdapter;
  handlers: BridgeHandlers;
  /** setInterval and clearInterval (tests pass fakes). */
  every?: (fn: () => void, ms: number) => unknown;
  stopEvery?: (h: unknown) => void;
}

export interface IncomingMessage {
  origin: string;
  source: unknown;
  data: unknown;
}

export const STATE_EVERY_MS = 500;

/** A short form of a message for the log (the log redacts what it must). */
function brief(data: Record<string, unknown>): string {
  try {
    return JSON.stringify(data).slice(0, 300);
  } catch {
    return String(data.type);
  }
}

export class LauncherBridge {
  private ticking: unknown = null;
  private readonly every: (fn: () => void, ms: number) => unknown;
  private readonly stopEvery: (h: unknown) => void;

  constructor(private readonly o: BridgeOptions) {
    this.every = o.every ?? ((fn, ms) => setInterval(fn, ms));
    this.stopEvery = o.stopEvery ?? ((h) => clearInterval(h as ReturnType<typeof setInterval>));
  }

  /** Takes one message event; true when it was the UI's and handled. */
  handle(e: IncomingMessage): boolean {
    const frame = this.o.frame();
    if (!frame || e.source !== frame) return false;
    if (this.o.origins.indexOf(e.origin) < 0) {
      log("bridge-in", `dropped: origin ${e.origin} is not the home's`);
      return false;
    }
    const data = e.data;
    if (!data || typeof data !== "object") return false;
    const m = data as Record<string, unknown>;
    if (typeof m.type !== "string" || m.type.indexOf(PREFIX) !== 0) return false;
    const name = m.type.slice(PREFIX.length);
    log("bridge-in", brief(m));
    try {
      this.dispatch(name, m);
    } catch (err) {
      this.error(name, "failed", err instanceof Error ? err.message : String(err));
    }
    return true;
  }

  private dispatch(name: string, m: Record<string, unknown>): void {
    const p = this.o.player;
    switch (name) {
      case "ready":
        this.o.handlers.ready?.();
        return;
      case "hello":
        this.send({
          type: `${PREFIX}hello`,
          re: typeof m.id === "number" ? m.id : 0,
          bridge_level: BRIDGE_LEVEL,
          ...this.o.info,
          messages: UI_MESSAGES.slice(),
        });
        return;
      case "exit":
        p.stop();
        this.stopTicking();
        this.o.handlers.exit();
        return;
      case "setLanguage":
        if (m.lang !== "en" && m.lang !== "ar") return this.error(name, "bad_message");
        this.o.handlers.setLanguage(m.lang);
        return;
      case "openHomePicker":
        p.stop();
        this.stopTicking();
        this.o.handlers.openHomePicker();
        return;
      case "play": {
        if (!playableUrl(m.url)) return this.error(name, "bad_message");
        const subs = Array.isArray(m.subtitles) ? (m.subtitles as unknown[]) : [];
        void p
          .play({
            url: m.url,
            start_sec: typeof m.start_sec === "number" ? m.start_sec : 0,
            subtitles: subs.filter(
              (s): s is { id: string; url: string } =>
                !!s &&
                typeof (s as { id?: unknown }).id === "string" &&
                typeof (s as { url?: unknown }).url === "string"
            ),
            audio: typeof m.audio === "number" ? m.audio : null,
            mode: m.mode === "live" ? "live" : "vod",
          })
          .then(
            () => this.sendState(),
            (err: unknown) => this.error(name, "failed", String(err))
          );
        this.startTicking();
        return;
      }
      case "pause":
        p.pause();
        this.sendState();
        return;
      case "resume":
        p.resume();
        this.startTicking();
        return;
      case "seek":
        if (typeof m.sec !== "number") return this.error(name, "bad_message");
        p.seek(m.sec);
        this.sendState();
        return;
      case "stop":
        p.stop();
        this.stopTicking();
        this.sendState();
        return;
      case "audioTrack":
        if (typeof m.i !== "number") return this.error(name, "bad_message");
        p.audioTrack(m.i);
        return;
      case "subtitle":
        if (m.id !== null && typeof m.id !== "string") return this.error(name, "bad_message");
        p.subtitle(m.id);
        return;
      case "logs":
        this.send({
          type: `${PREFIX}logs`,
          re: typeof m.id === "number" ? m.id : 0,
          lines: logText(),
        });
        return;
      default:
        this.error(name, "unknown");
    }
  }

  /** A key the iframe could not receive (a registered media or color key). */
  sendKey(action: BridgeAction): void {
    this.send({ type: `${PREFIX}action`, action });
  }

  /** The TV's app switcher hid or showed the app. */
  sendVisibility(hidden: boolean): void {
    if (hidden) this.o.player.pause();
    this.send({ type: `${PREFIX}visibility`, hidden });
  }

  sendState(): void {
    this.send({ type: `${PREFIX}state`, ...this.o.player.state() });
  }

  dispose(): void {
    this.stopTicking();
  }

  private error(name: string, error: "unknown" | "bad_message" | "failed", detail?: string): void {
    this.send({ type: `${PREFIX}error`, re_type: name, error, ...(detail ? { detail } : {}) });
  }

  private startTicking(): void {
    if (this.ticking !== null) return;
    this.ticking = this.every(() => {
      if (!this.o.player.active()) {
        this.stopTicking();
        return;
      }
      this.sendState();
      if (this.o.player.state().ended) this.stopTicking();
    }, STATE_EVERY_MS);
  }

  private stopTicking(): void {
    if (this.ticking === null) return;
    this.stopEvery(this.ticking);
    this.ticking = null;
  }

  private send(msg: Record<string, unknown>): void {
    const frame = this.o.frame();
    if (!frame) return;
    if (msg.type !== `${PREFIX}state`) log("bridge-out", brief(msg));
    frame.postMessage(msg, this.o.targetOrigin);
  }
}
