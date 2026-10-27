// Epic 46 — game controllers in big-screen mode (pure; the hook in
// usePads.ts polls navigator.getGamepads() and feeds `PadReader.step`).
//
// The W3C "standard" gamepad mapping puts the same buttons at the same
// indices on an Xbox and a PlayStation pad, so one table serves both:
//
//   0 A / ✕        select           8  View / Share     back
//   1 B / ○        back             9  Menu / Options   menu
//   2 X / □        playPause        12-15 D-pad         up down left right
//   3 Y / △        search           left stick (axes 0, 1), dead zone 0.5
//   4 LB / L1      previous         6  LT / L2          seekBack
//   5 RB / R1      next             7  RT / R2          seekForward
//
// A press is an action once, on the way down. A held direction (D-pad or
// stick) repeats after REPEAT_DELAY_MS, every REPEAT_EVERY_MS. The Home /
// PS button (16) is the system's and is ignored.
import type { BigAction } from "./actions";

export const STICK_DEAD_ZONE = 0.5;
export const TRIGGER_THRESHOLD = 0.5;
export const REPEAT_DELAY_MS = 400;
export const REPEAT_EVERY_MS = 120;

export const BUTTON_ACTIONS: ReadonlyArray<readonly [index: number, action: BigAction]> = [
  [0, "select"],
  [1, "back"],
  [2, "playPause"],
  [3, "search"],
  [4, "previous"],
  [5, "next"],
  [6, "seekBack"],
  [7, "seekForward"],
  [8, "back"],
  [9, "menu"],
  [12, "up"],
  [13, "down"],
  [14, "left"],
  [15, "right"],
];

const REPEATS: ReadonlySet<BigAction> = new Set<BigAction>(["up", "down", "left", "right"]);

/** What a pad looks like at one moment: its buttons' values (0..1) and its axes (-1..1). */
export interface PadSnapshot {
  id?: string;
  mapping?: string;
  buttons: ReadonlyArray<number | { pressed: boolean; value: number }>;
  axes: ReadonlyArray<number>;
}

function pressed(b: number | { pressed: boolean; value: number } | undefined, index: number) {
  if (b === undefined) return false;
  if (typeof b === "number") return b >= TRIGGER_THRESHOLD;
  // the triggers are analog: past the threshold counts, whatever `pressed` says
  if (index === 6 || index === 7) return b.pressed || b.value >= TRIGGER_THRESHOLD;
  return b.pressed;
}

/** The actions a pad holds down now (each at most once). */
export function heldActions(pad: PadSnapshot): Set<BigAction> {
  const held = new Set<BigAction>();
  for (const [i, action] of BUTTON_ACTIONS) if (pressed(pad.buttons[i], i)) held.add(action);
  const [x = 0, y = 0] = pad.axes;
  // the stick's stronger axis wins, so a diagonal is one direction
  if (Math.max(Math.abs(x), Math.abs(y)) >= STICK_DEAD_ZONE) {
    if (Math.abs(x) >= Math.abs(y)) held.add(x < 0 ? "left" : "right");
    else held.add(y < 0 ? "up" : "down");
  }
  return held;
}

/**
 * Turns successive snapshots of one pad into actions: a press once on its way
 * down, a held direction again after the repeat delay and then at the repeat
 * rate.
 */
export class PadReader {
  private down = new Map<BigAction, { since: number; last: number }>();

  step(pad: PadSnapshot, now: number): BigAction[] {
    const held = heldActions(pad);
    const out: BigAction[] = [];
    for (const a of held) {
      const was = this.down.get(a);
      if (!was) {
        this.down.set(a, { since: now, last: now });
        out.push(a);
      } else if (
        REPEATS.has(a) &&
        now - was.since >= REPEAT_DELAY_MS &&
        now - was.last >= REPEAT_EVERY_MS
      ) {
        was.last = now;
        out.push(a);
      }
    }
    for (const a of [...this.down.keys()]) if (!held.has(a)) this.down.delete(a);
    return out;
  }

  reset() {
    this.down.clear();
  }
}

export type PadLayout = "xbox" | "playstation" | "generic";

/** Which button names to show for a pad, from its id (the USB vendor or the name). */
export function padLayout(id: string | undefined): PadLayout {
  const s = (id ?? "").toLowerCase();
  // Xbox first: its pads are "Xbox Wireless Controller", and Sony's plain
  // "Wireless Controller" (a DualShock 4's name) would match both
  if (/045e|xbox|xinput|microsoft/.test(s)) return "xbox";
  if (/054c|playstation|dualsense|dualshock|wireless controller/.test(s)) return "playstation";
  return "generic";
}

/** The names of the select and back buttons on that layout. */
export function padGlyphs(layout: PadLayout): { select: string; back: string } {
  return layout === "playstation" ? { select: "✕", back: "○" } : { select: "A", back: "B" };
}
