// Epic 46 — a key press as a big-screen action (pure).
//
// Arrows move; Enter selects; Escape, Backspace (outside a text field), the
// browser's Back key and a TV remote's GoBack go back; the media keys drive
// the player; "/" opens Search; the context-menu key is "menu". Space plays /
// pauses in the player and is left to the browser elsewhere (it presses the
// focused button). A key held with Ctrl, Cmd or Alt is the browser's or the
// app's own shortcut, and an IME composition keystroke is never an action.
import type { BigAction } from "./actions";

export interface KeyLike {
  key: string;
  code?: string;
  ctrlKey?: boolean;
  metaKey?: boolean;
  altKey?: boolean;
  isComposing?: boolean;
  keyCode?: number;
}

export interface KeyContext {
  /** The key went to a text field (Search's): letters, Backspace, Space and arrows are its. */
  typing?: boolean;
  /** The player is on screen: Space plays and pauses. */
  player?: boolean;
}

const MEDIA: Record<string, BigAction> = {
  MediaPlayPause: "playPause",
  MediaPlay: "play",
  MediaPause: "pause",
  MediaStop: "pause",
  MediaTrackNext: "next",
  MediaTrackPrevious: "previous",
  MediaFastForward: "seekForward",
  MediaRewind: "seekBack",
};

const ARROWS: Record<string, BigAction> = {
  ArrowUp: "up",
  ArrowDown: "down",
  ArrowLeft: "left",
  ArrowRight: "right",
};

export function keyAction(e: KeyLike, ctx: KeyContext = {}): BigAction | null {
  if (e.isComposing || e.keyCode === 229) return null;
  if (e.ctrlKey || e.metaKey || e.altKey) return null;
  const k = e.key;
  if (k in MEDIA) return MEDIA[k]!;
  if (k === "BrowserBack" || k === "GoBack" || k === "Escape") return "back";
  if (ctx.typing) {
    // a text field keeps its keys; up / down still leave it, Enter submits there
    if (k === "ArrowUp" || k === "ArrowDown") return ARROWS[k]!;
    return null;
  }
  if (k in ARROWS) return ARROWS[k]!;
  if (k === "Enter" || k === "NumpadEnter" || e.code === "NumpadEnter" || k === "Select")
    return "select";
  if (k === "Backspace") return "back";
  if (k === " " || k === "Spacebar") return ctx.player ? "playPause" : null;
  if (k === "/") return "search";
  if (k === "ContextMenu") return "menu";
  return null;
}
