// /admin/usage — the anonymous usage data homes share (Epic 16 story 16.7):
// installs, versions, platforms, apps and features in use, top error groups
// and web-vitals p75, over the chosen number of days.
import type { ReactNode } from "react";
import { useSearchParams } from "react-router-dom";
import { Card } from "@ds/components/Card/Card";
import { Select } from "@ds/components/Select/Select";
import { api, withQuery } from "../../api";
import { useI18n } from "../../i18n";
import { LoadError, Loading, Page, useLoad } from "../../ui";

export interface UsageData {
  days: number;
  since: string;
  installs: { homes: number; app_installs: number; by_app: { app: string; installs: number }[] };
  server_versions: { version: string; homes: number }[];
  app_versions: { app: string; version: string; installs: number }[];
  server_platforms: { os: string; arch: string; homes: number }[];
  install_kinds: { install_kind: string; homes: number }[];
  app_platforms: { app: string; platform: string; form_factor: string; installs: number }[];
  apps_in_use: { app: string; platform: string; homes: number; installs: number }[];
  features: { feature: string; homes: number; share: number }[];
  feature_use: { app: string; feature: string; total: number; installs: number }[];
  errors: {
    fingerprint: string;
    source: string;
    app: string;
    message: string;
    first_frame?: string;
    count: number;
    installs: number;
    versions: string[];
    first_seen: string;
    last_seen: string;
  }[];
  vitals: { metric: string; p75: number; samples: number }[];
}

export const USAGE_DAYS = [7, 30, 90, 400];

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <Card className="mkc-stat">
      <div className="mkc-stat__value">{value}</div>
      <div className="mkc-muted">{label}</div>
    </Card>
  );
}

// One titled table; an empty list shows a line instead.
function Section<T>({
  title,
  cols,
  rows,
  row,
}: {
  title: string;
  cols: string[];
  rows: T[];
  row: (r: T) => ReactNode[];
}) {
  const { t } = useI18n();
  return (
    <section>
      <h2>{title}</h2>
      {rows.length === 0 ? (
        <p className="mkc-muted">{t("admin.usage.empty")}</p>
      ) : (
        <table className="mkc-table">
          <caption className="mkc-visually-hidden">{title}</caption>
          <thead>
            <tr>
              {cols.map((c) => (
                <th key={c} scope="col">
                  {c}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((r, i) => (
              <tr key={i}>
                {row(r).map((cell, j) => (
                  <td key={j}>{cell}</td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

export function AdminUsage() {
  const { t, n, formatDate } = useI18n();
  const [params, setParams] = useSearchParams();
  const asked = Number(params.get("days"));
  const days = USAGE_DAYS.includes(asked) ? asked : 30;
  const path = withQuery("/v1/admin/usage", { days });
  const data = useLoad(() => api.get<UsageData>(path), path);
  const pct = (v: number) => n(v, { style: "percent", maximumFractionDigits: 0 });
  const ms = (v: number) => n(v, { maximumFractionDigits: 0 });

  const picker = (
    <Select
      label={t("admin.usage.window")}
      value={String(days)}
      onChange={(e) => setParams({ days: e.target.value }, { replace: true })}
      options={USAGE_DAYS.map((d) => ({
        value: String(d),
        label: t("admin.usage.days", { days: d }),
      }))}
    />
  );

  if (data.error != null) {
    return (
      <Page title={t("admin.usage.title")}>
        <LoadError error={data.error} onRetry={data.reload} />
      </Page>
    );
  }
  if (!data.data) {
    return (
      <Page title={t("admin.usage.title")}>
        {picker}
        <Loading />
      </Page>
    );
  }
  const u = data.data;
  return (
    <Page title={t("admin.usage.title")}>
      <p className="mkc-muted">{t("admin.usage.about")}</p>
      <div className="mkc-filters">{picker}</div>
      <div className="mkc-stats">
        <Stat label={t("admin.usage.homes")} value={n(u.installs.homes)} />
        <Stat label={t("admin.usage.appInstalls")} value={n(u.installs.app_installs)} />
        {u.installs.by_app.map((a) => (
          <Stat
            key={a.app}
            label={t("admin.usage.appInstallsOf", { app: a.app })}
            value={n(a.installs)}
          />
        ))}
      </div>

      <Section
        title={t("admin.usage.serverVersions")}
        cols={[t("admin.col.version"), t("admin.usage.homes")]}
        rows={u.server_versions}
        row={(r) => [r.version, n(r.homes)]}
      />
      <Section
        title={t("admin.usage.appVersions")}
        cols={[t("admin.usage.app"), t("admin.col.version"), t("admin.usage.installs")]}
        rows={u.app_versions}
        row={(r) => [r.app, r.version, n(r.installs)]}
      />
      <Section
        title={t("admin.usage.serverPlatforms")}
        cols={[t("admin.usage.os"), t("admin.usage.arch"), t("admin.usage.homes")]}
        rows={u.server_platforms}
        row={(r) => [r.os, r.arch, n(r.homes)]}
      />
      <Section
        title={t("admin.usage.installKinds")}
        cols={[t("admin.usage.installKind"), t("admin.usage.homes")]}
        rows={u.install_kinds}
        row={(r) => [r.install_kind, n(r.homes)]}
      />
      <Section
        title={t("admin.usage.appPlatforms")}
        cols={[
          t("admin.usage.app"),
          t("admin.usage.platform"),
          t("admin.usage.formFactor"),
          t("admin.usage.installs"),
        ]}
        rows={u.app_platforms}
        row={(r) => [r.app, r.platform, r.form_factor || "—", n(r.installs)]}
      />
      <Section
        title={t("admin.usage.appsInUse")}
        cols={[
          t("admin.usage.app"),
          t("admin.usage.platform"),
          t("admin.usage.homes"),
          t("admin.usage.installs"),
        ]}
        rows={u.apps_in_use}
        row={(r) => [r.app, r.platform, n(r.homes), n(r.installs)]}
      />
      <Section
        title={t("admin.usage.features")}
        cols={[t("admin.usage.feature"), t("admin.usage.homes"), t("admin.usage.share")]}
        rows={u.features}
        row={(r) => [r.feature, n(r.homes), pct(r.share)]}
      />
      <Section
        title={t("admin.usage.featureUse")}
        cols={[
          t("admin.usage.app"),
          t("admin.usage.feature"),
          t("admin.usage.uses"),
          t("admin.usage.installs"),
        ]}
        rows={u.feature_use}
        row={(r) => [r.app, r.feature, n(r.total), n(r.installs)]}
      />
      <Section
        title={t("admin.usage.errors")}
        cols={[
          t("admin.usage.source"),
          t("admin.usage.app"),
          t("admin.usage.message"),
          t("admin.usage.count"),
          t("admin.usage.installs"),
          t("admin.usage.versions"),
          t("admin.col.lastSeen"),
        ]}
        rows={u.errors}
        row={(r) => [
          t(r.source === "server" ? "admin.usage.sourceServer" : "admin.usage.sourceApp"),
          r.app,
          <span key="m" className="mkc-detail">
            {r.message}
            {r.first_frame ? <span className="mkc-muted mkc-mono"> — {r.first_frame}</span> : null}
          </span>,
          n(r.count),
          n(r.installs),
          <span key="v" className="mkc-mono">
            {r.versions.join(", ")}
          </span>,
          formatDate(r.last_seen),
        ]}
      />
      <Section
        title={t("admin.usage.vitals")}
        cols={[t("admin.usage.metric"), t("admin.usage.p75"), t("admin.usage.samples")]}
        rows={u.vitals}
        row={(r) => [
          r.metric,
          r.metric === "CLS" ? n(r.p75, { maximumFractionDigits: 3 }) : ms(r.p75),
          n(r.samples),
        ]}
      />
    </Page>
  );
}
