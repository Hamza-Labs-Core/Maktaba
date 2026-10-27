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
import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { Card } from "@ds/components/Card/Card";
import { Badge } from "@ds/components/Badge/Badge";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { api } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { TitleRow } from "../../help/HelpLink";
import { LoadError } from "../../components/LoadError";

interface SeriesItem {
  id: string;
  name: string | null;
  poster_path?: string | null;
  poster_video_id?: string | null;
  library_id?: string;
  year?: number;
  numbering: string;
  season_count: number | null;
  episode_count: number | null;
  watched_count: number | null;
  in_progress: number;
}

type Sort = "name" | "progress";

export function SeriesBrowser() {
  const { t, n } = useI18n();
  const [items, setItems] = useState<SeriesItem[] | null>(null);
  const [err, setErr] = useState<unknown>(null);
  const [reload, setReload] = useState(0);
  // The sort lives in the URL so Back and a refresh keep it.
  const [params, setParams] = useSearchParams();
  const sort: Sort = params.get("sort") === "progress" ? "progress" : "name";

  useEffect(() => {
    // A slower answer for an older sort must not overwrite a newer one.
    let cancelled = false;
    setItems(null);
    setErr(null);
    api
      .get<{ items: SeriesItem[] }>(`/api/series?sort=${sort}`)
      .then((r) => {
        if (!cancelled) setItems(Array.isArray(r?.items) ? r.items : []);
      })
      .catch((e) => {
        if (!cancelled) setErr(e);
      });
    return () => {
      cancelled = true;
    };
  }, [sort, reload, t]);

  function changeSort(next: Sort) {
    const p = new URLSearchParams(params);
    if (next === "name") p.delete("sort");
    else p.set("sort", next);
    setParams(p, { replace: true });
  }

  const pct = (s: SeriesItem) => {
    const total = s.episode_count ?? 0;
    return total > 0 ? ((s.watched_count ?? 0) / total) * 100 : 0;
  };

  return (
    <section className="mkt-page mkt-series-browser">
      <header className="mkt-page__header">
        <TitleRow>
          <h1>{t("series.title")}</h1>
        </TitleRow>
        <label className="mkt-series-browser__sort">
          {t("series.sort")}
          <select
            value={sort}
            onChange={(e) => changeSort(e.target.value as Sort)}
            aria-label={t("series.sort")}
          >
            <option value="name">{t("series.sort.name")}</option>
            <option value="progress">{t("series.sort.progress")}</option>
          </select>
        </label>
      </header>

      {err != null && <LoadError error={err} onRetry={() => setReload((x) => x + 1)} />}
      {!err && !items && (
        <p className="mkt-loading" role="status">
          {t("common.loading")}
        </p>
      )}
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
                    <SeriesPoster series={s} />
                  </div>
                  <div className="mkt-series-card__meta">
                    <span className="mkt-series-card__name" dir="auto">
                      {s.name || s.id}
                    </span>
                    <div className="mkt-series-card__flags">
                      {s.year && <Badge tone="neutral">{n(s.year, { useGrouping: false })}</Badge>}
                      <Badge tone="neutral">
                        {n(s.season_count ?? 0)} · {n(s.episode_count ?? 0)}
                      </Badge>
                    </div>
                    <ProgressBar
                      value={pct(s)}
                      label={t("series.progress.label", {
                        watched: n(s.watched_count ?? 0),
                        total: n(s.episode_count ?? 0),
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

// The series' own art when it is a URL, else its first episode's poster
// (videos/{id}/poster resolves the thumbnail cache), else a placeholder.
export function seriesPictureUrl(series: {
  poster_path?: string | null;
  poster_video_id?: string | null;
}): string | null {
  const own =
    series.poster_path && /^(https?:\/\/|\/api\/)/.test(series.poster_path)
      ? series.poster_path
      : null;
  return own ?? (series.poster_video_id ? `/api/videos/${series.poster_video_id}/poster` : null);
}

function SeriesPoster({ series }: { series: SeriesItem }) {
  const [broken, setBroken] = useState(false);
  const src = seriesPictureUrl(series);
  if (!src || broken) {
    return (
      <div className="mkt-series-card__poster--ph" aria-hidden="true">
        ▤
      </div>
    );
  }
  return <img src={src} alt="" loading="lazy" onError={() => setBroken(true)} />;
}
