// Linking a home to a store account (story 54.15 §5): the device-authorisation shape of
// RFC 8628. The home keeps an Ed25519 home store key; afterwards it signs each request with
// it (`Authorization: MKS-Home id=…, ts=…, nonce=…, sig=…`), ±5 minutes, each nonce once.
// There is no bearer secret to leak. The store's home id is its own, unrelated to any cloud id.
import { type Ctx, audit, limit, reauthed, signedIn, verified } from "./auth";
import { newId, randomBytes, sha256hex } from "./crypto";
import { type Env, iso, now } from "./env";
import { body, clientIP, json, optStr, refuse, str } from "./http";
import { fromB64 } from "../signing/sig";

// no look-alike letters or digits (0/O, 1/I/L, 5/S, 8/B, U/V)
const ALPHABET = "ACDEFGHJKMNPQRTWXY234679";
const CODE_TTL = 600_000;
export const MAX_HOMES = 10;
const SKEW = 5 * 60_000;

function randomCode(n: number): string {
  const b = randomBytes(n);
  return Array.from(b, (x) => ALPHABET[x % ALPHABET.length]).join("");
}

export function normaliseCode(c: string): string {
  return c.toUpperCase().replace(/[^A-Z0-9]/g, "");
}

/** POST /v1/homes/pair {public_key, server_version, platform, name}: a code to show. */
export async function pair(ctx: Ctx): Promise<Response> {
  await limit(ctx, "pair", 20, 3600_000);
  const b = await body(ctx.req);
  const pub = str(b.public_key, "public_key", 64);
  let raw: Uint8Array;
  try {
    raw = fromB64(pub);
  } catch {
    return refuse(400, "key_invalid");
  }
  if (raw.length !== 32) refuse(400, "key_invalid");
  const at = now().getTime();
  for (let tries = 0; tries < 8; tries++) {
    const head = randomCode(4);
    const rest = randomCode(4);
    const device = `${head}.${[...randomBytes(24)].map((x) => x.toString(16).padStart(2, "0")).join("")}`;
    const ok = await ctx.env.PAIRING_CODE.get(ctx.env.PAIRING_CODE.idFromName(head)).create(
      {
        rest,
        deviceHash: await sha256hex(device),
        publicKey: pub,
        name: optStr(b.name, "name", 80) ?? "",
        serverVersion: optStr(b.server_version, "server_version", 40) ?? "",
        platform: optStr(b.platform, "platform", 40) ?? "",
        expires: at + CODE_TTL,
      },
      at,
    );
    if (ok) {
      return json({
        device_code: device,
        user_code: `${head}-${rest}`,
        verification_uri: `${ctx.env.STORE_ORIGIN}/link`,
        expires_in: CODE_TTL / 1000,
        interval: 5,
      });
    }
  }
  return refuse(503, "try_again");
}

/** POST /v1/homes/pair/poll {device_code}: pending, the home id, or 410 code_expired. */
export async function poll(ctx: Ctx): Promise<Response> {
  const b = await body(ctx.req);
  const device = str(b.device_code, "device_code", 100);
  const head = device.split(".")[0] ?? "";
  if (!/^[A-Z0-9]{4}$/.test(head)) refuse(400, "field_invalid", { field: "device_code" });
  const r = await ctx.env.PAIRING_CODE.get(ctx.env.PAIRING_CODE.idFromName(head)).poll(await sha256hex(device), now().getTime());
  if (r.status === "expired") refuse(410, "code_expired");
  return json(r.status === "linked" ? { status: "linked", home_id: r.homeId } : { status: "pending" });
}

/** POST /v1/homes/link {user_code}: the signed-in admin confirms on /link (re-auth applies). */
export async function link(ctx: Ctx): Promise<Response> {
  const a = verified(ctx);
  reauthed(ctx);
  await limit(ctx, "link", 20, 3600_000);
  const b = await body(ctx.req);
  const code = normaliseCode(str(b.user_code, "user_code", 20));
  if (code.length !== 8) refuse(400, "code_wrong");
  const stub = ctx.env.PAIRING_CODE.get(ctx.env.PAIRING_CODE.idFromName(code.slice(0, 4)));
  const r = await stub.confirm(code.slice(4), now().getTime());
  if (!r.ok) {
    await audit(ctx, a.id, "home.link", code.slice(0, 4), r.code);
    refuse(r.code === "code_expired" ? 410 : 400, r.code, r.left !== undefined ? { left: r.left } : {});
  }
  const home = (r as { ok: true; home: { publicKey: string; name: string; serverVersion: string; platform: string } }).home;
  if (b.confirm !== true) {
    // the first call shows what is being linked; the second, with confirm, links it
    return json({ status: "confirm", name: home.name, server_version: home.serverVersion, platform: home.platform });
  }
  const n = await ctx.env.DB_INSTALLS.prepare("SELECT count(*) AS n FROM homes WHERE account = ? AND unlinked_at IS NULL")
    .bind(a.id)
    .first<{ n: number }>();
  if ((n?.n ?? 0) >= MAX_HOMES) refuse(409, "too_many_homes", { max: MAX_HOMES });
  const id = newId("h");
  const at = iso(now());
  // a home links to one account at a time: the same key linked elsewhere is unlinked
  await ctx.env.DB_INSTALLS.batch([
    ctx.env.DB_INSTALLS.prepare("UPDATE homes SET unlinked_at = ? WHERE public_key = ? AND unlinked_at IS NULL").bind(at, home.publicKey),
    ctx.env.DB_INSTALLS.prepare(
      "INSERT INTO homes (id, account, public_key, name, server_version, platform, linked_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
    ).bind(id, a.id, home.publicKey, home.name, home.serverVersion, home.platform, at),
  ]);
  await stub.complete(a.id, id);
  await audit(ctx, a.id, "home.link", id);
  return json({ status: "linked", home_id: id, name: home.name });
}

/** GET /v1/homes: the account's linked homes. */
export async function listHomes(ctx: Ctx): Promise<Response> {
  const a = signedIn(ctx);
  const r = await ctx.env.DB_INSTALLS.prepare(
    "SELECT id, name, server_version, platform, linked_at FROM homes WHERE account = ? AND unlinked_at IS NULL ORDER BY linked_at",
  )
    .bind(a.id)
    .all();
  return json({ homes: r.results, max: MAX_HOMES });
}

export interface Home {
  id: string;
  account: string;
  public_key: string;
}

/**
 * The home a request is signed by (54.15 §5), or null when it carries no MKS-Home header.
 * A bad, old or replayed signature is 401 (T05).
 */
export async function signedHome(env: Env, req: Request, raw: Uint8Array): Promise<Home | null> {
  const h = req.headers.get("Authorization");
  if (!h || !h.startsWith("MKS-Home ")) return null;
  const parts = Object.fromEntries(
    h
      .slice(9)
      .split(",")
      .map((p) => {
        // split at the first "=" only: base64 values end in "="
        const t = p.trim();
        const i = t.indexOf("=");
        return i > 0 ? ([t.slice(0, i), t.slice(i + 1)] as [string, string]) : null;
      })
      .filter((p): p is [string, string] => p !== null),
  );
  const { id, ts, nonce, sig } = parts as Record<string, string | undefined>;
  if (!id || !ts || !nonce || !sig || !/^\d+$/.test(ts) || !/^[A-Za-z0-9_-]{16,64}$/.test(nonce)) refuse(401, "home_signature_invalid");
  const at = now().getTime();
  if (Math.abs(at - Number(ts) * 1000) > SKEW) refuse(401, "home_signature_stale");
  const home = await env.DB_INSTALLS.prepare("SELECT id, account, public_key FROM homes WHERE id = ? AND unlinked_at IS NULL")
    .bind(id)
    .first<Home>();
  if (!home) refuse(401, "home_unknown");
  const url = new URL(req.url);
  const msg = `MKS-Home\n${req.method}\n${url.pathname}\n${ts}\n${nonce}\n${await sha256hex(raw)}`;
  let ok = false;
  try {
    const key = await crypto.subtle.importKey("raw", fromB64(home!.public_key) as BufferSource, { name: "Ed25519" }, false, ["verify"]);
    ok = await crypto.subtle.verify({ name: "Ed25519" }, key, fromB64(sig!) as BufferSource, new TextEncoder().encode(msg) as BufferSource);
  } catch {
    ok = false;
  }
  if (!ok) refuse(401, "home_signature_invalid");
  const fresh = await env.IP_BUCKET.get(env.IP_BUCKET.idFromName(`home:${home!.id}`)).nonce(nonce!, 2 * SKEW, at);
  if (!fresh) refuse(401, "home_signature_replayed");
  return home;
}

/** POST /v1/homes/{id}/seen {server_version, platform, plugins: [{id, version}]} (signed). */
export async function seen(env: Env, req: Request, id: string): Promise<Response> {
  const raw = new Uint8Array(await req.arrayBuffer());
  if (raw.length > 64 << 10) refuse(413, "body_too_large");
  const home = await signedHome(env, req, raw);
  if (!home || home.id !== id) refuse(401, "home_signature_invalid");
  let b: { server_version?: string; platform?: string; plugins?: { id: string; version: string }[] };
  try {
    b = JSON.parse(new TextDecoder().decode(raw));
  } catch {
    return refuse(400, "body_not_json");
  }
  const list = (b.plugins ?? [])
    .slice(0, 500)
    .filter((p) => typeof p?.id === "string" && typeof p?.version === "string" && p.id.length <= 128 && p.version.length <= 64);
  const at = iso(now());
  const stmts = [
    env.DB_INSTALLS.prepare("UPDATE homes SET server_version = ?, platform = ? WHERE id = ?").bind(
      String(b.server_version ?? "").slice(0, 40),
      String(b.platform ?? "").slice(0, 40),
      id,
    ),
    ...list.map((p) =>
      env.DB_INSTALLS.prepare(
        "INSERT INTO installs (home, plugin, version, first_seen, last_seen) VALUES (?, ?, ?, ?, ?) ON CONFLICT (home, plugin) DO UPDATE SET version = excluded.version, last_seen = excluded.last_seen",
      ).bind(id, p.id, p.version, at, at),
    ),
    env.DB_INSTALLS.prepare(`DELETE FROM installs WHERE home = ? AND plugin NOT IN (${list.map(() => "?").join(",") || "''"})`).bind(
      id,
      ...list.map((p) => p.id),
    ),
  ];
  await env.DB_INSTALLS.batch(stmts);
  return json({ status: "seen", plugins: list.length });
}

/** DELETE /v1/homes/{id}: by the account (re-auth) or by the home itself (signed). */
export async function unlink(ctx: Ctx, id: string): Promise<Response> {
  const raw = new Uint8Array(await ctx.req.clone().arrayBuffer());
  const home = await signedHome(ctx.env, ctx.req, raw);
  let actor: string;
  if (home) {
    if (home.id !== id) refuse(403, "forbidden");
    actor = `home:${id}`;
  } else {
    const a = signedIn(ctx);
    reauthed(ctx);
    const owns = await ctx.env.DB_INSTALLS.prepare("SELECT 1 FROM homes WHERE id = ? AND account = ? AND unlinked_at IS NULL")
      .bind(id, a.id)
      .first();
    if (!owns) refuse(404, "not_found");
    actor = a.id;
  }
  await ctx.env.DB_INSTALLS.prepare("UPDATE homes SET unlinked_at = ? WHERE id = ? AND unlinked_at IS NULL").bind(iso(now()), id).run();
  await audit({ env: ctx.env, ipPrefix: clientIP(ctx.req).prefix }, actor, "home.unlink", id);
  return json({ status: "unlinked" });
}
