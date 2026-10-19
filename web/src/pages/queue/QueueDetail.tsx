// /queue/:stage — one queue: controls, KPIs, flow neighbours, throughput
// chart, running jobs with sub-steps, next up, failures and its log.
// ?host=<node id | home> narrows the running jobs to one machine.
import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { useI18n } from "../../lib/i18n";
import { queueApi } from "../../lib/queueApi";
import type {
  JobRef,
  LibraryQueueRow,
  QueueDetailData,
  RatePoint,
  WaitingReason,
} from "../../lib/queueApi";
import { runMachine, useJobStream, useQueuesSnapshot, useWorkers } from "../../lib/useQueueStream";
import { ActivityLog } from "../../components/queue/ActivityLog";
import { IssueList, groupKey } from "../../components/queue/IssueList";
import { NextUp } from "../../components/queue/NextUp";
import { PoolStepper } from "../../components/queue/PoolStepper";
import { RateChart } from "../../components/queue/RateChart";
import { RetriggerDialog } from "../../components/queue/RetriggerDialog";
import { RingGauge } from "../../components/queue/RingGauge";
import { RunningJobs } from "../../components/queue/RunningJobs";
import { StatGrid, StatTile } from "../../components/queue/StatTile";
import { hostWorkerIds, runsOnHost, useHostName } from "../../components/queue/hostFilter";
import { STATE_GLYPH, queueVisualState, useFmt } from "../../components/queue/format";
import { useAction } from "../../components/queue/useAction";
import { useThrottledReload } from "../../components/queue/useThrottledReload";
import "../../styles/queue.css";

function Kpi({
  label,
  value,
  sub,
  tone,
}: {
  label: string;
  value: string;
  sub?: string;
  tone?: "error";
}) {
  return (
    <StatTile label={label} value={value} tone={tone}>
      {sub ? <p>{sub}</p> : undefined}
    </StatTile>
  );
}

export function QueueDetail() {
  const { stage = "" } = useParams<{ stage: string }>();
  const { t, n } = useI18n();
  const fmt = useFmt();
  const { snapshot } = useQueuesSnapshot();
  const stream = useJobStream({ stage });
  const action = useAction();
  const host = useSearchParams()[0].get("host") || "";
  const workers = useWorkers();
  const hostName = useHostName(host, workers);

  const [detail, setDetail] = useState<QueueDetailData | null>(null);
  const [rate, setRate] = useState<RatePoint[]>([]);
  const [missing, setMissing] = useState(false);
  const [libraries, setLibraries] = useState<LibraryQueueRow[]>([]);
  const [dialog, setDialog] = useState(false);
  const [interrupt, setInterrupt] = useState(false);

  const load = useCallback(() => {
    queueApi
      .detail(stage)
      .then((d) => {
        setDetail(d);
        setMissing(false);
      })
      .catch(() => setMissing(true));
    queueApi
      .rate(stage, 6)
      .then((r) => setRate(r.points))
      .catch(() => {});
  }, [stage]);

  useEffect(() => {
    setDetail(null);
    load();
    queueApi
      .libraries()
      .then((r) => setLibraries(r.items))
      .catch(() => {});
  }, [stage, load]);
  useThrottledReload(load, stream.finishedTick);

  // Live values from the 2 s snapshot win over the one-off REST detail.
  const info = snapshot?.stages.find((s) => s.stage === stage) ?? detail?.info;
  const summary = snapshot?.queues.find((q) => q.stage === stage) ?? detail?.summary;
  const pool = snapshot?.pools.find((p) => p.pool === info?.pool);
  const stageLabel = useMemo(() => {
    const m = new Map((snapshot?.stages ?? []).map((s) => [s.stage, s.label]));
    return (s: string) => m.get(s) ?? s;
  }, [snapshot?.stages]);

  const running: JobRef[] = useMemo(() => {
    const byId = new Map<number, JobRef>((detail?.running ?? []).map((j) => [j.id, j]));
    for (const j of stream.jobs.values()) {
      if (j.state === "running" || j.state === "claimed" || j.state === "resuming")
        byId.set(j.id, j);
      else byId.delete(j.id);
    }
    let list = [...byId.values()];
    if (host) {
      const ids = hostWorkerIds(workers, host);
      list = list.filter((j) => runsOnHost(j, stream.running.get(j.id), ids));
    }
    return list.sort(
      (a, b) => (a.started_at ?? "").localeCompare(b.started_at ?? "") || a.id - b.id
    );
  }, [detail?.running, stream.jobs, stream.running, host, workers]);
  // where each running job runs (the cluster hub's job events)
  const machines = useMemo(
    () => new Map([...stream.running.values()].map((r) => [r.job.id, runMachine(r, null)])),
    [stream.running]
  );

  if (missing && !detail) {
    return (
      <section className="mkt-page mkt-q">
        <p role="alert" className="mkt-q-banner">
          {t("queue.detail.notFound", { stage })}
        </p>
        <Link to="/queue">{t("queue.detail.back")}</Link>
      </section>
    );
  }
  if (!detail || !info || !summary) {
    return (
      <section className="mkt-page mkt-q">
        <p className="mkt-loading" role="status">
          {t("common.loading")}
        </p>
      </section>
    );
  }

  const state = queueVisualState(info, summary);
  const { counts, wave } = summary;
  const total = Math.max(wave.total, wave.done);
  const descKey = `queue.stage.${stage}.desc`;
  const desc = t(descKey);
  const reason = (r: WaitingReason) =>
    r ? t(`queue.waiting.${r}`, { time: fmt.dur(summary.oldest_waiting_sec) }) : "";
  const paused = info.paused;
  const retryGroups = detail.failures.filter((g) => g.action === "retry");
  const feeds = [...new Set(info.edges.map((e) => e.to))];
  const poolSize = pool?.effective_size ?? 0;
  const noWorker = !!summary.no_worker && counts.running === 0 && counts.pending > 0;

  const afterAction = () => {
    load();
  };

  return (
    <section className="mkt-page mkt-q">
      <nav aria-label={t("queue.detail.breadcrumb")} className="mkt-q-crumbs">
        <ol>
          <li>
            <Link to="/queue">{t("queue.title")}</Link>
          </li>
          <li aria-current="page">{info.label}</li>
        </ol>
      </nav>

      <header className="mkt-q-detailhead">
        <div>
          <h1>{info.label}</h1>
          {desc !== descKey && <p className="mkt-q-muted">{desc}</p>}
          <span className={`mkt-q-state mkt-q-state--${state}`}>
            <span aria-hidden="true">{STATE_GLYPH[state]}</span> {t(`queue.state.${state}`)}
            {paused && ` · ${t("queue.detail.pausedFlag")}`}
          </span>
        </div>
        <div className="mkt-q-controls">
          {paused ? (
            <Button
              variant="primary"
              loading={action.busy === "resume"}
              onClick={() =>
                void action.run("resume", () => queueApi.resume(stage)).then(afterAction)
              }
            >
              {t("queue.detail.resume")}
            </Button>
          ) : (
            <>
              {counts.running > 0 && (
                <label className="mkt-q-check">
                  <input
                    type="checkbox"
                    checked={interrupt}
                    onChange={(e) => setInterrupt(e.target.checked)}
                  />
                  <span>{t("queue.detail.interrupt")}</span>
                </label>
              )}
              <Button
                variant="secondary"
                loading={action.busy === "pause"}
                onClick={() =>
                  void action.run("pause", () => queueApi.pause(stage, interrupt)).then(afterAction)
                }
              >
                {t("queue.detail.pause")}
              </Button>
            </>
          )}
          <Button
            variant="secondary"
            disabled={retryGroups.length === 0}
            loading={action.busy === "retry-all"}
            onClick={() =>
              void action
                .run("retry-all", async () => {
                  for (const g of retryGroups) await queueApi.retry(stage, { cause: g.cause });
                })
                .then(afterAction)
            }
          >
            {t("queue.detail.retryFailed", { n: n(retryGroups.reduce((a, g) => a + g.count, 0)) })}
          </Button>
          <Button variant="primary" onClick={() => setDialog(true)}>
            {t("queue.detail.requeue")}
          </Button>
        </div>
      </header>

      {action.error && (
        <p role="alert" className="mkt-q-banner">
          {action.error}
        </p>
      )}

      {noWorker && (
        <p role="status" className="mkt-q-banner mkt-q-banner--warn">
          {t("queue.waiting.no_worker", { stage: info.label })}
        </p>
      )}
      {(summary.served_by ?? []).length > 0 && (
        <p className="mkt-q-note">
          {t("queue.card.servedBy", { hosts: (summary.served_by ?? []).join(", ") })}
        </p>
      )}

      {pool && (
        <PoolStepper
          pool={pool}
          currentStage={stage}
          stageLabel={stageLabel}
          busy={action.busy === "pool"}
          onChange={(size) => void action.run("pool", () => queueApi.setPool(pool.pool, size))}
        />
      )}

      {info.external_write && (
        <label className="mkt-q-field mkt-q-field--inline">
          <span>{t("queue.detail.writeMode")}</span>
          <select
            value={info.write_mode}
            onChange={(e) =>
              void action
                .run("mode", () =>
                  queueApi.patchStage(stage, { write_mode: e.target.value as "auto" | "review" })
                )
                .then(afterAction)
            }
          >
            <option value="auto">{t("queue.detail.mode.auto")}</option>
            <option value="review">{t("queue.detail.mode.review")}</option>
          </select>
        </label>
      )}

      <div className="mkt-q-hero">
        <RingGauge
          size={132}
          done={wave.done}
          total={total}
          state={state}
          label={t("queue.card.ringAria", {
            label: info.label,
            progress: t("queue.card.doneOf", { done: n(wave.done), total: n(total) }),
          })}
        />
        <StatGrid>
          <Kpi
            label={t("queue.kpi.waiting")}
            value={n(counts.pending)}
            sub={
              summary.oldest_waiting_sec !== null
                ? t("queue.kpi.oldest", { time: fmt.dur(summary.oldest_waiting_sec) })
                : undefined
            }
          />
          <Kpi
            label={t("queue.kpi.running")}
            value={t("queue.kpi.ofPool", { n: n(counts.running), size: n(poolSize) })}
            sub={pool?.label}
          />
          <Kpi
            label={t("queue.kpi.rate")}
            value={fmt.rate(summary.rate_per_min)}
            sub={t("queue.kpi.rateLong", { rate: fmt.rate(detail.kpis.done_24h / 1440) })}
          />
          <Kpi
            label={t("queue.kpi.left")}
            value={summary.eta_sec !== null ? fmt.dur(summary.eta_sec) : "–"}
            sub={
              noWorker
                ? t("queue.waiting.no_worker", { stage: info.label })
                : counts.running === 0
                  ? reason(summary.waiting_reason)
                  : undefined
            }
          />
          <Kpi
            label={t("queue.kpi.failed")}
            value={n(counts.failed_open)}
            sub={t("queue.kpi.failed24", { n: n(detail.kpis.failed_24h) })}
            tone={counts.failed_open > 0 ? "error" : undefined}
          />
          <Kpi
            label={t("queue.kpi.duration")}
            value={fmt.ms(detail.kpis.median_duration_ms)}
            sub={t("queue.kpi.p95", { ms: fmt.ms(detail.kpis.p95_duration_ms) })}
          />
        </StatGrid>
      </div>

      <div className="mkt-q-neighbours" aria-label={t("queue.detail.neighbours")}>
        <ul className="mkt-q-neighbours__list">
          {info.fed_by.length === 0 && <li className="mkt-q-muted">{t("queue.detail.entry")}</li>}
          {info.fed_by.map((s) => (
            <li key={s}>
              <Link className="mkt-q-chip" to={`/queue/${encodeURIComponent(s)}`}>
                {stageLabel(s)}
              </Link>
            </li>
          ))}
        </ul>
        <span aria-hidden="true">→</span>
        <strong className="mkt-q-chip mkt-q-chip--current">{info.label}</strong>
        <span aria-hidden="true">→</span>
        <ul className="mkt-q-neighbours__list">
          {feeds.length === 0 && <li className="mkt-q-muted">{t("queue.detail.end")}</li>}
          {feeds.map((s) => (
            <li key={s}>
              <Link className="mkt-q-chip" to={`/queue/${encodeURIComponent(s)}`}>
                {stageLabel(s)}
              </Link>
            </li>
          ))}
        </ul>
      </div>

      <section className="mkt-q-panel" aria-labelledby="mkt-q-rate-h">
        <h2 className="mkt-q-section-title" id="mkt-q-rate-h">
          {t("queue.detail.rate")}
        </h2>
        <RateChart points={rate} />
      </section>

      <div className="mkt-q-cols">
        <section className="mkt-q-panel" aria-labelledby="mkt-q-run-h">
          <h2 className="mkt-q-section-title" id="mkt-q-run-h">
            {t("queue.detail.running")}
          </h2>
          {host && (
            <p className="mkt-q-note">
              {t("queue.host.filtered", { machine: hostName })}{" "}
              <Link to={`/queue/${encodeURIComponent(stage)}`}>{t("queue.host.all")}</Link>
            </p>
          )}
          <RunningJobs jobs={running} machines={machines} />
        </section>
        <section className="mkt-q-panel" aria-labelledby="mkt-q-next-h">
          <h2 className="mkt-q-section-title" id="mkt-q-next-h">
            {t("queue.detail.next")}
          </h2>
          <NextUp jobs={detail.next_up} />
        </section>
      </div>

      <section className="mkt-q-panel" aria-labelledby="mkt-q-fail-h">
        <h2 className="mkt-q-section-title" id="mkt-q-fail-h">
          {t("queue.detail.failures")}
        </h2>
        <IssueList
          hideStage
          groups={detail.failures}
          stageLabel={stageLabel}
          busyKey={action.busy}
          nasP95Ms={snapshot?.health.nas.open_ms_p95}
          onRetry={(g) =>
            void action
              .run(groupKey(g), () => queueApi.retry(stage, { cause: g.cause }))
              .then(afterAction)
          }
          onApply={(g) =>
            void action
              .run(groupKey(g), () => queueApi.apply(stage, { all: true }))
              .then(afterAction)
          }
        />
      </section>

      <section className="mkt-q-panel" aria-labelledby="mkt-q-qlog-h">
        <h2 className="mkt-q-section-title" id="mkt-q-qlog-h">
          {t("queue.detail.log")}
        </h2>
        <ActivityLog
          key={stage}
          stages={snapshot?.stages ?? [info]}
          live={stream.lines}
          fixedStage={stage}
          initial={detail.log}
        />
      </section>

      <RetriggerDialog
        open={dialog}
        onClose={() => setDialog(false)}
        stages={snapshot?.stages ?? [info]}
        libraries={libraries}
        initialStages={[stage]}
        onDone={afterAction}
      />
    </section>
  );
}
