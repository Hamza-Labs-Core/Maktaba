// The TV player's input map (Epic 34 story 34.6 AC1, design §8.2): keyboard,
// TV remotes (CH+/CH−, D-pad, number keys) and on-screen buttons all end in
// the same actions. "Up = next channel" holds in RTL too: channel order is
// numeric, not a reading direction, so nothing is mirrored.

export type TvAction =
  | { type: "next" }
  | { type: "prev" }
  | { type: "digit"; digit: string }
  | { type: "enter" }
  | { type: "last" }
  | { type: "guide" }
  | { type: "info" }
  | { type: "escape" };

/** The parts of a KeyboardEvent the map reads. */
export interface KeyLike {
  key: string;
  keyCode?: number;
  code?: string;
  altKey?: boolean;
  ctrlKey?: boolean;
  metaKey?: boolean;
}

// TV platforms that report remote keys by code only (Tizen, webOS, Android
// TV WebViews, HbbTV): CH+/CH−, back, info, guide, and the number keys.
const TV_KEY_CODES: Record<number, TvAction> = {
  427: { type: "next" }, // Tizen ChannelUp
  428: { type: "prev" }, // Tizen ChannelDown
  166: { type: "next" }, // Android KEYCODE_CHANNEL_UP
  167: { type: "prev" }, // Android KEYCODE_CHANNEL_DOWN
  457: { type: "info" }, // Tizen Info / webOS Info
  458: { type: "guide" }, // Tizen Guide
  10009: { type: "escape" }, // Tizen Return
  461: { type: "escape" }, // webOS Back
};

const KEYS: Record<string, TvAction> = {
  ArrowUp: { type: "next" },
  PageUp: { type: "next" },
  ChannelUp: { type: "next" },
  MediaTrackNext: { type: "next" },
  ArrowDown: { type: "prev" },
  PageDown: { type: "prev" },
  ChannelDown: { type: "prev" },
  MediaTrackPrevious: { type: "prev" },
  Enter: { type: "enter" },
  Backspace: { type: "last" },
  l: { type: "last" },
  L: { type: "last" },
  g: { type: "guide" },
  G: { type: "guide" },
  Guide: { type: "guide" },
  i: { type: "info" },
  I: { type: "info" },
  Info: { type: "info" },
  Escape: { type: "escape" },
  BrowserBack: { type: "escape" },
};

/** The action of a key press, or null when the player leaves the key alone. */
export function keyToAction(e: KeyLike): TvAction | null {
  if (e.ctrlKey || e.metaKey || e.altKey) return null; // browser shortcuts stay the browser's
  if (/^[0-9]$/.test(e.key)) return { type: "digit", digit: e.key };
  // Numpad digits with NumLock off still carry their code
  const np = /^Numpad([0-9])$/.exec(e.code ?? "");
  if (np) return { type: "digit", digit: np[1] };
  const byKey = KEYS[e.key];
  if (byKey) return byKey;
  if (e.keyCode !== undefined) {
    if (TV_KEY_CODES[e.keyCode]) return TV_KEY_CODES[e.keyCode];
    // remote number keys reported by code only (48-57)
    if (e.keyCode >= 48 && e.keyCode <= 57 && (e.key === "" || e.key === "Unidentified")) {
      return { type: "digit", digit: String(e.keyCode - 48) };
    }
  }
  return null;
}

/** Whether a key press comes from a text field (the player then ignores it). */
export function fromTextField(target: EventTarget | null): boolean {
  if (!target || typeof (target as HTMLElement).tagName !== "string") return false;
  const el = target as HTMLElement;
  const tag = el.tagName.toLowerCase();
  return tag === "input" || tag === "textarea" || tag === "select" || el.isContentEditable === true;
}
