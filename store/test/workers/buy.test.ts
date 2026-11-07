// Story 54.19 §5 on the store (T05's buyer and home routes, T02 through the Worker): a linked
// home asks for a purchase intent with its own signature; the admin's browser posts it to
// /buy/<plugin>; the buyer signs in to the account the home is linked to, sees the price and
// goes to the provider's checkout (the fake); the provider's webhook records the purchase and
// the home's entitlements route then signs it. The catalog names a paid plugin's product and
// prices. Every page is scriptless.
import { env } from "cloudflare:workers";
import { describe, expect, it } from "vitest";
import { exports } from "cloudflare:workers";
import { signFake } from "../../payments/providers/fake/index";
import { Root, emptyState } from "../../signing/verify";
import { countersign } from "../../web/publish";
import { b64url, sha256hex } from "../../web/crypto";
import { E, ORIGIN, account, buildPackage, call, developer, submit } from "./helpers";

const db = () => (env as unknown as { DB_COMMERCE: D1Database }).DB_COMMERCE;
const writer = () => E.CATALOG_WRITER.get(E.CATALOG_WRITER.idFromName("main"));
const keys = async () => (await Root.parse(E.PLUGINS_ROOT)).verifyStoreKeys(E.TEST_STORE_KEYS, emptyState(), new Date());

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

/** A home linked to a fresh buyer account. */
async function linkedHome() {
  const buyer = await account();
  const hk = await homeKey();
  const p = await call("POST", "/v1/homes/pair", { body: { public_key: hk.public, name: "Den" } });
  const ok = await call("POST", "/v1/homes/link", { cookie: buyer.cookie, body: { user_code: p.body.user_code, confirm: true } });
  expect(ok.status).toBe(200);
  return { buyer, hk, homeId: ok.body.home_id as string };
}

let n = 0;
/** A paid plugin: $10 one-time (and SAR 37.50), its developer onboarded with the fake. */
async function paidPlugin(): Promise<string> {
  const id = `dev.example.paid${Date.now()}${n++}`;
  await db().batch([
    db().prepare("INSERT OR IGNORE INTO payment_providers (id, enabled, priority, mode) VALUES ('fake', 1, 10, 'test')"),
    db().prepare("INSERT INTO products (plugin_id, kind, developer_id, active) VALUES (?, 'one_time', 'dev-buy', 1)").bind(id),
    db().prepare("INSERT INTO prices (plugin_id, kind, currency, amount_minor) VALUES (?, 'one_time', 'USD', 1000)").bind(id),
    db().prepare("INSERT INTO prices (plugin_id, kind, currency, amount_minor) VALUES (?, 'one_time', 'SAR', 3750)").bind(id),
    db().prepare(
      "INSERT OR IGNORE INTO provider_accounts (developer_id, provider_id, account_ref, status) VALUES ('dev-buy', 'fake', 'fake_acct_dev-buy', 'active')",
    ),
  ]);
  return id;
}

async function intent(hk: { k: CryptoKeyPair }, homeId: string, plugin: string, extra: Record<string, unknown> = {}) {
  const path = `/v1/homes/${homeId}/purchase-intents`;
  const body = JSON.stringify({ plugin_id: plugin, ...extra });
  return call("POST", path, {
    raw: body,
    headers: { ...(await signed(hk.k.privateKey, homeId, "POST", path, body)), "Content-Type": "application/json" },
  });
}

/** A page request with the browser's own cookies (the session and the buy intent). */
async function page(
  method: string,
  path: string,
  o: { session?: string; buy?: string; origin?: string | null; form?: Record<string, string>; country?: string } = {},
) {
  const h = new Headers({ "CF-Connecting-IP": "10.9.9.9" });
  if (o.origin !== null) h.set("Origin", o.origin ?? ORIGIN);
  if (o.country) h.set("CF-IPCountry", o.country);
  const c = [o.session ? `__Host-mks_session=${o.session}` : "", o.buy ? `__Host-mks_buy=${o.buy}` : ""].filter(Boolean).join("; ");
  if (c) h.set("Cookie", c);
  if (o.form) h.set("Content-Type", "application/x-www-form-urlencoded");
  const r = await (exports as unknown as { default: Fetcher }).default.fetch(`${ORIGIN}${path}`, {
    method,
    redirect: "manual",
    headers: h,
    body: o.form ? new URLSearchParams(o.form).toString() : undefined,
  });
  return { status: r.status, headers: r.headers, text: await r.text() };
}

const buyCookie = (h: Headers) => /__Host-mks_buy=([^;]*)/.exec(h.get("Set-Cookie") ?? "")?.[1] ?? "";

describe("a purchase intent", () => {
  it("is made only for the home's own signature and a plugin on sale", async () => {
    const { hk, homeId } = await linkedHome();
    const other = await linkedHome();
    const plugin = await paidPlugin();
    // another home's signature for this home's id
    const path = `/v1/homes/${homeId}/purchase-intents`;
    const body = JSON.stringify({ plugin_id: plugin });
    const forged = await call("POST", path, { raw: body, headers: await signed(other.hk.k.privateKey, homeId, "POST", path, body) });
    expect(forged.status).toBe(401);
    expect((await intent(hk, homeId, "dev.example.free")).status).toBe(404);
    expect((await intent(hk, homeId, plugin, { product: "yearly" })).status).toBe(404);
    const ok = await intent(hk, homeId, plugin, { lang: "ar" });
    expect(ok.status, JSON.stringify(ok.body)).toBe(200);
    expect(ok.body).toMatchObject({ buy_url: `${ORIGIN}/buy/${plugin}`, product: "one_time" });
    expect(String(ok.body.intent)).toMatch(/^[A-Za-z0-9_-]{43}$/);
    // only its hash is kept
    const row = await db().prepare("SELECT * FROM purchase_intents WHERE home_id = ?").bind(homeId).first<Record<string, string>>();
    expect(row!.id_hash).toBe(await sha256hex(String(ok.body.intent)));
    expect(row!.lang).toBe("ar");
  });
});

describe("the buy pages", () => {
  it("sign in, the price, the provider's checkout, the webhook and the home's entitlement", async () => {
    const { buyer, hk, homeId } = await linkedHome();
    const plugin = await paidPlugin();
    const t = String((await intent(hk, homeId, plugin)).body.intent);

    // the home's post (from the home's own origin): the intent goes into a same-site cookie
    const posted = await page("POST", `/buy/${plugin}`, { origin: "http://maktaba.local:8080", form: { intent: t } });
    expect(posted.status).toBe(303);
    expect(posted.headers.get("Location")).toBe(`/buy/${plugin}`);
    expect(posted.headers.get("Set-Cookie")).toMatch(/__Host-mks_buy=[A-Za-z0-9_-]{43}; Path=\/; Secure; HttpOnly; SameSite=Lax/);
    const buy = buyCookie(posted.headers);

    // signed out: to the store's sign-in, and back here after it
    const out = await page("GET", `/buy/${plugin}`, { buy });
    expect(out.status).toBe(303);
    expect(out.headers.get("Location")).toBe(`/signin?next=${encodeURIComponent(`/buy/${plugin}`)}`);

    // another account: told which one to use, nothing sold
    const stranger = await account();
    const wrong = await page("GET", `/buy/${plugin}`, { buy, session: stranger.cookie });
    expect(wrong.status).toBe(403);
    expect(wrong.text).toContain("linked to another store account");

    // the home's account: the price in the buyer's currency, a scriptless page
    const shown = await page("GET", `/buy/${plugin}`, { buy, session: buyer.cookie, country: "US" });
    expect(shown.status).toBe(200);
    expect(shown.text).toContain("$10.00, one time");
    expect(shown.text).toContain("Den");
    expect(shown.text).not.toContain("<script");
    expect(shown.headers.get("Content-Security-Policy")).toContain("default-src 'none'");
    expect(shown.headers.get("Content-Security-Policy")).not.toContain("script-src");
    const sa = await page("GET", `/buy/${plugin}`, { buy, session: buyer.cookie, country: "SA" });
    expect(sa.text).toContain("SAR");

    // the pay step only from the store's own page
    const cross = await page("POST", `/buy/${plugin}/pay`, {
      buy,
      session: buyer.cookie,
      origin: "https://evil.example",
      form: { currency: "USD" },
      country: "US",
    });
    expect(cross.status).toBe(403);
    const pay = await page("POST", `/buy/${plugin}/pay`, { buy, session: buyer.cookie, form: { currency: "USD" }, country: "US" });
    expect(pay.status, pay.text).toBe(303);
    expect(pay.headers.get("Location")).toMatch(/^https:\/\/plugins\.maktaba\.tv\/test-checkout\/pur_/);
    expect(pay.headers.get("Set-Cookie")).toContain("Max-Age=0");
    // the intent is used once
    const again = await page("POST", `/buy/${plugin}/pay`, { buy, session: buyer.cookie, form: { currency: "USD" }, country: "US" });
    expect(again.status).toBe(400);

    // the provider's webhook: paid, split 85/15, and the home's entitlement
    const purchase = await db()
      .prepare("SELECT * FROM purchases WHERE home_id = ? AND plugin_id = ?")
      .bind(homeId, plugin)
      .first<Record<string, unknown>>();
    expect(purchase).toMatchObject({ account_id: buyer.id, status: "pending", amount_minor: 1000, currency: "USD" });
    const events = JSON.stringify({
      events: [
        {
          eventId: `evt-${purchase!.id}`,
          type: "payment.succeeded",
          purchaseId: purchase!.id,
          providerRef: `fake_pay_${purchase!.id}`,
          amount: { minor: 1000, currency: "USD" },
          occurredAt: new Date().toISOString(),
        },
      ],
    });
    const forged = await call("POST", "/v1/payments/webhook/fake", { raw: events, headers: { "x-fake-signature": "00".repeat(32) } });
    expect(forged.status).toBe(401);
    const hook = await call("POST", "/v1/payments/webhook/fake", {
      raw: events,
      headers: { "x-fake-signature": await signFake("test-webhook-secret", events) },
    });
    expect(hook.status).toBe(200);
    expect(
      await db().prepare("SELECT status, maktaba_minor, developer_minor FROM purchases WHERE id = ?").bind(purchase!.id).first(),
    ).toEqual({
      status: "paid",
      maktaba_minor: 150,
      developer_minor: 850,
    });
    const path = `/v1/homes/${homeId}/entitlements`;
    const ents = await call("GET", path, { headers: await signed(hk.k.privateKey, homeId, "GET", path, "") });
    expect((ents.body.entitlements as { signed: { plugin_id: string } }[]).map((e) => e.signed.plugin_id)).toEqual([plugin]);

    // back from the provider
    const done = await page("GET", `/buy/${plugin}/done?lang=ar`);
    expect(done.status).toBe(200);
    expect(done.text).toContain('dir="rtl"');
  });

  it("an expired, unknown or other plugin's intent is the start-again page", async () => {
    const { buyer, hk, homeId } = await linkedHome();
    const plugin = await paidPlugin();
    const t = String((await intent(hk, homeId, plugin)).body.intent);
    expect((await page("POST", `/buy/${plugin}`, { form: { intent: "x".repeat(43) } })).status).toBe(400);
    expect((await page("POST", `/buy/dev.example.other`, { form: { intent: t } })).status).toBe(400);
    expect((await page("GET", `/buy/${plugin}`, { session: buyer.cookie })).status).toBe(400);
    await db()
      .prepare("UPDATE purchase_intents SET expires_at = ? WHERE home_id = ?")
      .bind(new Date(Date.now() - 1000).toISOString(), homeId)
      .run();
    const late = await page("POST", `/buy/${plugin}`, { form: { intent: t } });
    expect(late.status).toBe(400);
    expect(late.text).toContain("expired");
  });

  it("a country no enabled provider sells to is told so, and nothing is bought", async () => {
    const { buyer, hk, homeId } = await linkedHome();
    const plugin = await paidPlugin();
    await db().prepare("UPDATE payment_providers SET regions = '[\"SA\"]' WHERE id = 'fake'").run();
    try {
      const t = String((await intent(hk, homeId, plugin)).body.intent);
      const buy = buyCookie((await page("POST", `/buy/${plugin}`, { form: { intent: t } })).headers);
      const r = await page("GET", `/buy/${plugin}`, { buy, session: buyer.cookie, country: "US" });
      expect(r.status).toBe(200);
      expect(r.text).toContain("can&#39;t be bought from your country yet");
      expect(r.text).not.toContain("<form");
    } finally {
      await db().prepare("UPDATE payment_providers SET regions = '[]' WHERE id = 'fake'").run();
    }
  });
});

describe("the catalog", () => {
  it("names a paid plugin's product and carries its prices", async () => {
    const dev = await developer();
    const id = `${dev.prefix}.paidtagger`;
    const sid = await submit(dev, id, "1.0.0", await buildPackage(id, "1.0.0", dev.key));
    await countersign(env as never, sid, "reviewed");
    await db().batch([
      db().prepare("INSERT INTO products (plugin_id, kind, developer_id, active) VALUES (?, 'one_time', 'dev-cat', 1)").bind(id),
      db().prepare("INSERT INTO prices (plugin_id, kind, currency, amount_minor) VALUES (?, 'one_time', 'USD', 500)").bind(id),
      db().prepare("INSERT INTO prices (plugin_id, kind, currency, amount_minor) VALUES (?, 'one_time', 'EUR', 450)").bind(id),
    ]);
    await writer().regenerate();
    const cat = await (await keys()).verifyCatalog(await (await E.BUCKET.get("catalog-1.json"))!.text(), 1, emptyState(), new Date());
    const plugins = cat.signed.plugins as { id: string; price: string; extra?: { prices?: Record<string, number> } }[];
    const p = plugins.find((x) => x.id === id)!;
    expect(p.price).toBe("one_time");
    expect(p.extra?.prices).toEqual({ EUR: 450, USD: 500 });
    // a free plugin stays free, with no prices
    expect(plugins.filter((x) => x.id !== id && !x.id.includes("paid")).every((x) => x.price === "free" && !x.extra?.prices)).toBe(true);
  });
});
