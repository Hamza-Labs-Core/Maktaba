// Store admin (story 54.15 §4, §7) on the store's own host: roles, the one-time bootstrap of
// the first store admin, suspensions, the 7-day hold on an MFA reset, revocations and the
// batch re-sign of a key rotation. Every route checks the role and a passkey on the server
// and needs a passkey confirmed in the last 5 minutes; every action writes an audit row.
import { type Ctx, audit, endSessions, reauthed, reauthedPasskey, staff, verified } from "./auth";
import { sha256hex } from "./crypto";
import { type Env, iso, now } from "./env";
import { body, json, optStr, refuse, str } from "./http";
import { sendMail } from "./mail";

const HOLD_MS = 7 * 24 * 3600_000;
const ROLES = ["developer", "store_reviewer", "store_admin"] as const;
const ACTIONS = ["disable", "warn", "delist"] as const;

/** POST /v1/admin/bootstrap {token}: the first store admin, once (the owner's Worker secret). */
export async function bootstrap(ctx: Ctx): Promise<Response> {
  const a = verified(ctx);
  reauthed(ctx);
  if (ctx.factors.passkey === 0 || ctx.session!.mfa_kind !== "passkey") refuse(403, "passkey_required");
  const b = await body(ctx.req);
  const t = str(b.token, "token", 200);
  const want = ctx.env.BOOTSTRAP_ADMIN_TOKEN;
  if (!want || want.length < 32) refuse(404, "not_found");
  const h = await sha256hex(t);
  if (h !== (await sha256hex(want!))) {
    await audit(ctx, a.id, "admin.bootstrap", a.id, "bad_token");
    refuse(403, "forbidden");
  }
  const used = await ctx.env.DB_ACCOUNTS.prepare("INSERT OR IGNORE INTO bootstrap_used (token_hash, account, at) VALUES (?, ?, ?)")
    .bind(h, a.id, iso(now()))
    .run();
  if (used.meta.changes !== 1) refuse(410, "bootstrap_used");
  await ctx.env.DB_ACCOUNTS.prepare("INSERT OR IGNORE INTO roles (account, role, granted_by, at) VALUES (?, 'store_admin', 'bootstrap', ?)")
    .bind(a.id, iso(now()))
    .run();
  await audit(ctx, a.id, "admin.bootstrap", a.id);
  return json({ status: "store_admin" });
}

function admin(ctx: Ctx) {
  const a = staff(ctx, "store_admin");
  reauthedPasskey(ctx);
  return a;
}

/** POST /v1/admin/roles {account, role} and DELETE /v1/admin/roles/{account}/{role}. */
export async function grantRole(ctx: Ctx): Promise<Response> {
  const a = admin(ctx);
  const b = await body(ctx.req);
  const target = str(b.account, "account", 64);
  const role = str(b.role, "role", 32) as (typeof ROLES)[number];
  if (!ROLES.includes(role)) refuse(400, "role_invalid");
  const exists = await ctx.env.DB_ACCOUNTS.prepare("SELECT 1 FROM accounts WHERE id = ? AND status = 'active'").bind(target).first();
  if (!exists) refuse(404, "not_found");
  await ctx.env.DB_ACCOUNTS.prepare("INSERT OR IGNORE INTO roles (account, role, granted_by, at) VALUES (?, ?, ?, ?)")
    .bind(target, role, a.id, iso(now()))
    .run();
  await audit(ctx, a.id, "role.grant", `${target}:${role}`);
  return json({ status: "granted" });
}

export async function revokeRole(ctx: Ctx, target: string, role: string): Promise<Response> {
  const a = admin(ctx);
  if (target === a.id && role === "store_admin") refuse(409, "last_admin_guard");
  await ctx.env.DB_ACCOUNTS.prepare("DELETE FROM roles WHERE account = ? AND role = ?").bind(target, role).run();
  await endSessions(ctx.env, target);
  await audit(ctx, a.id, "role.revoke", `${target}:${role}`);
  return json({ status: "revoked" });
}

/** POST /v1/admin/accounts/{id}/suspend {reason}: signed out, keys suspended, plugins stay. */
export async function suspend(ctx: Ctx, target: string): Promise<Response> {
  const a = admin(ctx);
  const b = await body(ctx.req);
  const at = iso(now());
  await ctx.env.DB_ACCOUNTS.prepare("UPDATE accounts SET status = 'suspended' WHERE id = ? AND status = 'active'").bind(target).run();
  await endSessions(ctx.env, target);
  await ctx.env.DB_CATALOG.batch([
    ctx.env.DB_CATALOG.prepare("UPDATE developers SET status = 'suspended' WHERE account = ?").bind(target),
    ctx.env.DB_CATALOG.prepare("UPDATE developer_keys SET suspended_at = COALESCE(suspended_at, ?) WHERE account = ?").bind(at, target),
  ]);
  await audit(ctx, a.id, "account.suspend", `${target}:${optStr(b.reason, "reason", 200) ?? ""}`);
  return json({ status: "suspended" });
}

/** POST /v1/admin/accounts/{id}/mfa-reset: starts the 7-day hold, emailing the account. */
export async function startMfaReset(ctx: Ctx, target: string): Promise<Response> {
  const a = admin(ctx);
  const acc = await ctx.env.DB_ACCOUNTS.prepare("SELECT email FROM accounts WHERE id = ? AND status = 'active'")
    .bind(target)
    .first<{ email: string }>();
  if (!acc) refuse(404, "not_found");
  const at = now();
  const done = new Date(at.getTime() + HOLD_MS);
  await ctx.env.DB_ACCOUNTS.prepare(
    "INSERT INTO mfa_resets (account, requested_by, requested_at, completes_at) VALUES (?, ?, ?, ?) ON CONFLICT (account) DO UPDATE SET requested_by = excluded.requested_by, requested_at = excluded.requested_at, completes_at = excluded.completes_at, done_at = NULL, cancelled_at = NULL",
  )
    .bind(target, a.id, iso(at), iso(done))
    .run();
  await sendMail(ctx.env, { to: acc!.email, template: "mfa_reset_started", params: { date: iso(done) } });
  await audit(ctx, a.id, "mfa.reset_start", target);
  return json({ status: "held", completes_at: iso(done) }, 202);
}

/** DELETE /v1/admin/accounts/{id}/mfa-reset: cancels a hold. */
export async function cancelMfaReset(ctx: Ctx, target: string): Promise<Response> {
  const a = admin(ctx);
  await ctx.env.DB_ACCOUNTS.prepare("UPDATE mfa_resets SET cancelled_at = ? WHERE account = ? AND done_at IS NULL")
    .bind(iso(now()), target)
    .run();
  await audit(ctx, a.id, "mfa.reset_cancel", target);
  return json({ status: "cancelled" });
}

/** The 6-hourly cron: resets whose hold is over; the account's keys stay suspended until a new factor. */
export async function finishMfaResets(env: Env): Promise<number> {
  const due = await env.DB_ACCOUNTS.prepare(
    "SELECT account FROM mfa_resets WHERE done_at IS NULL AND cancelled_at IS NULL AND completes_at <= ?",
  )
    .bind(iso(now()))
    .all<{ account: string }>();
  for (const r of due.results) {
    const at = iso(now());
    await env.DB_ACCOUNTS.batch([
      env.DB_ACCOUNTS.prepare("DELETE FROM account_mfa WHERE account = ?").bind(r.account),
      env.DB_ACCOUNTS.prepare("DELETE FROM recovery_codes WHERE account = ?").bind(r.account),
      env.DB_ACCOUNTS.prepare("DELETE FROM sessions WHERE account = ?").bind(r.account),
      env.DB_ACCOUNTS.prepare("UPDATE mfa_resets SET done_at = ? WHERE account = ?").bind(at, r.account),
    ]);
    // a stolen inbox alone cannot publish: the keys wait for a new factor
    await env.DB_CATALOG.prepare(
      "UPDATE developer_keys SET suspended_at = COALESCE(suspended_at, ?) WHERE account = ? AND revoked_at IS NULL",
    )
      .bind(at, r.account)
      .run();
    const acc = await env.DB_ACCOUNTS.prepare("SELECT email FROM accounts WHERE id = ?").bind(r.account).first<{ email: string }>();
    if (acc) await sendMail(env, { to: acc.email, template: "mfa_reset_done", params: {} });
    await env.DB_ACCOUNTS.prepare("INSERT INTO audit (actor, action, target, at, result) VALUES ('system', 'mfa.reset_done', ?, ?, 'ok')")
      .bind(r.account, at)
      .run();
  }
  return due.results.length;
}

/** POST /v1/admin/revocations {plugin_id, versions, developer_key_id?, action, reason_code}. */
export async function addRevocation(ctx: Ctx): Promise<Response> {
  const a = admin(ctx);
  const b = await body(ctx.req);
  const action = str(b.action, "action", 10) as (typeof ACTIONS)[number];
  if (!ACTIONS.includes(action)) refuse(400, "action_invalid");
  const plugin = str(b.plugin_id, "plugin_id", 128);
  const versions = str(b.versions, "versions", 100);
  const reason = str(b.reason_code, "reason_code", 40);
  if (!/^[a-z_]+$/.test(reason)) refuse(400, "field_invalid", { field: "reason_code" });
  const key = optStr(b.developer_key_id, "developer_key_id", 64) ?? "";
  const r = await ctx.env.DB_CATALOG.prepare(
    "INSERT INTO revocations (plugin_id, versions, developer_key_id, action, reason_code, issued, issued_by) VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id",
  )
    .bind(plugin, versions, key, action, reason, iso(now()), a.id)
    .first<{ id: number }>();
  if (action === "delist")
    await ctx.env.DB_CATALOG.prepare("UPDATE plugins SET status = 'delisted' WHERE id = ? AND status = 'listed'").bind(plugin).run();
  if (key)
    await ctx.env.DB_CATALOG.prepare("UPDATE developer_keys SET revoked_at = COALESCE(revoked_at, ?) WHERE id = ?")
      .bind(iso(now()), key)
      .run();
  const v = await ctx.env.CATALOG_WRITER.get(ctx.env.CATALOG_WRITER.idFromName("main")).regenerate();
  await audit(ctx, a.id, "revocation.add", `${r!.id}:${plugin}:${action}`);
  return json({ id: r!.id, revoked_version: v.revoked }, 201);
}

/** DELETE /v1/admin/revocations/{id}: lifted (a home keeps the plugin held; its admin may start it). */
export async function liftRevocation(ctx: Ctx, id: string): Promise<Response> {
  const a = admin(ctx);
  const r = await ctx.env.DB_CATALOG.prepare("UPDATE revocations SET lifted_at = ? WHERE id = ? AND lifted_at IS NULL")
    .bind(iso(now()), Number(id))
    .run();
  if (r.meta.changes !== 1) refuse(404, "not_found");
  const v = await ctx.env.CATALOG_WRITER.get(ctx.env.CATALOG_WRITER.idFromName("main")).regenerate();
  await audit(ctx, a.id, "revocation.lift", id);
  return json({ status: "lifted", revoked_version: v.revoked });
}

/** POST /v1/admin/resign: the batch job of a key rotation (54.14 §5), queued in pages. */
export async function resignAll(ctx: Ctx): Promise<Response> {
  const a = admin(ctx);
  await ctx.env.Q_CATALOG.send({ kind: "resign", after: "" });
  await audit(ctx, a.id, "store.resign", "");
  return json({ status: "queued" }, 202);
}

/** GET /v1/admin/audit?before=<id>: the newest audit rows. */
export async function auditLog(ctx: Ctx): Promise<Response> {
  staff(ctx, "store_admin");
  const before = Number(ctx.url.searchParams.get("before") ?? Number.MAX_SAFE_INTEGER);
  const r = await ctx.env.DB_ACCOUNTS.prepare("SELECT * FROM audit WHERE id < ? ORDER BY id DESC LIMIT 200").bind(before).all();
  return json({ rows: r.results });
}
