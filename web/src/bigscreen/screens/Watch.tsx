// Epic 46 — big screen's player: the desk VideoPlayer's stream (session,
// HLS or direct play, signed-URL refresh, progress posts, failure reports) in
// its `bare` form, under 10-foot controls:
//
//   - the title, a progress bar with the chapters marked, the time;
//   - −30 s, −10 s, Play / Pause, +10 s, +30 s, Chapters, Subtitles, Audio
//     (when the engine lists more than one track), Next lesson (when the
//     player was opened from a series: ?series=);
//   - hidden after CONTROLS_IDLE_MS of no input while playing; any input
//     brings them back (playerKeys.ts says what each action does);
//   - at the end, Up next: the next lesson after a 10 s countdown (Settings →
//     "Play the next lesson automatically"), or Play now / Cancel.
// Back closes a panel, else leaves the player (the frame's Back).
import { useCallback, useEffect, useRef, useState } from "react";
import { useParams, useSearchParams } from "react-router-dom";
import { api } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { VideoPlayer } from "../../pages/VideoPlayer";
import { useFocusApi, useLayer } from "../focus/FocusProvider";
import { ACTION_EVENT, type ActionDetail } from "../input/actions";
import { bigPath, useBigNav } from "../nav";
import { readPrefs, SUBTITLE_SCALES } from "../prefs";
import { Panel, clock } from "../ui";
import { fetchEpisodes, lessonsInOrder, resumeAt } from "./Series";
import { lessonAfter, playerCommand, seekTo, type PlayerCommand } from "./playerKeys";

export const CONTROLS_IDLE_MS = 4000;
export const UP_NEXT_SECONDS = 10;

interface Chapter {
  start_sec: number;
  title: string;
}
interface NextLesson {
  video_id: string;
  title: string;
  progress_pct: number;
  watched: boolean;
  duration_sec?: number;
}
interface AudioTrackLike {
  id: string;
  label: string;
  language: string;
  enabled: boolean;
}
type PanelKind = "chapters" | "subtitles" | "audio";

function audioTracksOf(el: HTMLVideoElement | null): AudioTrackLike[] {
  const list = (el as unknown as { audioTracks?: ArrayLike<AudioTrackLike> } | null)?.audioTracks;
  return list ? Array.from(list) : [];
}

export function BigWatch() {
  const { videoId = "" } = useParams();
  const [params] = useSearchParams();
  const series = params.get("series");
  const { t, dir } = useI18n();
  const { go } = useBigNav();
  const focus = useFocusApi();
  const [el, setEl] = useState<HTMLVideoElement | null>(null);
  const [title, setTitle] = useState("");
  const [chapters, setChapters] = useState<Chapter[]>([]);
  const [next, setNext] = useState<NextLesson | null>(null);
  const [shown, setShown] = useState(true);
  const [panel, setPanel] = useState<PanelKind | null>(null);
  const [upNext, setUpNext] = useState<{ left: number | null } | null>(null);
  const [paused, setPaused] = useState(true);
  const [time, setTime] = useState(0);
  const [duration, setDuration] = useState(0);
  const [, setTracksSeen] = useState(0);

  const [captionScale, setCaptionScale] = useState(() => readPrefs().subtitleScale);
  const idleTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const playBtn = useRef<HTMLButtonElement | null>(null);

  // the title, the chapters, and what follows in the series
  useEffect(() => {
    let cancelled = false;
    const enc = encodeURIComponent(videoId);
    setTitle("");
    setChapters([]);
    setNext(null);
    setUpNext(null);
    api
      .get<{ title?: string | null; display_title?: string | null; filename?: string }>(
        `/api/videos/${enc}`
      )
      .then((v) => !cancelled && setTitle(v.display_title || v.title || v.filename || ""))
      .catch(() => undefined);
    api
      .get<{ items: Chapter[] }>(`/api/videos/${enc}/chapters`)
      .then(
        (r) =>
          !cancelled &&
          setChapters(
            (Array.isArray(r?.items) ? r.items : []).filter((c) => typeof c?.start_sec === "number")
          )
      )
      .catch(() => undefined);
    if (series) {
      fetchEpisodes(series)
        .then((d) => !cancelled && setNext(lessonAfter(lessonsInOrder(d.seasons), videoId)))
        .catch(() => undefined);
    }
    return () => {
      cancelled = true;
    };
  }, [videoId, series]);

  const tick = useCallback(
    () => setUpNext((u) => (u && u.left !== null ? { left: u.left - 1 } : u)),
    []
  );

  const playNext = useCallback(() => {
    if (!next) return;
    // replace: Back from the next lesson returns to where the first was opened
    go(bigPath.watch(next.video_id, { t: resumeAt(next), series }), { replace: true });
  }, [next, series, go]);

  // the element's state, and the subtitle preference once its tracks are there
  useEffect(() => {
    if (!el) return;
    const sync = () => {
      setPaused(el.paused);
      setTime(el.currentTime || 0);
      setDuration(Number.isFinite(el.duration) ? el.duration : 0);
    };
    let applied = false;
    const applyPrefs = () => {
      setTracksSeen((x) => x + 1);
      if (applied) return;
      const tracks = el.textTracks;
      if (!tracks || tracks.length === 0) return;
      applied = true;
      if (readPrefs().subtitlesOn) {
        for (let i = 0; i < tracks.length; i++) tracks[i]!.mode = i === 0 ? "showing" : "disabled";
      }
    };
    const onEnded = () => {
      sync();
      setUpNext({ left: readPrefs().autoplayNext ? UP_NEXT_SECONDS : null });
    };
    const events = ["play", "pause", "timeupdate", "durationchange", "loadedmetadata", "seeked"];
    for (const e of events) el.addEventListener(e, sync);
    el.addEventListener("loadedmetadata", applyPrefs);
    el.addEventListener("ended", onEnded);
    el.textTracks?.addEventListener?.("addtrack", applyPrefs);
    sync();
    applyPrefs();
    return () => {
      for (const e of events) el.removeEventListener(e, sync);
      el.removeEventListener("loadedmetadata", applyPrefs);
      el.removeEventListener("ended", onEnded);
      el.textTracks?.removeEventListener?.("addtrack", applyPrefs);
    };
  }, [el]);

  // the controls hide while watching
  const wake = useCallback(() => {
    setShown(true);
    if (idleTimer.current) clearTimeout(idleTimer.current);
    idleTimer.current = setTimeout(() => setShown(false), CONTROLS_IDLE_MS);
  }, []);
  useEffect(() => {
    if (paused || panel || upNext) {
      if (idleTimer.current) clearTimeout(idleTimer.current);
      setShown(true);
    } else wake();
  }, [paused, panel, upNext, wake]);
  useEffect(
    () => () => {
      if (idleTimer.current) clearTimeout(idleTimer.current);
    },
    []
  );
  // hidden controls give the focus up; shown again, Play / Pause has it
  const wasShown = useRef(shown);
  useEffect(() => {
    if (shown && !wasShown.current && !panel) playBtn.current?.focus({ preventScroll: true });
    if (!shown && document.activeElement instanceof HTMLElement) document.activeElement.blur();
    wasShown.current = shown;
  }, [shown, panel]);

  const run = useCallback(
    (cmd: Exclude<PlayerCommand, null>) => {
      if (!el) return;
      switch (cmd.kind) {
        case "seek":
          el.currentTime = seekTo(el.currentTime, cmd.by, el.duration);
          break;
        case "toggle":
          if (el.paused) void el.play?.()?.catch?.(() => {});
          else el.pause();
          break;
        case "play":
          void el.play?.()?.catch?.(() => {});
          break;
        case "pause":
          el.pause();
          break;
        case "restart":
          el.currentTime = 0;
          break;
        case "nextLesson":
          playNext();
          break;
        case "show":
          break;
      }
    },
    [el, playNext]
  );

  // the frame's actions: the player's first
  const state = useRef({ shown, dir, run, wake });
  state.current = { shown, dir, run, wake };
  useEffect(() => {
    const onAction = (e: Event) => {
      const { action } = (e as CustomEvent<ActionDetail>).detail;
      const s = state.current;
      const cmd = playerCommand(action, s.shown, s.dir);
      const wasHidden = !s.shown;
      s.wake();
      if (cmd) {
        e.preventDefault();
        s.run(cmd);
        return;
      }
      // the first press after the controls were hidden only shows them
      if (wasHidden && action !== "back") e.preventDefault();
    };
    window.addEventListener(ACTION_EVENT, onAction);
    return () => window.removeEventListener(ACTION_EVENT, onAction);
  }, []);

  const tracks = el?.textTracks ? Array.from(el.textTracks) : [];
  const showing = tracks.findIndex((tr) => tr.mode === "showing");
  const audio = audioTracksOf(el);
  const frac = duration > 0 ? Math.min(1, time / duration) : 0;

  return (
    <div
      className={`bs-watch${shown ? "" : " bs-watch--watching"}`}
      style={{ ["--mkt-caption-scale" as string]: String(captionScale) }}
      onPointerMove={wake}
      data-testid="big-watch"
    >
      <VideoPlayer bare autoPlay onVideoElement={setEl} />
      {el && (
        <div
          className="bs-watch__overlay"
          data-bs-skip={shown ? undefined : true}
          aria-hidden={!shown}
        >
          <header className="bs-watch__top">
            <h1 className="bs-watch__title">
              <bdi>{title}</bdi>
            </h1>
          </header>
          <div className="bs-watch__bottom">
            <div className="bs-watch__bar">
              <span className="bs-watch__time">{clock(time)}</span>
              <div
                className="bs-watch__track"
                role="progressbar"
                aria-label={t("big.player.position")}
                aria-valuemin={0}
                aria-valuemax={Math.round(duration)}
                aria-valuenow={Math.round(time)}
                aria-valuetext={`${clock(time)} / ${clock(duration)}`}
              >
                <span className="bs-watch__fill" style={{ inlineSize: `${frac * 100}%` }} />
                {duration > 0 &&
                  chapters.map((c, i) => (
                    <span
                      key={`${i}:${c.start_sec}`}
                      className="bs-watch__tick"
                      style={{
                        insetInlineStart: `${Math.min(100, (c.start_sec / duration) * 100)}%`,
                      }}
                    />
                  ))}
              </div>
              <span className="bs-watch__time">{clock(duration)}</span>
            </div>
            <div
              className="bs-watch__controls"
              data-bs-group="controls"
              role="toolbar"
              aria-label={t("live.controls")}
            >
              <Ctl
                id="back30"
                label={t("big.player.back", { s: 30 })}
                onClick={() => run({ kind: "seek", by: -30 })}
              >
                «30
              </Ctl>
              <Ctl
                id="back10"
                label={t("big.player.back", { s: 10 })}
                onClick={() => run({ kind: "seek", by: -10 })}
              >
                ‹10
              </Ctl>
              <button
                ref={playBtn}
                type="button"
                className="bs-ctl bs-ctl--main"
                data-bs-focus="ctl:play"
                data-bs-autofocus
                aria-label={paused ? t("big.player.play") : t("big.player.pause")}
                onClick={() => run({ kind: "toggle" })}
              >
                {paused ? "▶" : "❚❚"}
              </button>
              <Ctl
                id="fwd10"
                label={t("big.player.forward", { s: 10 })}
                onClick={() => run({ kind: "seek", by: 10 })}
              >
                10›
              </Ctl>
              <Ctl
                id="fwd30"
                label={t("big.player.forward", { s: 30 })}
                onClick={() => run({ kind: "seek", by: 30 })}
              >
                30»
              </Ctl>
              {chapters.length > 0 && (
                <Ctl id="chapters" wide onClick={() => setPanel("chapters")}>
                  {t("video.tab.chapters")}
                </Ctl>
              )}
              <Ctl id="subtitles" wide onClick={() => setPanel("subtitles")}>
                {t("big.player.subtitles")}
              </Ctl>
              {audio.length > 1 && (
                <Ctl id="audio" wide onClick={() => setPanel("audio")}>
                  {t("big.player.audio")}
                </Ctl>
              )}
              {next && (
                <Ctl id="next" wide onClick={playNext}>
                  {t("big.player.nextLesson")} ⏭
                </Ctl>
              )}
            </div>
          </div>
        </div>
      )}

      {panel === "chapters" && (
        <Panel title={t("video.tab.chapters")} onClose={() => setPanel(null)} testId="big-chapters">
          {chapters.map((c, i) => {
            const nextStart = chapters[i + 1]?.start_sec ?? Infinity;
            const current = time >= c.start_sec && time < nextStart;
            return (
              <button
                key={`${i}:${c.start_sec}`}
                type="button"
                className="bs-option"
                data-bs-focus={`pc:${i}`}
                data-bs-autofocus={current || undefined}
                aria-current={current || undefined}
                onClick={() => {
                  if (el) el.currentTime = c.start_sec;
                  setPanel(null);
                }}
              >
                <span className="bs-line__at">{clock(c.start_sec)}</span>
                <bdi>{c.title}</bdi>
              </button>
            );
          })}
        </Panel>
      )}

      {panel === "subtitles" && (
        <Panel
          title={t("big.player.subtitles")}
          onClose={() => setPanel(null)}
          testId="big-subtitles"
        >
          {[-1, ...tracks.map((_, i) => i)].map((i) => (
            <button
              key={i}
              type="button"
              className="bs-option"
              data-bs-focus={`ps:${i}`}
              data-bs-autofocus={showing === i || undefined}
              aria-pressed={showing === i}
              onClick={() => {
                tracks.forEach((tr, j) => (tr.mode = j === i ? "showing" : "disabled"));
                setTracksSeen((x) => x + 1);
              }}
            >
              {i < 0
                ? t("big.player.subtitlesOff")
                : tracks[i]!.label || tracks[i]!.language || `${i + 1}`}
            </button>
          ))}
          <button
            type="button"
            className="bs-option"
            data-bs-focus="ps:size"
            onClick={() => {
              const k = (SUBTITLE_SCALES as readonly number[]).indexOf(captionScale);
              setCaptionScale(SUBTITLE_SCALES[(k + 1) % SUBTITLE_SCALES.length]!);
            }}
          >
            {t("player.subtitleSize")}: {Math.round(captionScale * 100)}%
          </button>
        </Panel>
      )}

      {panel === "audio" && (
        <Panel title={t("big.player.audio")} onClose={() => setPanel(null)} testId="big-audio">
          {audio.map((a, i) => (
            <button
              key={a.id || i}
              type="button"
              className="bs-option"
              data-bs-focus={`pa:${i}`}
              data-bs-autofocus={a.enabled || undefined}
              aria-pressed={a.enabled}
              onClick={() => {
                audio.forEach((x, j) => (x.enabled = j === i));
                setTracksSeen((x) => x + 1);
              }}
            >
              {a.label || a.language || `${i + 1}`}
            </button>
          ))}
        </Panel>
      )}

      {upNext && (
        <UpNextCard
          next={next}
          left={upNext.left}
          onTick={tick}
          onPlay={playNext}
          onCancel={() => {
            setUpNext(null);
            focus.focusFirst();
          }}
        />
      )}
    </div>
  );
}

function Ctl({
  id,
  label,
  wide,
  onClick,
  children,
}: {
  id: string;
  label?: string;
  wide?: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      className={`bs-ctl${wide ? " bs-ctl--wide" : ""}`}
      data-bs-focus={`ctl:${id}`}
      aria-label={label}
      onClick={onClick}
    >
      {children}
    </button>
  );
}

/** The end of a video: the next lesson (counting down when autoplay is on), or the way back. */
function UpNextCard({
  next,
  left,
  onTick,
  onPlay,
  onCancel,
}: {
  next: NextLesson | null;
  left: number | null;
  onTick: () => void;
  onPlay: () => void;
  onCancel: () => void;
}) {
  const { t, n } = useI18n();
  const ref = useRef<HTMLDivElement | null>(null);
  useLayer(ref, onCancel, !!next);
  useEffect(() => {
    if (!next || left === null) return;
    if (left <= 0) {
      onPlay();
      return;
    }
    const id = setTimeout(onTick, 1000);
    return () => clearTimeout(id);
  }, [next, left, onTick, onPlay]);
  if (!next) return null;
  return (
    <div
      ref={ref}
      className="bs-upnext"
      role="dialog"
      aria-label={t("big.upNext")}
      data-bs-layer
      data-testid="big-upnext"
    >
      <p className="bs-upnext__kicker">
        {left !== null ? t("big.upNext.in", { s: n(Math.max(0, left)) }) : t("big.upNext")}
      </p>
      <p className="bs-upnext__title">
        <bdi>{next.title}</bdi>
      </p>
      <div className="bs-upnext__actions" data-bs-group="upnext">
        <button
          type="button"
          className="bs-btn bs-btn--primary"
          data-bs-focus="upnext:play"
          data-bs-autofocus
          onClick={onPlay}
        >
          ▶ {t("big.upNext.play")}
        </button>
        <button type="button" className="bs-btn" data-bs-focus="upnext:cancel" onClick={onCancel}>
          {t("common.cancel")}
        </button>
      </div>
    </div>
  );
}
