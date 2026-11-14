// The Queues page's notice that workers are not running their jobs (the `workers` block
// of GET /api/queues/pulse says not_responding): which workers, since when, what happens
// to the waiting jobs, and the buttons that restart them
// (POST /api/queues/workers/restart: the stalled workers' components, or all). The page
// and its API are the home admins'; the API refuses the restart to anyone else.
import { useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { dateTimeFormat } from "../../lib/intl";
import { pluralKey } from "../../lib/plural";
import { queueApi } from "../../lib/queueApi";
import type { StalledWorker, WorkersState } from "../../lib/queueApi";
import { useAction } from "./useAction";
import { useFmt } from "./format";

const KINDS = ["main", "transcription", "search"];

export function WorkersStalled({ workers }: { workers: WorkersState }) {
  const { t, n, locale } = useI18n();
  const fmt = useFmt();
  const action = useAction();
  // the restart was asked: the notice stays until the pulse says the workers run again
  const [asked, setAsked] = useState(false);

  const stalled = workers.stalled ?? [];
  const when = workers.since
    ? dateTimeFormat(locale === "ar" ? "ar-EG" : "en", {
        dateStyle: "medium",
        timeStyle: "short",
      }).format(new Date(workers.since))
    : null;
  // the components of the stalled workers this server can restart, each once
  const components = [...new Set(stalled.filter((w) => w.restartable).map((w) => w.component))];
  const elsewhere = stalled.some((w) => !w.restartable && w.node_name);
  const restart = (key: string, ids?: string[]) =>
    void action.run(key, () => queueApi.restartWorkers(ids)).then((r) => r && setAsked(true));

  const name = (w: StalledWorker) => {
    const kind = t(`queue.workersStalled.worker.${KINDS.includes(w.kind) ? w.kind : "main"}`);
    const machine = w.node_name || w.host;
    return machine
      ? t("queue.workersStalled.workerOn", { worker: kind, machine: bidiIsolate(machine) })
      : kind;
  };

  return (
    <div className="mkt-q-stall">
      <strong>
        {t(stalled.length > 0 ? "queue.workersStalled.title" : "queue.workersStalled.titleNone")}
      </strong>
      {when && <p>{t("queue.workersStalled.since", { when, time: fmt.since(workers.since) })}</p>}
      {stalled.length > 0 ? (
        <ul className="mkt-q-stall__list">
          {stalled.map((w) => (
            <li key={w.worker_id}>
              {t(
                w.reason === "no_dispatch"
                  ? "queue.workersStalled.noDispatch"
                  : "queue.workersStalled.noHeartbeat",
                {
                  worker: name(w),
                  time: fmt.since(w.reason === "no_dispatch" ? w.since : w.last_seen_at),
                }
              )}
            </li>
          ))}
        </ul>
      ) : (
        <p>{t("queue.workersStalled.noneRegistered")}</p>
      )}
      <p>
        {t(pluralKey(locale, "queue.workersStalled.waiting", workers.waiting), {
          n: n(workers.waiting),
        })}
      </p>
      {!workers.can_restart ? (
        <p>{t("queue.workersStalled.cannotRestart")}</p>
      ) : asked ? (
        <p role="status">{t("queue.workersStalled.restarting")}</p>
      ) : (
        <div className="mkt-q-stall__act">
          <span>{t("queue.workersStalled.adminHint")}</span>
          {components.length > 0 && (
            <Button
              size="sm"
              variant="secondary"
              loading={action.busy === "restart-stalled"}
              disabled={action.busy != null && action.busy !== "restart-stalled"}
              onClick={() => restart("restart-stalled", components)}
            >
              {t("queue.workersStalled.restart")}
            </Button>
          )}
          <Button
            size="sm"
            variant={components.length > 0 ? "ghost" : "secondary"}
            loading={action.busy === "restart-all"}
            disabled={action.busy != null && action.busy !== "restart-all"}
            onClick={() => restart("restart-all")}
          >
            {t("queue.workersStalled.restartAll")}
          </Button>
        </div>
      )}
      {elsewhere && <p>{t("queue.workersStalled.otherMachine")}</p>}
      <p>
        {t("queue.workersStalled.failedJobs")}{" "}
        <Link to="/queue/failures">{t("queue.workersStalled.failedJobsLink")}</Link>
      </p>
      {action.error && <p>{t("queue.workersStalled.failed", { error: action.error })}</p>}
    </div>
  );
}
