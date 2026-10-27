// Epic 46 — the actions big-screen mode understands, whatever sends them
// (the keyboard, a remote that presents itself as one, a game controller,
// the media keys, the desktop tray's Play / Pause).

export type BigAction =
  | "up"
  | "down"
  | "left"
  | "right"
  | "select"
  | "back"
  | "playPause"
  | "play"
  | "pause"
  | "seekBack"
  | "seekForward"
  | "next"
  | "previous"
  | "search"
  | "menu";

export const DIRECTIONS = ["up", "down", "left", "right"] as const;
export type DirectionAction = (typeof DIRECTIONS)[number];

export function isDirection(a: BigAction): a is DirectionAction {
  return (DIRECTIONS as readonly string[]).includes(a);
}

/** The DOM event every source dispatches on window; screens listen for it. */
export const ACTION_EVENT = "maktaba:big-action";

export interface ActionDetail {
  action: BigAction;
  /** "key", "pad" or "media": the player shows its controls for any. */
  source: "key" | "pad" | "media";
}
