// Live HLS on one <video> (audit P1-3, Epic 32.9 AC2/AC4).
//
// Safari / iOS play an .m3u8 natively, so there the playlist goes straight
// into <video src>. Everywhere else hls.js is lazy-loaded into its own chunk
// and drives the element through Media Source Extensions. When neither is
// available the stream reports `unsupported`.
//
// Mid-play resilience ("rejoin after a stall"):
//   • fatal NETWORK_ERROR → hls.startLoad() with bounded, backed-off retries
//   • fatal MEDIA_ERROR   → hls.recoverMediaError() (bounded)
//   • anything else, or exhausted retries → destroy + re-create the
//     instance (rejoins at the live edge); once that is exhausted too,
//     `onFatal` tells the owner
//   • a watchdog on both paths: `waiting`/`stalled` with no timeupdate
//     progress for STALL_MS while the viewer has not paused → rejoin
//
// `rewrite` (hls.js only) maps every playlist and segment URL just before it
// is requested: the remote path swaps its signed token there, so a new token
// takes effect on the next request without reloading the media (32.9 AC4).
import type HlsType from "hls.js";

export const HLS_MIME = "application/vnd.apple.mpegurl";
export const STALL_MS = 8000;
export const NET_RETRIES = 3;
export const NET_RETRY_BASE_MS = 1000;
export const MEDIA_RECOVERIES = 2;
export const MAX_REJOINS = 3;

export const HLS_CONFIG = {
  liveSyncDurationCount: 3,
  liveMaxLatencyDurationCount: 10,
  lowLatencyMode: false,
  enableWorker: true,
  backBufferLength: 30,
  // join at the live edge (34.6 design §8.3)
  startPosition: -1,
} as const;

export type LiveMode = "idle" | "native" | "hls" | "unsupported";

export interface LiveOptions {
  onMode?: (mode: LiveMode) => void;
  onReconnecting?: (on: boolean) => void;
  /** The stream cannot be recovered. */
  onFatal?: () => void;
  /** Maps each playlist/segment URL before it is requested (hls.js only). */
  rewrite?: (url: string) => string;
  /** Loads hls.js (default: a dynamic import into its own chunk). */
  loadHls?: () => Promise<typeof HlsType>;
}

export interface LiveHandle {
  readonly mode: LiveMode;
  detach(): void;
}

/** Whether the element plays HLS without MSE (Safari / iOS). */
export function hasNativeHls(video: HTMLVideoElement): boolean {
  try {
    return video.canPlayType(HLS_MIME) !== "";
  } catch {
    return false;
  }
}

const defaultLoadHls = () => import("hls.js").then((m) => m.default);

interface LoaderLike {
  load(context: { url: string }, config: unknown, callbacks: unknown): void;
}
type LoaderCtor = new (config: unknown) => LoaderLike;

/** An hls.js loader class that rewrites every URL first. */
function rewritingLoader(Hls: typeof HlsType, rewrite: (url: string) => string): LoaderCtor | null {
  const Base = (Hls as unknown as { DefaultConfig?: { loader?: LoaderCtor } }).DefaultConfig
    ?.loader;
  if (!Base) return null;
  return class extends Base {
    load(context: { url: string }, config: unknown, callbacks: unknown) {
      context.url = rewrite(context.url);
      super.load(context, config, callbacks);
    }
  };
}

/** Plays `src` (an .m3u8 URL) on `video` until detach. */
export function attachLive(
  video: HTMLVideoElement,
  src: string,
  opts: LiveOptions = {}
): LiveHandle {
  let disposed = false;
  let mode: LiveMode = "idle";
  let hls: HlsType | null = null;
  let HlsCtor: typeof HlsType | null = null;
  let netRetries = 0;
  let mediaRecoveries = 0;
  let rejoins = 0;
  let stallTrips = 0;
  let netTimer: ReturnType<typeof setTimeout> | null = null;
  let stallTimer: ReturnType<typeof setTimeout> | null = null;
  let lastTime = video.currentTime;
  let userPaused = false;
  let reconnecting = false;
  const native = hasNativeHls(video);

  const setMode = (m: LiveMode) => {
    mode = m;
    opts.onMode?.(m);
  };
  const setReconnecting = (on: boolean) => {
    if (on === reconnecting) return;
    reconnecting = on;
    opts.onReconnecting?.(on);
  };
  const clearNet = () => {
    if (netTimer) clearTimeout(netTimer);
    netTimer = null;
  };
  const clearStall = () => {
    if (stallTimer) clearTimeout(stallTimer);
    stallTimer = null;
  };
  const giveUp = () => {
    clearNet();
    clearStall();
    setReconnecting(false);
    opts.onFatal?.();
  };
  const destroyHls = () => {
    if (hls) {
      try {
        hls.destroy();
      } catch {
        /* already torn down */
      }
    }
    hls = null;
  };

  const createHls = () => {
    if (!HlsCtor || disposed) return;
    const Hls = HlsCtor;
    const config: Record<string, unknown> = { ...HLS_CONFIG };
    if (opts.rewrite) {
      const loader = rewritingLoader(Hls, opts.rewrite);
      if (loader) config.loader = loader;
    }
    const inst = new Hls(config);
    hls = inst;
    inst.on(Hls.Events.ERROR, (_evt, data) => {
      if (disposed || hls !== inst || !data.fatal) return;
      if (data.type === Hls.ErrorTypes.NETWORK_ERROR && netRetries < NET_RETRIES) {
        const delay = NET_RETRY_BASE_MS * 2 ** netRetries;
        netRetries += 1;
        setReconnecting(true);
        clearNet();
        netTimer = setTimeout(() => {
          if (!disposed && hls === inst) inst.startLoad();
        }, delay);
        return;
      }
      if (data.type === Hls.ErrorTypes.MEDIA_ERROR && mediaRecoveries < MEDIA_RECOVERIES) {
        mediaRecoveries += 1;
        setReconnecting(true);
        inst.recoverMediaError();
        return;
      }
      rejoin();
    });
    inst.attachMedia(video);
    inst.loadSource(opts.rewrite ? opts.rewrite(src) : src);
  };

  // Destroy + re-create (hls) or reload the playlist (native); both join at
  // the live edge. Bounded so a dead channel falls through to onFatal.
  const rejoin = () => {
    if (disposed) return;
    if (rejoins >= MAX_REJOINS) {
      giveUp();
      return;
    }
    rejoins += 1;
    netRetries = 0;
    mediaRecoveries = 0;
    clearNet();
    setReconnecting(true);
    if (HlsCtor) {
      destroyHls();
      createHls();
    } else {
      video.src = src;
    }
  };

  const onStallTimeout = () => {
    stallTimer = null;
    if (disposed || userPaused) return;
    stallTrips += 1;
    const live = hls?.liveSyncPosition;
    if (hls && stallTrips === 1 && typeof live === "number" && Number.isFinite(live)) {
      setReconnecting(true);
      try {
        video.currentTime = live;
      } catch {
        /* not seekable yet */
      }
      hls.startLoad();
    } else {
      rejoin();
    }
    stallTimer = setTimeout(onStallTimeout, STALL_MS);
  };
  const onWaiting = () => {
    if (!stallTimer && !userPaused) stallTimer = setTimeout(onStallTimeout, STALL_MS);
  };
  const onProgress = () => {
    if (video.currentTime === lastTime) return;
    lastTime = video.currentTime;
    clearStall();
    stallTrips = 0;
    setReconnecting(false);
  };
  const onPlaying = () => {
    userPaused = false;
    clearStall();
    stallTrips = 0;
    netRetries = 0;
    mediaRecoveries = 0;
    rejoins = 0;
    setReconnecting(false);
  };
  const onPause = () => {
    userPaused = true;
    clearStall();
  };
  const onPlay = () => {
    userPaused = false;
  };

  video.addEventListener("waiting", onWaiting);
  video.addEventListener("stalled", onWaiting);
  video.addEventListener("timeupdate", onProgress);
  video.addEventListener("playing", onPlaying);
  video.addEventListener("pause", onPause);
  video.addEventListener("play", onPlay);

  if (native) {
    setMode("native");
    video.src = src;
  } else {
    setMode("idle");
    (opts.loadHls ?? defaultLoadHls)()
      .then((Hls) => {
        if (disposed) return;
        if (!Hls.isSupported()) {
          setMode("unsupported");
          return;
        }
        HlsCtor = Hls;
        setMode("hls");
        createHls();
      })
      .catch(() => {
        if (!disposed) setMode("unsupported");
      });
  }

  return {
    get mode() {
      return mode;
    },
    detach() {
      if (disposed) return;
      disposed = true;
      clearNet();
      clearStall();
      destroyHls();
      video.removeEventListener("waiting", onWaiting);
      video.removeEventListener("stalled", onWaiting);
      video.removeEventListener("timeupdate", onProgress);
      video.removeEventListener("playing", onPlaying);
      video.removeEventListener("pause", onPause);
      video.removeEventListener("play", onPlay);
    },
  };
}
