// The analytics page's tabs (Epic 44 story 44.7). Each loads its own
// endpoints for the query it is given, so only the open tab asks the server.
import { useCallback, useEffect, useState } from "react";
import { Card } from "@ds/components/Card/Card";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { useI18n } from "../../../lib/i18n";
import { BarList, Heatmap, LineChart } from "../../../components/charts/Charts";
import {
  analyticsApi,
  formatPercent,
  formatPercentRatio,
  formatWatchTime,
  type ActivityResponse,
  type AppUsage,
  type NowPlay,
  type NowViewing,
  type Person,
  type RemoteDay,
  type TopChannel,
  type TopVideo,
  type UsagePoint,
} from "../../../lib/analytics";
import {
  Kpi,
  Kpis,
  LoadError,
  Panel,
  cleanStats,
  list,
  num,
  obj,
  str,
  surfaceKeys,
  useLabels,
  useLoad,
  type TabProps,
} from "./parts";

const NOW_POLL_MS = 15_000;

function cleanActivity(v: unknown): ActivityResponse {
  const a = obj(v);
  return {
    bucket: (str(a.bucket) || "day") as ActivityResponse["bucket"],
    series: list<Record<string, unknown>>(a.series).map((p) => ({
      bucket: str(p.bucket),
      watch_sec: num(p.watch_sec),
      video_sec: num(p.video_sec),
      tv_sec: num(p.tv_sec),
      plays: num(p.plays),
      viewings: num(p.viewings),
    })),
    // A null row stays a (blank) row so the days keep their places.
    heatmap: Array.isArray(a.heatmap)
      ? a.heatmap.map((row) => (Array.isArray(row) ? row.map(num) : []))
      : [],
  };
}

function useDayLabels() {
  const { t } = useI18n();
  return ["sun", "mon", "tue", "wed", "thu", "fri", "sat"].map((d) => t(`analytics.day.${d}`));
}

// ─── Overview ──────────────────────────────────────────────────────────

export function OverviewTab({ q, nonce }: TabProps) {
  const { t, n } = useI18n();
  const label = useLabels();
  const days = useDayLabels();
  const summary = useLoad(
    useCallback(() => analyticsApi.summary(q, nonce > 0).then(obj), [q, nonce]),
    nonce
  );
  const activity = useLoad(
    useCallback(() => analyticsApi.activityStats(q).then(cleanActivity), [q]),
    nonce
  );
  const s = summary.data;
  const surfaces = cleanStats(s?.surfaces);
  const apps = cleanStats(s?.apps);
  const series = activity.data?.series ?? [];
  const nothing = !!s && num(s.watch_sec) === 0 && num(s.plays) === 0 && num(s.viewings) === 0;
  return (
    <>
      <LoadError loads={[summary, activity]} />
      <Kpis>
        <Kpi
          label={t("analytics.kpi.watchTime")}
          value={s ? formatWatchTime(num(s.watch_sec)) : "—"}
        />
        <Kpi label={t("analytics.kpi.plays")} value={s ? n(num(s.plays)) : "—"} />
        <Kpi label={t("analytics.kpi.viewings")} value={s ? n(num(s.viewings)) : "—"} />
        <Kpi label={t("analytics.kpi.people")} value={s ? n(num(s.people)) : "—"} />
        <Kpi
          label={t("analytics.kpi.liveTvHours")}
          value={s ? n(num(s.live_tv_hours), { maximumFractionDigits: 1 }) : "—"}
        />
      </Kpis>
      {nothing && <EmptyState kind="filtered_out" title={t("analytics.empty")} />}
      <div className="mkt-analytics__grid">
        <Panel title={t("analytics.bySurface")} loading={summary.loading && !s}>
          <BarList
            ariaLabel={t("analytics.bySurface")}
            items={surfaces.map((c) => ({
              label: label.surface(c.label),
              value: c.watch_sec,
              sub: formatWatchTime(c.watch_sec),
            }))}
          />
        </Panel>
        <Panel title={t("analytics.byApp")} loading={summary.loading && !s}>
          <BarList
            ariaLabel={t("analytics.byApp")}
            items={apps.map((c) => ({
              label: label.app(c.label),
              value: c.watch_sec,
              sub: formatWatchTime(c.watch_sec),
            }))}
          />
        </Panel>
        <Panel title={t("analytics.trend")} loading={activity.loading && !activity.data}>
          <LineChart
            ariaLabel={t("analytics.trend")}
            points={series.map((p) => ({ label: p.bucket, value: p.watch_sec }))}
          />
        </Panel>
        <Panel title={t("analytics.peakHours")} loading={activity.loading && !activity.data}>
          {activity.data && activity.data.heatmap.length > 0 ? (
            <Heatmap
              matrix={activity.data.heatmap}
              dayLabels={days}
              ariaLabel={t("analytics.peakHours")}
            />
          ) : (
            <p className="mkt-muted">—</p>
          )}
        </Panel>
      </div>
    </>
  );
}

// ─── Now ───────────────────────────────────────────────────────────────

// NowRow is a play or a viewing, as the table shows it.
interface NowRow {
  key: string;
  person: string;
  what: string;
  surface: string;
  app: string;
  device: string;
  elapsed: number;
  progress: number | null;
}

function nowRows(v: unknown, tuner: string): NowRow[] {
  const d = obj(v);
  const plays = list<NowPlay>(d.plays).map((p) => ({
    key: `p-${str(p.session_id)}`,
    person: str(p.username),
    what: str(p.title),
    surface: "video",
    app: [str(p.client_app), str(p.client_version)].filter(Boolean).join(" "),
    device: [str(p.device_type), str(p.platform)].filter(Boolean).join(" · "),
    elapsed: num(p.elapsed_sec),
    progress: num(p.percent_complete),
  }));
  const views = list<NowViewing>(d.viewings).map((v) => ({
    key: `v-${str(v.id)}`,
    person: str(v.username) || (v.surface === "tuner" ? tuner : ""),
    what: [num(v.channel_number) || "", str(v.channel_name)].filter(Boolean).join(" "),
    surface: str(v.surface),
    app: [str(v.client_app), str(v.client_version)].filter(Boolean).join(" "),
    device: [str(v.device_type), str(v.platform)].filter(Boolean).join(" · "),
    elapsed: num(v.elapsed_sec),
    progress: null,
  }));
  return [...plays, ...views];
}

export function NowTab({ q, nonce }: TabProps) {
  const { t, n } = useI18n();
  const label = useLabels();
  // The tab asks again every 15 s while it is open, and keeps what it last
  // showed if one ask fails.
  const [tick, setTick] = useState(0);
  useEffect(() => {
    const id = window.setInterval(() => setTick((x) => x + 1), NOW_POLL_MS);
    return () => window.clearInterval(id);
  }, []);
  const now = useLoad(
    useCallback(() => analyticsApi.now(q), [q]),
    `${nonce}-${tick}`,
    true
  );
  const rows = now.data ? nowRows(now.data, t("analytics.now.tuner")) : null;
  const counts = obj(obj(now.data).counts);
  return (
    <>
      <LoadError loads={[now]} />
      <Kpis>
        {surfaceKeys.map((s) => (
          <Kpi
            key={s}
            label={t(`analytics.surface.${s}`)}
            value={now.data ? n(num(counts[s])) : "—"}
          />
        ))}
      </Kpis>
      <Card header={<h2>{t("analytics.now.title")}</h2>}>
        <p className="mkt-muted">{t("analytics.now.auto")}</p>
        {rows === null ? (
          <p className="mkt-loading">{t("common.loading")}</p>
        ) : rows.length === 0 ? (
          <EmptyState title={t("analytics.now.empty")} />
        ) : (
          <table className="mkt-table" aria-label={t("analytics.now.title")}>
            <thead>
              <tr>
                <th>{t("analytics.col.person")}</th>
                <th>{t("analytics.col.what")}</th>
                <th>{t("analytics.col.surface")}</th>
                <th>{t("analytics.col.app")}</th>
                <th>{t("analytics.col.device")}</th>
                <th>{t("analytics.col.elapsed")}</th>
                <th>{t("analytics.col.progress")}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.key}>
                  <td dir="auto">{label.dash(r.person)}</td>
                  <td dir="auto">{label.dash(r.what)}</td>
                  <td>{label.surface(r.surface)}</td>
                  <td dir="auto">{label.app(r.app)}</td>
                  <td dir="auto">{label.dash(r.device)}</td>
                  <td>{formatWatchTime(r.elapsed)}</td>
                  <td>{r.progress === null ? "—" : formatPercent(r.progress)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Card>
    </>
  );
}

// ─── Videos ────────────────────────────────────────────────────────────

export function VideosTab({ q, nonce }: TabProps) {
  const { t, n } = useI18n();
  const summary = useLoad(
    useCallback(() => analyticsApi.summary(q, nonce > 0).then(obj), [q, nonce]),
    nonce
  );
  const top = useLoad(
    useCallback(
      () => analyticsApi.topVideos(q, 20).then((v) => list<TopVideo>(obj(v).videos)),
      [q]
    ),
    nonce
  );
  const libs = useLoad(
    useCallback(() => analyticsApi.libraries(q).then((v) => cleanStats(obj(v).libraries)), [q]),
    nonce
  );
  const genres = useLoad(
    useCallback(() => analyticsApi.genres(q).then((v) => cleanStats(obj(v).genres)), [q]),
    nonce
  );
  const s = summary.data;
  return (
    <>
      <LoadError loads={[summary, top, libs, genres]} />
      <Kpis>
        <Kpi label={t("analytics.kpi.plays")} value={s ? n(num(s.plays)) : "—"} />
        <Kpi
          label={t("analytics.kpi.videoTime")}
          value={s ? formatWatchTime(num(s.video_sec)) : "—"}
        />
        <Kpi
          label={t("analytics.kpi.completion")}
          value={s ? formatPercentRatio(num(s.completion_rate)) : "—"}
        />
      </Kpis>
      <div className="mkt-analytics__grid">
        <Panel
          title={t("analytics.topVideos")}
          loading={top.loading && !top.data}
          empty={!!top.data && top.data.length === 0}
          emptyText={t("analytics.empty.videos")}
        >
          <table className="mkt-table" aria-label={t("analytics.topVideos")}>
            <thead>
              <tr>
                <th>{t("analytics.col.video")}</th>
                <th>{t("analytics.col.plays")}</th>
                <th>{t("analytics.col.watchTime")}</th>
                <th>{t("analytics.col.viewers")}</th>
                <th>{t("analytics.col.completion")}</th>
              </tr>
            </thead>
            <tbody>
              {(top.data ?? []).map((v, i) => (
                <tr key={`${str(v.video_id)}-${i}`}>
                  <td dir="auto">{str(v.title) || "—"}</td>
                  <td>{n(num(v.sessions))}</td>
                  <td>{formatWatchTime(num(v.watch_sec))}</td>
                  <td>{n(num(v.unique_viewers))}</td>
                  <td>{formatPercentRatio(num(v.completion_rate))}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Panel>
        <Panel title={t("analytics.libraries")} loading={libs.loading && !libs.data}>
          <BarList
            ariaLabel={t("analytics.libraries")}
            items={(libs.data ?? []).map((l) => ({
              label: l.label,
              value: l.watch_sec,
              sub: formatWatchTime(l.watch_sec),
            }))}
          />
        </Panel>
        <Panel title={t("analytics.genres")} loading={genres.loading && !genres.data}>
          <BarList
            ariaLabel={t("analytics.genres")}
            items={(genres.data ?? []).map((g) => ({
              label: g.label,
              value: g.sessions,
              sub: n(g.sessions),
            }))}
          />
        </Panel>
      </div>
    </>
  );
}

// ─── Live TV ───────────────────────────────────────────────────────────

export function LiveTVTab({ q, nonce }: TabProps) {
  const { t, n, formatDate } = useI18n();
  const label = useLabels();
  const channels = useLoad(
    useCallback(() => analyticsApi.topChannels(q, 20).then(obj), [q]),
    nonce
  );
  const remote = useLoad(
    useCallback(() => analyticsApi.remoteTV(q).then(obj), [q]),
    nonce
  );
  const activity = useLoad(
    useCallback(() => analyticsApi.activityStats(q).then(cleanActivity), [q]),
    nonce
  );
  const c = channels.data;
  const r = remote.data;
  const rows = list<TopChannel>(c?.channels);
  const days = list<RemoteDay>(r?.days);
  const peakAt = str(c?.peak_at);
  return (
    <>
      <LoadError loads={[channels, remote, activity]} />
      <Kpis>
        <Kpi label={t("analytics.kpi.peak")} value={c ? n(num(c.peak_viewers)) : "—"} />
        <Kpi
          label={t("analytics.kpi.remoteMinutes")}
          value={r ? n(num(r.minutes_delivered), { maximumFractionDigits: 0 }) : "—"}
        />
        <Kpi
          label={t("analytics.kpi.remoteCost")}
          value={r ? n(num(r.cost_usd), { style: "currency", currency: "USD" }) : "—"}
        />
      </Kpis>
      {peakAt && <p className="mkt-muted">{t("analytics.peakAt", { time: formatDate(peakAt) })}</p>}
      <div className="mkt-analytics__grid">
        <Panel
          title={t("analytics.topChannels")}
          loading={channels.loading && !c}
          empty={!!c && rows.length === 0}
          emptyText={t("analytics.empty.channels")}
        >
          <table className="mkt-table" aria-label={t("analytics.topChannels")}>
            <thead>
              <tr>
                <th>{t("analytics.col.channel")}</th>
                <th>{t("analytics.col.viewings")}</th>
                <th>{t("analytics.col.watchTime")}</th>
                <th>{t("analytics.col.people")}</th>
                <th>{t("analytics.col.peak")}</th>
                <th>{t("analytics.col.surface")}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((ch, i) => (
                <tr key={`${str(ch.channel_id)}-${i}`}>
                  <td dir="auto">
                    {[num(ch.number) || "", str(ch.name)].filter(Boolean).join(" ") || "—"}
                  </td>
                  <td>{n(num(ch.viewings))}</td>
                  <td>{formatWatchTime(num(ch.watch_sec))}</td>
                  <td>{n(num(ch.people))}</td>
                  <td>{n(num(ch.peak_viewers))}</td>
                  <td>
                    {cleanStats(ch.surfaces)
                      .map((s) => `${label.surface(s.label)} ${formatWatchTime(s.watch_sec)}`)
                      .join(" · ")}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </Panel>
        <Panel title={t("analytics.channelSurfaces")} loading={channels.loading && !c}>
          <BarList
            ariaLabel={t("analytics.channelSurfaces")}
            items={cleanStats(c?.surfaces).map((s) => ({
              label: label.surface(s.label),
              value: s.watch_sec,
              sub: formatWatchTime(s.watch_sec),
            }))}
          />
        </Panel>
        <Panel title={t("analytics.trend.tv")} loading={activity.loading && !activity.data}>
          <LineChart
            ariaLabel={t("analytics.trend.tv")}
            points={(activity.data?.series ?? []).map((p) => ({
              label: p.bucket,
              value: p.tv_sec,
            }))}
          />
        </Panel>
        <Panel
          title={t("analytics.remote.title")}
          loading={remote.loading && !r}
          empty={!!r && days.length === 0}
          emptyText={t("analytics.empty.remote")}
        >
          <table className="mkt-table" aria-label={t("analytics.remote.title")}>
            <thead>
              <tr>
                <th>{t("analytics.col.day")}</th>
                <th>{t("analytics.col.minutes")}</th>
                <th>{t("analytics.col.cost")}</th>
              </tr>
            </thead>
            <tbody>
              {days.map((d, i) => (
                <tr key={`${str(d.day)}-${i}`}>
                  <td>{str(d.day)}</td>
                  <td>{n(num(d.minutes_delivered), { maximumFractionDigits: 0 })}</td>
                  <td>{n(num(d.cost_usd), { style: "currency", currency: "USD" })}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Panel>
      </div>
    </>
  );
}

// ─── People ────────────────────────────────────────────────────────────

export function PeopleTab({ q, nonce }: TabProps) {
  const { t, n, formatDate } = useI18n();
  const label = useLabels();
  const people = useLoad(
    useCallback(() => analyticsApi.people(q).then((v) => list<Person>(obj(v).people)), [q]),
    nonce
  );
  const rows = people.data;
  return (
    <>
      <LoadError loads={[people]} />
      <Panel
        title={t("analytics.people.title")}
        loading={people.loading && !rows}
        empty={!!rows && rows.length === 0}
        emptyText={t("analytics.empty.people")}
      >
        <table className="mkt-table" aria-label={t("analytics.people.title")}>
          <thead>
            <tr>
              <th>{t("analytics.col.person")}</th>
              <th>{t("analytics.col.watchTime")}</th>
              <th>{t("analytics.col.plays")}</th>
              <th>{t("analytics.col.viewings")}</th>
              <th>{t("analytics.col.channels")}</th>
              <th>{t("analytics.col.apps")}</th>
              <th>{t("analytics.col.lastWatched")}</th>
              <th>{t("analytics.col.lastActive")}</th>
            </tr>
          </thead>
          <tbody>
            {(rows ?? []).map((p, i) => (
              <tr key={`${str(p.user_id)}-${i}`}>
                <td dir="auto">{str(p.username) || str(p.user_id) || "—"}</td>
                <td>{formatWatchTime(num(p.watch_sec))}</td>
                <td>{n(num(p.plays))}</td>
                <td>{n(num(p.viewings))}</td>
                <td>{n(num(p.channels))}</td>
                <td dir="auto">{list<string>(p.apps).map(label.app).join(", ") || "—"}</td>
                <td>{str(p.last_watched_at) ? formatDate(str(p.last_watched_at)) : "—"}</td>
                <td>{label.dash(str(p.last_active_day))}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </Panel>
    </>
  );
}

// ─── Apps ──────────────────────────────────────────────────────────────

export function AppsTab({ q, nonce }: TabProps) {
  const { t, n } = useI18n();
  const label = useLabels();
  const apps = useLoad(
    useCallback(() => analyticsApi.apps(q).then(obj), [q]),
    nonce
  );
  const a = apps.data;
  const rows = list<AppUsage>(a?.apps);
  const trend = list<UsagePoint>(a?.trend);
  const requests = rows.reduce((s, r) => s + num(r.requests), 0);
  const errors = rows.reduce((s, r) => s + num(r.errors), 0);
  const people = trend.reduce((m, p) => Math.max(m, num(p.people)), 0);
  const pct = (v: unknown) => n(num(v), { style: "percent", maximumFractionDigits: 1 });
  const ms = (v: unknown) => t("analytics.ms", { n: n(num(v), { maximumFractionDigits: 0 }) });
  return (
    <>
      <LoadError loads={[apps]} />
      <Kpis>
        <Kpi label={t("analytics.kpi.requests")} value={a ? n(requests) : "—"} />
        <Kpi label={t("analytics.kpi.errors")} value={a ? n(errors) : "—"} />
        <Kpi label={t("analytics.kpi.appPeople")} value={a ? n(people) : "—"} />
      </Kpis>
      <div className="mkt-analytics__grid">
        <Panel
          title={t("analytics.apps.title")}
          loading={apps.loading && !a}
          empty={!!a && rows.length === 0}
          emptyText={t("analytics.empty.apps")}
        >
          <table className="mkt-table" aria-label={t("analytics.apps.title")}>
            <thead>
              <tr>
                <th>{t("analytics.col.app")}</th>
                <th>{t("analytics.col.people")}</th>
                <th>{t("analytics.col.requests")}</th>
                <th>{t("analytics.col.errors")}</th>
                <th>{t("analytics.col.errorRate")}</th>
                <th>{t("analytics.col.slow")}</th>
                <th>{t("analytics.col.avgMs")}</th>
                <th>{t("analytics.col.versions")}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r, i) => (
                <tr key={`${str(r.client_app)}-${i}`}>
                  <td dir="auto">{label.app(str(r.client_app))}</td>
                  <td>{n(num(r.people))}</td>
                  <td>{n(num(r.requests))}</td>
                  <td>{n(num(r.errors))}</td>
                  <td>{pct(r.error_rate)}</td>
                  <td>{n(num(r.slow))}</td>
                  <td>{ms(r.avg_ms)}</td>
                  <td dir="auto">
                    {list<{ version: string; people: number }>(r.versions)
                      .map((v) => `${str(v.version) || "—"} (${n(num(v.people))})`)
                      .join(", ") || "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </Panel>
        <Panel
          title={t("analytics.apps.platforms")}
          loading={apps.loading && !a}
          empty={!!a && rows.length === 0}
          emptyText={t("analytics.empty.apps")}
        >
          <table className="mkt-table" aria-label={t("analytics.apps.platforms")}>
            <thead>
              <tr>
                <th>{t("analytics.col.app")}</th>
                <th>{t("analytics.col.platform")}</th>
                <th>{t("analytics.col.requests")}</th>
                <th>{t("analytics.col.errorRate")}</th>
                <th>{t("analytics.col.slow")}</th>
                <th>{t("analytics.col.avgMs")}</th>
              </tr>
            </thead>
            <tbody>
              {rows.flatMap((r, i) =>
                list<AppUsage["platforms"][number]>(r.platforms).map((p, j) => (
                  <tr key={`${i}-${j}`}>
                    <td dir="auto">{label.app(str(r.client_app))}</td>
                    <td dir="auto">{label.dash(str(p.platform))}</td>
                    <td>{n(num(p.requests))}</td>
                    <td>{pct(p.error_rate)}</td>
                    <td>{n(num(p.slow))}</td>
                    <td>{ms(p.avg_ms)}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </Panel>
        <Panel title={t("analytics.apps.trend")} loading={apps.loading && !a}>
          <LineChart
            ariaLabel={t("analytics.apps.trend")}
            points={trend.map((p) => ({ label: str(p.day), value: num(p.requests) }))}
          />
        </Panel>
        <Panel title={t("analytics.platforms")} loading={apps.loading && !a}>
          <BarList
            ariaLabel={t("analytics.platforms")}
            items={cleanStats(a?.platforms).map((s) => ({
              label: label.value(s.label),
              value: s.watch_sec,
              sub: formatWatchTime(s.watch_sec),
            }))}
          />
        </Panel>
        <Panel title={t("analytics.devices")} loading={apps.loading && !a}>
          <BarList
            ariaLabel={t("analytics.devices")}
            items={cleanStats(a?.devices).map((s) => ({
              label: label.value(s.label),
              value: s.watch_sec,
              sub: formatWatchTime(s.watch_sec),
            }))}
          />
        </Panel>
      </div>
    </>
  );
}
