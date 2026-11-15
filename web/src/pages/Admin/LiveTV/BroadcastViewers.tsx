// The Broadcast panel's viewers (Epic 50 story 50.28 §3): who watches this home's channels, from
// Maktaba Cloud's report. The home's API asks through the relay agent with the home's own
// credential (the cloud answers for this home only) and only the home's admins may ask. The
// report comes folded: a country or device with fewer than 5 sessions in an hour is "Other" for
// that hour, and the panel shows it as it came, never finer. Viewers now, viewer-hours today,
// over 7 days and this month against the plan's allowance; on the Broadcaster tier, the
// period's peak, average watch time, countries and devices. Hidden while the home isn't claimed.
import { useCallback, useEffect, useState } from "react";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { Select } from "@ds/components/Select/Select";
import { useI18n } from "../../../lib/i18n";
import {
  tvBroadcastApi,
  type ViewerCount,
  type ViewerPeriod,
  type ViewersView,
} from "../../../lib/tvBroadcastApi";

const PERIODS: ViewerPeriod[] = ["today", "7d", "month"];
const DEVICES = ["web", "ios", "tvos", "android", "androidtv", "desktop"];

function zone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}

export function BroadcastViewers() {
  const { t, n } = useI18n();
  const [period, setPeriod] = useState<ViewerPeriod>("today");
  const [view, setView] = useState<ViewersView | null>(null);

  const load = useCallback(async (p: ViewerPeriod) => {
    try {
      setView(await tvBroadcastApi.viewers(p, zone()));
    } catch {
      setView(null); // the panel works without its viewer numbers
    }
  }, []);

  useEffect(() => {
    void load(period);
  }, [load, period]);

  if (!view || view.cloud === "unclaimed") return null;
  const r = view.cloud === "ok" ? view.report : undefined;
  if (view.cloud === "ok" && (!r || typeof r.hours !== "object" || r.hours === null)) return null;

  const hours = (v: number) => n(v, { maximumFractionDigits: 1 });
  const other = t("broadcast.analytics.other");
  const country = (code: string) => {
    if (code === "other") return other;
    try {
      return (
        new Intl.DisplayNames([t("broadcast.analytics.locale")], { type: "region" }).of(code) ??
        code
      );
    } catch {
      return code;
    }
  };
  const device = (key: string) =>
    key === "other" ? other : DEVICES.includes(key) ? t(`broadcast.analytics.device.${key}`) : key;
  const empty =
    r !== undefined &&
    r.live_viewers === 0 &&
    r.period_hours === 0 &&
    r.hours.month === 0 &&
    (r.sessions ?? 0) === 0;

  const stat = (label: string, value: string) => (
    <div className="mkt-viewers__stat">
      <dt>{label}</dt>
      <dd>
        <bdi>{value}</bdi>
      </dd>
    </div>
  );

  return (
    <section
      className="mkt-viewers"
      aria-labelledby="broadcast-viewers"
      data-testid="broadcast-viewers"
    >
      <h3 id="broadcast-viewers">{t("broadcast.analytics.title")}</h3>
      {view.cloud === "unreachable" && (
        <p role="status" className="mkt-livetv__hint">
          {t("broadcast.analytics.unreachable")}
        </p>
      )}
      {r && (
        <>
          <div className="mkt-viewers__period">
            <Select
              label={t("broadcast.analytics.period")}
              value={period}
              onChange={(e) => setPeriod(e.target.value as ViewerPeriod)}
              options={PERIODS.map((p) => ({
                value: p,
                label: t(`broadcast.analytics.period.${p}`),
              }))}
            />
          </div>
          {empty && <p data-testid="broadcast-viewers-empty">{t("broadcast.analytics.empty")}</p>}
          <dl className="mkt-viewers__stats">
            {stat(t("broadcast.analytics.live"), n(r.live_viewers))}
            {stat(t("broadcast.analytics.periodHours"), hours(r.period_hours))}
            {r.peak !== undefined && stat(t("broadcast.analytics.peak"), n(r.peak))}
            {r.avg_watch_minutes !== undefined &&
              stat(
                t("broadcast.analytics.avgWatch"),
                t("broadcast.analytics.minutes", {
                  n: n(r.avg_watch_minutes, { maximumFractionDigits: 0 }),
                })
              )}
          </dl>
          <h4>{t("broadcast.analytics.hours")}</h4>
          <dl className="mkt-viewers__stats">
            {stat(t("broadcast.analytics.period.today"), hours(r.hours.today))}
            {stat(t("broadcast.analytics.period.7d"), hours(r.hours.week))}
            {stat(
              t("broadcast.analytics.period.month"),
              r.hours.allowance
                ? t("broadcast.analytics.ofAllowance", {
                    used: hours(r.hours.month),
                    allowance: n(r.hours.allowance),
                  })
                : hours(r.hours.month)
            )}
          </dl>
          {r.hours.allowance ? (
            <ProgressBar
              label={t("broadcast.analytics.allowance")}
              value={(r.hours.month / r.hours.allowance) * 100}
            />
          ) : null}
          {r.level === "full" ? (
            <div className="mkt-viewers__cols">
              <Counts
                title={t("broadcast.analytics.countries")}
                counts={r.countries}
                label={country}
              />
              <Counts title={t("broadcast.analytics.devices")} counts={r.devices} label={device} />
            </div>
          ) : (
            <p className="mkt-livetv__hint">{t("broadcast.analytics.upgrade")}</p>
          )}
          <p className="mkt-livetv__hint">{t("broadcast.analytics.privacy")}</p>
          <p className="mkt-livetv__hint">{t("broadcast.analytics.zone", { zone: r.time_zone })}</p>
        </>
      )}
    </section>
  );
}

function Counts({
  title,
  counts,
  label,
}: {
  title: string;
  counts?: ViewerCount[];
  label: (key: string) => string;
}) {
  const { t, n } = useI18n();
  const list = Array.isArray(counts) ? counts : [];
  return (
    <div className="mkt-viewers__counts">
      <h4>{title}</h4>
      {list.length === 0 ? (
        <p className="mkt-livetv__hint">{t("broadcast.analytics.none")}</p>
      ) : (
        <table>
          <tbody>
            {list.map((c) => (
              <tr key={c.key}>
                <th scope="row">{label(c.key)}</th>
                <td>
                  <bdi>{n(c.value)}</bdi>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
