// Story 26.10 — Series detail (season → episode grid).
//
// Server contract:
//   GET  /api/series/{id}                  -> SeriesHeader
//   GET  /api/series/{id}/episodes         -> { seasons:[Season], numbering, next_episode? }
//   GET  /api/series/{id}/missing          -> { gaps:[{season,episode}], numbering }
//   POST /api/series/{id}/enrichment/accept-all  (26.6 batch-accept)
//
// Each season renders an episode grid; gaps from /missing render as
// ghost cells ("Episode N — missing"). A "Continue watching" button
// targets the next unwatched/in-progress episode. Season 0 is a
// "Specials" row. RTL-correct for Arabic.
import { useEffect, useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { Badge } from "@ds/components/Badge/Badge";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { useToast } from "@ds/components/Toast/Toast";
import { api, ApiError } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import { useI18n } from "../../lib/i18n";
import { TitleRow } from "../../help/HelpLink";
import { seriesPictureUrl } from "./SeriesBrowser";

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
  const [header, setHeader] = useState<SeriesHeader | null>(null);
  const [data, setData] = useState<EpisodesResponse | null>(null);
  const [gaps, setGaps] = useState<Gap[]>([]);
  const [err, setErr] = useState<{ message: string; status?: number } | null>(null);
  const [accepting, setAccepting] = useState(false);
  const [reload, setReload] = useState(0);
  const [headerBroken, setHeaderBroken] = useState(false);

  useEffect(() => {
    if (!id) return;
    // Answers for a series we already left must not land on the new one.
    let cancelled = false;
    setHeader(null);
    setData(null);
    setGaps([]);
    setErr(null);
    const enc = encodeURIComponent(id);
    api
      .get<SeriesHeader>(`/api/series/${enc}`)
      .then((h) => !cancelled && setHeader(h))
      .catch(() => !cancelled && setHeader(null));
    api
      .get<EpisodesResponse>(`/api/series/${enc}/episodes`)
      .then((r) => {
        if (cancelled) return;
        const seasons = (Array.isArray(r?.seasons) ? r.seasons : [])
          .filter((s): s is Season => s != null && typeof s === "object")
          .map((s) => ({ ...s, episodes: Array.isArray(s.episodes) ? s.episodes : [] }));
        setData({ seasons, numbering: r?.numbering ?? "", next_episode: r?.next_episode });
      })
      .catch((e) => {
        if (cancelled) return;
        setErr(
          e instanceof ApiError
            ? { message: e.problem.title, status: e.status }
            : { message: t("common.error") }
        );
      });
    api
      .get<{ gaps: Gap[] }>(`/api/series/${enc}/missing`)
      .then((r) => !cancelled && setGaps(Array.isArray(r?.gaps) ? r.gaps : []))
      .catch(() => !cancelled && setGaps([]));
    return () => {
      cancelled = true;
    };
  }, [id, t, reload]);

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
  // list of cells in episode order (ghost cells sit among the real ones).
  const seasons = useMemo(() => {
    if (!data) return [];
    const bySeason = new Map<number, Episode[]>();
    for (const s of data.seasons) bySeason.set(s.season, s.episodes);
    for (const season of gapsBySeason.keys()) if (!bySeason.has(season)) bySeason.set(season, []);
    return [...bySeason.entries()]
      .sort(([a], [b]) => a - b)
      .map(([season, eps]) => {
        const cells: Cell[] = [
          ...eps.filter((ep) => ep != null).map((ep): Cell => ({ kind: "ep", ep })),
          ...(gapsBySeason.get(season) ?? []).map((no): Cell => ({ kind: "gap", no })),
        ];
        cells.sort((a, b) => cellNo(a) - cellNo(b));
        return { season, cells };
      });
  }, [data, gapsBySeason]);

  async function acceptAll() {
    if (!id || accepting) return;
    setAccepting(true);
    try {
      await api.post(`/api/series/${encodeURIComponent(id)}/enrichment/accept-all`);
      toast.show({ message: t("series.acceptAll.done"), tone: "success" });
      setReload((x) => x + 1);
    } catch (e) {
      toast.show({
        message: e instanceof ApiError ? e.problem.title : t("common.error"),
        tone: "error",
      });
    } finally {
      setAccepting(false);
    }
  }

  if (err) {
    const missing = err.status === 404 || err.status === 403;
    return (
      <ErrorState
        kind={err.status === 404 ? "not_found" : err.status === 403 ? "permission" : "server"}
        title={t("common.error")}
        description={err.message}
        action={
          missing ? (
            <Link to="/series" className="mkt-btn mkt-btn--secondary">
              {t("nav.series")}
            </Link>
          ) : (
            <Button onClick={() => setReload((x) => x + 1)}>{t("common.retry")}</Button>
          )
        }
      />
    );
  }
  if (!data || !id) return <p className="mkt-loading">{t("common.loading")}</p>;

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
