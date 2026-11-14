// Epic 58 story 58.08 — the launcher's bridge as an input source (the seam
// story 58.09 builds on).
//
// The Samsung and LG launcher loads /tv/ in an iframe and talks to it with
// postMessage. Keys pressed while the iframe has focus come to /tv/ as its own
// keydown events (keys.ts maps the TVs' key codes); the bridge carries what a
// launcher sees first (a key it registered with tizen.tvinputdevice, say) as:
//
//   launcher → /tv/   { type: "maktaba:action", action: <BigAction> }
//   /tv/ → launcher   { type: "maktaba:exit" }        after Leave Maktaba?
//                     { type: "maktaba:ready" }       once /tv/ listens
//
// Only the parent window is listened to, and only from a launcher's origin: a
// packaged TV app (Tizen and webOS load theirs from file:// or app://, which a
// message reports as "null", "file://" or "app://…") or the home itself. A web
// page that frames /tv/ is not a launcher (and, its session cookie being
// SameSite=Lax, such a frame is signed out anyway). Only a known action is
// taken: a message is a key press, never a command with arguments.
import type { BigAction } from "./actions";

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

/** Tells the launcher something (no-op outside one). */
export function tellLauncher(type: typeof BRIDGE_EXIT | typeof BRIDGE_READY) {
  if (!inLauncher()) return;
  // the launcher is a packaged app (its origin is the TV's own): any target
  window.parent.postMessage({ type }, "*");
}

/** Listens to the launcher's actions; returns the way to stop. */
export function listenToLauncher(onAction: (a: BigAction) => void): () => void {
  const onMessage = (e: MessageEvent) => {
    if (e.source !== window.parent || e.source === window) return;
    if (!launcherOrigin(e.origin)) return;
    const a = bridgeAction(e.data);
    if (a) onAction(a);
  };
  window.addEventListener("message", onMessage);
  tellLauncher(BRIDGE_READY);
  return () => window.removeEventListener("message", onMessage);
}
