// Thin fetch wrapper that:
//
//   - sends credentials: 'include' so the `mkt_sess` cookie rides
//   - echoes `mkt_csrf` into the `X-Maktaba-CSRF` header on mutating verbs
//     (Story 10.10 double-submit pattern). The header name MUST match the
//     server constant `auth.CSRFHeader` and the CORS allow-list, both of
//     which are `X-Maktaba-CSRF` — a mismatch silently 403s every
//     cookie-authenticated mutation (constant-time compare in csrf.go).
//   - decodes RFC 9457 problem+json responses into a structured ApiError
//
// No global state: every call takes the absolute path. The base URL is
// pulled from `import.meta.env.VITE_API_BASE` so the dev proxy and the
// production build both work without ifdefs.

const BASE = (import.meta.env.VITE_API_BASE ?? "") as string;

/** The URL of an API path for something the browser loads itself (an iframe, a link). */
export const apiUrl = (path: string): string => `${BASE}${path}`;

// Must equal the server's `auth.CSRFHeader` and the CORS allow-list.
const CSRF_HEADER = "X-Maktaba-CSRF";

const MUTATING = new Set(["POST", "PUT", "PATCH", "DELETE"]);

export interface Problem {
  type: string;
  title: string;
  status: number;
  detail?: string;
  instance?: string;
  requestId?: string;
  /** a node's machine-readable reason (`paused`, `home_unreachable`, ...) */
  code?: string;
}

export class ApiError extends Error {
  status: number;
  problem: Problem;
  constructor(problem: Problem) {
    super(problem.title + (problem.detail ? `: ${problem.detail}` : ""));
    this.name = "ApiError";
    this.status = problem.status;
    this.problem = problem;
  }
}

// logApiError surfaces every failed API call to the browser console so
// troubleshooting (and the diagnostics export) has a client-side trail.
// Kept to console.error with a stable "[api]" prefix so it is greppable
// in a captured console log.
function logApiError(method: string, path: string, status: number, detail: string): void {
  console.error(`[api] ${method} ${path} failed (${status}): ${detail}`);
}

// Epic 35 (story 35.10): a node answers 503 {code: "paused"} when its code
// does not fit the home's database, or {code: "home_unreachable"} when it
// cannot reach its home. Every call that sees one tells the app: NodeGuard
// shows the paused page for `paused`, and for `home_unreachable` keeps the
// app readable under a notice. A read the node still serves in a home outage
// carries HOME_STATE_HEADER: unreachable, which tells the app the same. The
// node decides what still works meanwhile (reads, playback on this machine),
// so every request is still sent; one the node cannot serve this person at
// all says {readable: false}, which swaps the app for the paused page too.
export const NODE_UNAVAILABLE_EVENT = "maktaba:node-unavailable";
/** Fired when the home answers again (the notice cleared it). */
export const NODE_AVAILABLE_EVENT = "maktaba:node-available";
export const HOME_STATE_HEADER = "X-Maktaba-Home";
/** A video the node cannot read while the home, which could play it, does not answer. */
export const PROBLEM_VIDEO_UNAVAILABLE_OFFLINE =
  "https://maktaba.dev/problems/video-unavailable-offline";
/** Too little free on the streaming server's disk to prepare (transcode) a video. */
export const PROBLEM_INSUFFICIENT_STORAGE = "https://maktaba.dev/problems/insufficient-storage";

export type NodeUnavailableCode = "paused" | "home_unreachable";

export interface NodeUnavailableDetail {
  code: NodeUnavailableCode;
  reason?: string;
  /** home_unreachable only: false when this machine cannot serve even reads */
  readable?: boolean;
}

/** Whether a node-unavailable answer takes the whole app (the paused page). */
export function blocksApp(d: NodeUnavailableDetail | null): boolean {
  return d?.code === "paused" || (d?.code === "home_unreachable" && d.readable === false);
}

/** The graver of two answers: `paused`, then a node that serves nothing, then the notice. */
export function worseUnavailable(
  prev: NodeUnavailableDetail | null,
  next: NodeUnavailableDetail
): NodeUnavailableDetail {
  const rank = (d: NodeUnavailableDetail | null) =>
    d === null ? 0 : d.code === "paused" ? 3 : blocksApp(d) ? 2 : 1;
  return rank(prev) >= rank(next) && prev !== null ? prev : next;
}

let lastUnavailable: NodeUnavailableDetail | null = null;

/** The last paused / home_unreachable answer seen (for a listener mounted after it). */
export function lastNodeUnavailable(): NodeUnavailableDetail | null {
  return lastUnavailable;
}

/** Forgets the last paused / home_unreachable answer (tests; a page reload does it too). */
export function clearNodeUnavailable(): void {
  lastUnavailable = null;
}

/** The home answers again: forget a home_unreachable and tell the app (a pause stays). */
export function noteHomeBack(): void {
  if (lastUnavailable?.code !== "home_unreachable") return;
  lastUnavailable = null;
  if (typeof window !== "undefined") window.dispatchEvent(new Event(NODE_AVAILABLE_EVENT));
}

/**
 * Fired when a sign-in made on this machine while its home was away meets a
 * change once the home is back (401 {code: "signin_required"}): the home knows
 * nothing of it, so the app asks for a sign-in with the home.
 */
export const SIGNIN_REQUIRED_EVENT = "maktaba:signin-required";

function noteSignInRequired(status: number, problem: unknown): void {
  if (status !== 401 || !problem || typeof problem !== "object") return;
  if ((problem as { code?: unknown }).code !== "signin_required") return;
  if (typeof window !== "undefined") window.dispatchEvent(new Event(SIGNIN_REQUIRED_EVENT));
}

/** Whether this machine cannot reach its home (changes need it). */
export function homeUnreachable(): boolean {
  return lastUnavailable?.code === "home_unreachable";
}

/** Whether a failed call was refused because this machine cannot reach its home. */
export function isHomeUnreachable(e: unknown): boolean {
  return e instanceof ApiError && e.status === 503 && e.problem.code === "home_unreachable";
}

/**
 * Whether a call this machine passed to its home got no answer in time (504
 * home_timeout): the home may or may not have done it.
 */
export function isHomeTimeout(e: unknown): boolean {
  return e instanceof ApiError && e.status === 504 && e.problem.code === "home_timeout";
}

function noteUnavailable(status: number, problem: unknown): void {
  if (status !== 503 || !problem || typeof problem !== "object") return;
  const p = problem as { code?: unknown; reason?: unknown; readable?: unknown };
  if (p.code !== "paused" && p.code !== "home_unreachable") return;
  const detail: NodeUnavailableDetail = { code: p.code };
  if (typeof p.reason === "string" && p.reason !== "") detail.reason = p.reason;
  if (p.code === "home_unreachable" && p.readable === false) detail.readable = false;
  lastUnavailable = worseUnavailable(lastUnavailable, detail);
  if (typeof window !== "undefined") {
    window.dispatchEvent(new CustomEvent(NODE_UNAVAILABLE_EVENT, { detail }));
  }
}

function noteHomeState(headers: Headers | null): void {
  if (headers?.get(HOME_STATE_HEADER) === "unreachable") {
    noteUnavailable(503, { code: "home_unreachable" });
  }
}

// csrfHeaders returns the double-submit header for a hand-rolled fetch
// (e.g. a streaming POST that cannot go through request()).
export function csrfHeaders(): Record<string, string> {
  const tok = csrfToken();
  return tok ? { [CSRF_HEADER]: tok } : {};
}

function csrfToken(): string | null {
  const m = document.cookie.match(/(?:^|;\s*)mkt_csrf=([^;]+)/);
  return m ? decodeURIComponent(m[1]) : null;
}

interface RequestOptions extends Omit<RequestInit, "body"> {
  body?: unknown;
}

export async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const method = (opts.method ?? "GET").toUpperCase();
  const headers = new Headers(opts.headers);
  if (!headers.has("Accept")) {
    headers.set("Accept", "application/json");
  }

  let body: BodyInit | null = null;
  if (opts.body !== undefined && opts.body !== null) {
    if (typeof opts.body === "string" || opts.body instanceof FormData) {
      body = opts.body as BodyInit;
    } else {
      body = JSON.stringify(opts.body);
      if (!headers.has("Content-Type")) {
        headers.set("Content-Type", "application/json");
      }
    }
  }

  if (MUTATING.has(method)) {
    const tok = csrfToken();
    if (tok) {
      headers.set(CSRF_HEADER, tok);
    }
  }

  let res: Response;
  try {
    res = await fetch(`${BASE}${path}`, {
      ...opts,
      method,
      headers,
      body,
      credentials: "include",
    });
  } catch (e) {
    // Network-level failure (offline, DNS, CORS) never reaches the
    // status-based branch below — log it so it isn't swallowed.
    logApiError(method, path, 0, e instanceof Error ? e.message : "network error");
    throw e;
  }

  noteHomeState(res.headers);

  if (res.status === 204) {
    return undefined as T;
  }

  const text = await res.text();
  const ct = res.headers.get("Content-Type") ?? "";

  if (!res.ok) {
    if (ct.includes("problem+json") || ct.includes("json")) {
      try {
        const parsed = JSON.parse(text) as Problem;
        noteUnavailable(res.status, parsed);
        noteSignInRequired(res.status, parsed);
        const err = new ApiError({ ...parsed, status: parsed.status || res.status });
        logApiError(method, path, err.status, err.message);
        throw err;
      } catch (e) {
        if (e instanceof ApiError) throw e;
        // fall through to generic
      }
    }
    // A plain-text body (Go's http.Error) is a useful message; an HTML page
    // (a proxy's 502, a dev server's error page) is not something to show the
    // user, so it only goes to the console log.
    const html = ct.includes("html") || /^\s*</.test(text);
    const err = new ApiError({
      type: "about:blank",
      title: res.statusText || "request failed",
      status: res.status,
      ...(html || !text ? {} : { detail: text.slice(0, 256) }),
    });
    logApiError(method, path, err.status, html ? text.slice(0, 256) : err.message);
    throw err;
  }

  if (ct.includes("json")) {
    return JSON.parse(text) as T;
  }
  return text as unknown as T;
}

// downloadBlob fetches a binary endpoint (e.g. the diagnostics .tar.gz
// bundle) with the session cookie and triggers a browser download. The
// filename is taken from the response's Content-Disposition when
// present, else the fallbackName. Errors are logged like any other API
// call and rethrown so the caller can surface a toast.
export async function downloadBlob(path: string, fallbackName: string): Promise<void> {
  let res: Response;
  try {
    res = await fetch(`${BASE}${path}`, { credentials: "include", headers: { Accept: "*/*" } });
  } catch (e) {
    logApiError("GET", path, 0, e instanceof Error ? e.message : "network error");
    throw e;
  }
  noteHomeState(res.headers);
  if (!res.ok) {
    // decoded like request(): a problem+json answer (a node's paused /
    // home_unreachable among them) keeps its type and tells the app
    const text = await res.text();
    let err: ApiError | null = null;
    if ((res.headers.get("Content-Type") ?? "").includes("json")) {
      try {
        const parsed = JSON.parse(text) as Problem;
        noteUnavailable(res.status, parsed);
        err = new ApiError({ ...parsed, status: parsed.status || res.status });
      } catch {
        // not JSON after all: the plain answer below
      }
    }
    err ??= new ApiError({
      type: "about:blank",
      title: res.statusText || "download failed",
      status: res.status,
      detail: text.slice(0, 256),
    });
    logApiError("GET", path, err.status, err.message);
    throw err;
  }
  const blob = await res.blob();
  const disposition = res.headers.get("Content-Disposition") ?? "";
  const match = disposition.match(/filename="?([^"]+)"?/);
  const name = match ? match[1] : fallbackName;
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

// postBeacon sends a fire-and-forget POST that survives page unload.
//
// We deliberately use `fetch(..., { keepalive: true })` rather than
// `navigator.sendBeacon`: the beacon API cannot set request headers, so
// it can never carry the `X-Maktaba-CSRF` token a cookie-authenticated
// mutation requires (it would 403). keepalive fetch keeps the request
// alive past document teardown AND lets us attach the CSRF header +
// credentials, so the watch-session `stop` actually lands on `pagehide`.
// Body is capped well under the 64 KB keepalive limit.
export function postBeacon(path: string, body: unknown): void {
  const headers = new Headers({ "Content-Type": "application/json" });
  const tok = csrfToken();
  if (tok) headers.set(CSRF_HEADER, tok);
  try {
    void fetch(`${BASE}${path}`, {
      method: "POST",
      headers,
      body: JSON.stringify(body),
      credentials: "include",
      keepalive: true,
    }).catch(() => {});
  } catch {
    // Some engines throw synchronously when keepalive+body exceeds the
    // limit or during teardown — non-fatal, the heartbeat already
    // captured most of the watch time.
  }
}

// deleteBeacon sends a fire-and-forget DELETE that survives page unload
// (keepalive fetch with the CSRF header; navigator.sendBeacon can do neither
// DELETE nor headers). Epic 32.9: a remote TV session ends on page unload.
export function deleteBeacon(path: string): void {
  const headers = new Headers();
  const tok = csrfToken();
  if (tok) headers.set(CSRF_HEADER, tok);
  try {
    void fetch(`${BASE}${path}`, {
      method: "DELETE",
      headers,
      credentials: "include",
      keepalive: true,
    }).catch(() => {});
  } catch {
    // teardown: the server times the session out after 150 s anyway
  }
}

export const api = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, body?: unknown) => request<T>(path, { method: "POST", body }),
  patch: <T>(path: string, body?: unknown) => request<T>(path, { method: "PATCH", body }),
  put: <T>(path: string, body?: unknown) => request<T>(path, { method: "PUT", body }),
  delete: <T>(path: string, body?: unknown) =>
    request<T>(path, body === undefined ? { method: "DELETE" } : { method: "DELETE", body }),
};

/**
 * uploadWithProgress POSTs a multipart form and reports the upload's
 * progress (0..1). fetch cannot report upload progress, so this one call
 * uses XMLHttpRequest — with the same cookie, CSRF header and problem+json
 * error decoding as `request`.
 */
export function uploadWithProgress<T>(
  path: string,
  form: FormData,
  onProgress?: (fraction: number) => void
): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", `${BASE}${path}`);
    xhr.withCredentials = true;
    xhr.setRequestHeader("Accept", "application/json");
    const tok = csrfToken();
    if (tok) xhr.setRequestHeader(CSRF_HEADER, tok);
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && e.total > 0) onProgress?.(e.loaded / e.total);
    };
    xhr.onerror = () => {
      logApiError("POST", path, 0, "network error");
      reject(new Error("network error"));
    };
    xhr.onload = () => {
      const text = xhr.responseText ?? "";
      if (xhr.status >= 200 && xhr.status < 300) {
        onProgress?.(1);
        try {
          resolve((text ? JSON.parse(text) : undefined) as T);
        } catch {
          resolve(text as unknown as T);
        }
        return;
      }
      let problem: Problem;
      try {
        problem = JSON.parse(text) as Problem;
        noteUnavailable(xhr.status, problem);
      } catch {
        problem = {
          type: "about:blank",
          title: xhr.statusText || "upload failed",
          status: xhr.status,
          detail: text.slice(0, 256),
        };
      }
      const err = new ApiError({ ...problem, status: problem.status || xhr.status });
      logApiError("POST", path, err.status, err.message);
      reject(err);
    };
    xhr.send(form);
  });
}
