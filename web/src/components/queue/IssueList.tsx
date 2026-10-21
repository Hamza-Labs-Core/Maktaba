// "Needs attention": open failures grouped by cause, one action each.
// retry -> POST /api/queues/{stage}/retry {cause}; apply (review-mode
// external write) -> POST /api/queues/{stage}/apply {all:true};
// open_settings / show_workers navigate.
import { Link } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import type { FailureGroup } from "../../lib/queueApi";
import { useFmt } from "./format";
import { HelpLink } from "../../help/HelpLink";
import { causeHelp } from "../../help/registry";

export const groupKey = (g: FailureGroup) => `${g.stage}:${g.cause}`;

export interface IssueListProps {
  groups: FailureGroup[];
  stageLabel: (stage: string) => string;
  busyKey?: string | null;
  onRetry: (g: FailureGroup) => void;
  onApply: (g: FailureGroup) => void;
  /** Current NAS p95 open time; shown on retry buttons of nas_* causes. */
  nasP95Ms?: number | null;
  /** Hide the per-group stage chip (queue page already names the stage). */
  hideStage?: boolean;
}

export function IssueList({
  groups,
  stageLabel,
  busyKey,
  onRetry,
  onApply,
  nasP95Ms,
  hideStage,
}: IssueListProps) {
  const { t, n } = useI18n();
  const fmt = useFmt();
  if (groups.length === 0) {
    return <p className="mkt-q-empty">{t("queue.issues.none")}</p>;
  }
  return (
    <ul className="mkt-q-issues">
      {groups.map((g) => {
        const key = groupKey(g);
        const nasNote = g.cause.startsWith("nas_") && nasP95Ms != null;
        return (
          <li className={`mkt-q-issue mkt-q-issue--${g.action}`} key={key}>
            <div className="mkt-q-issue__main">
              <p className="mkt-q-issue__label">
                <span className="mkt-q-issue__count">{n(g.count)}</span> {g.label}
              </p>
              <p className="mkt-q-issue__meta">
                {!hideStage && <span className="mkt-q-chip">{stageLabel(g.stage)}</span>}{" "}
                {t("queue.issues.last", { time: fmt.ago(g.last_at) })}
                {" · "}
                <HelpLink
                  to={causeHelp(g.cause)}
                  label={t("help.causeHelpNamed", { cause: g.label })}
                />
              </p>
              {g.sample_message && <p className="mkt-q-issue__msg">{g.sample_message}</p>}
              {g.sample_subjects.length > 0 && (
                <p className="mkt-q-issue__subjects">
                  {g.sample_subjects.map((s, i) => (
                    <span key={s.id}>
                      {i > 0 && ", "}
                      <Link
                        to={`/queue/items/${encodeURIComponent(s.kind)}/${encodeURIComponent(s.id)}`}
                      >
                        {bidiIsolate(s.label)}
                      </Link>
                    </span>
                  ))}
                </p>
              )}
            </div>
            <div className="mkt-q-issue__action">
              {g.action === "retry" && (
                <Button
                  size="sm"
                  variant="secondary"
                  loading={busyKey === key}
                  onClick={() => onRetry(g)}
                >
                  {nasNote
                    ? t("queue.issues.retryNas", { n: n(g.count), ms: fmt.ms(nasP95Ms) })
                    : t("queue.issues.retry", { n: n(g.count) })}
                </Button>
              )}
              {g.action === "apply" && (
                <Button
                  size="sm"
                  variant="primary"
                  loading={busyKey === key}
                  onClick={() => onApply(g)}
                >
                  {t("queue.issues.apply")}
                </Button>
              )}
              {g.action === "open_settings" && (
                <Link className="mkt-q-linkbtn" to="/admin/extensions">
                  {t("queue.issues.openSettings")}
                </Link>
              )}
              {g.action === "show_workers" && (
                <Link className="mkt-q-linkbtn" to="/admin/health">
                  {t("queue.issues.showWorkers")}
                </Link>
              )}
            </div>
          </li>
        );
      })}
    </ul>
  );
}
