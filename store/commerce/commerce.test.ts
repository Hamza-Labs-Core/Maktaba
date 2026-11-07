// T02, T05 (the refusals), T08 and the acceptance criteria of story 54.19, on the fake
// provider and the real store-commerce schema.

import { describe, expect, it } from "vitest";
import { T0, fakeConfig, fakeWebhook, registry, testDB, testSigner } from "../testing/harness.js";
import { handlePaymentWebhook } from "../web/payments_webhook.js";
import { canonical, homeEntitlements } from "./entitlements.js";
import { balances } from "./ledger.js";
import { SaleRefused, approvePayout, requestRefund, runPayouts, setPrice, startPurchase, startSellerFee } from "./sales.js";
import type { ProviderEvent } from "../payments/provider.js";
import { utf8, fromBase64 } from "../payments/crypto.js";

const buyer = { accountId: "acct-1", email: "buyer@example.com", country: "SA" };

async function store(fakeSettings: Record<string, unknown> = {}) {
  const db = testDB();
  const reg = registry([fakeConfig({ settings: fakeSettings })]);
  const run = (sql: string, ...v: unknown[]) => db.prepare(sql).bind(...v).run();
  await run(`INSERT INTO seller_fees (developer_id, purchase_id, paid_at) VALUES ('dev-1', 'fee-1', ?)`, T0.toISOString());
  await run(`INSERT INTO provider_accounts (developer_id, provider_id, account_ref, status) VALUES ('dev-1', 'fake', 'fake_acct_dev-1', 'active')`);
  await setPrice(db, "dev-1", "dev.example.loudness", "one_time", { minor: 1000, currency: "USD" }, false);
  await setPrice(db, "dev-1", "dev.example.loudness", "one_time", { minor: 3750, currency: "SAR" }, false);
  const hook = (events: ProviderEvent[], at = T0) =>
    fakeWebhook(events).then((req) => handlePaymentWebhook(req, { db, registry: reg, now: () => at }));
  return { db, reg, hook };
}

const paid = (purchaseId: string, minor = 1000, currency = "USD", eventId = `ev-pay-${purchaseId}`): ProviderEvent => ({
  eventId,
  type: "payment.succeeded",
  purchaseId,
  providerRef: `fake_pay_${purchaseId}`,
  amount: { minor, currency },
  occurredAt: T0.toISOString(),
});

describe("paid plugins on the fake provider", () => {
  it("a $10 purchase: Maktaba $1.50, the developer $8.50, and an entitlement signed by E", async () => {
    const { db, reg, hook } = await store();
    const { url, providerId } = await startPurchase(db, reg, {
      purchaseId: "pur-1", buyer: { ...buyer, country: "US" }, homeId: "home-1", pluginId: "dev.example.loudness",
      kind: "one_time", currency: "USD", returnUrl: "https://plugins.maktaba.tv/buy/x/done", now: T0,
    });
    expect(providerId).toBe("fake");
    expect(url).toContain("pur-1");
    const res = await hook([paid("pur-1")]);
    expect(res.status).toBe(200);
    const p = await db.prepare(`SELECT * FROM purchases WHERE id = 'pur-1'`).first<Record<string, unknown>>();
    expect([p!.status, p!.maktaba_minor, p!.developer_minor]).toEqual(["paid", 150, 850]);
    expect(await balances(db, "developer:dev-1")).toEqual([{ provider_id: "fake", currency: "USD", minor: 850 }]);
    expect(await balances(db, "maktaba")).toEqual([{ provider_id: "fake", currency: "USD", minor: 150 }]);

    const { signer, publicKey } = await testSigner();
    const ents = await homeEntitlements(db, "home-1", async () => "ab".repeat(32), signer, T0);
    expect(ents).toHaveLength(1);
    const e = ents[0]!;
    expect(e.signed).toMatchObject({ v: 1, home_id: "home-1", home_key_fp: "ab".repeat(32), plugin_id: "dev.example.loudness", purchase_id: "pur-1" });
    expect(new Date(e.signed.valid_until).getTime() - T0.getTime()).toBe(31 * 86_400_000);
    expect(await crypto.subtle.verify({ name: "Ed25519" }, publicKey, fromBase64(e.sig), utf8(canonical(e.signed)))).toBe(true);
    // nothing in it names the provider
    expect(JSON.stringify(e)).not.toContain("fake");
    // another home gets nothing
    expect(await homeEntitlements(db, "home-2", async () => "cd".repeat(32), signer, T0)).toEqual([]);
  });

  it("the same webhook twice makes one purchase and one set of ledger rows", async () => {
    const { db, reg, hook } = await store();
    await startPurchase(db, reg, { purchaseId: "pur-2", buyer, homeId: "home-1", pluginId: "dev.example.loudness", kind: "one_time", currency: "SAR", returnUrl: "https://x/", now: T0 });
    expect((await hook([paid("pur-2", 3750, "SAR")])).status).toBe(200);
    expect((await hook([paid("pur-2", 3750, "SAR")])).status).toBe(200);
    const n = await db.prepare(`SELECT COUNT(*) AS n FROM ledger_entries WHERE purchase_id = 'pur-2'`).first<{ n: number }>();
    expect(n!.n).toBe(2);
    expect(await balances(db, "developer:dev-1")).toEqual([{ provider_id: "fake", currency: "SAR", minor: 3188 }]);
  });

  it("refuses a webhook that does not verify, and stores nothing", async () => {
    const { db, reg } = await store();
    await startPurchase(db, reg, { purchaseId: "pur-3", buyer, homeId: "home-1", pluginId: "dev.example.loudness", kind: "one_time", currency: "SAR", returnUrl: "https://x/", now: T0 });
    const req = await fakeWebhook([paid("pur-3", 3750, "SAR")], "another secret");
    expect((await handlePaymentWebhook(req, { db, registry: reg, now: () => T0 })).status).toBe(401);
    const e = await db.prepare(`SELECT COUNT(*) AS n FROM provider_events`).first<{ n: number }>();
    expect(e!.n).toBe(0);
    const unknown = new Request("https://plugins.maktaba.tv/v1/payments/webhook/nope", { method: "POST", body: "{}" });
    expect((await handlePaymentWebhook(unknown, { db, registry: reg, now: () => T0 })).status).toBe(404);
  });

  it("a refund within 14 days reverses the developer's share and ends the entitlement", async () => {
    const { db, reg, hook } = await store();
    await startPurchase(db, reg, { purchaseId: "pur-4", buyer, homeId: "home-1", pluginId: "dev.example.loudness", kind: "one_time", currency: "USD", returnUrl: "https://x/", now: T0 });
    await hook([paid("pur-4")]);
    const later = new Date(T0.getTime() + 15 * 86_400_000);
    await expect(requestRefund(db, reg, "pur-4", "buyer_request", later)).rejects.toThrow("refund_window_over");
    await requestRefund(db, reg, "pur-4", "buyer_request", new Date(T0.getTime() + 13 * 86_400_000));
    await hook([{ eventId: "ev-refund-4", type: "refund.succeeded", purchaseId: "pur-4", providerRef: "fake_refund_pur-4", occurredAt: T0.toISOString() }]);
    expect(await balances(db, "developer:dev-1")).toEqual([{ provider_id: "fake", currency: "USD", minor: 0 }]);
    const { signer } = await testSigner();
    expect(await homeEntitlements(db, "home-1", async () => "ab".repeat(32), signer, T0)).toEqual([]);
    // a store admin may refund later
    await expect(requestRefund(db, reg, "pur-4", "store_admin", later)).rejects.toThrow("not_refundable");
  });

  it("a lost dispute ends the entitlement and reverses the share; a won one keeps it", async () => {
    const { db, reg, hook } = await store();
    for (const id of ["pur-5", "pur-6"])
      await startPurchase(db, reg, { purchaseId: id, buyer, homeId: "home-1", pluginId: "dev.example.loudness", kind: "one_time", currency: "USD", returnUrl: "https://x/", now: T0 });
    await hook([paid("pur-5"), paid("pur-6")]);
    const ev = (id: string, type: ProviderEvent["type"], n: number): ProviderEvent => ({ eventId: `ev-${type}-${id}-${n}`, type, purchaseId: id, providerRef: "d", occurredAt: T0.toISOString() });
    await hook([ev("pur-5", "dispute.opened", 1), ev("pur-6", "dispute.opened", 1)]);
    await hook([ev("pur-5", "dispute.lost", 2), ev("pur-6", "dispute.won", 2)]);
    const status = async (id: string) => (await db.prepare(`SELECT status FROM purchases WHERE id = ?`).bind(id).first<{ status: string }>())!.status;
    expect([await status("pur-5"), await status("pur-6")]).toEqual(["lost", "paid"]);
    expect(await balances(db, "developer:dev-1")).toEqual([{ provider_id: "fake", currency: "USD", minor: 850 }]);
    const { signer } = await testSigner();
    expect((await homeEntitlements(db, "home-1", async () => "ab".repeat(32), signer, T0)).map((e) => e.signed.purchase_id)).toEqual(["pur-6"]);
  });

  it("pricing needs the seller fee and active payouts; a free plugin stays free", async () => {
    const db = testDB();
    const price = { minor: 1000, currency: "USD" };
    await expect(setPrice(db, "dev-2", "dev.example.p", "one_time", price, false)).rejects.toThrow("seller_fee_required");
    await db.prepare(`INSERT INTO seller_fees VALUES ('dev-2', 'f', ?)`).bind(T0.toISOString()).run();
    await expect(setPrice(db, "dev-2", "dev.example.p", "one_time", price, false)).rejects.toThrow("payouts_required");
    await db.prepare(`INSERT INTO provider_accounts (developer_id, provider_id, account_ref, status) VALUES ('dev-2', 'fake', 'r', 'restricted')`).run();
    await expect(setPrice(db, "dev-2", "dev.example.p", "one_time", price, false)).rejects.toBeInstanceOf(SaleRefused);
    await db.prepare(`UPDATE provider_accounts SET status = 'active'`).run();
    await setPrice(db, "dev-2", "dev.example.p", "one_time", price, false);
    await expect(setPrice(db, "dev-2", "dev.example.p", "one_time", { minor: 50, currency: "USD" }, false)).rejects.toThrow("price_out_of_range");
    await expect(setPrice(db, "dev-2", "dev.example.free", "one_time", price, true)).rejects.toThrow("free_stays_free");
  });

  it("the seller fee is all the store's and opens selling", async () => {
    const { db, reg, hook } = await store();
    await startSellerFee(db, reg, { purchaseId: "fee-9", developerId: "dev-9", buyer, returnUrl: "https://x/", now: T0 });
    await hook([paid("fee-9", 2500, "USD")]);
    expect(await db.prepare(`SELECT developer_id FROM seller_fees WHERE developer_id = 'dev-9'`).first()).toBeTruthy();
    expect(await balances(db, "maktaba")).toEqual([{ provider_id: "fake", currency: "USD", minor: 2500 }]);
    expect(await balances(db, "developer:dev-9")).toEqual([]);
  });

  it("a buyer in a country no provider sells to cannot buy", async () => {
    const db = testDB();
    const reg = registry([fakeConfig({ regions: ["AE"] })]);
    await db.prepare(`INSERT INTO seller_fees VALUES ('dev-1', 'f', ?)`).bind(T0.toISOString()).run();
    await db.prepare(`INSERT INTO provider_accounts (developer_id, provider_id, account_ref, status) VALUES ('dev-1', 'fake', 'r', 'active')`).run();
    await setPrice(db, "dev-1", "dev.example.x", "one_time", { minor: 1000, currency: "USD" }, false);
    await expect(
      startPurchase(db, reg, { purchaseId: "p", buyer: { ...buyer, country: "US" }, homeId: "h", pluginId: "dev.example.x", kind: "one_time", currency: "USD", returnUrl: "https://x/", now: T0 })
    ).rejects.toThrow("no_provider_for_region");
  });
});

// T08: a merchant-of-record provider: the store pays developers monthly, 85 % of net sales
// after refunds, and under $25 rolls over
describe("monthly payouts through a store_batches provider", () => {
  it("pays what the ledger holds, rolls over small amounts, and takes a failed payout back", async () => {
    const { db, reg, hook } = await store({ model: "merchant_of_record" });
    await db.prepare(`INSERT INTO seller_fees VALUES ('dev-small', 'f2', ?)`).bind(T0.toISOString()).run();
    await db.prepare(`INSERT INTO provider_accounts (developer_id, provider_id, account_ref, status) VALUES ('dev-small', 'fake', 'fake_acct_dev-small', 'active')`).run();
    await setPrice(db, "dev-small", "dev.example.small", "one_time", { minor: 1000, currency: "USD" }, false);
    for (let i = 0; i < 4; i++)
      await startPurchase(db, reg, { purchaseId: `big-${i}`, buyer: { ...buyer, country: "US" }, homeId: "h", pluginId: "dev.example.loudness", kind: "one_time", currency: "USD", returnUrl: "https://x/", now: T0 });
    await startPurchase(db, reg, { purchaseId: "small-0", buyer: { ...buyer, country: "US" }, homeId: "h", pluginId: "dev.example.small", kind: "one_time", currency: "USD", returnUrl: "https://x/", now: T0 });
    await hook([paid("big-0"), paid("big-1"), paid("big-2"), paid("big-3"), paid("small-0")]);
    await hook([{ eventId: "r", type: "refund.succeeded", purchaseId: "big-3", providerRef: "x", occurredAt: T0.toISOString() }]);
    const lines = await runPayouts(db, reg, "2026-11", T0);
    expect(lines).toEqual([
      { developerId: "dev-1", providerId: "fake", currency: "USD", minor: 2550, status: "draft" },
      { developerId: "dev-small", providerId: "fake", currency: "USD", minor: 850, status: "rolled_over" },
    ]);
    const id = "po_2026-11_dev-1_fake_USD";
    await approvePayout(db, reg, id, "admin-1", T0);
    expect((await db.prepare(`SELECT status FROM payouts WHERE id = ?`).bind(id).first<{ status: string }>())!.status).toBe("sent");
    expect(await balances(db, "developer:dev-1")).toEqual([{ provider_id: "fake", currency: "USD", minor: 0 }]);
    await hook([{ eventId: "pf", type: "payout.failed", payoutId: id, providerRef: "x", occurredAt: T0.toISOString() }]);
    expect(await balances(db, "developer:dev-1")).toEqual([{ provider_id: "fake", currency: "USD", minor: 2550 }]);
  });

  it("a marketplace provider paid developers itself: report lines only", async () => {
    const { db, reg, hook } = await store();
    await startPurchase(db, reg, { purchaseId: "m-1", buyer: { ...buyer, country: "US" }, homeId: "h", pluginId: "dev.example.loudness", kind: "one_time", currency: "USD", returnUrl: "https://x/", now: T0 });
    await hook([paid("m-1")]);
    const lines = await runPayouts(db, reg, "2026-11", T0);
    expect(lines.map((l) => l.status)).toEqual(["settled_by_provider"]);
    expect((await db.prepare(`SELECT COUNT(*) AS n FROM payouts`).first<{ n: number }>())!.n).toBe(0);
  });
});
