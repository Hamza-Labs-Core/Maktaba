// Epic 58 story 58.08 (Epic 56 §6.5) — what a focused card does with the
// remote's other keys:
//
//   - Play/Pause plays it at once: a video (resuming when started), a series'
//     Continue, a collection's or a playlist's Play;
//   - a long OK, or the menu key, opens its actions (Play next, Add to queue,
//     Add to playlist, Go to series, Mark watched / unwatched).
//
// A card registers what it can do on its element (cardRef); the frame looks
// the focused element up when one of those keys comes. The registry is a
// WeakMap: an entry lives as long as its element.

export type CardKind = "video" | "series" | "collection" | "playlist";

export interface CardActions {
  kind: CardKind;
  /** the card's name, for the actions' title */
  title: string;
  /** Play/Pause on the card */
  play: () => void;
  /** a video's id: Play next, Add to queue, Add to playlist, Mark watched */
  videoId?: string;
  /** a video's series: Go to series */
  seriesId?: string | null;
  /** whether the video is watched (the menu offers the other) */
  watched?: boolean;
}

const registry = new WeakMap<Element, CardActions>();

/** What the element (a focused card) can do, if it is a card. */
export function cardOf(el: Element | null): CardActions | null {
  return (el && registry.get(el)) ?? null;
}

/** Whether a card has actions for its menu (a video has; the others play only). */
export function hasMenu(c: CardActions | null): c is CardActions {
  return !!c && c.kind === "video" && !!c.videoId;
}

/** A ref callback that registers the card's actions on its element (the latest render's). */
export function cardRef(actions: CardActions | null) {
  return (el: HTMLElement | null) => {
    if (el && actions) registry.set(el, actions);
  };
}

/** How long OK must be held to open a card's actions. */
export const LONG_PRESS_MS = 500;

/**
 * The long-press of OK, as a state machine the frame feeds with key downs,
 * key ups and a timer: a short press clicks, a long one opens the menu.
 */
export class LongPress {
  private timer: ReturnType<typeof setTimeout> | null = null;
  private fired = false;

  constructor(
    private readonly onShort: () => void,
    private readonly onLong: () => void,
    private readonly ms = LONG_PRESS_MS
  ) {}

  /** OK went down (a repeat while held changes nothing). */
  down(repeat: boolean) {
    if (repeat || this.timer) return;
    this.fired = false;
    this.timer = setTimeout(() => {
      this.timer = null;
      this.fired = true;
      this.onLong();
    }, this.ms);
  }

  /** OK came up: a short press clicks; after a long one nothing more happens. */
  up() {
    if (this.timer) {
      clearTimeout(this.timer);
      this.timer = null;
      this.onShort();
    }
    this.fired = false;
  }

  /** Whether a press is under way (its key up belongs to it). */
  get pending(): boolean {
    return this.timer !== null || this.fired;
  }

  cancel() {
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
    this.fired = false;
  }
}
