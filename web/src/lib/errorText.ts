// What a screen says when a call fails: the error's kind in the app's words
// (lib/errorKind), never the server's problem title, a Go error, a stack or a
// path on the home's disk. The one exception is a request the home refused
// for what the person typed (400 / 409 / 422): its detail ("a playlist with
// this name already exists") is the useful answer, so it is shown when it
// reads as a sentence and names no path.
import { ApiError } from "./api";
import { classifyError, type AppErrorKind } from "./errorKind";

/** A kind, plus "offline": the browser itself has no network. */
export type UserErrorKind = AppErrorKind | "offline";

type T = (key: string, vars?: Record<string, string | number>) => string;

/** Whether the browser says it has no network (true where it cannot tell). */
export function isOnline(): boolean {
  return typeof navigator === "undefined" || navigator.onLine !== false;
}

// fetch's own TypeError when no answer came (Chrome, Firefox, Safari wordings);
// any other TypeError is a bug or bad data in the answer, not the network
const NO_ANSWER = /fetch|network|load failed/i;

/** The kind a screen shows for a failed call. */
export function userErrorKind(e: unknown): UserErrorKind {
  const kind = classifyError(e).kind;
  if (kind !== "unreachable") return kind;
  if (e instanceof TypeError && !NO_ANSWER.test(e.message)) return "server";
  return isOnline() ? "unreachable" : "offline";
}

// a request refused for what was sent
const INPUT_STATUSES = new Set([400, 409, 422]);

/**
 * Whether a problem detail can be shown as it is: one short sentence, with no
 * path, markup, braces or stack in it.
 */
export function plainDetail(detail: unknown): detail is string {
  if (typeof detail !== "string") return false;
  const d = detail.trim();
  if (d === "" || d.length > 200 || /[\r\n{}<>]/.test(d)) return false;
  // a path: /Volumes/x/y, C:\x\y, ./x/y
  if (/(^|[\s"'(=:])(\.{0,2}[\\/][^\s\\/]+[\\/]|[A-Za-z]:\\)/.test(d)) return false;
  // a wrapped Go error ("open x: read y: no such file") or a stack frame
  if ((d.match(/: /g) ?? []).length >= 2 || /\bat \S+:\d+/.test(d)) return false;
  return true;
}

/**
 * The detail of a request refused for what the person sent (400 / 409 / 422),
 * when it can be shown as it is; null otherwise. With `refusals`, a 403's
 * reason too, for a screen where the home's 403s are written for people
 * (pairing: "a sign-in through Maktaba Cloud cannot sign in a device").
 */
export function inputDetail(e: unknown, opts: { refusals?: boolean } = {}): string | null {
  if (
    e instanceof ApiError &&
    (INPUT_STATUSES.has(e.status) || (opts.refusals === true && e.status === 403)) &&
    ["server", "noAccess"].includes(classifyError(e).kind) &&
    plainDetail(e.problem.detail)
  ) {
    return e.problem.detail.trim();
  }
  return null;
}

/** The sentence a screen shows for a failed call. */
export function errorText(e: unknown, t: T): string {
  return inputDetail(e) ?? t(`error.kind.${userErrorKind(e)}`);
}
