// Story 29.3, rebuilt by Epic 44 story 44.7 — the admin analytics page.
//
// Every way of watching (videos in the apps, Live TV in the apps, Plex Live
// TV through the tuner, the remote TV) and every app's use of the server, as
// tabs over one range (today, 7, 30, 90 days or custom dates) and one set of
// filters (library, person, app, way of watching):
//
//   Overview  watch time, plays, viewings, people, Live TV hours; by way of
//             watching and by app; the trend and the day x hour heatmap
//   Now       who is watching what, on which app and device (every 15 s)
//   Videos    top videos (with completion), libraries, genres
//   Live TV   top channels by way of watching, peak viewers, the remote TV's
//             minutes and cost
//   People    per person: watch time, plays, channels, apps, last seen
//   Apps      per app and platform: people, requests, errors, speed,
//             versions in use; platforms and devices
//
// Export downloads the plays and viewings of the range and filters (CSV or
// JSON). A return shows each tab's last answers for the same range and
// filters at once (useLoad's key), the live Now tab excepted. ACL: AdminGate
// is a UX guard only — every endpoint re-checks the
// principal server-side and 403s non-admins.
import { useCallback, useMemo, useState } from "react";
import { Select } from "@ds/components/Select/Select";
import { Input } from "@ds/components/Input/Input";
import { Button } from "@ds/components/Button/Button";
import { Tabs } from "@ds/components/Tabs/Tabs";
import { AdminGate } from "../../components/AdminGate";
import { useI18n } from "../../lib/i18n";
import {
  SURFACES,
  analyticsApi,
  type AnalyticsQuery,
  type RangeKey,
  type Surface,
} from "../../lib/analytics";
import { TitleRow } from "../../help/HelpLink";
import { list, obj, str, useLabels, useLoad } from "./analytics/parts";
import { AppsTab, LiveTVTab, NowTab, OverviewTab, PeopleTab, VideosTab } from "./analytics/tabs";

type RangeChoice = RangeKey | "custom";
const RANGES: RangeChoice[] = ["today", "7d", "30d", "90d", "custom"];
// The apps that say which they are (X-Maktaba-Client), and "unknown" for
// the ones that do not (old builds).
export const APPS = ["web", "desktop", "mobile", "player", "remote", "tv", "unknown"];
export const TABS = ["overview", "now", "videos", "livetv", "people", "apps"] as const;
type Tab = (typeof TABS)[number];

const isoDay = (d: Date) => d.toISOString().slice(0, 10);

export function AdminAnalytics() {
  return (
    <AdminGate>
      <AnalyticsInner />
    </AdminGate>
  );
}

function AnalyticsInner() {
  const { t } = useI18n();
  const label = useLabels();
  const [tab, setTab] = useState<Tab>("overview");
  const [range, setRange] = useState<RangeChoice>("7d");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [library, setLibrary] = useState("");
  const [user, setUser] = useState("");
  const [app, setApp] = useState("");
  const [surface, setSurface] = useState<Surface | "">("");
  const [nonce, setNonce] = useState(0);

  // The range alone (the filter lists offer what the range holds), and the
  // range with the filters (what every tab reads).
  const rangeQ = useMemo<AnalyticsQuery>(
    () => (range === "custom" ? { from: from || undefined, to: to || undefined } : { range }),
    [range, from, to]
  );
  const q = useMemo<AnalyticsQuery>(
    () => ({
      ...rangeQ,
      library: library || undefined,
      user: user || undefined,
      client_app: app || undefined,
      surface: surface || undefined,
    }),
    [rangeQ, library, user, app, surface]
  );

  // The libraries and people seen in the range fill the filter lists.
  const libs = useLoad(
    useCallback(
      () =>
        analyticsApi
          .libraries(rangeQ)
          .then((v) => list<{ id: string; label: string }>(obj(v).libraries)),
      [rangeQ]
    ),
    nonce,
    false,
    `filter-libraries:${JSON.stringify(rangeQ)}`
  );
  const people = useLoad(
    useCallback(
      () =>
        analyticsApi
          .people(rangeQ, 500)
          .then((v) => list<{ user_id: string; username: string }>(obj(v).people)),
      [rangeQ]
    ),
    nonce,
    false,
    `filter-people:${JSON.stringify(rangeQ)}`
  );
  const withSelected = (opts: { value: string; label: string }[], value: string) =>
    value && !opts.some((o) => o.value === value) ? [...opts, { value, label: value }] : opts;
  const all = { value: "", label: t("analytics.filter.all") };
  const libraryOptions = [
    all,
    ...withSelected(
      (libs.data ?? []).map((l) => ({ value: str(l.id), label: str(l.label) })),
      library
    ),
  ];
  const personOptions = [
    all,
    ...withSelected(
      (people.data ?? [])
        .filter((p) => str(p.user_id))
        .map((p) => ({ value: str(p.user_id), label: str(p.username) || str(p.user_id) })),
      user
    ),
  ];
  const filtered = !!(library || user || app || surface);

  function pickRange(v: RangeChoice) {
    if (v === "custom" && !from) {
      const today = new Date();
      setFrom(isoDay(new Date(today.getTime() - 7 * 86_400_000)));
      setTo(isoDay(today));
    }
    setRange(v);
  }

  function clearFilters() {
    setLibrary("");
    setUser("");
    setApp("");
    setSurface("");
  }

  const props = { q, nonce };
  const content: Record<Tab, JSX.Element> = {
    overview: <OverviewTab {...props} />,
    now: <NowTab {...props} />,
    videos: <VideosTab {...props} />,
    livetv: <LiveTVTab {...props} />,
    people: <PeopleTab {...props} />,
    apps: <AppsTab {...props} />,
  };

  return (
    <section className="mkt-page mkt-analytics">
      <header className="mkt-page__header mkt-analytics__header">
        <TitleRow>
          <h1>{t("analytics.title")}</h1>
        </TitleRow>
        <div className="mkt-analytics__controls">
          <a className="mkt-btn mkt-btn--secondary" href={analyticsApi.exportUrl("csv", q)}>
            {t("analytics.export.csv")}
          </a>
          <a className="mkt-btn mkt-btn--secondary" href={analyticsApi.exportUrl("json", q)}>
            {t("analytics.export.json")}
          </a>
          <Button variant="secondary" onClick={() => setNonce((x) => x + 1)}>
            {t("analytics.refresh")}
          </Button>
        </div>
      </header>

      <div className="mkt-analytics__filters" role="group" aria-label={t("analytics.filters")}>
        <Select
          label={t("analytics.range")}
          value={range}
          options={RANGES.map((r) => ({ value: r, label: t(`analytics.range.${r}`) }))}
          onChange={(e) => pickRange(e.target.value as RangeChoice)}
        />
        {range === "custom" && (
          <>
            <Input
              type="date"
              label={t("analytics.from")}
              value={from}
              max={to || undefined}
              onChange={(e) => setFrom(e.target.value)}
            />
            <Input
              type="date"
              label={t("analytics.to")}
              value={to}
              min={from || undefined}
              onChange={(e) => setTo(e.target.value)}
            />
          </>
        )}
        <Select
          label={t("analytics.filter.library")}
          value={library}
          options={libraryOptions}
          onChange={(e) => setLibrary(e.target.value)}
        />
        <Select
          label={t("analytics.filter.person")}
          value={user}
          options={personOptions}
          onChange={(e) => setUser(e.target.value)}
        />
        <Select
          label={t("analytics.filter.app")}
          value={app}
          options={[all, ...APPS.map((a) => ({ value: a, label: label.app(a) }))]}
          onChange={(e) => setApp(e.target.value)}
        />
        <Select
          label={t("analytics.filter.surface")}
          value={surface}
          options={[all, ...SURFACES.map((s) => ({ value: s, label: label.surface(s) }))]}
          onChange={(e) => setSurface(e.target.value as Surface | "")}
        />
        {filtered && (
          <Button variant="secondary" onClick={clearFilters}>
            {t("analytics.filter.clear")}
          </Button>
        )}
      </div>

      <Tabs
        label={t("analytics.tabs")}
        value={tab}
        onChange={(id) => setTab(id as Tab)}
        items={TABS.map((id) => ({
          id,
          label: t(`analytics.tab.${id}`),
          content: content[id],
        }))}
      />
    </section>
  );
}
