// The in-process fallbacks of one first-party stage (Epic 54, story 54.43): how many of its
// jobs ran in the pipeline worker instead of its plugin runner, per machine and reason.
// Shown to admins on the Queue detail page (every queue route is admin only).
import { useEffect, useState } from "react";
import { useI18n } from "../../lib/i18n";
import { FALLBACK_REASONS, queueApi, type StageFallback } from "../../lib/queueApi";
import { useFmt } from "./format";

const KNOWN = new Set<string>(FALLBACK_REASONS);

export function FallbackCounts({ stage }: { stage: string }) {
  const { t, n } = useI18n();
  const fmt = useFmt();
  const [rows, setRows] = useState<StageFallback[] | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let live = true;
    setRows(null);
    setFailed(false);
    queueApi
      .fallbacks(stage)
      .then((r) => {
        if (live) setRows(Array.isArray(r?.fallbacks) ? r.fallbacks : []);
      })
      .catch(() => {
        if (live) setFailed(true);
      });
    return () => {
      live = false;
    };
  }, [stage]);

  if (failed || rows === null) return null;
  return (
    <section
      className="mkt-q-panel"
      aria-labelledby="mkt-q-fallbacks-h"
      data-testid="queue-fallbacks"
    >
      <h2 className="mkt-q-section-title" id="mkt-q-fallbacks-h">
        {t("queue.fallbacks.title")}
      </h2>
      {rows.length === 0 ? (
        <p className="mkt-q-muted">{t("queue.fallbacks.none")}</p>
      ) : (
        <table className="mkt-q-table">
          <thead>
            <tr>
              <th>{t("queue.fallbacks.reason")}</th>
              <th>{t("queue.fallbacks.machine")}</th>
              <th>{t("queue.fallbacks.count")}</th>
              <th>{t("queue.fallbacks.last")}</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((f) => (
              <tr key={`${f.machine}/${f.reason}`}>
                <td>{KNOWN.has(f.reason) ? t(`queue.fallbacks.why.${f.reason}`) : f.reason}</td>
                <td>
                  {f.machine === "home" ? t("queue.fallbacks.home") : (f.machine_name ?? f.machine)}
                </td>
                <td>{n(f.count)}</td>
                <td>{fmt.ago(f.last_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}
