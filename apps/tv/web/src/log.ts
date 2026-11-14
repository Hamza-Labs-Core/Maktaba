// Story 58.09 — the launcher's log: the last 500 lines of its own log and of the bridge
// traffic, kept in memory. Help → Send a report on /tv/ asks for them over the bridge
// (`maktaba:logs`), and CI keeps them in each run's report (window.__maktabaLauncherLog).
//
// Every line is redacted before it is kept: a media URL's `sig`, a cloud code's grant, a
// device code, a poll secret or a token never reaches the buffer.

export const LOG_LINES = 500;

export type LogKind = "launcher" | "bridge-in" | "bridge-out" | "player" | "error";

export interface LogLine {
  at: string;
  kind: LogKind;
  text: string;
}

const SECRET_PARAMS = [
  "sig",
  "grant",
  "device_code",
  "poll_secret",
  "token",
  "access_token",
  "refresh_token",
  "code",
];

const PARAM_RE = new RegExp(`([?&#](?:${SECRET_PARAMS.join("|")})=)[^&#\\s"']*`, "gi");
const FIELD_RE = new RegExp(
  `("(?:${SECRET_PARAMS.filter((p) => p !== "code" && p !== "sig").join("|")})"\\s*:\\s*")[^"]*(")`,
  "gi"
);

/** The text with every secret query value and JSON field replaced. */
export function redact(text: string): string {
  return text.replace(PARAM_RE, "$1[REDACTED]").replace(FIELD_RE, "$1[REDACTED]$2");
}

const lines: LogLine[] = [];

/** Keeps one line (redacted), dropping the oldest past LOG_LINES. */
export function log(kind: LogKind, text: string): void {
  lines.push({ at: new Date().toISOString(), kind, text: redact(text) });
  if (lines.length > LOG_LINES) lines.splice(0, lines.length - LOG_LINES);
  if (kind === "error") console.error(`[launcher] ${redact(text)}`);
}

/** A copy of the kept lines, oldest first. */
export function logLines(): LogLine[] {
  return lines.slice();
}

/** The kept lines as text, one per line. */
export function logText(): string {
  return lines.map((l) => `${l.at} ${l.kind} ${l.text}`).join("\n");
}

/** Empties the buffer (tests). */
export function clearLog(): void {
  lines.length = 0;
}

declare global {
  interface Window {
    __maktabaLauncherLog?: () => string;
  }
}

if (typeof window !== "undefined") window.__maktabaLauncherLog = logText;
