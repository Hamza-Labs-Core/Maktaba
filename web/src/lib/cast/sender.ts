// The web's Cast sender (Epic 58 story 58.06): Chrome casts the player's video, or the server
// play queue it plays (Play all, Shuffle), to Maktaba's receiver (story 58.07).
//
// On a cast it mints a cast grant (POST /api/cast/grants: the role and parental checks are the
// home's) and hands it to the receiver in the load's custom data (castData.ts). The receiver
// then opens its own sessions, walks the queue and reports progress with the grant, so this
// tab can close and the home still knows where the TV is; the player here pauses and only
// follows the TV (the media status Google relays: the item, the position). A cast longer than
// the grant gets a fresh one over the custom namespace before the first ends.
//
// One sender per tab (a module singleton): it outlives the player page, which remounts when
// the TV moves to the queue's next video.

import { api, ApiError } from "../api";
import { IN_CLOUD } from "../basePath";
import { castData } from "./castData";
import {
  CAST_NAMESPACE,
  castBlocked,
  loadCastSdk,
  type CastApi,
  type CastContext,
  type CastSession,
  type RemotePlayer,
  type RemotePlayerController,
} from "./sdk";

/** What the player draws: the button, then the mini controller. */
export interface CastView {
  /** The SDK loaded and the home has an address a TV can reach: show the button. */
  available: boolean;
  /** Why the button is hidden (null while it may show). */
  blocked: "no-app" | "insecure" | "browser" | "cloud" | "unreachable" | "loading" | null;
  /** none: no TV found on the network; idle: TVs found, not casting. */
  state: "none" | "idle" | "connecting" | "connected";
  device: string | null;
  /** The video the TV plays (from the receiver's media status). */
  videoId: string | null;
  queueId: string | null;
  queuePos: number | null;
  paused: boolean;
  currentTime: number;
  duration: number;
  /** The last thing that went wrong, as an i18n key. */
  error: CastErrorKey | null;
}

export type CastErrorKey =
  | "cast.error.refused"
  | "cast.error.cloud"
  | "cast.error.failed"
  | "cast.error.home"
  | "cast.error.format"
  | "cast.error.grantEnded"
  | "cast.error.update";

/** What to cast: a video, or the server queue at a position. */
export interface CastItem {
  videoId: string;
  queueId?: string;
  queuePos?: number;
  startSec: number;
  title?: string;
}

interface Grant {
  grant: string;
  expires_at: string;
}

interface Discovery {
  urls?: string[];
  api_level?: number;
}

export interface SenderDeps {
  appId: string;
  loadSdk: () => Promise<CastApi | null>;
  post: <T>(path: string, body: unknown) => Promise<T>;
  get: <T>(path: string) => Promise<T>;
  win: Pick<Window, "isSecureContext" | "location" | "navigator">;
  inCloud: boolean;
  lang: () => string;
  now: () => number;
  setTimer: (fn: () => void, ms: number) => unknown;
  clearTimer: (t: unknown) => void;
}

/** A fresh grant is minted this long before the current one ends. */
export const GRANT_REFRESH_BEFORE_MS = 30 * 60_000;

const INITIAL: CastView = {
  available: false,
  blocked: "loading",
  state: "idle",
  device: null,
  videoId: null,
  queueId: null,
  queuePos: null,
  paused: true,
  currentTime: 0,
  duration: 0,
  error: null,
};

const RECEIVER_ERRORS: Record<string, CastErrorKey> = {
  home: "cast.error.home",
  format: "cast.error.format",
  grant: "cast.error.grantEnded",
  update: "cast.error.update",
};

export class CastSender {
  private view: CastView = INITIAL;
  private listeners = new Set<(v: CastView) => void>();
  private started: Promise<void> | null = null;
  private sdk: CastApi | null = null;
  private ctx: CastContext | null = null;
  private player: RemotePlayer | null = null;
  private controller: RemotePlayerController | null = null;
  private discovery: Discovery = {};
  private listened: CastSession | null = null;
  private refresh: unknown = null;
  /** The item whose grant the refresh timer renews. */
  private granted: CastItem | null = null;

  constructor(private deps: SenderDeps) {}

  get current(): CastView {
    return this.view;
  }

  subscribe(fn: (v: CastView) => void): () => void {
    this.listeners.add(fn);
    fn(this.view);
    return () => this.listeners.delete(fn);
  }

  private set(patch: Partial<CastView>) {
    this.view = { ...this.view, ...patch };
    for (const fn of this.listeners) fn(this.view);
  }

  /** Loads the SDK once where casting can work; the view says whether the button shows. */
  start(): Promise<void> {
    if (!this.started) this.started = this.boot();
    return this.started;
  }

  private async boot() {
    const blocked = this.deps.inCloud ? "cloud" : castBlocked(this.deps.appId, this.deps.win);
    if (blocked) {
      this.set({ blocked, available: false });
      return;
    }
    try {
      this.discovery = await this.deps.get<Discovery>("/api/discovery/home");
    } catch {
      this.discovery = {};
    }
    // no address a TV could load from (a home reached by IP alone): no button
    if (!this.dataFor({ videoId: "x", startSec: 0 }, "cast_probe")) {
      this.set({ blocked: "unreachable", available: false });
      return;
    }
    const sdk = await this.deps.loadSdk();
    if (!sdk) {
      this.set({ blocked: "browser", available: false });
      return;
    }
    this.sdk = sdk;
    const fw = sdk.framework;
    const ctx = fw.CastContext.getInstance();
    this.ctx = ctx;
    ctx.setOptions({
      receiverApplicationId: this.deps.appId,
      autoJoinPolicy: sdk.chromeCast.AutoJoinPolicy.ORIGIN_SCOPED,
      language: this.deps.lang(),
    });
    this.player = new fw.RemotePlayer();
    this.controller = new fw.RemotePlayerController(this.player);
    this.controller.addEventListener(fw.RemotePlayerEventType.ANY_CHANGE, () => this.follow());
    ctx.addEventListener(fw.CastContextEventType.CAST_STATE_CHANGED, (e) =>
      this.set({ state: this.stateOf(e.castState ?? ctx.getCastState()) })
    );
    ctx.addEventListener(fw.CastContextEventType.SESSION_STATE_CHANGED, (e) => {
      if (e.sessionState === fw.SessionState.SESSION_ENDED) this.ended();
      else this.attach();
    });
    this.set({ available: true, blocked: null, state: this.stateOf(ctx.getCastState()) });
    this.attach(); // a cast this origin left running (a reload): join it
  }

  private stateOf(s: string | undefined): CastView["state"] {
    const cs = this.sdk?.framework.CastState;
    if (!cs) return "idle";
    if (s === cs.NO_DEVICES_AVAILABLE) return "none";
    if (s === cs.CONNECTING) return "connecting";
    if (s === cs.CONNECTED) return "connected";
    return "idle";
  }

  private dataFor(item: CastItem, grant: string) {
    return castData({
      senderBase: this.deps.win.location.origin,
      discoveryUrls: this.discovery.urls ?? [],
      grant,
      videoId: item.videoId,
      queueId: item.queueId,
      queuePos: item.queuePos,
      startSec: item.startSec,
      lang: this.deps.lang(),
      apiLevel: this.discovery.api_level ?? 1,
    });
  }

  private attach() {
    const s = this.ctx?.getCurrentSession() ?? null;
    if (!s || s === this.listened) return;
    this.listened = s;
    s.addMessageListener(CAST_NAMESPACE, this.onMessage);
    this.set({ device: s.getCastDevice().friendlyName, state: "connected" });
    this.follow();
  }

  private onMessage = (_ns: string, raw: string) => {
    let m: { type?: unknown; error?: unknown };
    try {
      m = JSON.parse(raw) as typeof m;
    } catch {
      return;
    }
    if (m.type === "error" && typeof m.error === "string" && RECEIVER_ERRORS[m.error])
      this.set({ error: RECEIVER_ERRORS[m.error] });
  };

  /** The receiver's media status: what the TV plays and where. */
  private follow() {
    const p = this.player;
    if (!p) return;
    const data = (p.mediaInfo?.customData ?? null) as {
      video_id?: unknown;
      queue_id?: unknown;
      queue_pos?: unknown;
    } | null;
    // before the receiver's first media status (the load still on its way) the item stays
    // the one this sender loaded
    this.set({
      videoId: typeof data?.video_id === "string" ? data.video_id : this.view.videoId,
      queueId: typeof data?.queue_id === "string" ? data.queue_id : this.view.queueId,
      queuePos: typeof data?.queue_pos === "number" ? data.queue_pos : this.view.queuePos,
      paused: p.isPaused,
      currentTime: p.currentTime,
      duration: p.duration,
    });
  }

  private ended() {
    if (this.listened) this.listened.removeMessageListener(CAST_NAMESPACE, this.onMessage);
    this.listened = null;
    this.granted = null;
    if (this.refresh !== null) this.deps.clearTimer(this.refresh);
    this.refresh = null;
    this.set({
      device: null,
      videoId: null,
      queueId: null,
      queuePos: null,
      state: this.stateOf(this.ctx?.getCastState()),
    });
  }

  private async mint(item: CastItem): Promise<Grant> {
    return this.deps.post<Grant>(
      "/api/cast/grants",
      item.queueId ? { queue_id: item.queueId } : { video_id: item.videoId }
    );
  }

  private problemKey(e: unknown): CastErrorKey {
    if (e instanceof ApiError) {
      if (e.problem.code === "cast_via_cloud") return "cast.error.cloud";
      if (e.status === 403 || e.status === 404) return "cast.error.refused";
    }
    return "cast.error.failed";
  }

  /**
   * Casts item: asks Chrome for a TV when none is connected (Chrome's own picker), mints the
   * grant and loads it on the receiver. False when nothing was cast (the person closed the
   * picker, the home refused the grant: view.error says which).
   */
  async cast(item: CastItem): Promise<boolean> {
    await this.start();
    const sdk = this.sdk;
    const ctx = this.ctx;
    if (!sdk || !ctx || !this.view.available) return false;
    this.set({ error: null });
    let session = ctx.getCurrentSession();
    if (!session) {
      try {
        await ctx.requestSession();
      } catch {
        return false; // the picker was closed
      }
      session = ctx.getCurrentSession();
      if (!session) return false;
      this.attach();
    }
    let grant: Grant;
    try {
      grant = await this.mint(item);
    } catch (e) {
      this.set({ error: this.problemKey(e) });
      return false;
    }
    const data = this.dataFor(item, grant.grant);
    if (!data) {
      this.set({ error: "cast.error.failed" });
      return false;
    }
    const media = new sdk.chromeCast.media.MediaInfo(item.videoId, "video/mp4");
    media.customData = data;
    const meta = new sdk.chromeCast.media.GenericMediaMetadata();
    if (item.title) meta.title = item.title;
    media.metadata = meta;
    const req = new sdk.chromeCast.media.LoadRequest(media);
    req.autoplay = true;
    req.currentTime = data.start_sec;
    try {
      await session.loadMedia(req);
    } catch {
      this.set({ error: "cast.error.failed" });
      return false;
    }
    this.set({
      videoId: item.videoId,
      queueId: item.queueId ?? null,
      queuePos: item.queuePos ?? null,
    });
    this.keepGrant(item, grant);
    return true;
  }

  /** Mints the next grant before this one ends and hands it to the receiver. */
  private keepGrant(item: CastItem, grant: Grant) {
    this.granted = item;
    if (this.refresh !== null) this.deps.clearTimer(this.refresh);
    const ends = Date.parse(grant.expires_at);
    if (!Number.isFinite(ends)) return;
    const wait = Math.max(60_000, ends - GRANT_REFRESH_BEFORE_MS - this.deps.now());
    this.refresh = this.deps.setTimer(() => void this.renew(item), wait);
  }

  private async renew(item: CastItem) {
    this.refresh = null;
    const s = this.ctx?.getCurrentSession();
    if (!s || this.granted !== item) return;
    try {
      const g = await this.mint(item);
      await s.sendMessage(CAST_NAMESPACE, { type: "grant", grant: g.grant });
      this.keepGrant(item, g);
    } catch {
      // the item plays out on the grant it has; the receiver then asks for a new cast
    }
  }

  playOrPause() {
    this.controller?.playOrPause();
  }

  seekTo(sec: number) {
    if (!this.player || !this.controller) return;
    this.player.currentTime = Math.max(0, sec);
    this.controller.seek();
  }

  /** Ends the cast; the TV stops and the receiver closes. */
  stop() {
    this.ctx?.getCurrentSession()?.endSession(true);
  }

  clearError() {
    this.set({ error: null });
  }
}

let shared: CastSender | null = null;

/** The tab's sender; its language is the page's (I18nProvider keeps <html lang> on it). */
export function castSender(): CastSender {
  if (!shared) {
    shared = new CastSender({
      appId: ((import.meta.env.VITE_CAST_APP_ID as string | undefined) ?? "").trim(),
      loadSdk: () => loadCastSdk(),
      post: (path, body) => api.post(path, body),
      get: (path) => api.get(path),
      win: window,
      inCloud: IN_CLOUD,
      lang: () => document.documentElement.lang,
      now: () => Date.now(),
      setTimer: (fn, ms) => setTimeout(fn, ms),
      clearTimer: (t) => clearTimeout(t as ReturnType<typeof setTimeout>),
    });
  }
  return shared;
}
