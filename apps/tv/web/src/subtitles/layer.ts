// Epic 58 — the shared subtitle layer (stories 58.10 and 58.11). Both TVs use it: webOS
// gives it the cues of a sidecar file or of the manifest's WebVTT rendition, Samsung gives it
// AVPlay's onsubtitlechange text. The layer draws them over the video and over the
// transparent iframe, never catching the pointer:
//   - right to left for Arabic (and any RTL language), from the track's language or, when
//     there is none (AVPlay's embedded text), from the text's first strong letter;
//   - sized for ten feet, at the person's size (/tv/'s subtitleScale: 0.75 to 2);
//   - set with textContent only, so nothing in a subtitle file is ever run as markup.

import { cueText, cuesAt, type Cue } from "./vtt";

/** /tv/'s sizes (web/src/bigscreen/prefs.ts SUBTITLE_SCALES): what the UI sends. */
export const SUBTITLE_SCALES = [0.75, 1, 1.25, 1.5, 2] as const;

/** The text height at size 1, in CSS pixels of the 1920×1080 page. */
export const BASE_FONT_PX = 48;

const RTL_LANGS = new Set([
  "ar",
  "arc",
  "ckb",
  "dv",
  "fa",
  "he",
  "iw",
  "ku",
  "ps",
  "sd",
  "ug",
  "ur",
  "yi",
]);

const RTL_CHAR = /[֐-ࣿיִ-﷿ﹰ-ﻼ]/;
const LTR_CHAR = /[A-Za-zÀ-ɏͰ-ϿЀ-ӿ]/;

/** The direction of a subtitle: its language's, else its first strong letter's. */
export function textDir(text: string, lang?: string | null): "rtl" | "ltr" {
  if (lang) {
    const base = lang.toLowerCase().split(/[-_]/)[0]!;
    if (base) return RTL_LANGS.has(base) ? "rtl" : "ltr";
  }
  for (const ch of text) {
    if (RTL_CHAR.test(ch)) return "rtl";
    if (LTR_CHAR.test(ch)) return "ltr";
  }
  return "ltr";
}

/** A size the UI sent, or 1 when it is not one of /tv/'s. */
export function validScale(n: unknown): number {
  return typeof n === "number" && (SUBTITLE_SCALES as readonly number[]).includes(n) ? n : 1;
}

export class SubtitleLayer {
  readonly element: HTMLElement;
  private cues: readonly Cue[] = [];
  private lang: string | null = null;
  private shown = "";
  private scale = 1;

  constructor(host: HTMLElement) {
    const doc = host.ownerDocument;
    this.element = doc.createElement("div");
    this.element.className = "ml-subs";
    this.element.setAttribute("aria-live", "off");
    this.element.setAttribute("data-testid", "subtitles");
    this.element.hidden = true;
    host.appendChild(this.element);
    this.setScale(1);
  }

  /** The cues of a file (a sidecar or a rendition), drawn by render(t). */
  setCues(cues: readonly Cue[], lang?: string | null): void {
    this.cues = cues;
    this.lang = lang ?? null;
    this.draw([]);
  }

  /** Draws the cues showing at t seconds. */
  render(t: number): void {
    if (this.cues.length === 0) return;
    this.draw(cuesAt(this.cues, t).map((c) => c.text));
  }

  /** Draws one text (AVPlay's onsubtitlechange, a track's active cues), its tags removed;
   * null clears. */
  showText(text: string | null, lang?: string | null): void {
    this.cues = [];
    if (lang !== undefined) this.lang = lang;
    const plain = text ? cueText(text) : "";
    this.draw(plain ? [plain] : []);
  }

  /** The person's size (one of SUBTITLE_SCALES; anything else is 1). */
  setScale(n: number): void {
    this.scale = validScale(n);
    this.element.style.fontSize = `${Math.round(BASE_FONT_PX * this.scale)}px`;
  }

  currentScale(): number {
    return this.scale;
  }

  /** Removes every cue and the text shown. */
  clear(): void {
    this.cues = [];
    this.lang = null;
    this.draw([]);
  }

  /** The text shown now ("" when none), lines joined by "\n". */
  text(): string {
    return this.shown;
  }

  private draw(texts: string[]): void {
    const key = texts.join("\n\n");
    if (key === this.shown) return;
    this.shown = key;
    const el = this.element;
    while (el.firstChild) el.removeChild(el.firstChild);
    el.hidden = texts.length === 0;
    const doc = el.ownerDocument;
    for (const t of texts) {
      const cue = doc.createElement("div");
      cue.className = "ml-cue";
      cue.dir = textDir(t, this.lang);
      if (this.lang) cue.lang = this.lang;
      const span = doc.createElement("span");
      span.textContent = t;
      cue.appendChild(span);
      el.appendChild(cue);
    }
  }
}
