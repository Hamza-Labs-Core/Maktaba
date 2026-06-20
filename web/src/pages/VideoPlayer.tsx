// Story 11.3 — Video player.
//
// Real streaming handshake (api/internal/handlers/streaming):
//   POST   /api/stream/sessions { video_id }
//     -> { session_id, mode, manifest_url, direct_url, expires_at }
//   POST   /api/stream/sessions/{id}/progress { position_sec[, completed] }
//   DELETE /api/stream/sessions/{id}                       (teardown)
//
// HLS: native where the browser plays application/vnd.apple.mpegurl
// (Safari/iOS); other browsers get the direct_url fallback. A bundled
// hls.js MSE player is intentionally NOT added here — that needs a new
// runtime dependency (lockfile change) which is out of this slice's
// scope; see the report's deferral note. The rich control surface
// (speed, PiP, keyboard map, chapter ticks, caption sizing, resume)
// works on top of the native <video> regardless.
import { useCallback, useEffect, useRef, useState } from "react";
import { useParams, useNavigate, useSearchParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { api, ApiError } from "../lib/api";
import { useI18n } from "../lib/i18n";
import { createWatchSession } from "../lib/watchSession";

interface OpenSessionResponse {
  session_id: string;
  mode: string;
  manifest_url?: string;
  direct_url?: string;
  expires_at: string;
}
interface Chapter {
  id: number;
  start_sec: number;
  title: string;
}

const SPEEDS = [0.5, 0.75, 1, 1.25, 1.5, 2];
const CAPTION_SCALES = [0.75, 1, 1.25, 1.5];

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
      if (startAt > 0) el.currentTime = startAt;
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
  }, [session, startAt]);

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

  const src = session.manifest_url || session.direct_url || "";

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
          src={src}
          controls
          playsInline
          preload="metadata"
        />
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
