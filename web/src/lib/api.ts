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

  if (res.status === 204) {
    return undefined as T;
  }

  const text = await res.text();
  const ct = res.headers.get("Content-Type") ?? "";

  if (!res.ok) {
    if (ct.includes("problem+json") || ct.includes("json")) {
      try {
        const err = new ApiError(JSON.parse(text) as Problem);
        logApiError(method, path, err.status, err.message);
        throw err;
      } catch (e) {
        if (e instanceof ApiError) throw e;
        // fall through to generic
      }
    }
    const err = new ApiError({
      type: "about:blank",
      title: res.statusText || "request failed",
      status: res.status,
      detail: text.slice(0, 256),
    });
    logApiError(method, path, err.status, err.message);
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
  if (!res.ok) {
    const detail = (await res.text()).slice(0, 256);
    logApiError("GET", path, res.status, detail);
    throw new ApiError({
      type: "about:blank",
      title: res.statusText || "download failed",
      status: res.status,
      detail,
    });
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

export const api = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, body?: unknown) => request<T>(path, { method: "POST", body }),
  patch: <T>(path: string, body?: unknown) => request<T>(path, { method: "PATCH", body }),
  put: <T>(path: string, body?: unknown) => request<T>(path, { method: "PUT", body }),
  delete: <T>(path: string) => request<T>(path, { method: "DELETE" }),
};
