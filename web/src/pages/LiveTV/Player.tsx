// Story 27.7 — Live channel player.
//
// The "watching TV" experience, distinct from the on-demand player. It
// plays a channel's live HLS joined at the live edge and layers the
// affordances people expect from a TV:
//   • channel surfing — ch-up/ch-down (keys, on-screen, D-pad) + number
//     entry with a commit timeout; rapid surfing is debounced so holding
//     ch-up doesn't open N sessions (EC2)
//   • a tune banner — number/logo/name + current program + next-up +
//     progress, auto-hiding after ~5s, re-summoned with Info/OK (AC3)
//   • a mini-guide overlay (the guide button) over the still-playing
//     channel (AC4)
//   • "watch from beginning" — hands the current program's video_id to
//     the on-demand player at offset 0 (AC5)
//   • PiP where supported (AC6)
//
// Deep link: /live/{number} tunes directly; an unknown/disabled number
// resolves to an empty state (EC6).
//
// Playback is the channel's HLS playlist through the web port
// (/stream/channel/{id}/index.m3u8, proxied to the streaming service).
// Tuning is lazy — requesting the playlist starts the channel — so a
// first request can fail while the encoder warms up; the player retries a
// few times before showing the "can't play here" slate.
//
// Safari/iOS play the playlist natively; other browsers get hls.js,
// lazy-loaded into its own chunk (useLiveHls), which also rejoins the live
// edge after a mid-play stall and shows a "Reconnecting…" indicator.
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { useI18n } from "../../lib/i18n";
import {
  channelLogo,
  channelsApi,
  isAiring,
  liveStreamUrl,
  progressFraction,
  type Channel,
  type NowEntry,
} from "../../lib/channels";
import { useLiveHls } from "./useLiveHls";

const BANNER_MS = 5000;
const SURF_COMMIT_MS = 400; // debounce rapid surfing (EC2)
const NUMBER_COMMIT_MS = 1500; // number-entry commit window (AC2)
export const LIVE_RETRIES = 3; // lazy tuning: the first playlist request may race the encoder
export const LIVE_RETRY_MS = 2000;

export function Player() {
  const { number } = useParams();
  const nav = useNavigate();
  const { t } = useI18n();

  const [lineup, setLineup] = useState<Channel[] | null>(null);
  const [nowEntries, setNowEntries] = useState<NowEntry[]>([]);
  const [tuning, setTuning] = useState(false);
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [bannerVisible, setBannerVisible] = useState(true);
  const [miniGuide, setMiniGuide] = useState(false);
  const [numberBuffer, setNumberBuffer] = useState("");
  const [notFound, setNotFound] = useState(false);
  const [, tick] = useState(0);

  const videoRef = useRef<HTMLVideoElement | null>(null);
  const [videoEl, setVideoEl] = useState<HTMLVideoElement | null>(null);
  const attachVideo = useCallback((el: HTMLVideoElement | null) => {
    videoRef.current = el;
    setVideoEl(el);
  }, []);
  const offsetRef = useRef(0);
  const bannerTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const surfTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const numberTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const retryTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const targetNumber = Number(number);

  // Lineup + now data drive surfing order and the banner/mini-guide.
  const loadMeta = useCallback(() => {
    channelsApi
      .list({ enabled: true })
      .then((r) => setLineup([...r.items].sort((a, b) => a.number - b.number)))
      .catch(() => setLineup([]));
    channelsApi
      .now()
      .then((r) => {
        const srv = Date.parse(r.server_time);
        if (!Number.isNaN(srv)) offsetRef.current = srv - Date.now();
        setNowEntries(r.items);
      })
      .catch(() => setNowEntries([]));
  }, []);

  useEffect(() => {
    loadMeta();
    const refresh = setInterval(loadMeta, 30_000);
    const beat = setInterval(() => tick((x) => x + 1), 1000);
    return () => {
      clearInterval(refresh);
      clearInterval(beat);
    };
  }, [loadMeta]);

  const channel = useMemo(
    () => lineup?.find((c) => c.number === targetNumber) ?? null,
    [lineup, targetNumber]
  );
  const nowMs = Date.now() + offsetRef.current;
  const entry = useMemo(
    () => nowEntries.find((e) => e.channel.id === channel?.id) ?? null,
    [nowEntries, channel]
  );

  // Tune whenever the resolved channel changes: point the <video> at the
  // channel's playlist and wait for the first frame.
  useEffect(() => {
    if (lineup === null) return; // still loading lineup
    if (!channel) {
      setNotFound(true);
      return;
    }
    setNotFound(false);
    setTuning(true);
    setFailed(false);
    setAttempt(0);
    setBannerVisible(true);
    return () => {
      if (retryTimer.current) clearTimeout(retryTimer.current);
    };
  }, [channel, lineup]);

  const onPlaying = useCallback(() => setTuning(false), []);
  const onStreamFailed = useCallback(() => {
    if (retryTimer.current) clearTimeout(retryTimer.current);
    if (attempt + 1 >= LIVE_RETRIES) {
      setTuning(false);
      setFailed(true);
      return;
    }
    retryTimer.current = setTimeout(() => setAttempt((a) => a + 1), LIVE_RETRY_MS);
  }, [attempt]);

  // A retry re-requests the playlist (the cache-buster defeats a cached
  // error); attempt 0 is the plain URL.
  const base = channel ? liveStreamUrl(channel.id) : "";
  const src = base && attempt > 0 ? `${base}?retry=${attempt}` : base;
  const { mode: hlsMode, reconnecting } = useLiveHls(videoEl, failed ? "" : src, onStreamFailed);
  // With hls.js the element's own error events are MSE noise — hls.js
  // reports (and recovers) them itself, escalating via onStreamFailed.
  const onVideoError = useCallback(() => {
    if (hlsMode !== "hls") onStreamFailed();
  }, [hlsMode, onStreamFailed]);

  // Auto-hide the banner ~5s after it appears (suppressed while the
  // mini-guide is open to avoid stacked overlays, EC3).
  useEffect(() => {
    if (bannerTimer.current) clearTimeout(bannerTimer.current);
    if (bannerVisible && !miniGuide) {
      bannerTimer.current = setTimeout(() => setBannerVisible(false), BANNER_MS);
    }
    return () => {
      if (bannerTimer.current) clearTimeout(bannerTimer.current);
    };
  }, [bannerVisible, miniGuide, channel]);

  // surf debounces to the channel the user lands on for >SURF_COMMIT_MS.
  const surf = useCallback(
    (dir: number) => {
      if (!lineup || lineup.length === 0) return;
      const idx = lineup.findIndex((c) => c.number === targetNumber);
      const nextIdx = ((idx === -1 ? 0 : idx) + dir + lineup.length) % lineup.length;
      const nextNum = lineup[nextIdx].number;
      setBannerVisible(true);
      if (surfTimer.current) clearTimeout(surfTimer.current);
      surfTimer.current = setTimeout(() => nav(`/live/${nextNum}`), SURF_COMMIT_MS);
    },
    [lineup, targetNumber, nav]
  );

  const commitNumber = useCallback(
    (buf: string) => {
      const num = Number(buf);
      const exists = lineup?.some((c) => c.number === num);
      if (exists) nav(`/live/${num}`);
      setNumberBuffer("");
    },
    [lineup, nav]
  );

  const onDigit = useCallback(
    (d: string) => {
      setNumberBuffer((prev) => {
        const buf = (prev + d).slice(0, 4);
        if (numberTimer.current) clearTimeout(numberTimer.current);
        numberTimer.current = setTimeout(() => commitNumber(buf), NUMBER_COMMIT_MS);
        return buf;
      });
    },
    [commitNumber]
  );

  const watchFromStart = useCallback(() => {
    const vid = entry?.current?.video_id;
    if (vid) nav(`/videos/${encodeURIComponent(vid)}/watch?t=0`);
  }, [entry, nav]);

  async function togglePip() {
    const el = videoRef.current as
      | (HTMLVideoElement & { requestPictureInPicture?: () => Promise<unknown> })
      | null;
    if (!el) return;
    try {
      if (document.pictureInPictureElement) await document.exitPictureInPicture();
      else await el.requestPictureInPicture?.();
    } catch {
      /* PiP unsupported / blocked — non-fatal (AC6) */
    }
  }

  // D-pad / remote map (AC8): up/down surf, g opens mini-guide, OK/Enter
  // toggles banner, digits do number entry, b watches from beginning.
  const onKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      const k = e.key;
      if (k === "ArrowUp") {
        e.preventDefault();
        surf(1);
      } else if (k === "ArrowDown") {
        e.preventDefault();
        surf(-1);
      } else if (k === "g" || k === "G") {
        e.preventDefault();
        setMiniGuide((v) => !v);
      } else if (k === "Enter" || k === " " || k === "i" || k === "I") {
        e.preventDefault();
        setBannerVisible((v) => !v);
      } else if (/^[0-9]$/.test(k)) {
        e.preventDefault();
        onDigit(k);
      } else if (k === "Escape") {
        setMiniGuide(false);
      }
    },
    [surf, onDigit]
  );

  if (notFound) {
    return (
      <section className="mkt-page">
        <ErrorState
          kind="not_found"
          title={t("live.notFound.title")}
          description={t("live.notFound.desc")}
          action={<Button onClick={() => nav("/guide")}>{t("live.openGuide")}</Button>}
        />
      </section>
    );
  }
  if (lineup === null) return <p className="mkt-loading">{t("common.loading")}</p>;

  const current = entry?.current ?? null;
  const next = entry?.next ?? null;
  const prog = current ? progressFraction(current, nowMs) : null;
  const unsupported = hlsMode === "unsupported";
  const degraded = failed || !src || unsupported;
  const logo = channel ? channelLogo(channel) : null;

  return (
    <section
      className="mkt-live"
      onKeyDown={onKeyDown}
      tabIndex={0}
      aria-label={t("live.title")}
      data-testid="live-player"
    >
      <div className="mkt-live__stage">
        {degraded ? (
          <div className="mkt-live__slate" data-testid="live-slate">
            <p>{unsupported ? t("live.noHls") : t("live.unavailable")}</p>
            {src && (
              <p className="mkt-live__slate-hint">
                {t("live.openElsewhere")}{" "}
                <a href={base} data-testid="live-direct-link">
                  {t("live.directLink")}
                </a>
              </p>
            )}
          </div>
        ) : (
          <video
            ref={attachVideo}
            data-testid="live-video"
            className="mkt-live__video"
            autoPlay
            playsInline
            controls
            onPlaying={onPlaying}
            onLoadedData={onPlaying}
            onError={onVideoError}
          />
        )}

        <span className="mkt-live__badge" aria-label={t("live.liveLabel")}>
          ● {t("live.liveLabel")}
        </span>
        {tuning && <div className="mkt-live__tuning">{t("live.tuning")}</div>}
        {reconnecting && !tuning && !degraded && (
          <div className="mkt-live__tuning" role="status" data-testid="live-reconnecting">
            {t("live.reconnecting")}
          </div>
        )}
        {numberBuffer && (
          <div className="mkt-live__numentry" data-testid="number-entry">
            {numberBuffer}
          </div>
        )}

        {bannerVisible && !miniGuide && channel && (
          <div className="mkt-live__banner" data-testid="tune-banner">
            <div className="mkt-live__banner-id">
              {logo && <img className="mkt-live__logo" src={logo} alt="" />}
              <span className="mkt-guide__channum">{channel.number}</span>
              <strong>{channel.name}</strong>
            </div>
            <div className="mkt-live__banner-prog">
              <strong>{current ? current.title : t("guide.noContent")}</strong>
              {prog !== null && <ProgressBar value={prog * 100} label={t("guide.duration")} />}
              {next && (
                <span className="mkt-nowcard__next">
                  {t("guide.nextLabel")}: {next.title}
                </span>
              )}
            </div>
          </div>
        )}

        {miniGuide && (
          <div
            className="mkt-live__miniguide"
            data-testid="mini-guide"
            role="dialog"
            aria-label={t("live.miniGuide")}
          >
            <header>
              <strong>{t("live.miniGuide")}</strong>
              <Button size="sm" variant="ghost" onClick={() => setMiniGuide(false)}>
                {t("common.close")}
              </Button>
            </header>
            <ul>
              {nowEntries.map((e) => (
                <li key={e.channel.id}>
                  <button
                    type="button"
                    className={e.channel.id === channel?.id ? "is-current" : ""}
                    onClick={() => {
                      setMiniGuide(false);
                      nav(`/live/${e.channel.number}`);
                    }}
                  >
                    <span className="mkt-guide__channum">{e.channel.number}</span>
                    <span className="mkt-guide__channame">{e.channel.name}</span>
                    <span className="mkt-live__mg-prog">
                      {e.current ? e.current.title : t("guide.noContent")}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>

      <div className="mkt-live__controls" role="toolbar" aria-label={t("live.controls")}>
        <Button variant="ghost" onClick={() => nav("/guide")}>
          ← {t("live.openGuide")}
        </Button>
        <Button variant="secondary" onClick={() => surf(1)} aria-label={t("live.channelUp")}>
          ▲ {t("live.channelUp")}
        </Button>
        <Button variant="secondary" onClick={() => surf(-1)} aria-label={t("live.channelDown")}>
          ▼ {t("live.channelDown")}
        </Button>
        <Button variant="secondary" onClick={() => setMiniGuide((v) => !v)}>
          {t("live.guideBtn")}
        </Button>
        {current?.video_id && current && isAiring(current, nowMs) && (
          <Button variant="secondary" onClick={watchFromStart}>
            {t("live.watchFromStart")}
          </Button>
        )}
        <Button variant="secondary" onClick={togglePip}>
          {t("player.pip")}
        </Button>
      </div>
    </section>
  );
}
