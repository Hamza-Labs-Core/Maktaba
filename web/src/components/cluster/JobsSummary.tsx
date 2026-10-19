// "Jobs running: 3 · View in queues": the one line a machine shows about
// its work. The jobs themselves (with their progress) live on the
// Processing page, filtered by the machine (/queue?host=<id>).
import { memo } from "react";
import { Link } from "react-router-dom";
import { useI18n } from "../../lib/i18n";
import { queueHref } from "./machineSummary";

export const JobsSummary = memo(function JobsSummary({
  host,
  count,
  name,
}: {
  /** the node id, or "home" */
  host: string;
  /** null: not known (the live view is not connected) */
  count: number | null;
  /** the machine's name, for the link's accessible name */
  name: string;
}) {
  const { t, n } = useI18n();
  return (
    <span className="mkt-mc-jobs">
      <span className="mkt-mc-jobs__count">
        {t("machines.jobs.count", { n: count === null ? "–" : n(count) })}
      </span>
      <Link
        className="mkt-mc-jobs__link"
        to={queueHref(host, name)}
        aria-label={t("machines.jobs.viewAria", { name })}
      >
        {t("machines.jobs.view")}
      </Link>
    </span>
  );
});
