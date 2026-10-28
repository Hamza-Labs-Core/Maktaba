// What the Updates page and About say about updates, derived once from GET
// /api/cluster/updates so both say the same thing in the same words: each machine's
// status, its last update that completed (never a rolled-back, failed, cancelled or
// superseded try), and what happens next, with a time that has its units.
import {
  RUN_ENDED,
  compareVersions,
  validVersion,
  type CatalogRelease,
  type UpdateAttempt,
  type UpdateMachine,
  type UpdatesOverview,
} from "./updatesApi";

/** A machine's or the home's update status, in the words both screens use. */
export type UpdateStatusWord =
  | "up_to_date"
  | "update_available"
  | "updating"
  | "behind"
  | "offline";

/** A message key and its values (the caller's t() renders it). */
export interface Words {
  key: string;
  vars: Record<string, string | number>;
}

type T = (key: string, vars?: Record<string, string | number>) => string;

/** The outcomes that are an update that completed. */
const INSTALLED = "installed";

/** Whether a run is under way. */
export function runActive(ov: Pick<UpdatesOverview, "run">): boolean {
  return Boolean(ov.run && !RUN_ENDED.has(ov.run.state));
}

/** A machine's last update that completed: the server's (last_update), else its newest
 * installed attempt. A rolled-back, failed, put-back or cancelled attempt never is. */
export function lastUpdateOf(
  m: Pick<UpdateMachine, "attempts" | "last_update">
): UpdateAttempt | null {
  if (m.last_update && m.last_update.outcome === INSTALLED) return m.last_update;
  let best: UpdateAttempt | null = null;
  for (const a of m.attempts ?? []) {
    if (a.outcome !== INSTALLED || !a.to) continue;
    if (!best || Date.parse(a.at) > Date.parse(best.at)) best = a;
  }
  return best;
}

/** How an update that completed came: a run of the cluster ("run"), the desktop app's own
 * updater ("app"), or a release's first start outside any run: installed by hand
 * ("server"). A first start during a run, or of a release a run brought, is the run's. */
export function lastUpdateHow(
  a: UpdateAttempt,
  m: Pick<UpdateMachine, "attempts">,
  ov: Pick<UpdatesOverview, "run">
): "run" | "app" | "server" {
  if (a.source === "run" || a.source === "app") return a.source;
  const same = (v?: string) => !!v && validVersion(v) && compareVersions(v, a.to) === 0;
  if (same(ov.run?.version)) return "run";
  const byRun = (x: UpdateAttempt) =>
    x.source === "run" && same(x.to) && (x.outcome === INSTALLED || x.outcome === "running");
  if ((m.attempts ?? []).some(byRun)) return "run";
  return "server";
}

/** The home's row (the overview's first machine), or one made from the overview. */
export function homeOf(ov: UpdatesOverview): UpdateMachine {
  const home = (ov.machines ?? []).find((m) => m.role === "home");
  return (
    home ?? {
      id: "home",
      name: "home",
      role: "home",
      version: ov.current_version,
      state: runActive(ov) ? "updating" : ov.plan?.target ? "behind" : "up_to_date",
    }
  );
}

/** The release the cluster runs: the watermark, or the home's when newer. */
export function clusterRelease(ov: Pick<UpdatesOverview, "watermark" | "current_version">): string {
  const wm = ov.watermark ?? "";
  const cur = ov.current_version ?? "";
  if (!wm) return cur;
  if (cur && validVersion(cur) && validVersion(wm) && compareVersions(cur, wm) > 0) return cur;
  return wm;
}

/** A machine's status word: updating during its part in a run, behind the cluster's
 * release, offline, else up to date. The home: an available release is "update available". */
export function machineStatus(m: UpdateMachine, ov: UpdatesOverview): UpdateStatusWord {
  if (m.role === "home") {
    if (runActive(ov)) return "updating";
    return ov.plan?.target ? "update_available" : "up_to_date";
  }
  if (m.state === "offline" || m.state === "worker_only") return "offline";
  if (m.target || (runActive(ov) && !["up_to_date", "behind", "ahead_of_home"].includes(m.state)))
    return "updating";
  const rel = clusterRelease(ov);
  if (
    m.state === "behind" ||
    (m.version && rel && validVersion(m.version) && compareVersions(m.version, rel) < 0)
  )
    return "behind";
  return "up_to_date";
}

/** The tone of a status word's badge. */
export function statusTone(s: UpdateStatusWord): "success" | "accent" | "warn" | "neutral" {
  switch (s) {
    case "up_to_date":
      return "success";
    case "updating":
    case "update_available":
      return "accent";
    case "behind":
      return "warn";
    default:
      return "neutral";
  }
}

/** A span of time with its units ("2 hours 15 minutes", "45 minutes", "3 days"), in the
 * reader's language. Under a minute: "less than a minute" (updates.span.lessThanMinute). */
export function spanText(ms: number, locale: string, t: T): string {
  const mins = Math.max(0, Math.round(ms / 60000));
  if (mins < 1) return t("updates.span.lessThanMinute");
  const lang = locale === "ar" ? "ar-EG" : "en";
  const unit = (n: number, u: "day" | "hour" | "minute") =>
    new Intl.NumberFormat(lang, { style: "unit", unit: u, unitDisplay: "long" }).format(n);
  const days = Math.floor(mins / 1440);
  const hours = Math.floor((mins % 1440) / 60);
  const rest = mins % 60;
  if (days > 0)
    return hours > 0 ? `${unit(days, "day")} ${unit(hours, "hour")}` : unit(days, "day");
  if (hours > 0)
    return rest > 0 ? `${unit(hours, "hour")} ${unit(rest, "minute")}` : unit(hours, "hour");
  return unit(rest, "minute");
}

/** "in 2 hours 15 minutes" (or "now" when it is due). */
export function inText(iso: string, now: number, locale: string, t: T): string {
  const ms = Date.parse(iso) - now;
  if (!Number.isFinite(ms)) return "";
  if (ms <= 30_000) return t("updates.span.now");
  return t("updates.span.in", { span: spanText(ms, locale, t) });
}

/** A day, as the reader says it: today, tomorrow, else its weekday and date. */
export function dayText(iso: string, now: number, locale: string, t: T): string {
  const d = new Date(iso);
  const n = new Date(now);
  const day = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const diff = Math.round((day(d) - day(n)) / 86_400_000);
  if (diff === 0) return t("updates.day.today");
  if (diff === 1) return t("updates.day.tomorrow");
  return d.toLocaleDateString(locale === "ar" ? "ar-EG" : "en", {
    weekday: "long",
    month: "short",
    day: "numeric",
  });
}

/** The quiet window's two ends ("03:00", "05:00"), or null without a valid one. */
export function windowEnds(w: string | undefined): [string, string] | null {
  const m = /^\s*(\d{1,2}:\d{2})\s*-\s*(\d{1,2}:\d{2})\s*$/.exec(w ?? "");
  return m ? [m[1]!.padStart(5, "0"), m[2]!.padStart(5, "0")] : null;
}

/** What happens next to the home: an update under way, a release that waits for approval,
 * one that installs at a time (its day, the window and how long until then), one that
 * waits for a reason, or nothing: up to date. */
export function homeNext(ov: UpdatesOverview, now: number, locale: string, t: T): Words {
  if (runActive(ov) && ov.run)
    return { key: "updates.next.updating", vars: { version: ov.run.version } };
  const plan = ov.plan ?? ({} as UpdatesOverview["plan"]);
  const target = plan.target;
  if (!target) return { key: "updates.next.upToDate", vars: {} };
  const version = target.version;
  if (plan.held) return { key: "updates.plan.held", vars: { version } };
  if (plan.next_run_at) {
    const ends = windowEnds(ov.policy?.window);
    const vars = {
      version,
      day: dayText(plan.next_run_at, now, locale, t),
      in: inText(plan.next_run_at, now, locale, t),
    };
    return ends
      ? { key: "updates.next.inWindow", vars: { ...vars, from: ends[0], to: ends[1] } }
      : { key: "updates.next.at", vars: { ...vars, at: timeOf(plan.next_run_at, locale) } };
  }
  const waiting = plan.waiting ?? "auto_install_off";
  return { key: `updates.plan.waiting.${waiting}`, vars: { version } };
}

function timeOf(iso: string, locale: string): string {
  return new Date(iso).toLocaleTimeString(locale === "ar" ? "ar-EG" : "en", {
    hour: "2-digit",
    minute: "2-digit",
  });
}

/** What happens next to a joined machine behind the cluster's release (the server's
 * catch-up decision, `next`), null when nothing is to happen (up to date, or a run brings
 * it now). */
export function machineNext(
  m: UpdateMachine,
  ov: UpdatesOverview,
  now: number,
  locale: string,
  t: T
): Words | null {
  if (m.role === "home") return null;
  const status = machineStatus(m, ov);
  if (status === "updating" && m.target)
    return { key: "updates.next.machine.updating", vars: { version: m.target } };
  if (status !== "behind" && status !== "offline") return null;
  const version = m.next?.version || clusterRelease(ov);
  if (
    !version ||
    !m.version ||
    !validVersion(m.version) ||
    compareVersions(m.version, version) >= 0
  )
    return null;
  const reason = m.next?.reason ?? (status === "offline" ? "offline" : "unknown");
  const at = m.next?.at;
  if (reason === "catch_up_window" && at) {
    const ends = windowEnds(ov.policy?.window);
    const vars = {
      version,
      day: dayText(at, now, locale, t),
      in: inText(at, now, locale, t),
      from: ends?.[0] ?? timeOf(at, locale),
      to: ends?.[1] ?? "",
    };
    return {
      key: ends ? "updates.next.inWindow" : "updates.next.at",
      vars: { ...vars, at: timeOf(at, locale) },
    };
  }
  if (reason === "catch_up_retry" && at)
    return {
      key: "updates.next.machine.catch_up_retry",
      vars: { version, in: inText(at, now, locale, t) },
    };
  return { key: `updates.next.machine.${reason}`, vars: { version } };
}

/** Whether a release changes the library database (its schema is newer than the one the
 * home runs): only then does the update back the database up first. Unknown: false. */
export function releaseMigrates(target: CatalogRelease | undefined, ov: UpdatesOverview): boolean {
  const to = target?.manifest?.schema_rev;
  if (!to) return false;
  const cur = ov.catalog?.find(
    (r) => validVersion(r.version) && compareVersions(r.version, ov.current_version) === 0
  )?.manifest?.schema_rev;
  return cur ? to > cur : false;
}
/**
 * The release page link, only for http(s) URLs: the value comes from the
 * server, and a `javascript:` or `data:` URL in an href would run when clicked.
 */
export function safeReleaseUrl(raw: unknown): string | null {
  if (typeof raw !== "string" || raw.trim() === "") return null;
  try {
    const u = new URL(raw);
    return u.protocol === "http:" || u.protocol === "https:" ? u.href : null;
  } catch {
    return null; // relative or malformed
  }
}
