// Story 58.11 — LG's player: the TV's own <video> in the launcher, under the iframe. webOS 5
// and later play HLS v7 natively (TS and fMP4 segments) and MP4 and MKV files by URL, with
// HDR and passthrough handled by the TV's media pipeline, so the signed URL (?sig=, no
// header) is set straight on the element: no hls.js, no MSE (research.md §4.2).
//
//   - Start position: set once the metadata is in (webOS ignores an earlier currentTime).
//   - Speed: webOS plays only at 1.0, so the rate is held there (and the UI shows no speed
//     menu on any TV launcher).
//   - Audio: the element's audioTracks, the one at the index enabled.
//   - Subtitles: Maktaba draws them (layer.ts), Arabic right to left. A sidecar file from
//     play.subtitles is fetched and its cues drawn by the clock; a WebVTT rendition of the
//     manifest (story 48.4) reaches the element as a text track, kept hidden, whose active
//     cues are drawn. The TV's own subtitle renderer never shows.

import { log } from "../log";
import {
  IDLE,
  playableUrl,
  type AudioTrackInfo,
  type PlayRequest,
  type PlayerAdapter,
  type PlayerState,
} from "../player";
import { SubtitleLayer, loadVtt, type Cue } from "../subtitles";

type FetchText = (url: string) => Promise<Response>;

interface AudioTrackList {
  length: number;
  [i: number]: { enabled: boolean; language?: string; label?: string };
}

export class WebOSPlayer implements PlayerAdapter {
  readonly name = "webos-video";
  private loaded = false;
  private buffering = false;
  private error: string | null = null;
  private req: PlayRequest | null = null;
  private subtitleId: string | null = null;
  private cueTrack: TextTrack | null = null;
  private readonly sidecars = new Map<string, Promise<Cue[]>>();
  private pendingStart = 0;
  private pendingAudio: number | null = null;
  readonly layer: SubtitleLayer;

  constructor(
    private readonly video: HTMLVideoElement,
    private readonly fetchText: FetchText,
    layer?: SubtitleLayer
  ) {
    this.layer = layer ?? new SubtitleLayer(video.parentElement ?? video.ownerDocument.body);
    video.addEventListener("waiting", () => (this.buffering = true));
    video.addEventListener("playing", () => (this.buffering = false));
    video.addEventListener("canplay", () => (this.buffering = false));
    video.addEventListener("loadedmetadata", () => this.onMetadata());
    video.addEventListener("timeupdate", () => this.layer.render(video.currentTime));
    video.addEventListener("seeked", () => this.layer.render(video.currentTime));
    video.addEventListener("ratechange", () => {
      if (video.playbackRate !== 1) video.playbackRate = 1;
    });
    video.addEventListener("error", () => {
      this.error = video.error ? `webos media error ${video.error.code}` : "webos media error";
      log("player", `webos: ${this.error}`);
    });
    // a manifest's text tracks are never drawn by the TV
    video.textTracks?.addEventListener?.("addtrack", () => this.hideTextTracks());
  }

  async play(req: PlayRequest): Promise<void> {
    if (!playableUrl(req.url)) throw new Error("not an http(s) media URL");
    this.stop();
    this.req = req;
    this.error = null;
    this.loaded = true;
    this.buffering = true;
    this.pendingStart = req.start_sec && req.start_sec > 0 ? req.start_sec : 0;
    this.pendingAudio = typeof req.audio === "number" ? req.audio : null;
    if (typeof req.subtitle_scale === "number") this.layer.setScale(req.subtitle_scale);
    const v = this.video;
    while (v.firstChild) v.removeChild(v.firstChild);
    v.src = req.url;
    v.playbackRate = 1;
    v.hidden = false;
    log(
      "player",
      `webos play ${req.mode ?? "vod"} ${req.url}${this.pendingStart ? ` at ${this.pendingStart}` : ""}`
    );
    try {
      await v.play();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
      log("player", `webos play failed: ${this.error}`);
    }
  }

  private onMetadata(): void {
    if (!this.loaded) return;
    if (this.pendingStart > 0) {
      this.video.currentTime = this.pendingStart;
      this.pendingStart = 0;
    }
    if (this.pendingAudio !== null) {
      this.audioTrack(this.pendingAudio);
      this.pendingAudio = null;
    }
    this.hideTextTracks();
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
    if (!Number.isFinite(sec) || sec < 0) return;
    if (this.video.readyState < 1) this.pendingStart = sec;
    else this.video.currentTime = sec;
  }

  stop(): void {
    this.detachCueTrack();
    this.subtitleId = null;
    this.layer.clear();
    if (!this.loaded) return;
    const v = this.video;
    v.pause();
    v.removeAttribute("src");
    v.load();
    v.hidden = true;
    this.loaded = false;
    this.buffering = false;
    this.req = null;
    this.sidecars.clear();
  }

  audioTrack(index: number): void {
    const tracks = (this.video as HTMLVideoElement & { audioTracks?: AudioTrackList }).audioTracks;
    if (!tracks || tracks.length === 0) {
      log("player", "webos: no audio track list on this TV");
      return;
    }
    if (index < 0 || index >= tracks.length) return;
    for (let i = 0; i < tracks.length; i++) tracks[i]!.enabled = i === index;
    log("player", `webos audio track ${index}`);
  }

  subtitle(id: string | null): void {
    this.subtitleId = id;
    this.detachCueTrack();
    this.layer.clear();
    if (id === null) return;
    const side = this.req?.subtitles?.find((s) => s.id === id);
    if (side && playableUrl(side.url)) {
      let cues = this.sidecars.get(id);
      if (!cues) {
        cues = loadVtt(side.url, this.fetchText, (why) =>
          log("player", `webos subtitles ${id}: ${why}`)
        );
        this.sidecars.set(id, cues);
      }
      void cues.then((c) => {
        if (this.subtitleId !== id) return; // another choice since
        this.layer.setCues(c, side.lang ?? null);
        this.layer.render(this.video.currentTime);
      });
      return;
    }
    // a rendition of the manifest: its text track, hidden, its cues drawn by Maktaba
    const tracks = this.video.textTracks;
    for (let i = 0; i < (tracks?.length ?? 0); i++) {
      const t = tracks[i]!;
      if (t.id === id || t.language === id || t.label === id) {
        this.attachCueTrack(t);
        return;
      }
    }
    log("player", `webos: no subtitle track ${id}`);
  }

  subtitleScale(n: number): void {
    this.layer.setScale(n);
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
      ...(this.audioTracks().length > 0 ? { audio_tracks: this.audioTracks() } : {}),
    };
  }

  /** The element's audio tracks as the UI lists them. */
  private audioTracks(): AudioTrackInfo[] {
    const tracks = (this.video as HTMLVideoElement & { audioTracks?: AudioTrackList }).audioTracks;
    const out: AudioTrackInfo[] = [];
    for (let i = 0; i < (tracks?.length ?? 0); i++) {
      const t = tracks![i]!;
      out.push({
        enabled: !!t.enabled,
        ...(t.language ? { lang: t.language } : {}),
        ...(t.label ? { label: t.label } : {}),
      });
    }
    return out;
  }

  private hideTextTracks(): void {
    const tracks = this.video.textTracks;
    for (let i = 0; i < (tracks?.length ?? 0); i++) {
      const t = tracks[i]!;
      if (t.mode === "showing") t.mode = t === this.cueTrack ? "hidden" : "disabled";
    }
  }

  private readonly onCueChange = () => {
    const t = this.cueTrack;
    if (!t) return;
    const active = t.activeCues;
    const texts: string[] = [];
    for (let i = 0; i < (active?.length ?? 0); i++) {
      const c = active![i] as TextTrackCue & { text?: string };
      if (c.text) texts.push(c.text);
    }
    this.layer.showText(texts.length ? texts.join("\n") : null, t.language || null);
  };

  private attachCueTrack(t: TextTrack): void {
    this.cueTrack = t;
    t.mode = "hidden";
    t.addEventListener("cuechange", this.onCueChange);
    this.onCueChange();
  }

  private detachCueTrack(): void {
    const t = this.cueTrack;
    if (!t) return;
    t.removeEventListener("cuechange", this.onCueChange);
    t.mode = "disabled";
    this.cueTrack = null;
  }
}
