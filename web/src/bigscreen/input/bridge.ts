// Epic 58 stories 58.08 and 58.09 — /tv/'s side of the bridge to the Samsung and LG
// launcher (bridge_level 1; the contract is apps/tv/web/bridge.schema.json, the launcher's
// side apps/tv/web/src/bridge.ts).
//
// The launcher loads /tv/?bridge=1 in an iframe and talks to it with postMessage. Keys
// pressed while the iframe has focus come to /tv/ as its own keydown events (keys.ts maps
// the TVs' key codes); the bridge carries what a launcher sees first (a key it registered
// with tizen.tvinputdevice, say), and what only the packaged app can do:
//
//   launcher → /tv/   maktaba:action {action}      a key press, as a BigAction
//                     maktaba:hello {bridge_level, platform, model, year, engine, version,
//                                    device_caps, keys, messages}   the answer to hello
//                     maktaba:state {…}            the launcher's player, every 500 ms
//                     maktaba:visibility {hidden}  the TV's app switcher
//                     maktaba:logs {lines} · maktaba:error {re_type, error}
//                     maktaba:signIn {grant}       a cloud code's grant, once (58.09)
//   /tv/ → launcher   maktaba:ready                once /tv/ listens
//                     maktaba:hello {id, takes}    what are you? (bridge_level 1); takes
//                                                  ["signIn"]: /tv/ redeems a grant
//                     maktaba:exit                 after Leave Maktaba?
//                     maktaba:setLanguage {lang} · maktaba:openHomePicker (Switch home)
//                     maktaba:play {url,…} · pause · resume · seek · stop · audioTrack ·
//                     subtitle · logs
//
// Only the parent window is listened to, and only from a launcher's origin: a packaged TV
// app (Tizen and webOS load theirs from file:// or app://, which a message reports as
// "null", "file://" or "app://…") or the home itself. A web page that frames /tv/ is not a
// launcher. Only a known action is taken: an action is a key press, never a command with
// arguments. /tv/ uses a launcher message only when hello listed it.
//
// signIn (story 58.09's second part): after a cloud code's approval the launcher's hello
// answer says `sign_in: true` and a `maktaba:signIn {grant}` follows, once. /tv/ takes it
// only from the launcher that answered hello, only when that answer announced it, and only
// once; it redeems the grant in its own origin (POST /api/pairing/device/token), so the
// tokens never cross the bridge. The grant is the home's own single-use pairing secret.
//
// Until such a launcher has answered hello, /tv/ sends nothing but ready and hello (no play
// address, no command). A packaged app's origin is opaque ("null"), which postMessage cannot
// name, so those messages go to "*"; a launcher at an http(s) origin (the home itself, in a
// test) is sent to by its origin.
import type { BigAction } from "./actions";

export const BRIDGE_LEVEL = 1;
export const BRIDGE_ACTION = "maktaba:action";
export const BRIDGE_EXIT = "maktaba:exit";
export const BRIDGE_READY = "maktaba:ready";

const ACTIONS: readonly BigAction[] = [
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
];

/** What the launcher said it is (its hello answer). */
export interface LauncherHello {
  bridge_level: number;
  platform: "tizen" | "webos" | "browser";
  model: string;
  year: number;
  engine: string;
  version: string;
  /** The API's DeviceCaps (story 58.02): what the TV plays, for session creation. */
  device_caps: {
    containers: string[];
    video: { codec: string; max_height?: number; max_fps?: number; bit_depth?: number }[];
    hdr: string[];
    audio: string[];
    passthrough: string[];
    max_audio_channels?: number;
    hls_segments: string[];
    dash?: boolean;
    max_bitrate_kbps?: number;
  };
  keys: string[];
  messages: string[];
  /** A cloud code's grant follows this answer (maktaba:signIn). */
  sign_in?: boolean;
}

/** The launcher's player, as its state messages say. */
export interface LauncherPlayerState {
  position: number;
  duration: number;
  buffering: boolean;
  ended: boolean;
  error: string | null;
  /** The stream's audio tracks, when the launcher's player lists them. */
  audio_tracks?: { enabled: boolean; lang?: string; label?: string }[];
}

/** /tv/ → launcher messages (bridge.schema.json's uiToLauncher). */
export type UiMessage =
  | { type: "maktaba:ready" }
  | { type: "maktaba:hello"; id: number; bridge_level: number; takes?: string[] }
  | { type: "maktaba:exit" }
  | { type: "maktaba:setLanguage"; lang: "en" | "ar" }
  | { type: "maktaba:openHomePicker" }
  | {
      type: "maktaba:play";
      url: string;
      start_sec?: number;
      subtitles?: { id: string; url: string; lang?: string; label?: string }[];
      audio?: number | null;
      mode?: "vod" | "live";
      height?: number;
      subtitle_scale?: number;
    }
  | { type: "maktaba:pause" }
  | { type: "maktaba:resume" }
  | { type: "maktaba:seek"; sec: number }
  | { type: "maktaba:stop" }
  | { type: "maktaba:audioTrack"; i: number }
  | { type: "maktaba:subtitle"; id: string | null; subtitle_scale?: number }
  | { type: "maktaba:logs"; id: number };

/** The action a bridge message carries, or null for anything else. */
export function bridgeAction(data: unknown): BigAction | null {
  if (!data || typeof data !== "object") return null;
  const m = data as { type?: unknown; action?: unknown };
  if (m.type !== BRIDGE_ACTION || typeof m.action !== "string") return null;
  return (ACTIONS as readonly string[]).includes(m.action) ? (m.action as BigAction) : null;
}

/** Whether a message's origin can be a launcher's (see the header). */
export function launcherOrigin(origin: string): boolean {
  if (origin === "null" || origin.startsWith("file:") || origin.startsWith("app:")) return true;
  try {
    return origin === window.location.origin;
  } catch {
    return false;
  }
}

/** Whether /tv/ runs inside a launcher's iframe. */
export function inLauncher(): boolean {
  try {
    return window.parent !== window;
  } catch {
    return true;
  }
}

/** Sends a message to the launcher (no-op outside one, or before its hello: see the header). */
export function postToLauncher(msg: UiMessage): void {
  if (!inLauncher()) return;
  if (!hello && msg.type !== BRIDGE_READY && msg.type !== "maktaba:hello") return;
  const target =
    helloOrigin.startsWith("http:") || helloOrigin.startsWith("https:") ? helloOrigin : "*";
  window.parent.postMessage(msg, target);
}

/** Tells the launcher something without arguments (no-op outside one). */
export function tellLauncher(
  type:
    | typeof BRIDGE_EXIT
    | typeof BRIDGE_READY
    | "maktaba:openHomePicker"
    | "maktaba:pause"
    | "maktaba:resume"
    | "maktaba:stop"
) {
  postToLauncher({ type } as UiMessage);
}

/** A message from the launcher (the parent, at a launcher's origin), or null. */
function fromLauncher(e: MessageEvent): Record<string, unknown> | null {
  if (e.source !== window.parent || e.source === window) return null;
  if (!launcherOrigin(e.origin)) return null;
  if (!e.data || typeof e.data !== "object") return null;
  const m = e.data as Record<string, unknown>;
  return typeof m.type === "string" && m.type.startsWith("maktaba:") ? m : null;
}

/** Listens to the launcher's actions; returns the way to stop. */
export function listenToLauncher(onAction: (a: BigAction) => void): () => void {
  const onMessage = (e: MessageEvent) => {
    if (!fromLauncher(e)) return;
    const a = bridgeAction(e.data);
    if (a) onAction(a);
  };
  window.addEventListener("message", onMessage);
  tellLauncher(BRIDGE_READY);
  return () => window.removeEventListener("message", onMessage);
}

// ---- hello, state and visibility (story 58.09) ------------------------------------------

let hello: LauncherHello | null = null;
/** The origin hello came from ("null", "file://", "app://…" or the home's own). */
let helloOrigin = "";
let nextId = 1;
const helloWaiters: ((h: LauncherHello | null) => void)[] = [];
const stateListeners = new Set<(s: LauncherPlayerState) => void>();
const visibilityListeners = new Set<(hidden: boolean) => void>();
/** The one log request waiting for its answer (a newer request replaces it). */
let logsPending: { id: number; done: (lines: string | null) => void } | null = null;
let started = false;
/** A grant the hello answer announced: "waiting" until its signIn comes, then the grant. */
let grantState: null | "waiting" | { grant: string } = null;
const grantWaiters: ((g: string | null) => void)[] = [];

/** The shape of a grant (the home's pairing secret, as the cloud checks it at claim). */
const GRANT_SHAPE = /^[A-Za-z0-9_-]{16,128}$/;

function isHello(m: Record<string, unknown>): boolean {
  return (
    m.type === "maktaba:hello" &&
    typeof m.bridge_level === "number" &&
    typeof m.platform === "string" &&
    Array.isArray(m.messages)
  );
}

function onBridgeMessage(e: MessageEvent): void {
  const m = fromLauncher(e);
  if (!m) return;
  switch (m.type) {
    case "maktaba:hello":
      if (!isHello(m)) return;
      {
        // only the first answer can announce a grant
        const first = hello === null;
        hello = m as unknown as LauncherHello;
        helloOrigin = e.origin;
        if (first && m.sign_in === true) grantState = "waiting";
      }
      helloWaiters.splice(0).forEach((f) => f(hello));
      return;
    case "maktaba:signIn": {
      // only the launcher that answered hello, as announced there, and once
      if (grantState !== "waiting" || e.origin !== helloOrigin) return;
      const g = m.grant;
      if (typeof g !== "string" || !GRANT_SHAPE.test(g)) {
        grantState = null;
        grantWaiters.splice(0).forEach((f) => f(null));
        return;
      }
      grantState = { grant: g };
      const waiting = grantWaiters.splice(0);
      if (waiting.length) {
        grantState = null;
        waiting[0]!(g);
        waiting.slice(1).forEach((f) => f(null));
      }
      return;
    }
    case "maktaba:state":
      stateListeners.forEach((f) => f(m as unknown as LauncherPlayerState));
      return;
    case "maktaba:visibility":
      if (typeof m.hidden === "boolean") visibilityListeners.forEach((f) => f(m.hidden as boolean));
      return;
    case "maktaba:logs": {
      // the answer to the request waiting, matched by its id (never looked up by it)
      const p = logsPending;
      if (p && m.re === p.id && typeof m.lines === "string") {
        logsPending = null;
        p.done(m.lines);
      }
      return;
    }
    default:
      return;
  }
}

/**
 * Starts the bridge in a launcher: listens, says ready, and asks hello. Resolves with the
 * launcher's answer (null outside a launcher, or when none comes within timeoutMs).
 */
export function startBridge(timeoutMs = 5000): Promise<LauncherHello | null> {
  if (!inLauncher()) return Promise.resolve(null);
  if (!started) {
    started = true;
    window.addEventListener("message", onBridgeMessage);
  }
  if (hello) return Promise.resolve(hello);
  return new Promise((resolve) => {
    const timer = setTimeout(() => {
      const i = helloWaiters.indexOf(done);
      if (i >= 0) helloWaiters.splice(i, 1);
      resolve(null);
    }, timeoutMs);
    function done(h: LauncherHello | null) {
      clearTimeout(timer);
      resolve(h);
    }
    helloWaiters.push(done);
    postToLauncher({ type: BRIDGE_READY });
    postToLauncher({
      type: "maktaba:hello",
      id: nextId++,
      bridge_level: BRIDGE_LEVEL,
      takes: ["signIn"],
    });
  });
}

/** Whether the launcher announced a cloud code's grant that /tv/ has not taken yet. */
export function launcherGrantExpected(): boolean {
  return grantState !== null;
}

/**
 * Takes the cloud code's grant the launcher hands over (once: a second call gets null).
 * Resolves with null when none was announced, or none came within timeoutMs.
 */
export function takeLauncherGrant(timeoutMs = 10000): Promise<string | null> {
  if (grantState === null) return Promise.resolve(null);
  if (grantState !== "waiting") {
    const g = grantState.grant;
    grantState = null;
    return Promise.resolve(g);
  }
  return new Promise((resolve) => {
    const timer = setTimeout(() => {
      const i = grantWaiters.indexOf(done);
      if (i >= 0) grantWaiters.splice(i, 1);
      if (grantState === "waiting") grantState = null;
      resolve(null);
    }, timeoutMs);
    function done(g: string | null) {
      clearTimeout(timer);
      resolve(g);
    }
    grantWaiters.push(done);
  });
}

/** The launcher's hello answer, once it came. */
export function launcherHello(): LauncherHello | null {
  return hello;
}

/** Whether the launcher takes a message (hello listed it). */
export function launcherTakes(name: string): boolean {
  return !!hello && hello.messages.includes(name);
}

/** Follows the launcher's player state; returns the way to stop. */
export function onLauncherState(f: (s: LauncherPlayerState) => void): () => void {
  stateListeners.add(f);
  return () => stateListeners.delete(f);
}

/** Follows the TV's app switcher (hidden: the app went to the background). */
export function onLauncherVisibility(f: (hidden: boolean) => void): () => void {
  visibilityListeners.add(f);
  return () => visibilityListeners.delete(f);
}

/** The launcher's log lines (for a report), or null when it does not answer. */
export function launcherLogs(timeoutMs = 2000): Promise<string | null> {
  if (!launcherTakes("logs")) return Promise.resolve(null);
  const id = nextId++;
  logsPending?.done(null);
  return new Promise((resolve) => {
    const timer = setTimeout(() => {
      if (logsPending?.id === id) logsPending = null;
      resolve(null);
    }, timeoutMs);
    logsPending = {
      id,
      done: (lines) => {
        clearTimeout(timer);
        resolve(lines);
      },
    };
    postToLauncher({ type: "maktaba:logs", id });
  });
}

/** Tests: as if a launcher at `origin` had answered hello. */
export function setLauncherHelloForTest(h: LauncherHello, origin = "null"): void {
  hello = h;
  helloOrigin = origin;
  grantState = h.sign_in ? "waiting" : null;
}

/** Tests: forget the launcher's answer. */
export function resetBridgeForTest(): void {
  hello = null;
  helloOrigin = "";
  helloWaiters.length = 0;
  stateListeners.clear();
  visibilityListeners.clear();
  logsPending = null;
  grantState = null;
  grantWaiters.length = 0;
  if (started) window.removeEventListener("message", onBridgeMessage);
  started = false;
}
