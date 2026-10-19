// Story 11.3 — Video player.
//
// Real streaming handshake (api/internal/handlers/streaming):
//   POST   /api/stream/sessions { video_id }
//     -> { session_id, mode, manifest_url, direct_url, expires_at }
//   POST   /api/stream/sessions/{id}/progress { position_sec[, completed] }
//   DELETE /api/stream/sessions/{id}                       (teardown)
//
// HLS: native where the browser plays application/vnd.apple.mpegurl
// (Safari/iOS); elsewhere (Chrome/Firefox desktop) hls.js — lazy-loaded
// into its own chunk only when native HLS is missing — drives the element
// through MSE (@maktaba/tvplayer attachVod, the same loading the live player
// uses). Direct-play sessions set direct_url straight on the element.
//
// Signed URLs: manifest_url carries ?sig= and every child URI the streaming
// service serves carries it too. When the token expires mid-video (HTTP
// 401/403 under hls.js; under native HLS a media error or stall past
// expires_at, or a manifest probe answering 401/403) the session is reopened
// for a fresh signed URL and playback resumes at the current position.
// The rich control surface (speed, PiP, keyboard map, chapter ticks,
// caption sizing, resume) works on top of the <video> either way.
import { useCallback, useEffect, useRef, useState } from "react";
import { useParams, useNavigate, useSearchParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { api, ApiError } from "../lib/api";
import { useI18n } from "../lib/i18n";
import { createWatchSession } from "../lib/watchSession";
import { attachVod, carrySig } from "@maktaba/tvplayer";

interface OpenSessionResponse {
  session_id: string;
  mode: string;
  manifest_url?: string;
  direct_url?: string;
  expires_at: string;
  /** Sidecar subtitle tracks, when the server lists them. */
  subtitles?: SubtitleTrack[];
}
interface SubtitleTrack {
  lang: string;
  label?: string;
  url: string;
}
interface Chapter {
  id: number;
  start_sec: number;
  title: string;
}

const SPEEDS = [0.5, 0.75, 1, 1.25, 1.5, 2];
const CAPTION_SCALES = [0.75, 1, 1.25, 1.5];
/** Treat a signed URL as expired this long before expires_at. */
const EXPIRY_SKEW_MS = 30_000;

export function VideoPlayer() {
  const { videoId } = useParams();
  const [params] = useSearchParams();
  const nav = useNavigate();
  const { t } = useI18n();
  const [session, setSession] = useState<OpenSessionResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [chapters, setChapters] = useState<Chapter[]>([]);
  const [duration, setDuration] = useState(0);
  const [speed, setSpeed] = useState(1);
  const [captionScale, setCaptionScale] = useState(1);
  const videoRef = useRef<HTMLVideoElement>(null);
  const sessionIdRef = useRef<string | null>(null);
  // Where the next attachment starts: the ?t= deep link first, then the
  // position playback had reached when a signed URL had to be refreshed.
  const resumeRef = useRef<{ at: number; play: boolean } | null>(null);
  const refreshingRef = useRef(false);

  const startAt = Number(params.get("t")) || 0;

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
        setErr(e instanceof ApiError ? e.problem.title : t("common.error"));
      });
    return () => {
      cancelled = true;
      const id = sessionIdRef.current;
      if (id) {
        sessionIdRef.current = null;
        void api.delete(`/api/stream/sessions/${encodeURIComponent(id)}`).catch(() => {});
      }
    };
  }, [videoId, t]);

  // Chapter ticks for the scrubber.
  useEffect(() => {
    if (!videoId) return;
    api
      .get<{ items: Chapter[] }>(`/api/videos/${encodeURIComponent(videoId)}/chapters`)
      .then((r) => setChapters(r.items ?? []))
      .catch(() => setChapters([]));
  }, [videoId]);

  // Watch-progress reporting (server debounces to ≤1 write/s/session).
  useEffect(() => {
    const el = videoRef.current;
    if (!el || !session) return;
    let last = 0;
    const beat = (completed = false) => {
      void api
        .post(`/api/stream/sessions/${encodeURIComponent(session.session_id)}/progress`, {
          position_sec: Math.floor(el.currentTime),
          ...(completed ? { completed: true } : {}),
        })
        .catch(() => {});
    };
    const onTimeUpdate = () => {
      const now = Date.now();
      if (now - last < 5000) return;
      last = now;
      beat();
    };
    const onPause = () => beat();
    const onEnded = () => beat(true);
    const onLoaded = () => {
      setDuration(el.duration || 0);
    };
    el.addEventListener("timeupdate", onTimeUpdate);
    el.addEventListener("pause", onPause);
    el.addEventListener("ended", onEnded);
    el.addEventListener("loadedmetadata", onLoaded);
    return () => {
      el.removeEventListener("timeupdate", onTimeUpdate);
      el.removeEventListener("pause", onPause);
      el.removeEventListener("ended", onEnded);
      el.removeEventListener("loadedmetadata", onLoaded);
    };
  }, [session]);

  // Attach the stream. Direct play: the file URL on the element. HLS: native
  // or hls.js (attachVod), starting at the ?t= offset or the resume point.
  useEffect(() => {
    const el = videoRef.current;
    if (!el || !session || !videoId) return;
    const resume = resumeRef.current;
    resumeRef.current = null;
    const at = resume ? resume.at : startAt;
    const resumePlay = () => {
      if (resume?.play) void el.play?.()?.catch?.(() => {});
    };
    if (!session.manifest_url) {
      const onMeta = () => {
        if (at > 0) el.currentTime = at;
        resumePlay();
      };
      el.addEventListener("loadedmetadata", onMeta, { once: true });
      el.src = session.direct_url || "";
      return () => el.removeEventListener("loadedmetadata", onMeta);
    }
    let cancelled = false;
    // Expired signed URL: reopen the session (the server hands back the same
    // session with a freshly signed URL) and re-attach where we were.
    const refresh = (position: number, wasPlaying: boolean) => {
      if (refreshingRef.current) return;
      refreshingRef.current = true;
      api
        .post<OpenSessionResponse>("/api/stream/sessions", { video_id: videoId })
        .then((s) => {
          if (cancelled) {
            // Unmounted / navigated away while reopening: close what we got.
            void api
              .delete(`/api/stream/sessions/${encodeURIComponent(s.session_id)}`)
              .catch(() => {});
            return;
          }
          const prev = sessionIdRef.current;
          if (prev && prev !== s.session_id) {
            void api.delete(`/api/stream/sessions/${encodeURIComponent(prev)}`).catch(() => {});
          }
          sessionIdRef.current = s.session_id;
          resumeRef.current = { at: position, play: wasPlaying };
          setSession(s);
        })
        .catch((e) => {
          if (!cancelled) setErr(e instanceof ApiError ? e.problem.title : t("common.error"));
        })
        .finally(() => {
          refreshingRef.current = false;
        });
    };
    const expiresAt = Date.parse(session.expires_at);
    const h = attachVod(el, session.manifest_url, {
      startAt: at,
      onAuthError: refresh,
      isExpired: () => Number.isFinite(expiresAt) && Date.now() >= expiresAt - EXPIRY_SKEW_MS,
    });
    el.addEventListener("loadedmetadata", resumePlay, { once: true });
    return () => {
      cancelled = true;
      el.removeEventListener("loadedmetadata", resumePlay);
      h.detach();
    };
  }, [session, videoId, startAt, t]);

  // Watch-analytics lifecycle (Story 29.1). Independent of the streaming
  // session above: keyed on the video itself, it opens a watch session on
  // play, beats every 30 s, and closes it on pause/end/unmount/unload.
  // The same wiring covers the Tauri desktop and Capacitor mobile shells,
  // which load this exact bundle — detectClient() tags which one.
  useEffect(() => {
    const el = videoRef.current;
    if (!el || !videoId) return;
    const watch = createWatchSession({
      videoId,
      getPosition: () => el.currentTime,
      quality: session?.mode || "auto",
    });
    const onPlay = () => watch.start();
    const onPause = () => watch.stop();
    const onEnded = () => watch.stop();
    el.addEventListener("play", onPlay);
    el.addEventListener("pause", onPause);
    el.addEventListener("ended", onEnded);
    return () => {
      el.removeEventListener("play", onPlay);
      el.removeEventListener("pause", onPause);
      el.removeEventListener("ended", onEnded);
      watch.dispose();
    };
  }, [videoId, session?.mode]);

  const changeSpeed = useCallback((dir: number) => {
    setSpeed((cur) => {
      const idx = SPEEDS.indexOf(cur);
      const next = SPEEDS[Math.min(SPEEDS.length - 1, Math.max(0, idx + dir))];
      if (videoRef.current) videoRef.current.playbackRate = next;
      return next;
    });
  }, []);

  // Keyboard map: Space/K play-pause, J/L ∓10s, ←/→ ∓5s, M mute,
  // ,/. frame-ish step, 0-9 seek %, F fullscreen, C captions, +/- speed.
  const onKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      const el = videoRef.current;
      if (!el) return;
      const k = e.key;
      if (k === " " || k === "k") {
        e.preventDefault();
        if (el.paused) void el.play();
        else el.pause();
      } else if (k === "j") el.currentTime -= 10;
      else if (k === "l") el.currentTime += 10;
      else if (k === "ArrowLeft") el.currentTime -= 5;
      else if (k === "ArrowRight") el.currentTime += 5;
      else if (k === "m") el.muted = !el.muted;
      else if (k === ",") el.currentTime -= 1 / 30;
      else if (k === ".") el.currentTime += 1 / 30;
      else if (/^[0-9]$/.test(k) && el.duration) el.currentTime = (Number(k) / 10) * el.duration;
      else if (k === "f") {
        if (document.fullscreenElement) void document.exitFullscreen();
        else void el.requestFullscreen?.();
      } else if (k === "c") {
        const tr = el.textTracks?.[0];
        if (tr) tr.mode = tr.mode === "showing" ? "hidden" : "showing";
      } else if (k === "+" || k === "=") changeSpeed(1);
      else if (k === "-") changeSpeed(-1);
    },
    [changeSpeed]
  );

  async function togglePip() {
    const el = videoRef.current as
      | (HTMLVideoElement & { requestPictureInPicture?: () => Promise<unknown> })
      | null;
    if (!el) return;
    try {
      if (document.pictureInPictureElement) await document.exitPictureInPicture();
      else await el.requestPictureInPicture?.();
    } catch {
      /* PiP unsupported / blocked — non-fatal */
    }
  }

  if (err) return <ErrorState kind="server" title={t("common.error")} description={err} />;
  if (!session) return <p className="mkt-loading">{t("common.loading")}</p>;

  const tracks = session.subtitles ?? [];
  const sigSource = session.manifest_url || session.direct_url || "";

  return (
    <section
      className="mkt-player"
      onKeyDown={onKeyDown}
      tabIndex={0}
      aria-label={t("video.tab.watch")}
    >
      <div className="mkt-player__bar">
        <Button variant="ghost" onClick={() => nav(-1)}>
          ← {t("player.back")}
        </Button>
        <div className="mkt-player__controls">
          <label className="mkt-field">
            <span className="mkt-field__label">{t("player.speed")}</span>
            <select
              value={speed}
              aria-label={t("player.speed")}
              onChange={(e) => {
                const v = Number(e.target.value);
                setSpeed(v);
                if (videoRef.current) videoRef.current.playbackRate = v;
              }}
            >
              {SPEEDS.map((s) => (
                <option key={s} value={s}>
                  {s}×
                </option>
              ))}
            </select>
          </label>
          <label className="mkt-field">
            <span className="mkt-field__label">{t("player.subtitleSize")}</span>
            <select
              value={captionScale}
              aria-label={t("player.subtitleSize")}
              onChange={(e) => setCaptionScale(Number(e.target.value))}
            >
              {CAPTION_SCALES.map((s) => (
                <option key={s} value={s}>
                  {Math.round(s * 100)}%
                </option>
              ))}
            </select>
          </label>
          <Button variant="secondary" onClick={togglePip}>
            {t("player.pip")}
          </Button>
        </div>
      </div>
      <div
        className="mkt-player__stage"
        style={{ ["--mkt-caption-scale" as string]: String(captionScale) }}
      >
        <video
          ref={videoRef}
          data-testid="mkt-video"
          className="mkt-player__video"
          controls
          playsInline
          preload="metadata"
        >
          {tracks.map((tr) => (
            <track
              key={`${tr.lang}:${tr.url}`}
              kind="subtitles"
              srcLang={tr.lang}
              label={tr.label || tr.lang}
              src={carrySig(tr.url, sigSource)}
            />
          ))}
        </video>
        {duration > 0 && chapters.length > 0 && (
          <div className="mkt-player__chapters" aria-hidden="true">
            {chapters.map((c) => (
              <span
                key={c.id}
                className="mkt-player__tick"
                title={c.title}
                style={{ insetInlineStart: `${(c.start_sec / duration) * 100}%` }}
              />
            ))}
          </div>
        )}
      </div>
    </section>
  );
}
