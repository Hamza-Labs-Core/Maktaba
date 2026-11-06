// Errors (Epic 53 stories 53.10 and 53.11): the home's error groups, what keeps failing.
// The list: title, component, count (24 h and in all), a 14-day sparkline, users, first and
// last seen and the status; filtered by status (new and seen by default) and component,
// sorted by last seen, count or users. A group's page: its hours, its recent lines (each
// opens Follow a request in Logs), its last version, and Mark seen, Resolve, Mute (24 h,
// 7 days, until resolved). lib/logIndex.ts has the routes; each change is audited there.
import { useCallback, useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { Card } from "@ds/components/Card/Card";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Select } from "@ds/components/Select/Select";
import { useI18n } from "../../lib/i18n";
import { ApiError } from "../../lib/api";
import { AdminGate } from "../../components/AdminGate";
import { TitleRow } from "../../help/HelpLink";
import {
  getErrorGroup,
  listErrors,
  logsPageLink,
  setErrorStatus,
  type ErrorGroup,
  type ErrorGroupDetail,
  type ErrorsQuery,
} from "../../lib/logIndex";
import { IndexRow } from "./LogSearch";
import { ERRORS_CHANGED_EVENT } from "../../lib/newErrors";

const STATUSES = ["new,seen", "new", "seen", "resolved", "muted"] as const;

function statusTone(s: string): "neutral" | "accent" | "warn" | "error" {
  return s === "new" ? "error" : s === "seen" ? "warn" : s === "resolved" ? "accent" : "neutral";
}

/** A row of tiny bars, one a day, oldest first (the list's sparkline). */
function Spark({ days }: { days?: number[] }) {
  if (!days || days.length === 0) return null;
  const max = Math.max(1, ...days);
  return (
    <span className="mkt-errors__spark" aria-hidden="true">
      {days.map((n, i) => (
        <span
          key={i}
          className="mkt-errors__bar"
          style={{ height: `${Math.max(2, (n / max) * 100)}%` }}
        />
      ))}
    </span>
  );
}

export function AdminErrors() {
  return (
    <AdminGate>
      <ErrorsList />
    </AdminGate>
  );
}

function ErrorsList() {
  const { t, n, formatDate } = useI18n();
  const [f, setF] = useState<ErrorsQuery>(() => {
    const p = new URLSearchParams(typeof window === "undefined" ? "" : window.location.search);
    return {
      status: p.get("status") || "new,seen",
      component: p.get("component") || undefined,
      sort: (p.get("sort") as ErrorsQuery["sort"]) || "last_seen",
    };
  });
  const [groups, setGroups] = useState<ErrorGroup[] | null>(null);
  const [down, setDown] = useState(false);

  useEffect(() => {
    let alive = true;
    listErrors(f)
      .then((g) => {
        if (!alive) return;
        setGroups(g);
        setDown(false);
      })
      .catch((e: unknown) => {
        if (!alive) return;
        setGroups([]);
        setDown(e instanceof ApiError && e.status === 503);
      });
    return () => {
      alive = false;
    };
  }, [f]);

  return (
    <section className="mkt-errors">
      <header>
        <TitleRow>
          <h1>{t("errors.title")}</h1>
        </TitleRow>
        <span className="mkt-muted">{t("errors.subtitle")}</span>
      </header>
      <div className="mkt-logs__toolbar">
        <label className="mkt-logs__filter">
          <span className="mkt-muted">{t("errors.filter.status")}</span>
          <Select
            value={f.status}
            onChange={(e) => setF((p) => ({ ...p, status: e.target.value }))}
          >
            {STATUSES.map((s) => (
              <option key={s} value={s}>
                {t(`errors.statusFilter.${s.replace(",", "_")}`)}
              </option>
            ))}
          </Select>
        </label>
        <label className="mkt-logs__filter">
          <span className="mkt-muted">{t("errors.filter.sort")}</span>
          <Select
            value={f.sort}
            onChange={(e) => setF((p) => ({ ...p, sort: e.target.value as ErrorsQuery["sort"] }))}
          >
            {(["last_seen", "count", "users"] as const).map((s) => (
              <option key={s} value={s}>
                {t(`errors.sort.${s}`)}
              </option>
            ))}
          </Select>
        </label>
        {f.component && (
          <span className="mkt-muted" data-testid="errors-component">
            {t("logs.filter.component")}: <bdi>{f.component}</bdi>{" "}
            <Button
              variant="secondary"
              size="sm"
              onClick={() => setF((p) => ({ ...p, component: undefined }))}
            >
              {t("logs.window.all")}
            </Button>
          </span>
        )}
      </div>
      {down && (
        <p className="mkt-logs__notice" role="status">
          {t("errors.indexDown")}
        </p>
      )}
      {groups && groups.length === 0 && !down && <p className="mkt-muted">{t("errors.empty")}</p>}
      {groups && groups.length > 0 && (
        <Card>
          <table className="mkt-errors__table">
            <thead>
              <tr>
                <th>{t("errors.col.title")}</th>
                <th>{t("errors.col.component")}</th>
                <th>{t("errors.col.count")}</th>
                <th>{t("errors.col.days")}</th>
                <th>{t("errors.col.users")}</th>
                <th>{t("errors.col.lastSeen")}</th>
                <th>{t("errors.col.status")}</th>
              </tr>
            </thead>
            <tbody>
              {groups.map((g) => (
                <tr key={g.fp} data-testid="error-group">
                  <td>
                    <Link to={`/admin/errors/${g.fp}`}>
                      <bdi dir="ltr">{g.title}</bdi>
                    </Link>
                    {g.came_back_in && (
                      <Badge tone="error" className="mkt-badge--sm">
                        {t("errors.cameBack", { version: g.came_back_in })}
                      </Badge>
                    )}
                  </td>
                  <td>{g.component ?? g.service}</td>
                  <td>{t("errors.counts", { day: n(g.count_24h), all: n(g.count) })}</td>
                  <td>
                    <Spark days={g.days} />
                  </td>
                  <td>{n(g.users)}</td>
                  <td>{formatDate(g.last_seen)}</td>
                  <td>
                    <Badge tone={statusTone(g.status)} className="mkt-badge--sm">
                      {t(`errors.status.${g.status}`)}
                    </Badge>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>
      )}
    </section>
  );
}

export function AdminErrorGroup() {
  return (
    <AdminGate>
      <GroupPage />
    </AdminGate>
  );
}

function GroupPage() {
  const { fp = "" } = useParams();
  const { t, n, formatDate } = useI18n();
  const nav = useNavigate();
  const [d, setD] = useState<ErrorGroupDetail | null>(null);
  const [err, setErr] = useState(false);
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => {
    getErrorGroup(fp)
      .then((r) => {
        setD(r);
        setErr(false);
      })
      .catch(() => setErr(true));
  }, [fp]);
  useEffect(load, [load]);

  const act = (body: Parameters<typeof setErrorStatus>[1]) => {
    setBusy(true);
    setErrorStatus(fp, body)
      .then(() => {
        window.dispatchEvent(new Event(ERRORS_CHANGED_EVENT));
        load();
      })
      .catch(() => setErr(true))
      .finally(() => setBusy(false));
  };
  const muteFor = (hours: number) =>
    act({ status: "muted", muted_until: new Date(Date.now() + hours * 3600_000).toISOString() });

  if (err && !d) return <p className="mkt-muted">{t("errors.loadError")}</p>;
  if (!d) return null;
  const g = d.group;
  const hours = Object.entries(d.hours ?? {}).sort(([a], [b]) => a.localeCompare(b));
  const max = Math.max(1, ...hours.map(([, v]) => v));
  const samples = d.samples?.lines ?? [];
  return (
    <section className="mkt-errors" data-testid="error-group-page">
      <header>
        <Button variant="secondary" size="sm" onClick={() => nav("/admin/errors")}>
          {t("logs.back")}
        </Button>
        <h1>
          <bdi dir="ltr">{g.title}</bdi>
        </h1>
        <p>
          <Badge tone={statusTone(g.status)}>{t(`errors.status.${g.status}`)}</Badge>{" "}
          {g.came_back_in && t("errors.cameBack", { version: g.came_back_in })}
        </p>
        <dl className="mkt-errors__facts">
          <dt>{t("errors.col.component")}</dt>
          <dd>{g.component ?? g.service}</dd>
          <dt>{t("errors.col.count")}</dt>
          <dd>{t("errors.counts", { day: n(g.count_24h), all: n(g.count) })}</dd>
          <dt>{t("errors.col.users")}</dt>
          <dd>{n(g.users)}</dd>
          <dt>{t("errors.firstSeen")}</dt>
          <dd>{formatDate(g.first_seen)}</dd>
          <dt>{t("errors.col.lastSeen")}</dt>
          <dd>{formatDate(g.last_seen)}</dd>
          {g.last_version && (
            <>
              <dt>{t("errors.version")}</dt>
              <dd>
                <bdi dir="ltr">{g.last_version}</bdi>
              </dd>
            </>
          )}
        </dl>
      </header>
      <div className="mkt-errors__actions">
        <Button disabled={busy || g.status === "seen"} onClick={() => act({ status: "seen" })}>
          {t("errors.markSeen")}
        </Button>
        <Button
          disabled={busy || g.status === "resolved"}
          onClick={() => act({ status: "resolved", version: g.last_version ?? "" })}
        >
          {t("errors.resolve")}
        </Button>
        <Button variant="secondary" disabled={busy} onClick={() => muteFor(24)}>
          {t("errors.mute24h")}
        </Button>
        <Button variant="secondary" disabled={busy} onClick={() => muteFor(24 * 7)}>
          {t("errors.mute7d")}
        </Button>
        <Button variant="secondary" disabled={busy} onClick={() => act({ status: "muted" })}>
          {t("errors.muteUntilResolved")}
        </Button>
      </div>
      {hours.length > 0 && (
        <Card className="mkt-errors__chart" aria-label={t("errors.chart")}>
          <div className="mkt-errors__spark mkt-errors__spark--big">
            {hours.map(([h, v]) => (
              <span
                key={h}
                className="mkt-errors__bar"
                title={`${formatDate(h)}: ${n(v)}`}
                style={{ height: `${Math.max(2, (v / max) * 100)}%` }}
              />
            ))}
          </div>
        </Card>
      )}
      <h2>{t("errors.samples")}</h2>
      <Card className="mkt-logs__viewport">
        <ul className="mkt-logs__list">
          {samples.map((l, i) => (
            <IndexRow
              key={d.samples?.ids[i] ?? i}
              line={l}
              users={{}}
              onFollow={(rid) => nav(logsPageLink({ request: rid }))}
            />
          ))}
        </ul>
      </Card>
    </section>
  );
}
