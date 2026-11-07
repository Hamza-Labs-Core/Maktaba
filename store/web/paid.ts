// Paid plugins' packages (story 54.19 §6): never on the public CDN. A paid plugin's package
// is kept in the private bucket (paid/<sha256>.mkplugin), the catalog names
// plugins-cdn.maktaba.tv/paid/<sha256>, and a home downloads it with a signed URL the store
// issues only against a valid entitlement: POST /v1/homes/{id}/downloads, signed by the home,
// answers .../paid/<sha256>?t=<token>, where the token binds the home, the sha256 and an
// expiry 10 minutes ahead, under the store's secret PAID_URL_KEY. The same Worker serves
// plugins-cdn.maktaba.tv/paid/* (a route on the CDN's host): it checks the token, the path's
// sha256 and the home's id (X-Maktaba-Home), then streams the file. An expired, forged or
// another home's token, or another file's, is 403.

import { holds } from "../commerce/entitlements";
import { hmacSha256, safeEqual, utf8 } from "../payments/crypto";
import { b64url, fromB64url } from "./crypto";
import { type Env, now } from "./env";
import { json, refuse } from "./http";
import { signedHome } from "./homes";
import { commerceDB } from "./payments";

/** How long a download URL lasts. */
export const PAID_URL_MS = 10 * 60_000;
/** The header a home names itself with when it downloads (bound in the token). */
export const HOME_HEADER = "X-Maktaba-Home";
const SHA = /^[0-9a-f]{64}$/;
const HOME = /^[\w-]{1,64}$/;
/** A token's two parts: base64url, nothing else. */
const B64URL = /^[A-Za-z0-9_-]{1,400}$/;
/** A token's payload: home id, sha256, expiry (unix seconds), ASCII only. */
const PAYLOAD = /^[\w-]{1,64}\.[0-9a-f]{64}\.\d{1,12}$/;

/** The private object of a paid package. */
export const paidKey = (sha: string) => `paid/${sha}.mkplugin`;

/** Whether the store sells a plugin (an active product in store-commerce). */
export async function isPaidPlugin(env: Env, plugin: string): Promise<boolean> {
  const db = commerceDB(env);
  if (!db) return false;
  const r = await db.prepare("SELECT 1 AS x FROM products WHERE plugin_id = ? AND active = 1").bind(plugin).first<{ x: number }>();
  return !!r;
}

async function mac(secret: string, payload: string): Promise<string> {
  return b64url(await hmacSha256(utf8(secret), `maktaba-paid/v1\n${payload}`));
}

/** A download token: the home, the sha256 and the expiry (unix seconds), MACed. */
export async function paidToken(secret: string, homeId: string, sha: string, expires: Date): Promise<string> {
  const payload = `${homeId}.${sha}.${Math.floor(expires.getTime() / 1000)}`;
  return `${b64url(utf8(payload))}.${await mac(secret, payload)}`;
}

/** The token's home when it is this store's, for this file, and not expired; else null. */
export async function checkPaidToken(secret: string, token: string, sha: string, at: Date): Promise<string | null> {
  if (token.length > 512) return null;
  const parts = token.split(".");
  if (parts.length !== 2 || !B64URL.test(parts[0] ?? "") || !B64URL.test(parts[1] ?? "")) return null;
  let payload: string;
  try {
    payload = new TextDecoder().decode(fromB64url(parts[0]!));
  } catch {
    return null;
  }
  // strict: the payload is ASCII only (the home id, the sha256, the expiry); a byte that is
  // not valid UTF-8 decodes to U+FFFD and fails here, before the MAC is even compared
  if (!PAYLOAD.test(payload)) return null;
  if (!safeEqual(parts[1]!, await mac(secret, payload))) return null;
  const [home, s, exp, ...rest] = payload.split(".");
  if (rest.length || !home || !HOME.test(home) || s !== sha || !/^\d{1,12}$/.test(exp ?? "")) return null;
  if (Number(exp) * 1000 <= at.getTime()) return null;
  return home;
}

/** POST /v1/homes/{id}/downloads {plugin_id, sha256}: a paid package's download URL. */
export async function paidDownloadURL(env: Env, req: Request, id: string): Promise<Response> {
  const raw = new Uint8Array(await req.arrayBuffer());
  if (raw.length > 4096) refuse(413, "body_too_large");
  const home = await signedHome(env, req, raw);
  if (!home || home.id !== id) refuse(401, "home_signature_invalid");
  const db = commerceDB(env);
  if (!db || !env.PAID_URL_KEY) refuse(503, "paid_downloads_unavailable");
  let b: { plugin_id?: unknown; sha256?: unknown };
  try {
    b = JSON.parse(new TextDecoder().decode(raw)) as typeof b;
  } catch {
    return refuse(400, "body_not_json");
  }
  const plugin =
    typeof b.plugin_id === "string" && /^[a-z0-9][a-z0-9.-]{0,127}$/.test(b.plugin_id)
      ? b.plugin_id
      : refuse(400, "field_invalid", { field: "plugin_id" });
  const sha = typeof b.sha256 === "string" && SHA.test(b.sha256) ? b.sha256 : refuse(400, "field_invalid", { field: "sha256" });
  // a version of this plugin the store published
  const v = await env.DB_CATALOG.prepare("SELECT 1 AS x FROM versions WHERE plugin = ? AND sha256 = ? AND status = 'approved'")
    .bind(plugin, sha)
    .first<{ x: number }>();
  if (!v) refuse(404, "version_not_found");
  const at = now();
  if (!(await holds(db!, home!.id, plugin, at))) refuse(403, "entitlement_missing");
  const expires = new Date(at.getTime() + PAID_URL_MS);
  const t = await paidToken(env.PAID_URL_KEY!, home!.id, sha, expires);
  return json({ url: `${env.CDN_ORIGIN}/paid/${sha}?t=${encodeURIComponent(t)}`, expires_at: expires.toISOString() });
}

const forbidden = () =>
  new Response("forbidden", { status: 403, headers: { "Cache-Control": "no-store", "Content-Type": "text/plain; charset=utf-8" } });

/**
 * GET plugins-cdn.maktaba.tv/paid/<sha256>?t=…: the package, for the home the token names
 * only, while it lasts. Never cached by the CDN.
 */
export async function paidPackage(env: Env, req: Request): Promise<Response> {
  const url = new URL(req.url);
  const m = /^\/paid\/([0-9a-f]{64})$/.exec(url.pathname);
  if (!m || (req.method !== "GET" && req.method !== "HEAD")) return new Response("not found", { status: 404 });
  const sha = m[1]!;
  if (!env.PAID_URL_KEY) return new Response("unavailable", { status: 503 });
  const t = url.searchParams.get("t") ?? "";
  const home = await checkPaidToken(env.PAID_URL_KEY, t, sha, now());
  if (!home || req.headers.get(HOME_HEADER) !== home) return forbidden();
  // a package published before its plugin was priced stays where it was put
  const obj = (await env.PRIVATE.get(paidKey(sha))) ?? (await env.BUCKET.get(`${sha}.mkplugin`));
  if (!obj) return new Response("not found", { status: 404, headers: { "Cache-Control": "no-store" } });
  return new Response(req.method === "HEAD" ? null : obj.body, {
    headers: {
      "Content-Type": "application/vnd.maktaba.plugin",
      "Content-Length": String(obj.size),
      "Cache-Control": "private, no-store",
      "X-Content-Type-Options": "nosniff",
    },
  });
}
