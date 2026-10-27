// Epic 45 Phase 2 (story 45.14) — the video page's own actions and its place in
// a series:
//
//   Resume (when you stopped part-way), Mark watched / unwatched, Favorite and
//   Add to playlist, from GET /api/me/videos/{id} (the caller's marks); admins
//   also Feature on Home (story 45.21);
//   for a lesson of a series, "Lesson n of m", the series' progress and the
//   Previous / Next lessons (GET /api/series/{id}/episodes).
//
// A server without these routes (or a video the caller may not see) shows
// nothing here: the page keeps its Play button.
import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../lib/api";
import { useI18n } from "../lib/i18n";
import {
  fetchMyVideo,
  markWatched,
  seriesPosition,
  setFavorite,
  type MyVideo,
  type SeriesPosition,
} from "../lib/me";
import { watchPath } from "../lib/playQueue";
import { formatDuration } from "./VideoViews";
import { AddToPlaylist } from "./AddToPlaylist";
import { FeatureOnHome } from "./FeatureOnHome";

/** Resume is offered past the first 30 seconds of an unfinished video. */
export const RESUME_MIN_SEC = 30;

export function VideoActions({ videoId }: { videoId: string }) {
  const { t, n } = useI18n();
  const toast = useToast();
  const [mine, setMine] = useState<MyVideo | null>(null);
  const [pos, setPos] = useState<SeriesPosition | null>(null);
  const [busy, setBusy] = useState(false);
  const [playlistOpen, setPlaylistOpen] = useState(false);
  // the video shown now: an answer for a video navigated away from (Next lesson
  // clicked before Mark watched came back) never lands on the next one
  const shown = useRef(videoId);
  shown.current = videoId;

  useEffect(() => {
    let cancelled = false;
    setMine(null);
    setPos(null);
    fetchMyVideo(videoId)
      .then((m) => {
        if (cancelled) return;
        setMine(m);
        if (m.series) {
          seriesPosition(m.series, videoId)
            .then((p) => !cancelled && setPos(p))
            .catch(() => {});
        }
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [videoId]);

  if (!mine) return null;

  const fail = (e: unknown) =>
    toast.show({
      tone: "error",
      message: `${t("browse.bulk.error")}: ${
        e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error")
      }`,
    });

  async function toggleWatched() {
    if (!mine) return;
    const id = videoId;
    const series = mine.series;
    const next = !mine.completed;
    setBusy(true);
    try {
      await markWatched([id], next);
      if (shown.current !== id) return;
      setMine((m) => (m && m.video_id === id ? { ...m, completed: next, position_sec: 0 } : m));
      // the series' "n of m watched" counts this lesson now
      if (series) {
        seriesPosition(series, id)
          .then((p) => shown.current === id && setPos(p))
          .catch(() => {});
      }
      toast.show({
        tone: "success",
        message: t(next ? "video.markedWatched" : "video.markedUnwatched"),
      });
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }

  async function toggleFavorite() {
    if (!mine) return;
    const id = videoId;
    const next = !mine.favorite;
    setBusy(true);
    try {
      await setFavorite([id], next);
      setMine((m) => (m && m.video_id === id ? { ...m, favorite: next } : m));
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }

  const resumeAt =
    !mine.completed && mine.position_sec > RESUME_MIN_SEC ? Math.floor(mine.position_sec) : 0;

  return (
    <div className="mkt-video__mine">
      <div className="mkt-video__actions" role="group" aria-label={t("video.actions")}>
        {resumeAt > 0 && (
          <Link className="mkt-btn mkt-btn--primary" to={`${watchPath(videoId)}?t=${resumeAt}`}>
            ▶ {t("video.resumeAt", { at: formatDuration(resumeAt) })}
          </Link>
        )}
        <Button
          variant="secondary"
          aria-pressed={mine.completed}
          disabled={busy}
          onClick={() => void toggleWatched()}
        >
          {mine.completed ? `✓ ${t("video.watched")}` : t("video.markWatched")}
        </Button>
        <Button
          variant="secondary"
          aria-pressed={mine.favorite}
          disabled={busy}
          onClick={() => void toggleFavorite()}
        >
          <span aria-hidden="true">{mine.favorite ? "♥" : "♡"}</span>{" "}
          {mine.favorite ? t("video.favorited") : t("video.favorite")}
        </Button>
        <Button variant="ghost" onClick={() => setPlaylistOpen(true)}>
          + {t("video.addToPlaylist")}
        </Button>
        <FeatureOnHome videoId={videoId} />
      </div>
      {mine.playlists.length > 0 && (
        <p className="mkt-muted mkt-video__inlists">
          {t("video.inPlaylists")}{" "}
          {mine.playlists.map((p, i) => (
            <span key={p.id}>
              {i > 0 && t("video.listSep")}
              <Link to={`/playlists/${encodeURIComponent(p.id)}`}>
                <bdi>{p.name}</bdi>
              </Link>
            </span>
          ))}
        </p>
      )}
      {pos && (
        <nav className="mkt-video__series" aria-label={t("video.series.nav")}>
          <p>
            {t("video.series.lesson", { n: n(pos.index + 1), total: n(pos.total) })}{" "}
            <Link to={`/series/${encodeURIComponent(pos.series.id)}`}>
              <bdi>{pos.series.name}</bdi>
            </Link>
          </p>
          <ProgressBar
            value={pos.total > 0 ? Math.round((pos.watched / pos.total) * 100) : 0}
            label={t("video.series.progress", { n: n(pos.watched), total: n(pos.total) })}
          />
          <span className="mkt-muted">
            {t("video.series.progress", { n: n(pos.watched), total: n(pos.total) })}
          </span>
          <span className="mkt-video__series-nav">
            {pos.previous && (
              <Link
                className="mkt-btn mkt-btn--ghost"
                to={`/videos/${encodeURIComponent(pos.previous.video_id)}`}
                rel="prev"
              >
                {t("video.series.previous")}: <bdi>{pos.previous.title}</bdi>
              </Link>
            )}
            {pos.next && (
              <Link
                className="mkt-btn mkt-btn--ghost"
                to={`/videos/${encodeURIComponent(pos.next.video_id)}`}
                rel="next"
              >
                {t("video.series.next")}: <bdi>{pos.next.title}</bdi>
              </Link>
            )}
          </span>
        </nav>
      )}
      <AddToPlaylist
        open={playlistOpen}
        videoIds={[videoId]}
        onClose={() => setPlaylistOpen(false)}
        onAdded={(p) =>
          setMine((m) =>
            m && !m.playlists.some((x) => x.id === p.id)
              ? { ...m, playlists: [...m.playlists, p] }
              : m
          )
        }
      />
    </div>
  );
}
