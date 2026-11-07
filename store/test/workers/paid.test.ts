// Story 54.19 §6, T09: a paid plugin's package is never on the public CDN. The catalog names
// plugins-cdn.maktaba.tv/paid/<sha256>; a home gets a download URL, signed by the store for it
// and that file for 10 minutes, only while it holds a valid entitlement; the CDN route streams
// the file to that home and answers 403 to an expired, forged or another home's token.
import { env, exports } from "cloudflare:workers";
import { describe, expect, it } from "vitest";
import { Root, emptyState } from "../../signing/verify";
import { b64url, sha256hex } from "../../web/crypto";
import { HOME_HEADER, paidKey, paidToken } from "../../web/paid";
import { countersign } from "../../web/publish";
import { E, account, buildPackage, call, developer, submit } from "./helpers";

const CDN = "https://plugins-cdn.maktaba.tv";
const db = () => (env as unknown as { DB_COMMERCE: D1Database }).DB_COMMERCE;
const writer = () => E.CATALOG_WRITER.get(E.CATALOG_WRITER.idFromName("main"));
const keys = async () => (await Root.parse(E.PLUGINS_ROOT)).verifyStoreKeys(E.TEST_STORE_KEYS, emptyState(), new Date());
const KEY = (env as unknown as { PAID_URL_KEY: string }).PAID_URL_KEY;

async function homeKey() {
  const k = (await crypto.subtle.generateKey({ name: "Ed25519" }, true, ["sign", "verify"])) as CryptoKeyPair;
  const raw = new Uint8Array((await crypto.subtle.exportKey("raw", k.publicKey)) as ArrayBuffer);
  return { k, public: btoa(String.fromCharCode(...raw)) };
}

async function signed(key: CryptoKey, homeId: string, method: string, path: string, body: string) {
  const ts = Math.floor(Date.now() / 1000);
  const nonce = b64url(crypto.getRandomValues(new Uint8Array(16)));
  const msg = `MKS-Home\n${method}\n${path}\n${ts}\n${nonce}\n${await sha256hex(body)}`;
  const sig = new Uint8Array(await crypto.subtle.sign({ name: "Ed25519" }, key, new TextEncoder().encode(msg)));
  return { Authorization: `MKS-Home id=${homeId}, ts=${ts}, nonce=${nonce}, sig=${btoa(String.fromCharCode(...sig))}` };
}

async function linkedHome() {
  const buyer = await account();
  const hk = await homeKey();
  const p = await call("POST", "/v1/homes/pair", { body: { public_key: hk.public, name: "Den" } });
  const ok = await call("POST", "/v1/homes/link", { cookie: buyer.cookie, body: { user_code: p.body.user_code, confirm: true } });
  expect(ok.status).toBe(200);
  return { hk, homeId: ok.body.home_id as string };
}

async function entitle(homeId: string, plugin: string) {
  const at = new Date(Date.now() - 86_400_000).toISOString();
  const id = `pur-${homeId}-${plugin}`;
  await db().batch([
    db()
      .prepare(
        `INSERT INTO purchases (id, item, account_id, home_id, plugin_id, product, developer_id, provider_id, currency,
           amount_minor, maktaba_minor, developer_minor, status, created_at, paid_at)
         VALUES (?, 'plugin', 'acct', ?, ?, 'one_time', 'dev', 'fake', 'USD', 1000, 150, 850, 'paid', ?, ?)`,
      )
      .bind(id, homeId, plugin, at, at),
    db()
      .prepare(`INSERT INTO entitlements (purchase_id, home_id, plugin_id, product) VALUES (?, ?, ?, 'one_time')`)
      .bind(id, homeId, plugin),
  ]);
}

async function askURL(hk: { k: CryptoKeyPair }, homeId: string, plugin: string, sha: string) {
  const path = `/v1/homes/${homeId}/downloads`;
  const body = JSON.stringify({ plugin_id: plugin, sha256: sha });
  return call("POST", path, {
    raw: body,
    headers: { ...(await signed(hk.k.privateKey, homeId, "POST", path, body)), "Content-Type": "application/json" },
  });
}

async function cdn(url: string, home?: string) {
  const h = new Headers();
  if (home) h.set(HOME_HEADER, home);
  const r = await (exports as unknown as { default: Fetcher }).default.fetch(url, { headers: h, redirect: "manual" });
  return { status: r.status, headers: r.headers, bytes: new Uint8Array(await r.arrayBuffer()) };
}

describe("a paid plugin's package", () => {
  it("is private, named /paid/ in the catalog, and streamed only by a valid, unexpired URL to its home", async () => {
    const dev = await developer();
    const id = `${dev.prefix}.paidpkg`;
    await db().prepare("INSERT INTO products (plugin_id, kind, developer_id, active) VALUES (?, 'one_time', 'dev-paid', 1)").bind(id).run();
    await db().prepare("INSERT INTO prices (plugin_id, kind, currency, amount_minor) VALUES (?, 'one_time', 'USD', 900)").bind(id).run();
    const sid = await submit(dev, id, "1.0.0", await buildPackage(id, "1.0.0", dev.key));
    const out = await countersign(env as never, sid, "reviewed");
    // never public
    expect(await E.BUCKET.head(`${out.sha256}.mkplugin`)).toBeNull();
    const priv = await E.PRIVATE.get(paidKey(out.sha256));
    expect(priv).not.toBeNull();
    const file = new Uint8Array(await priv!.arrayBuffer());

    await writer().regenerate();
    const cat = await (await keys()).verifyCatalog(await (await E.BUCKET.get("catalog-1.json"))!.text(), 1, emptyState(), new Date());
    const p = (cat.signed.plugins as { id: string; versions: { url: string }[] }[]).find((x) => x.id === id)!;
    expect(p.versions[0]!.url).toBe(`${CDN}/paid/${out.sha256}`);

    const a = await linkedHome();
    const b = await linkedHome();
    // no entitlement: no URL
    const none = await askURL(a.hk, a.homeId, id, out.sha256);
    expect(none.status).toBe(403);
    expect(none.body.error).toBe("entitlement_missing");
    // another home's signature for this home's id
    const path = `/v1/homes/${a.homeId}/downloads`;
    const body = JSON.stringify({ plugin_id: id, sha256: out.sha256 });
    expect((await call("POST", path, { raw: body, headers: await signed(b.hk.k.privateKey, a.homeId, "POST", path, body) })).status).toBe(
      401,
    );
    // a file that is not this plugin's
    expect((await askURL(a.hk, a.homeId, id, "0".repeat(64))).status).toBe(404);

    await entitle(a.homeId, id);
    const ok = await askURL(a.hk, a.homeId, id, out.sha256);
    expect(ok.status, JSON.stringify(ok.body)).toBe(200);
    const url = String(ok.body.url);
    expect(url).toMatch(new RegExp(`^${CDN}/paid/${out.sha256}\\?t=`));
    expect(Date.parse(String(ok.body.expires_at)) - Date.now()).toBeLessThanOrEqual(10 * 60_000);

    // the home named in the token gets the file, never cached
    const got = await cdn(url, a.homeId);
    expect(got.status).toBe(200);
    expect(got.bytes).toEqual(file);
    expect(got.headers.get("Cache-Control")).toContain("no-store");

    // 403: no home, another home, a forged token, an expired one, another file's path, no token
    expect((await cdn(url)).status).toBe(403);
    expect((await cdn(url, b.homeId)).status).toBe(403);
    const t = new URL(url).searchParams.get("t")!;
    const [payload] = t.split(".");
    expect((await cdn(`${CDN}/paid/${out.sha256}?t=${payload}.${"A".repeat(43)}`, a.homeId)).status).toBe(403);
    const forgedPayload = b64url(new TextEncoder().encode(`${b.homeId}.${out.sha256}.${Math.floor(Date.now() / 1000) + 600}`));
    expect((await cdn(`${CDN}/paid/${out.sha256}?t=${forgedPayload}.${t.split(".")[1]}`, b.homeId)).status).toBe(403);
    const late = await paidToken(KEY, a.homeId, out.sha256, new Date(Date.now() - 1000));
    expect((await cdn(`${CDN}/paid/${out.sha256}?t=${encodeURIComponent(late)}`, a.homeId)).status).toBe(403);
    expect((await cdn(`${CDN}/paid/${"f".repeat(64)}?t=${encodeURIComponent(t)}`, a.homeId)).status).toBe(403);
    expect((await cdn(`${CDN}/paid/${out.sha256}`, a.homeId)).status).toBe(403);
    // a payload that is not UTF-8, or a part outside base64url
    const notUtf8 = b64url(new Uint8Array([0xff, 0xfe, 0x2e, 0x61]));
    expect((await cdn(`${CDN}/paid/${out.sha256}?t=${notUtf8}.${t.split(".")[1]}`, a.homeId)).status).toBe(403);
    expect((await cdn(`${CDN}/paid/${out.sha256}?t=${encodeURIComponent(`${payload}+/=.${t.split(".")[1]}`)}`, a.homeId)).status).toBe(403);
    // the rest of the CDN's host is not this Worker's
    expect((await cdn(`${CDN}/${out.sha256}.mkplugin`, a.homeId)).status).toBe(404);
  });
});
