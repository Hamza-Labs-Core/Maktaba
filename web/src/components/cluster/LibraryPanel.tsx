// The Library tab: the libraries now (the hub's `library` topic, every 5
// minutes) and hours transcribed over a day, live or from the files.
import { useEffect, useMemo, useState } from "react";
import { Select } from "@ds/components/Select/Select";
import { clusterChannelApi, tsMs, type LibraryStats } from "../../lib/clusterApi";
import { libraryTotals, type ClusterStream, type LibraryPoint } from "../../lib/clusterStream";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { useClusterFmt } from "./format";
import { TimeChart } from "./TimeChart";
import { datesOf, nodeWith, type FilesState } from "./useClusterFiles";

const LIVE = "live";

export function LibraryPanel({ stream, files }: { stream: ClusterStream; files: FilesState }) {
  const { t, n } = useI18n();
  const f = useClusterFmt();
  const days = datesOf(files.files, "library");
  const [day, setDay] = useState(LIVE);
  const [saved, setSaved] = useState<{ points: LibraryPoint[]; last: LibraryStats[] | null }>({
    points: [],
    last: null,
  });
  const [error, setError] = useState(false);
  const fileDay = day === LIVE ? days[0] : day;
  const node = fileDay ? nodeWith(files.files, "library", fileDay) : null;

  useEffect(() => {
    if (!node || !fileDay) {
      setSaved({ points: [], last: null });
      return;
    }
    let alive = true;
    setError(false);
    clusterChannelApi
      .library(node.ip, { date: fileDay })
      .then((r) => {
        if (!alive) return;
        const samples = (Array.isArray(r?.samples) ? r.samples : [])
          .map((s) => ({ s, t: tsMs(s.ts) }))
          .filter((x) => Number.isFinite(x.t))
          .sort((a, b) => a.t - b.t);
        setSaved({
          points: samples.map(({ s, t: at }) => ({ t: at, totals: libraryTotals(s) })),
          last: samples.length ? (samples[samples.length - 1].s.libraries ?? []) : null,
        });
      })
      .catch(() => alive && setError(true));
    return () => {
      alive = false;
    };
  }, [node?.ip, fileDay]);

  const points = useMemo(() => {
    if (day !== LIVE) return saved.points;
    const firstLive = stream.libraryHistory[0]?.t ?? Infinity;
    return [...saved.points.filter((p) => p.t < firstLive), ...stream.libraryHistory];
  }, [day, saved.points, stream.libraryHistory]);

  const libraries =
    day === LIVE ? (stream.library?.libraries ?? saved.last ?? []) : (saved.last ?? []);
  const totals: LibraryStats | null =
    day === LIVE
      ? stream.library
        ? libraryTotals(stream.library)
        : (points[points.length - 1]?.totals ?? null)
      : (points[points.length - 1]?.totals ?? null);

  const hoursPct =
    totals?.hours && totals.transcribed_hours !== undefined
      ? (totals.transcribed_hours / totals.hours) * 100
      : undefined;

  const tiles: [string, string][] = totals
    ? [
        [t("cluster.library.videos"), n(totals.videos ?? 0)],
        [t("cluster.library.hours"), f.num(totals.hours, 0)],
        [
          t("cluster.library.transcribedHours"),
          `${f.num(totals.transcribed_hours, 0)} (${f.pct(hoursPct)})`,
        ],
        [t("cluster.library.transcribedVideos"), n(totals.transcribed_videos ?? 0)],
        [t("cluster.library.series"), n(totals.series ?? 0)],
        [t("cluster.library.seasons"), n(totals.seasons ?? 0)],
        [t("cluster.library.tagged"), n(totals.tagged ?? 0)],
        [t("cluster.library.indexed"), n(totals.indexed ?? 0)],
        [t("cluster.library.size"), f.bytes(totals.bytes)],
      ]
    : [];

  return (
    <div className="mkt-mc-section">
      <Select
        label={t("cluster.queues.day")}
        value={day}
        onChange={(e) => setDay(e.currentTarget.value)}
        options={[
          { value: LIVE, label: t("cluster.library.live") },
          ...days.map((d) => ({ value: d, label: d })),
        ]}
      />
      {error && (
        <p className="mkt-mc-error" role="alert">
          {t("cluster.files.error")}
        </p>
      )}
      {tiles.length === 0 ? (
        <p className="mkt-mc-muted">{t("cluster.library.none")}</p>
      ) : (
        <dl className="mkt-cl-tiles" aria-label={t("cluster.library.totals")}>
          {tiles.map(([label, value]) => (
            <div className="mkt-cl-tile" key={label}>
              <dt>{label}</dt>
              <dd>
                <strong>{value}</strong>
              </dd>
            </div>
          ))}
        </dl>
      )}
      <section className="mkt-mc-sub" aria-labelledby="mkt-cl-lib-h">
        <h2 id="mkt-cl-lib-h" className="mkt-mc-sub__title">
          {t("cluster.library.chart")}
        </h2>
        <TimeChart
          label={t("cluster.library.chart")}
          times={points.map((p) => p.t)}
          series={[
            {
              key: "transcribed",
              label: t("cluster.library.transcribedHours"),
              values: points.map((p) => p.totals.transcribed_hours ?? null),
            },
          ]}
          format={(v) => f.num(v, 0)}
          formatTime={(ms) => f.time(ms)}
          emptyText={t("cluster.library.chartEmpty")}
        />
      </section>
      {libraries.length > 0 && (
        <div className="mkt-mc-table-wrap">
          <table className="mkt-mc-table">
            <caption className="mkt-visually-hidden">{t("cluster.library.table")}</caption>
            <thead>
              <tr>
                <th scope="col">{t("cluster.library.name")}</th>
                <th scope="col">{t("cluster.library.videos")}</th>
                <th scope="col">{t("cluster.library.hours")}</th>
                <th scope="col">{t("cluster.library.transcribedHours")}</th>
                <th scope="col">{t("cluster.library.tagged")}</th>
                <th scope="col">{t("cluster.library.indexed")}</th>
              </tr>
            </thead>
            <tbody>
              {libraries.map((l, i) => (
                <tr key={String(l.library_id ?? i)}>
                  <th scope="row">{bidiIsolate(l.name ?? String(l.library_id ?? ""))}</th>
                  <td>{n(l.videos ?? 0)}</td>
                  <td>{f.num(l.hours, 0)}</td>
                  <td>{f.num(l.transcribed_hours, 0)}</td>
                  <td>{n(l.tagged ?? 0)}</td>
                  <td>{n(l.indexed ?? 0)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
