// Story 16.5: the web app's anonymous usage analytics, strictly opt-in. Until
// the user opts in (the prompt, or Settings → Privacy) nothing is collected or
// sent. Once in, this device sends to its own server's sink (story 16.7,
// POST /api/telemetry) under a pseudonym made at opt-in:
//
//   - app.open: the app's version, OS and locale, the library's size as a range
//   - feature.used: how often each part of the app was opened (a count per area)
//   - error.uncaught: an error's message and first five frames, without paths,
//     the server's address or query strings
//
// Never a file name, a transcript, a search query, a user or an address. A
// server whose admin set [telemetry] enabled = "off" stores nothing it gets.
import { api } from "../api";
import { isDesktop } from "../desktop";
import { nativePlatform } from "../native";
import {
  allPseudonyms,
  dropPseudonyms,
  ensurePseudonym,
  pseudonym,
  readConsent,
  retirePseudonym,
  writeConsent,
} from "./consent";
import { TelemetryQueue, type PayloadValue, type TelemetryEvent } from "./queue";
import { firstFrames, librarySizeBucket, routeFeature, sanitizeMessage } from "./sanitize";

/** the event kinds the server allows (story 16.7's allow-list), with their fields */
export const Events = {
  APP_OPEN: { kind: "app.open", fields: ["cold_start", "route", "library_size_bucket"] },
  FEATURE_USED: { kind: "feature.used", fields: ["feature", "count"] },
  ERROR_UNCAUGHT: { kind: "error.uncaught", fields: ["error_message", "stack_first_5"] },
} as const;

export type EventName = keyof typeof Events;

const QUEUE_KEY = "mkt:telemetry:queue";
// feature counts are summed and sent every few minutes, not per page view
const FEATURE_FLUSH_MS = 5 * 60_000;
// a page that throws in a loop sends a few reports, not thousands
const MAX_ERRORS_PER_LAUNCH = 20;

let queue: TelemetryQueue | null = null;
let featureCounts = new Map<string, number>();
let featureTimer: ReturnType<typeof setTimeout> | null = null;
let errorsSent = 0;
let opened = false;
let detach: (() => void) | null = null;

/** enabled reports whether this device opted in. */
export function telemetryEnabled(): boolean {
  return readConsent()?.granted === true;
}

/** decided reports whether the user answered the prompt (either way). */
export function telemetryDecided(): boolean {
  return readConsent() !== null;
}

function appVersion(): string {
  return import.meta.env?.VITE_APP_VERSION ?? "dev";
}

/** the OS family and, when the browser says, its version: never the user agent itself */
export function osInfo(): { os: string; os_version: string } {
  const native = nativePlatform();
  if (native !== "web") return { os: native, os_version: "" };
  const ua = typeof navigator === "undefined" ? "" : navigator.userAgent;
  const pick = (re: RegExp) => (ua.match(re)?.[1] ?? "").replace(/_/g, ".");
  let os = "other";
  let version = "";
  if (/iPhone|iPad/.test(ua)) {
    os = "ios";
    version = pick(/OS (\d+[_.]\d+)/);
  } else if (/Android/.test(ua)) {
    os = "android";
    version = pick(/Android (\d+)/);
  } else if (/Mac OS X/.test(ua)) {
    os = "macos";
  } else if (/Windows/.test(ua)) {
    os = "windows";
  } else if (/Linux|CrOS/.test(ua)) {
    os = "linux";
  }
  return { os: isDesktop() ? `${os}-desktop` : os, os_version: version };
}

function locale(): string {
  return typeof document === "undefined" ? "" : document.documentElement.lang || "";
}

async function send(batch: TelemetryEvent[]): Promise<void> {
  const p = pseudonym();
  if (!p) return; // forgotten while the batch waited
  const { os, os_version } = osInfo();
  await api.post("/api/telemetry", {
    device_pseudonym: p,
    app_version: appVersion(),
    os,
    os_version,
    locale: locale(),
    events: batch,
  });
}

/** track queues an event; a no-op unless this device opted in. */
export function track(name: EventName, payload: Record<string, PayloadValue> = {}): void {
  if (!queue) return;
  const allowed: readonly string[] = Events[name].fields;
  const kept: Record<string, PayloadValue> = {};
  for (const [k, v] of Object.entries(payload)) {
    if (allowed.includes(k)) kept[k] = v;
  }
  queue.enqueue({ event_kind: Events[name].kind, payload: kept, ts: new Date().toISOString() });
}

/** noteRoute counts a visit to the route's area of the app (feature.used). */
export function noteRoute(pathname: string): void {
  if (!queue) return;
  const f = routeFeature(pathname);
  if (!f) return;
  featureCounts.set(f, (featureCounts.get(f) ?? 0) + 1);
  if (featureTimer === null) {
    featureTimer = setTimeout(flushFeatures, FEATURE_FLUSH_MS);
  }
}

function flushFeatures(): void {
  if (featureTimer !== null) clearTimeout(featureTimer);
  featureTimer = null;
  const counts = featureCounts;
  featureCounts = new Map();
  for (const [feature, count] of counts) track("FEATURE_USED", { feature, count });
}

/** reportError queues an uncaught error, sanitized. */
export function reportError(err: unknown): void {
  if (!queue || errorsSent >= MAX_ERRORS_PER_LAUNCH) return;
  errorsSent++;
  const e =
    err instanceof Error ? err : new Error(typeof err === "string" ? err : "non-error thrown");
  track("ERROR_UNCAUGHT", {
    error_message: sanitizeMessage(`${e.name}: ${e.message}`),
    stack_first_5: firstFrames(e.stack),
  });
}

function listen(): () => void {
  const onError = (ev: ErrorEvent) => reportError(ev.error ?? ev.message);
  const onRejection = (ev: PromiseRejectionEvent) => reportError(ev.reason);
  const onHide = () => {
    if (document.visibilityState === "hidden") flushFeatures();
  };
  window.addEventListener("error", onError);
  window.addEventListener("unhandledrejection", onRejection);
  document.addEventListener("visibilitychange", onHide);
  return () => {
    window.removeEventListener("error", onError);
    window.removeEventListener("unhandledrejection", onRejection);
    document.removeEventListener("visibilitychange", onHide);
  };
}

async function libraryBucket(): Promise<string | null> {
  try {
    const libs = await api.get<{ items?: { id: string }[] }>("/api/libraries");
    let total = 0;
    for (const l of libs.items ?? []) {
      const s = await api.get<{ total_videos?: number }>(
        `/api/libraries/${encodeURIComponent(l.id)}/stats`
      );
      total += s.total_videos ?? 0;
    }
    return librarySizeBucket(total);
  } catch {
    return null;
  }
}

/**
 * startTelemetry begins collecting when this device opted in (at boot, and
 * right after an opt-in); otherwise it does nothing. app.open is sent once a
 * launch.
 */
export function startTelemetry(pathname = ""): void {
  if (queue || !telemetryEnabled()) return;
  ensurePseudonym();
  queue = new TelemetryQueue({ send, storageKey: QUEUE_KEY });
  detach = listen();
  if (!opened) {
    opened = true;
    const route = routeFeature(pathname) ?? "";
    void libraryBucket().then((bucket) => {
      const payload: Record<string, PayloadValue> = { cold_start: true };
      if (route) payload.route = route;
      if (bucket) payload.library_size_bucket = bucket;
      track("APP_OPEN", payload);
    });
  }
}

function stopTelemetry(): void {
  queue?.stop();
  queue = null;
  detach?.();
  detach = null;
  if (featureTimer !== null) clearTimeout(featureTimer);
  featureTimer = null;
  featureCounts = new Map();
  try {
    localStorage.removeItem(QUEUE_KEY);
  } catch {
    /* ignored */
  }
}

/** optIn records consent, makes a pseudonym and starts collecting. */
export function optIn(pathname = ""): void {
  writeConsent(true);
  startTelemetry(pathname);
}

/**
 * optOut records the refusal and stops at once: waiting events are dropped and
 * nothing more is queued. The data already sent stays until "Forget my device".
 */
export function optOut(): void {
  writeConsent(false);
  stopTelemetry();
  retirePseudonym();
}

/**
 * forgetDevice deletes everything this device sent (each pseudonym it used) from
 * the server and drops the waiting events. Still opted in, it carries on under a
 * fresh pseudonym. Throws when a delete fails (the pseudonyms not yet deleted are
 * kept, so it can be tried again).
 */
export async function forgetDevice(): Promise<void> {
  const wasOn = queue !== null;
  stopTelemetry();
  const done: string[] = [];
  try {
    for (const p of allPseudonyms()) {
      await api.delete(`/api/telemetry/devices/${encodeURIComponent(p)}`);
      done.push(p);
    }
  } finally {
    dropPseudonyms(done);
    if (wasOn && telemetryEnabled()) startTelemetry();
  }
}

/** hasDeviceData reports whether this device ever sent under a pseudonym it still holds. */
export function hasDeviceData(): boolean {
  return allPseudonyms().length > 0;
}

/** test hook: forget this module's state */
export function resetTelemetryForTests(): void {
  stopTelemetry();
  errorsSent = 0;
  opened = false;
}

/** test hook: the waiting events */
export function queuedForTests(): number {
  return queue?.size ?? 0;
}
