// Story 16.5: this app's anonymous usage data for Maktaba (its makers, to
// improve it), strictly opt-in and this app's own: the home's admin cannot turn
// it on for anyone, and the admin's switch for the server's own data does not
// cover it. Until this app's user agrees (the ask, or Settings → Privacy),
// nothing is collected or sent. Once in, the app posts its batches to its home
// (POST /api/telemetry/app, with the consent in the body); the home scrubs them
// and forwards them to Maktaba Cloud. The app itself never reaches the cloud.
//
//   - app.open: once a launch
//   - feature.used: how often each part of the app was opened (a count per area)
//   - error.uncaught: an error's kind (TypeError), never its message, and its
//     first frames in the app's own code
//
// The app, its version, platform and kind of device are the X-Maktaba-Client
// header's (Epic 44); the language is the page's. Never a file name, a title, a
// transcript, a search, a user or an address; the id is random per install.
import { api } from "../api";
import { clientHeaderValue } from "../clientHeader";
import {
  allPseudonyms,
  dropPseudonyms,
  ensurePseudonym,
  forgetLegacyConsent,
  pseudonym,
  readConsent,
  retirePseudonym,
  writeConsent,
} from "./consent";
import { TelemetryQueue, type PayloadValue, type TelemetryEvent } from "./queue";
import { firstFrames, routeFeature } from "./sanitize";

/** the event kinds the home allows (story 16.7's allow-list), with their fields */
export const Events = {
  APP_OPEN: { kind: "app.open", fields: ["cold_start"] },
  FEATURE_USED: { kind: "feature.used", fields: ["feature", "count"] },
  ERROR_UNCAUGHT: { kind: "error.uncaught", fields: ["error_message", "stack_first_5"] },
} as const;

export type EventName = keyof typeof Events;

const QUEUE_KEY = "mkt:usage:queue";
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

forgetLegacyConsent();

/** telemetryEnabled reports whether this app's user agreed. */
export function telemetryEnabled(): boolean {
  return readConsent()?.granted === true;
}

/** decided reports whether the user answered the prompt (either way). */
export function telemetryDecided(): boolean {
  return readConsent() !== null;
}

/** the header's parts: `<app>/<version> (<platform>; <device>)` */
export function clientInfo(): {
  app: string;
  app_version: string;
  platform: string;
  form_factor: string;
} {
  const m = clientHeaderValue().match(/^([^/]+)\/(\S+) \(([^;]+); ([^)]+)\)$/);
  if (!m) return { app: "web", app_version: "dev", platform: "web", form_factor: "" };
  return { app: m[1], app_version: m[2], platform: m[3], form_factor: m[4] };
}

/** the OS version, when the browser says it: never the user agent itself */
function osVersion(): string {
  const ua = typeof navigator === "undefined" ? "" : navigator.userAgent;
  const m = ua.match(/OS (\d+[_.]\d+)/) ?? ua.match(/Android (\d+)/);
  return (m?.[1] ?? "").replace(/_/g, ".");
}

function locale(): string {
  return typeof document === "undefined" ? "" : document.documentElement.lang || "";
}

async function send(batch: TelemetryEvent[]): Promise<void> {
  const id = pseudonym();
  if (!id || !telemetryEnabled()) return; // turned off while the batch waited
  await api.post("/api/telemetry/app", {
    consent: true,
    install_id: id,
    ...clientInfo(),
    os_version: osVersion(),
    locale: locale(),
    events: batch,
  });
}

/** track queues an event; a no-op unless this app's user agreed. */
export function track(name: EventName, payload: Record<string, PayloadValue> = {}): void {
  if (!queue) return;
  const allowed: readonly string[] = Events[name].fields;
  const kept: Record<string, PayloadValue> = {};
  for (const [k, v] of Object.entries(payload)) {
    if (allowed.includes(k)) kept[k] = v;
  }
  queue.enqueue({ kind: Events[name].kind, payload: kept, ts: new Date().toISOString() });
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
  // the error's kind only, never its message (which could name a title or a
  // file); the home keeps of the frames only the app's own code
  track("ERROR_UNCAUGHT", {
    error_message: /^[A-Za-z_$][\w$.]{0,63}$/.test(e.name) ? e.name : "Error",
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

/**
 * startTelemetry begins collecting when this app's user agreed (at boot, and
 * right after an opt-in); otherwise it does nothing. app.open is sent once a
 * launch.
 */
export function startTelemetry(): void {
  if (queue || !telemetryEnabled()) return;
  ensurePseudonym();
  queue = new TelemetryQueue({ send, storageKey: QUEUE_KEY });
  detach = listen();
  if (!opened) {
    opened = true;
    track("APP_OPEN", { cold_start: true });
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
export function optIn(): void {
  writeConsent(true);
  startTelemetry();
}

/**
 * optOut records the refusal and stops at once: waiting events are dropped and
 * nothing more is queued. What was already sent stays until "Delete what this
 * app sent".
 */
export function optOut(): void {
  writeConsent(false);
  stopTelemetry();
  retirePseudonym();
}

/**
 * forgetDevice asks for everything this app sent (under each id it used) to be
 * deleted: the home drops what still waits and asks Maktaba Cloud to delete the
 * rest. The waiting events go too. Still opted in, it carries on under a fresh
 * id. Throws when a delete fails (the ids not yet done are kept, so it can be
 * tried again).
 */
export async function forgetDevice(): Promise<void> {
  const wasOn = queue !== null;
  stopTelemetry();
  const done: string[] = [];
  try {
    for (const p of allPseudonyms()) {
      await api.delete(`/api/telemetry/app/${encodeURIComponent(p)}`);
      done.push(p);
    }
  } finally {
    dropPseudonyms(done);
    if (wasOn && telemetryEnabled()) startTelemetry();
  }
}

/** hasDeviceData reports whether this app ever sent under an id it still holds. */
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
