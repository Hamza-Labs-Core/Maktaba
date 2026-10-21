// One queue in the flow: ring gauge, done/total, rate + time left or the
// reason it is waiting, worker-pool chip. The title is the link; its
// ::after stretches over the card so the whole card is clickable while
// the DOM keeps a single, real anchor.
import { Link } from "react-router-dom";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import type { JobRef, Pool, QueueSummary, StageInfo } from "../../lib/queueApi";
import { RingGauge } from "./RingGauge";
import { STATE_GLYPH, queueVisualState, useFmt } from "./format";

/** Display name of an external service named by the API ("plex" -> "Plex"). */
const SERVICE_NAMES: Record<string, string> = {
  plex: "Plex",
  dizquetv: "dizqueTV",
  youtube: "YouTube",
};

export function serviceName(service: string): string {
  return SERVICE_NAMES[service] ?? service;
}

type T = (key: string, vars?: Record<string, string | number>) => string;

/** What parked jobs wait on, in words: "the model", "the share smb://nas/O", "disk space". */
export function waitingOnLabel(on: string, t: T): string {
  const [head, kind, ...rest] = on.split(":");
  const name = rest.join(":");
  if (head === "dependency" && kind === "model") return t("queue.health.on.model");
  if (head === "dependency" && kind === "share") return t("queue.health.on.share", { root: name });
  if (head === "resource" && kind === "disk") return t("queue.health.on.disk");
  if (head === "resource" && kind === "shm") return t("queue.health.on.shm");
  return kind ? `${kind}${name ? ` ${name}` : ""}` : on;
}

/** The failures page, filtered. */
export function failuresLink(params: Record<string, string>): string {
  return `/queue/failures?${new URLSearchParams(params).toString()}`;
}

export interface QueueCardProps {
  info: StageInfo;
  summary: QueueSummary;
  pool?: Pool;
  /** Hidden stages folded into this card (e.g. fp_gate under detect). */
  subSteps?: { info: StageInfo; summary: QueueSummary }[];
  compact?: boolean;
  /** Display name of the owning extension (badge); absent for core queues. */
  extensionLabel?: string;
  /** The queue's jobs running now (from the cluster hub), with their machine. */
  live?: QueueLiveJob[];
}

export interface QueueLiveJob {
  label: string;
  machine: string | null;
  job: JobRef;
}

/** At most this many live jobs are listed on a card. */
export const CARD_LIVE_MAX = 3;

function LiveJobs({ jobs, queue }: { jobs: QueueLiveJob[]; queue: string }) {
  const { t, n } = useI18n();
  const shown = jobs.slice(0, CARD_LIVE_MAX);
  return (
    <ul className="mkt-q-card__live" aria-label={t("queue.card.liveJobs", { queue })}>
      {shown.map(({ label, machine, job }) => {
        const p = job.progress;
        const pct = p && p.total_units > 0 ? (p.done_units / p.total_units) * 100 : undefined;
        return (
          <li key={job.id} className="mkt-q-card__livejob">
            <span className="mkt-q-card__livetext">
              {bidiIsolate(label || String(job.id))}
              {machine && (
                <span className="mkt-q-muted">
                  {" "}
                  · {t("queue.card.on", { machine: bidiIsolate(machine) })}
                </span>
              )}
            </span>
            <ProgressBar
              label={t("queue.running.progressAria", { label: label || String(job.id) })}
              value={pct}
              indeterminate={pct === undefined}
            />
          </li>
        );
      })}
      {jobs.length > shown.length && (
        <li className="mkt-q-muted">
          {t("queue.card.liveMore", { n: n(jobs.length - shown.length) })}
        </li>
      )}
    </ul>
  );
}

export function QueueCard({
  info,
  summary,
  pool,
  subSteps = [],
  compact,
  extensionLabel,
  live = [],
}: QueueCardProps) {
  const { t, n } = useI18n();
  const fmt = useFmt();
  const state = queueVisualState(info, summary);
  const servedBy = summary.served_by ?? [];
  const { counts, wave } = summary;
  const health = summary.health ?? null;
  const total = Math.max(wave.total, wave.done);
  const progress = t("queue.card.doneOf", { done: n(wave.done), total: n(total) });
  const noWorker = !!summary.no_worker && counts.running === 0 && counts.pending > 0;
  const service = summary.retrying_service ? serviceName(summary.retrying_service) : null;
  const reasonKey = noWorker
    ? "no_worker"
    : (summary.waiting_reason ?? (counts.running === 0 && counts.pending === 0 ? "idle" : null));
  const detail =
    counts.running > 0 ? (
      <>
        {fmt.rate(summary.rate_per_min)}
        {summary.eta_sec !== null &&
          ` · ${t("queue.card.left", { time: fmt.dur(summary.eta_sec) })}`}
      </>
    ) : reasonKey ? (
      t(`queue.waiting.${reasonKey}`, {
        time: fmt.dur(summary.oldest_waiting_sec),
        stage: info.label,
        service: service ?? "",
      })
    ) : null;

  return (
    <article
      className={`mkt-q-card mkt-q-card--${state}${compact ? " mkt-q-card--compact" : ""}`}
      data-stage={info.stage}
      data-state={state}
    >
      <div className="mkt-q-card__head">
        <h3 className="mkt-q-card__title">
          <Link className="mkt-q-card__link" to={`/queue/${encodeURIComponent(info.stage)}`}>
            {info.label}
          </Link>
        </h3>
        <span className={`mkt-q-state mkt-q-state--${state}`}>
          <span aria-hidden="true">{STATE_GLYPH[state]}</span> {t(`queue.state.${state}`)}
        </span>
      </div>
      {info.extension && (
        <span className="mkt-q-ext-badge">
          {t("queue.ext.badge", { name: extensionLabel ?? info.extension })}
        </span>
      )}
      <div className="mkt-q-card__body">
        <RingGauge
          done={wave.done}
          total={total}
          state={state}
          label={t("queue.card.ringAria", { label: info.label, progress })}
        />
        <div className="mkt-q-card__nums">
          <strong className="mkt-q-card__progress">{progress}</strong>
          <span className="mkt-q-card__detail">{detail}</span>
          {service && counts.running > 0 && (
            <span className="mkt-q-card__flag mkt-q-card__flag--retrying">
              <span aria-hidden="true">↻ </span>
              {t("queue.card.serviceRetrying", { service })}
            </span>
          )}
          {health?.state === "failing" && health.failing && (
            <span className="mkt-q-card__flag" title={health.failing.example}>
              <span aria-hidden="true">! </span>
              {t("queue.health.failing", {
                n: n(health.failing.count),
                signature: health.failing.signature,
              })}
            </span>
          )}
          {health?.state === "waiting" && health.waiting[0] && (
            <span
              className="mkt-q-card__flag mkt-q-card__flag--waiting"
              title={health.waiting[0].message ?? undefined}
            >
              <span aria-hidden="true">… </span>
              {t("queue.health.waiting", {
                n: n(health.waiting.reduce((a, w) => a + w.count, 0)),
                on: waitingOnLabel(health.waiting[0].on, t),
              })}
            </span>
          )}
          {!health && counts.failed_open > 0 && (
            <span className="mkt-q-card__flag">
              <span aria-hidden="true">! </span>
              {t("queue.card.failed", { n: n(counts.failed_open) })}
            </span>
          )}
          {health && (counts.failed_open > 0 || health.needs_you > 0) && (
            <span className="mkt-q-card__health">
              {counts.failed_open > 0 && (
                <Link className="mkt-q-card__failedlink" to={failuresLink({ stage: info.stage })}>
                  {t("queue.health.failedSmall", { n: n(counts.failed_open) })}
                </Link>
              )}
              {health.needs_you > 0 && (
                <Link
                  className="mkt-q-card__needsyou"
                  to={failuresLink({ stage: info.stage, status: "needs_you" })}
                >
                  {t("queue.health.needsYou", { n: n(health.needs_you) })}
                </Link>
              )}
            </span>
          )}
        </div>
      </div>
      {info.extension && (reasonKey === "not_configured" || reasonKey === "disabled") && (
        <Link
          className="mkt-q-card__setup"
          to={`/admin/extensions#${encodeURIComponent(info.extension)}`}
        >
          {t("queue.ext.setup")}
        </Link>
      )}
      {live.length > 0 && <LiveJobs jobs={live} queue={info.label} />}
      {subSteps.map((s) => (
        <p className="mkt-q-card__sub" key={s.info.stage}>
          {t("queue.card.subStep", {
            label: s.info.label,
            running: n(s.summary.counts.running),
            waiting: n(s.summary.counts.pending),
          })}
        </p>
      ))}
      {pool && (
        <span className="mkt-q-pool-chip" title={pool.label}>
          {pool.pool} {n(pool.busy)}/{n(pool.effective_size)}
        </span>
      )}
      {servedBy.length > 0 && (
        <span className="mkt-q-card__host">
          {t("queue.card.servedBy", { hosts: servedBy.join(", ") })}
        </span>
      )}
    </article>
  );
}
