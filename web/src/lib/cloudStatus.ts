// Maktaba Cloud's status (Epic 50 story 50.11): https://status.maktaba.tv/summary.json, read
// only when the app reaches the home through the cloud (the relay's address, or the cloud's
// player and remote editions). Read at start, every 5 minutes while the page is visible, and
// at once when a request fails as the relay fails. It never blocks: a failed read shows
// nothing. The status strip (53.06) draws its line at priority 6, after the home's own.
import { useEffect, useSyncExternalStore } from "react";
import { IN_CLOUD } from "./basePath";

export const STATUS_SITE = "https://status.maktaba.tv";
/** How often the feed is read while the page is visible. */
export const CLOUD_EVERY_MS = 5 * 60_000;
/** The relay's public names: a home reached at <slug>.relay.maktaba.tv is reached through it. */
export const RELAY_DOMAIN = "relay.maktaba.tv";
/** Maintenance shows from this long before it starts. */
export const MAINTENANCE_AHEAD_MS = 24 * 3600_000;

export type CloudState = "operational" | "degraded" | "outage" | "maintenance";
type Title = { en: string; ar: string };

export interface CloudSummary {
  updated_at: string;
  overall: CloudState;
  components: Partial<Record<"relay" | "api" | "push" | "plugins", CloudState>>;
  incident: { id: string; state: string; components?: string[]; title: Title; url: string } | null;
  maintenance: {
    id?: string;
    starts_at: string;
    ends_at: string;
    components: string[];
    title: Title;
    url?: string;
  }[];
}

/** The app reaches its home through Maktaba Cloud. */
export function throughCloud(hostname: string = location.hostname, inCloud = IN_CLOUD): boolean {
  return inCloud || hostname === RELAY_DOMAIN || hostname.endsWith("." + RELAY_DOMAIN);
}

let current: CloudSummary | null = null;
const listeners = new Set<() => void>();
let inflight: Promise<void> | null = null;

function publish(s: CloudSummary | null) {
  current = s;
  listeners.forEach((l) => l());
}

function valid(d: unknown): d is CloudSummary {
  const s = d as CloudSummary;
  return (
    !!s &&
    typeof s === "object" &&
    !!s.components &&
    typeof s.components === "object" &&
    Array.isArray(s.maintenance ?? [])
  );
}

/**
 * Reads summary.json once (a read under way is shared). The minute is in the address: GitHub
 * Pages lets caches keep a copy for 10 minutes. A failure keeps what was known.
 */
export function refreshCloudStatus(
  fetcher: typeof fetch = fetch,
  now: number = Date.now()
): Promise<void> {
  if (inflight) return inflight;
  inflight = (async () => {
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), 5000);
    try {
      const r = await fetcher(`${STATUS_SITE}/summary.json?t=${Math.floor(now / 60_000)}`, {
        cache: "no-store",
        credentials: "omit",
        signal: ctrl.signal,
      });
      if (!r.ok) return;
      const doc: unknown = await r.json();
      if (valid(doc)) publish({ ...doc, maintenance: doc.maintenance ?? [] });
    } catch {
      // the status page did not answer: nothing new to show
    } finally {
      clearTimeout(timer);
      inflight = null;
    }
  })();
  return inflight;
}

/** A request failed: when it is the relay failing (no answer, a gateway error), read now. */
export function noteCloudRequestFailed(status: number): void {
  if (listeners.size > 0 && (status === 0 || status === 502 || status === 503 || status === 504)) {
    void refreshCloudStatus();
  }
}

/** The feed as last read (tests set it). */
export function setCloudStatus(s: CloudSummary | null): void {
  publish(s);
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

/** The cloud's status, kept fresh while mounted and enabled (null until a read answered). */
export function useCloudStatus(enabled: boolean): CloudSummary | null {
  useEffect(() => {
    if (!enabled) return;
    void refreshCloudStatus();
    const t = window.setInterval(() => {
      if (document.visibilityState !== "hidden") void refreshCloudStatus();
    }, CLOUD_EVERY_MS);
    return () => window.clearInterval(t);
  }, [enabled]);
  const s = useSyncExternalStore(subscribe, () => current);
  return enabled ? s : null;
}

/** The strip's cloud line: an i18n key, its values, and where Details goes. */
export interface CloudLine {
  /** changes with the incident or notice, so a closed line comes back for the next one */
  key: string;
  text: string;
  vars?: Record<string, string>;
  href: string;
}

const BANNERED = ["relay", "api"] as const;

/**
 * The cloud's line (story 50.11 §5), or null: the relay or the API down or slow, or
 * maintenance on either from 24 hours before it starts until it ends. Notifications alone
 * never show. Worst first: down, slow, maintenance.
 */
export function cloudLine(
  s: CloudSummary | null,
  now: number,
  fmt: { date: (d: Date) => string; time: (d: Date) => string }
): CloudLine | null {
  if (!s) return null;
  const c = s.components ?? {};
  const inc = s.incident;
  const incFor = (comp: string) =>
    inc && (!inc.components || inc.components.includes(comp)) ? inc : null;
  const href = (comp: string) => incFor(comp)?.url ?? STATUS_SITE;
  const id = (comp: string, st: string) => `cloud:${incFor(comp)?.id ?? comp + "-" + st}`;
  for (const st of ["outage", "degraded"] as const) {
    for (const comp of BANNERED) {
      if (c[comp] !== st) continue;
      return { key: id(comp, st), text: `banner.${comp}.${st}`, href: href(comp) };
    }
  }
  const m = (s.maintenance ?? [])
    .filter((x) => x.components.some((k) => (BANNERED as readonly string[]).includes(k)))
    .map((x) => ({ ...x, start: Date.parse(x.starts_at), end: Date.parse(x.ends_at) }))
    .filter(
      (x) =>
        !isNaN(x.start) && !isNaN(x.end) && x.start - MAINTENANCE_AHEAD_MS <= now && now < x.end
    )
    .sort((a, b) => a.start - b.start)[0];
  if (m) {
    const start = new Date(m.start);
    return {
      key: `cloud:${m.id ?? m.starts_at}`,
      text: "banner.maintenance",
      vars: { date: fmt.date(start), start: fmt.time(start), end: fmt.time(new Date(m.end)) },
      href: m.url ?? STATUS_SITE,
    };
  }
  return null;
}
