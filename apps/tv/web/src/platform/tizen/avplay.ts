// Story 58.10 — the launcher's player on Samsung: webapis.avplay (research.md §3.2: HTML5
// MSE stops at FHD on Samsung TVs; adaptive streaming, 4K, HDR and passthrough are AVPlay's).
//
//   play     open(url) · setListener · setDisplayRect (the whole screen, under the
//            transparent iframe) · setDisplayMethod letterbox · SET_MODE_4K for a 2160p
//            source · prepareAsync · seekTo(start) · the audio track · play. AVPlay opens
//            HLS (TS or fMP4) and MP4, MKV and TS files by URL; the URL is signed (`?sig=`),
//            since AVPlay cannot set a header per segment.
//   tracks   audio from getTotalTrackInfo, chosen with setSelectTrack('AUDIO', index)
//   subs     Maktaba draws them itself (the platform's renderer reverses Arabic): a sidecar
//            WebVTT is fetched and drawn by the shared subtitle layer, timed by AVPlay's
//            oncurrentplaytime; an embedded track ("embedded:<n>", AVPlay's TEXT track n)
//            arrives as text through onsubtitlechange and is drawn the same way
//   errors   onerror becomes state.error; the UI retries once with force_transcode (58.02),
//            and the launcher says so over the video meanwhile (tizen.error.playback)
//
// The TV's app switcher: hidden, AVPlay is suspended; shown again, restored (Samsung's
// multitasking rule). While AVPlay plays, the page behind the iframe is transparent, since
// the video plane lies beneath the whole web layer.

import { log } from "../../log";
import { IDLE, playableUrl, type PlayRequest, type PlayerAdapter, type PlayerState } from "../../player";
import type { Cue } from "../../subtitles";

/** One entry of webapis.avplay.getTotalTrackInfo(). */
export interface AvplayTrack {
  index: number;
  type: "VIDEO" | "AUDIO" | "TEXT" | string;
  extra_info?: string;
}

export interface AvplayListener {
  onbufferingstart?(): void;
  onbufferingprogress?(percent: number): void;
  onbufferingcomplete?(): void;
  oncurrentplaytime?(ms: number): void;
  onstreamcompleted?(): void;
  onevent?(type: string, data: string): void;
  onerror?(type: string): void;
  onsubtitlechange?(duration: number, text: string, type?: number, attr?: unknown): void;
}

/** The part of webapis.avplay the adapter uses (Samsung's AVPlay API, Tizen 5.5 and up). */
export interface Avplay {
  open(url: string): void;
  close(): void;
  setListener(l: AvplayListener): void;
  setDisplayRect(x: number, y: number, w: number, h: number): void;
  setDisplayMethod(method: string): void;
  setStreamingProperty(name: string, value: string): void;
  prepareAsync(ok: () => void, fail: (e: unknown) => void): void;
  play(): void;
  pause(): void;
  stop(): void;
  seekTo(ms: number, ok?: () => void, fail?: (e: unknown) => void): void;
  getState(): string;
  getDuration(): number;
  getCurrentTime(): number;
  getTotalTrackInfo(): AvplayTrack[];
  setSelectTrack(type: string, index: number): void;
  setSilentSubtitle?(silent: boolean): void;
  suspend?(): void;
  restore?(): void;
}

/** Where subtitles are drawn: the shared SubtitleLayer (apps/tv/web/src/subtitles/). */
export interface SubtitleSink {
  setCues(cues: readonly Cue[], lang?: string | null): void;
  render(timeSec: number): void;
  showText(text: string | null, lang?: string): void;
  clear(): void;
  setScale(scale: number): void;
}

export interface AvplayDeps {
  avplay: Avplay;
  doc: Document;
  /** The element AVPlay draws into: <object type="application/avplayer">. */
  plane: HTMLElement;
  subtitles: SubtitleSink;
  /** WebVTT text to cues (the shared layer's parseVtt). */
  parseVtt(text: string): Cue[];
  /** Fetches a sidecar's text. */
  fetchText(url: string): Promise<string>;
  /** Whether the panel is UHD (4K mode for an adaptive source of unknown height). */
  uhd: boolean;
  /** Says a short notice over the video (an error, and that a converted version follows). */
  notice(kind: "playback-error"): void;
}

/** The screen AVPlay draws on: the launcher's page is 1920 wide on every TV. */
export const SCREEN = { width: 1920, height: 1080 };

const EMBEDDED = /^embedded:(\d+)$/;

export class AvplayPlayer implements PlayerAdapter {
  readonly name = "avplay";
  private loaded = false;
  private buffering = false;
  private ended = false;
  private error: string | null = null;
  private position = 0;
  private duration = 0;
  private subs: { id: string; url: string; lang?: string }[] = [];
  private sub: string | null = null;
  private subToken = 0;
  private suspended = false;
  /** Bumped by every play and stop: a late callback of an earlier play is ignored. */
  private generation = 0;

  constructor(private readonly d: AvplayDeps) {
    d.doc.addEventListener("visibilitychange", () => this.onVisibility(d.doc.hidden));
  }

  async play(req: PlayRequest): Promise<void> {
    if (!playableUrl(req.url)) throw new Error("not an http(s) media URL");
    this.close();
    const gen = ++this.generation;
    const av = this.d.avplay;
    this.loaded = true;
    this.buffering = true;
    this.ended = false;
    this.error = null;
    this.position = req.start_sec && req.start_sec > 0 ? req.start_sec : 0;
    this.duration = 0;
    this.subs = (req.subtitles ?? []).filter((s) => playableUrl(s.url));
    if (req.subtitle_scale) this.d.subtitles.setScale(req.subtitle_scale);
    this.d.plane.hidden = false;
    this.d.doc.body.classList.add("ml-avplay");
    log("player", `avplay open ${req.mode ?? "vod"} ${req.url}`);
    try {
      av.open(req.url);
      av.setListener(this.listener(gen));
      av.setDisplayRect(0, 0, SCREEN.width, SCREEN.height);
      av.setDisplayMethod("PLAYER_DISPLAY_MODE_LETTER_BOX");
      if (this.wants4k(req)) {
        try {
          av.setStreamingProperty("SET_MODE_4K", "TRUE");
        } catch (e) {
          log("player", `avplay: SET_MODE_4K refused: ${String(e)}`);
        }
      }
    } catch (e) {
      this.fail(gen, `open: ${errText(e)}`);
      return;
    }
    await new Promise<void>((resolve) => {
      av.prepareAsync(
        () => {
          if (gen !== this.generation) return resolve();
          this.duration = msToSec(safeNum(() => av.getDuration()));
          if (this.position > 0) av.seekTo(Math.round(this.position * 1000));
          if (typeof req.audio === "number") this.audioTrack(req.audio);
          this.applySubtitle();
          av.play();
          this.buffering = false;
          log("player", `avplay playing, ${Math.round(this.duration)} s`);
          resolve();
        },
        (e) => {
          this.fail(gen, `prepare: ${errText(e)}`);
          resolve();
        }
      );
    });
  }

  pause(): void {
    if (this.loaded) this.call("pause", () => this.d.avplay.pause());
  }

  resume(): void {
    if (this.loaded) this.call("play", () => this.d.avplay.play());
  }

  seek(sec: number): void {
    if (!this.loaded || !Number.isFinite(sec) || sec < 0) return;
    const gen = this.generation;
    this.buffering = true;
    this.position = sec;
    this.d.subtitles.clear();
    this.call("seekTo", () =>
      this.d.avplay.seekTo(
        Math.round(sec * 1000),
        () => {
          if (gen === this.generation) this.buffering = false;
        },
        (e) => {
          if (gen !== this.generation) return;
          this.buffering = false;
          log("player", `avplay seek failed: ${errText(e)}`);
        }
      )
    );
  }

  stop(): void {
    if (!this.loaded) return;
    this.generation++;
    this.close();
  }

  /** Index i among the stream's audio tracks (the UI's numbering), as AVPlay's track index. */
  audioTrack(i: number): void {
    if (!this.loaded) return;
    const audio = this.tracks().filter((t) => t.type === "AUDIO");
    const t = audio[i];
    if (!t) {
      log("player", `avplay: no audio track ${i} (of ${audio.length})`);
      return;
    }
    this.call("setSelectTrack", () => this.d.avplay.setSelectTrack("AUDIO", t.index));
  }

  subtitle(id: string | null): void {
    this.sub = id;
    if (this.loaded) this.applySubtitle();
  }

  subtitleScale(scale: number): void {
    this.d.subtitles.setScale(scale);
  }

  active(): boolean {
    return this.loaded;
  }

  state(): PlayerState {
    if (!this.loaded) return { ...IDLE, error: this.error };
    return {
      position: this.position,
      duration: this.duration,
      buffering: this.buffering,
      ended: this.ended,
      error: this.error,
    };
  }

  // ---- inside ------------------------------------------------------------------------

  private listener(gen: number): AvplayListener {
    const mine = () => gen === this.generation;
    return {
      onbufferingstart: () => {
        if (mine()) this.buffering = true;
      },
      onbufferingcomplete: () => {
        if (mine()) this.buffering = false;
      },
      oncurrentplaytime: (ms) => {
        if (!mine()) return;
        this.position = msToSec(ms);
        if (this.sidecar()) this.d.subtitles.render(this.position);
      },
      onstreamcompleted: () => {
        if (!mine()) return;
        this.ended = true;
        this.buffering = false;
        this.d.subtitles.clear();
        log("player", "avplay: ended");
      },
      onerror: (type) => this.fail(gen, String(type)),
      onevent: (type, data) => {
        if (mine()) log("player", `avplay event ${type} ${String(data).slice(0, 120)}`);
      },
      onsubtitlechange: (_duration, text) => {
        if (!mine() || !this.sub || !EMBEDDED.test(this.sub)) return;
        this.d.subtitles.showText(text ? String(text) : null);
      },
    };
  }

  /** The 4K mode: a 2160p source, or an adaptive one of unknown height on a UHD panel. */
  private wants4k(req: PlayRequest): boolean {
    if (typeof req.height === "number") return req.height >= 2160;
    return this.d.uhd && /\.(m3u8|mpd)(\?|$)/i.test(req.url);
  }

  private sidecar(): { id: string; url: string; lang?: string } | null {
    if (!this.sub) return null;
    return this.subs.find((s) => s.id === this.sub) ?? null;
  }

  private applySubtitle(): void {
    const token = ++this.subToken;
    const av = this.d.avplay;
    this.d.subtitles.clear();
    this.d.subtitles.setCues([]);
    const m = this.sub ? EMBEDDED.exec(this.sub) : null;
    if (m) {
      const index = Number(m[1]);
      this.call("setSilentSubtitle", () => av.setSilentSubtitle?.(false));
      this.call("setSelectTrack TEXT", () => av.setSelectTrack("TEXT", index));
      return;
    }
    // no embedded text: AVPlay's own subtitle events stay off
    this.call("setSilentSubtitle", () => av.setSilentSubtitle?.(true));
    const s = this.sidecar();
    if (!s) return;
    this.d.fetchText(s.url).then(
      (text) => {
        if (token !== this.subToken) return;
        const cues = this.d.parseVtt(text);
        this.d.subtitles.setCues(cues, s.lang);
        this.d.subtitles.render(this.position);
        log("player", `subtitles ${s.id}: ${cues.length} cues`);
      },
      (e: unknown) => {
        if (token === this.subToken) log("player", `subtitles ${s.id} failed: ${errText(e)}`);
      }
    );
  }

  private fail(gen: number, what: string): void {
    if (gen !== this.generation) return;
    this.error = `avplay ${what}`;
    this.buffering = false;
    log("player", this.error);
    this.d.notice("playback-error");
  }

  private close(): void {
    const av = this.d.avplay;
    const was = this.loaded;
    this.loaded = false;
    this.buffering = false;
    this.subToken++;
    this.d.subtitles.clear();
    this.d.plane.hidden = true;
    this.d.doc.body.classList.remove("ml-avplay");
    if (!was) return;
    const state = safeStr(() => av.getState());
    if (state !== "NONE" && state !== "IDLE") this.call("stop", () => av.stop());
    this.call("close", () => av.close());
  }

  private onVisibility(hidden: boolean): void {
    if (!this.loaded) return;
    if (hidden && !this.suspended) {
      this.suspended = true;
      this.call("suspend", () => this.d.avplay.suspend?.());
    } else if (!hidden && this.suspended) {
      this.suspended = false;
      this.call("restore", () => this.d.avplay.restore?.());
    }
  }

  private tracks(): AvplayTrack[] {
    try {
      return this.d.avplay.getTotalTrackInfo() || [];
    } catch (e) {
      log("player", `avplay getTotalTrackInfo failed: ${errText(e)}`);
      return [];
    }
  }

  private call(what: string, fn: () => void): void {
    try {
      fn();
    } catch (e) {
      log("player", `avplay ${what} failed: ${errText(e)}`);
    }
  }
}

function msToSec(ms: number): number {
  return Number.isFinite(ms) && ms > 0 ? ms / 1000 : 0;
}

function safeNum(fn: () => number): number {
  try {
    return Number(fn());
  } catch {
    return 0;
  }
}

function safeStr(fn: () => string): string {
  try {
    return String(fn());
  } catch {
    return "";
  }
}

/** AVPlay throws WebAPIException objects ({name, message}), not Errors. */
function errText(e: unknown): string {
  if (e && typeof e === "object") {
    const o = e as { name?: unknown; message?: unknown };
    if (o.name || o.message) return `${String(o.name ?? "")} ${String(o.message ?? "")}`.trim();
  }
  return String(e);
}
