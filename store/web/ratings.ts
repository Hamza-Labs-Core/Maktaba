// Ratings and reports (story 54.18 §3, §4). A rating is 1-5 stars and optional plain text,
// one per store account per plugin, editable, only from a verified account linked to a home
// that has had the plugin installed for 24 hours (the home's signed `seen`, or the home's own
// signed request). Reports come from a verified account or a linked home; malware and data
// misuse alert the store's admins and lead the moderation queue.
import { type Account, type Ctx, audit, limit, reauthedPasskey, staff, verified } from "./auth";
import { newId } from "./crypto";
import { type Env, iso, now } from "./env";
import { type Home, signedHome } from "./homes";
import { body, json, optStr, refuse, str } from "./http";
import { sendMail } from "./mail";

export const ELIGIBLE_MS = 24 * 3600_000;
export const MIN_SHOWN = 5;
export const REASONS = ["malware", "data_misuse", "does_not_work", "misleading", "offensive", "copyright", "other"] as const;
export const URGENT = new Set(["malware", "data_misuse"]);
export const OUTCOMES = ["no_action", "contact_developer", "warn", "delist", "disable", "suspend_developer"] as const;

/** Plain text only: no markup survives (the pages and the apps show text, never HTML). */
export function plain(s: string, max = 2000): string {
  // control characters out (but new lines), trimmed; nothing else is interpreted
  return s
    .replace(/[\u0000-\u0009\u000B-\u001F\u007F]/g, "")
    .trim()
    .slice(0, max);
}

async function listed(env: Env, plugin: string): Promise<void> {
  const p = await env.DB_CATALOG.prepare("SELECT status FROM plugins WHERE id = ?").bind(plugin).first<{ status: string }>();
  if (!p) refuse(404, "not_found");
  if (p!.status !== "listed") refuse(409, "plugin_delisted");
}

/** The earliest first_seen of the plugin on any of the account's linked homes (null: none). */
async function installedSince(env: Env, account: string, plugin: string, home?: string): Promise<string | null> {
  const q = home
    ? env.DB_INSTALLS.prepare("SELECT i.first_seen FROM installs i JOIN homes h ON h.id = i.home WHERE h.id = ? AND h.unlinked_at IS NULL AND i.plugin = ?").bind(home, plugin)
    : env.DB_INSTALLS.prepare(
        "SELECT MIN(i.first_seen) AS first_seen FROM installs i JOIN homes h ON h.id = i.home WHERE h.account = ? AND h.unlinked_at IS NULL AND i.plugin = ?",
      ).bind(account, plugin);
  const r = await q.first<{ first_seen: string | null }>();
  return r?.first_seen ?? null;
}

async function rateAs(env: Env, a: Account, plugin: string, b: Record<string, unknown>, home?: string): Promise<Response> {
  await listed(env, plugin);
  const linked = await env.DB_INSTALLS.prepare("SELECT count(*) AS n FROM homes WHERE account = ? AND unlinked_at IS NULL").bind(a.id).first<{ n: number }>();
  if (!linked?.n) refuse(403, "rating_no_home");
  const since = await installedSince(env, a.id, plugin, home);
  if (!since || now().getTime() - Date.parse(since) < ELIGIBLE_MS) refuse(403, "rating_too_early", { after_hours: 24 });
  const stars = Number(b.stars);
  if (!Number.isInteger(stars) || stars < 1 || stars > 5) refuse(400, "field_invalid", { field: "stars" });
  const text = plain(optStr(b.text, "text", 4000) ?? "");
  const at = iso(now());
  await env.DB_CATALOG.prepare(
    `INSERT INTO ratings (id, plugin, account, author, stars, text, created, updated) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
     ON CONFLICT (plugin, account) DO UPDATE SET stars = excluded.stars, text = excluded.text, updated = excluded.updated`,
  )
    .bind(newId("r"), plugin, a.id, a.display_name || "—", stars, text, at, at)
    .run();
  return json({ status: "rated" });
}

/** POST /v1/plugins/{id}/ratings {stars, text}: a verified, eligible store account. */
export async function rate(ctx: Ctx, plugin: string): Promise<Response> {
  const a = verified(ctx);
  await limit(ctx, "rate", 30, 3600_000);
  return rateAs(ctx.env, a, plugin, await body(ctx.req));
}

/** The account of a signed home, verified. */
async function homeAccount(env: Env, home: Home): Promise<Account> {
  const a = await env.DB_ACCOUNTS.prepare("SELECT * FROM accounts WHERE id = ? AND status = 'active'").bind(home.account).first<Account>();
  if (!a) refuse(401, "home_unknown");
  if (!a!.email_verified_at) refuse(403, "email_unverified");
  return a!;
}

/** POST /v1/homes/{id}/ratings {plugin, stars, text}: the admin rates from inside the home (signed). */
export async function rateFromHome(env: Env, req: Request, id: string): Promise<Response> {
  const raw = new Uint8Array(await req.arrayBuffer());
  const home = await signedHome(env, req, raw);
  if (!home || home.id !== id) refuse(401, "home_signature_invalid");
  const b = JSON.parse(new TextDecoder().decode(raw)) as Record<string, unknown>;
  return rateAs(env, await homeAccount(env, home!), str(b.plugin, "plugin", 128), b, home!.id);
}

/** GET /v1/plugins/{id}/ratings: public; the average shows from 5 ratings. */
export async function ratings(env: Env, plugin: string): Promise<Response> {
  const r = await env.DB_CATALOG.prepare(
    "SELECT id, author, stars, text, created, updated, reply, replied_at FROM ratings WHERE plugin = ? AND hidden_at IS NULL ORDER BY updated DESC LIMIT 100",
  )
    .bind(plugin)
    .all<{ stars: number }>();
  const agg = await env.DB_CATALOG.prepare("SELECT COUNT(*) AS n, AVG(stars) AS avg FROM ratings WHERE plugin = ? AND hidden_at IS NULL").bind(plugin).first<{ n: number; avg: number | null }>();
  const n = agg?.n ?? 0;
  return json({ count: n, average: n >= MIN_SHOWN ? Math.round((agg?.avg ?? 0) * 10) / 10 : null, reviews: r.results });
}

/** POST /v1/ratings/{id}/reply {text}: the plugin's developer, once per review. */
export async function reply(ctx: Ctx, id: string): Promise<Response> {
  const a = verified(ctx);
  const r = await ctx.env.DB_CATALOG.prepare("SELECT r.plugin, r.reply, p.developer FROM ratings r JOIN plugins p ON p.id = r.plugin WHERE r.id = ?")
    .bind(id)
    .first<{ plugin: string; reply: string; developer: string }>();
  if (!r) refuse(404, "not_found");
  if (r!.developer !== a.id) {
    await audit(ctx, a.id, "rating.reply", id, "forbidden");
    refuse(403, "forbidden");
  }
  if (r!.reply) refuse(409, "already_replied");
  const text = plain(str((await body(ctx.req)).text, "text", 4000));
  await ctx.env.DB_CATALOG.prepare("UPDATE ratings SET reply = ?, replied_at = ? WHERE id = ?").bind(text, iso(now()), id).run();
  return json({ status: "replied" });
}

/** POST /v1/ratings/{id}/report: a verified account flags a review for the moderators. */
export async function reportRating(ctx: Ctx, id: string): Promise<Response> {
  verified(ctx);
  await limit(ctx, "report", 20, 3600_000);
  await ctx.env.DB_CATALOG.prepare("UPDATE ratings SET reported = reported + 1 WHERE id = ?").bind(id).run();
  return json({ status: "reported" });
}

async function file(env: Env, plugin: string, b: Record<string, unknown>, who: { account?: string; home?: string }): Promise<Response> {
  const reason = str(b.reason, "reason", 20);
  if (!(REASONS as readonly string[]).includes(reason)) refuse(400, "reason_invalid", { allowed: REASONS });
  const p = await env.DB_CATALOG.prepare("SELECT id FROM plugins WHERE id = ?").bind(plugin).first();
  if (!p) refuse(404, "not_found");
  const id = newId("rp");
  const urgent = URGENT.has(reason);
  await env.DB_CATALOG.prepare("INSERT INTO reports (id, plugin, version, reason, details, account, home, urgent, created) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)")
    .bind(id, plugin, (optStr(b.version, "version", 64) ?? "").slice(0, 64), reason, plain(optStr(b.details, "details", 6000) ?? "", 4000), who.account ?? "", who.home ?? "", urgent ? 1 : 0, iso(now()))
    .run();
  if (urgent) {
    // the store's own on-call: its admins, by email (54.16 §5), never cloud monitoring
    const admins = await env.DB_ACCOUNTS.prepare("SELECT a.email FROM accounts a JOIN roles r ON r.account = a.id WHERE r.role = 'store_admin' AND a.status = 'active'").all<{ email: string }>();
    for (const a of admins.results) await sendMail(env, { to: a.email, template: "report_urgent", params: { reason, plugin } });
  }
  return json({ id, status: "filed", urgent }, 201);
}

/** POST /v1/plugins/{id}/reports {reason, details, version}: a verified store account. */
export async function report(ctx: Ctx, plugin: string): Promise<Response> {
  const a = verified(ctx);
  await limit(ctx, "report", 20, 3600_000);
  return file(ctx.env, plugin, await body(ctx.req), { account: a.id });
}

/** POST /v1/homes/{id}/reports {plugin, reason, details, version}: from the home (signed). */
export async function reportFromHome(env: Env, req: Request, id: string): Promise<Response> {
  const raw = new Uint8Array(await req.arrayBuffer());
  const home = await signedHome(env, req, raw);
  if (!home || home.id !== id) refuse(401, "home_signature_invalid");
  const b = JSON.parse(new TextDecoder().decode(raw)) as Record<string, unknown>;
  return file(env, str(b.plugin, "plugin", 128), b, { home: home!.id, account: home!.account });
}

/** GET /v1/admin/reports: open reports, malware and data misuse first, then oldest. */
export async function moderationQueue(ctx: Ctx): Promise<Response> {
  staff(ctx, "store_reviewer");
  const r = await ctx.env.DB_CATALOG.prepare("SELECT * FROM reports WHERE status = 'open' ORDER BY urgent DESC, created ASC LIMIT 200").all();
  const reviews = await ctx.env.DB_CATALOG.prepare("SELECT id, plugin, author, stars, text, reported FROM ratings WHERE reported > 0 AND hidden_at IS NULL ORDER BY reported DESC LIMIT 100").all();
  return json({ reports: r.results, reviews: reviews.results });
}

/** POST /v1/admin/reports/{id}/resolve {outcome}: the reporter is told the outcome in general terms. */
export async function resolveReport(ctx: Ctx, id: string): Promise<Response> {
  const a = staff(ctx, "store_reviewer");
  reauthedPasskey(ctx);
  const outcome = str((await body(ctx.req)).outcome, "outcome", 30);
  if (!(OUTCOMES as readonly string[]).includes(outcome)) refuse(400, "outcome_invalid", { allowed: OUTCOMES });
  const r = await ctx.env.DB_CATALOG.prepare("UPDATE reports SET status = 'resolved', outcome = ?, resolved_at = ?, resolved_by = ? WHERE id = ? AND status = 'open' RETURNING account")
    .bind(outcome, iso(now()), a.id, id)
    .first<{ account: string }>();
  if (!r) refuse(404, "not_found");
  if (r!.account) {
    const to = await ctx.env.DB_ACCOUNTS.prepare("SELECT email FROM accounts WHERE id = ?").bind(r!.account).first<{ email: string }>();
    if (to) await sendMail(ctx.env, { to: to.email, template: "report_resolved", params: { outcome: outcome === "no_action" ? "no action was needed" : "the store acted on it" } });
  }
  await audit(ctx, a.id, "report.resolve", `${id}:${outcome}`);
  return json({ status: "resolved" });
}

/** POST /v1/admin/ratings/{id}/hide: a moderator hides a review (brigading, abuse). */
export async function hideRating(ctx: Ctx, id: string): Promise<Response> {
  const a = staff(ctx, "store_reviewer");
  reauthedPasskey(ctx);
  await ctx.env.DB_CATALOG.prepare("UPDATE ratings SET hidden_at = ? WHERE id = ?").bind(iso(now()), id).run();
  await audit(ctx, a.id, "rating.hide", id);
  return json({ status: "hidden" });
}

