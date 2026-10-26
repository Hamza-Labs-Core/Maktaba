// HTTP client for the Maktaba Cloud API (cloud/docs/accounts-billing-admin.md).
//
//   - The access token (15 min) lives in memory only, never in storage.
//   - The refresh session is the httpOnly `maktaba_refresh` cookie, so every
//     call goes out with credentials: "include".
//   - A 401 on an authenticated call triggers one POST /v1/auth/refresh (shared
//     by concurrent callers: the cookie rotates, and presenting a rotated one
//     twice revokes every session) and the call is retried once.
//   - Errors are `{error, message}`; they surface as CloudApiError.

export interface CloudUser {
  id: string;
  email: string;
  email_verified: boolean;
  display_name?: string;
  plan: string;
  role: "user" | "service_admin" | string;
}

export interface AuthResult {
  access_token: string;
  expires_in: number;
  user: CloudUser;
}

export class CloudApiError extends Error {
  status: number;
  code: string;
  body: Record<string, unknown>;
  /** Seconds, from a Retry-After header (429). */
  retryAfter?: number;
  constructor(status: number, code: string, message: string, body: Record<string, unknown>) {
    super(message || code);
    this.name = "CloudApiError";
    this.status = status;
    this.code = code;
    this.body = body;
  }
}

const BASE = (import.meta.env.VITE_CLOUD_API_BASE ?? "") as string;

/** The URL of an API path the browser loads itself (the OAuth starts). */
export const apiUrl = (path: string): string => `${BASE}${path}`;

let accessToken: string | null = null;
let refreshing: Promise<AuthResult | null> | null = null;

type SessionListener = (result: AuthResult | null) => void;
const listeners = new Set<SessionListener>();

/** Called whenever a refresh succeeds (new user/token) or fails (signed out). */
export function onSessionChange(fn: SessionListener): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

export function setAccessToken(token: string | null): void {
  accessToken = token;
}

export function getAccessToken(): string | null {
  return accessToken;
}

async function parseError(res: Response): Promise<CloudApiError> {
  let body: Record<string, unknown> = {};
  try {
    const parsed: unknown = await res.json();
    if (parsed && typeof parsed === "object") body = parsed as Record<string, unknown>;
  } catch {
    // not JSON (a proxy page, an empty body)
  }
  const code = typeof body.error === "string" ? body.error : `http_${res.status}`;
  const message = typeof body.message === "string" ? body.message : "";
  const err = new CloudApiError(res.status, code, message, body);
  const ra = res.headers.get("Retry-After");
  if (ra && /^\d+$/.test(ra)) err.retryAfter = Number(ra);
  return err;
}

/**
 * POST /v1/auth/refresh with the cookie. Resolves to the new session, or null
 * when there is none (401 no_session or any failure). Concurrent callers share
 * one request.
 */
export function refreshSession(): Promise<AuthResult | null> {
  if (!refreshing) {
    refreshing = (async () => {
      try {
        const res = await fetch(`${BASE}/v1/auth/refresh`, {
          method: "POST",
          credentials: "include",
        });
        if (!res.ok) {
          accessToken = null;
          listeners.forEach((l) => l(null));
          return null;
        }
        const result = (await res.json()) as AuthResult;
        accessToken = result.access_token;
        listeners.forEach((l) => l(result));
        return result;
      } catch {
        return null;
      } finally {
        refreshing = null;
      }
    })();
  }
  return refreshing;
}

export interface RequestOptions {
  body?: unknown;
  /** Send the bearer token and refresh on 401 (default true). */
  auth?: boolean;
  signal?: AbortSignal;
}

/** The raw Response of an API call (with the 401 → refresh → retry), or a CloudApiError. */
export async function fetchApi(
  method: string,
  path: string,
  opts: RequestOptions = {}
): Promise<Response> {
  const auth = opts.auth ?? true;
  const send = () => {
    const headers: Record<string, string> = { Accept: "application/json" };
    if (opts.body !== undefined) headers["Content-Type"] = "application/json";
    if (auth && accessToken) headers.Authorization = `Bearer ${accessToken}`;
    return fetch(`${BASE}${path}`, {
      method,
      headers,
      credentials: "include",
      body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
      signal: opts.signal,
    });
  };
  let res = await send();
  if (res.status === 401 && auth) {
    const refreshed = await refreshSession();
    if (refreshed) res = await send();
  }
  if (!res.ok) throw await parseError(res);
  return res;
}

export async function request<T>(method: string, path: string, opts?: RequestOptions): Promise<T> {
  const res = await fetchApi(method, path, opts);
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  if (!text) return undefined as T;
  try {
    return JSON.parse(text) as T;
  } catch {
    return undefined as T;
  }
}

export const api = {
  get: <T>(path: string, opts?: RequestOptions) => request<T>("GET", path, opts),
  post: <T>(path: string, body?: unknown, opts?: RequestOptions) =>
    request<T>("POST", path, { ...opts, body }),
  patch: <T>(path: string, body?: unknown, opts?: RequestOptions) =>
    request<T>("PATCH", path, { ...opts, body }),
  delete: <T>(path: string, body?: unknown, opts?: RequestOptions) =>
    request<T>("DELETE", path, { ...opts, body }),
};

/** Builds `path?k=v` from the non-empty values. */
export function withQuery(path: string, params: Record<string, string | number | undefined>) {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== "") q.set(k, String(v));
  }
  const s = q.toString();
  return s ? `${path}?${s}` : path;
}
