// Developers (story 54.15 §4, §6, §8): the agreement, signing keys, id prefixes and
// submissions with their uploads. Every route checks the role and the owner on the server.
import { AwsClient } from "aws4fetch";
import { fromB64, keyId } from "../signing/sig";
import { type Ctx, audit, developer, reauthed, verified } from "./auth";
import { newId, sha256hex, token } from "./crypto";
import { lookupTXT } from "./dns";
import { type Env, iso, now } from "./env";
import { body, json, optStr, refuse, str } from "./http";
import { sendMail } from "./mail";

const PACKAGE_MAX = 50 << 20;
const SOURCE_MAX = 200 << 20;
const ID = /^[a-z][a-z0-9-]*(\.[a-z0-9][a-z0-9-]*){2,}$/;
const VERSION = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z.-]+)?$/;
const HANDLE = /^[a-z][a-z0-9-]{2,30}$/;

/** POST /v1/developers {agreement_version}: become a developer, or accept a new agreement. */
export async function becomeDeveloper(ctx: Ctx): Promise<Response> {
  const a = verified(ctx);
  reauthed(ctx);
  const b = await body(ctx.req);
  const version = Number(ctx.env.AGREEMENT_VERSION);
  if (b.agreement_version !== version) refuse(400, "agreement_version", { current: version });
  const at = iso(now());
  await ctx.env.DB_CATALOG.prepare(
    "INSERT INTO developers (account, agreement_version, created) VALUES (?, ?, ?) ON CONFLICT (account) DO UPDATE SET agreement_version = excluded.agreement_version",
  )
    .bind(a.id, version, at)
    .run();
  await ctx.env.DB_ACCOUNTS.prepare("INSERT OR IGNORE INTO roles (account, role, granted_by, at) VALUES (?, 'developer', 'agreement', ?)")
    .bind(a.id, at)
    .run();
  await audit(ctx, a.id, "developer.agreement", String(version));
  return json({ status: "developer", agreement_version: version });
}

/** GET /v1/developers/me: the console's data. */
export async function developerMe(ctx: Ctx): Promise<Response> {
  const a = verified(ctx);
  const dev = await ctx.env.DB_CATALOG.prepare("SELECT * FROM developers WHERE account = ?").bind(a.id).first();
  const keys = await ctx.env.DB_CATALOG.prepare(
    "SELECT id, public_key, added, revoked_at, suspended_at FROM developer_keys WHERE account = ? ORDER BY added",
  )
    .bind(a.id)
    .all();
  const prefixes = await ctx.env.DB_CATALOG.prepare(
    "SELECT prefix, kind, token, verified_at, checked_at, lapsed_at FROM developer_prefixes WHERE account = ?",
  )
    .bind(a.id)
    .all();
  const plugins = await ctx.env.DB_CATALOG.prepare("SELECT id, names, status FROM plugins WHERE developer = ? ORDER BY id")
    .bind(a.id)
    .all();
  const subs = await ctx.env.DB_CATALOG.prepare(
    "SELECT id, plugin, version, status, decision, reasons, notes, created, decided_at FROM submissions WHERE uploaded_by = ? ORDER BY created DESC LIMIT 100",
  )
    .bind(a.id)
    .all();
  return json({
    developer: dev,
    agreement_current: Number(ctx.env.AGREEMENT_VERSION),
    keys: keys.results,
    prefixes: prefixes.results,
    plugins: plugins.results,
    submissions: subs.results,
  });
}

// ---- signing keys -----------------------------------------------------------------------

/** POST /v1/developers/keys {id, public}: the output of `mkp keys export-public`. */
export async function addKey(ctx: Ctx): Promise<Response> {
  const a = developer(ctx);
  reauthed(ctx);
  const b = await body(ctx.req);
  const id = str(b.id, "id", 64);
  const pub = str(b.public, "public", 64);
  let raw: Uint8Array;
  try {
    raw = fromB64(pub);
  } catch {
    return refuse(400, "key_invalid");
  }
  if (raw.length !== 32 || (await keyId(raw)) !== id) refuse(400, "key_invalid");
  const taken = await ctx.env.DB_CATALOG.prepare("SELECT account FROM developer_keys WHERE id = ?").bind(id).first<{ account: string }>();
  if (taken) refuse(409, taken.account === a.id ? "key_exists" : "key_taken");
  await ctx.env.DB_CATALOG.prepare("INSERT INTO developer_keys (id, account, public_key, added) VALUES (?, ?, ?, ?)")
    .bind(id, a.id, pub, iso(now()))
    .run();
  await audit(ctx, a.id, "key.add", id);
  return json({ id }, 201);
}

/** DELETE /v1/developers/keys/{id}: new uploads signed by it are refused from now on. */
export async function revokeKey(ctx: Ctx, id: string): Promise<Response> {
  const a = developer(ctx);
  reauthed(ctx);
  const r = await ctx.env.DB_CATALOG.prepare("UPDATE developer_keys SET revoked_at = ? WHERE id = ? AND account = ? AND revoked_at IS NULL")
    .bind(iso(now()), id, a.id)
    .run();
  if (r.meta.changes !== 1) {
    await audit(ctx, a.id, "key.revoke", id, "forbidden");
    refuse(404, "not_found");
  }
  await audit(ctx, a.id, "key.revoke", id);
  return json({ status: "revoked" });
}

// ---- id prefixes ------------------------------------------------------------------------

/** The domain a prefix proves: "dev.example" → "example.dev". */
export const domainOf = (prefix: string) => prefix.split(".").reverse().join(".");

/** POST /v1/developers/prefixes {prefix} | {community: handle}. */
export async function claimPrefix(ctx: Ctx): Promise<Response> {
  const a = developer(ctx);
  reauthed(ctx);
  const b = await body(ctx.req);
  const at = iso(now());
  if (b.community !== undefined) {
    const handle = str(b.community, "community", 31);
    if (!HANDLE.test(handle)) refuse(400, "handle_invalid");
    const prefix = `tv.maktaba.community.${handle}`;
    const r = await ctx.env.DB_CATALOG.prepare(
      "INSERT OR IGNORE INTO developer_prefixes (prefix, account, kind, verified_at) VALUES (?, ?, 'community', ?)",
    )
      .bind(prefix, a.id, at)
      .run();
    if (r.meta.changes !== 1) refuse(409, "prefix_taken");
    await audit(ctx, a.id, "prefix.claim", prefix);
    return json({ prefix, verified: true }, 201);
  }
  const prefix = str(b.prefix, "prefix", 100).toLowerCase();
  if (!/^[a-z0-9-]+(\.[a-z0-9-]+)+$/.test(prefix) || prefix.startsWith("tv.maktaba")) refuse(400, "prefix_invalid");
  const t = token().slice(0, 24);
  const r = await ctx.env.DB_CATALOG.prepare(
    "INSERT OR IGNORE INTO developer_prefixes (prefix, account, kind, token) VALUES (?, ?, 'dns', ?)",
  )
    .bind(prefix, a.id, t)
    .run();
  if (r.meta.changes !== 1) refuse(409, "prefix_taken");
  await audit(ctx, a.id, "prefix.claim", prefix);
  return json({ prefix, domain: domainOf(prefix), txt: `maktaba-developer=${t}`, verified: false }, 201);
}

/** Checks a DNS prefix's TXT record now; answers whether it is there. */
export async function checkPrefix(env: Env, prefix: string, fetcher: typeof fetch = fetch): Promise<boolean> {
  const p = await env.DB_CATALOG.prepare("SELECT * FROM developer_prefixes WHERE prefix = ? AND kind = 'dns'")
    .bind(prefix)
    .first<{ token: string; verified_at: string | null }>();
  if (!p) return false;
  const records = await lookupTXT(domainOf(prefix), fetcher);
  const ok = records.includes(`maktaba-developer=${p.token}`);
  const at = iso(now());
  if (ok)
    await env.DB_CATALOG.prepare(
      "UPDATE developer_prefixes SET verified_at = COALESCE(verified_at, ?), checked_at = ?, lapsed_at = NULL WHERE prefix = ?",
    )
      .bind(at, at, prefix)
      .run();
  else if (p.verified_at)
    await env.DB_CATALOG.prepare("UPDATE developer_prefixes SET checked_at = ?, lapsed_at = COALESCE(lapsed_at, ?) WHERE prefix = ?")
      .bind(at, at, prefix)
      .run();
  return ok;
}

/** POST /v1/developers/prefixes/{prefix}/check. */
export async function checkPrefixRoute(ctx: Ctx, prefix: string): Promise<Response> {
  const a = developer(ctx);
  const own = await ctx.env.DB_CATALOG.prepare("SELECT account FROM developer_prefixes WHERE prefix = ?")
    .bind(prefix)
    .first<{ account: string }>();
  if (!own || own.account !== a.id) refuse(404, "not_found");
  return json({ prefix, verified: await checkPrefix(ctx.env, prefix) });
}

/** The monthly Cron Trigger: every DNS prefix again; a lapsed one blocks new submissions. */
export async function recheckPrefixes(env: Env, fetcher: typeof fetch = fetch): Promise<void> {
  const rows = await env.DB_CATALOG.prepare(
    "SELECT prefix, account, lapsed_at FROM developer_prefixes WHERE kind = 'dns' AND verified_at IS NOT NULL",
  ).all<{
    prefix: string;
    account: string;
    lapsed_at: string | null;
  }>();
  for (const r of rows.results) {
    const ok = await checkPrefix(env, r.prefix, fetcher);
    if (!ok && !r.lapsed_at) {
      const a = await env.DB_ACCOUNTS.prepare("SELECT email FROM accounts WHERE id = ?").bind(r.account).first<{ email: string }>();
      if (a) await sendMail(env, { to: a.email, template: "prefix_lapsed", params: { prefix: r.prefix } });
    }
  }
}

// ---- submissions --------------------------------------------------------------------------

async function uploadURL(env: Env, origin: string, key: string, kind: "package" | "source"): Promise<string> {
  if (env.UPLOADS === "worker") {
    // local and test stores: the Worker takes the upload itself (never in production, where
    // a 50 MB body would pass through a Worker)
    const t = token();
    await env.PRIVATE.put(
      `upload-tokens/${await sha256hex(t)}`,
      JSON.stringify({ key, kind, max: kind === "package" ? PACKAGE_MAX : SOURCE_MAX, expires: now().getTime() + 3600_000 }),
    );
    return `${origin}/v1/uploads/${t}`;
  }
  if (!env.R2_ACCESS_KEY_ID || !env.R2_SECRET_ACCESS_KEY || !env.R2_ACCOUNT_ID) refuse(503, "uploads_unavailable");
  const aws = new AwsClient({
    accessKeyId: env.R2_ACCESS_KEY_ID!,
    secretAccessKey: env.R2_SECRET_ACCESS_KEY!,
    service: "s3",
    region: "auto",
  });
  const url = new URL(`https://${env.R2_ACCOUNT_ID}.r2.cloudflarestorage.com/maktaba-plugins-private/${key}`);
  url.searchParams.set("X-Amz-Expires", "3600");
  const signed = await aws.sign(new Request(url, { method: "PUT" }), { aws: { signQuery: true } });
  return signed.url;
}

/** PUT /v1/uploads/{token}: a local store's upload (UPLOADS = "worker" only). */
export async function workerUpload(env: Env, req: Request, t: string): Promise<Response> {
  if (env.UPLOADS !== "worker") refuse(404, "not_found");
  const key = `upload-tokens/${await sha256hex(t)}`;
  const meta = await env.PRIVATE.get(key);
  if (!meta) refuse(410, "upload_expired");
  const m = JSON.parse(await meta!.text()) as { key: string; max: number; expires: number };
  await env.PRIVATE.delete(key);
  if (m.expires < now().getTime()) refuse(410, "upload_expired");
  const data = new Uint8Array(await req.arrayBuffer());
  if (data.length > m.max) refuse(413, "upload_too_large");
  await env.PRIVATE.put(m.key, data);
  return json({ status: "stored" });
}

interface Plugin {
  id: string;
  developer: string;
  status: string;
}

/** The plugin, refusing (and auditing) a developer who does not own it (§8, T01). */
export async function ownPlugin(ctx: Ctx, id: string, account: string): Promise<Plugin | null> {
  const p = await ctx.env.DB_CATALOG.prepare("SELECT id, developer, status FROM plugins WHERE id = ?").bind(id).first<Plugin>();
  if (p && p.developer !== account) {
    await audit(ctx, account, "plugin.access", id, "forbidden");
    refuse(403, "forbidden");
  }
  return p;
}

/** POST /v1/submissions {plugin_id, version, package_sha256, package_size, source_size, notes_en, notes_ar}. */
export async function startSubmission(ctx: Ctx): Promise<Response> {
  const a = developer(ctx);
  reauthed(ctx);
  if (ctx.env.SUBMISSIONS_OPEN !== "true") refuse(403, "submissions_closed");
  const b = await body(ctx.req);
  const id = str(b.plugin_id, "plugin_id", 128);
  const version = str(b.version, "version", 64);
  if (!ID.test(id)) refuse(400, "plugin_id_invalid");
  if (!VERSION.test(version)) refuse(400, "version_invalid");
  const sha = str(b.package_sha256, "package_sha256", 64);
  if (!/^[0-9a-f]{64}$/.test(sha)) refuse(400, "field_invalid", { field: "package_sha256" });
  const size = Number(b.package_size);
  const sourceSize = Number(b.source_size);
  if (!Number.isInteger(size) || size <= 0 || size > PACKAGE_MAX) refuse(400, "package_too_large", { max: PACKAGE_MAX });
  // the source archive is required for every version (54.16 C10)
  if (!Number.isInteger(sourceSize) || sourceSize <= 0 || sourceSize > SOURCE_MAX) refuse(400, "source_required", { max: SOURCE_MAX });
  const dev = await ctx.env.DB_CATALOG.prepare("SELECT agreement_version, status FROM developers WHERE account = ?")
    .bind(a.id)
    .first<{ agreement_version: number; status: string }>();
  if (!dev || dev.status !== "active") refuse(403, "not_developer");
  if (dev!.agreement_version !== Number(ctx.env.AGREEMENT_VERSION))
    refuse(403, "agreement_required", { current: Number(ctx.env.AGREEMENT_VERSION) });
  const key = await ctx.env.DB_CATALOG.prepare(
    "SELECT id FROM developer_keys WHERE account = ? AND revoked_at IS NULL AND suspended_at IS NULL LIMIT 1",
  )
    .bind(a.id)
    .first();
  if (!key) refuse(403, "key_required");
  const prefixes = await ctx.env.DB_CATALOG.prepare(
    "SELECT prefix FROM developer_prefixes WHERE account = ? AND verified_at IS NOT NULL AND lapsed_at IS NULL",
  )
    .bind(a.id)
    .all<{ prefix: string }>();
  if (!prefixes.results.some((p) => id.startsWith(p.prefix + "."))) refuse(403, "prefix_unverified");
  const existing = await ownPlugin(ctx, id, a.id);
  if (existing?.status === "removed") refuse(403, "plugin_removed");
  const v = await ctx.env.DB_CATALOG.prepare("SELECT status FROM versions WHERE plugin = ? AND version = ?")
    .bind(id, version)
    .first<{ status: string }>();
  if (v) refuse(409, "version_exists");
  const sid = newId("s");
  const at = iso(now());
  const pkgKey = `submissions/${sid}/package.mkplugin`;
  const srcKey = `submissions/${sid}/source.tar.zst`;
  await ctx.env.DB_CATALOG.batch([
    ctx.env.DB_CATALOG.prepare("INSERT OR IGNORE INTO plugins (id, developer, developer_name, created) VALUES (?, ?, ?, ?)").bind(
      id,
      a.id,
      a.display_name,
      at,
    ),
    ctx.env.DB_CATALOG.prepare("INSERT INTO versions (plugin, version, status, notes_en, notes_ar) VALUES (?, ?, 'submitted', ?, ?)").bind(
      id,
      version,
      optStr(b.notes_en, "notes_en", 4000) ?? "",
      optStr(b.notes_ar, "notes_ar", 4000) ?? "",
    ),
    ctx.env.DB_CATALOG.prepare(
      `INSERT INTO submissions (id, plugin, version, uploaded_by, package_key, source_key, package_sha256, package_size, source_size, created, first_version)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOT EXISTS (SELECT 1 FROM versions WHERE plugin = ? AND status = 'approved'))`,
    ).bind(sid, id, version, a.id, pkgKey, srcKey, sha, size, sourceSize, at, id),
  ]);
  await audit(ctx, a.id, "submission.start", `${id}@${version}`);
  const origin = ctx.env.STORE_ORIGIN;
  return json(
    {
      id: sid,
      upload: { package: await uploadURL(ctx.env, origin, pkgKey, "package"), source: await uploadURL(ctx.env, origin, srcKey, "source") },
    },
    201,
  );
}

async function digest(obj: R2ObjectBody): Promise<string> {
  const d = new crypto.DigestStream("SHA-256");
  await obj.body.pipeTo(d);
  return [...new Uint8Array(await d.digest)].map((x) => x.toString(16).padStart(2, "0")).join("");
}

/** POST /v1/submissions/{id}/uploaded: the uploads are checked and the checks queued (54.16). */
export async function uploaded(ctx: Ctx, sid: string): Promise<Response> {
  const a = developer(ctx);
  const s = await ctx.env.DB_CATALOG.prepare("SELECT * FROM submissions WHERE id = ?").bind(sid).first<{
    uploaded_by: string;
    status: string;
    package_key: string;
    source_key: string;
    package_sha256: string;
    package_size: number;
    plugin: string;
    version: string;
  }>();
  if (!s || s.uploaded_by !== a.id) {
    await audit(ctx, a.id, "submission.uploaded", sid, "forbidden");
    refuse(s ? 403 : 404, s ? "forbidden" : "not_found");
  }
  if (s!.status !== "uploading") refuse(409, "submission_state", { status: s!.status });
  const pkg = await ctx.env.PRIVATE.get(s!.package_key);
  const src = await ctx.env.PRIVATE.head(s!.source_key);
  if (!pkg || !src) refuse(400, "upload_missing", { package: !!pkg, source: !!src });
  if (pkg!.size !== s!.package_size || src!.size > SOURCE_MAX) refuse(400, "upload_size");
  if ((await digest(pkg!)) !== s!.package_sha256) refuse(400, "upload_digest");
  await ctx.env.DB_CATALOG.batch([
    ctx.env.DB_CATALOG.prepare("UPDATE submissions SET status = 'checking' WHERE id = ?").bind(sid),
    ctx.env.DB_CATALOG.prepare("UPDATE versions SET status = 'checking' WHERE plugin = ? AND version = ?").bind(s!.plugin, s!.version),
  ]);
  await ctx.env.Q_CHECKS.send({ submission: sid });
  await audit(ctx, a.id, "submission.uploaded", sid);
  return json({ id: sid, status: "checking" });
}

/** GET /v1/submissions/{id}: the developer's own, or a reviewer's. */
export async function getSubmission(ctx: Ctx, sid: string): Promise<Response> {
  const a = verified(ctx);
  const s = await ctx.env.DB_CATALOG.prepare(
    "SELECT id, plugin, version, uploaded_by, status, flags, decision, reasons, notes, report_key, created, checked_at, decided_at FROM submissions WHERE id = ?",
  )
    .bind(sid)
    .first<Record<string, unknown> & { uploaded_by: string; report_key: string }>();
  const staff = ctx.roles.has("store_reviewer") || ctx.roles.has("store_admin");
  if (!s || (s.uploaded_by !== a.id && !(staff && ctx.session!.mfa_kind === "passkey"))) {
    if (s) await audit(ctx, a.id, "submission.read", sid, "forbidden");
    refuse(s ? 403 : 404, s ? "forbidden" : "not_found");
  }
  let report: unknown = null;
  if (s!.report_key) report = JSON.parse((await (await ctx.env.PRIVATE.get(s!.report_key))?.text()) ?? "null");
  return json({ ...s, report });
}

/** POST /v1/plugins/{id}/withdraw: the developer delists their own plugin. */
export async function withdraw(ctx: Ctx, id: string): Promise<Response> {
  const a = developer(ctx);
  reauthed(ctx);
  const p = await ownPlugin(ctx, id, a.id);
  if (!p) refuse(404, "not_found");
  await ctx.env.DB_CATALOG.prepare("UPDATE plugins SET status = 'delisted' WHERE id = ? AND status IN ('draft', 'listed')").bind(id).run();
  await ctx.env.Q_CATALOG.send({ kind: "regenerate", reason: `withdrawn ${id}` });
  await audit(ctx, a.id, "plugin.withdraw", id);
  return json({ status: "delisted" });
}
