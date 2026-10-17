// The next jobs to be picked up by this queue (first 20 by priority, id).
import { Link } from "react-router-dom";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import type { JobRef } from "../../lib/queueApi";
import { useFmt } from "./format";
import { jobHref } from "./RunningJobs";

export function NextUp({ jobs }: { jobs: JobRef[] }) {
  const { t, n } = useI18n();
  const fmt = useFmt();
  if (jobs.length === 0) return <p className="mkt-q-empty">{t("queue.next.none")}</p>;
  return (
    <ol className="mkt-q-nextup">
      {jobs.map((j) => (
        <li key={j.id}>
          <Link to={jobHref(j)}>{bidiIsolate(j.subject_label)}</Link>
          <span className="mkt-q-muted">
            {" "}
            · {t("queue.next.priority", { n: n(j.priority) })} ·{" "}
            {t("queue.next.waited", { time: fmt.since(j.created_at) })}
          </span>
        </li>
      ))}
    </ol>
  );
}
