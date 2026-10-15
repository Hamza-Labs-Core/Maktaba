// Jobs running now, each with a progress bar and the sub-step list from
// job.progress.steps (done / current / upcoming).
import { Link } from "react-router-dom";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import type { JobRef } from "../../lib/queueApi";
import { useFmt } from "./format";

export const jobHref = (j: Pick<JobRef, "subject_kind" | "subject_id">) =>
  `/queue/items/${encodeURIComponent(j.subject_kind)}/${encodeURIComponent(j.subject_id)}`;

export function RunningJobs({ jobs }: { jobs: JobRef[] }) {
  const { t, n } = useI18n();
  const fmt = useFmt();
  if (jobs.length === 0) return <p className="mkt-q-empty">{t("queue.running.none")}</p>;
  return (
    <ul className="mkt-q-running">
      {jobs.map((j) => {
        const p = j.progress;
        const pct = p && p.total_units > 0 ? (p.done_units / p.total_units) * 100 : undefined;
        const current = p ? p.steps.indexOf(p.step) : -1;
        return (
          <li className="mkt-q-running__job" key={j.id}>
            <div className="mkt-q-running__head">
              <Link to={jobHref(j)}>{bidiIsolate(j.subject_label)}</Link>
              <span className="mkt-q-muted">
                {j.started_at && t("queue.running.for", { time: fmt.since(j.started_at) })}
                {j.attempts > 1 &&
                  ` · ${t("queue.running.attempt", { n: n(j.attempts), max: n(j.max_attempts) })}`}
              </span>
            </div>
            <ProgressBar
              label={t("queue.running.progressAria", { label: j.subject_label })}
              value={pct}
              indeterminate={pct === undefined}
              tone="accent"
            />
            {p && p.steps.length > 0 && (
              <ol className="mkt-q-steps">
                {p.steps.map((s, i) => (
                  <li
                    key={s}
                    className={
                      i < current
                        ? "mkt-q-step mkt-q-step--done"
                        : i === current
                          ? "mkt-q-step mkt-q-step--now"
                          : "mkt-q-step"
                    }
                    aria-current={i === current ? "step" : undefined}
                  >
                    <span aria-hidden="true">{i < current ? "✓" : i === current ? "▶" : "·"}</span>{" "}
                    {s}
                  </li>
                ))}
              </ol>
            )}
            {p?.message && <p className="mkt-q-muted">{p.message}</p>}
          </li>
        );
      })}
    </ul>
  );
}
