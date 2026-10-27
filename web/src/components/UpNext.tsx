// Epic 45 Phase 2 (story 45.13) — Up next, over the player: when a video ends,
// the next one (the play queue's, else the next lesson of the video's series)
// with a 10-second countdown, Play now and Cancel. The next one is looked up
// near the end (the last 30 seconds) or at the end, never while the video is
// just opened, so a page that is only looked at reads nothing more.
import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { api } from "../lib/api";
import { useI18n } from "../lib/i18n";
import { fetchLessons, fetchMyVideo } from "../lib/me";
import { advanceTo, queueNext, watchPath } from "../lib/playQueue";

/** Seconds Up next waits before it starts the next video. */
export const UP_NEXT_SEC = 10;
/** How long before the end the next video is looked up. */
const LOOKUP_BEFORE_END_SEC = 30;

export interface NextVideo {
  id: string;
  title: string;
  fromQueue: boolean;
}

/** The video after videoId: the queue's next, else the next lesson of its series. */
export async function resolveNext(videoId: string): Promise<NextVideo | null> {
  const q = await queueNext(videoId);
  if (q) {
    let title = "";
    try {
      const v = await api.get<{ title?: string | null; filename?: string }>(
        `/api/videos/${encodeURIComponent(q)}`
      );
      title = v?.title || v?.filename || "";
    } catch {
      /* the title is only a nicety */
    }
    return { id: q, title, fromQueue: true };
  }
  try {
    const mine = await fetchMyVideo(videoId);
    if (!mine.series) return null;
    const lessons = await fetchLessons(mine.series.id);
    const at = lessons.findIndex((l) => l.video_id === videoId);
    const next = at >= 0 ? lessons[at + 1] : undefined;
    return next ? { id: next.video_id, title: next.title, fromQueue: false } : null;
  } catch {
    return null;
  }
}

export function UpNext({ videoId, el }: { videoId: string; el: HTMLVideoElement | null }) {
  const { t, n } = useI18n();
  const nav = useNavigate();
  const [next, setNext] = useState<NextVideo | null>(null);
  const [shown, setShown] = useState(false);
  const [left, setLeft] = useState(UP_NEXT_SEC);
  const lookup = useRef<Promise<NextVideo | null> | null>(null);

  // a new video starts afresh
  useEffect(() => {
    lookup.current = null;
    setNext(null);
    setShown(false);
    setLeft(UP_NEXT_SEC);
  }, [videoId]);

  const find = useCallback(() => {
    if (!lookup.current) {
      lookup.current = resolveNext(videoId);
      void lookup.current.then((x) => setNext(x));
    }
    return lookup.current;
  }, [videoId]);

  useEffect(() => {
    if (!el) return;
    const onTime = () => {
      const d = el.duration;
      if (Number.isFinite(d) && d > 0 && d - el.currentTime <= LOOKUP_BEFORE_END_SEC) void find();
    };
    const onEnded = () => {
      void find().then((x) => {
        if (x) {
          setLeft(UP_NEXT_SEC);
          setShown(true);
        }
      });
    };
    // playing again (seeking back) hides it
    const onPlay = () => setShown(false);
    el.addEventListener("timeupdate", onTime);
    el.addEventListener("ended", onEnded);
    el.addEventListener("play", onPlay);
    return () => {
      el.removeEventListener("timeupdate", onTime);
      el.removeEventListener("ended", onEnded);
      el.removeEventListener("play", onPlay);
    };
  }, [el, find]);

  const go = useCallback(() => {
    if (!next) return;
    if (next.fromQueue) advanceTo(next.id);
    setShown(false);
    nav(watchPath(next.id), { state: { autoplay: true } });
  }, [next, nav]);

  useEffect(() => {
    if (!shown) return;
    if (left <= 0) {
      go();
      return;
    }
    const id = setTimeout(() => setLeft((s) => s - 1), 1000);
    return () => clearTimeout(id);
  }, [shown, left, go]);

  if (!shown || !next) return null;
  return (
    <div className="mkt-upnext" role="dialog" aria-labelledby="mkt-upnext-title" aria-live="polite">
      <p id="mkt-upnext-title" className="mkt-upnext__label">
        {t("player.upNext.in", { n: n(left) })}
      </p>
      <p className="mkt-upnext__title">
        <bdi>{next.title || t("player.upNext.untitled")}</bdi>
      </p>
      <span className="mkt-upnext__actions">
        <Button variant="primary" onClick={go} autoFocus>
          ▶ {t("player.upNext.playNow")}
        </Button>
        <Button variant="ghost" onClick={() => setShown(false)}>
          {t("common.cancel")}
        </Button>
      </span>
    </div>
  );
}
