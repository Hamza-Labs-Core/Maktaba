// Every error nothing caught — a throw in an event handler or timer, a promise
// rejection no one handled — leaves a line in the console with a stable prefix
// ("[uncaught]"), so troubleshooting has a trail even when the person did not
// opt in to usage data (telemetry/client.ts reports these too, once they do).
//
// The line holds the error's kind, a short message and the first frames,
// never the value it was thrown with: an ApiError (already logged by lib/api
// with its method and path) is named by its status only.
// Each such line, an API failure and a route change are also kept as Epic 53 log
// lines (story 53.01, shared/log/schema/line.schema.json) in a 2,000-line ring in
// memory, scrubbed with the shared redaction list: what a problem report attaches.
import { ApiError } from "./api";
import { redactAttrs, scrub } from "./redact";

export type LogLevel = "debug" | "info" | "warn" | "error" | "fatal";

/** One 53.01 log line, as the web keeps it. */
export interface LogLine {
  ts: string;
  level: LogLevel;
  service: "web";
  msg: string;
  app_version?: string;
  platform: "web";
  request_id?: string;
  attrs?: Record<string, unknown>;
}

/** How many lines the ring keeps (the newest). */
export const RING_LINES = 2000;
const MAX_ATTRS = 50;
const ring: LogLine[] = [];
let appVersion: string | undefined;

/** The version every line carries (the server's, once known). */
export function setLogAppVersion(v: string | undefined): void {
  appVersion = v || undefined;
}

/** Adds a line to the ring. `msg` is a snake_case event name, never runtime data. */
export function appLog(
  level: LogLevel,
  msg: string,
  attrs?: Record<string, unknown>,
  requestId?: string
): void {
  const line: LogLine = {
    ts: new Date().toISOString(),
    level,
    service: "web",
    msg,
    platform: "web",
  };
  if (appVersion) line.app_version = appVersion;
  if (requestId) line.request_id = requestId;
  if (attrs && Object.keys(attrs).length) {
    line.attrs = redactAttrs(Object.fromEntries(Object.entries(attrs).slice(0, MAX_ATTRS)));
  }
  ring.push(line);
  if (ring.length > RING_LINES) ring.splice(0, ring.length - RING_LINES);
}

/** The ring's lines, oldest first (a copy). */
export function logLines(): LogLine[] {
  return ring.slice();
}

/** The ring as NDJSON, one line per entry. */
export function logNDJSON(): string {
  return ring.map((l) => JSON.stringify(l)).join("\n");
}

/** Empties the ring (tests). */
export function clearLogLines(): void {
  ring.length = 0;
}

/** A route change, by its pattern (`/videos/:id`), never the URL. */
export function logRouteChanged(pattern: string): void {
  appLog("info", "route_changed", { route: pattern });
}

/** A failed API call (lib/api): method, the path without its query, the status. */
export function logApiFailure(
  method: string,
  path: string,
  status: number,
  requestId?: string
): void {
  appLog(
    status >= 500 || status === 0 ? "error" : "warn",
    "api_failed",
    { method, path: routePattern(path), status },
    requestId
  );
}

/** A path with the segments that may name something (ids, codes) taken out. */
export function routePattern(path: string): string {
  return path
    .split("?")[0]
    .split("/")
    .map((seg) => (/^[A-Za-z_.-]{1,32}$/.test(seg) || seg === "" ? seg : ":id"))
    .join("/");
}

const MAX_MESSAGE = 200;
const MAX_FRAMES = 3;

/** One short, safe line for what was thrown. */
export function describeThrown(thrown: unknown): string {
  if (thrown instanceof ApiError) return `ApiError (${thrown.status})`;
  if (thrown instanceof Error) {
    const name = /^[A-Za-z_$][\w$.]{0,63}$/.test(thrown.name) ? thrown.name : "Error";
    const message = thrown.message.replace(/\s+/g, " ").slice(0, MAX_MESSAGE);
    const frames = (thrown.stack ?? "")
      .split("\n")
      .map((l) => l.trim())
      .filter((l) => l.startsWith("at ") || /@\S+:\d+/.test(l))
      .slice(0, MAX_FRAMES);
    return [`${name}: ${message}`, ...frames].join("\n  ");
  }
  if (typeof thrown === "string") return thrown.slice(0, MAX_MESSAGE);
  return thrown === null ? "null" : typeof thrown;
}

let installed: (() => void) | null = null;

/** Starts logging uncaught errors and unhandled rejections; returns the undo. */
export function installErrorLog(): () => void {
  if (installed) return installed;
  const onError = (ev: ErrorEvent) => {
    const what = describeThrown(ev.error ?? ev.message);
    console.error("[uncaught] error", what);
    appLog("error", "uncaught_error", { what: scrub(what) });
  };
  const onRejection = (ev: PromiseRejectionEvent) => {
    const what = describeThrown(ev.reason);
    console.error("[uncaught] promise rejection", what);
    appLog("error", "unhandled_rejection", { what: scrub(what) });
  };
  window.addEventListener("error", onError);
  window.addEventListener("unhandledrejection", onRejection);
  installed = () => {
    window.removeEventListener("error", onError);
    window.removeEventListener("unhandledrejection", onRejection);
    installed = null;
  };
  return installed;
}
