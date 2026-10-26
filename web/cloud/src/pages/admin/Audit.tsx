// /admin/audit — the audit log, newest first, 50 per page.
import { useState } from "react";
import type { FormEvent } from "react";
import { useSearchParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { Input } from "@ds/components/Input/Input";
import { api, withQuery } from "../../api";
import { useI18n } from "../../i18n";
import { LoadError, Loading, Page, useLoad } from "../../ui";
import { detailText } from "./AccountDetail";
import { Pager } from "./shared";

export interface AuditEntry {
  id: string;
  actor_id?: string;
  actor_email?: string;
  action: string;
  target_kind?: string;
  target_id?: string;
  detail: unknown;
  ip?: string;
  created_at: string;
}

const PAGE = 50;

export function AdminAudit() {
  const { t, n, formatDate } = useI18n();
  const [params, setParams] = useSearchParams();
  const actor = params.get("actor") ?? "";
  const action = params.get("action") ?? "";
  const target = params.get("target") ?? "";
  const page = Math.max(1, Number(params.get("page")) || 1);
  const [draft, setDraft] = useState({ actor, action, target });

  const path = withQuery("/v1/admin/audit", {
    actor,
    action,
    target,
    limit: PAGE,
    offset: (page - 1) * PAGE,
  });
  const data = useLoad(() => api.get<{ entries: AuditEntry[]; total: number }>(path), path);

  function apply(e: FormEvent) {
    e.preventDefault();
    const p = new URLSearchParams();
    for (const [k, v] of Object.entries(draft)) if (v.trim()) p.set(k, v.trim());
    setParams(p, { replace: true });
  }

  function goTo(pageNo: number) {
    const p = new URLSearchParams(params);
    p.set("page", String(pageNo));
    setParams(p, { replace: true });
  }

  return (
    <Page title={t("admin.audit.title")}>
      <form className="mkc-filters" onSubmit={apply} role="search">
        <Input
          label={t("admin.audit.actor")}
          value={draft.actor}
          onChange={(e) => setDraft({ ...draft, actor: e.target.value })}
        />
        <Input
          label={t("admin.audit.action")}
          value={draft.action}
          onChange={(e) => setDraft({ ...draft, action: e.target.value })}
        />
        <Input
          label={t("admin.audit.target")}
          value={draft.target}
          onChange={(e) => setDraft({ ...draft, target: e.target.value })}
        />
        <Button type="submit" variant="secondary">
          {t("admin.audit.filter")}
        </Button>
      </form>
      {data.error != null ? (
        <LoadError error={data.error} onRetry={data.reload} />
      ) : !data.data ? (
        <Loading />
      ) : data.data.entries.length === 0 ? (
        <EmptyState kind="filtered_out" title={t("admin.audit.none")} />
      ) : (
        <>
          <p className="mkc-muted">{t("admin.total", { count: n(data.data.total) })}</p>
          <table className="mkc-table">
            <thead>
              <tr>
                <th scope="col">{t("admin.col.when")}</th>
                <th scope="col">{t("admin.audit.actor")}</th>
                <th scope="col">{t("admin.audit.action")}</th>
                <th scope="col">{t("admin.audit.target")}</th>
                <th scope="col">{t("admin.audit.detail")}</th>
                <th scope="col">{t("admin.audit.ip")}</th>
              </tr>
            </thead>
            <tbody>
              {data.data.entries.map((e) => (
                <tr key={e.id}>
                  <td>{formatDate(e.created_at)}</td>
                  <td>{e.actor_email ?? e.actor_id ?? "—"}</td>
                  <td className="mkc-mono">{e.action}</td>
                  <td className="mkc-mono">
                    {e.target_kind ? `${e.target_kind}:${e.target_id ?? ""}` : "—"}
                  </td>
                  <td className="mkc-detail">{e.detail != null ? detailText(e.detail) : ""}</td>
                  <td className="mkc-mono">{e.ip ?? "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <Pager page={page} total={data.data.total} size={PAGE} onChange={goTo} />
        </>
      )}
    </Page>
  );
}
