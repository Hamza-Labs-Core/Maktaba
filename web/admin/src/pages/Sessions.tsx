// /sessions — the staff account's own signed-in devices (story 50.33 §2: GET /v1/staff/sessions)
// and "Sign out everywhere" (POST /v1/staff/sessions/revoke-all), which ends this session too.
// A staff account with no role can open it: it is about the account, not the admin routes.
import { useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { Modal } from "@ds/components/Modal/Modal";
import { useI18n } from "@cloud-shared/i18n";
import { Banner, LoadError, Loading, Page, useErrorMessage, useLoad } from "@cloud-shared/ui";
import { api } from "../api";
import { useStaff } from "../session";

export interface StaffSessionRow {
  id: string;
  signed_in_at: string;
  last_active_at: string;
  ip: string;
  user_agent: string;
  current: boolean;
}

export function StaffSessions() {
  const { t, formatDate } = useI18n();
  const message = useErrorMessage();
  const { signOutEverywhere } = useStaff();
  const data = useLoad(
    () => api.get<{ sessions: StaffSessionRow[] }>("/v1/staff/sessions"),
    "sessions"
  );
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  async function signOutAll() {
    setBusy(true);
    setError(null);
    try {
      await signOutEverywhere();
    } catch (err) {
      setError(err);
      setBusy(false);
    }
  }

  return (
    <Page
      title={t("staff.sessions.title")}
      actions={
        <Button variant="destructive" onClick={() => setConfirming(true)}>
          {t("staff.sessions.signOutAll")}
        </Button>
      }
    >
      {data.error != null ? (
        <LoadError error={data.error} onRetry={data.reload} />
      ) : !data.data ? (
        <Loading />
      ) : data.data.sessions.length === 0 ? (
        <EmptyState kind="filtered_out" title={t("staff.sessions.none")} />
      ) : (
        <table className="mkc-table">
          <thead>
            <tr>
              <th scope="col">{t("staff.sessions.device")}</th>
              <th scope="col">{t("staff.sessions.ip")}</th>
              <th scope="col">{t("staff.sessions.signedIn")}</th>
              <th scope="col">{t("staff.sessions.lastActive")}</th>
            </tr>
          </thead>
          <tbody>
            {data.data.sessions.map((s) => (
              <tr key={s.id}>
                <td>
                  {s.user_agent || "—"}
                  {s.current && <strong> · {t("staff.sessions.current")}</strong>}
                </td>
                <td className="mkc-mono">{s.ip || "—"}</td>
                <td>{formatDate(s.signed_in_at)}</td>
                <td>{formatDate(s.last_active_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <Modal
        open={confirming}
        onClose={() => setConfirming(false)}
        title={t("staff.sessions.signOutAllTitle")}
        footer={
          <div className="mkc-row">
            <Button variant="secondary" onClick={() => setConfirming(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="destructive" loading={busy} onClick={() => void signOutAll()}>
              {t("staff.sessions.signOutAll")}
            </Button>
          </div>
        }
      >
        <p>{t("staff.sessions.signOutAllBody")}</p>
        {error != null && <Banner tone="error">{message(error)}</Banner>}
      </Modal>
    </Page>
  );
}
