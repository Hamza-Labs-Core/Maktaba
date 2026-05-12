// Thin fetch wrapper that:
//
//   - sends credentials: 'include' so the `mkt_sess` cookie rides
//   - echoes `mkt_csrf` into the `X-CSRF-Token` header on mutating verbs
//     (Story 10.10 double-submit pattern)
//   - decodes RFC 9457 problem+json responses into a structured ApiError
//
// No global state: every call takes the absolute path. The base URL is
// pulled from `import.meta.env.VITE_API_BASE` so the dev proxy and the
// production build both work without ifdefs.

const BASE = (import.meta.env.VITE_API_BASE ?? "") as string;

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
      headers.set("X-CSRF-Token", tok);
    }
  }

  const res = await fetch(`${BASE}${path}`, {
    ...opts,
    method,
    headers,
    body,
    credentials: "include",
  });

  if (res.status === 204) {
    return undefined as T;
  }

  const text = await res.text();
  const ct = res.headers.get("Content-Type") ?? "";

  if (!res.ok) {
    if (ct.includes("problem+json") || ct.includes("json")) {
      try {
        throw new ApiError(JSON.parse(text) as Problem);
      } catch (e) {
        if (e instanceof ApiError) throw e;
        // fall through to generic
      }
    }
    throw new ApiError({
      type: "about:blank",
      title: res.statusText || "request failed",
      status: res.status,
      detail: text.slice(0, 256),
    });
  }

  if (ct.includes("json")) {
    return JSON.parse(text) as T;
  }
  return text as unknown as T;
}

export const api = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, body?: unknown) => request<T>(path, { method: "POST", body }),
  patch: <T>(path: string, body?: unknown) => request<T>(path, { method: "PATCH", body }),
  put: <T>(path: string, body?: unknown) => request<T>(path, { method: "PUT", body }),
  delete: <T>(path: string) => request<T>(path, { method: "DELETE" }),
};
