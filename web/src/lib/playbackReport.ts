// Playback failure reports.
//
// Once a streaming session is open, what goes wrong (a decode error, a
// playlist or segment answering 404, a direct file that stops arriving) shows
// only in the browser. The player describes each failure here; it is posted to
// POST /api/stream/sessions/{id}/errors, which writes it to the server's log as
// `playback_failed` next to the session's open and cleanup.
//
// A report never gets in the way of playback: its own failure is only noted
// in the console. The server strips URL queries (the signature) before it logs.
import { api } from "./api";

export interface PlaybackFailure {
  /** What played it: hls.js, native HLS, or the element on a direct file. */
  engine: "hls.js" | "native" | "direct";
  /** fatal: the error screen; reopen: the video opened again; stalled: no progress. */
  outcome: "fatal" | "reopen" | "stalled";
  code: string;
  message?: string;
  httpStatus?: number;
  url?: string;
}

export interface PlaybackContext {
  sessionId: string;
  videoId: string;
  /** The session's mode (direct, remux, transcode). */
  mode: string;
  positionSec: number;
}

export function reportPlaybackFailure(ctx: PlaybackContext, f: PlaybackFailure): void {
  console.warn("playback failed", { ...ctx, ...f });
  const position = Number.isFinite(ctx.positionSec) ? Math.max(0, ctx.positionSec) : 0;
  // sent after the player's own reaction (a reopen's request goes first)
  void Promise.resolve()
    .then(() =>
      api.post(`/api/stream/sessions/${encodeURIComponent(ctx.sessionId)}/errors`, {
        video_id: ctx.videoId,
        mode: ctx.mode,
        engine: f.engine,
        outcome: f.outcome,
        code: f.code,
        message: f.message ?? "",
        http_status: f.httpStatus ?? 0,
        url: f.url ?? "",
        position_sec: Math.round(position * 10) / 10,
      })
    )
    .catch((e: unknown) => console.warn("playback report not sent", e));
}

/** A MediaError as a failure code and message. */
export function mediaError(el: HTMLVideoElement): { code: string; message: string } {
  const e = el.error;
  return e
    ? { code: `media_err_${e.code}`, message: e.message || "" }
    : { code: "media_err_unknown", message: "" };
}
