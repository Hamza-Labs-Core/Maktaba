// Epic 46 — big screen's video page: the picture, title, length and
// description; Resume (the user's playback_state) or Play, and Play from the
// start; the chapters (each plays from there); the opening of the transcript.
//   GET /api/videos/{id}            (404 for a video the user may not see)
//   GET /api/videos/{id}/chapters   { items: [{ start_sec, title }] }
//   GET /api/videos/{id}/segments?from=0&to=240  { items: [{ start_sec, text }] }
import { useEffect, useState } from "react";
import { useParams, useSearchParams } from "react-router-dom";
import { api, ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { fetchMyVideo, markWatched, setFavorite, type MyVideo } from "../../lib/me";
import { bigPath, useBigNav } from "../nav";
import { Failed, Loading, Picture, Progress, Screen, clock, posterOf } from "../ui";

interface Video {
  id: string;
  title?: string | null;
  display_title?: string | null;
  filename: string;
  description?: string | null;
  duration_sec?: number | null;
  playback_state?: { position_sec: number; completed: boolean } | null;
}
interface Chapter {
  start_sec: number;
  title: string;
}
interface Segment {
  start_sec: number;
  text: string;
}

/** The transcript lines shown on the page. */
export const EXCERPT_LINES = 8;
/** A position under this is a fresh start, not something to resume. */
const RESUME_MIN_SEC = 30;

export function BigVideo() {
  const { id = "" } = useParams();
  const [params] = useSearchParams();
  const series = params.get("series");
  const { t, n } = useI18n();
  const { go } = useBigNav();
  const [video, setVideo] = useState<Video | null>(null);
  const [err, setErr] = useState<{ message: string; status?: number } | null>(null);
  const [chapters, setChapters] = useState<Chapter[]>([]);
  const [excerpt, setExcerpt] = useState<Segment[]>([]);
  const [reload, setReload] = useState(0);
  // the caller's own marks (Epic 45 phase 2): watched, favorite
  const [mine, setMine] = useState<MyVideo | null>(null);

  useEffect(() => {
    let cancelled = false;
    const enc = encodeURIComponent(id);
    setErr(null);
    setVideo(null);
    api
      .get<Video>(`/api/videos/${enc}`)
      .then((v) => !cancelled && setVideo(v))
      .catch(
        (e) =>
          !cancelled &&
          setErr(
            e instanceof ApiError
              ? { message: e.problem.title, status: e.status }
              : { message: t("common.error") }
          )
      );
    api
      .get<{ items: Chapter[] }>(`/api/videos/${enc}/chapters`)
      .then(
        (r) =>
          !cancelled &&
          setChapters(
            (Array.isArray(r?.items) ? r.items : []).filter((c) => typeof c?.start_sec === "number")
          )
      )
      .catch(() => !cancelled && setChapters([]));
    api
      .get<{ items: Segment[] }>(`/api/videos/${enc}/segments?from=0&to=240`)
      .then(
        (r) =>
          !cancelled &&
          setExcerpt(
            (Array.isArray(r?.items) ? r.items : []).filter((s) => s?.text).slice(0, EXCERPT_LINES)
          )
      )
      .catch(() => !cancelled && setExcerpt([]));
    setMine(null);
    fetchMyVideo(id)
      .then((m) => !cancelled && setMine(m))
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [id, reload, t]);

  if (err) {
    const missing = err.status === 404 || err.status === 403;
    return (
      <Failed
        message={missing ? t("video.notFound") : err.message}
        onRetry={missing ? undefined : () => setReload((x) => x + 1)}
      />
    );
  }
  if (!video) return <Loading />;

  const name = video.display_title || video.title || video.filename;
  const ps = video.playback_state;
  const dur = video.duration_sec ?? 0;
  const resume = ps && !ps.completed && ps.position_sec >= RESUME_MIN_SEC ? ps.position_sec : 0;
  const watch = (at: number) => go(bigPath.watch(id, { t: at, series }));

  return (
    <Screen testId="big-video">
      <section className="bs-hero bs-hero--video">
        <Picture src={posterOf(id)} className="bs-hero__art" />
        <div className="bs-hero__text">
          <h1 className="bs-hero__title">
            <bdi>{name}</bdi>
          </h1>
          <p className="bs-hero__meta">
            {dur > 0 && <span>{clock(dur)}</span>}
            {ps?.completed && <span>✓ {t("big.watched")}</span>}
            {resume > 0 && dur > 0 && (
              <>
                <Progress fraction={resume / dur} label={t("home.watched")} />
                <span>{t("big.leftAt", { at: clock(resume) })}</span>
              </>
            )}
          </p>
          <div className="bs-hero__actions" data-bs-group="video-actions">
            <button
              type="button"
              className="bs-btn bs-btn--primary"
              data-bs-focus="video:play"
              data-bs-autofocus
              onClick={() => watch(resume)}
            >
              ▶ {resume > 0 ? t("big.resume") : t("video.play")}
            </button>
            {resume > 0 && (
              <button
                type="button"
                className="bs-btn"
                data-bs-focus="video:start"
                onClick={() => watch(0)}
              >
                ⟲ {t("big.fromStart")}
              </button>
            )}
            {mine && (
              <>
                <button
                  type="button"
                  className="bs-btn"
                  data-bs-focus="video:watched"
                  aria-pressed={mine.completed}
                  onClick={() => {
                    const next = !mine.completed;
                    void markWatched([id], next)
                      .then(() => setMine((m) => (m ? { ...m, completed: next } : m)))
                      .catch(() => undefined);
                  }}
                >
                  {mine.completed ? `✓ ${t("video.watched")}` : t("video.markWatched")}
                </button>
                <button
                  type="button"
                  className="bs-btn"
                  data-bs-focus="video:favorite"
                  aria-pressed={mine.favorite}
                  onClick={() => {
                    const next = !mine.favorite;
                    void setFavorite([id], next)
                      .then(() => setMine((m) => (m ? { ...m, favorite: next } : m)))
                      .catch(() => undefined);
                  }}
                >
                  {mine.favorite ? `★ ${t("video.favorited")}` : `☆ ${t("video.favorite")}`}
                </button>
              </>
            )}
          </div>
          {video.description && (
            <p className="bs-hero__desc" dir="auto">
              {video.description}
            </p>
          )}
        </div>
      </section>

      {chapters.length > 0 && (
        <section className="bs-row">
          <h2 className="bs-row__title">
            {t("video.tab.chapters")} <span className="bs-count">{n(chapters.length)}</span>
          </h2>
          <div className="bs-row__scroller" data-bs-group="row:chapters" role="list">
            {chapters.map((c, i) => (
              <button
                key={`${i}:${c.start_sec}`}
                type="button"
                role="listitem"
                className="bs-tile bs-tile--text"
                data-bs-focus={`chapter:${i}`}
                onClick={() => watch(c.start_sec)}
              >
                <span className="bs-tile__meta">{clock(c.start_sec)}</span>
                <bdi className="bs-tile__title">{c.title}</bdi>
              </button>
            ))}
          </div>
        </section>
      )}

      {excerpt.length > 0 && (
        <section className="bs-row bs-excerpt">
          <h2 className="bs-row__title">{t("video.tab.transcript")}</h2>
          <div className="bs-excerpt__lines" data-bs-group="excerpt">
            {excerpt.map((s, i) => (
              <button
                key={`${i}:${s.start_sec}`}
                type="button"
                className="bs-line"
                data-bs-focus={`line:${i}`}
                onClick={() => watch(s.start_sec)}
              >
                <span className="bs-line__at">{clock(s.start_sec)}</span>
                <span className="bs-line__text" dir="auto">
                  {s.text}
                </span>
              </button>
            ))}
          </div>
        </section>
      )}
    </Screen>
  );
}
