// Story 11.3 — Video player.
//
// Performs the real streaming handshake (api/internal/handlers/streaming):
//   POST /api/stream/sessions { video_id }
//     -> { session_id, mode, manifest_url, direct_url, expires_at }
//   then plays manifest_url (HLS) or direct_url (direct mode), and
//   POSTs watch progress to /api/stream/sessions/{id}/progress.
// The old GET /api/videos/{id}/stream route was never mounted.
//
// Story 11.3's full implementation (HLS.js, sprite hover-scrubbing,
// sidecar subtitle switching) ships later in Epic 11; this wires the
// correct contract surface.
import { useEffect, useRef, useState } from "react";
import { useParams, useNavigate } from "react-router-dom";
import { api, ApiError } from "../lib/api";
import { useI18n } from "../lib/i18n";

// Mirrors streaming.OpenSessionResponse JSON tags.
interface OpenSessionResponse {
  session_id: string;
  mode: string; // direct, remux, transcode
  manifest_url?: string;
  direct_url?: string;
  expires_at: string;
}

export function VideoPlayer() {
  const { videoId } = useParams();
  const nav = useNavigate();
  const { t } = useI18n();
  const [session, setSession] = useState<OpenSessionResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const videoRef = useRef<HTMLVideoElement>(null);
  // Holds the latest resolved session_id so the open-session effect's
  // teardown can DELETE the exact session it opened (the session state is
  // set asynchronously, so closing over it would be stale).
  const sessionIdRef = useRef<string | null>(null);

  // Open a streaming session for this video.
  useEffect(() => {
    if (!videoId) return;
    let cancelled = false;
    api
      .post<OpenSessionResponse>("/api/stream/sessions", { video_id: videoId })
      .then((s) => {
        if (!cancelled) {
          sessionIdRef.current = s.session_id;
          setSession(s);
        }
      })
      .catch((e) => {
        if (cancelled) return;
        if (e instanceof ApiError) setErr(e.problem.title);
        else setErr(t("common.error"));
      });
    return () => {
      cancelled = true;
      // Best-effort teardown of the server-side encode/remux pipeline so it
      // is not orphaned until expires_at. Fires on unmount and on videoId
      // change (closing the prior session before the next one opens). Guard
      // on the ref so we never delete a session that never opened, and never
      // double-delete (the server also TTL-expires the session anyway).
      const id = sessionIdRef.current;
      if (id) {
        sessionIdRef.current = null;
        void api.delete(`/api/stream/sessions/${encodeURIComponent(id)}`).catch(() => {
          /* best-effort; the session TTL-expires server-side */
        });
      }
    };
  }, [videoId, t]);

  // Debounced watch-progress reporting. The server itself debounces to
  // at most one persisted write/second/session (Story 7.11), so a light
  // client throttle is enough to avoid flooding it on every timeupdate.
  useEffect(() => {
    const el = videoRef.current;
    if (!el || !session) return;
    let last = 0;
    const onTimeUpdate = () => {
      const now = Date.now();
      if (now - last < 5000) return;
      last = now;
      void api
        .post(`/api/stream/sessions/${encodeURIComponent(session.session_id)}/progress`, {
          position_sec: Math.floor(el.currentTime),
        })
        .catch(() => {
          // Progress is best-effort; a failed beat must not break playback.
        });
    };
    const onEnded = () => {
      void api
        .post(`/api/stream/sessions/${encodeURIComponent(session.session_id)}/progress`, {
          position_sec: Math.floor(el.currentTime),
          completed: true,
        })
        .catch(() => {
          /* best-effort */
        });
    };
    el.addEventListener("timeupdate", onTimeUpdate);
    el.addEventListener("ended", onEnded);
    return () => {
      el.removeEventListener("timeupdate", onTimeUpdate);
      el.removeEventListener("ended", onEnded);
    };
  }, [session]);

  if (err)
    return (
      <div className="mkt-alert" role="alert">
        {err}
      </div>
    );
  if (!session) return <p>{t("common.loading")}</p>;

  // direct mode streams the original file; remux/transcode serve an HLS
  // manifest. Prefer whichever the server populated.
  const src = session.manifest_url || session.direct_url || "";

  return (
    <section className="mkt-player">
      <button type="button" onClick={() => nav(-1)} className="mkt-btn mkt-btn--ghost">
        ← Back
      </button>
      <video
        ref={videoRef}
        data-testid="mkt-video"
        className="mkt-player__video"
        src={src}
        controls
        playsInline
        preload="metadata"
      />
    </section>
  );
}
