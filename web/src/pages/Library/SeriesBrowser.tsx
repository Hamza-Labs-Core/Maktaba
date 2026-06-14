// Story 26.10 — Cross-library series browser.
//
// Server contract (api/internal/handlers/series):
//   GET /api/series?library_id=&sort=  -> { items: [SeriesItem] }
//
// A responsive grid of every detected series across all libraries the
// user can access, with a sort control (name / progress) and a
// per-library filter. Each card shows the poster, name (user override
// honoured server-side), a library badge, and watched/episode progress.
// RTL-correct for Arabic series names via dir="auto".
import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { Card } from "@ds/components/Card/Card";
import { Badge } from "@ds/components/Badge/Badge";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { api, ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";

interface SeriesItem {
  id: string;
  name: string;
  poster_path?: string;
  library_id?: string;
  year?: number;
  numbering: string;
  season_count: number;
  episode_count: number;
  watched_count: number;
  in_progress: number;
}

type Sort = "name" | "progress";

export function SeriesBrowser() {
  const { t, n } = useI18n();
  const [items, setItems] = useState<SeriesItem[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [sort, setSort] = useState<Sort>("name");

  useEffect(() => {
    setItems(null);
    setErr(null);
    api
      .get<{ items: SeriesItem[] }>(`/api/series?sort=${sort}`)
      .then((r) => setItems(r.items ?? []))
      .catch((e) => setErr(e instanceof ApiError ? e.problem.title : t("common.error")));
  }, [sort, t]);

  const pct = useMemo(
    () => (s: SeriesItem) => (s.episode_count > 0 ? (s.watched_count / s.episode_count) * 100 : 0),
    []
  );

  return (
    <section className="mkt-page mkt-series-browser">
      <header className="mkt-page__header">
        <h1>{t("series.title")}</h1>
        <label className="mkt-series-browser__sort">
          {t("series.sort")}
          <select
            value={sort}
            onChange={(e) => setSort(e.target.value as Sort)}
            aria-label={t("series.sort")}
          >
            <option value="name">{t("series.sort.name")}</option>
            <option value="progress">{t("series.sort.progress")}</option>
          </select>
        </label>
      </header>

      {err && <ErrorState kind="server" title={t("common.error")} description={err} />}
      {!err && !items && <p className="mkt-loading">{t("common.loading")}</p>}
      {!err && items && items.length === 0 && (
        <EmptyState title={t("series.empty.title")} description={t("series.empty.desc")} />
      )}

      {items && items.length > 0 && (
        <ul className="mkt-series-grid" role="list">
          {items.map((s) => (
            <li key={s.id}>
              <Link to={`/series/${s.id}`} className="mkt-series-card-link">
                <Card interactive className="mkt-series-card">
                  <div className="mkt-series-card__poster">
                    {s.poster_path ? (
                      <img src={s.poster_path} alt="" loading="lazy" />
                    ) : (
                      <div className="mkt-series-card__poster--ph" aria-hidden="true">
                        ▤
                      </div>
                    )}
                  </div>
                  <div className="mkt-series-card__meta">
                    <span className="mkt-series-card__name" dir="auto">
                      {s.name}
                    </span>
                    <div className="mkt-series-card__flags">
                      {s.year && <Badge tone="neutral">{n(s.year)}</Badge>}
                      <Badge tone="neutral">
                        {n(s.season_count)} · {n(s.episode_count)}
                      </Badge>
                    </div>
                    <ProgressBar
                      value={pct(s)}
                      label={t("series.progress.label", {
                        watched: String(s.watched_count),
                        total: String(s.episode_count),
                      })}
                    />
                  </div>
                </Card>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
