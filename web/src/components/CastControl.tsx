// The player's Cast button and mini controller (Epic 58 story 58.06; lib/cast/sender.ts).
//
// The button shows only where casting can work: Chrome, the home's HTTPS address, Maktaba's
// receiver registered. Pressed, Chrome's own picker asks for a TV, and the video (or the server
// queue it plays) goes there with a cast grant. While the TV plays it, the player here pauses
// and this bar shows the TV's state; when the TV moves to the queue's next video the page
// follows it, and when the cast ends the player here picks up where the TV stopped.
import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { bidiIsolate, useI18n } from "../lib/i18n";
import { castSender, type CastSender, type CastView } from "../lib/cast/sender";
import { currentItem, queueHas, readServerQueue, watchPath } from "../lib/playQueue";

export function useCastView(sender: CastSender): CastView {
  const [view, setView] = useState<CastView>(sender.current);
  useEffect(() => {
    void sender.start();
    return sender.subscribe(setView);
  }, [sender]);
  return view;
}

function clock(sec: number): string {
  const s = Math.max(0, Math.floor(sec));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const ss = String(s % 60).padStart(2, "0");
  return h > 0 ? `${h}:${String(m).padStart(2, "0")}:${ss}` : `${m}:${ss}`;
}

export function CastControl({
  videoId,
  el,
  sender = castSender(),
}: {
  videoId: string;
  el: HTMLVideoElement | null;
  sender?: CastSender;
}) {
  const { t } = useI18n();
  const nav = useNavigate();
  const view = useCastView(sender);
  const [hint, setHint] = useState(false);
  const casting = view.device !== null;
  const here = casting && view.videoId === videoId;
  const last = useRef<{ videoId: string | null; at: number; casting: boolean }>({
    videoId: null,
    at: 0,
    casting: false,
  });

  // the TV plays this video: the player here stays paused
  useEffect(() => {
    if (here && el && !el.paused) el.pause();
  }, [here, el, view.paused]);

  useEffect(() => {
    const prev = last.current;
    // the TV moved on (the queue's next video): follow it, from the one this page showed
    if (
      casting &&
      prev.casting &&
      view.videoId &&
      prev.videoId === videoId &&
      view.videoId !== videoId
    )
      nav(watchPath(view.videoId));
    // the cast ended on this video: pick up where the TV stopped
    if (!casting && prev.casting && prev.videoId === videoId && el && prev.at > 0)
      el.currentTime = prev.at;
    last.current = { videoId: view.videoId, at: view.currentTime, casting };
  }, [casting, view.videoId, view.currentTime, videoId, el, nav]);

  if (!view.available) return null;

  async function start() {
    setHint(false);
    if (view.state === "none") {
      setHint(true);
      return;
    }
    const q = readServerQueue();
    const item = q && queueHas(videoId) ? currentItem(q) : null;
    const ok = await sender.cast({
      videoId,
      queueId: item ? q!.id : undefined,
      queuePos: item?.pos,
      startSec: el?.currentTime ?? 0,
    });
    if (ok) el?.pause();
  }

  return (
    <div className="mkt-cast" data-testid="mkt-cast">
      {!here && (
        <Button
          variant="secondary"
          onClick={() => void start()}
          disabled={view.state === "connecting"}
          aria-label={t("cast.button")}
        >
          <CastIcon />
          {view.state === "connecting" ? t("cast.connecting") : t("cast.button")}
        </Button>
      )}
      {here && (
        <div
          className="mkt-cast__mini"
          role="group"
          aria-label={t("cast.button")}
          data-testid="mkt-cast-mini"
        >
          <CastIcon />
          <span className="mkt-cast__device">
            {t("cast.castingTo", { device: bidiIsolate(view.device ?? "") })}
          </span>
          <span className="mkt-cast__time" dir="ltr">
            {clock(view.currentTime)}
            {view.duration > 0 ? ` / ${clock(view.duration)}` : ""}
          </span>
          <Button variant="secondary" onClick={() => sender.playOrPause()}>
            {view.paused ? t("cast.play") : t("cast.pause")}
          </Button>
          <Button variant="secondary" onClick={() => sender.stop()}>
            {t("cast.stop")}
          </Button>
        </div>
      )}
      {hint && (
        <p className="mkt-cast__note" role="status">
          {t("cast.noDevices")}
        </p>
      )}
      {view.error && (
        <p className="mkt-cast__note mkt-cast__note--error" role="alert">
          {t(view.error)}
        </p>
      )}
    </div>
  );
}

/** The Cast mark: a screen with waves from its corner (it does not mirror in RTL). */
function CastIcon() {
  return (
    <svg
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      aria-hidden="true"
      className="mkt-cast__icon"
    >
      <path d="M2 16.1A5 5 0 0 1 5.9 20M2 12.05A9 9 0 0 1 9.95 20M2 8V6a2 2 0 0 1 2-2h16a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-6" />
      <line x1="2" y1="20" x2="2.01" y2="20" />
    </svg>
  );
}
