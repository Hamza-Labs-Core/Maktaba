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

/** The kind a screen shows for a failed call. */
export function userErrorKind(e: unknown): UserErrorKind {
  const kind = classifyError(e).kind;
  if (kind !== "unreachable") return kind;
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
  // the home's sentence for it is English: componentOffText has the words
  if (e instanceof ApiError && e.problem.code === "component_off") return null;
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

/** A word's translation, or "" when the dictionary has none. */
function word(t: T, key: string): string {
  const w = t(key);
  return w && w !== key ? w : "";
}

/**
 * A 409 `component_off` (Epic 51: the part of the home that route needs is turned
 * off) in the app's words, with the part's name: never the home's English detail.
 * The name is the feature's (story 51.09: `components.feature.<feature>`), else the
 * component's title, else its id. An admin is told where to turn it on
 * (`error.kind.offAdmin`); with no name at all it is `error.kind.off`.
 * null for any other error.
 */
export function componentOffText(
  e: unknown,
  t: T,
  opts: { admin?: boolean } = {}
): string | null {
  if (!(e instanceof ApiError) || e.status !== 409 || e.problem.code !== "component_off")
    return null;
  const id = typeof e.problem.component === "string" ? e.problem.component : "";
  const feature = typeof e.problem.feature === "string" ? e.problem.feature : "";
  const name =
    (feature && word(t, `components.feature.${feature}`)) ||
    (id && word(t, `components.${id}.title`)) ||
    id;
  if (!name) return t("error.kind.off");
  if (opts.admin) return t("error.kind.offAdmin", { feature: name });
  return t("error.componentOff", { component: name });
}

/** The sentence a screen shows for a failed call (admin: component_off says where to turn it on). */
export function errorText(e: unknown, t: T, opts: { admin?: boolean } = {}): string {
  return componentOffText(e, t, opts) ?? inputDetail(e) ?? t(`error.kind.${userErrorKind(e)}`);
}
