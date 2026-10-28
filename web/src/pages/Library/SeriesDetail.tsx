// Story 26.10 — Series detail (season → episode grid).
//
// Server contract:
//   GET  /api/series/{id}                  -> SeriesHeader
//   GET  /api/series/{id}/episodes         -> { seasons:[Season], numbering, next_episode? }
//   GET  /api/series/{id}/missing          -> { gaps:[{season,episode}], numbering }
//   POST /api/series/{id}/enrichment/accept-all  (26.6 batch-accept)
//
// Each season renders an episode grid, newest-added first as every list of
// videos (the server's order), or in episode order with ?sort=episode; gaps
// from /missing render as ghost cells ("Episode N — missing"), among the
// episodes in episode order and after them otherwise. A "Continue watching"
// button targets the next unwatched/in-progress episode in lesson order,
// whatever the sort. Season 0 is a "Specials" row. RTL-correct for Arabic.
import { useMemo, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { Badge } from "@ds/components/Badge/Badge";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { Skeleton } from "@ds/components/Skeleton/Skeleton";
import { HeadingPlaceholder, Loading } from "../../components/Placeholders";
import { usePageData } from "../../lib/usePageData";
import { VIDEO_ANSWERS, forgetAnswers } from "../../lib/pageCache";
import { useToast } from "@ds/components/Toast/Toast";
import { api } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import { useI18n } from "../../lib/i18n";
import { TitleRow } from "../../help/HelpLink";
import { seriesPictureUrl } from "./SeriesBrowser";
import { errorText } from "../../lib/errorText";
import { LoadError } from "../../components/LoadError";

interface Episode {
  video_id: string;
  season?: number;
  episode?: number;
  absolute_number?: number;
  title: string;
  thumbnail?: string;
  duration_sec?: number;
  progress_pct: number;
  watched: boolean;
}
interface Season {
  season: number;
  episodes: Episode[];
}
interface EpisodesResponse {
  seasons: Season[];
  numbering: string;
  next_episode?: Episode;
}
interface Gap {
  season: number;
  episode: number;
}
interface SeriesHeader {
  id: string;
  name: string;
  poster_path?: string;
  poster_video_id?: string;
  year?: number;
  numbering: string;
}

type Cell = { kind: "ep"; ep: Episode } | { kind: "gap"; no: number };

const cellNo = (c: Cell) =>
  c.kind === "gap" ? c.no : (c.ep.episode ?? c.ep.absolute_number ?? Number.POSITIVE_INFINITY);

export function SeriesDetail() {
  const { id } = useParams();
  const { t, n } = useI18n();
  const { user } = useAuth();
  const toast = useToast();
  const [accepting, setAccepting] = useState(false);
  const [headerBroken, setHeaderBroken] = useState(false);
  const [params, setParams] = useSearchParams();
  // the person's pick; without one, the server's newest-added order
  const byEpisode = params.get("sort") === "episode";

  // Three reads, each kept for a return (answers for a series already left
  // never land on the new one): the header and the gaps are extras, their
  // failure only leaves them out.
  const enc = encodeURIComponent(id ?? "");
  const headerRead = usePageData<SeriesHeader>(
    id ? `series:${id}:header` : null,
    () => api.get<SeriesHeader>(`/api/series/${enc}`),
    [t]
  );
  const episodesRead = usePageData<EpisodesResponse>(
    id ? `series:${id}:episodes:${byEpisode ? "episode" : "added"}` : null,
    () =>
      api
        .get<EpisodesResponse>(`/api/series/${enc}/episodes${byEpisode ? "?sort=episode" : ""}`)
        .then((r) => {
          const seasons = (Array.isArray(r?.seasons) ? r.seasons : [])
            .filter((s): s is Season => s != null && typeof s === "object")
            .map((s) => ({ ...s, episodes: Array.isArray(s.episodes) ? s.episodes : [] }));
          return { seasons, numbering: r?.numbering ?? "", next_episode: r?.next_episode };
        }),
    [t]
  );
  const gapsRead = usePageData<Gap[]>(
    id ? `series:${id}:missing` : null,
    () =>
      api
        .get<{ gaps: Gap[] }>(`/api/series/${enc}/missing`)
        .then((r) => (Array.isArray(r?.gaps) ? r.gaps : [])),
    [t]
  );
  const header = headerRead.err == null ? headerRead.data : null;
  const data = episodesRead.data;
  const err = episodesRead.err;
  const gaps = useMemo(
    () => (gapsRead.err == null ? (gapsRead.data ?? []) : []),
    [gapsRead.data, gapsRead.err]
  );
  function reload() {
    headerRead.reload();
    episodesRead.reload();
    gapsRead.reload();
  }

  // Index gaps by season so each season row can render its ghost cells.
  const gapsBySeason = useMemo(() => {
    const m = new Map<number, number[]>();
    for (const g of gaps) {
      if (!g || typeof g.season !== "number" || typeof g.episode !== "number") continue;
      m.set(g.season, [...(m.get(g.season) ?? []), g.episode]);
    }
    return m;
  }, [gaps]);

  // Seasons with episodes plus seasons that only have gaps, each as one
  // list of cells: the episodes in the server's order, the ghost cells
  // among them in episode order and after them otherwise.
  const seasons = useMemo(() => {
    if (!data) return [];
    const bySeason = new Map<number, Episode[]>();
    for (const s of data.seasons) bySeason.set(s.season, s.episodes);
    for (const season of gapsBySeason.keys()) if (!bySeason.has(season)) bySeason.set(season, []);
    return [...bySeason.entries()]
      .sort(([a], [b]) => a - b)
      .map(([season, eps]) => {
        const ghosts = (gapsBySeason.get(season) ?? [])
          .map((no): Cell => ({ kind: "gap", no }))
          .sort((a, b) => cellNo(a) - cellNo(b));
        const cells: Cell[] = [
          ...eps.filter((ep) => ep != null).map((ep): Cell => ({ kind: "ep", ep })),
          ...ghosts,
        ];
        if (byEpisode) cells.sort((a, b) => cellNo(a) - cellNo(b));
        return { season, cells };
      });
  }, [data, gapsBySeason, byEpisode]);

  async function acceptAll() {
    if (!id || accepting) return;
    setAccepting(true);
    try {
      await api.post(`/api/series/${encodeURIComponent(id)}/enrichment/accept-all`);
      toast.show({ message: t("series.acceptAll.done"), tone: "success" });
      // the lessons' titles and pictures, everywhere they show
      forgetAnswers(...VIDEO_ANSWERS);
      reload();
    } catch (e) {
      toast.show({
        message: errorText(e, t),
        tone: "error",
      });
    } finally {
      setAccepting(false);
    }
  }

  if (err != null) {
    return <LoadError error={err} onRetry={reload} backTo="/series" backLabel={t("nav.series")} />;
  }
  if (!data || !id) {
    return (
      <section className="mkt-page mkt-series-detail">
        <Loading loading={episodesRead.loading}>
          <HeadingPlaceholder />
          <section className="mkt-season" aria-hidden="true">
            <h2 className="mkt-season__title">
              <Skeleton variant="text" className="mkt-ph-title mkt-ph-inline" />
              {"\u00a0"}
            </h2>
            <ul className="mkt-episode-grid">
              {Array.from({ length: 8 }, (_, i) => (
                <li key={i}>
                  <div className="mkt-episode">
                    <div className="mkt-episode__thumb">
                      <Skeleton variant="rect" className="mkt-ph-full" />
                    </div>
                    <Skeleton variant="text" className="mkt-ph-line" width="2.5rem" />
                    <Skeleton variant="text" className="mkt-ph-line" width="80%" />
                  </div>
                </li>
              ))}
            </ul>
          </section>
        </Loading>
      </section>
    );
  }

  const title = header?.name ?? t("series.title");
  const headerPicture = header ? seriesPictureUrl(header) : null;

  return (
    <section className="mkt-page mkt-series-detail">
      <header className="mkt-page__header mkt-series-detail__header">
        {headerPicture && !headerBroken && (
          <img
            className="mkt-series-detail__poster"
            src={headerPicture}
            alt=""
            onError={() => setHeaderBroken(true)}
          />
        )}
        <div>
          <TitleRow>
            <h1 dir="auto">{title}</h1>
          </TitleRow>
          <div className="mkt-series-detail__flags">
            {header?.year ? (
              <Badge tone="neutral">{n(header.year, { useGrouping: false })}</Badge>
            ) : null}
            {data.numbering === "absolute" && <Badge tone="neutral">{t("series.absolute")}</Badge>}
          </div>
          <div className="mkt-series-detail__actions">
            {data.next_episode && (
              <Link
                to={`/videos/${data.next_episode.video_id}/watch`}
                className="mkt-btn mkt-btn--primary"
              >
                ▶ {t("series.continue")}
              </Link>
            )}
            <label className="mkt-field">
              <span className="mkt-field__label">{t("library.sort")}</span>
              <select
                value={byEpisode ? "episode" : "added"}
                onChange={(e) =>
                  setParams(
                    (p) => {
                      const next = new URLSearchParams(p);
                      if (e.target.value === "episode") next.set("sort", "episode");
                      else next.delete("sort");
                      return next;
                    },
                    { replace: true }
                  )
                }
                aria-label={t("library.sort")}
              >
                <option value="added">{t("library.sort.added")}</option>
                <option value="episode">{t("series.sort.episode")}</option>
              </select>
            </label>
            {/* Accepting matches is an editor (admin) write; the server refuses others. */}
            {user?.is_admin && (
              <Button variant="secondary" onClick={acceptAll} loading={accepting}>
                {t("series.acceptAll")}
              </Button>
            )}
          </div>
        </div>
      </header>

      {seasons.length === 0 ? (
        <EmptyState title={t("series.empty.episodes")} />
      ) : (
        seasons.map((s) => (
          <section key={s.season} className="mkt-season">
            <h2 className="mkt-season__title">
              {s.season === 0 ? t("series.specials") : t("series.season", { n: String(s.season) })}
            </h2>
            <ul className="mkt-episode-grid" role="list">
              {s.cells.map((c) =>
                c.kind === "ep" ? (
                  <li key={c.ep.video_id}>
                    <Link to={`/videos/${c.ep.video_id}`} className="mkt-episode">
                      <div className="mkt-episode__thumb">
                        <EpisodePicture src={c.ep.thumbnail} />
                        {c.ep.watched && (
                          <span className="mkt-episode__watched" aria-hidden="true">
                            ✓
                          </span>
                        )}
                      </div>
                      <span className="mkt-episode__no">
                        {c.ep.episode != null
                          ? `E${String(c.ep.episode).padStart(2, "0")}`
                          : c.ep.absolute_number != null
                            ? `#${c.ep.absolute_number}`
                            : ""}
                      </span>
                      <span className="mkt-episode__title" dir="auto">
                        {c.ep.title}
                      </span>
                      {c.ep.progress_pct > 0 && !c.ep.watched && (
                        <ProgressBar
                          value={c.ep.progress_pct}
                          label={t("series.episode.progress", {
                            pct: String(Math.round(c.ep.progress_pct)),
                          })}
                        />
                      )}
                    </Link>
                  </li>
                ) : (
                  <li key={`gap-${s.season}-${c.no}`}>
                    <div className="mkt-episode mkt-episode--missing" aria-disabled="true">
                      <div
                        className="mkt-episode__thumb mkt-episode__thumb--missing"
                        aria-hidden="true"
                      >
                        ?
                      </div>
                      <span className="mkt-episode__no">E{String(c.no).padStart(2, "0")}</span>
                      <span className="mkt-episode__title mkt-muted">{t("series.missing")}</span>
                    </div>
                  </li>
                )
              )}
            </ul>
          </section>
        ))
      )}
    </section>
  );
}

/** An episode's picture; none yet (404) shows the placeholder. */
function EpisodePicture({ src }: { src?: string }) {
  const [broken, setBroken] = useState(false);
  if (!src || broken) return <span aria-hidden="true">▤</span>;
  return <img src={src} alt="" loading="lazy" onError={() => setBroken(true)} />;
}
