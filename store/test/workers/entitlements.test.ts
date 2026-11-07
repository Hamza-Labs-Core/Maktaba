// Story 54.19 §6 (the store's half of T03/T04): a linked home asks for its entitlements with
// its own signature and gets each paid, unended purchase signed by the entitlement key E,
// bound to its home id and its store key; another home's signature gets nothing of it.
import { env } from "cloudflare:test";
import { describe, expect, it } from "vitest";
import { b64url, sha256hex } from "../../web/crypto";
import { KIND, Signer, fromB64, verifySig } from "../../signing/sig";
import { account, call } from "./helpers";

async function homeKey() {
  const k = (await crypto.subtle.generateKey({ name: "Ed25519" }, true, ["sign", "verify"])) as CryptoKeyPair;
  const raw = new Uint8Array((await crypto.subtle.exportKey("raw", k.publicKey)) as ArrayBuffer);
  return { k, raw, public: btoa(String.fromCharCode(...raw)) };
}

async function signed(key: CryptoKey, homeId: string, method: string, path: string, body: string) {
  const ts = Math.floor(Date.now() / 1000);
  const nonce = b64url(crypto.getRandomValues(new Uint8Array(16)));
  const msg = `MKS-Home\n${method}\n${path}\n${ts}\n${nonce}\n${await sha256hex(body)}`;
  const sig = new Uint8Array(await crypto.subtle.sign({ name: "Ed25519" }, key, new TextEncoder().encode(msg)));
  return { Authorization: `MKS-Home id=${homeId}, ts=${ts}, nonce=${nonce}, sig=${btoa(String.fromCharCode(...sig))}` };
}

async function linkedHome() {
  const admin = await account();
  const hk = await homeKey();
  const p = await call("POST", "/v1/homes/pair", { body: { public_key: hk.public, name: "Den" } });
  const ok = await call("POST", "/v1/homes/link", { cookie: admin.cookie, body: { user_code: p.body.user_code, confirm: true } });
  expect(ok.status).toBe(200);
  return { hk, homeId: ok.body.home_id as string };
}

const db = () => (env as unknown as { DB_COMMERCE: D1Database }).DB_COMMERCE;

async function paidPurchase(id: string, homeId: string, plugin: string, status = "paid") {
  const at = new Date(Date.now() - 86_400_000).toISOString();
  await db()
    .prepare(
      `INSERT INTO purchases (id, item, account_id, home_id, plugin_id, product, developer_id, provider_id, currency,
         amount_minor, maktaba_minor, developer_minor, status, created_at, paid_at)
       VALUES (?, 'plugin', 'acct', ?, ?, 'one_time', 'dev', 'fake', 'USD', 1000, 150, 850, ?, ?, ?)`,
    )
    .bind(id, homeId, plugin, status, at, at)
    .run();
  await db()
    .prepare(`INSERT INTO entitlements (purchase_id, home_id, plugin_id, product) VALUES (?, ?, ?, 'one_time')`)
    .bind(id, homeId, plugin)
    .run();
}

describe("a home's entitlements", () => {
  it("are signed by E for this home and its key, and only the paid ones", async () => {
    const { hk, homeId } = await linkedHome();
    await paidPurchase(`pur-${homeId}-1`, homeId, "dev.example.loudness");
    await paidPurchase(`pur-${homeId}-2`, homeId, "dev.example.refunded", "refunded");
    const path = `/v1/homes/${homeId}/entitlements`;
    const r = await call("GET", path, { headers: await signed(hk.k.privateKey, homeId, "GET", path, "") });
    expect(r.status, JSON.stringify(r.body)).toBe(200);
    const list = r.body.entitlements as { signed: Record<string, unknown>; signatures: { key_id: string; sig: string }[] }[];
    expect(list.map((e) => e.signed.plugin_id)).toEqual(["dev.example.loudness"]);
    const e = list[0]!;
    expect(e.signed).toMatchObject({ v: 1, home_id: homeId, home_key_fp: await sha256hex(hk.raw), product: "one_time" });
    expect(String(e.signed.valid_until)).toMatch(/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/);
    const E = await Signer.fromSecret((env as unknown as { ENTITLEMENT_SIGNING_KEY: string }).ENTITLEMENT_SIGNING_KEY);
    expect(e.signatures[0]!.key_id).toBe(E.id);
    expect(await verifySig(fromB64(E.public), KIND.entitlement, JSON.stringify(e.signed), e.signatures[0]!.sig)).toBe(true);
    expect(JSON.stringify(r.body)).not.toContain("fake");
  });

  it("are refused to a request not signed by that home", async () => {
    const a = await linkedHome();
    const b = await linkedHome();
    await paidPurchase(`pur-${a.homeId}-x`, a.homeId, "dev.example.loudness");
    const path = `/v1/homes/${a.homeId}/entitlements`;
    // home b signs a request for home a's entitlements
    const r = await call("GET", path, { headers: await signed(b.hk.k.privateKey, b.homeId, "GET", path, "") });
    expect(r.status).toBe(401);
    expect((await call("GET", path)).status).toBe(401);
  });
});
