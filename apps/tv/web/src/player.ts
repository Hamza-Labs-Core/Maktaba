// Story 58.09 — the player the bridge drives. The launcher owns video playback: the UI in
// the iframe asks with `play`, `pause`, `seek`… and hears `state` every 500 ms while
// playing. The video plane sits under the transparent iframe.
//
// PlayerAdapter is the seam: Samsung's AVPlay adapter is story 58.10's, LG's native <video>
// HLS adapter story 58.11's (each draws Maktaba's own subtitles, research.md §3.2). The
// VideoElementPlayer here plays in an HTML <video>: the browser platform (development and
// the tests) uses it, and it is where 58.11 starts.

import { log } from "./log";

export interface SubtitleTrack {
  id: string;
  url: string;
  lang?: string;
  label?: string;
}

/** The UI's `play` message. */
export interface PlayRequest {
  /** A signed media URL (story 58.02: no header reaches a TV player). */
  url: string;
  start_sec?: number;
  subtitles?: SubtitleTrack[];
  /** The audio track's index (null: the stream's default). */
  audio?: number | null;
  mode?: "vod" | "live";
  /** The source's height, when the UI knows it (AVPlay turns on its 4K mode for 2160). */
  height?: number;
  /** The person's subtitle size, a factor of the default (/tv/'s SUBTITLE_SCALES). */
  subtitle_scale?: number;
}

/** The launcher's `state` message. */
export interface PlayerState {
  position: number;
  duration: number;
  buffering: boolean;
  ended: boolean;
  error: string | null;
  /** The stream's audio tracks, when the player can list them (additive, bridge_level 1). */
  audio_tracks?: AudioTrackInfo[];
}

/** One audio track of the stream: the UI's Audio panel lists them, by index. */
export interface AudioTrackInfo {
  enabled: boolean;
  lang?: string;
  label?: string;
}

export interface PlayerAdapter {
  readonly name: string;
  play(req: PlayRequest): Promise<void>;
  pause(): void;
  resume(): void;
  seek(sec: number): void;
  stop(): void;
  audioTrack(index: number): void;
  subtitle(id: string | null): void;
  /** The person's subtitle size, when the adapter draws subtitles itself. */
  subtitleScale?(scale: number): void;
  /** Whether something is loaded (state is sent every 500 ms while it is). */
  active(): boolean;
  state(): PlayerState;
}

export const IDLE: PlayerState = {
  position: 0,
  duration: 0,
  buffering: false,
  ended: false,
  error: null,
};

/** Only http(s) media: a `play` naming javascript:, data: or file: is refused. */
export function playableUrl(url: unknown): url is string {
  if (typeof url !== "string") return false;
  try {
    const u = new URL(url);
    return u.protocol === "https:" || u.protocol === "http:";
  } catch {
    return false;
  }
}

/** Plays in an HTML <video> (the video plane under the iframe). */
export class VideoElementPlayer implements PlayerAdapter {
  readonly name = "video-element";
  private loaded = false;
  private buffering = false;
  private error: string | null = null;

  constructor(private readonly video: HTMLVideoElement) {
    video.addEventListener("waiting", () => (this.buffering = true));
    video.addEventListener("playing", () => (this.buffering = false));
    video.addEventListener("canplay", () => (this.buffering = false));
    video.addEventListener("error", () => {
      this.error = video.error ? `media error ${video.error.code}` : "media error";
      log("player", `video: ${this.error}`);
    });
  }

  async play(req: PlayRequest): Promise<void> {
    if (!playableUrl(req.url)) throw new Error("not an http(s) media URL");
    this.error = null;
    this.loaded = true;
    this.buffering = true;
    const v = this.video;
    while (v.firstChild) v.removeChild(v.firstChild);
    for (const s of req.subtitles ?? []) {
      if (!playableUrl(s.url)) continue;
      const tr = document.createElement("track");
      tr.kind = "subtitles";
      tr.src = s.url;
      tr.id = s.id;
      if (s.lang) tr.srclang = s.lang;
      if (s.label) tr.label = s.label;
      v.appendChild(tr);
    }
    v.src = req.url;
    v.hidden = false;
    if (req.start_sec && req.start_sec > 0) v.currentTime = req.start_sec;
    log("player", `play ${req.mode ?? "vod"} ${req.url}`);
    try {
      await v.play();
    } catch (e) {
      // autoplay refused or the source failed: the state says so
      this.error = e instanceof Error ? e.message : String(e);
      log("player", `play failed: ${this.error}`);
    }
  }

  pause(): void {
    this.video.pause();
  }

  resume(): void {
    void this.video.play().catch((e: unknown) => {
      this.error = e instanceof Error ? e.message : String(e);
    });
  }

  seek(sec: number): void {
    if (Number.isFinite(sec) && sec >= 0) this.video.currentTime = sec;
  }

  stop(): void {
    if (!this.loaded) return;
    const v = this.video;
    v.pause();
    v.removeAttribute("src");
    v.load();
    v.hidden = true;
    this.loaded = false;
    this.buffering = false;
  }

  audioTrack(index: number): void {
    const tracks = (
      this.video as HTMLVideoElement & {
        audioTracks?: { length: number; [i: number]: { enabled: boolean } };
      }
    ).audioTracks;
    if (!tracks) return;
    for (let i = 0; i < tracks.length; i++) tracks[i]!.enabled = i === index;
  }

  subtitle(id: string | null): void {
    const tracks = this.video.textTracks;
    const els = Array.from(this.video.querySelectorAll("track"));
    for (let i = 0; i < tracks.length; i++) {
      tracks[i]!.mode = els[i] && els[i]!.id === id ? "showing" : "disabled";
    }
  }

  active(): boolean {
    return this.loaded;
  }

  state(): PlayerState {
    const v = this.video;
    if (!this.loaded) return { ...IDLE, error: this.error };
    return {
      position: Number.isFinite(v.currentTime) ? v.currentTime : 0,
      duration: Number.isFinite(v.duration) ? v.duration : 0,
      buffering: this.buffering,
      ended: v.ended,
      error: this.error,
    };
  }
}
