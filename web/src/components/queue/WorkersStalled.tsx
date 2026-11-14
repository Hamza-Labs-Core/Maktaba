// The Queues page's notice that the workers are not running jobs (the `workers` block of
// GET /api/queues/pulse says not_responding): since when, what happens to the waiting
// jobs, and for an admin a button that restarts the workers
// (POST /api/queues/workers/restart, which the API refuses to anyone else). Other users
// read whom to ask.
import { useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { useI18n } from "../../lib/i18n";
import { dateTimeFormat } from "../../lib/intl";
import { pluralKey } from "../../lib/plural";
import { queueApi } from "../../lib/queueApi";
import type { WorkersState } from "../../lib/queueApi";
import { useAction } from "./useAction";
import { useFmt } from "./format";
import { useIsAdmin } from "./useIsAdmin";

export function WorkersStalled({ workers }: { workers: WorkersState }) {
  const { t, n, locale } = useI18n();
  const fmt = useFmt();
  const admin = useIsAdmin();
  const action = useAction();
  // the restart was asked: the notice stays until the pulse says the workers run again
  const [asked, setAsked] = useState(false);

  const when = workers.since
    ? dateTimeFormat(locale === "ar" ? "ar-EG" : "en", {
        dateStyle: "medium",
        timeStyle: "short",
      }).format(new Date(workers.since))
    : null;
  const restart = () =>
    void action.run("restart-workers", queueApi.restartWorkers).then((r) => r && setAsked(true));

  return (
    <div className="mkt-q-stall">
      <strong>{t("queue.workersStalled.title")}</strong>
      <p>
        {when && t("queue.workersStalled.since", { when, time: fmt.since(workers.since) })}{" "}
        {workers.reason === "no_dispatch"
          ? t("queue.workersStalled.noDispatch")
          : t("queue.workersStalled.noHeartbeat")}
      </p>
      <p>
        {t(pluralKey(locale, "queue.workersStalled.waiting", workers.waiting), {
          n: n(workers.waiting),
        })}
      </p>
      {!admin ? (
        <p>{t("queue.workersStalled.askAdmin")}</p>
      ) : !workers.can_restart ? (
        <p>{t("queue.workersStalled.cannotRestart")}</p>
      ) : asked ? (
        <p role="status">{t("queue.workersStalled.restarting")}</p>
      ) : (
        <div className="mkt-q-stall__act">
          <span>{t("queue.workersStalled.adminHint")}</span>
          <Button
            size="sm"
            variant="secondary"
            loading={action.busy === "restart-workers"}
            onClick={restart}
          >
            {t("queue.workersStalled.restart")}
          </Button>
        </div>
      )}
      {action.error && <p>{t("queue.workersStalled.failed", { error: action.error })}</p>}
    </div>
  );
}
