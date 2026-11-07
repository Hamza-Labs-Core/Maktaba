// Settings → Privacy → Problem reports to Maktaba (Epic 53 story 53.16), an admin's. The
// switch is the reports.share component (off by default; it needs the home linked to Maktaba
// Cloud). On, the apps' "Report a problem" offers "Also send to Maktaba", unticked; a report
// goes only when its sender ticks it, or an admin sends it with their own preview. "Delete
// what Maktaba has" deletes every report of this home at Maktaba.
//
//   GET    /api/reports/maktaba   { available, enabled, delete, queued, sent }
//   PUT    /api/reports/maktaba   { enabled }
//   DELETE /api/reports/maktaba
import { useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Toggle } from "@ds/components/Choice/Toggle";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import {
  deleteAtMaktaba,
  maktabaSetting,
  setMaktabaSetting,
  type MaktabaSetting,
} from "../../lib/problemReports";

export function ReportsToMaktaba() {
  const { t, n, formatDate } = useI18n();
  const toast = useToast();
  const [s, setS] = useState<MaktabaSetting | null>(null);
  const [failed, setFailed] = useState(false);
  const [busy, setBusy] = useState(false);

  async function load() {
    try {
      setS(await maktabaSetting());
      setFailed(false);
    } catch {
      setFailed(true);
    }
  }
  useEffect(() => {
    void load();
  }, []);

  function problem(e: unknown) {
    toast.show({
      tone: "error",
      message: e instanceof ApiError && e.problem.title ? e.problem.title : t("common.error"),
    });
  }

  async function toggle() {
    if (!s) return;
    setBusy(true);
    try {
      await setMaktabaSetting(!s.enabled);
      await load();
    } catch (e) {
      problem(e);
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    setBusy(true);
    try {
      await deleteAtMaktaba();
      await load();
    } catch (e) {
      problem(e);
    } finally {
      setBusy(false);
    }
  }

  const del = s?.delete;
  return (
    <div className="mkt-telemetry-settings__server" data-testid="reports-to-maktaba">
      <h2>{t("privacy.reportsToMaktaba")}</h2>
      <p className="mkt-muted">{t("privacy.reportsToMaktaba.help")}</p>
      {failed && <p role="alert">{t("privacy.reportsToMaktaba.loadFailed")}</p>}
      {s && !s.available && <p role="note">{t("privacy.reportsToMaktaba.notLinked")}</p>}
      <Toggle
        checked={s?.enabled === true}
        disabled={!s || busy || (!s.available && !s.enabled)}
        onChange={() => void toggle()}
        label={t("privacy.reportsToMaktaba")}
      />
      {s && (s.sent ?? 0) + (s.queued ?? 0) > 0 && (
        <p className="mkt-muted" data-testid="reports-to-maktaba-counts">
          {t("privacy.reportsToMaktaba.counts", {
            sent: n(s.sent ?? 0),
            queued: n(s.queued ?? 0),
          })}
        </p>
      )}
      {s?.available && (
        <div className="mkt-report__maktaba">
          <Button variant="secondary" disabled={busy} onClick={() => void remove()}>
            {t("privacy.deleteAtMaktaba")}
          </Button>
          {del?.state === "done" && del.deleted_at && (
            <p className="mkt-muted" role="status" data-testid="reports-deleted">
              {t("privacy.deleteAtMaktaba.done", { when: formatDate(del.deleted_at) })}
            </p>
          )}
          {del?.state === "pending" && (
            <p className="mkt-muted" role="status" data-testid="reports-delete-pending">
              {t("privacy.deleteAtMaktaba.pending")}
            </p>
          )}
        </div>
      )}
    </div>
  );
}
