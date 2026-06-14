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
import { useI18n } from "../../lib/i18n";

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
  year?: number;
  numbering: string;
}

export function SeriesDetail() {
  const { id } = useParams();
  const { t, n } = useI18n();
  const toast = useToast();
  const [header, setHeader] = useState<SeriesHeader | null>(null);
  const [data, setData] = useState<EpisodesResponse | null>(null);
  const [gaps, setGaps] = useState<Gap[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [accepting, setAccepting] = useState(false);

  useEffect(() => {
    if (!id) return;
    setData(null);
    setErr(null);
    const enc = encodeURIComponent(id);
    api
      .get<SeriesHeader>(`/api/series/${enc}`)
      .then(setHeader)
      .catch(() => setHeader(null));
    api
      .get<EpisodesResponse>(`/api/series/${enc}/episodes`)
      .then((r) =>
        setData({ seasons: r.seasons ?? [], numbering: r.numbering, next_episode: r.next_episode })
      )
      .catch((e) => setErr(e instanceof ApiError ? e.problem.title : t("common.error")));
    api
      .get<{ gaps: Gap[] }>(`/api/series/${enc}/missing`)
      .then((r) => setGaps(r.gaps ?? []))
      .catch(() => setGaps([]));
  }, [id, t]);

  // Index gaps by season so each season row can render its ghost cells.
  const gapsBySeason = useMemo(() => {
    const m = new Map<number, number[]>();
    for (const g of gaps) m.set(g.season, [...(m.get(g.season) ?? []), g.episode]);
    return m;
  }, [gaps]);

  async function acceptAll() {
    if (!id) return;
    setAccepting(true);
    try {
      await api.post(`/api/series/${encodeURIComponent(id)}/enrichment/accept-all`);
      toast.show({ message: t("series.acceptAll.done"), tone: "success" });
    } catch (e) {
      toast.show({
        message: e instanceof ApiError ? e.problem.title : t("common.error"),
        tone: "error",
      });
    } finally {
      setAccepting(false);
    }
  }

  if (err) return <ErrorState kind="server" title={t("common.error")} description={err} />;
  if (!data || !id) return <p className="mkt-loading">{t("common.loading")}</p>;

  const title = header?.name ?? t("series.title");

  return (
    <section className="mkt-page mkt-series-detail">
      <header className="mkt-page__header mkt-series-detail__header">
        {header?.poster_path && (
          <img className="mkt-series-detail__poster" src={header.poster_path} alt="" />
        )}
        <div>
          <h1 dir="auto">{title}</h1>
          <div className="mkt-series-detail__flags">
            {header?.year && <Badge tone="neutral">{n(header.year)}</Badge>}
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
            <Button variant="secondary" onClick={acceptAll} loading={accepting}>
              {t("series.acceptAll")}
            </Button>
          </div>
        </div>
      </header>

      {data.seasons.length === 0 ? (
        <EmptyState title={t("series.empty.episodes")} />
      ) : (
        data.seasons.map((s) => (
          <section key={s.season} className="mkt-season">
            <h2 className="mkt-season__title">
              {s.season === 0 ? t("series.specials") : t("series.season", { n: String(s.season) })}
            </h2>
            <ul className="mkt-episode-grid" role="list">
              {s.episodes.map((ep) => (
                <li key={ep.video_id}>
                  <Link to={`/videos/${ep.video_id}`} className="mkt-episode">
                    <div className="mkt-episode__thumb">
                      {ep.thumbnail ? (
                        <img src={ep.thumbnail} alt="" loading="lazy" />
                      ) : (
                        <span aria-hidden="true">▤</span>
                      )}
                      {ep.watched && (
                        <span className="mkt-episode__watched" aria-hidden="true">
                          ✓
                        </span>
                      )}
                    </div>
                    <span className="mkt-episode__no">
                      {ep.episode != null
                        ? `E${String(ep.episode).padStart(2, "0")}`
                        : ep.absolute_number != null
                          ? `#${ep.absolute_number}`
                          : ""}
                    </span>
                    <span className="mkt-episode__title" dir="auto">
                      {ep.title}
                    </span>
                    {ep.progress_pct > 0 && !ep.watched && (
                      <ProgressBar
                        value={ep.progress_pct}
                        label={t("series.episode.progress", {
                          pct: String(Math.round(ep.progress_pct)),
                        })}
                      />
                    )}
                  </Link>
                </li>
              ))}
              {/* Missing episodes render as ghost cells (Story 26.10 AC). */}
              {(gapsBySeason.get(s.season) ?? []).map((epNo) => (
                <li key={`gap-${s.season}-${epNo}`}>
                  <div className="mkt-episode mkt-episode--missing" aria-disabled="true">
                    <div
                      className="mkt-episode__thumb mkt-episode__thumb--missing"
                      aria-hidden="true"
                    >
                      ?
                    </div>
                    <span className="mkt-episode__no">E{String(epNo).padStart(2, "0")}</span>
                    <span className="mkt-episode__title mkt-muted">{t("series.missing")}</span>
                  </div>
                </li>
              ))}
            </ul>
          </section>
        ))
      )}
    </section>
  );
}
