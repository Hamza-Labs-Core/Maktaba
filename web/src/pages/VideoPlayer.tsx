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
// Playback failures after the session opened (lib/playbackReport) are posted
// to POST /api/stream/sessions/{id}/errors, which logs them as
// `playback_failed`: the element's error on a direct file, hls.js / native
// HLS failures that end in a reopen or the error screen, and a stream that
// stops making progress while it should be playing (STALL_REPORT_MS).
// The rich control surface (speed, PiP, keyboard map, chapter ticks,
// caption sizing, resume) works on top of the <video> either way.
// Epic 45 Phase 2: at the end, Up next (components/UpNext) offers the play
// queue's next video or the series' next lesson, and starts it after 10 s.
import { useCallback, useEffect, useRef, useState } from "react";
import { useParams, useNavigate, useSearchParams, useLocation } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import {
  api,
  ApiError,
  PROBLEM_INSUFFICIENT_STORAGE,
  PROBLEM_VIDEO_UNAVAILABLE_OFFLINE,
} from "../lib/api";
import { useI18n } from "../lib/i18n";
import { attachVod, carrySig } from "@maktaba/tvplayer";
import { mediaError, reportPlaybackFailure, type PlaybackFailure } from "../lib/playbackReport";
import { UpNext } from "../components/UpNext";

interface OpenSessionResponse {
  session_id: string;
  mode: string;
  manifest_url?: string;
  direct_url?: string;
  expires_at: string;
  /** The video's length in the catalogue (absent when unknown). */
  duration_sec?: number;
  /**
   * Where in the video the stream starts (absent: its start): a transcode
   * opened at a position. Its times are the video's; before it, nothing plays.
   */
  start_sec?: number;
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
/** How long after `g` the next key belongs to the global `g …` leader. */
const LEADER_MS = 1200;

// Letter shortcuts match the physical key (e.code), so Caps Lock, Shift and
// a non-Latin layout (Arabic) still work.
function shortcutKey(e: React.KeyboardEvent): string {
  if (/^Key[A-Z]$/.test(e.code)) return e.code.slice(3).toLowerCase();
  return e.key.length === 1 ? e.key.toLowerCase() : e.key;
}

// Keys typed into a control (the speed select, a button) are that control's.
function isControlTarget(el: EventTarget | null): boolean {
  if (!(el instanceof HTMLElement)) return false;
  return Boolean(el.closest("input, select, textarea, button, a, [contenteditable='true']"));
}

/** Reopens of a stream gone within GONE_WINDOW_MS before the error screen. */
const GONE_REOPENS = 3;
const GONE_WINDOW_MS = 2 * 60_000;

/** Playing but no progress this long: the stall is reported (once per stream). */
export const STALL_REPORT_MS = 15_000;

/** At the video's end: within 2% of its length, 5 s at least. */
export function nearEnd(position: number, duration: number): boolean {
  if (!Number.isFinite(duration) || duration <= 0) return false;
  return position >= duration - Math.max(5, duration * 0.02);
}

export interface VideoPlayerProps {
  /**
   * Times (seconds) marked on the progress bar, like the chapter ticks —
   * the search matches page marks every match.
   */
  markers?: number[];
  /** The <video> element once it is on the page (null when it goes), so a parent can seek it. */
  onVideoElement?: (el: HTMLVideoElement | null) => void;
  /** Leaves out the Back button (a parent page has its own). */
  hideBack?: boolean;
  /** Starts playing as soon as the stream is attached (the viewer already pressed Play). */
  autoPlay?: boolean;
}

export function VideoPlayer({
  markers,
  onVideoElement,
  hideBack = false,
  autoPlay = false,
}: VideoPlayerProps = {}) {
  const { videoId } = useParams();
  const [params] = useSearchParams();
  const nav = useNavigate();
  const location = useLocation();
  const { t, dir } = useI18n();
  const [session, setSession] = useState<OpenSessionResponse | null>(null);
  const [err, setErr] = useState<{ message: string; status?: number } | null>(null);
  const [playError, setPlayError] = useState(false);
  const [reload, setReload] = useState(0);
  const leaderUntil = useRef(0);
  const [chapters, setChapters] = useState<Chapter[]>([]);
  const [duration, setDuration] = useState(0);
  const [speed, setSpeed] = useState(1);
  const [captionScale, setCaptionScale] = useState(1);
  const videoRef = useRef<HTMLVideoElement | null>(null);
  // the element as state too: Up next listens to it
  const [videoEl, setVideoElState] = useState<HTMLVideoElement | null>(null);
  const onVideoElementRef = useRef(onVideoElement);
  onVideoElementRef.current = onVideoElement;
  const setVideoEl = useCallback((el: HTMLVideoElement | null) => {
    videoRef.current = el;
    setVideoElState(el);
    onVideoElementRef.current?.(el);
  }, []);
  // Up next (and Play all) open the next video already playing
  const autoplay = autoPlay || (location.state as { autoplay?: boolean } | null)?.autoplay === true;
  const sessionIdRef = useRef<string | null>(null);
  // Where the next attachment starts: the ?t= deep link first, then the
  // position playback had reached when a signed URL had to be refreshed.
  const resumeRef = useRef<{ at: number; play: boolean } | null>(null);
  const refreshingRef = useRef(false);
  // When the stream went away and was opened again: past GONE_REOPENS within
  // GONE_WINDOW_MS, the error screen instead of a reopen loop.
  const goneRef = useRef<number[]>([]);
  // A stream attached at a position, not yet playing: the element's time is
  // not the viewer's yet, so no progress is posted (it would move the resume
  // point back).
  const startingRef = useRef(false);

  // Posts a playback failure of the stream now attached to the server's log.
  const report = useCallback(
    (s: OpenSessionResponse, f: PlaybackFailure) => {
      if (!videoId) return;
      reportPlaybackFailure(
        {
          sessionId: s.session_id,
          videoId,
          mode: s.mode,
          positionSec: videoRef.current?.currentTime ?? 0,
        },
        f
      );
    },
    [videoId]
  );

  const tParam = Number(params.get("t"));
  const startAt = Number.isFinite(tParam) && tParam > 0 ? tParam : 0;

  const failure = useCallback(
    (e: unknown) =>
      e instanceof ApiError
        ? {
            message:
              e.problem.type === PROBLEM_VIDEO_UNAVAILABLE_OFFLINE
                ? t("homeOffline.video")
                : e.problem.type === PROBLEM_INSUFFICIENT_STORAGE
                  ? t("player.diskFull")
                  : e.problem.title,
            status: e.status,
          }
        : { message: t("common.error") },
    [t]
  );

  // Open a streaming session for this video.
  useEffect(() => {
    if (!videoId) return;
    let cancelled = false;
    // A new video (or a retry) starts clean: no old error, no old stream.
    setErr(null);
    setPlayError(false);
    setSession(null);
    api
      .post<OpenSessionResponse>("/api/stream/sessions", {
        video_id: videoId,
        // a transcode starts there (?t=, the resume point)
        ...(startAt > 0 ? { start_sec: Math.floor(startAt) } : {}),
      })
      .then((s) => {
        if (cancelled) {
          // Left before the answer came: close the session it opened.
          if (s?.session_id) {
            void api
              .delete(`/api/stream/sessions/${encodeURIComponent(s.session_id)}`)
              .catch(() => {});
          }
          return;
        }
        if (!s?.session_id || !(s.manifest_url || s.direct_url)) {
          // Nothing to play: say so instead of showing a dead player.
          if (s?.session_id) {
            void api
              .delete(`/api/stream/sessions/${encodeURIComponent(s.session_id)}`)
              .catch(() => {});
          }
          setErr({ message: t("common.error") });
          return;
        }
        sessionIdRef.current = s.session_id;
        setSession(s);
      })
      .catch((e) => {
        if (cancelled) return;
        setErr(failure(e));
      });
    return () => {
      cancelled = true;
      const id = sessionIdRef.current;
      if (id) {
        sessionIdRef.current = null;
        void api.delete(`/api/stream/sessions/${encodeURIComponent(id)}`).catch(() => {});
      }
    };
  }, [videoId, startAt, t, failure, reload]);

  // Chapter ticks for the scrubber.
  useEffect(() => {
    if (!videoId) return;
    api
      .get<{ items: Chapter[] }>(`/api/videos/${encodeURIComponent(videoId)}/chapters`)
      .then((r) =>
        setChapters(
          (Array.isArray(r?.items) ? r.items : []).filter(
            (c): c is Chapter => c != null && typeof c.start_sec === "number"
          )
        )
      )
      .catch(() => setChapters([]));
  }, [videoId]);

  // Watch-progress reporting (server debounces to ≤1 write/s/session).
  useEffect(() => {
    const el = videoRef.current;
    if (!el || !session) return;
    let last = 0;
    const beat = (completed = false) => {
      if (startingRef.current) return;
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
    // Completed only at the video's real end (the catalogue's length): a stream
    // that stopped short of it ends early. With no length known at all, the end
    // of playback is all there is to go by.
    const onEnded = () => {
      const length = session.duration_sec || el.duration;
      beat(!(length > 0) || nearEnd(el.currentTime, length));
    };
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
    el.playbackRate = speed;
    startingRef.current = at > 0;
    const onPlaying = () => {
      startingRef.current = false;
    };
    el.addEventListener("playing", onPlaying);
    const resumePlay = () => {
      if (resume?.play) void el.play?.()?.catch?.(() => {});
    };
    // A start past the end (?t=99999) lands just before the end instead: the
    // video's end (a transcode's playlist ends where it has got to).
    const clampStart = () => {
      const d = session.duration_sec || el.duration;
      if (Number.isFinite(d) && d > 0 && el.currentTime > d - 1)
        el.currentTime = Math.max(0, d - 1);
    };
    // direct_url: the file itself (the API keeps it in manifest_url too, for the apps)
    if (session.direct_url || !session.manifest_url) {
      const onMeta = () => {
        if (at > 0) el.currentTime = at;
        clampStart();
        resumePlay();
      };
      const onError = () => {
        report(session, {
          engine: "direct",
          outcome: "fatal",
          ...mediaError(el),
          url: el.currentSrc || session.direct_url,
        });
        setPlayError(true);
      };
      el.addEventListener("loadedmetadata", onMeta, { once: true });
      el.addEventListener("error", onError);
      el.src = session.direct_url || "";
      return () => {
        el.removeEventListener("loadedmetadata", onMeta);
        el.removeEventListener("error", onError);
        el.removeEventListener("playing", onPlaying);
      };
    }
    let cancelled = false;
    // Expired signed URL: reopen the session (the server hands back the same
    // session with a freshly signed URL) and re-attach where we were. A stream
    // gone (its transcode stopped short, its session cleaned up) the same way:
    // the server opens a new one.
    const refresh = (position: number, wasPlaying: boolean) => {
      if (refreshingRef.current) return;
      refreshingRef.current = true;
      api
        .post<OpenSessionResponse>("/api/stream/sessions", {
          video_id: videoId,
          start_sec: Math.floor(position),
        })
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
          if (!cancelled) setErr(failure(e));
        })
        .finally(() => {
          refreshingRef.current = false;
        });
    };
    const gone = (position: number, wasPlaying: boolean) => {
      const now = Date.now();
      const recent = goneRef.current.filter((t) => now - t < GONE_WINDOW_MS);
      if (recent.length >= GONE_REOPENS) {
        if (!cancelled) {
          report(session, {
            engine: h.mode === "native" ? "native" : "hls.js",
            outcome: "fatal",
            code: "reopen_limit",
            message: `stream gone ${recent.length + 1} times in ${GONE_WINDOW_MS / 1000}s`,
          });
          setPlayError(true);
        }
        return;
      }
      goneRef.current = [...recent, now];
      refresh(position, wasPlaying);
    };
    const expiresAt = Date.parse(session.expires_at);
    const h = attachVod(el, session.manifest_url, {
      startAt: at,
      onAuthError: refresh,
      onGone: gone,
      onFatal: () => {
        if (!cancelled) setPlayError(true);
      },
      onError: (e) => {
        if (!cancelled) report(session, e);
      },
      isExpired: () => Number.isFinite(expiresAt) && Date.now() >= expiresAt - EXPIRY_SKEW_MS,
    });
    el.addEventListener("loadedmetadata", clampStart, { once: true });
    el.addEventListener("loadedmetadata", resumePlay, { once: true });
    // A transcode opened at a position starts there: a seek before it opens the
    // video again at the seek's position.
    const streamStart = session.start_sec ?? 0;
    const onSeeking = () => {
      if (streamStart > 0 && el.currentTime < streamStart - 1) refresh(el.currentTime, !el.paused);
    };
    el.addEventListener("seeking", onSeeking);
    return () => {
      cancelled = true;
      el.removeEventListener("loadedmetadata", clampStart);
      el.removeEventListener("loadedmetadata", resumePlay);
      el.removeEventListener("playing", onPlaying);
      el.removeEventListener("seeking", onSeeking);
      h.detach();
    };
    // speed is applied on attach only; the controls set it live.
  }, [session, videoId, startAt, t, failure, report]);

  // A stream that stops arriving while it should play (a direct file whose
  // reads stall on the NAS, segments that never come) raises no error of its
  // own: past STALL_REPORT_MS without progress it is reported, once a stream.
  useEffect(() => {
    const el = videoRef.current;
    if (!el || !session) return;
    let timer: ReturnType<typeof setTimeout> | null = null;
    let reported = false;
    const clear = () => {
      if (timer) clearTimeout(timer);
      timer = null;
    };
    const arm = () => {
      clear();
      if (reported || el.paused || el.ended) return;
      const at = el.currentTime;
      timer = setTimeout(() => {
        timer = null;
        if (reported || el.paused || el.ended || el.currentTime !== at) return;
        reported = true;
        report(session, {
          engine: session.direct_url || !session.manifest_url ? "direct" : "hls.js",
          outcome: "stalled",
          code: "stalled",
          message: `no progress for ${STALL_REPORT_MS / 1000}s (readyState ${el.readyState}, networkState ${el.networkState})`,
          url: session.direct_url || session.manifest_url,
        });
      }, STALL_REPORT_MS);
    };
    // progress re-arms the timer; a pause or the end stops it
    const onProgress = () => arm();
    el.addEventListener("play", arm);
    el.addEventListener("waiting", arm);
    el.addEventListener("timeupdate", onProgress);
    el.addEventListener("pause", clear);
    el.addEventListener("ended", clear);
    return () => {
      clear();
      el.removeEventListener("play", arm);
      el.removeEventListener("waiting", arm);
      el.removeEventListener("timeupdate", onProgress);
      el.removeEventListener("pause", clear);
      el.removeEventListener("ended", clear);
    };
  }, [session, report]);

  // Watch analytics (Epic 44 story 44.3): the server records the play from the
  // stream session's progress posts above and its close, with the app from
  // X-Maktaba-Client; the player no longer calls /api/watch/* (one record per
  // play, not two).

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
      // Leave browser shortcuts (Ctrl/Cmd+F, Cmd+C) and keys typed into a
      // control alone.
      if (e.ctrlKey || e.metaKey || e.altKey || isControlTarget(e.target)) return;
      const k = shortcutKey(e);
      // `g` starts the global `g …` leader: the next key is the app's.
      if (Date.now() < leaderUntil.current) {
        leaderUntil.current = 0;
        return;
      }
      if (k === "g") {
        leaderUntil.current = Date.now() + LEADER_MS;
        return;
      }
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

  // Back to where the viewer came from; a pasted link (no in-app history)
  // goes to the video's page instead of leaving the app.
  const goBack = () => {
    if (location.key === "default") nav(videoId ? `/videos/${videoId}` : "/library");
    else nav(-1);
  };
  const backButton = hideBack ? null : (
    <Button variant="ghost" onClick={goBack}>
      {dir === "rtl" ? "→" : "←"} {t("player.back")}
    </Button>
  );

  if (err) {
    const notFound = err.status === 404;
    return (
      <ErrorState
        kind={notFound ? "not_found" : err.status === 403 ? "permission" : "server"}
        title={notFound ? t("video.notFound") : t("common.error")}
        description={err.message}
        action={
          <>
            {backButton}
            {!notFound && err.status !== 403 && (
              <Button onClick={() => setReload((x) => x + 1)}>{t("common.retry")}</Button>
            )}
          </>
        }
      />
    );
  }
  if (!session)
    return (
      <div className="mkt-player__loading">
        {backButton}
        <p className="mkt-loading">{t("common.loading")}</p>
      </div>
    );

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
        {backButton}
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
      {playError && (
        <div className="mkt-player__error" role="alert">
          <p>{t("common.error")}</p>
          <Button onClick={() => setReload((x) => x + 1)}>{t("common.retry")}</Button>
        </div>
      )}
      <div
        className="mkt-player__stage"
        style={{ ["--mkt-caption-scale" as string]: String(captionScale) }}
      >
        <video
          ref={setVideoEl}
          data-testid="mkt-video"
          className="mkt-player__video"
          controls
          playsInline
          autoPlay={autoplay}
          preload="metadata"
          // the video's picture until it plays (a missing one leaves the frame)
          poster={videoId ? carrySig(`/api/videos/${videoId}/poster`, sigSource) : undefined}
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
                style={{
                  insetInlineStart: `${Math.min(100, Math.max(0, (c.start_sec / duration) * 100))}%`,
                }}
              />
            ))}
          </div>
        )}
        {duration > 0 && markers && markers.length > 0 && (
          <div className="mkt-player__marks" aria-hidden="true" data-testid="mkt-player-marks">
            {markers.map((at, i) => (
              <span
                key={`${i}:${at}`}
                className="mkt-player__mark"
                style={{
                  insetInlineStart: `${Math.min(100, Math.max(0, (at / duration) * 100))}%`,
                }}
              />
            ))}
          </div>
        )}
        {videoId && <UpNext videoId={videoId} el={videoEl} />}
      </div>
    </section>
  );
}
