// Live HLS attachment as a React hook (audit P1-3). The logic — native HLS
// on Safari / iOS, lazy hls.js elsewhere, recovery from fatal errors and the
// stall watchdog that rejoins the live edge — lives in the shared TV player
// (@maktaba/tvplayer live.ts, Epic 34.6), which the live player uses for its
// two-element channel switching; this hook keeps one element on one URL.
import { useEffect, useRef, useState } from "react";
import { attachLive, type LiveMode } from "@maktaba/tvplayer";

export {
  HLS_CONFIG,
  HLS_MIME,
  MAX_REJOINS,
  MEDIA_RECOVERIES,
  NET_RETRIES,
  NET_RETRY_BASE_MS,
  STALL_MS,
  hasNativeHls,
} from "@maktaba/tvplayer";

export type LiveHlsMode = LiveMode;

export interface LiveHlsState {
  mode: LiveHlsMode;
  reconnecting: boolean;
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
    setReconnecting(false);
    const h = attachLive(video, src, {
      onMode: setMode,
      onReconnecting: setReconnecting,
      onFatal: () => fatalRef.current(),
    });
    return () => h.detach();
  }, [video, src]);

  return { mode, reconnecting };
}
