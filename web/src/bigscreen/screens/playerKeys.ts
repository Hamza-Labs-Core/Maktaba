// Epic 46 — what an action does in big screen's player (pure).
//
// With the controls hidden (watching), Left / Right seek 10 s, Enter plays /
// pauses, Up / Down bring the controls back. In Arabic the progress bar runs
// right to left (its start is on the right), so Left is forward there. With
// the controls shown, the arrows and Enter move and press, like everywhere
// else. Whatever is on screen, the media keys, a pad's X and its shoulders and
// triggers act directly.
import type { BigAction } from "../input/actions";

export const SEEK_SHORT = 10;
export const SEEK_LONG = 30;

export type PlayerCommand =
  | { kind: "seek"; by: number }
  | { kind: "toggle" }
  | { kind: "play" }
  | { kind: "pause" }
  | { kind: "nextLesson" }
  | { kind: "restart" }
  | { kind: "show" }
  /** not the player's: the frame moves focus, presses, goes back */
  | null;

export function playerCommand(
  action: BigAction,
  controlsShown: boolean,
  dir: "ltr" | "rtl"
): PlayerCommand {
  switch (action) {
    case "playPause":
      return { kind: "toggle" };
    case "play":
      return { kind: "play" };
    case "pause":
      return { kind: "pause" };
    case "seekBack":
      return { kind: "seek", by: -SEEK_LONG };
    case "seekForward":
      return { kind: "seek", by: SEEK_LONG };
    case "next":
      return { kind: "nextLesson" };
    case "previous":
      return { kind: "restart" };
  }
  if (controlsShown) return null;
  const forward = dir === "rtl" ? "left" : "right";
  const backward = dir === "rtl" ? "right" : "left";
  if (action === forward) return { kind: "seek", by: SEEK_SHORT };
  if (action === backward) return { kind: "seek", by: -SEEK_SHORT };
  if (action === "select") return { kind: "toggle" };
  if (action === "up" || action === "down") return { kind: "show" };
  return null;
}

/** A time `by` seconds from `at`, kept inside the video. */
export function seekTo(at: number, by: number, duration: number): number {
  const end = Number.isFinite(duration) && duration > 0 ? Math.max(0, duration - 1) : Infinity;
  return Math.min(end, Math.max(0, at + by));
}

/** The lesson after `videoId` in `lessons` (in order), or null. */
export function lessonAfter<T extends { video_id: string }>(
  lessons: readonly T[],
  videoId: string
): T | null {
  const i = lessons.findIndex((l) => l.video_id === videoId);
  return i >= 0 && i + 1 < lessons.length ? lessons[i + 1]! : null;
}
