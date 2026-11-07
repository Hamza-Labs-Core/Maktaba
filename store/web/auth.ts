// Store accounts' sessions, roles and the guards every route applies on the server (story
// 54.15 §4, §7). The store reads only its own cookie, `__Host-mks_session`: a Maktaba Cloud
// cookie or bearer token is never read, so such a request is signed out (T08).
import { type Env, iso, now } from "./env";
import { clientIP, cookie, refuse } from "./http";
import { sha256hex, token } from "./crypto";

export const COOKIE = "__Host-mks_session";
export type Role = "buyer" | "developer" | "store_reviewer" | "store_admin";
export type FactorKind = "password" | "totp" | "passkey" | "recovery";

/** Idle limits by the strongest role (§4). */
const IDLE: Record<Role, number> = {
  buyer: 30 * 24 * 3600_000,
  developer: 12 * 3600_000,
  store_reviewer: 8 * 3600_000,
  store_admin: 8 * 3600_000,
};
export const REAUTH_MS = 5 * 60_000;

export interface Account {
  id: string;
  email: string;
  email_verified_at: string | null;
  display_name: string;
  password_hash: string | null;
  locale: "en" | "ar";
  status: "active" | "suspended" | "deleted";
  created: string;
}

export interface Session {
  id_hash: string;
  account: string;
  created: string;
  last_seen: string;
  mfa_at: string;
  mfa_kind: FactorKind;
}

export interface Ctx {
  env: Env;
  req: Request;
  url: URL;
  ip: string;
  ipPrefix: string;
  account?: Account;
  session?: Session;
  roles: Set<Role>;
  factors: { totp: number; passkey: number };
  /** Headers the answer carries (a new or cleared cookie). */
  setCookie?: string;
}

export function idleLimit(roles: Set<Role>): number {
  let ms = IDLE.buyer;
  for (const r of roles) ms = Math.min(ms, IDLE[r]);
  return ms;
}

export async function loadFactors(env: Env, account: string): Promise<{ totp: number; passkey: number }> {
  const r = await env.DB_ACCOUNTS.prepare(
    "SELECT SUM(kind = 'totp') AS totp, SUM(kind = 'passkey') AS passkey FROM account_mfa WHERE account = ?",
  )
    .bind(account)
    .first<{ totp: number | null; passkey: number | null }>();
  return { totp: r?.totp ?? 0, passkey: r?.passkey ?? 0 };
}

export async function loadRoles(env: Env, account: string): Promise<Set<Role>> {
  const r = await env.DB_ACCOUNTS.prepare("SELECT role FROM roles WHERE account = ?").bind(account).all<{ role: Role }>();
  return new Set(r.results.map((x) => x.role));
}

/** The request's context: the signed-in account, when its session cookie is valid. */
export async function context(env: Env, req: Request): Promise<Ctx> {
  const { ip, prefix } = clientIP(req);
  const ctx: Ctx = { env, req, url: new URL(req.url), ip, ipPrefix: prefix, roles: new Set(), factors: { totp: 0, passkey: 0 } };
  const value = cookie(req, COOKIE);
  if (!value || !/^[A-Za-z0-9_-]{43}$/.test(value)) return ctx;
  const idHash = await sha256hex(value);
  const s = await env.DB_ACCOUNTS.prepare("SELECT * FROM sessions WHERE id_hash = ?").bind(idHash).first<Session>();
  if (!s) return ctx;
  const a = await env.DB_ACCOUNTS.prepare("SELECT * FROM accounts WHERE id = ?").bind(s.account).first<Account>();
  if (!a || a.status !== "active") return ctx;
  const roles = await loadRoles(env, a.id);
  const at = now();
  if (at.getTime() - Date.parse(s.last_seen) > idleLimit(roles)) {
    await env.DB_ACCOUNTS.prepare("DELETE FROM sessions WHERE id_hash = ?").bind(idHash).run();
    return ctx;
  }
  // last_seen moves at most once a minute (one write, not one per request)
  if (at.getTime() - Date.parse(s.last_seen) > 60_000) {
    await env.DB_ACCOUNTS.prepare("UPDATE sessions SET last_seen = ? WHERE id_hash = ?").bind(iso(at), idHash).run();
  }
  ctx.account = a;
  ctx.session = s;
  ctx.roles = roles;
  ctx.factors = await loadFactors(env, a.id);
  return ctx;
}

/** Starts a session for account (a factor of kind was just confirmed); sets the cookie. */
export async function startSession(ctx: Ctx, account: string, kind: FactorKind): Promise<void> {
  const value = token();
  const t = iso(now());
  await ctx.env.DB_ACCOUNTS.prepare(
    "INSERT INTO sessions (id_hash, account, created, last_seen, mfa_at, mfa_kind, user_agent) VALUES (?, ?, ?, ?, ?, ?, ?)",
  )
    .bind(await sha256hex(value), account, t, t, t, kind, summariseAgent(ctx.req.headers.get("User-Agent") ?? ""))
    .run();
  ctx.setCookie = `${COOKIE}=${value}; Path=/; Secure; HttpOnly; SameSite=Lax; Max-Age=${30 * 24 * 3600}`;
}

export function clearCookie(ctx: Ctx): void {
  ctx.setCookie = `${COOKIE}=; Path=/; Secure; HttpOnly; SameSite=Lax; Max-Age=0`;
}

/** Ends every session of an account (a password or factor change, sign out everywhere). */
export async function endSessions(env: Env, account: string, except?: string): Promise<void> {
  if (except) await env.DB_ACCOUNTS.prepare("DELETE FROM sessions WHERE account = ? AND id_hash <> ?").bind(account, except).run();
  else await env.DB_ACCOUNTS.prepare("DELETE FROM sessions WHERE account = ?").bind(account).run();
}

/** A factor was confirmed again in this session (re-auth). */
export async function confirmed(ctx: Ctx, kind: FactorKind): Promise<void> {
  const t = iso(now());
  await ctx.env.DB_ACCOUNTS.prepare("UPDATE sessions SET mfa_at = ?, mfa_kind = ? WHERE id_hash = ?")
    .bind(t, kind, ctx.session!.id_hash)
    .run();
  ctx.session!.mfa_at = t;
  ctx.session!.mfa_kind = kind;
}

function summariseAgent(ua: string): string {
  const os = /Windows|Mac OS X|Android|iPhone|iPad|Linux/.exec(ua)?.[0] ?? "";
  const br = /Edg|Firefox|Chrome|Safari/.exec(ua)?.[0] ?? "";
  return [br, os].filter(Boolean).join(" on ").slice(0, 60);
}

// ---- the guards (every route applies them on the server, §4, §7) ---------------------------

export function signedIn(ctx: Ctx): Account {
  if (!ctx.account) refuse(401, "signed_out");
  return ctx.account!;
}

export function verified(ctx: Ctx): Account {
  const a = signedIn(ctx);
  if (!a.email_verified_at) refuse(403, "email_unverified");
  return a;
}

export const hasMFA = (ctx: Ctx) => ctx.factors.totp + ctx.factors.passkey > 0;

/** A developer with a second factor (54.15: before any submit, key or listing change). */
export function developer(ctx: Ctx): Account {
  const a = verified(ctx);
  if (!ctx.roles.has("developer")) refuse(403, "not_developer");
  if (!hasMFA(ctx)) refuse(403, "mfa_required");
  return a;
}

/** A store reviewer or admin, signed in with a passkey (phishing-resistant, §4, §7). */
export function staff(ctx: Ctx, role: "store_reviewer" | "store_admin"): Account {
  const a = verified(ctx);
  const ok = role === "store_reviewer" ? ctx.roles.has("store_reviewer") || ctx.roles.has("store_admin") : ctx.roles.has("store_admin");
  if (!ok) refuse(403, "forbidden");
  if (ctx.factors.passkey === 0 || ctx.session!.mfa_kind !== "passkey") refuse(403, "passkey_required");
  return a;
}

/** A factor confirmed in the last 5 minutes (a second factor when the account has one). */
export function reauthed(ctx: Ctx): void {
  const s = ctx.session!;
  const fresh = now().getTime() - Date.parse(s.mfa_at) <= REAUTH_MS;
  const strong = !hasMFA(ctx) || s.mfa_kind !== "password";
  if (!fresh || !strong) refuse(401, "reauth_required");
}

/** Staff decisions: a passkey confirmed in the last 5 minutes. */
export function reauthedPasskey(ctx: Ctx): void {
  const s = ctx.session!;
  if (s.mfa_kind !== "passkey" || now().getTime() - Date.parse(s.mfa_at) > REAUTH_MS) refuse(401, "reauth_required");
}

/** Writes an audit row (§4: every role and sensitive action). */
export async function audit(
  ctx: Ctx | { env: Env; ipPrefix: string },
  actor: string,
  action: string,
  target: string,
  result = "ok",
): Promise<void> {
  await ctx.env.DB_ACCOUNTS.prepare("INSERT INTO audit (actor, action, target, at, ip_prefix, result) VALUES (?, ?, ?, ?, ?, ?)")
    .bind(actor, action, target, iso(now()), ctx.ipPrefix, result)
    .run();
}

/** A per-address rate limit (IpBucket); 429 rate_limited when spent. */
export async function limit(ctx: Ctx, action: string, perWindow: number, windowMs: number): Promise<void> {
  for (const key of [ctx.ip, ctx.ipPrefix]) {
    const ok = await ctx.env.IP_BUCKET.get(ctx.env.IP_BUCKET.idFromName(key)).take(action, perWindow, windowMs, now().getTime());
    if (!ok) refuse(429, "rate_limited");
  }
}

/** A state-changing request with the session cookie must come from the store's own pages. */
export function sameOrigin(ctx: Ctx): void {
  if (!ctx.account) return;
  const origin = ctx.req.headers.get("Origin");
  if (origin !== ctx.env.STORE_ORIGIN) refuse(403, "cross_origin");
}
