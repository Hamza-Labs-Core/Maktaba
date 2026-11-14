// The receiver's logic (story 58.07), apart from Google's SDK so the tests can drive it with a
// fake (test/fakeCaf.ts): a LOAD from a Maktaba sender carries the home's address and a cast
// grant in its custom data; the receiver finds the home (LAN first, the relay after 3 s), opens
// its own stream session with profile `cast` and the device's report, reports progress every
// 10 s, renews the signed URLs before they end, and walks a play queue.
//
// The grant never leaves the page: the media status CAF sends every sender carries a copy of
// the custom data without it (`publicData`).

import {
  HomeClient,
  HomeError,
  grantExpiry,
  sidecarTracks,
  type Chapter,
  type Queue,
  type Session,
  type SubtitleTrack,
  type Video,
} from "./api";
import { constant, type CafContext, type CafFramework, type CafLoadRequest } from "./caf";
import { deviceCaps, segmentFormat } from "./caps";
import { HomeUnreachable, resolveHome, type ResolvedHome } from "./home";
import { langOf, type Key, type Lang } from "./i18n";
import type { Screens } from "./ui";
import { NAMESPACE } from "../loader/contract";

export { NAMESPACE };
/** Progress goes to the home this often (ms). */
export const PROGRESS_MS = 10_000;
/** The URLs are renewed this long before they end (ms). */
export const RENEW_BEFORE_MS = 120_000;
/** The Up next card shows this long before the end (s). */
export const UP_NEXT_SEC = 30;

/** What a Maktaba sender puts in the load's custom data (58.06). */
export interface CastData {
  home: string;
  urls?: string[];
  grant: string;
  video_id?: string;
  queue_id?: string;
  queue_pos?: number;
  start_sec?: number;
  lang: Lang;
  api_level?: number;
  /** Set by the receiver itself on its retry after a format error. */
  force_transcode?: boolean;
}

/** The custom data of a load, or null when it is not a Maktaba sender's (then the load plays
 * as it came, as on Google's Default Media Receiver). */
export function castData(raw: unknown): CastData | null {
  if (!raw || typeof raw !== "object") return null;
  const r = raw as Record<string, unknown>;
  if (typeof r.home !== "string" || typeof r.grant !== "string" || !r.grant.startsWith("cast_"))
    return null;
  const video = typeof r.video_id === "string" && r.video_id ? r.video_id : undefined;
  const queue = typeof r.queue_id === "string" && r.queue_id ? r.queue_id : undefined;
  if (!video && !queue) return null;
  const num = (v: unknown) =>
    typeof v === "number" && Number.isFinite(v) && v >= 0 ? v : undefined;
  return {
    home: r.home,
    urls: Array.isArray(r.urls)
      ? r.urls.filter((u): u is string => typeof u === "string")
      : undefined,
    grant: r.grant,
    video_id: video,
    queue_id: queue,
    queue_pos: num(r.queue_pos),
    start_sec: num(r.start_sec),
    lang: langOf(r.lang),
    api_level: num(r.api_level),
    force_transcode: r.force_transcode === true ? true : undefined,
  };
}

/** The custom data the media status may carry: no grant, no address. */
export function publicData(
  d: CastData,
  videoId: string,
  queuePos?: number
): Record<string, unknown> {
  const out: Record<string, unknown> = { video_id: videoId, lang: d.lang };
  if (d.queue_id) {
    out.queue_id = d.queue_id;
    if (queuePos !== undefined) out.queue_pos = queuePos;
  }
  return out;
}

export interface ReceiverEnv {
  framework: CafFramework;
  context: CafContext;
  screens: Screens;
  fetch?: (input: string, init?: RequestInit) => Promise<Response>;
  now?: () => number;
  /** A namespace message from a sender (the loader owns the listener: it must be registered
   * before the context starts). */
  onMessage?: (fn: (data: unknown, senderId: string) => void) => void;
  log?: (msg: string, extra?: Record<string, unknown>) => void;
}

interface Current {
  data: CastData;
  client: HomeClient;
  home: ResolvedHome;
  video: Video;
  session: Session;
  chapters: Chapter[];
  queue: Queue | null;
  queuePos: number | undefined;
  sessionExpires: number;
  grantExpires: number;
  retried: boolean;
  renewing: boolean;
}

export class Receiver {
  private readonly fw: CafFramework;
  private readonly ctx: CafContext;
  private readonly ui: Screens;
  private readonly fetchFn: (input: string, init?: RequestInit) => Promise<Response>;
  private readonly now: () => number;
  private readonly log: (msg: string, extra?: Record<string, unknown>) => void;
  private cur: Current | null = null;
  private homeCache: { key: string; home: ResolvedHome } | null = null;
  private timer: ReturnType<typeof setInterval> | undefined;
  private playing = false;
  /** The grant ran out (or stopped reaching this) while an item played: the end of the item
   * shows receiver.error.grant instead of the next one. */
  private grantEnded = false;

  constructor(env: ReceiverEnv) {
    this.fw = env.framework;
    this.ctx = env.context;
    this.ui = env.screens;
    this.fetchFn = env.fetch ?? ((i, init) => fetch(i, init));
    this.now = env.now ?? (() => Date.now());
    this.log = env.log ?? (() => {});
    env.onMessage?.((data, senderId) => this.onMessage(data, senderId));
    this.listen();
  }

  private ev(name: string): string {
    return constant(this.fw.events.EventType, name, name);
  }

  private listen(): void {
    const pm = this.ctx.getPlayerManager();
    pm.addEventListener(this.ev("PLAYING"), () => {
      this.playing = true;
      if (this.ui.screen === "paused") this.ui.resumed();
      else if (this.cur) this.ui.playing(this.nowPlaying(this.cur));
      this.broadcast({ type: "state", state: "playing" });
    });
    pm.addEventListener(this.ev("PAUSE"), () => {
      this.playing = false;
      if (!this.cur) return;
      this.ui.paused();
      void this.report(false);
      this.broadcast({ type: "state", state: "paused" });
    });
    pm.addEventListener(this.ev("BUFFERING"), (e) => this.ui.buffering(e.isBuffering === true));
    pm.addEventListener(this.ev("SEEKED"), () => this.ui.flash());
    pm.addEventListener(this.ev("TIME_UPDATE"), () => this.tick());
    pm.addEventListener(this.ev("MEDIA_FINISHED"), () => void this.finished());
    pm.addEventListener(this.ev("ERROR"), () => void this.playError());
  }

  /** The LOAD interceptor: the request to play, filled in, or an error for the sender. */
  async onLoad(req: CafLoadRequest): Promise<CafLoadRequest | object> {
    const data = castData(req.media?.customData ?? req.customData);
    if (!data) return req;
    this.ui.setLang(data.lang);
    // a renewal or a retry of the item playing keeps its picture: no loading screen
    const same =
      this.cur !== null &&
      (data.video_id ? data.video_id === this.cur.video.id : data.queue_pos === this.cur.queuePos);
    if (!same) {
      this.ui.loading("");
      this.broadcast({ type: "state", state: "loading" });
    }
    try {
      const home = await this.home(data);
      const client = new HomeClient(home.base, data.grant, this.fetchFn);
      let queue: Queue | null = null;
      let queuePos: number | undefined;
      let videoId = data.video_id ?? "";
      let subtitle: string | undefined;
      if (data.queue_id) {
        queue = await client.queue(data.queue_id);
        queuePos = data.queue_pos ?? queue.current_pos;
        const item = queue.items.find((i) => i.pos === queuePos) ?? queue.items[0];
        if (!item) throw new HomeError(404, "", "the queue is empty");
        queuePos = item.pos;
        videoId = item.id;
        subtitle = item.series_name || undefined;
      }
      const video = await client.video(videoId);
      if (!same) this.ui.loading(video.title);
      const session = await client.openSession({
        video_id: videoId,
        start_sec: data.start_sec,
        device_caps: deviceCaps(this.ctx, this.fw),
        force_transcode: data.force_transcode,
      });
      const chapters = await client.chapters(videoId).catch(() => [] as Chapter[]);
      const subs =
        session.direct_url && session.stream_sig
          ? await client.subtitles(videoId).catch(() => [] as SubtitleTrack[])
          : [];
      const previous = this.cur;
      this.cur = {
        data,
        client,
        home,
        video,
        session,
        chapters,
        queue,
        queuePos,
        sessionExpires: Date.parse(session.expires_at) || 0,
        grantExpires: grantExpiry(data.grant),
        retried: data.force_transcode === true,
        renewing: false,
      };
      this.grantEnded = false;
      if (previous && previous.session.session_id !== session.session_id) {
        void previous.client.closeSession(previous.session.session_id).catch(() => {});
      }
      this.fill(req, this.cur, subtitle, subs);
      this.start();
      this.broadcast({
        type: "state",
        state: "ready",
        remote: home.remote,
        video_id: videoId,
        queue_pos: queuePos,
      });
      this.log("cast load", { video_id: videoId, mode: session.mode, remote: home.remote });
      return req;
    } catch (e) {
      return this.loadFailed(e);
    }
  }

  private async home(data: CastData): Promise<ResolvedHome> {
    const key = `${data.home}|${(data.urls ?? []).join(",")}`;
    if (this.homeCache?.key === key) return this.homeCache.home;
    const home = await resolveHome(data.home, data.urls, this.fetchFn);
    this.homeCache = { key, home };
    return home;
  }

  private fill(
    req: CafLoadRequest,
    c: Current,
    subtitle?: string,
    subs: readonly SubtitleTrack[] = []
  ): void {
    const s = c.session;
    const url = s.manifest_url || s.direct_url || "";
    const hls = Boolean(s.manifest_url);
    const m = this.fw.messages;
    req.media = req.media ?? {};
    req.media.contentId = url;
    req.media.contentUrl = url;
    req.media.contentType = hls ? "application/x-mpegurl" : "video/mp4";
    req.media.streamType = constant(m.StreamType, "BUFFERED", "BUFFERED");
    if (hls) {
      const fmt = segmentFormat(s);
      req.media.hlsSegmentFormat = constant(m.HlsSegmentFormat, fmt, fmt.toLowerCase());
      req.media.hlsVideoSegmentFormat =
        fmt === "FMP4"
          ? constant(m.HlsVideoSegmentFormat, "FMP4", "fmp4")
          : constant(m.HlsVideoSegmentFormat, "MPEG2_TS", "mpeg2_ts");
    }
    const duration = s.duration_sec || c.video.duration_sec || undefined;
    if (duration) req.media.duration = duration;
    req.media.metadata = { title: c.video.title, subtitle };
    const tracks = hls ? [] : sidecarTracks(s, subs);
    req.media.tracks = tracks.length
      ? tracks.map((t, i) => ({
          trackId: i + 1,
          type: constant(m.TrackType, "TEXT", "TEXT"),
          trackContentId: t.url,
          trackContentType: "text/vtt",
          subtype: constant(m.TextTrackType, "SUBTITLES", "SUBTITLES"),
          name: t.language,
          language: t.language,
        }))
      : undefined;
    req.media.customData = publicData(c.data, c.video.id, c.queuePos);
    req.customData = undefined;
    req.currentTime = s.start_sec || c.data.start_sec || 0;
    req.autoplay = true;
    this.posterURL = "";
    void c.client.poster(c.video.id).then((p) => {
      if (this.cur === c && p) {
        this.posterURL = p;
        this.ui.setPoster(p);
      }
    });
    this.subtitle = subtitle;
  }

  private posterURL = "";
  private subtitle: string | undefined;

  private nowPlaying(c: Current) {
    return {
      title: c.video.title,
      subtitle: this.subtitle,
      description: c.video.description ?? undefined,
      poster: this.posterURL || undefined,
      remote: c.home.remote,
    };
  }

  private loadFailed(e: unknown): object {
    let key: Key = "receiver.error.home";
    if (e instanceof HomeUnreachable) key = "receiver.error.home";
    else if (e instanceof HomeError && (e.grantEnded || e.status === 404 || e.status === 403))
      key = "receiver.error.grant";
    this.log("cast load failed", { error: String(e) });
    this.showError(key);
    const m = this.fw.messages;
    const type = constant(m.ErrorType, "LOAD_FAILED", "LOAD_FAILED");
    const err = m.ErrorData ? new m.ErrorData(type) : { type };
    (err as { customData?: unknown }).customData = { error: key.replace("receiver.error.", "") };
    return err;
  }

  private showError(key: Key): void {
    this.stop();
    this.ui.error(key);
    this.broadcast({ type: "error", error: key.replace("receiver.error.", "") });
  }

  private start(): void {
    this.stop();
    this.timer = setInterval(() => void this.everyTen(), PROGRESS_MS);
  }

  private stop(): void {
    if (this.timer !== undefined) clearInterval(this.timer);
    this.timer = undefined;
  }

  private async everyTen(): Promise<void> {
    const c = this.cur;
    if (!c) return;
    if (this.playing) await this.report(false);
    // renew before the URLs end, unless the grant is over: then the item plays out on the
    // segments already signed
    if (
      !c.renewing &&
      c.sessionExpires &&
      this.now() >= c.sessionExpires - RENEW_BEFORE_MS &&
      !this.grantOver(c)
    ) {
      await this.renew(c);
    }
  }

  private grantOver(c: Current): boolean {
    return this.grantEnded || (c.grantExpires > 0 && this.now() >= c.grantExpires);
  }

  private async report(completed: boolean): Promise<void> {
    const c = this.cur;
    if (!c) return;
    const at = this.ctx.getPlayerManager().getCurrentTimeSec();
    try {
      await c.client.progress(c.session.session_id, at, completed);
    } catch (e) {
      if (e instanceof HomeError && e.grantEnded) this.grantEnded = true;
      this.log("progress failed", { error: String(e) });
    }
  }

  /** New signed URLs: the session opened again at the position, then the player reloaded on
   * them (the home hands back the open session). */
  private async renew(c: Current): Promise<void> {
    c.renewing = true;
    const pm = this.ctx.getPlayerManager();
    const at = pm.getCurrentTimeSec();
    try {
      await pm.load(this.loadFor(c.data, c.video.id, c.queuePos, at, c.retried));
    } catch (e) {
      this.log("renew failed", { error: String(e) });
    } finally {
      c.renewing = false;
    }
  }

  private loadFor(
    d: CastData,
    videoId: string,
    queuePos: number | undefined,
    at: number,
    force: boolean
  ): CafLoadRequest {
    const data: CastData = { ...d, start_sec: at, force_transcode: force || undefined };
    if (d.queue_id) data.queue_pos = queuePos;
    else data.video_id = videoId;
    return { media: { contentId: videoId, customData: data }, autoplay: true, currentTime: at };
  }

  private tick(): void {
    const c = this.cur;
    if (!c) return;
    const pm = this.ctx.getPlayerManager();
    const at = pm.getCurrentTimeSec();
    const duration = pm.getDurationSec() || c.session.duration_sec || c.video.duration_sec || 0;
    const ch = c.chapters.find((x) => at >= x.start_sec && at < x.end_sec);
    this.ui.time(at, duration, ch?.title);
    const next = this.nextItem(c);
    if (next && duration > 0 && duration - at <= UP_NEXT_SEC && !this.grantOver(c)) {
      this.ui.showUpNext(next.title || next.filename || "");
    } else {
      this.ui.hideUpNext();
    }
  }

  private nextItem(c: Current) {
    if (!c.queue || c.queuePos === undefined) return undefined;
    return c.queue.items.find((i) => i.pos > (c.queuePos as number));
  }

  private async finished(): Promise<void> {
    const c = this.cur;
    if (!c) return;
    this.playing = false;
    try {
      await c.client.progress(
        c.session.session_id,
        this.ctx.getPlayerManager().getDurationSec() || c.session.duration_sec || 0,
        true
      );
    } catch (e) {
      if (e instanceof HomeError && e.grantEnded) this.grantEnded = true;
    }
    if (this.grantOver(c)) {
      this.cur = null;
      this.showError("receiver.error.grant");
      return;
    }
    const next = this.nextItem(c);
    if (!next || !c.queue || !c.data.queue_id) {
      this.stop();
      this.cur = null;
      this.ui.idle();
      this.broadcast({ type: "state", state: "idle" });
      return;
    }
    try {
      const q = await c.client.setCurrent(c.data.queue_id, next.pos, c.queue.version);
      c.queue = q;
    } catch (e) {
      // a queue moved on another device (409) still plays the item it was going to
      if (e instanceof HomeError && e.grantEnded) {
        this.cur = null;
        this.showError("receiver.error.grant");
        return;
      }
    }
    await this.ctx.getPlayerManager().load(this.loadFor(c.data, next.id, next.pos, 0, false));
  }

  /** The player could not play what the home sent: once, ask again for a transcode (the home
   * converts it); then the format error. */
  private async playError(): Promise<void> {
    const c = this.cur;
    if (!c) return;
    if (!c.retried) {
      c.retried = true;
      const at = this.ctx.getPlayerManager().getCurrentTimeSec();
      this.log("play error: retrying as a transcode", { video_id: c.video.id });
      try {
        await this.ctx
          .getPlayerManager()
          .load(this.loadFor(c.data, c.video.id, c.queuePos, at, true));
        return;
      } catch {
        // the format error below
      }
    }
    this.showError("receiver.error.format");
  }

  private onMessage(data: unknown, _senderId: string): void {
    if (!data || typeof data !== "object") return;
    const m = data as { type?: unknown; grant?: unknown };
    // a fresh grant from the sender (the cast outlives the first one)
    if (
      m.type === "grant" &&
      typeof m.grant === "string" &&
      m.grant.startsWith("cast_") &&
      this.cur
    ) {
      this.cur.data = { ...this.cur.data, grant: m.grant };
      this.cur.client.setGrant(m.grant);
      this.cur.grantExpires = grantExpiry(m.grant);
      this.grantEnded = false;
    }
  }

  private broadcast(msg: Record<string, unknown>): void {
    try {
      this.ctx.sendCustomMessage(NAMESPACE, undefined, msg);
    } catch {
      // no sender connected
    }
  }
}
