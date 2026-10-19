// Story 29.3 — Watch-analytics admin dashboard.
//
// The visual counterpart to the admin aggregate API. Cards: who's
// watching right now (live), headline KPIs, watch-time over time (line),
// top videos + most-active users (tables), popular genres + device +
// library breakdowns (bar lists) and the peak-hours heatmap. A range
// selector drives a coordinated refetch; export buttons hit the CSV/JSON
// endpoints (29.6).
//
// ACL: AdminGate is a UX guard only — every endpoint re-checks the
// principal server-side and 403s non-admins.
import { useCallback, useEffect, useRef, useState } from "react";
import { Card } from "@ds/components/Card/Card";
import { Select } from "@ds/components/Select/Select";
import { Button } from "@ds/components/Button/Button";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { AdminGate } from "../../components/AdminGate";
import { useI18n } from "../../lib/i18n";
import { ApiError } from "../../lib/api";
import { BarList, Heatmap, LineChart } from "../../components/charts/Charts";
import {
  analyticsApi,
  formatPercent,
  formatPercentRatio,
  formatWatchTime,
  type ActiveUser,
  type ActivityResponse,
  type LiveSession,
  type RangeKey,
  type Summary,
  type TopVideo,
} from "../../lib/analytics";

const RANGES: RangeKey[] = ["today", "7d", "30d", "90d", "1y", "all"];

// The server (or a misrouted proxy) may send null lists, missing keys or
// not even an object; every card treats those as empty instead of crashing.
const list = <T,>(v: unknown): T[] => (Array.isArray(v) ? (v.filter((x) => x != null) as T[]) : []);
const num = (v: unknown): number => (typeof v === "number" && Number.isFinite(v) ? v : 0);
const obj = (v: unknown): Record<string, unknown> =>
  v && typeof v === "object" ? (v as Record<string, unknown>) : {};

function cleanSummary(v: unknown): Summary {
  const s = obj(v);
  return {
    range: String(s.range ?? ""),
    total_watch_sec: num(s.total_watch_sec),
    total_sessions: num(s.total_sessions),
    unique_viewers: num(s.unique_viewers),
    completion_rate: num(s.completion_rate),
    devices: list(s.devices),
    platforms: list(s.platforms),
    libraries: list(s.libraries),
    genres: list(s.genres),
  };
}

function cleanActivity(v: unknown): ActivityResponse {
  const a = obj(v);
  return {
    bucket: (a.bucket as ActivityResponse["bucket"]) ?? "day",
    series: list(a.series),
    // A null row stays a (blank) row so the days keep their places.
    heatmap: Array.isArray(a.heatmap)
      ? a.heatmap.map((row) => (Array.isArray(row) ? row.map(num) : []))
      : [],
  };
}

export function AdminAnalytics() {
  return (
    <AdminGate>
      <AnalyticsInner />
    </AdminGate>
  );
}

function AnalyticsInner() {
  const { t } = useI18n();
  const [range, setRange] = useState<RangeKey>("7d");
  const [live, setLive] = useState<LiveSession[] | null>(null);
  const [summary, setSummary] = useState<Summary | null>(null);
  const [top, setTop] = useState<TopVideo[] | null>(null);
  const [activity, setActivity] = useState<ActivityResponse | null>(null);
  const [users, setUsers] = useState<ActiveUser[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  // Only the latest load may write state (fast range switching).
  const loadSeq = useRef(0);

  const load = useCallback(async () => {
    const seq = ++loadSeq.current;
    setErr(null);
    // Each card fills in on its own; one failing endpoint must not blank the rest.
    const [s, tv, ac, us] = await Promise.allSettled([
      analyticsApi.summary(range),
      analyticsApi.topVideos(range),
      analyticsApi.activityStats(range, "day"),
      analyticsApi.users(range),
    ]);
    if (seq !== loadSeq.current) return;
    // A card whose endpoint failed is cleared, so it never shows the old
    // range's numbers under the new range.
    setSummary(s.status === "fulfilled" ? cleanSummary(s.value) : null);
    setTop(tv.status === "fulfilled" ? list(obj(tv.value).videos) : null);
    setActivity(cleanActivity(ac.status === "fulfilled" ? ac.value : {}));
    setUsers(us.status === "fulfilled" ? list(obj(us.value).users) : null);
    const failed = [s, tv, ac, us].find((r) => r.status === "rejected");
    if (failed) {
      const e: unknown = failed.reason;
      setErr(e instanceof ApiError ? e.message : t("common.error"));
    }
  }, [range, t]);

  const loadLive = useCallback(async () => {
    try {
      const l = await analyticsApi.live();
      setLive(list<LiveSession>(obj(l).sessions));
    } catch {
      /* live is best-effort; leave prior value */
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  // Poll the live card every 15s so "currently watching" stays fresh.
  useEffect(() => {
    void loadLive();
    const id = window.setInterval(() => void loadLive(), 15_000);
    return () => window.clearInterval(id);
  }, [loadLive]);

  const dayLabels = [
    t("analytics.day.sun"),
    t("analytics.day.mon"),
    t("analytics.day.tue"),
    t("analytics.day.wed"),
    t("analytics.day.thu"),
    t("analytics.day.fri"),
    t("analytics.day.sat"),
  ];

  return (
    <section className="mkt-page mkt-analytics">
      <header className="mkt-page__header mkt-analytics__header">
        <h1>{t("analytics.title")}</h1>
        <div className="mkt-analytics__controls">
          <Select
            label={t("analytics.range")}
            value={range}
            options={RANGES.map((r) => ({ value: r, label: t(`analytics.range.${r}`) }))}
            onChange={(e) => setRange(e.target.value as RangeKey)}
          />
          <a className="mkt-btn mkt-btn--secondary" href={analyticsApi.exportUrl("csv", range)}>
            {t("analytics.export.csv")}
          </a>
          <a className="mkt-btn mkt-btn--secondary" href={analyticsApi.exportUrl("json", range)}>
            {t("analytics.export.json")}
          </a>
          <Button variant="secondary" onClick={() => void load()}>
            {t("common.retry")}
          </Button>
        </div>
      </header>

      {err && (
        <p className="mkt-error" role="alert">
          {err}
        </p>
      )}

      {/* Currently watching */}
      <Card header={<h2>{t("analytics.live.title")}</h2>} className="mkt-analytics__live">
        {live === null ? (
          <p className="mkt-loading">{t("common.loading")}</p>
        ) : live.length === 0 ? (
          <EmptyState title={t("analytics.live.empty")} />
        ) : (
          <table className="mkt-table" aria-label={t("analytics.live.title")}>
            <thead>
              <tr>
                <th>{t("analytics.col.user")}</th>
                <th>{t("analytics.col.video")}</th>
                <th>{t("analytics.col.elapsed")}</th>
                <th>{t("analytics.col.progress")}</th>
                <th>{t("analytics.col.device")}</th>
              </tr>
            </thead>
            <tbody>
              {live.map((s) => (
                <tr key={s.session_id}>
                  <td dir="auto">{s.username}</td>
                  <td dir="auto">{s.title}</td>
                  <td>{formatWatchTime(num(s.elapsed_sec))}</td>
                  <td>{formatPercent(num(s.percent_complete))}</td>
                  <td>{s.device_type || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Card>

      {/* KPI row */}
      <div className="mkt-analytics__kpis">
        <Kpi
          label={t("analytics.kpi.watchTime")}
          value={summary ? formatWatchTime(summary.total_watch_sec) : "—"}
        />
        <Kpi
          label={t("analytics.kpi.sessions")}
          value={summary ? String(summary.total_sessions) : "—"}
        />
        <Kpi
          label={t("analytics.kpi.viewers")}
          value={summary ? String(summary.unique_viewers) : "—"}
        />
        <Kpi
          label={t("analytics.kpi.completion")}
          value={summary ? formatPercentRatio(summary.completion_rate) : "—"}
        />
      </div>

      <div className="mkt-analytics__grid">
        {/* Watch time over time */}
        <Card header={<h2>{t("analytics.watchTimeOverTime")}</h2>}>
          <LineChart
            ariaLabel={t("analytics.watchTimeOverTime")}
            points={(activity?.series ?? []).map((p) => ({
              label: p.bucket ?? "",
              value: num(p.watch_sec),
            }))}
          />
        </Card>

        {/* Peak hours */}
        <Card header={<h2>{t("analytics.peakHours")}</h2>}>
          {activity && activity.heatmap.length === 0 ? (
            <p className="mkt-muted">—</p>
          ) : activity ? (
            <Heatmap
              matrix={activity.heatmap}
              dayLabels={dayLabels}
              ariaLabel={t("analytics.peakHours")}
            />
          ) : (
            <p className="mkt-loading">{t("common.loading")}</p>
          )}
        </Card>

        {/* Top videos */}
        <Card header={<h2>{t("analytics.topVideos")}</h2>}>
          <BarList
            ariaLabel={t("analytics.topVideos")}
            items={(top ?? []).map((v) => ({
              label: v.title ?? "",
              value: num(v.sessions),
              sub: `${num(v.sessions)} · ${formatWatchTime(num(v.watch_sec))}`,
            }))}
          />
        </Card>

        {/* Active users */}
        <Card header={<h2>{t("analytics.activeUsers")}</h2>}>
          <BarList
            ariaLabel={t("analytics.activeUsers")}
            items={(users ?? []).map((u) => ({
              label: u.username ?? "",
              value: num(u.watch_sec),
              sub: formatWatchTime(num(u.watch_sec)),
            }))}
          />
        </Card>

        {/* Genres */}
        <Card header={<h2>{t("analytics.genres")}</h2>}>
          <BarList
            ariaLabel={t("analytics.genres")}
            items={(summary?.genres ?? []).map((g) => ({
              label: g.label ?? "",
              value: num(g.sessions),
              sub: String(num(g.sessions)),
            }))}
          />
        </Card>

        {/* Devices */}
        <Card header={<h2>{t("analytics.devices")}</h2>}>
          <BarList
            ariaLabel={t("analytics.devices")}
            items={(summary?.devices ?? []).map((d) => ({
              label: d.label ?? "",
              value: num(d.sessions),
              sub: String(num(d.sessions)),
            }))}
          />
        </Card>

        {/* Libraries */}
        <Card header={<h2>{t("analytics.libraries")}</h2>}>
          <BarList
            ariaLabel={t("analytics.libraries")}
            items={(summary?.libraries ?? []).map((l) => ({
              label: l.label ?? "",
              value: num(l.sessions),
              sub: formatWatchTime(num(l.watch_sec)),
            }))}
          />
        </Card>
      </div>
    </section>
  );
}

function Kpi({ label, value }: { label: string; value: string }) {
  return (
    <Card className="mkt-kpi">
      <span className="mkt-kpi__value">{value}</span>
      <span className="mkt-kpi__label">{label}</span>
    </Card>
  );
}
