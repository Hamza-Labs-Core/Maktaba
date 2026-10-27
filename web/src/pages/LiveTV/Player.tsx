// Story 27.7 — Live channel player; Epic 32.9 (LAN or remote) and Epic 34.6
// (channel switching) through the shared TV player (@maktaba/tvplayer).
//
// The "watching TV" experience, distinct from the on-demand player. It
// plays a channel's live HLS joined at the live edge and layers the
// affordances people expect from a TV:
//   • switching like a TV: ▲/▼ (keys, PageUp/PageDown, CH+/CH−, on-screen,
//     a vertical swipe on the picture), digits + Enter (or 1.5 s), L /
//     Backspace for the last channel, I or a tap for the mini-guide, G for
//     the full guide; rapid surfing moves the tuning card at once and tunes
//     300 ms after the last press, one tune in flight
//   • make before break: the channel playing keeps playing until the new
//     one has buffered its first segment; the tuning card (logo, number,
//     now, next) shows over the old picture from the press to the new first
//     frame, so there is never a black screen
//   • the neighbours' playlists (previous, next, last channel) are fetched
//     ahead — only channels already on air, and never a segment
//   • a tune banner, a mini-guide with now/next, "watch from beginning", PiP
//   • "Return to live" once the viewer has paused or seeked back behind the
//     live edge (27.7 AC5): it jumps back to the edge
//   • deep links: /live/{number}; every switch rewrites the URL (replace
//     while surfing, push once a channel has played 5 s)
//
// Two paths (Epic 32.9, from GET /api/tv/info's remote.via_relay):
//   LAN    the channel's HLS through the web port
//          (/stream/channel/{id}/index.m3u8; requesting it starts the
//          channel, so a first request may fail while the encoder warms up
//          and is retried)
//   remote away from home: POST /api/tv/remote/tune, a heartbeat every
//          60 s that swaps the signed token without reloading the media,
//          and the session ended on a switch, on stop and on unload
// A LAN playback that fails three times while remote viewing is available
// offers "Watch remotely"; a LAN failure re-reads /api/tv/info and moves to
// the remote path when the device is now away (Wi-Fi → mobile data, EC1).
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import {
  MiniGuide,
  NumberOverlay,
  PREPARE_RETRIES,
  PREPARE_RETRY_MS,
  SwitchLog,
  TuningCard,
  useTvSwitcher,
  type ResolveContext,
  type Stream,
  type SwitchError,
  type TvAction,
  type TvSwitcher,
} from "@maktaba/tvplayer";
import { useI18n } from "../../lib/i18n";
import {
  channelLogo,
  channelsApi,
  isAiring,
  liveStreamUrl,
  progressFraction,
  tvApi,
  type Channel,
  type NowEntry,
  type TvInfo,
} from "../../lib/channels";
import { RemoteSession } from "../../lib/remoteTv";
import { plainDetail } from "../../lib/errorText";

const BANNER_MS = 5000;
/** A channel that played this long is a history entry of its own (push, not replace). */
const SETTLED_PUSH_MS = 5000;
export const LIVE_RETRIES = PREPARE_RETRIES; // lazy tuning: the first playlist request may race the encoder
export const LIVE_RETRY_MS = PREPARE_RETRY_MS;
/** LAN failures in a row before "Watch remotely" is offered (32.9 AC8). */
export const LAN_FAILS_TO_OFFER = 3;
// How far behind the usual distance from the live edge the picture must be
// before "Return to live" shows (seconds); the usual distance is the
// smallest one seen while playing this channel.
export const BEHIND_LIVE_S = 10;

/** liveGap is how far (s) el plays behind the end of its seekable window. */
export function liveGap(el: HTMLMediaElement): number | null {
  const s = el.seekable;
  if (!s || s.length === 0) return null;
  const end = s.end(s.length - 1);
  if (!Number.isFinite(end)) return null;
  return Math.max(0, end - el.currentTime);
}

// useBehindLive watches the active picture: behind once its distance from
// the live edge grows BEHIND_LIVE_S past the usual one (a pause, or a seek
// back with the native controls). returnToLive seeks to the usual distance
// and plays; where the window is unknown it retunes the channel.
function useBehindLive(
  el: HTMLVideoElement | null,
  channelKey: string,
  retune: () => void
): { behind: boolean; returnToLive: () => void } {
  const [behind, setBehind] = useState(false);
  const baseline = useRef<number | null>(null);
  useEffect(() => {
    baseline.current = null;
    setBehind(false);
    if (!el) return;
    const check = () => {
      const gap = liveGap(el);
      if (gap === null) {
        setBehind(false);
        return;
      }
      if (!el.paused && (baseline.current === null || gap < baseline.current)) {
        baseline.current = gap;
      }
      setBehind(baseline.current !== null && gap - baseline.current > BEHIND_LIVE_S);
    };
    const events = ["timeupdate", "pause", "play", "seeked", "playing"] as const;
    for (const e of events) el.addEventListener(e, check);
    // a paused picture fires nothing while the edge moves on
    const timer = setInterval(check, 1000);
    return () => {
      for (const e of events) el.removeEventListener(e, check);
      clearInterval(timer);
    };
  }, [el, channelKey]);
  const returnToLive = useCallback(() => {
    if (!el) return;
    const s = el.seekable;
    if (s && s.length > 0 && Number.isFinite(s.end(s.length - 1))) {
      try {
        el.currentTime = Math.max(0, s.end(s.length - 1) - (baseline.current ?? 0));
      } catch {
        retune();
        return;
      }
      void el.play?.()?.catch?.(() => {});
    } else {
      retune();
    }
    setBehind(false);
  }, [el, retune]);
  return { behind, returnToLive };
}

type Path = "lan" | "remote";

/** The words of a switch error (32.9 AC6, 34.6 EC1). */
function errorKey(e: SwitchError | null, path: Path): string {
  switch (e?.code) {
    case "unsupported":
      return "live.noHls";
    case "budget_exhausted":
      return "live.remote.budget";
    case "user_streams_full":
      return "live.remote.userFull";
    case "remote_channels_full":
      return "live.remote.channelsFull";
    case "remote_storage_full":
      return "live.remote.storageFull";
    case "remote_unavailable":
      return "live.remote.notSetUp";
    case "remote_not_allowed":
      return "live.remote.notAllowed";
    case "remote_timeout":
      return "live.remote.timeout";
    case "no_such_channel":
      return "live.notFound.title";
  }
  return path === "remote" ? "live.remote.blocked" : "live.unavailable";
}

/** Telemetry of the switches (34.6 AC9), kept in memory for diagnostics. */
export const switchLog = new SwitchLog();

/**
 * Where the player's own links go: the desk app's pages, or (Epic 46) big
 * screen's, with the history state big screen keeps.
 */
export interface LiveRoutes {
  channel: (n: number) => string;
  guide: string;
  watch: (videoId: string) => string;
  state?: unknown;
}

const DESK_ROUTES: LiveRoutes = {
  channel: (n) => `/live/${n}`,
  guide: "/guide",
  watch: (videoId) => `/videos/${encodeURIComponent(videoId)}/watch?t=0`,
};

export function Player({ routes = DESK_ROUTES }: { routes?: LiveRoutes } = {}) {
  const { number } = useParams();
  const nav = useNavigate();
  const { t } = useI18n();

  const [lineup, setLineup] = useState<Channel[] | null>(null);
  const [nowEntries, setNowEntries] = useState<NowEntry[]>([]);
  const [info, setInfo] = useState<TvInfo | null>(null);
  const [path, setPath] = useState<Path | null>(null);
  const [lanFails, setLanFails] = useState(0);
  const [bannerVisible, setBannerVisible] = useState(true);
  const [miniGuide, setMiniGuide] = useState(false);
  const [, tick] = useState(0);
  const offsetRef = useRef(0);
  const bannerTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const settledAt = useRef<number>(0);

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

  // LAN or remote (32.9 AC3): the server knows whether we came through the relay.
  const readInfo = useCallback(
    () =>
      tvApi
        .info()
        .then((i) => {
          setInfo(i);
          return i;
        })
        .catch(() => null),
    []
  );
  useEffect(() => {
    void readInfo().then((i) => setPath(i?.remote?.via_relay ? "remote" : "lan"));
  }, [readInfo]);

  const channel = useMemo(
    () => lineup?.find((c) => c.number === targetNumber) ?? null,
    [lineup, targetNumber]
  );
  const nowById = useMemo(() => {
    const m = new Map<string, NowEntry>();
    for (const e of nowEntries) m.set(e.channel.id, e);
    return m;
  }, [nowEntries]);
  const nowRef = useRef(nowById);
  nowRef.current = nowById;
  const nowMs = Date.now() + offsetRef.current;
  const sw = useRef<TvSwitcher<Channel> | null>(null);

  const resolve = useCallback(
    async (c: Channel, ctx: ResolveContext): Promise<Stream> => {
      if (path === "remote") {
        return RemoteSession.open(c.id, ctx, { onEnded: () => sw.current?.retune() });
      }
      // a retry re-requests the playlist (the cache-buster defeats a cached error)
      const base = liveStreamUrl(c.id);
      return {
        url: ctx.attempt > 0 ? `${base}?retry=${ctx.attempt}` : base,
        path: "lan",
        release() {},
      };
    },
    [path]
  );

  const onSettled = useCallback(
    (c: Channel, from: Channel | null) => {
      setLanFails(0);
      const now = Date.now();
      const replace = !from || now - settledAt.current < SETTLED_PUSH_MS;
      settledAt.current = now;
      setBannerVisible(true);
      if (c.number !== Number(number))
        nav(routes.channel(c.number), { replace, state: routes.state });
    },
    [nav, number, routes]
  );

  const onAction = useCallback(
    (a: TvAction) => {
      switch (a.type) {
        case "guide":
          nav(routes.guide, { state: routes.state });
          return;
        case "info":
          setMiniGuide((v) => !v);
          return;
        case "enter":
          setBannerVisible((v) => !v);
          return;
        case "escape":
          setMiniGuide(false);
          return;
      }
    },
    [nav, routes]
  );

  // a LAN stream that died: is this device away now (EC1)? else count it (AC8)
  const onLanTrouble = useCallback(() => {
    if (path !== "lan") return;
    setLanFails((n) => n + 1);
    void readInfo().then((i) => {
      if (i?.remote?.via_relay && i.remote.available) setPath("remote");
    });
  }, [path, readInfo]);

  const player = useTvSwitcher<Channel>({
    channels: lineup,
    channel: path ? channel : null,
    resolve,
    pathKey: path ?? "",
    onSettled,
    onAction,
    telemetry: switchLog.sink,
    isWarm: (c) => nowRef.current.get(c.id)?.on_air ?? false,
    // LAN only: the playlists of neighbours already on air (asking for an idle
    // channel's playlist would start its encoder); remote neighbours would each
    // need a tune (a push and a stream slot), so none
    prefetchUrls: (ns) =>
      path === "lan"
        ? ns.filter((c) => nowRef.current.get(c.id)?.on_air).map((c) => liveStreamUrl(c.id))
        : [],
    onFatal: () => {
      onLanTrouble();
      sw.current?.retune();
    },
  });
  sw.current = player;
  const { state } = player;

  // a failed tune on the LAN counts towards "Watch remotely"
  const lastErr = useRef<SwitchError | null>(null);
  useEffect(() => {
    if (state.phase === "error" && state.error && state.error !== lastErr.current) {
      lastErr.current = state.error;
      // the only trail of a failed tune (no server report for channels yet)
      console.warn("[live] channel failed", {
        channel: number,
        path,
        code: state.error.code,
        retryable: state.error.retryable,
      });
      onLanTrouble();
    }
  }, [state.phase, state.error, onLanTrouble, number, path]);

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
  }, [bannerVisible, miniGuide, state.current]);

  const playing = state.current;
  const entry = playing ? (nowById.get(playing.id) ?? null) : null;
  // the element on screen; the switcher swaps elements on a channel change
  const [activeEl, setActiveEl] = useState<HTMLVideoElement | null>(null);
  useEffect(() => {
    const el = player.activeVideo();
    setActiveEl((prev) => (prev === el ? prev : el));
  });
  const { behind, returnToLive } = useBehindLive(
    activeEl,
    `${playing?.id ?? ""}:${player.activeIndex}`,
    player.retune
  );
  const watchFromStart = useCallback(() => {
    const vid = entry?.current?.video_id;
    if (vid) nav(routes.watch(vid), { state: routes.state });
  }, [entry, nav, routes]);

  async function togglePip() {
    const el = player.activeVideo() as
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

  const unmute = () => {
    const el = player.activeVideo();
    if (el) {
      el.muted = false;
      void el.play?.()?.catch?.(() => {});
    }
  };

  if (lineup !== null && !channel && !playing) {
    return (
      <section className="mkt-page">
        <ErrorState
          kind="not_found"
          title={t("live.notFound.title")}
          description={t("live.notFound.desc")}
          action={
            <Button onClick={() => nav(routes.guide, { state: routes.state })}>
              {t("live.openGuide")}
            </Button>
          }
        />
      </section>
    );
  }
  if (lineup === null) return <p className="mkt-loading">{t("common.loading")}</p>;

  const shown = state.target ?? playing ?? channel;
  const shownEntry = shown ? (nowById.get(shown.id) ?? null) : null;
  const current = entry?.current ?? null;
  const next = entry?.next ?? null;
  const prog = current ? progressFraction(current, nowMs) : null;
  const switching = state.target !== null || (state.phase === "loading" && !playing);
  const failedFirst = state.phase === "error" && !playing;
  const unsupported = state.error?.code === "unsupported" || player.mode === "unsupported";
  const degraded = failedFirst || unsupported;
  const directLink = channel && path !== "remote" ? liveStreamUrl(channel.id) : null;
  const offerRemote = path === "lan" && lanFails >= LAN_FAILS_TO_OFFER && !!info?.remote?.available;
  const remoteMissing = path === "remote" && info?.remote && !info.remote.available;

  let statusLine: string | null = null;
  if (state.phase === "error") {
    statusLine = t(errorKey(state.error, path ?? "lan"));
    // the home's reason, when it is a plain sentence (never a path or a Go error)
    if (
      state.error?.code === "remote_unavailable" &&
      state.error.message !== state.error.code &&
      plainDetail(state.error.message)
    ) {
      statusLine += ` (${state.error.message})`;
    }
  } else if (state.status === "remote_starting") statusLine = t("live.remote.starting");
  else if (switching) statusLine = path === "remote" ? t("live.remote.tuning") : t("live.tuning");

  const card = shown && (switching || state.phase === "error") && !degraded && (
    <TuningCard
      className="mkt-live__card"
      channel={{ id: shown.id, number: shown.number, name: shown.name, logo: channelLogo(shown) }}
      now={shownEntry?.current?.title ?? t("guide.noContent")}
      next={shownEntry?.next ? `${t("guide.nextLabel")}: ${shownEntry.next.title}` : undefined}
      status={statusLine}
      error={state.phase === "error"}
    />
  );
  const logo = playing ? channelLogo(playing) : null;

  return (
    <section
      className="mkt-live"
      onKeyDown={player.onKeyDown}
      tabIndex={0}
      aria-label={t("live.title")}
      data-testid="live-player"
      data-path={path ?? ""}
    >
      <div className="mkt-live__stage" onClick={player.mutedByPolicy ? unmute : undefined}>
        {[0, 1].map((i) => (
          <video
            key={i}
            ref={player.videoRef[i]}
            data-testid={i === player.activeIndex ? "live-video" : "live-video-standby"}
            className={
              "mkt-live__video" + (i === player.activeIndex ? " is-active" : " is-standby")
            }
            hidden={degraded}
            playsInline
            controls={i === player.activeIndex}
            aria-hidden={i === player.activeIndex ? undefined : true}
            tabIndex={i === player.activeIndex ? undefined : -1}
          />
        ))}
        {degraded && (
          <div className="mkt-live__slate" data-testid="live-slate">
            <p>
              {unsupported
                ? t("live.noHls")
                : remoteMissing
                  ? `${t("live.remote.notSetUp")}${info?.remote?.reason ? ` (${info.remote.reason})` : ""}`
                  : statusLine || t("live.unavailable")}
            </p>
            {directLink && (
              <p className="mkt-live__slate-hint">
                {t("live.openElsewhere")}{" "}
                <a href={directLink} data-testid="live-direct-link">
                  {t("live.directLink")}
                </a>
              </p>
            )}
            {offerRemote && (
              <Button onClick={() => setPath("remote")} data-testid="watch-remote">
                {t("live.remote.watch")}
              </Button>
            )}
          </div>
        )}
        {player.mutedByPolicy && !degraded && (
          <button
            type="button"
            className="mkt-live__unmute"
            onClick={(e) => {
              e.stopPropagation();
              unmute();
            }}
            data-testid="live-unmute-overlay"
          >
            <span aria-hidden="true">🔇</span> {t("live.tapForSound")}
          </button>
        )}
        {/* swipes and taps over the picture, above the native controls bar */}
        <div className="mkt-live__touch" ref={player.gestureRef} aria-hidden="true" />

        <span
          className={"mkt-live__badge" + (behind ? " is-behind" : "")}
          aria-label={behind ? t("live.behindLive") : t("live.liveLabel")}
        >
          ● {behind ? t("live.behindLive") : t("live.liveLabel")}
          {path === "remote" && <span className="mkt-live__via"> · {t("live.remote.badge")}</span>}
        </span>
        {card}
        {player.reconnecting && !degraded && (
          <div className="mkt-live__tuning" role="status" data-testid="live-reconnecting">
            {t("live.reconnecting")}
          </div>
        )}
        {(player.numberBuffer || player.noChannel !== null) && (
          <NumberOverlay
            className="mkt-live__numentry"
            text={player.numberBuffer || t("live.noChannel", { n: String(player.noChannel ?? "") })}
          />
        )}

        {bannerVisible && !miniGuide && !card && playing && (
          <div className="mkt-live__banner" data-testid="tune-banner">
            <div className="mkt-live__banner-id">
              {logo && <img className="mkt-live__logo" src={logo} alt="" />}
              <span className="mkt-guide__channum">{playing.number}</span>
              <strong>{playing.name}</strong>
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
          <MiniGuide
            className="mkt-live__miniguide"
            title={t("live.miniGuide")}
            closeLabel={t("common.close")}
            nextLabel={t("guide.nextLabel")}
            currentId={state.target?.id ?? playing?.id ?? null}
            rows={nowEntries.map((e) => ({
              channel: { id: e.channel.id, number: e.channel.number, name: e.channel.name },
              now: e.current ? e.current.title : t("guide.noContent"),
              next: e.next?.title,
            }))}
            onClose={() => setMiniGuide(false)}
            onPick={(id) => {
              setMiniGuide(false);
              const c = lineup.find((x) => x.id === id);
              if (c) player.tune(c);
            }}
          />
        )}
      </div>

      <div className="mkt-live__controls" role="toolbar" aria-label={t("live.controls")}>
        <Button variant="ghost" onClick={() => nav(routes.guide, { state: routes.state })}>
          ← {t("live.openGuide")}
        </Button>
        <Button variant="secondary" onClick={() => player.surf(1)} aria-label={t("live.channelUp")}>
          ▲ {t("live.channelUp")}
        </Button>
        <Button
          variant="secondary"
          onClick={() => player.surf(-1)}
          aria-label={t("live.channelDown")}
        >
          ▼ {t("live.channelDown")}
        </Button>
        {state.last && (
          <Button variant="secondary" onClick={player.last} aria-label={t("live.lastChannel")}>
            ↺ {t("live.lastChannel")}
          </Button>
        )}
        <Button variant="secondary" onClick={() => setMiniGuide((v) => !v)}>
          {t("live.guideBtn")}
        </Button>
        {behind && !degraded && (
          <Button variant="primary" onClick={returnToLive} data-testid="return-to-live">
            ⏭ {t("live.returnToLive")}
          </Button>
        )}
        {current?.video_id && current && isAiring(current, nowMs) && (
          <Button variant="secondary" onClick={watchFromStart}>
            {t("live.watchFromStart")}
          </Button>
        )}
        <Button variant="secondary" onClick={togglePip}>
          {t("player.pip")}
        </Button>
        {player.mutedByPolicy && (
          <Button variant="secondary" onClick={unmute} data-testid="live-unmute">
            {t("live.tapForSound")}
          </Button>
        )}
        {offerRemote && !degraded && (
          <Button variant="secondary" onClick={() => setPath("remote")} data-testid="watch-remote">
            {t("live.remote.watch")}
          </Button>
        )}
      </div>
    </section>
  );
}
