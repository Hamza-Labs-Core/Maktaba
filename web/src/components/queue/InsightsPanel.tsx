// The status counters of the stand-alone control centre that the queue graph
// does not show: how many videos / seasons / collections / channels there are,
// and the "Production style" and "Topics" panels (GET /api/queues/insights).
import { useEffect, useState } from "react";
import { useI18n } from "../../lib/i18n";
import { PRODUCTION_STYLES, queueApi } from "../../lib/queueApi";
import type { Insights } from "../../lib/queueApi";
import { StatGrid, StatTile } from "./StatTile";

const REFRESH_MS = 60_000;

function Bars({
  rows,
  label,
}: {
  rows: { key: string; label: string; n: number }[];
  label: string;
}) {
  const { n } = useI18n();
  const max = Math.max(1, ...rows.map((r) => r.n));
  return (
    <ul className="mkt-q-bars" aria-label={label}>
      {rows.map((r) => (
        <li key={r.key}>
          <span className="mkt-q-bars__name" dir="auto">
            {r.label}
          </span>
          <span className="mkt-q-bars__track" aria-hidden="true">
            <i style={{ inlineSize: `${(100 * r.n) / max}%` }} />
          </span>
          <span className="mkt-q-bars__n">{n(r.n)}</span>
        </li>
      ))}
    </ul>
  );
}

export function InsightsPanel() {
  const { t, n } = useI18n();
  const [data, setData] = useState<Insights | null>(null);

  useEffect(() => {
    let alive = true;
    const load = () =>
      queueApi
        .insights()
        .then((r) => alive && setData(r))
        .catch(() => {});
    void load();
    const id = setInterval(load, REFRESH_MS);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, []);

  if (!data) return null;
  const c = data.totals;
  const counters: [string, number][] = [
    ["videos", c.videos],
    ["videos_with_length", c.videos_with_length],
    ["series", c.series],
    ["seasons", c.seasons],
    ["plex_collections", c.plex_collections],
    ["dizquetv_shows", c.dizquetv_shows],
    ["channels", c.channels],
    ["style_pinned", c.style_pinned],
  ];
  const styles = PRODUCTION_STYLES.filter((s) => data.styles[s]).map((s) => ({
    key: s,
    label: t(`queue.style.${s}`),
    n: data.styles[s] ?? 0,
  }));
  const genres = data.genres.map((g) => ({ key: g.genre, label: g.genre, n: g.seasons }));

  return (
    <section className="mkt-q-insights" aria-labelledby="mkt-q-insights-h">
      <h2 className="mkt-q-section-title" id="mkt-q-insights-h">
        {t("queue.insights.title")}
      </h2>
      <StatGrid>
        {counters.map(([k, v]) => (
          <StatTile key={k} label={t(`queue.insights.${k}`)} value={n(v)} />
        ))}
      </StatGrid>
      {(styles.length > 0 || genres.length > 0) && (
        <div className="mkt-q-cols">
          {styles.length > 0 && (
            <section className="mkt-q-panel">
              <h3 className="mkt-q-eyebrow">{t("queue.insights.styles")}</h3>
              <Bars rows={styles} label={t("queue.insights.styles")} />
            </section>
          )}
          {genres.length > 0 && (
            <section className="mkt-q-panel">
              <h3 className="mkt-q-eyebrow">{t("queue.insights.topics")}</h3>
              <Bars rows={genres} label={t("queue.insights.topics")} />
            </section>
          )}
        </div>
      )}
    </section>
  );
}
