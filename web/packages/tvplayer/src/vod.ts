// VOD HLS on one <video> (library playback, the watch page).
//
// The live path (live.ts) is tuned for a moving live edge; a library video is
// a finite playlist the viewer seeks around in, so it gets its own small
// attachment that shares the same loading rules: Safari / iOS play the .m3u8
// natively, everywhere else hls.js is lazy-loaded into its own chunk (only
// when native HLS is missing) and drives the element through MSE.
//
// Signed URLs (PR #114): the manifest URL carries `?sig=` and the streaming
// service copies that token onto every child URI it serves, so hls.js must
// request URLs exactly as resolved — no query stripping. As a belt-and-braces
// step the loader also carries the manifest's sig onto a same-session child
// URL that arrives without one (never onto another origin or path).
//
// An expired / revoked token surfaces as HTTP 401/403 on a playlist or
// segment: hls.js does not retry 4xx, so `onAuthError(position, playing)` tells the
// owner to reopen the session for a fresh signed URL and re-attach at that
// position. A stream that is gone — 410 (its transcode stopped short of the
// end), or loads that still fail after the network retries (a 404 once the
// session was cleaned up) — tells it `onGone(position, playing)`: open the
// video again there. Other fatal errors get bounded network retries and
// media-error recovery before `onFatal`.
//
// Every failure that ends in a reopen or in `onFatal` is also described to
// `onError` (the engine, hls.js's error details or the element's MediaError,
// the HTTP status and URL of the failing load): the browser is the only place
// these show, so the owner can report them to the server's log.
import type HlsType from "hls.js";
import { hasNativeHls } from "./live";

export const VOD_NET_RETRIES = 3;
export const VOD_NET_RETRY_BASE_MS = 1000;
export const VOD_MEDIA_RECOVERIES = 2;
/** Native path: no progress for this long while playing → probe the URL. */
export const VOD_STALL_MS = 8000;

export const VOD_HLS_CONFIG = {
  enableWorker: true,
  lowLatencyMode: false,
  // Keep enough behind the playhead for a short seek back without refetching.
  backBufferLength: 90,
  // In-manifest subtitle renditions become the element's TextTracks, so the
  // native captions menu and the "c" shortcut work as on Safari.
  renderTextTracksNatively: true,
} as const;

export type VodMode = "idle" | "native" | "hls" | "unsupported";

/** A playback failure, as `onError` hears of it. */
export interface VodError {
  /** What plays the stream: hls.js over MSE, or the element's native HLS. */
  engine: "hls.js" | "native";
  /** What came of it: the stream is opened again (`onGone`), or `onFatal`. */
  outcome: "reopen" | "fatal";
  /** hls.js's error details (fragLoadError…), or media_err_<code> natively. */
  code: string;
  message: string;
  /** HTTP status of the failing load (0: none known). */
  httpStatus: number;
  /** The failing load's URL, when known (it carries the signature). */
  url?: string;
}

export interface VodOptions {
  /** Position (s) to start at; 0 / undefined = the beginning. */
  startAt?: number;
  onMode?: (mode: VodMode) => void;
  /** HTTP 401/403 on a playlist or segment: the signed URL expired. */
  onAuthError?: (position: number, playing: boolean) => void;
  /**
   * The stream is gone (410: its transcode stopped short of the end; or its
   * loads keep failing, a 404 once the session was cleaned up): open the video
   * again at that position. Without it, the error is fatal.
   */
  onGone?: (position: number, playing: boolean) => void;
  /** The stream cannot be recovered. */
  onFatal?: () => void;
  /** Called with the failure before each reopen (`onGone`) or `onFatal`. */
  onError?: (e: VodError) => void;
  /**
   * Native HLS reports no HTTP status: a media error while this says the
   * signed URL has expired is treated as an auth error. Otherwise the
   * manifest is probed and a 401/403 there counts as one too.
   */
  isExpired?: () => boolean;
  /** Status probe for the native path (default: fetch the manifest). */
  probe?: (url: string) => Promise<number>;
  /** Loads hls.js (default: a dynamic import into its own chunk). */
  loadHls?: () => Promise<typeof HlsType>;
}

export interface VodHandle {
  readonly mode: VodMode;
  detach(): void;
}

const defaultLoadHls = () => import("hls.js").then((m) => m.default);

/** HTTP status of a GET of `url` (the body is discarded). */
async function defaultProbe(url: string): Promise<number> {
  const res = await fetch(url, { credentials: "include", cache: "no-store" });
  void res.body?.cancel().catch(() => {});
  return res.status;
}

/** The `sig` query value of `url`, or "" when it has none. */
export function sigOf(url: string): string {
  try {
    return new URL(url, baseHref()).searchParams.get("sig") ?? "";
  } catch {
    return "";
  }
}

function baseHref(): string {
  return typeof location !== "undefined" ? location.href : "http://localhost/";
}

/**
 * Returns `url` unchanged when it already carries a sig or lies outside the
 * manifest's session directory; otherwise appends the manifest's sig. The
 * rest of the URL (path, other query parameters, fragment) is kept as is.
 */
export function carrySig(url: string, manifest: string): string {
  const sig = sigOf(manifest);
  if (!sig) return url;
  try {
    const m = new URL(manifest, baseHref());
    const u = new URL(url, m);
    if (u.searchParams.has("sig")) return url;
    if (u.origin !== m.origin) return url;
    const dir = m.pathname.slice(0, m.pathname.lastIndexOf("/") + 1);
    if (!u.pathname.startsWith(dir)) return url;
    const [base, frag] = splitHash(url);
    return `${base}${base.includes("?") ? "&" : "?"}sig=${encodeURIComponent(sig)}${frag}`;
  } catch {
    return url;
  }
}

function splitHash(url: string): [string, string] {
  const i = url.indexOf("#");
  return i < 0 ? [url, ""] : [url.slice(0, i), url.slice(i)];
}

/** A MediaError's description, for `onError`. */
function mediaErrorOf(video: HTMLVideoElement): { code: string; message: string } {
  const e = video.error;
  if (!e) return { code: "media_err_unknown", message: "" };
  return { code: `media_err_${e.code}`, message: e.message || "" };
}

/** The `onError` description of an hls.js error event. */
export function hlsErrorOf(data: {
  details?: string;
  type?: string;
  error?: { message?: string };
  reason?: string;
  url?: string;
  frag?: { url?: string };
  context?: { url?: string };
  response?: { code?: number; text?: string };
  networkDetails?: { status?: number };
}): Omit<VodError, "engine" | "outcome"> {
  return {
    code: data.details || data.type || "hls_error",
    message: data.error?.message || data.reason || data.response?.text || "",
    httpStatus: httpStatusOf(data),
    url: data.url || data.frag?.url || data.context?.url,
  };
}

/** HTTP status of an hls.js error, when it has one. */
export function httpStatusOf(data: {
  response?: { code?: number };
  networkDetails?: { status?: number };
}): number {
  const code = data.response?.code ?? data.networkDetails?.status;
  return typeof code === "number" ? code : 0;
}

interface LoaderLike {
  load(context: { url: string }, config: unknown, callbacks: unknown): void;
}
type LoaderCtor = new (config: unknown) => LoaderLike;

function sigLoader(Hls: typeof HlsType, manifest: string): LoaderCtor | null {
  const Base = (Hls as unknown as { DefaultConfig?: { loader?: LoaderCtor } }).DefaultConfig
    ?.loader;
  if (!Base) return null;
  return class extends Base {
    load(context: { url: string }, config: unknown, callbacks: unknown) {
      context.url = carrySig(context.url, manifest);
      super.load(context, config, callbacks);
    }
  };
}

/** Plays the VOD playlist `src` on `video` until detach. */
export function attachVod(video: HTMLVideoElement, src: string, opts: VodOptions = {}): VodHandle {
  let disposed = false;
  let mode: VodMode = "idle";
  let hls: HlsType | null = null;
  let netRetries = 0;
  let mediaRecoveries = 0;
  let authReported = false;
  let netTimer: ReturnType<typeof setTimeout> | null = null;
  const startAt = opts.startAt && opts.startAt > 0 ? opts.startAt : 0;

  const setMode = (m: VodMode) => {
    mode = m;
    opts.onMode?.(m);
  };
  const clearNet = () => {
    if (netTimer) clearTimeout(netTimer);
    netTimer = null;
  };
  const position = () => {
    const t = video.currentTime;
    return Number.isFinite(t) && t > 0 ? t : startAt;
  };
  type Detail = Omit<VodError, "engine" | "outcome">;
  const report = (outcome: VodError["outcome"], d: Detail | undefined) => {
    if (!d || !opts.onError) return;
    try {
      opts.onError({ engine: mode === "hls" ? "hls.js" : "native", outcome, ...d });
    } catch {
      /* a reporter's failure is not the stream's */
    }
  };
  const fatal = (d?: Detail) => {
    report("fatal", d);
    opts.onFatal?.();
  };
  const authError = (at = position(), playing = !video.paused) => {
    if (authReported || disposed) return;
    authReported = true;
    clearNet();
    opts.onAuthError?.(at, playing);
  };
  // reported once, like an auth error: the owner re-attaches a new stream
  const gone = (d?: Detail, at = position(), playing = !video.paused) => {
    if (authReported || disposed) return;
    if (!opts.onGone) {
      fatal(d);
      return;
    }
    authReported = true;
    clearNet();
    report("reopen", d);
    opts.onGone(at, playing);
  };

  // Native path: a start offset is applied once the metadata is known.
  const onNativeMeta = () => {
    if (startAt > 0) {
      try {
        video.currentTime = startAt;
      } catch {
        /* not seekable yet */
      }
    }
  };

  // Native HLS gives no HTTP status: on a media error, or when playback
  // stalls (Chrome's native HLS just stops on a 403 segment), probe the
  // manifest; 401/403 there means the signed URL expired.
  let probing = false;
  let stallTimer: ReturnType<typeof setTimeout> | null = null;
  let lastTime = -1;
  let wantPlay = false;
  const clearStall = () => {
    if (stallTimer) clearTimeout(stallTimer);
    stallTimer = null;
  };
  const checkNative = (onOther?: (status: number) => void) => {
    if (disposed || authReported || probing) return;
    // Native HLS pauses on an error (the flag can flip before the pause event
    // is dispatched): keep what the viewer was doing.
    const at = position();
    const playing = wantPlay || !video.paused;
    if (opts.isExpired?.()) {
      authError(at, playing);
      return;
    }
    probing = true;
    (opts.probe ?? defaultProbe)(src)
      .then((status) => {
        if (disposed) return;
        if (status === 401 || status === 403) authError(at, playing);
        else if (status === 404 || status === 410)
          gone(
            { ...mediaErrorOf(video), code: "manifest_gone", httpStatus: status, url: src },
            at,
            playing
          );
        else onOther?.(status);
      })
      .catch(() => {
        if (!disposed) onOther?.(0);
      })
      .finally(() => {
        probing = false;
      });
  };
  const onNativeError = () => {
    clearStall();
    checkNative((status) => fatal({ ...mediaErrorOf(video), httpStatus: status, url: src }));
  };
  const armStall = () => {
    clearStall();
    if (disposed || video.paused || video.ended) return;
    stallTimer = setTimeout(() => {
      stallTimer = null;
      if (disposed || video.paused || video.ended) return;
      checkNative(armStall);
    }, VOD_STALL_MS);
  };
  const onNativeProgress = () => {
    if (video.currentTime === lastTime) return;
    lastTime = video.currentTime;
    armStall();
  };
  const onNativePlay = () => {
    wantPlay = true;
    armStall();
  };
  const onNativePause = () => {
    wantPlay = false;
    clearStall();
  };
  const NATIVE_EVENTS: [string, () => void][] = [
    ["error", onNativeError],
    ["play", onNativePlay],
    ["playing", onNativePlay],
    ["timeupdate", onNativeProgress],
    ["pause", onNativePause],
    ["ended", onNativePause],
  ];

  const startHls = (Hls: typeof HlsType) => {
    const config: Record<string, unknown> = { ...VOD_HLS_CONFIG, startPosition: startAt || -1 };
    const loader = sigLoader(Hls, src);
    if (loader) config.loader = loader;
    const inst = new Hls(config);
    hls = inst;
    inst.on(Hls.Events.ERROR, (_evt, data) => {
      if (disposed || hls !== inst) return;
      const status = httpStatusOf(data);
      if (status === 401 || status === 403) {
        authError();
        return;
      }
      if (status === 410) {
        gone(hlsErrorOf(data));
        return;
      }
      if (!data.fatal) return;
      if (data.type === Hls.ErrorTypes.NETWORK_ERROR && netRetries < VOD_NET_RETRIES) {
        const delay = VOD_NET_RETRY_BASE_MS * 2 ** netRetries;
        netRetries += 1;
        clearNet();
        netTimer = setTimeout(() => {
          if (!disposed && hls === inst) inst.startLoad(position());
        }, delay);
        return;
      }
      if (data.type === Hls.ErrorTypes.MEDIA_ERROR && mediaRecoveries < VOD_MEDIA_RECOVERIES) {
        mediaRecoveries += 1;
        inst.recoverMediaError();
        return;
      }
      if (data.type === Hls.ErrorTypes.NETWORK_ERROR) {
        // the loads still fail: the stream is gone (cleaned up, stopped)
        gone(hlsErrorOf(data));
        return;
      }
      clearNet();
      fatal(hlsErrorOf(data));
    });
    inst.attachMedia(video);
    inst.loadSource(src);
  };

  const onPlaying = () => {
    netRetries = 0;
    mediaRecoveries = 0;
  };
  video.addEventListener("playing", onPlaying);

  if (hasNativeHls(video)) {
    setMode("native");
    video.addEventListener("loadedmetadata", onNativeMeta, { once: true });
    for (const [e, fn] of NATIVE_EVENTS) video.addEventListener(e, fn);
    video.src = src;
  } else {
    (opts.loadHls ?? defaultLoadHls)()
      .then((Hls) => {
        if (disposed) return;
        if (!Hls.isSupported()) {
          // No MSE: hand the URL to the element and let it try (old behaviour).
          setMode("unsupported");
          video.src = src;
          return;
        }
        setMode("hls");
        startHls(Hls);
      })
      .catch(() => {
        if (disposed) return;
        setMode("unsupported");
        video.src = src;
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
      video.removeEventListener("playing", onPlaying);
      video.removeEventListener("loadedmetadata", onNativeMeta);
      clearStall();
      for (const [e, fn] of NATIVE_EVENTS) video.removeEventListener(e, fn);
      if (hls) {
        try {
          hls.destroy();
        } catch {
          /* already torn down */
        }
        hls = null;
      }
    },
  };
}
