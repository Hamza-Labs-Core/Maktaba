// The Jobs tab: every job event from every machine as it happens (the
// hub's `jobs` topic), filtered by machine / process, queue and outcome,
// with each queue's throughput and average duration over the last 10
// minutes.
import { useMemo, useState } from "react";
import { Select } from "@ds/components/Select/Select";
import { tsMs } from "../../lib/clusterApi";
import { groupByMachine, type ClusterStream, type LiveJob } from "../../lib/clusterStream";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { useFmt } from "../queue/format";
import { useClusterFmt } from "./format";
import { whereName } from "./names";

export const STATS_WINDOW_MS = 10 * 60_000;
export const FEED_MAX = 200;

type Outcome = "all" | "running" | "done" | "failed" | "skipped" | "other";

function outcomeOf(j: LiveJob): Exclude<Outcome, "all"> {
  switch (j.event) {
    case "claimed":
    case "progress":
      return "running";
    case "done":
    case "failed":
    case "skipped":
      return j.event;
    default:
      return "other";
  }
}

export interface StageStats {
  stage: string;
  done: number;
  failed: number;
  perMin: number;
  avgMs: number | null;
}

/** Per-queue counts over the window ending at `now` (receive time). */
export function jobStats(jobs: LiveJob[], now: number, windowMs = STATS_WINDOW_MS): StageStats[] {
  const by = new Map<string, { done: number; failed: number; ms: number[] }>();
  for (const j of jobs) {
    if (j.at < now - windowMs) continue;
    if (j.event !== "done" && j.event !== "failed") continue;
    const s = by.get(j.stage) ?? { done: 0, failed: 0, ms: [] };
    if (j.event === "done") {
      s.done++;
      if (typeof j.duration_ms === "number") s.ms.push(j.duration_ms);
    } else s.failed++;
    by.set(j.stage, s);
  }
  return [...by.entries()]
    .map(([stage, s]) => ({
      stage,
      done: s.done,
      failed: s.failed,
      perMin: s.done / (windowMs / 60_000),
      avgMs: s.ms.length ? s.ms.reduce((a, b) => a + b, 0) / s.ms.length : null,
    }))
    .sort((a, b) => b.done - a.done || a.stage.localeCompare(b.stage));
}

export function JobsPanel({
  stream,
  queueLabel,
}: {
  stream: ClusterStream;
  queueLabel: (stage: string) => string;
}) {
  const { t, n } = useI18n();
  const f = useClusterFmt();
  const fmt = useFmt();
  const [where, setWhere] = useState("");
  const [stage, setStage] = useState("");
  const [outcome, setOutcome] = useState<Outcome>("all");

  const groups = groupByMachine(stream.processes);
  const stages = useMemo(() => [...new Set(stream.jobs.map((j) => j.stage))].sort(), [stream.jobs]);

  const inScope = (j: LiveJob) =>
    !where || (where.startsWith("s:") ? j.session === where.slice(2) : j.ip === where.slice(3));
  const scoped = stream.jobs.filter(inScope);
  const now = Date.now();
  const stats = jobStats(scoped, now);
  const feed = scoped
    .filter((j) => (!stage || j.stage === stage) && (outcome === "all" || outcomeOf(j) === outcome))
    .filter((j) => j.event !== "progress")
    .slice(-FEED_MAX)
    .reverse();

  return (
    <div className="mkt-mc-section">
      <div className="mkt-cl-filters">
        <Select
          label={t("cluster.jobs.where")}
          value={where}
          onChange={(e) => setWhere(e.currentTarget.value)}
          options={[
            { value: "", label: t("cluster.jobs.everywhere") },
            ...groups.flatMap((g) => [
              { value: `ip:${g.ip}`, label: g.name },
              ...g.processes.map((p) => ({
                value: `s:${p.session}`,
                label: t("cluster.logs.processOption", { name: g.name, worker: p.worker_id }),
              })),
            ]),
          ]}
        />
        <Select
          label={t("cluster.jobs.queue")}
          value={stage}
          onChange={(e) => setStage(e.currentTarget.value)}
          options={[
            { value: "", label: t("cluster.jobs.allQueues") },
            ...stages.map((s) => ({ value: s, label: queueLabel(s) })),
          ]}
        />
        <Select
          label={t("cluster.jobs.outcome")}
          value={outcome}
          onChange={(e) => setOutcome(e.currentTarget.value as Outcome)}
          options={(["all", "running", "done", "failed", "skipped", "other"] as const).map((o) => ({
            value: o,
            label: t(`cluster.jobs.outcome.${o}`),
          }))}
        />
      </div>

      <section className="mkt-mc-sub" aria-labelledby="mkt-cl-jobstats-h">
        <h2 id="mkt-cl-jobstats-h" className="mkt-mc-sub__title">
          {t("cluster.jobs.statsTitle")}
        </h2>
        {stats.length === 0 ? (
          <p className="mkt-mc-muted">{t("cluster.jobs.noStats")}</p>
        ) : (
          <div className="mkt-mc-table-wrap">
            <table className="mkt-mc-table">
              <thead>
                <tr>
                  <th scope="col">{t("cluster.jobs.queue")}</th>
                  <th scope="col">{t("cluster.jobs.done")}</th>
                  <th scope="col">{t("cluster.jobs.perMin")}</th>
                  <th scope="col">{t("cluster.jobs.avg")}</th>
                  <th scope="col">{t("cluster.jobs.failed")}</th>
                </tr>
              </thead>
              <tbody>
                {stats.map((s) => (
                  <tr key={s.stage}>
                    <th scope="row">{queueLabel(s.stage)}</th>
                    <td>{n(s.done)}</td>
                    <td>{f.num(s.perMin)}</td>
                    <td>{s.avgMs === null ? "–" : fmt.ms(s.avgMs)}</td>
                    <td>{n(s.failed)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section className="mkt-mc-sub" aria-labelledby="mkt-cl-jobfeed-h">
        <h2 id="mkt-cl-jobfeed-h" className="mkt-mc-sub__title">
          {t("cluster.jobs.feedTitle")}
        </h2>
        {feed.length === 0 ? (
          <p className="mkt-mc-muted">
            {stream.status === "unavailable"
              ? t("cluster.live.unavailable")
              : t("cluster.jobs.none")}
          </p>
        ) : (
          <ol className="mkt-cl-feed" aria-label={t("cluster.jobs.feedTitle")}>
            {feed.map((j) => {
              const ms = tsMs(j.ts);
              return (
                <li key={j.key} className={`mkt-cl-feed__row mkt-cl-feed__row--${outcomeOf(j)}`}>
                  <time>{f.time(Number.isFinite(ms) ? ms : j.at)}</time>
                  <span className="mkt-mc-chip">{queueLabel(j.stage)}</span>
                  <strong>{bidiIsolate(j.title || `${j.subject_kind} ${j.subject_id}`)}</strong>
                  <span className="mkt-mc-muted">
                    {t("queue.card.on", {
                      machine: bidiIsolate(whereName(stream.processes, j.session, j.ip)),
                    })}
                  </span>
                  <span className={`mkt-cl-outcome mkt-cl-outcome--${outcomeOf(j)}`}>
                    {t(`queue.jobEvent.${j.event}`)}
                  </span>
                  {typeof j.duration_ms === "number" && (
                    <span className="mkt-mc-muted">{fmt.ms(j.duration_ms)}</span>
                  )}
                  {j.attempt > 1 && (
                    <span className="mkt-mc-muted">
                      {t("cluster.jobs.attempt", { n: n(j.attempt) })}
                    </span>
                  )}
                  {j.outcome?.error && <span className="mkt-mc-error">{j.outcome.error}</span>}
                </li>
              );
            })}
          </ol>
        )}
      </section>
    </div>
  );
}
