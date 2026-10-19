// Live HLS attachment for the channel player (audit P1-3).
//
// Safari / iOS play an .m3u8 natively, so there the playlist goes straight
// into <video src>. Everywhere else (Chrome, Firefox, Edge, Android WebView)
// hls.js is lazy-loaded into its own chunk and drives the element through
// Media Source Extensions. When neither is available the hook reports
// `unsupported` and the player shows a slate with a direct stream link.
//
// Mid-play resilience ("rejoin after a stall"):
//   • fatal NETWORK_ERROR → hls.startLoad() with bounded, backed-off retries
//   • fatal MEDIA_ERROR   → hls.recoverMediaError() (bounded)
//   • anything else, or exhausted retries → destroy + re-create the
//     instance (rejoins at the live edge); once that is exhausted too the
//     player's own retry/slate logic takes over via `onFatal`
//   • a watchdog on both paths: `waiting`/`stalled` with no timeupdate
//     progress for STALL_MS while the viewer has not paused → rejoin (seek
//     to hls.liveSyncPosition first, then a full reload; native reloads src)
// `reconnecting` is true while any of that is in flight.
import { useEffect, useRef, useState } from "react";
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
} as const;

export type LiveHlsMode = "idle" | "native" | "hls" | "unsupported";

export interface LiveHlsState {
  mode: LiveHlsMode;
  reconnecting: boolean;
}

/** Whether the element plays HLS without MSE (Safari / iOS). */
export function hasNativeHls(video: HTMLVideoElement): boolean {
  try {
    return video.canPlayType(HLS_MIME) !== "";
  } catch {
    return false;
  }
}

/**
 * Attach `src` (an .m3u8 URL) to `video`. `onFatal` is called when the
 * stream cannot be recovered so the caller can retry or show a slate.
 */
export function useLiveHls(
  video: HTMLVideoElement | null,
  src: string,
  onFatal: () => void
): LiveHlsState {
  const [mode, setMode] = useState<LiveHlsMode>("idle");
  const [reconnecting, setReconnecting] = useState(false);
  const fatalRef = useRef(onFatal);
  fatalRef.current = onFatal;

  useEffect(() => {
    if (!video || !src) return;
    let disposed = false;
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
    const native = hasNativeHls(video);

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
      fatalRef.current();
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
      const inst = new Hls(HLS_CONFIG);
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
      inst.loadSource(src);
    };

    // Destroy + re-create (hls) or reload the playlist (native); both join
    // at the live edge. Bounded so a dead channel falls through to onFatal.
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

    // Watchdog: a stall that is not followed by progress within STALL_MS.
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

    setReconnecting(false);
    if (native) {
      setMode("native");
      video.src = src;
    } else {
      setMode("idle");
      import("hls.js")
        .then((m) => {
          if (disposed) return;
          const Hls = m.default;
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

    return () => {
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
    };
  }, [video, src]);

  return { mode, reconnecting };
}
