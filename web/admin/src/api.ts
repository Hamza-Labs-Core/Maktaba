// The operator console's API client (story 50.33): the cloud apps' client shape (api, fetchApi,
// withQuery, CloudApiError) on the staff session instead of the customer one.
//
//   - The staff access token (10 min) lives in this module's memory only, never in storage.
//   - The session is the httpOnly `__Host-maktaba_staff` cookie: POST /v1/staff/refresh rotates
//     it and hands a new token. Concurrent callers share one refresh (a rotated cookie presented
//     twice revokes every session of the account).
//   - Every call that isn't a GET or HEAD carries the console's CSRF header (STAFF_HEADERS).
//   - An admin call that answers 401 reauth_required asks the session for a fresh MFA check
//     (the re-auth dialog) and is sent once more; 401 no_session tries one refresh, and ends the
//     session (with the server's reason, idle or capped) when that fails too.
//   - 403 admin_access_denied, mfa_required and ip_not_allowed are told to the session, which
//     shows them in the frame, and still reject the call.
import { CloudApiError, withQuery } from "@cloud-shared/api";
import { STAFF_HEADERS } from "./staff";

export { CloudApiError, withQuery };

export interface StaffAccount {
  id: string;
  email: string;
  roles: string[];
}

/** The answer of every step that opens or renews a session. */
export interface StaffSession {
  access_token: string;
  expires_in: number;
  idle_ends_at?: string;
  capped_at?: string;
  staff: StaffAccount;
}

/** Why the server ended a session (POST /v1/staff/refresh, or an admin call's 401). */
export type EndReason = "idle" | "capped";

export interface StaffHooks {
  /** A refresh or a sign-in step brought a session (null: there is none any more). */
  onSession?: (s: StaffSession | null, reason?: EndReason) => void;
  /** An admin call needs a fresh MFA check; resolves true once it is done. */
  onReauth?: () => Promise<boolean>;
  /** An admin call reached the API with this session (the idle clock restarts). */
  onActivity?: () => void;
  /** An admin call was refused for the account or the network (shown in the frame). */
  onDenied?: (code: string) => void;
}

const BASE = (import.meta.env.VITE_CLOUD_API_BASE ?? "") as string;
const DENIALS = new Set(["admin_access_denied", "mfa_required", "ip_not_allowed"]);

let accessToken: string | null = null;
let refreshing: Promise<StaffSession | EndReason | null> | null = null;
let hooks: StaffHooks = {};

/** The session provider's callbacks (one provider at a time). */
export function setStaffHooks(h: StaffHooks): void {
  hooks = h;
}

export function setStaffToken(token: string | null): void {
  accessToken = token;
}

export function getStaffToken(): string | null {
  return accessToken;
}

const isChange = (method: string): boolean => !["GET", "HEAD"].includes(method.toUpperCase());
const isAdmin = (path: string): boolean => path.startsWith("/v1/admin/");

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

/** The server's reason for ending a session, when it gave one. */
export function endReason(err: unknown): EndReason | undefined {
  if (!(err instanceof CloudApiError)) return undefined;
  const r = err.body.reason;
  return r === "idle" || r === "capped" ? r : undefined;
}

/** Keeps a new session's token and tells the provider. */
export function acceptSession(s: StaffSession): StaffSession {
  accessToken = s.access_token;
  hooks.onSession?.(s);
  return s;
}

/** Forgets the token and tells the provider the session is over. */
export function endSession(reason?: EndReason): void {
  accessToken = null;
  hooks.onSession?.(null, reason);
}

/**
 * POST /v1/staff/refresh with the cookie: the new session, the reason the server ended it
 * (idle, capped), or null when there is none. A network failure keeps the current token.
 */
export function refreshStaff(): Promise<StaffSession | EndReason | null> {
  if (!refreshing) {
    refreshing = (async () => {
      try {
        const res = await fetch(`${BASE}/v1/staff/refresh`, {
          method: "POST",
          headers: { Accept: "application/json", ...STAFF_HEADERS },
          credentials: "include",
        });
        if (!res.ok) {
          const reason = endReason(await parseError(res));
          endSession(reason);
          return reason ?? null;
        }
        return acceptSession((await res.json()) as StaffSession);
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
  /** Send the staff token and handle a 401 (default true). */
  auth?: boolean;
  signal?: AbortSignal;
}

/** The raw Response of an API call (the 401 and 403 handling above), or a CloudApiError. */
export async function fetchApi(
  method: string,
  path: string,
  opts: RequestOptions = {}
): Promise<Response> {
  const auth = opts.auth ?? true;
  const send = () => {
    const headers: Record<string, string> = {
      Accept: "application/json",
      ...(isChange(method) ? STAFF_HEADERS : {}),
    };
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
  let reauthed = false;
  let refreshed = false;
  // at most one re-auth and one refresh per call, so a server that keeps refusing ends here
  for (;;) {
    if (res.ok || !auth) break;
    if (res.status !== 401 && res.status !== 403) break;
    const err = await parseError(res);
    if (res.status === 403) {
      if (DENIALS.has(err.code)) hooks.onDenied?.(err.code);
      throw err;
    }
    if (err.code === "reauth_required" && !reauthed && hooks.onReauth) {
      reauthed = true;
      if (isAdmin(path)) hooks.onActivity?.();
      if (!(await hooks.onReauth())) throw err;
      res = await send();
      continue;
    }
    // a wrong code (mfa_failed) and the like are the call's own answer, not a lost session
    if (err.code !== "no_session") throw err;
    const reason = endReason(err);
    if (!reason && !refreshed) {
      refreshed = true;
      const r = await refreshStaff();
      if (r !== null && typeof r === "object") {
        res = await send();
        continue;
      }
      throw err;
    }
    endSession(reason);
    throw err;
  }
  if (auth && isAdmin(path) && res.status !== 401) hooks.onActivity?.();
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
