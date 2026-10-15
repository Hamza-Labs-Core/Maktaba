// One queue in the flow: ring gauge, done/total, rate + time left or the
// reason it is waiting, worker-pool chip. The title is the link; its
// ::after stretches over the card so the whole card is clickable while
// the DOM keeps a single, real anchor.
import { Link } from "react-router-dom";
import { useI18n } from "../../lib/i18n";
import type { Pool, QueueSummary, StageInfo } from "../../lib/queueApi";
import { RingGauge } from "./RingGauge";
import { STATE_GLYPH, queueVisualState, useFmt } from "./format";

export interface QueueCardProps {
  info: StageInfo;
  summary: QueueSummary;
  pool?: Pool;
  /** Hidden stages folded into this card (e.g. fp_gate under detect). */
  subSteps?: { info: StageInfo; summary: QueueSummary }[];
  compact?: boolean;
  /** Display name of the owning extension (badge); absent for core queues. */
  extensionLabel?: string;
}

export function QueueCard({
  info,
  summary,
  pool,
  subSteps = [],
  compact,
  extensionLabel,
}: QueueCardProps) {
  const { t, n } = useI18n();
  const fmt = useFmt();
  const state = queueVisualState(info, summary);
  const servedBy = summary.served_by ?? [];
  const { counts, wave } = summary;
  const total = Math.max(wave.total, wave.done);
  const progress = t("queue.card.doneOf", { done: n(wave.done), total: n(total) });
  const noWorker = !!summary.no_worker && counts.running === 0 && counts.pending > 0;
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
          {counts.failed_open > 0 && (
            <span className="mkt-q-card__flag">
              <span aria-hidden="true">! </span>
              {t("queue.card.failed", { n: n(counts.failed_open) })}
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
