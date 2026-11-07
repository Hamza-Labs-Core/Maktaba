// Crash reports to the home (Epic 53 story 53.14). Off until the person says yes: the
// switch lives on this device (each person, each device), asked once after sign-in and kept
// in Settings → Privacy. When on, an uncaught error or unhandled rejection (at most one per
// fingerprint per page load) is sent to the home as a Sentry envelope with the consent in its
// header (the home refuses one without it), with the app's recent lines, already scrubbed.
// Nothing leaves the device while it is off.
import { apiUrl, csrfHeaders } from "./api";
import { isDesktop } from "./desktop";
import { describeThrown, logAppVersion, logLines } from "./errorLog";
import { scrub } from "./redact";
import { uuidv7 } from "./requestIds";

const KEY = "mkt:crash-reports";
const INSTALL = "mkt:install-id";
export const CONSENT = "crash_reports_to_home";

function read(k: string): string | null {
  try {
    return localStorage.getItem(k);
  } catch {
    return null;
  }
}

function write(k: string, v: string) {
  try {
    localStorage.setItem(k, v);
  } catch {
    // a private window keeps no choice: it stays off
  }
}

/** Whether the person turned crash reports to the home on, on this device. */
export function crashReportsOn(): boolean {
  return read(KEY) === "on";
}

/** Whether the person was asked yet (the one-time question after sign-in). */
export function crashReportsAsked(): boolean {
  return read(KEY) !== null;
}

const listeners = new Set<() => void>();

export function setCrashReports(on: boolean): void {
  write(KEY, on ? "on" : "off");
  listeners.forEach((l) => l());
}

export function onCrashReportsChange(l: () => void): () => void {
  listeners.add(l);
  return () => listeners.delete(l);
}

/** This browser's install id (random, kept on the device): the home's rate limit counts it. */
export function installId(): string {
  let id = read(INSTALL);
  if (!id) {
    id = uuidv7();
    write(INSTALL, id);
  }
  return id;
}

/** A Sentry envelope for one error: the header, the event and the app's lines. */
export function envelope(what: string, stack: string | undefined, appVersion: string): string {
  const frames = (stack ?? "")
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l.startsWith("at ") || l.includes("@"))
    .slice(0, 30)
    .reverse()
    .map((l) => ({
      function: scrub(l.replace(/^at /, "").split(" (")[0]),
      in_app: !l.includes("node_modules"),
    }));
  const [type, ...rest] = what.split(": ");
  const event = JSON.stringify({
    platform: "javascript",
    exception: {
      values: [
        {
          type: rest.length ? type : "Error",
          value: scrub(rest.length ? rest.join(": ") : what),
          stacktrace: { frames },
        },
      ],
    },
  });
  const log = logLines()
    .slice(-500)
    .map((l) => JSON.stringify(l))
    .join("\n")
    .slice(-512 * 1024);
  const header = JSON.stringify({
    event_id: uuidv7().replace(/-/g, ""),
    consent: CONSENT,
    app: "web",
    app_version: appVersion,
    platform: "web",
    install_id: installId(),
  });
  const enc = new TextEncoder();
  return [
    header,
    JSON.stringify({ type: "event", length: enc.encode(event).length }),
    event,
    JSON.stringify({ type: "attachment", length: enc.encode(log).length }),
    log,
    "",
  ].join("\n");
}

const sent = new Set<string>();

/** Sends one error to the home when crash reports are on (once per fingerprint per page load). */
export function reportCrash(thrown: unknown, appVersion = logAppVersion()): Promise<boolean> {
  if (!crashReportsOn()) return Promise.resolve(false);
  const what = describeThrown(thrown);
  const stack = thrown instanceof Error ? thrown.stack : undefined;
  // the message line and the first frame's function, numbers folded (ids, sizes, lines)
  const head = what.split("\n")[0].slice(0, 200);
  const frame = ((stack ?? "").split("\n")[1] ?? "")
    .trim()
    .replace(/\s*\(.*\)$/, "")
    .replace(/@.*$/, "");
  const fp = `${head}|${frame}`.replace(/\d+/g, "0");
  if (sent.has(fp)) return Promise.resolve(false);
  sent.add(fp);
  return fetch(apiUrl("/api/diagnostics/envelope"), {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/x-sentry-envelope", ...csrfHeaders() },
    body: envelope(what, stack, appVersion),
  })
    .then((r) => r.ok)
    .catch(() => false);
}

/** A crash of the desktop shell itself (apps/desktop/src-tauri/src/crashes.rs), kept scrubbed. */
export interface ShellCrash {
  id: string;
  at: number;
  thread: string;
  message: string;
  location: string;
  app_version: string;
  platform: string;
  log: string[];
}

/** The shell's crash as an envelope (app "desktop"): the panic's message and its place. */
export function shellEnvelope(c: ShellCrash): string {
  const event = JSON.stringify({
    platform: "native",
    level: "fatal",
    timestamp: new Date(c.at).toISOString(),
    release: c.app_version,
    exception: {
      values: [
        {
          type: "panic",
          value: scrub(c.message),
          mechanism: { type: "panic", handled: false },
          stacktrace: { frames: [{ function: scrub(c.location || c.thread), in_app: true }] },
        },
      ],
    },
  });
  const log = c.log
    .map(scrub)
    .join("\n")
    .slice(-512 * 1024);
  const header = JSON.stringify({
    event_id: uuidv7().replace(/-/g, ""),
    consent: CONSENT,
    app: "desktop",
    app_version: c.app_version,
    platform: c.platform,
    install_id: installId(),
  });
  const enc = new TextEncoder();
  return [
    header,
    JSON.stringify({ type: "event", length: enc.encode(event).length }),
    event,
    JSON.stringify({ type: "attachment", length: enc.encode(log).length }),
    log,
    "",
  ].join("\n");
}

type Invoke = <T>(cmd: string, args?: Record<string, unknown>) => Promise<T | undefined>;

async function shellInvoke<T>(cmd: string, args?: Record<string, unknown>): Promise<T | undefined> {
  if (!isDesktop()) return undefined;
  try {
    const core = await import("@tauri-apps/api/core");
    return await core.invoke<T>(cmd, args);
  } catch {
    return undefined;
  }
}

/**
 * The desktop shell's own crashes (Epic 53 story 53.14, the desktop row): when crash reports
 * are on here, each one the shell kept is sent to the home, then the shell is told which went
 * (it keeps them, never offering them again). Off: nothing leaves. Answers how many went.
 */
export async function sendShellCrashes(invoke: Invoke = shellInvoke): Promise<number> {
  if (!crashReportsOn()) return 0;
  const crashes = (await invoke<ShellCrash[]>("shell_crashes")) ?? [];
  const sent: string[] = [];
  for (const c of crashes) {
    const ok = await fetch(apiUrl("/api/diagnostics/envelope"), {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/x-sentry-envelope", ...csrfHeaders() },
      body: shellEnvelope(c),
    })
      .then((r) => r.ok)
      .catch(() => false);
    if (ok) sent.push(c.id);
  }
  if (sent.length) await invoke<number>("shell_crashes_sent", { ids: sent });
  return sent.length;
}

/** Starts sending uncaught errors (when on); returns the undo. */
export function installCrashReports(): () => void {
  // the desktop shell's own crashes, at this start and whenever the switch turns on
  void sendShellCrashes();
  const offSwitch = onCrashReportsChange(() => void sendShellCrashes());
  const onError = (ev: ErrorEvent) => void reportCrash(ev.error ?? ev.message);
  const onRejection = (ev: PromiseRejectionEvent) => void reportCrash(ev.reason);
  window.addEventListener("error", onError);
  window.addEventListener("unhandledrejection", onRejection);
  return () => {
    offSwitch();
    window.removeEventListener("error", onError);
    window.removeEventListener("unhandledrejection", onRejection);
  };
}

/** For tests: forget what was sent this page load. */
export function resetCrashReportsForTest(): void {
  sent.clear();
}
