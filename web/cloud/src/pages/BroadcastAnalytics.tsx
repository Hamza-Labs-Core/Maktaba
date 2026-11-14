// /homes/:id/viewers — a publisher's viewer analytics (Epic 50 story 50.28 §3), from
// GET /v1/broadcast/analytics. The cloud answers only the home's owner (403 otherwise) and only
// folded counts: a country or device with fewer than 5 sessions in the period is "Other", and
// nothing personal is kept. Pro's guest sharing sees live viewers and hours; the Broadcaster
// tier everything. Times are in the viewer's own time zone.
import { useState } from "react";
import { useParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { Select } from "@ds/components/Select/Select";
import { useToast } from "@ds/components/Toast/Toast";
import { api, fetchApi, withQuery } from "../shared/api";
import { useI18n } from "../shared/i18n";
import { LoadError, Loading, Page, useErrorMessage, useLoad } from "../shared/ui";

export interface Count {
  key: string;
  value: number;
}

export interface AnalyticsReport {
  level: "hours" | "full";
  from: string;
  to: string;
  time_zone: string;
  k: number;
  live_viewers: number;
  hours: { today: number; week: number; month: number; allowance: number | null };
  period_hours: number;
  peak?: number;
  sessions?: number;
  avg_watch_minutes?: number;
  rebuffer_ratio?: number;
  countries?: Count[];
  devices?: Count[];
  quality?: Count[];
  errors?: Count[];
  blocked?: Count[];
  daily?: {
    date: string;
    viewer_minutes: number;
    sessions: number;
    peak: number;
    errors: number;
  }[];
}

export type Period = "today" | "7d" | "month";
const PERIODS: Period[] = ["today", "7d", "month"];
const DEVICES = ["web", "ios", "tvos", "android", "androidtv", "desktop"];
const REASONS = ["geo", "full", "ip", "revoked"];

function zone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}

export function analyticsPath(server: string, period: Period, tz: string, format?: "csv") {
  return withQuery("/v1/broadcast/analytics", { server, period, tz, format });
}

export function BroadcastAnalytics() {
  const { id = "" } = useParams();
  const { t, n } = useI18n();
  const toast = useToast();
  const errorMessage = useErrorMessage();
  const [period, setPeriod] = useState<Period>("today");
  const [exporting, setExporting] = useState(false);
  const tz = zone();
  const report = useLoad(
    () => api.get<AnalyticsReport>(analyticsPath(id, period, tz)),
    `broadcast-analytics:${id}:${period}`
  );

  async function exportCSV() {
    setExporting(true);
    try {
      const res = await fetchApi("GET", analyticsPath(id, period, tz, "csv"));
      const url = URL.createObjectURL(await res.blob());
      const a = document.createElement("a");
      a.href = url;
      a.download = `maktaba-viewers-${period}.csv`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
    } catch (err) {
      toast.show({ tone: "error", message: errorMessage(err) });
    } finally {
      setExporting(false);
    }
  }

  const named = (prefix: string, known: string[], key: string) =>
    key === "other"
      ? t("broadcast.analytics.other")
      : known.includes(key)
        ? t(`${prefix}.${key}`)
        : key;
  const country = (code: string) => {
    if (code === "other") return t("broadcast.analytics.other");
    try {
      return (
        new Intl.DisplayNames([t("broadcast.analytics.locale")], { type: "region" }).of(code) ??
        code
      );
    } catch {
      return code;
    }
  };

  const r = report.data;
  return (
    <Page
      title={t("broadcast.analytics.title")}
      actions={
        <>
          <Select
            label={t("broadcast.analytics.period")}
            value={period}
            onChange={(e) => setPeriod(e.target.value as Period)}
            options={PERIODS.map((p) => ({
              value: p,
              label: t(`broadcast.analytics.period.${p}`),
            }))}
          />
          {r && (
            <Button variant="secondary" loading={exporting} onClick={() => void exportCSV()}>
              {t("broadcast.analytics.export")}
            </Button>
          )}
        </>
      }
    >
      {report.loading && <Loading />}
      {report.error !== null && report.error !== undefined && (
        <LoadError error={report.error} onRetry={report.reload} />
      )}
      {r && (
        <>
          <div className="mkc-stats">
            <Stat label={t("broadcast.analytics.live")} value={n(r.live_viewers)} />
            {r.peak !== undefined && (
              <Stat label={t("broadcast.analytics.peak")} value={n(r.peak)} />
            )}
            <Stat
              label={t("broadcast.analytics.hours")}
              value={n(r.period_hours, { maximumFractionDigits: 1 })}
            />
            {r.avg_watch_minutes !== undefined && (
              <Stat
                label={t("broadcast.analytics.avgWatch")}
                value={t("broadcast.analytics.minutes", {
                  n: n(r.avg_watch_minutes, { maximumFractionDigits: 0 }),
                })}
              />
            )}
          </div>
          <Card header={<h2>{t("broadcast.analytics.hours")}</h2>}>
            <dl className="mkc-dl">
              <dt>{t("broadcast.analytics.period.today")}</dt>
              <dd>{n(r.hours.today, { maximumFractionDigits: 1 })}</dd>
              <dt>{t("broadcast.analytics.period.7d")}</dt>
              <dd>{n(r.hours.week, { maximumFractionDigits: 1 })}</dd>
              <dt>{t("broadcast.analytics.period.month")}</dt>
              <dd>
                {r.hours.allowance
                  ? t("broadcast.analytics.ofAllowance", {
                      used: n(r.hours.month, { maximumFractionDigits: 1 }),
                      allowance: n(r.hours.allowance),
                    })
                  : n(r.hours.month, { maximumFractionDigits: 1 })}
              </dd>
            </dl>
            {r.hours.allowance ? (
              <ProgressBar
                label={t("broadcast.analytics.allowance")}
                value={Math.min(100, (r.hours.month / r.hours.allowance) * 100)}
              />
            ) : null}
          </Card>
          {r.level === "full" ? (
            <>
              <CountCard
                title={t("broadcast.analytics.countries")}
                counts={r.countries}
                label={country}
              />
              <CountCard
                title={t("broadcast.analytics.devices")}
                counts={r.devices}
                label={(k) => named("broadcast.analytics.device", DEVICES, k)}
              />
              <Card header={<h2>{t("broadcast.analytics.quality")}</h2>}>
                {(r.quality ?? []).length === 0 ? (
                  <p className="mkc-muted">{t("broadcast.analytics.none")}</p>
                ) : (
                  (r.quality ?? []).map((q) => (
                    <ProgressBar key={q.key} label={q.key} value={Math.round(q.value * 100)} />
                  ))
                )}
                <p className="mkc-muted">
                  {t("broadcast.analytics.rebuffer", {
                    pct: n(r.rebuffer_ratio ?? 0, { style: "percent", maximumFractionDigits: 2 }),
                  })}
                </p>
              </Card>
              <CountCard
                title={t("broadcast.analytics.errors")}
                counts={r.errors}
                label={(k) => k}
                code
              />
              <CountCard
                title={t("broadcast.analytics.blocked")}
                counts={r.blocked}
                label={(k) => named("broadcast.analytics.reason", REASONS, k)}
              />
            </>
          ) : (
            <p className="mkc-muted">{t("broadcast.analytics.upgrade")}</p>
          )}
          <p className="mkc-muted">{t("broadcast.analytics.privacy")}</p>
          <p className="mkc-muted">{t("broadcast.analytics.zone", { zone: r.time_zone })}</p>
        </>
      )}
    </Page>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <Card>
      <p className="mkc-muted">{label}</p>
      <p className="mkc-stat__value">
        <bdi>{value}</bdi>
      </p>
    </Card>
  );
}

function CountCard({
  title,
  counts,
  label,
  code,
}: {
  title: string;
  counts?: Count[];
  label: (key: string) => string;
  code?: boolean;
}) {
  const { t, n } = useI18n();
  const list = counts ?? [];
  return (
    <Card header={<h2>{title}</h2>}>
      {list.length === 0 ? (
        <p className="mkc-muted">{t("broadcast.analytics.none")}</p>
      ) : (
        <table className="mkc-table">
          <tbody>
            {list.map((c) => (
              <tr key={c.key}>
                <th scope="row">{code ? <code>{label(c.key)}</code> : label(c.key)}</th>
                <td>{n(c.value)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Card>
  );
}
