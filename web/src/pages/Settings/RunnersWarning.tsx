// Epic 54: a first-party plugin runner that failed leaves the stages set to run as plugins
// in it running in the worker, outside their sandbox. System health's plugin_runners item
// names each failed runner, its machine and those stages; the Plugins page and the Updates
// page (an update is what sets stages to plugin) show it here. Nothing when every runner
// with work runs, or when System health cannot be read.
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { fetchStatus, type StatusItem } from "../../lib/systemStatus";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import "./plugins.css";

export interface RunnerIssue {
  machine: string;
  runner: string;
  stages: string[];
}

/** The failed runners in a System health report's plugin_runners item. */
export function failedRunners(items: StatusItem[] | undefined): RunnerIssue[] {
  const it = items?.find((i) => i.kind === "plugin_runners");
  if (!it || it.status === "ok") return [];
  const failed = it.details?.failed;
  return Array.isArray(failed) ? (failed as RunnerIssue[]) : [];
}

export function RunnersWarning() {
  const { t } = useI18n();
  const [issues, setIssues] = useState<RunnerIssue[]>([]);
  useEffect(() => {
    let live = true;
    void fetchStatus().then((r) => {
      if (live) setIssues(failedRunners(r?.items));
    });
    return () => {
      live = false;
    };
  }, []);
  if (issues.length === 0) return null;
  return (
    <div role="alert" className="mkt-plugins__runners" data-testid="runners-warning">
      <p>
        <strong>{t("plugins.runners.title")}</strong>
      </p>
      <ul>
        {issues.map((is) => (
          <li
            key={`${is.machine}/${is.runner}`}
            data-testid={`runners-warning-${is.machine}-${is.runner}`}
          >
            {t("plugins.runners.line", {
              runner: bidiIsolate(is.runner),
              machine: is.machine === "home" ? t("plugins.runners.home") : bidiIsolate(is.machine),
              count: is.stages.length,
            })}
            {is.stages.length > 0 && (
              <>
                {" "}
                <span className="mkt-muted" dir="ltr">
                  ({is.stages.join(", ")})
                </span>
              </>
            )}
          </li>
        ))}
      </ul>
      <p className="mkt-muted">
        {t("plugins.runners.how")}{" "}
        <Link to="/settings/components">{t("plugins.runners.components")}</Link>
      </p>
    </div>
  );
}
