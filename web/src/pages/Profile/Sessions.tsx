// The signed-in user's active web sessions (web-pages-batch2).
//
// Real contracts:
//   GET    /api/me/sessions      { items: [{ id, created_at, last_seen_at,
//                                  expires_at, ip, user_agent, current }] }
//   DELETE /api/me/sessions/{id} (owner-scoped revoke; 204)
//
// A table of every active session with a per-row revoke. The session
// backing the current request is badged and cannot be revoked from here
// (use Sign out for that), so a user can't accidentally lock themselves
// out mid-action.
import { useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Badge } from "@ds/components/Badge/Badge";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { useToast } from "@ds/components/Toast/Toast";
import { api, ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";

interface Session {
  id: string;
  created_at: string;
  last_seen_at: string;
  expires_at: string;
  ip: string | null;
  user_agent: string | null;
  current: boolean;
}

export function Sessions() {
  const { t, formatDate } = useI18n();
  const toast = useToast();
  const [sessions, setSessions] = useState<Session[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [revoking, setRevoking] = useState<string | null>(null);

  function load() {
    setError(null);
    api
      .get<{ items: Session[] }>("/api/me/sessions")
      .then((r) => setSessions(r.items ?? []))
      .catch((e) => {
        setError(e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error"));
        setSessions([]);
      });
  }

  useEffect(load, []);

  async function revoke(s: Session) {
    setRevoking(s.id);
    try {
      await api.delete(`/api/me/sessions/${encodeURIComponent(s.id)}`);
      toast.show({ tone: "success", message: t("sessions.revoked") });
      load();
    } catch (e) {
      toast.show({
        tone: "error",
        message: e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error"),
      });
    } finally {
      setRevoking(null);
    }
  }

  return (
    <section className="mkt-page mkt-sessions">
      <header className="mkt-page__header">
        <h1>{t("sessions.title")}</h1>
      </header>
      <p className="mkt-muted">{t("sessions.desc")}</p>

      {error ? (
        <ErrorState
          kind="server"
          title={t("common.error")}
          description={error}
          action={
            <Button variant="secondary" onClick={load}>
              {t("common.retry")}
            </Button>
          }
        />
      ) : sessions === null ? (
        <p className="mkt-loading">{t("common.loading")}</p>
      ) : sessions.length === 0 ? (
        <EmptyState title={t("sessions.empty.title")} description={t("sessions.empty.desc")} />
      ) : (
        <table className="mkt-table" aria-label={t("sessions.title")}>
          <thead>
            <tr>
              <th>{t("sessions.col.device")}</th>
              <th>{t("sessions.col.ip")}</th>
              <th>{t("sessions.col.created")}</th>
              <th>{t("sessions.col.lastSeen")}</th>
              <th>{t("common.actions")}</th>
            </tr>
          </thead>
          <tbody>
            {sessions.map((s) => (
              <tr key={s.id}>
                <td>
                  <span className="mkt-truncate">{s.user_agent || t("sessions.unknownDevice")}</span>
                  {s.current && (
                    <>
                      {" "}
                      <Badge tone="accent">{t("sessions.current")}</Badge>
                    </>
                  )}
                </td>
                <td className="mkt-mono">{s.ip || "—"}</td>
                <td>{formatDate(s.created_at)}</td>
                <td>{formatDate(s.last_seen_at)}</td>
                <td className="mkt-row-actions">
                  {s.current ? (
                    <span className="mkt-muted">{t("sessions.thisDevice")}</span>
                  ) : (
                    <Button
                      size="sm"
                      variant="destructive"
                      loading={revoking === s.id}
                      onClick={() => revoke(s)}
                    >
                      {t("sessions.revoke")}
                    </Button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}
