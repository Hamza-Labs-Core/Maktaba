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
//   /tv/ → launcher   maktaba:ready                once /tv/ listens
//                     maktaba:hello {id}           what are you? (bridge_level 1)
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
  device_caps: { video: string[]; audio: string[]; hdr: string[]; max_height: number };
  keys: string[];
  messages: string[];
}

/** The launcher's player, as its state messages say. */
export interface LauncherPlayerState {
  position: number;
  duration: number;
  buffering: boolean;
  ended: boolean;
  error: string | null;
}

/** /tv/ → launcher messages (bridge.schema.json's uiToLauncher). */
export type UiMessage =
  | { type: "maktaba:ready" }
  | { type: "maktaba:hello"; id: number; bridge_level: number }
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
    }
  | { type: "maktaba:pause" }
  | { type: "maktaba:resume" }
  | { type: "maktaba:seek"; sec: number }
  | { type: "maktaba:stop" }
  | { type: "maktaba:audioTrack"; i: number }
  | { type: "maktaba:subtitle"; id: string | null }
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

/** Sends a message to the launcher (no-op outside one). */
export function postToLauncher(msg: UiMessage): void {
  if (!inLauncher()) return;
  // the launcher is a packaged app (its origin is the TV's own): any target
  window.parent.postMessage(msg, "*");
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
let nextId = 1;
const helloWaiters: ((h: LauncherHello | null) => void)[] = [];
const stateListeners = new Set<(s: LauncherPlayerState) => void>();
const visibilityListeners = new Set<(hidden: boolean) => void>();
const logWaiters = new Map<number, (lines: string | null) => void>();
let started = false;

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
      hello = m as unknown as LauncherHello;
      helloWaiters.splice(0).forEach((f) => f(hello));
      return;
    case "maktaba:state":
      stateListeners.forEach((f) => f(m as unknown as LauncherPlayerState));
      return;
    case "maktaba:visibility":
      if (typeof m.hidden === "boolean") visibilityListeners.forEach((f) => f(m.hidden as boolean));
      return;
    case "maktaba:logs": {
      const w = typeof m.re === "number" ? logWaiters.get(m.re) : undefined;
      if (w && typeof m.lines === "string") {
        logWaiters.delete(m.re as number);
        w(m.lines);
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
    postToLauncher({ type: "maktaba:hello", id: nextId++, bridge_level: BRIDGE_LEVEL });
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
  return new Promise((resolve) => {
    const timer = setTimeout(() => {
      logWaiters.delete(id);
      resolve(null);
    }, timeoutMs);
    logWaiters.set(id, (lines) => {
      clearTimeout(timer);
      resolve(lines);
    });
    postToLauncher({ type: "maktaba:logs", id });
  });
}

/** Tests: forget the launcher's answer. */
export function resetBridgeForTest(): void {
  hello = null;
  helloWaiters.length = 0;
  stateListeners.clear();
  visibilityListeners.clear();
  logWaiters.clear();
  if (started) window.removeEventListener("message", onBridgeMessage);
  started = false;
}
