// /admin/logs and /admin/errors — the cloud's own logs and errors (Epic 53 story 53.17), for
// the service admin only. The log index runs on the witness W over every node's lines, so the
// whole cloud is searched from one place even while a node is down. Errors lists the cloud's
// own groups beside the homes' anonymous ones (Epic 16.7), each with the problem reports that
// name it (53.16). A line names a home only by home_h.
import { useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { Input } from "@ds/components/Input/Input";
import { Select } from "@ds/components/Select/Select";
import { api, withQuery } from "../../api";
import { useI18n } from "../../i18n";
import { LoadError, Loading, Page, useLoad } from "../../ui";

interface LogLine {
  ts?: string;
  level?: string;
  service?: string;
  node?: string;
  msg?: string;
  request_id?: string;
  home_h?: string;
  [k: string]: unknown;
}

const SERVICES = ["", "cloud.api", "cloud.relay", "cloud.worker", "cloud.logs"];
const LEVELS = ["", "info", "warn", "error"];
const NODES = ["", "A", "B", "W"];

export function AdminLogs() {
  const { t, formatDate } = useI18n();
  const [params, setParams] = useSearchParams();
  const f = {
    q: params.get("q") ?? "",
    service: params.get("service") ?? "",
    level: params.get("level") ?? "",
    node: params.get("node") ?? "",
    request_id: params.get("request_id") ?? "",
  };
  const [q, setQ] = useState(f.q);
  const path = withQuery("/v1/admin/logs/search", { ...f, limit: 200 });
  const data = useLoad(() => api.get<{ lines: LogLine[]; truncated?: boolean }>(path), path);
  function set(next: Record<string, string>) {
    const p = new URLSearchParams(params);
    for (const [k, v] of Object.entries(next)) {
      if (v) p.set(k, v);
      else p.delete(k);
    }
    setParams(p, { replace: true });
  }
  return (
    <Page title={t("admin.logs.title")}>
      <p className="mkc-muted">{t("admin.logs.about")}</p>
      <form
        className="mkc-filters"
        onSubmit={(e) => {
          e.preventDefault();
          set({ q });
        }}
      >
        <Input
          label={t("admin.logs.words")}
          value={q}
          onChange={(e) => setQ(e.target.value)}
          dir="ltr"
        />
        <Button type="submit">{t("admin.logs.search")}</Button>
        <Select
          label={t("admin.logs.service")}
          value={f.service}
          onChange={(e) => set({ service: e.target.value })}
          options={SERVICES.map((s) => ({ value: s, label: s || t("admin.filter.any") }))}
        />
        <Select
          label={t("admin.logs.level")}
          value={f.level}
          onChange={(e) => set({ level: e.target.value })}
          options={LEVELS.map((l) => ({
            value: l,
            label: l ? t(`admin.logs.level.${l}`) : t("admin.filter.any"),
          }))}
        />
        <Select
          label={t("admin.logs.node")}
          value={f.node}
          onChange={(e) => set({ node: e.target.value })}
          options={NODES.map((n) => ({ value: n, label: n || t("admin.filter.any") }))}
        />
      </form>
      {f.request_id && (
        <p className="mkc-muted">
          {t("admin.logs.following", { id: f.request_id })}{" "}
          <Button variant="secondary" onClick={() => set({ request_id: "" })}>
            {t("admin.logs.clear")}
          </Button>
        </p>
      )}
      {data.error != null ? (
        <LoadError error={data.error} onRetry={data.reload} />
      ) : !data.data ? (
        <Loading />
      ) : data.data.lines.length === 0 ? (
        <EmptyState kind="filtered_out" title={t("admin.logs.none")} />
      ) : (
        <ul className="mkc-list" data-testid="cloud-log-lines">
          {data.data.lines.map((l, i) => (
            <li key={i} className="mkc-list__row">
              <span className="mkc-muted">{l.ts ? formatDate(l.ts) : "—"}</span>{" "}
              <Badge
                tone={l.level === "error" ? "error" : l.level === "warn" ? "warn" : "neutral"}
              >
                {l.level ?? "info"}
              </Badge>{" "}
              <span className="mkc-mono">
                {l.service} {l.node}
              </span>{" "}
              <span className="mkc-mono" dir="ltr">
                {l.msg}
              </span>
              {l.request_id && (
                <>
                  {" "}
                  <Link
                    to={withQuery("/admin/logs", { request_id: l.request_id })}
                    className="mkc-mono"
                  >
                    {l.request_id.slice(0, 8)}
                  </Link>
                </>
              )}
            </li>
          ))}
        </ul>
      )}
    </Page>
  );
}

interface CloudGroup {
  fp: string;
  service: string;
  code?: string;
  title: string;
  last_seen: string;
  count: number;
  count_24h: number;
  status: string;
  reports?: number;
}

interface HomeGroup {
  fp: string;
  source: string;
  app: string;
  message: string;
  count: number;
  installs: number;
  last_version: string;
  last_seen: string;
  reports: number;
}

const DAYS = ["1", "7", "14", "30", "90"];

export function AdminErrors() {
  const { t, n, formatDate } = useI18n();
  const [params, setParams] = useSearchParams();
  const days = params.get("days") ?? "14";
  const path = withQuery("/v1/admin/errors", { days });
  const data = useLoad(
    () => api.get<{ cloud?: CloudGroup[]; cloud_error?: string; homes: HomeGroup[] }>(path),
    path
  );
  const reports = (fp: string, count?: number) =>
    count ? (
      <Link to={withQuery("/admin/reports", { fp })}>
        {t("admin.errors.reports", { count: n(count) })}
      </Link>
    ) : (
      "—"
    );
  return (
    <Page title={t("admin.errors.title")}>
      <div className="mkc-filters">
        <Select
          label={t("admin.errors.period")}
          value={days}
          onChange={(e) =>
            setParams(new URLSearchParams({ days: e.target.value }), { replace: true })
          }
          options={DAYS.map((d) => ({
            value: d,
            label: t("admin.errors.days", { count: n(Number(d)) }),
          }))}
        />
      </div>
      {data.error != null ? (
        <LoadError error={data.error} onRetry={data.reload} />
      ) : !data.data ? (
        <Loading />
      ) : (
        <>
          <h2>{t("admin.errors.cloud")}</h2>
          {data.data.cloud_error ? (
            <p className="mkc-muted" data-testid="cloud-errors-unavailable">
              {t(`admin.errors.${data.data.cloud_error}`)}
            </p>
          ) : (data.data.cloud ?? []).length === 0 ? (
            <p className="mkc-muted">{t("admin.errors.none")}</p>
          ) : (
            <table className="mkc-table" data-testid="cloud-error-groups">
              <thead>
                <tr>
                  <th scope="col">{t("admin.errors.what")}</th>
                  <th scope="col">{t("admin.logs.service")}</th>
                  <th scope="col">{t("admin.errors.count")}</th>
                  <th scope="col">{t("admin.errors.lastSeen")}</th>
                  <th scope="col">{t("admin.errors.reportsCol")}</th>
                </tr>
              </thead>
              <tbody>
                {(data.data.cloud ?? []).map((g) => (
                  <tr key={g.fp}>
                    <td>
                      <span dir="ltr">{g.title}</span>{" "}
                      {g.code && <span className="mkc-muted mkc-mono">{g.code}</span>}
                    </td>
                    <td className="mkc-mono">{g.service}</td>
                    <td>{n(g.count)}</td>
                    <td>{formatDate(g.last_seen)}</td>
                    <td>{reports(g.fp, g.reports)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          <h2>{t("admin.errors.homes")}</h2>
          <p className="mkc-muted">{t("admin.errors.homesAbout")}</p>
          {data.data.homes.length === 0 ? (
            <p className="mkc-muted">{t("admin.errors.none")}</p>
          ) : (
            <table className="mkc-table" data-testid="home-error-groups">
              <thead>
                <tr>
                  <th scope="col">{t("admin.errors.what")}</th>
                  <th scope="col">{t("admin.errors.app")}</th>
                  <th scope="col">{t("admin.errors.count")}</th>
                  <th scope="col">{t("admin.errors.installs")}</th>
                  <th scope="col">{t("admin.col.version")}</th>
                  <th scope="col">{t("admin.errors.reportsCol")}</th>
                </tr>
              </thead>
              <tbody>
                {data.data.homes.map((g) => (
                  <tr key={g.fp}>
                    <td dir="ltr">{g.message}</td>
                    <td className="mkc-mono">{g.app}</td>
                    <td>{n(g.count)}</td>
                    <td>{n(g.installs)}</td>
                    <td className="mkc-mono">{g.last_version}</td>
                    <td>{reports(g.fp, g.reports)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </>
      )}
    </Page>
  );
}
