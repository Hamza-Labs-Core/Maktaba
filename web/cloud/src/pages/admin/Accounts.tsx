// /admin/accounts — search and filter every cloud account.
import { useState } from "react";
import type { FormEvent } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { Input } from "@ds/components/Input/Input";
import { Select } from "@ds/components/Select/Select";
import { api, withQuery } from "../../api";
import { useI18n } from "../../i18n";
import { LoadError, Loading, Page, useLoad } from "../../ui";
import { Pager, statusTone } from "./shared";

export interface AdminAccount {
  id: string;
  email: string;
  display_name?: string;
  plan: string;
  status: string;
  role: string;
  created_at: string;
  last_login_at?: string;
  servers: number;
}

const PAGE = 25;

export function AdminAccounts() {
  const { t, n, formatDate } = useI18n();
  const [params, setParams] = useSearchParams();
  const q = params.get("q") ?? "";
  const status = params.get("status") ?? "";
  const plan = params.get("plan") ?? "";
  const page = Math.max(1, Number(params.get("page")) || 1);
  const [draft, setDraft] = useState(q);

  const path = withQuery("/v1/admin/accounts", {
    q,
    status,
    plan,
    limit: PAGE,
    offset: (page - 1) * PAGE,
  });
  const data = useLoad(() => api.get<{ accounts: AdminAccount[]; total: number }>(path), path);

  function set(next: Record<string, string>) {
    const p = new URLSearchParams(params);
    for (const [k, v] of Object.entries(next)) {
      if (v) p.set(k, v);
      else p.delete(k);
    }
    if (!("page" in next)) p.delete("page");
    setParams(p, { replace: true });
  }

  function search(e: FormEvent) {
    e.preventDefault();
    set({ q: draft.trim() });
  }

  return (
    <Page title={t("admin.accounts.title")}>
      <form className="mkc-filters" onSubmit={search} role="search">
        <Input
          label={t("admin.accounts.search")}
          type="search"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
        />
        <Select
          label={t("admin.col.status")}
          value={status}
          onChange={(e) => set({ status: e.target.value })}
          options={[
            { value: "", label: t("admin.filter.any") },
            { value: "active", label: t("admin.status.active") },
            { value: "suspended", label: t("admin.status.suspended") },
          ]}
        />
        <Select
          label={t("admin.col.plan")}
          value={plan}
          onChange={(e) => set({ plan: e.target.value })}
          options={[
            { value: "", label: t("admin.filter.any") },
            { value: "free", label: "free" },
            { value: "pro", label: "pro" },
            { value: "family", label: "family" },
          ]}
        />
        <Button type="submit" variant="secondary">
          {t("admin.accounts.searchSubmit")}
        </Button>
      </form>
      {data.error != null ? (
        <LoadError error={data.error} onRetry={data.reload} />
      ) : !data.data ? (
        <Loading />
      ) : data.data.accounts.length === 0 ? (
        <EmptyState kind="filtered_out" title={t("admin.accounts.none")} />
      ) : (
        <>
          <p className="mkc-muted">{t("admin.total", { count: n(data.data.total) })}</p>
          <table className="mkc-table">
            <thead>
              <tr>
                <th scope="col">{t("admin.col.email")}</th>
                <th scope="col">{t("admin.col.plan")}</th>
                <th scope="col">{t("admin.col.status")}</th>
                <th scope="col">{t("admin.col.servers")}</th>
                <th scope="col">{t("admin.col.created")}</th>
                <th scope="col">{t("admin.col.lastLogin")}</th>
              </tr>
            </thead>
            <tbody>
              {data.data.accounts.map((a) => (
                <tr key={a.id}>
                  <td>
                    <Link to={`/admin/accounts/${encodeURIComponent(a.id)}`}>{a.email}</Link>
                    {a.role === "service_admin" && (
                      <>
                        {" "}
                        <Badge tone="accent">{t("admin.role.service_admin")}</Badge>
                      </>
                    )}
                    {a.display_name && <div className="mkc-muted">{a.display_name}</div>}
                  </td>
                  <td>{a.plan}</td>
                  <td>
                    <Badge tone={statusTone(a.status)}>{a.status}</Badge>
                  </td>
                  <td>{n(a.servers)}</td>
                  <td>{formatDate(a.created_at)}</td>
                  <td>{a.last_login_at ? formatDate(a.last_login_at) : "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <Pager
            page={page}
            total={data.data.total}
            size={PAGE}
            onChange={(p) => set({ page: String(p) })}
          />
        </>
      )}
    </Page>
  );
}
