// The receiver's screens, 10-foot and dark (Ink & Brass): idle with the Maktaba mark, loading
// and buffering, the player overlay (title, series, position, chapter, Up next, the Remote
// badge), paused with the poster and description, the errors with their next step, and the
// "Update the home" screen. Built with textContent only: titles and descriptions are the
// library's, never markup. The layout is logical CSS (static/receiver.css), so dir="rtl"
// mirrors it.

import { dirOf, type Key, type Lang, position, t } from "./i18n";

export type Screen = "idle" | "loading" | "playing" | "paused" | "error" | "update";

export interface NowPlaying {
  title: string;
  subtitle?: string;
  description?: string;
  poster?: string;
  remote: boolean;
}

function el<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  cls: string,
  parent?: HTMLElement
): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  e.className = cls;
  parent?.appendChild(e);
  return e;
}

/** How long the overlay stays after the picture starts or moves (ms). */
export const OVERLAY_MS = 5000;

export class Screens {
  lang: Lang = "en";
  screen: Screen = "idle";
  private readonly box: HTMLElement;
  private readonly idleText: HTMLElement;
  private readonly loadingText: HTMLElement;
  private readonly title: HTMLElement;
  private readonly subtitle: HTMLElement;
  private readonly chapter: HTMLElement;
  private readonly badge: HTMLElement;
  private readonly bar: HTMLElement;
  private readonly pos: HTMLElement;
  private readonly upNext: HTMLElement;
  private readonly upNextLabel: HTMLElement;
  private readonly upNextTitle: HTMLElement;
  private readonly poster: HTMLImageElement;
  private readonly pausedLabel: HTMLElement;
  private readonly pausedTitle: HTMLElement;
  private readonly pausedText: HTMLElement;
  private readonly errorText: HTMLElement;
  private now: NowPlaying | null = null;
  private errorKey: Key = "receiver.error.home";
  private hideTimer: ReturnType<typeof setTimeout> | undefined;

  constructor(
    root: HTMLElement,
    private readonly markURL: string
  ) {
    root.replaceChildren();
    this.box = el("div", "mk", root);

    const idle = el("section", "mk-idle", this.box);
    const mark = el("img", "mk-mark", idle);
    mark.src = this.markURL;
    mark.alt = "";
    this.idleText = el("p", "mk-idle-text", idle);

    const loading = el("section", "mk-loading", this.box);
    el("div", "mk-spinner", loading).setAttribute("aria-hidden", "true");
    this.loadingText = el("p", "mk-loading-text", loading);
    loading.setAttribute("role", "status");

    const overlay = el("section", "mk-overlay", this.box);
    this.badge = el("span", "mk-badge", overlay);
    const info = el("div", "mk-info", overlay);
    this.title = el("h1", "mk-title", info);
    this.subtitle = el("p", "mk-subtitle", info);
    this.chapter = el("p", "mk-chapter", info);
    const progress = el("div", "mk-progress", overlay);
    const track = el("div", "mk-track", progress);
    this.bar = el("span", "mk-bar", track);
    this.pos = el("span", "mk-pos", progress);

    this.upNext = el("aside", "mk-upnext", this.box);
    this.upNextLabel = el("span", "mk-upnext-label", this.upNext);
    this.upNextTitle = el("strong", "mk-upnext-title", this.upNext);

    const paused = el("section", "mk-paused", this.box);
    this.poster = el("img", "mk-poster", paused);
    this.poster.alt = "";
    const pinfo = el("div", "mk-paused-info", paused);
    this.pausedLabel = el("span", "mk-paused-label", pinfo);
    this.pausedTitle = el("h1", "mk-paused-title", pinfo);
    this.pausedText = el("p", "mk-paused-text", pinfo);

    const error = el("section", "mk-error", this.box);
    error.setAttribute("role", "alert");
    this.errorText = el("p", "mk-error-text", error);

    this.setLang("en");
    this.show("idle");
  }

  setLang(lang: Lang): void {
    this.lang = lang;
    const html = document.documentElement;
    html.lang = lang;
    html.dir = dirOf(lang);
    this.idleText.textContent = t(lang, "receiver.idle");
    this.badge.textContent = t(lang, "receiver.remote");
    this.upNextLabel.textContent = t(lang, "receiver.upNext");
    this.pausedLabel.textContent = t(lang, "receiver.paused");
    this.errorText.textContent = t(lang, this.errorKey);
  }

  private show(screen: Screen): void {
    this.screen = screen;
    this.box.dataset.screen = screen;
  }

  idle(): void {
    this.now = null;
    this.hideUpNext();
    this.show("idle");
  }

  loading(title: string): void {
    this.loadingText.textContent = title
      ? t(this.lang, "receiver.loading", title)
      : t(this.lang, "receiver.buffering");
    this.hideUpNext();
    this.show("loading");
  }

  buffering(on: boolean): void {
    this.box.classList.toggle("mk-buffering", on);
  }

  playing(now: NowPlaying): void {
    this.now = now;
    this.title.textContent = now.title;
    this.subtitle.textContent = now.subtitle ?? "";
    this.subtitle.hidden = !now.subtitle;
    this.badge.hidden = !now.remote;
    this.box.classList.toggle("mk-remote", now.remote);
    this.show("playing");
    this.flash();
  }

  /** Shows the overlay for OVERLAY_MS (playback started, resumed or moved). */
  flash(): void {
    this.box.classList.add("mk-show");
    clearTimeout(this.hideTimer);
    this.hideTimer = setTimeout(() => this.box.classList.remove("mk-show"), OVERLAY_MS);
  }

  /** The position, the bar and the chapter. */
  time(at: number, duration: number, chapter?: string): void {
    this.pos.textContent = position(this.lang, at, duration);
    const frac = duration > 0 ? Math.min(1, Math.max(0, at / duration)) : 0;
    this.bar.style.inlineSize = `${(frac * 100).toFixed(2)}%`;
    this.chapter.textContent = chapter ? t(this.lang, "receiver.chapter", chapter) : "";
    this.chapter.hidden = !chapter;
  }

  paused(): void {
    const now = this.now;
    this.pausedTitle.textContent = now?.title ?? "";
    this.pausedText.textContent = now?.description ?? "";
    this.pausedText.hidden = !now?.description;
    if (now?.poster) {
      this.poster.src = now.poster;
      this.poster.hidden = false;
    } else {
      this.poster.removeAttribute("src");
      this.poster.hidden = true;
    }
    this.show("paused");
  }

  /** The poster, when it arrives after the item started (it is fetched beside the load). */
  setPoster(url: string): void {
    if (!this.now) return;
    this.now = { ...this.now, poster: url };
    if (this.screen === "paused") this.paused();
  }

  resumed(): void {
    if (this.now) {
      this.show("playing");
      this.flash();
    }
  }

  showUpNext(title: string): void {
    this.upNextTitle.textContent = title;
    this.upNext.hidden = false;
    this.box.classList.add("mk-has-next");
  }

  hideUpNext(): void {
    this.upNext.hidden = true;
    this.box.classList.remove("mk-has-next");
  }

  error(key: Key): void {
    this.errorKey = key;
    this.errorText.textContent = t(this.lang, key);
    this.hideUpNext();
    this.show("error");
  }

  update(): void {
    this.error("receiver.updateHome");
    this.show("update");
  }
}
