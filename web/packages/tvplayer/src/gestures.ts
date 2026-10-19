// Vertical swipe and tap on the video area (Epic 34.6 AC1, design §8.2).
// Swipe up = next channel, swipe down = previous; horizontal moves are left
// to the browser (and to RTL page scrolling). A tap opens the mini-guide.

export const SWIPE_MIN_PX = 50;
export const SWIPE_MAX_MS = 800;
export const TAP_MAX_PX = 10;
export const TAP_MAX_MS = 350;

export type Gesture = "swipe_up" | "swipe_down" | "tap";

/** The gesture of one pointer stroke, or null. */
export function classify(dx: number, dy: number, ms: number): Gesture | null {
  const ax = Math.abs(dx);
  const ay = Math.abs(dy);
  if (ms <= TAP_MAX_MS && ax <= TAP_MAX_PX && ay <= TAP_MAX_PX) return "tap";
  if (ms > SWIPE_MAX_MS || ay < SWIPE_MIN_PX || ay < ax * 1.5) return null;
  return dy < 0 ? "swipe_up" : "swipe_down";
}

interface Start {
  x: number;
  y: number;
  t: number;
  id: number;
}

/**
 * Listens for swipes and taps on el (touch and pen; a mouse click is a tap).
 * Returns the detach function.
 */
export function attachGestures(
  el: HTMLElement,
  onGesture: (g: Gesture) => void,
  now: () => number = () => performance.now()
): () => void {
  let start: Start | null = null;
  const down = (e: PointerEvent) => {
    if (e.button !== undefined && e.button > 0) return;
    start = { x: e.clientX, y: e.clientY, t: now(), id: e.pointerId };
  };
  const up = (e: PointerEvent) => {
    if (!start || e.pointerId !== start.id) return;
    const g = classify(e.clientX - start.x, e.clientY - start.y, now() - start.t);
    start = null;
    if (g) onGesture(g);
  };
  const cancel = () => {
    start = null;
  };
  el.addEventListener("pointerdown", down);
  el.addEventListener("pointerup", up);
  el.addEventListener("pointercancel", cancel);
  return () => {
    el.removeEventListener("pointerdown", down);
    el.removeEventListener("pointerup", up);
    el.removeEventListener("pointercancel", cancel);
  };
}
