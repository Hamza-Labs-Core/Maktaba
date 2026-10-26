// /admin/accounts/:id — one account: subscription, servers, usage, abuse and
// audit, with Suspend / Unsuspend (a reason is required and audited).
import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { useToast } from "@ds/components/Toast/Toast";
import { api } from "../../api";
import { useI18n } from "../../i18n";
import { LoadError, Loading, Page, useBytes, useLoad } from "../../ui";
import type { AdminAccount } from "./Accounts";
import type { AuditEntry } from "./Audit";
import type { AbuseSignal } from "./Abuse";
import { ReasonDialog } from "./ReasonDialog";
import { statusTone } from "./shared";

interface AdminServer {
  id: string;
  name: string;
  slug: string;
  plan: string;
  version: string;
  online: boolean;
  last_seen_at?: string;
}

interface AccountDetailData {
  account: AdminAccount;
  subscription: Record<string, unknown> | null;
  servers: AdminServer[];
  usage: { month: string; used_bytes: number; limit_bytes: number };
  sessions_active: number;
  abuse: AbuseSignal[];
  audit: AuditEntry[];
}

/** The subscription object's scalar fields, as a key/value list. */
function scalarEntries(o: Record<string, unknown> | null): [string, string][] {
  if (!o) return [];
  return Object.entries(o)
    .filter(([, v]) => v !== null && v !== undefined && typeof v !== "object")
    .map(([k, v]) => [k, String(v)]);
}

export function AdminAccountDetail() {
  const { id = "" } = useParams();
  const { t, n, formatDate } = useI18n();
  const bytes = useBytes();
  const toast = useToast();
  const path = `/v1/admin/accounts/${encodeURIComponent(id)}`;
  const data = useLoad(() => api.get<AccountDetailData>(path), path);
  const [dialog, setDialog] = useState(false);

  if (data.error != null) {
    return (
      <Page title={t("admin.account.title")}>
        <LoadError error={data.error} onRetry={data.reload} />
      </Page>
    );
  }
  if (!data.data) {
    return (
      <Page title={t("admin.account.title")}>
        <Loading />
      </Page>
    );
  }

  const d = data.data;
  const a = d.account;
  const suspended = a.status === "suspended";
  const action = suspended ? "unsuspend" : "suspend";

  async function apply(reason: string) {
    await api.post(`${path}/${action}`, { reason });
    setDialog(false);
    toast.show({ tone: "success", message: t(`admin.account.${action}ed`) });
    data.reload();
  }

  const sub = scalarEntries(d.subscription);
  const limited = d.usage.limit_bytes > 0;

  return (
    <Page
      title={a.email}
      actions={
        <Button variant={suspended ? "primary" : "destructive"} onClick={() => setDialog(true)}>
          {t(`admin.account.${action}`)}
        </Button>
      }
    >
      <p>
        <Link to="/admin/accounts">{t("admin.account.back")}</Link>
      </p>
      <div className="mkc-stack">
        <Card header={<h2>{t("admin.account.title")}</h2>}>
          <dl className="mkc-kv">
            <dt>{t("admin.col.id")}</dt>
            <dd className="mkc-mono">{a.id}</dd>
            {a.display_name && (
              <>
                <dt>{t("field.displayName")}</dt>
                <dd>{a.display_name}</dd>
              </>
            )}
            <dt>{t("admin.col.status")}</dt>
            <dd>
              <Badge tone={statusTone(a.status)}>{a.status}</Badge>
            </dd>
            <dt>{t("admin.col.plan")}</dt>
            <dd>{a.plan}</dd>
            <dt>{t("admin.col.role")}</dt>
            <dd>{a.role}</dd>
            <dt>{t("admin.col.created")}</dt>
            <dd>{formatDate(a.created_at)}</dd>
            <dt>{t("admin.col.lastLogin")}</dt>
            <dd>{a.last_login_at ? formatDate(a.last_login_at) : "—"}</dd>
            <dt>{t("admin.account.sessionsActive")}</dt>
            <dd>{n(d.sessions_active)}</dd>
          </dl>
        </Card>

        <Card header={<h2>{t("admin.account.subscription")}</h2>}>
          {sub.length === 0 ? (
            <p className="mkc-muted">{t("admin.account.noSubscription")}</p>
          ) : (
            <dl className="mkc-kv">
              {sub.map(([k, v]) => (
                <div key={k} className="mkc-kv__pair">
                  <dt className="mkc-mono">{k}</dt>
                  <dd>{v}</dd>
                </div>
              ))}
            </dl>
          )}
        </Card>

        <Card header={<h2>{t("admin.account.usage", { month: d.usage.month })}</h2>}>
          <p>
            {limited
              ? t("billing.usage.ofLimit", {
                  used: bytes(d.usage.used_bytes),
                  limit: bytes(d.usage.limit_bytes),
                })
              : t("billing.usage.unlimited", { used: bytes(d.usage.used_bytes) })}
          </p>
          {limited && (
            <ProgressBar
              label={t("billing.usage.bar")}
              value={(d.usage.used_bytes / d.usage.limit_bytes) * 100}
            />
          )}
        </Card>

        <Card header={<h2>{t("admin.account.servers")}</h2>}>
          {d.servers.length === 0 ? (
            <p className="mkc-muted">{t("admin.account.noServers")}</p>
          ) : (
            <table className="mkc-table">
              <thead>
                <tr>
                  <th scope="col">{t("admin.col.name")}</th>
                  <th scope="col">{t("admin.col.plan")}</th>
                  <th scope="col">{t("admin.col.version")}</th>
                  <th scope="col">{t("admin.col.status")}</th>
                  <th scope="col">{t("admin.col.lastSeen")}</th>
                </tr>
              </thead>
              <tbody>
                {d.servers.map((s) => (
                  <tr key={s.id}>
                    <td>
                      {s.name} <span className="mkc-muted mkc-mono">{s.slug}</span>
                    </td>
                    <td>{s.plan}</td>
                    <td className="mkc-mono">{s.version}</td>
                    <td>
                      <Badge tone={s.online ? "success" : "neutral"}>
                        {s.online ? t("admin.server.online") : t("admin.server.offline")}
                      </Badge>
                    </td>
                    <td>{s.last_seen_at ? formatDate(s.last_seen_at) : "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Card>

        <Card header={<h2>{t("admin.account.abuse")}</h2>}>
          {d.abuse.length === 0 ? (
            <p className="mkc-muted">{t("admin.abuse.noSignals")}</p>
          ) : (
            <ul className="mkc-list">
              {d.abuse.map((s, i) => (
                <li key={s.id ?? i}>
                  <strong>{s.kind}</strong> <Badge tone="warn">{String(s.severity)}</Badge>{" "}
                  <span className="mkc-muted">{s.created_at ? formatDate(s.created_at) : ""}</span>
                </li>
              ))}
            </ul>
          )}
        </Card>

        <Card header={<h2>{t("admin.account.audit")}</h2>}>
          {d.audit.length === 0 ? (
            <p className="mkc-muted">{t("admin.audit.none")}</p>
          ) : (
            <ul className="mkc-list">
              {d.audit.map((e, i) => (
                <li key={e.id ?? i}>
                  <strong className="mkc-mono">{e.action}</strong>{" "}
                  <span className="mkc-muted">
                    {e.actor_email ?? e.actor_id ?? "—"} · {formatDate(e.created_at)}
                  </span>
                  {e.detail != null && <div>{detailText(e.detail)}</div>}
                </li>
              ))}
            </ul>
          )}
        </Card>
      </div>

      <ReasonDialog
        open={dialog}
        title={t(`admin.account.${action}Title`, { email: a.email })}
        body={t(`admin.account.${action}Body`)}
        confirmLabel={t(`admin.account.${action}`)}
        destructive={!suspended}
        onClose={() => setDialog(false)}
        onConfirm={apply}
      />
    </Page>
  );
}

/** An audit row's detail: a string, or an object shown as compact JSON. */
export function detailText(detail: unknown): string {
  if (typeof detail === "string") return detail;
  try {
    return JSON.stringify(detail);
  } catch {
    return String(detail);
  }
}
