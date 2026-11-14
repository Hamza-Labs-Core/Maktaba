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
// out mid-action. A return to the page shows the last list at once and
// reads it again quietly (lib/usePageData).
//
// Below them, the apps signed in with a code (Epic 58 story 58.03):
//   GET    /api/me/app-sign-ins      { items: [{ id, kind, label, via, created_at,
//                                      last_seen_at, expires_at, current }] }
//   DELETE /api/me/app-sign-ins/{id} (owner-scoped sign-out of that app; 204)
// A TV signed in with a code from Maktaba Cloud says so (via cloud_code).
import { useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Badge } from "@ds/components/Badge/Badge";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { useToast } from "@ds/components/Toast/Toast";
import { api } from "../../lib/api";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { TitleRow } from "../../help/HelpLink";
import { errorText } from "../../lib/errorText";
import { usePageData } from "../../lib/usePageData";
import { Loading, TablePlaceholder } from "../../components/Placeholders";
import { tvPlatformName } from "../Pair";

interface Session {
  id: string;
  created_at: string | null;
  last_seen_at: string | null;
  expires_at: string;
  ip: string | null;
  user_agent: string | null;
  current: boolean;
}

/** An app signed in with a code (GET /api/me/app-sign-ins). */
export interface AppSignIn {
  id: string;
  kind: string;
  label: string;
  via: string;
  created_at: string;
  last_seen_at: string;
  expires_at: string;
  current: boolean;
}

function loadAppSignIns() {
  return api.get<{ items: AppSignIn[] }>("/api/me/app-sign-ins").then((r) => r.items ?? []);
}

/** The apps signed in to the account with a code, each signed out here. */
export function AppSignIns() {
  const { t, formatDate } = useI18n();
  const toast = useToast();
  const { data, err, loading, reload, setData } = usePageData<AppSignIn[]>(
    "account:app-sign-ins",
    loadAppSignIns
  );
  const [busy, setBusy] = useState<string | null>(null);

  function name(a: AppSignIn): string {
    const platform = bidiIsolate(tvPlatformName(a.label) || t("sessions.unknownDevice"));
    if (a.via === "cloud_code") return t("devices.viaCloudCode", { platform });
    const key = `pair.app.${a.kind || "unknown"}`;
    const app = t(key);
    return a.label ? `${app === key ? a.kind : app} · ${platform}` : app === key ? a.kind : app;
  }

  async function signOut(a: AppSignIn) {
    if (busy) return;
    setBusy(a.id);
    try {
      await api.delete(`/api/me/app-sign-ins/${encodeURIComponent(a.id)}`);
      toast.show({ tone: "success", message: t("appSignIns.signedOut") });
      setData((cur) => (cur ? cur.filter((x) => x.id !== a.id) : cur));
      reload();
    } catch (e) {
      toast.show({ tone: "error", message: errorText(e, t) });
    } finally {
      setBusy(null);
    }
  }

  return (
    <section className="mkt-app-sign-ins" aria-labelledby="app-sign-ins-title">
      <h2 id="app-sign-ins-title">{t("appSignIns.title")}</h2>
      <p className="mkt-muted">{t("appSignIns.desc")}</p>
      {err != null ? (
        <ErrorState
          kind="server"
          title={t("common.error")}
          description={errorText(err, t)}
          action={
            <Button variant="secondary" onClick={reload}>
              {t("common.retry")}
            </Button>
          }
        />
      ) : data === null ? (
        <Loading loading={loading}>
          <TablePlaceholder head columns={4} rows={2} />
        </Loading>
      ) : data.length === 0 ? (
        <EmptyState title={t("appSignIns.empty.title")} description={t("appSignIns.empty.desc")} />
      ) : (
        <table className="mkt-table" aria-label={t("appSignIns.title")}>
          <thead>
            <tr>
              <th>{t("appSignIns.col.app")}</th>
              <th>{t("sessions.col.created")}</th>
              <th>{t("sessions.col.lastSeen")}</th>
              <th>{t("common.actions")}</th>
            </tr>
          </thead>
          <tbody>
            {data.map((a) => (
              <tr key={a.id} data-testid="app-sign-in">
                <td>
                  <span className="mkt-truncate">{name(a)}</span>
                  {a.current && (
                    <>
                      {" "}
                      <Badge tone="accent">{t("sessions.current")}</Badge>
                    </>
                  )}
                </td>
                <td>{formatDate(a.created_at)}</td>
                <td>{formatDate(a.last_seen_at)}</td>
                <td className="mkt-row-actions">
                  <Button
                    size="sm"
                    variant="destructive"
                    loading={busy === a.id}
                    onClick={() => void signOut(a)}
                  >
                    {t("appSignIns.signOut")}
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

function loadSessions() {
  return api.get<{ items: Session[] }>("/api/me/sessions").then((r) => r.items ?? []);
}

export function Sessions() {
  const { t, formatDate } = useI18n();
  const toast = useToast();
  const {
    data: sessions,
    err,
    loading,
    reload,
    setData: setSessions,
  } = usePageData<Session[]>("account:sessions", loadSessions);
  const error = err != null ? errorText(err, t) : null;
  // Ids with a revoke in flight: each row keeps its own busy state.
  const [revoking, setRevoking] = useState<ReadonlySet<string>>(new Set());

  function setBusy(id: string, on: boolean) {
    setRevoking((cur) => {
      const next = new Set(cur);
      if (on) next.add(id);
      else next.delete(id);
      return next;
    });
  }

  async function revoke(s: Session) {
    if (revoking.has(s.id)) return;
    setBusy(s.id, true);
    try {
      await api.delete(`/api/me/sessions/${encodeURIComponent(s.id)}`);
      toast.show({ tone: "success", message: t("sessions.revoked") });
      // gone from the page and its kept answer at once, then read again
      setSessions((cur) => (cur ? cur.filter((x) => x.id !== s.id) : cur));
      reload();
    } catch (e) {
      toast.show({
        tone: "error",
        message: errorText(e, t),
      });
    } finally {
      setBusy(s.id, false);
    }
  }

  return (
    <section className="mkt-page mkt-sessions">
      <header className="mkt-page__header">
        <TitleRow>
          <h1>{t("sessions.title")}</h1>
        </TitleRow>
      </header>
      <p className="mkt-muted">{t("sessions.desc")}</p>

      {error ? (
        <ErrorState
          kind="server"
          title={t("common.error")}
          description={error}
          action={
            <Button variant="secondary" onClick={reload}>
              {t("common.retry")}
            </Button>
          }
        />
      ) : sessions === null ? (
        <Loading loading={loading}>
          <TablePlaceholder head columns={5} rows={3} />
        </Loading>
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
                  <span className="mkt-truncate">
                    {s.user_agent || t("sessions.unknownDevice")}
                  </span>
                  {s.current && (
                    <>
                      {" "}
                      <Badge tone="accent">{t("sessions.current")}</Badge>
                    </>
                  )}
                </td>
                <td className="mkt-mono">{s.ip || "—"}</td>
                <td>{s.created_at ? formatDate(s.created_at) : "—"}</td>
                <td>{s.last_seen_at ? formatDate(s.last_seen_at) : "—"}</td>
                <td className="mkt-row-actions">
                  {s.current ? (
                    <span className="mkt-muted">{t("sessions.thisDevice")}</span>
                  ) : (
                    <Button
                      size="sm"
                      variant="destructive"
                      loading={revoking.has(s.id)}
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
      <AppSignIns />
    </section>
  );
}
