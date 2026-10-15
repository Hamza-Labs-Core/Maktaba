// Worker-pool size stepper. A pool is shared by every queue in it, so the
// control says so and lists them. Bounds mirror PUT /api/pools (0-64).
import { useI18n } from "../../lib/i18n";
import type { Pool } from "../../lib/queueApi";

export const POOL_MIN = 0;
export const POOL_MAX = 64;

export interface PoolStepperProps {
  pool: Pool;
  /** stage id -> label, for the "also used by" list. */
  stageLabel: (stage: string) => string;
  /** The stage whose page we are on; excluded from "also used by". */
  currentStage?: string;
  busy?: boolean;
  onChange: (size: number | null) => void;
}

export function PoolStepper({ pool, stageLabel, currentStage, busy, onChange }: PoolStepperProps) {
  const { t, n } = useI18n();
  const value = pool.effective_size;
  const others = pool.stages.filter((s) => s !== currentStage).map(stageLabel);
  const isAuto = pool.size === null;
  return (
    <div
      className="mkt-q-pool"
      role="group"
      aria-label={t("queue.pool.aria", { pool: pool.label })}
    >
      <span className="mkt-q-pool__name">{t("queue.pool.workers", { pool: pool.label })}</span>
      <div className="mkt-q-stepper">
        <button
          type="button"
          className="mkt-q-stepper__btn"
          aria-label={t("queue.pool.dec")}
          disabled={busy || value <= POOL_MIN}
          onClick={() => onChange(value - 1)}
        >
          −
        </button>
        <output className="mkt-q-stepper__value" aria-live="polite">
          {n(value)}
        </output>
        <button
          type="button"
          className="mkt-q-stepper__btn"
          aria-label={t("queue.pool.inc")}
          disabled={busy || value >= POOL_MAX}
          onClick={() => onChange(value + 1)}
        >
          +
        </button>
        {!isAuto && (
          <button
            type="button"
            className="mkt-q-stepper__auto"
            disabled={busy}
            onClick={() => onChange(null)}
          >
            {t("queue.pool.auto")}
          </button>
        )}
      </div>
      <p className="mkt-q-pool__note">
        {isAuto && `${t("queue.pool.isAuto")} · `}
        {pool.scope === "global" ? t("queue.pool.global") : t("queue.pool.perWorker")}
        {others.length > 0 && ` · ${t("queue.pool.shared", { stages: others.join(", ") })}`}
      </p>
    </div>
  );
}
