// Watch-session controller (Story 29.1 client wiring).
//
// The watch-analytics API has three calls — start / heartbeat / stop —
// but no client drove them before this. This controller is the single
// place that owns their lifecycle so every player (the web VideoPlayer,
// and by extension the Tauri desktop and Capacitor mobile shells that
// load the same bundle) reports identically:
//
//   play            → start()      opens a session, begins the 30 s beat
//   …every 30 s     → heartbeat()  advances credited time + position
//   pause / ended   → stop()       closes the session with a final position
//   navigate away   → stop()       (controller.dispose on unmount)
//   tab/app unload  → stopBeacon() keepalive POST so the last position lands
//
// Design notes:
//   - A session spans one continuous play→stop run. Pausing closes the
//     session; resuming opens a fresh one (start() is a no-op only while a
//     session is already open or opening, never permanently latched).
//   - When the user has paused tracking (Story 29.4), start() returns
//     {tracking:false} with no session_id; we record that and turn every
//     subsequent call into a no-op until the next start() attempt.
//   - All network errors are swallowed: analytics must never break
//     playback. The server already debounces and reaps abandoned
//     sessions, so a dropped beat or stop self-heals.
import { analyticsApi, detectClient } from "./analytics";

// 30 s cadence per the Story 29.1 client contract.
export const HEARTBEAT_MS = 30_000;

export interface WatchSessionOptions {
  videoId: string;
  // getPosition returns the current playback position in whole seconds.
  getPosition: () => number;
  // quality is the stream rendition label, when known (e.g. "auto",
  // "1080p", the streaming mode). Optional.
  quality?: string;
  // installUnload wires the page-unload beacon. Defaults to true; tests
  // pass false to keep jsdom listeners out of the way.
  installUnload?: boolean;
}

export interface WatchSessionController {
  start(): void;
  heartbeat(): void;
  stop(): void;
  dispose(): void;
  // Test/diagnostics accessor.
  sessionId(): string | null;
}

export function createWatchSession(opts: WatchSessionOptions): WatchSessionController {
  const { videoId, getPosition, quality } = opts;
  const installUnload = opts.installUnload !== false;

  let sessionId: string | null = null;
  let opening = false;
  let timer: ReturnType<typeof setInterval> | null = null;

  const clearTimer = () => {
    if (timer !== null) {
      clearInterval(timer);
      timer = null;
    }
  };

  const heartbeat = () => {
    if (!sessionId) return;
    void analyticsApi.heartbeat({ session_id: sessionId, position_sec: pos() }).catch(() => {});
  };

  const pos = () => Math.max(0, Math.floor(getPosition() || 0));

  const start = () => {
    if (sessionId || opening) return;
    opening = true;
    const client = detectClient();
    analyticsApi
      .start({ video_id: videoId, ...client, quality })
      .then((r) => {
        opening = false;
        // tracking:false (paused) or a server that returned no id → stay idle.
        if (!r.tracking || !r.session_id) return;
        sessionId = r.session_id;
        clearTimer();
        timer = setInterval(heartbeat, HEARTBEAT_MS);
      })
      .catch(() => {
        opening = false;
      });
  };

  const stop = () => {
    clearTimer();
    const id = sessionId;
    if (!id) return;
    sessionId = null;
    void analyticsApi.stop({ session_id: id, position_sec: pos() }).catch(() => {});
  };

  // Unload: synchronous keepalive beacon so the final position survives
  // the document being torn down. pagehide fires reliably on mobile
  // Safari where beforeunload does not, so we prefer it.
  const onUnload = () => {
    clearTimer();
    const id = sessionId;
    if (!id) return;
    sessionId = null;
    analyticsApi.stopBeacon({ session_id: id, position_sec: pos() });
  };

  if (installUnload && typeof window !== "undefined") {
    window.addEventListener("pagehide", onUnload);
  }

  const dispose = () => {
    if (installUnload && typeof window !== "undefined") {
      window.removeEventListener("pagehide", onUnload);
    }
    stop();
  };

  return { start, heartbeat, stop, dispose, sessionId: () => sessionId };
}
