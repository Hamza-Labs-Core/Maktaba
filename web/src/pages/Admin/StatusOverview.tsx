// The System health page's answer to "is everything OK, and if not, what is
// wrong and what do I do" (GET /api/admin/system/status): a banner with the
// overall state and the parts that are not OK, then one tile per part in
// five groups (Services, Workers, Dependencies, Integrations, Queues & TV),
// each with its colour, a one-line reason, when it was checked, a link to
// the page that shows more and "Learn more" into the help. Records kept for
// reference (a worker replaced by a newer one) sit under History and never
// count as a failure.
import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { Badge } from "@ds/components/Badge/Badge";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import type { HealthStatus, StatusItem, StatusReport } from "../../lib/systemStatus";
import { useFmt, type Fmt } from "../../components/queue/format";
import { HelpLink } from "../../help/HelpLink";
import { systemHealthHelp } from "../../help/registry";

export const GROUPS = ["services", "workers", "dependencies", "integrations", "queues_tv"] as const;

type T = (key: string, vars?: Record<string, string | number>) => string;

const TONE: Record<HealthStatus, "success" | "warn" | "error"> = {
  ok: "success",
  degraded: "warn",
  down: "error",
};

/** The singletons named by the catalog; per-machine items keep their own name. */
const NAMED = new Set([
  "api",
  "postgres",
  "streaming",
  "pipeline",
  "processes",
  "workers",
  "streaming_hosts",
  "dependencies",
  "plex_livetv",
  "queues",
  "tv_channels",
  "tv_change",
  "tv_slot",
  "tv_tuner",
  "host_access",
]);

function dependencyName(name: string, t: T): string {
  const [kind, what, ...rest] = name.split(":");
  const tail = rest.join(":");
  if (kind === "dependency" && what === "model") return t("health.dep.model", { section: tail });
  if (kind === "dependency" && what === "share") return t("health.dep.share", { root: tail });
  if (kind === "resource" && what === "disk") return t("health.dep.disk");
  if (kind === "resource" && what === "shm") return t("health.dep.shm");
  return name;
}

export function itemName(it: StatusItem, t: T): string {
  if (NAMED.has(it.id)) return t(`health.item.${it.id}`);
  switch (it.kind) {
    case "process": {
      const k = `health.processes.role.${it.name}`;
      const s = t(k);
      return s === k ? it.name : s;
    }
    case "dependency":
      return dependencyName(it.name, t);
    case "tv_slot":
      return t("health.item.tv_slot_named", { slot: String(it.details?.slot ?? it.name) });
  }
  return it.name;
}

/** The item's reason in the reader's language; the server's English when the catalog has none. */
export function itemReason(it: StatusItem, t: T, fmt: Fmt, n: (v: number) => string): string {
  const vars: Record<string, string | number> = {};
  for (const [k, v] of Object.entries(it.params ?? {})) {
    if (typeof v === "number") {
      vars[k] = n(v);
      // age_seconds -> {age}, uptime_seconds -> {uptime}: a duration
      if (k.endsWith("_seconds")) vars[k.slice(0, -"_seconds".length)] = fmt.dur(v);
    } else if (v !== null && v !== undefined) {
      vars[k] = String(v);
    }
  }
  const key = `health.reason.${it.code}`;
  const s = t(key, vars);
  return s === key ? it.reason : s;
}

function StatusWord({ status, t }: { status: HealthStatus; t: T }) {
  const label =
    status === "ok"
      ? t("health.status.up")
      : status === "down"
        ? t("health.status.down")
        : t("health.status.degraded");
  return <Badge tone={TONE[status] ?? "warn"}>{label}</Badge>;
}

function listOf(names: string[], locale: string): string {
  return names.join(locale === "ar" ? "، " : ", ");
}

/** The overall banner: the state, the parts that make it, and their reasons. */
export function StatusBanner({
  report,
  unreachable,
}: {
  report: StatusReport | null;
  unreachable: boolean;
}) {
  const { t, n, locale } = useI18n();
  const fmt = useFmt();
  if (!report) {
    if (!unreachable) return null;
    return (
      <div
        className="mkt-health-banner mkt-health-banner--down"
        role="alert"
        data-testid="health-banner"
      >
        <strong className="mkt-health-banner__title">{t("health.status.down")}</strong>
        <span>{t("health.banner.unreachable")}</span>
      </div>
    );
  }
  const live = report.items.filter((i) => !i.history);
  const culprits =
    report.status === "down"
      ? live.filter((i) => i.critical && i.status === "down")
      : live.filter((i) => i.status !== "ok");
  const names = listOf(
    culprits.map((i) => itemName(i, t)),
    locale
  );
  const title =
    report.status === "ok"
      ? t("health.banner.ok")
      : t(`health.banner.${report.status === "down" ? "down" : "degraded"}`, { items: names });
  return (
    <div
      className={`mkt-health-banner mkt-health-banner--${report.status}`}
      role={report.status === "ok" ? "status" : "alert"}
      data-testid="health-banner"
    >
      <span className="mkt-health-banner__dot" aria-hidden="true" />
      <div className="mkt-health-banner__body">
        <strong className="mkt-health-banner__title">{title}</strong>
        {culprits.length > 0 && (
          <ul className="mkt-health-banner__list">
            {culprits.slice(0, 4).map((i) => (
              <li key={i.id}>
                <strong>{bidiIsolate(itemName(i, t))}</strong>: {itemReason(i, t, fmt, n)}{" "}
                <HelpLink
                  to={systemHealthHelp(i.help || i.kind)}
                  label={t("health.learnMoreAbout", { name: itemName(i, t) })}
                />
              </li>
            ))}
          </ul>
        )}
        <span className="mkt-muted">
          {t("health.banner.checked", { time: fmt.ago(report.checked_at) })}
        </span>
      </div>
    </div>
  );
}

function workerLine(it: StatusItem, t: T, n: (v: number) => string): ReactNode {
  const d = it.details ?? {};
  const role = d.role === "transcription" ? t("health.role.transcription") : t("health.role.main");
  const running = (d.running ?? {}) as Record<string, number>;
  const jobs = Object.entries(running)
    .filter(([, c]) => c > 0)
    .map(([s, c]) => `${s} ${n(c)}`);
  const parts = [role];
  if (typeof d.host === "string" && d.host && d.host !== it.name) parts.push(bidiIsolate(d.host));
  if (!it.history)
    parts.push(jobs.length ? t("health.runningJobs", { list: jobs.join(", ") }) : t("health.idle"));
  return <span className="mkt-muted mkt-health-tile__detail">{parts.join(" · ")}</span>;
}

function Tile({ it }: { it: StatusItem }) {
  const { t, n } = useI18n();
  const fmt = useFmt();
  const name = itemName(it, t);
  const tone = it.history ? "history" : it.status;
  return (
    <li
      className={`mkt-health-tile mkt-health-tile--${tone}`}
      data-testid={`health-item-${it.id}`}
      data-status={it.status}
    >
      <div className="mkt-health-tile__head">
        <span className="mkt-health-tile__dot" aria-hidden="true" />
        <strong className="mkt-health-tile__name">{bidiIsolate(name)}</strong>
        {it.history ? (
          <Badge tone="neutral">{t("health.history")}</Badge>
        ) : (
          <StatusWord status={it.status} t={t} />
        )}
        {it.attention && !it.history && <Badge tone="accent">{t("health.needsYou")}</Badge>}
      </div>
      <p className="mkt-health-tile__reason">{itemReason(it, t, fmt, n)}</p>
      {it.kind === "worker" && workerLine(it, t, n)}
      <div className="mkt-health-tile__foot">
        <span className="mkt-muted">
          {t("health.checkedAgo", { time: fmt.ago(it.last_checked) })}
        </span>
        {it.link && <Link to={it.link}>{t("health.open")}</Link>}
        <HelpLink
          to={systemHealthHelp(it.help || it.kind)}
          label={t("health.learnMoreAbout", { name })}
        />
      </div>
    </li>
  );
}

/** The five groups of tiles. */
export function StatusGrid({ report }: { report: StatusReport }) {
  const { t, n } = useI18n();
  return (
    <div className="mkt-health-groups">
      {GROUPS.map((g) => {
        const items = report.items.filter((i) => i.group === g);
        if (items.length === 0) return null;
        const live = items.filter((i) => !i.history);
        const history = items.filter((i) => i.history);
        return (
          <section key={g} className="mkt-health-group" aria-labelledby={`health-group-${g}`}>
            <h2 id={`health-group-${g}`}>{t(`health.group.${g}`)}</h2>
            <ul className="mkt-health-tiles">
              {live.map((it) => (
                <Tile key={it.id} it={it} />
              ))}
            </ul>
            {history.length > 0 && (
              <details className="mkt-health-history">
                <summary>{t("health.historyCount", { n: n(history.length) })}</summary>
                <ul className="mkt-health-tiles">
                  {history.map((it) => (
                    <Tile key={it.id} it={it} />
                  ))}
                </ul>
              </details>
            )}
          </section>
        );
      })}
    </div>
  );
}
