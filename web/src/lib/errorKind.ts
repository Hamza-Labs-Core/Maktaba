// What went wrong, in the app's words.
//
// Every Maktaba client sorts a failure into the same few kinds (the shared
// error spec): the screen shows the kind's sentence and its actions, never the
// server's problem title or detail (those can name paths on the home's disk:
// "open /Volumes/…/index.m3u8: no such file or directory"). The status, the
// problem type's slug, title and detail stay on the classified error for the
// screen's Details disclosure and the logs only.
import { ApiError, PROBLEM_VIDEO_UNAVAILABLE_OFFLINE } from "./api";

export type AppErrorKind =
  | "notFound"
  | "notReady"
  | "signedOut"
  | "noAccess"
  | "unreachable"
  | "busy"
  | "server";

export interface ClassifiedError {
  kind: AppErrorKind;
  /** HTTP status (0: none, a transport failure). */
  status: number;
  /** The problem type's slug (`rendition-not-found`), "" when it has none. */
  slug: string;
  title: string;
  detail: string;
  /** busy only: the Retry-After the server sent, in seconds. */
  retryAfter?: number;
}

/**
 * The slug after `/problems/` in a problem type (the domain varies:
 * maktaba.invalid, maktaba.dev), or "" (about:blank, no type).
 */
export function problemSlug(type: string | undefined): string {
  if (!type) return "";
  const i = type.lastIndexOf("/problems/");
  return i < 0 ? "" : type.slice(i + "/problems/".length).replace(/[/?#].*$/, "");
}

/** The streaming server answers these while a transcode is still starting. */
const NOT_READY_SLUGS = new Set(["manifest-not-found", "rendition-not-found"]);
const UNREACHABLE_SLUGS = new Set([
  "home-unreachable",
  problemSlug(PROBLEM_VIDEO_UNAVAILABLE_OFFLINE),
]);

/** The kind of an HTTP answer: its status, problem slug and machine code. */
export function kindOfStatus(status: number, slug = "", code = ""): AppErrorKind {
  if (UNREACHABLE_SLUGS.has(slug)) return "unreachable";
  if (status === 503 && code === "home_unreachable") return "unreachable";
  if (status === 504 && code === "home_timeout") return "unreachable";
  if (status === 401) return "signedOut";
  if (status === 403) return "noAccess";
  if (status === 404) return NOT_READY_SLUGS.has(slug) ? "notReady" : "notFound";
  if (status === 410) return "notFound";
  if (status === 429 || status === 503) return "busy";
  if (status === 0) return "unreachable";
  return "server";
}

function text(v: unknown): string {
  return typeof v === "string" ? v : "";
}

/** Sorts what a call threw into a kind (fetch's TypeError: the home can't be reached). */
export function classifyError(e: unknown): ClassifiedError {
  if (e instanceof ApiError) {
    const slug = problemSlug(e.problem.type);
    const kind = kindOfStatus(e.status, slug, e.problem.code ?? "");
    return {
      kind,
      status: e.status,
      slug,
      // a malformed problem (a title that is not a string) is not shown at all
      title: text(e.problem.title),
      detail: text(e.problem.detail),
      ...(kind === "busy" && e.retryAfter !== undefined ? { retryAfter: e.retryAfter } : {}),
    };
  }
  // fetch rejects with a TypeError when no answer came (offline, DNS, CORS)
  if (e instanceof TypeError) {
    return { kind: "unreachable", status: 0, slug: "", title: "", detail: e.message };
  }
  return {
    kind: "server",
    status: 0,
    slug: "",
    title: "",
    detail: e instanceof Error ? e.message : "",
  };
}

/** A playback failure the player saw (hls.js, native HLS, a direct file). */
export interface PlaybackFault {
  /** hls.js's error details, media_err_<code>, reopen_limit… */
  code: string;
  /** HTTP status of the failing load (0 / absent: none known). */
  httpStatus?: number;
}

/**
 * The kind of a playback failure. A playlist that still 404s after the
 * retries is a stream not ready yet (a transcode starting); a load with no
 * answer at all, or the element's network error, cannot reach the home.
 */
export function classifyPlayback(f: PlaybackFault | null): ClassifiedError {
  const status = f?.httpStatus ?? 0;
  const code = f?.code ?? "";
  let kind: AppErrorKind;
  if (status === 404) kind = "notReady";
  else if (status === 410) kind = "server";
  else if (status > 0) kind = kindOfStatus(status);
  else if (/Load(Error|TimeOut)$/.test(code) || code === "media_err_2") kind = "unreachable";
  else kind = "server";
  return { kind, status, slug: "", title: code, detail: "" };
}

/** The design system's ErrorState category for a kind. */
export function errorStateKind(
  kind: AppErrorKind
): "network" | "server" | "permission" | "not_found" {
  switch (kind) {
    case "notFound":
      return "not_found";
    case "signedOut":
    case "noAccess":
      return "permission";
    case "unreachable":
      return "network";
    default:
      return "server";
  }
}
