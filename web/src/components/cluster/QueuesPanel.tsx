// The Queues tab: waiting / running and done per minute over time, for
// all queues or one, live (the hub's `queues` topic over today's saved
// samples) or for an earlier day from the files; and each queue now.
import { useEffect, useMemo, useState } from "react";
import { Select } from "@ds/components/Select/Select";
import { clusterChannelApi, tsMs } from "../../lib/clusterApi";
import {
  queuePoint,
  QUEUE_WINDOW_MS,
  type ClusterStream,
  type QueuePoint,
} from "../../lib/clusterStream";
import { useI18n } from "../../lib/i18n";
import { useClusterFmt } from "./format";
import { TimeChart } from "./TimeChart";
import { datesOf, nodeWith, type FilesState } from "./useClusterFiles";

const LIVE = "live";

function sum(p: QueuePoint, stage: string, k: "waiting" | "running" | "rate"): number {
  if (stage) return p.stages[stage]?.[k] ?? 0;
  return Object.values(p.stages).reduce((a, s) => a + s[k], 0);
}

export function QueuesPanel({
  stream,
  files,
  queueLabel,
}: {
  stream: ClusterStream;
  files: FilesState;
  queueLabel: (stage: string) => string;
}) {
  const { t, n } = useI18n();
  const f = useClusterFmt();
  const days = datesOf(files.files, "queues");
  const [day, setDay] = useState(LIVE);
  const [stage, setStage] = useState("");
  const [saved, setSaved] = useState<QueuePoint[]>([]);
  const [error, setError] = useState(false);
  const fileDay = day === LIVE ? days[0] : day;
  const node = fileDay ? nodeWith(files.files, "queues", fileDay) : null;

  useEffect(() => {
    if (!node || !fileDay) {
      setSaved([]);
      return;
    }
    let alive = true;
    setError(false);
    clusterChannelApi
      .queues(node.ip, { date: fileDay })
      .then((r) => {
        if (!alive) return;
        const samples = Array.isArray(r?.samples) ? r.samples : [];
        setSaved(
          samples
            .map((s) => queuePoint(s.snapshot, tsMs(s.ts)))
            .filter((p) => Number.isFinite(p.t))
            .sort((a, b) => a.t - b.t)
        );
      })
      .catch(() => alive && setError(true));
    return () => {
      alive = false;
    };
  }, [node?.ip, fileDay]);

  const points = useMemo(() => {
    if (day !== LIVE) return saved;
    const firstLive = stream.queues[0]?.t ?? Infinity;
    const cutoff = (stream.queues[stream.queues.length - 1]?.t ?? Date.now()) - QUEUE_WINDOW_MS;
    return [...saved.filter((p) => p.t < firstLive && p.t >= cutoff), ...stream.queues];
  }, [day, saved, stream.queues]);

  const stages = useMemo(() => {
    const set = new Set<string>();
    for (const p of points) Object.keys(p.stages).forEach((s) => set.add(s));
    return [...set].sort((a, b) => queueLabel(a).localeCompare(queueLabel(b)));
  }, [points, queueLabel]);

  const times = points.map((p) => p.t);
  const which = stage ? queueLabel(stage) : t("cluster.queues.all");
  const latest = points[points.length - 1];
  const fmtTime = (ms: number) => f.time(ms);

  return (
    <div className="mkt-mc-section">
      <div className="mkt-cl-filters">
        <Select
          label={t("cluster.queues.day")}
          value={day}
          onChange={(e) => setDay(e.currentTarget.value)}
          options={[
            { value: LIVE, label: t("cluster.queues.live") },
            ...days.map((d) => ({ value: d, label: d })),
          ]}
        />
        <Select
          label={t("cluster.jobs.queue")}
          value={stage}
          onChange={(e) => setStage(e.currentTarget.value)}
          options={[
            { value: "", label: t("cluster.queues.all") },
            ...stages.map((s) => ({ value: s, label: queueLabel(s) })),
          ]}
        />
      </div>
      {error && (
        <p className="mkt-mc-error" role="alert">
          {t("cluster.files.error")}
        </p>
      )}
      <section className="mkt-mc-sub" aria-labelledby="mkt-cl-q1-h">
        <h3 id="mkt-cl-q1-h" className="mkt-mc-sub__title">
          {t("cluster.queues.load", { queue: which })}
        </h3>
        <TimeChart
          label={t("cluster.queues.loadAria", { queue: which })}
          times={times}
          series={[
            {
              key: "waiting",
              label: t("cluster.queues.waiting"),
              values: points.map((p) => sum(p, stage, "waiting")),
            },
            {
              key: "running",
              label: t("cluster.queues.running"),
              values: points.map((p) => sum(p, stage, "running")),
            },
          ]}
          format={(v) => n(Math.round(v))}
          formatTime={fmtTime}
          emptyText={t("cluster.queues.empty")}
        />
      </section>
      <section className="mkt-mc-sub" aria-labelledby="mkt-cl-q2-h">
        <h3 id="mkt-cl-q2-h" className="mkt-mc-sub__title">
          {t("cluster.queues.rate", { queue: which })}
        </h3>
        <TimeChart
          label={t("cluster.queues.rateAria", { queue: which })}
          times={times}
          series={[
            {
              key: "rate",
              label: t("cluster.queues.perMin"),
              values: points.map((p) => sum(p, stage, "rate")),
            },
          ]}
          format={(v) => f.num(v)}
          formatTime={fmtTime}
          emptyText={t("cluster.queues.empty")}
        />
      </section>
      {latest && (
        <section className="mkt-mc-sub" aria-labelledby="mkt-cl-q3-h">
          <h3 id="mkt-cl-q3-h" className="mkt-mc-sub__title">
            {t("cluster.queues.now", { time: f.time(latest.t) })}
          </h3>
          <div className="mkt-mc-table-wrap">
            <table className="mkt-mc-table">
              <thead>
                <tr>
                  <th scope="col">{t("cluster.jobs.queue")}</th>
                  <th scope="col">{t("cluster.queues.waiting")}</th>
                  <th scope="col">{t("cluster.queues.running")}</th>
                  <th scope="col">{t("cluster.queues.perMin")}</th>
                </tr>
              </thead>
              <tbody>
                {stages.map((s) => (
                  <tr key={s}>
                    <th scope="row">{queueLabel(s)}</th>
                    <td>{n(latest.stages[s]?.waiting ?? 0)}</td>
                    <td>{n(latest.stages[s]?.running ?? 0)}</td>
                    <td>{f.num(latest.stages[s]?.rate ?? 0)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}
    </div>
  );
}
