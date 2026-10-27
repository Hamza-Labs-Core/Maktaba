// Epic 46 — big screen's Series: every series the user can see (GET
// /api/series) and one series (GET /api/series/{id}, /episodes): Continue
// (the server's next_episode), a season picker and the lessons with their
// progress. A lesson plays from where the user left it, with the series named
// in the player's URL so its Next lesson knows what follows.
import { useEffect, useMemo, useState } from "react";
import { useParams } from "react-router-dom";
import { api, ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { seriesPictureUrl } from "../../pages/Library/SeriesBrowser";
import { bigPath, useBigNav } from "../nav";
import { Empty, Failed, Grid, Loading, Picture, Progress, Screen, clock } from "../ui";

interface SeriesItem {
  id: string;
  name: string | null;
  poster_path?: string | null;
  poster_video_id?: string | null;
  episode_count: number | null;
  watched_count: number | null;
}

export function BigSeriesList() {
  const { t, n } = useI18n();
  const { go } = useBigNav();
  const [items, setItems] = useState<SeriesItem[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [reload, setReload] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setErr(null);
    api
      .get<{ items: SeriesItem[] }>("/api/series?sort=name")
      .then((r) => !cancelled && setItems(Array.isArray(r?.items) ? r.items : []))
      .catch(
        (e) => !cancelled && setErr(e instanceof ApiError ? e.problem.title : t("common.error"))
      );
    return () => {
      cancelled = true;
    };
  }, [reload, t]);

  if (err) return <Failed message={err} onRetry={() => setReload((x) => x + 1)} />;
  if (!items) return <Loading />;
  return (
    <Screen title={t("big.tab.series")} testId="big-series-list">
      {items.length === 0 ? (
        <Empty>{t("big.series.empty")}</Empty>
      ) : (
        <Grid id="series">
          {items.map((s, i) => {
            const total = s.episode_count ?? 0;
            const done = s.watched_count ?? 0;
            return (
              <button
                key={s.id}
                type="button"
                role="listitem"
                className="bs-tile"
                data-bs-focus={`series:${s.id}`}
                data-bs-autofocus={i === 0 || undefined}
                onClick={() => go(bigPath.series(s.id))}
              >
                <span className="bs-tile__art">
                  <Picture src={seriesPictureUrl(s)} />
                  {total > 0 && done > 0 && (
                    <Progress fraction={done / total} label={t("home.watched")} />
                  )}
                </span>
                <bdi className="bs-tile__title">{s.name || t("big.series.untitled")}</bdi>
                <span className="bs-tile__meta">
                  {t("big.lessonsWatched", { done: n(done), total: n(total) })}
                </span>
              </button>
            );
          })}
        </Grid>
      )}
    </Screen>
  );
}

interface Episode {
  video_id: string;
  season?: number;
  episode?: number;
  absolute_number?: number;
  title: string;
  duration_sec?: number;
  progress_pct: number;
  watched: boolean;
}
interface Season {
  season: number;
  episodes: Episode[];
}
interface Episodes {
  seasons: Season[];
  next_episode?: Episode;
}
interface Header {
  id: string;
  name: string;
  poster_path?: string;
  poster_video_id?: string;
  year?: number;
}

/** The seasons' lessons in order (specials last), for Next lesson. */
export function lessonsInOrder(seasons: Season[]): Episode[] {
  return [...seasons]
    .sort((a, b) => (a.season === 0 ? 1 : b.season === 0 ? -1 : a.season - b.season))
    .flatMap((s) =>
      [...s.episodes].sort(
        (a, b) =>
          (a.episode ?? a.absolute_number ?? Infinity) -
          (b.episode ?? b.absolute_number ?? Infinity)
      )
    );
}

/** Where a lesson resumes: its progress, unless it is watched or barely started. */
export function resumeAt(ep: Pick<Episode, "progress_pct" | "watched" | "duration_sec">): number {
  if (ep.watched || !ep.duration_sec || ep.progress_pct <= 0 || ep.progress_pct >= 95) return 0;
  return Math.floor((ep.progress_pct / 100) * ep.duration_sec);
}

export function fetchEpisodes(id: string): Promise<Episodes> {
  return api.get<Episodes>(`/api/series/${encodeURIComponent(id)}/episodes`).then((r) => ({
    seasons: (Array.isArray(r?.seasons) ? r.seasons : [])
      .filter((s): s is Season => s != null && typeof s === "object")
      .map((s) => ({ ...s, episodes: Array.isArray(s.episodes) ? s.episodes : [] })),
    next_episode: r?.next_episode,
  }));
}

export function BigSeries() {
  const { id = "" } = useParams();
  const { t, n } = useI18n();
  const { go } = useBigNav();
  const [header, setHeader] = useState<Header | null>(null);
  const [data, setData] = useState<Episodes | null>(null);
  const [err, setErr] = useState<{ message: string; status?: number } | null>(null);
  const [season, setSeason] = useState<number | null>(null);
  const [reload, setReload] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setErr(null);
    setData(null);
    api
      .get<Header>(`/api/series/${encodeURIComponent(id)}`)
      .then((h) => !cancelled && setHeader(h))
      .catch(() => !cancelled && setHeader(null));
    fetchEpisodes(id)
      .then((d) => {
        if (cancelled) return;
        setData(d);
        // the season of the lesson to continue, else the first
        const nextSeason = d.next_episode?.season;
        setSeason(
          nextSeason !== undefined && d.seasons.some((s) => s.season === nextSeason)
            ? nextSeason
            : (d.seasons[0]?.season ?? null)
        );
      })
      .catch(
        (e) =>
          !cancelled &&
          setErr(
            e instanceof ApiError
              ? { message: e.problem.title, status: e.status }
              : { message: t("common.error") }
          )
      );
    return () => {
      cancelled = true;
    };
  }, [id, reload, t]);

  const shown = useMemo(
    () => lessonsInOrder((data?.seasons ?? []).filter((s) => s.season === season)),
    [data, season]
  );

  if (err)
    return (
      <Failed
        message={err.status === 404 ? t("big.series.notFound") : err.message}
        onRetry={err.status === 404 ? undefined : () => setReload((x) => x + 1)}
      />
    );
  if (!data) return <Loading />;
  const play = (ep: Episode) => go(bigPath.watch(ep.video_id, { t: resumeAt(ep), series: id }));
  const next = data.next_episode;
  const all = lessonsInOrder(data.seasons);
  const watched = all.filter((e) => e.watched).length;
  const seasonLabel = (s: number) =>
    s === 0 ? t("big.season.specials") : t("big.season", { n: n(s) });

  return (
    <Screen testId="big-series">
      <section className="bs-hero bs-hero--series">
        <Picture src={header ? seriesPictureUrl(header) : null} className="bs-hero__art" />
        <div className="bs-hero__text">
          <h1 className="bs-hero__title">
            <bdi>{header?.name || t("big.series.untitled")}</bdi>
          </h1>
          <p className="bs-hero__meta">
            {t("big.lessonsWatched", { done: n(watched), total: n(all.length) })}
          </p>
          {next && (
            <div className="bs-hero__actions" data-bs-group="series-actions">
              <button
                type="button"
                className="bs-btn bs-btn--primary"
                data-bs-focus="series:continue"
                data-bs-autofocus
                onClick={() => play(next)}
              >
                ▶ {next.progress_pct > 0 ? t("big.resume") : t("big.series.start")}:{" "}
                <bdi>{next.title}</bdi>
              </button>
            </div>
          )}
        </div>
      </section>
      {data.seasons.length > 1 && (
        <nav className="bs-chips" data-bs-group="seasons" aria-label={t("big.seasons")}>
          {[...data.seasons]
            .sort((a, b) => (a.season === 0 ? 1 : b.season === 0 ? -1 : a.season - b.season))
            .map((s) => (
              <button
                key={s.season}
                type="button"
                className="bs-chip"
                data-bs-focus={`season:${s.season}`}
                aria-pressed={s.season === season}
                onClick={() => setSeason(s.season)}
              >
                {seasonLabel(s.season)}
              </button>
            ))}
        </nav>
      )}
      {shown.length === 0 ? (
        <Empty>{t("big.series.noLessons")}</Empty>
      ) : (
        <ol className="bs-lessons" data-bs-group={`lessons:${season}`}>
          {shown.map((ep, i) => {
            const no = ep.episode ?? ep.absolute_number;
            return (
              <li key={ep.video_id}>
                <button
                  type="button"
                  className="bs-lesson"
                  data-bs-focus={`lesson:${ep.video_id}`}
                  data-bs-autofocus={(!next && i === 0) || undefined}
                  onClick={() => play(ep)}
                >
                  <span className="bs-lesson__no">{no !== undefined ? n(no) : "•"}</span>
                  <bdi className="bs-lesson__title">{ep.title}</bdi>
                  {ep.duration_sec ? (
                    <span className="bs-lesson__dur">{clock(ep.duration_sec)}</span>
                  ) : null}
                  {ep.watched ? (
                    <span className="bs-lesson__done" aria-label={t("big.watched")}>
                      ✓
                    </span>
                  ) : ep.progress_pct > 0 ? (
                    <Progress fraction={ep.progress_pct / 100} label={t("home.watched")} />
                  ) : null}
                </button>
              </li>
            );
          })}
        </ol>
      )}
    </Screen>
  );
}
