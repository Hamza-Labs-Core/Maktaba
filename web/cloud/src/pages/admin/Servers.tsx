// /admin/servers — every linked home server, filterable by online / offline.
import { useSearchParams } from "react-router-dom";
import { Badge } from "@ds/components/Badge/Badge";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { Select } from "@ds/components/Select/Select";
import { api, withQuery } from "../../api";
import { useI18n } from "../../i18n";
import { LoadError, Loading, Page, useLoad } from "../../ui";
import { Pager } from "./shared";

interface FleetServer {
  id: string;
  name: string;
  slug: string;
  owner_email: string;
  plan: string;
  version: string;
  online: boolean;
  last_seen_at?: string;
  last_heartbeat?: string;
  relay_latency_ms?: number;
}

const PAGE = 50;

export function AdminServers() {
  const { t, n, formatDate } = useI18n();
  const [params, setParams] = useSearchParams();
  const status = params.get("status") ?? "";
  const page = Math.max(1, Number(params.get("page")) || 1);
  const path = withQuery("/v1/admin/servers", {
    status,
    limit: PAGE,
    offset: (page - 1) * PAGE,
  });
  const data = useLoad(() => api.get<{ servers: FleetServer[]; total: number }>(path), path);

  function set(next: Record<string, string>) {
    const p = new URLSearchParams(params);
    for (const [k, v] of Object.entries(next)) {
      if (v) p.set(k, v);
      else p.delete(k);
    }
    if (!("page" in next)) p.delete("page");
    setParams(p, { replace: true });
  }

  return (
    <Page title={t("admin.servers.title")}>
      <div className="mkc-filters">
        <Select
          label={t("admin.col.status")}
          value={status}
          onChange={(e) => set({ status: e.target.value })}
          options={[
            { value: "", label: t("admin.filter.any") },
            { value: "online", label: t("admin.server.online") },
            { value: "offline", label: t("admin.server.offline") },
          ]}
        />
      </div>
      {data.error != null ? (
        <LoadError error={data.error} onRetry={data.reload} />
      ) : !data.data ? (
        <Loading />
      ) : data.data.servers.length === 0 ? (
        <EmptyState kind="filtered_out" title={t("admin.servers.none")} />
      ) : (
        <>
          <p className="mkc-muted">{t("admin.total", { count: n(data.data.total) })}</p>
          <table className="mkc-table">
            <thead>
              <tr>
                <th scope="col">{t("admin.col.name")}</th>
                <th scope="col">{t("admin.col.owner")}</th>
                <th scope="col">{t("admin.col.plan")}</th>
                <th scope="col">{t("admin.col.version")}</th>
                <th scope="col">{t("admin.col.status")}</th>
                <th scope="col">{t("admin.col.lastSeen")}</th>
                <th scope="col">{t("admin.col.latency")}</th>
              </tr>
            </thead>
            <tbody>
              {data.data.servers.map((s) => (
                <tr key={s.id}>
                  <td>
                    {s.name} <span className="mkc-muted mkc-mono">{s.slug}</span>
                  </td>
                  <td>{s.owner_email}</td>
                  <td>{s.plan}</td>
                  <td className="mkc-mono">{s.version}</td>
                  <td>
                    <Badge tone={s.online ? "success" : "neutral"}>
                      {s.online ? t("admin.server.online") : t("admin.server.offline")}
                    </Badge>
                  </td>
                  <td>{s.last_seen_at ? formatDate(s.last_seen_at) : "—"}</td>
                  <td>
                    {s.relay_latency_ms != null
                      ? t("admin.server.ms", { ms: n(s.relay_latency_ms) })
                      : "—"}
                  </td>
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
