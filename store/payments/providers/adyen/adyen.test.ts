import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { registry } from "../../../testing/harness.js";
import { WebhookSignatureError, ProviderError } from "../../provider.js";
import { adyenHosts, adyenSign, adyenSigningString } from "./index.js";

const HMAC = "44782DEF547AAA06C910C43932B1EB0C71FC68D9D0C057550C48EC2ACF6BA056";
const T0 = new Date("2026-11-06T10:00:00Z");

function adyen(answers: Record<string, unknown> = {}) {
  const calls: { url: string; body: any; key: string | null; method: string }[] = [];
  const fetchFn = (async (url: string, init: RequestInit) => {
    calls.push({ url, body: init.body ? JSON.parse(String(init.body)) : undefined, key: new Headers(init.headers).get("X-API-Key"), method: String(init.method) });
    const path = Object.keys(answers).find((k) => url.endsWith(k));
    if (!path) return new Response("{}", { status: 404 });
    return new Response(JSON.stringify(answers[path]), { status: 200 });
  }) as unknown as typeof fetch;
  const reg = registry([{ id: "adyen", enabled: true, priority: 1, mode: "test", regions: [], currencies: [], resaleConfirmedAt: null, settings: { merchantAccount: "MaktabaStore" } }], () => T0, fetchFn);
  return { p: reg.get("adyen")!.provider, calls };
}

async function signed() {
  const doc = JSON.parse(readFileSync(join(import.meta.dirname, "testdata", "notification.json"), "utf8"));
  for (const i of doc.notificationItems) i.NotificationRequestItem.additionalData.hmacSignature = await adyenSign(HMAC, i.NotificationRequestItem);
  return doc;
}

describe("the Adyen for Platforms adapter", () => {
  it("opens a hosted session with the 85/15 split to the developer's balance account", async () => {
    const { p, calls } = adyen({ "/sessions": { id: "CS1", url: "https://checkoutshopper-test.adyen.com/x", expiresAt: "2026-11-06T11:00:00Z" } });
    const s = await p.createCheckout({
      purchaseId: "pur-1", buyer: { accountId: "a", email: "b@example.com", country: "SA" }, homeId: "home-1",
      item: { kind: "plugin", pluginId: "dev.example.x", product: "one_time" },
      price: { minor: 3750, currency: "SAR" },
      split: { maktaba: { minor: 562, currency: "SAR" }, developer: { minor: 3188, currency: "SAR" } },
      developer: { id: "dev-1", email: "", country: "AE", accountRef: "AH1/BA1" }, returnUrl: "https://plugins.maktaba.tv/buy/x/done",
    });
    expect(s).toEqual({ providerRef: "CS1", url: "https://checkoutshopper-test.adyen.com/x", expiresAt: "2026-11-06T11:00:00Z" });
    const c = calls[0]!;
    expect(c.url).toBe("https://checkout-test.adyen.com/v71/sessions");
    expect(c.key).toBe("test-checkout-key");
    expect(c.body).toMatchObject({ merchantAccount: "MaktabaStore", mode: "hosted", reference: "pur-1", countryCode: "SA", amount: { value: 3750, currency: "SAR" } });
    expect(c.body.splits).toEqual([
      { type: "BalanceAccount", account: "BA1", amount: { value: 3188 }, reference: "pur-1-developer" },
      { type: "Commission", amount: { value: 562 }, reference: "pur-1-store" },
    ]);
    await expect(
      p.createCheckout({ purchaseId: "p", buyer: { accountId: "a", email: "", country: "SA" }, homeId: "h", item: { kind: "plugin", pluginId: "x", product: "one_time" },
        price: { minor: 100, currency: "SAR" }, split: { maktaba: { minor: 15, currency: "SAR" }, developer: { minor: 85, currency: "SAR" } }, returnUrl: "https://x/" })
    ).rejects.toBeInstanceOf(ProviderError);
  });

  it("verifies each notification's HMAC and maps the events the store follows", async () => {
    const { p } = adyen();
    const doc = await signed();
    const events = await p.parseWebhook({ headers: new Headers(), body: JSON.stringify(doc) });
    expect(events).toEqual([
      { eventId: "QFQTPCQ8HXSKGK82:AUTHORISATION:true", type: "payment.succeeded", purchaseId: "pur-1", providerRef: "QFQTPCQ8HXSKGK82",
        amount: { minor: 3750, currency: "SAR" }, occurredAt: "2026-11-06T09:00:00.000Z" },
    ]);
    expect(p.webhookAck()).toBe("[accepted]");
    // one changed field, a wrong key or another merchant account: refused whole
    const tampered = await signed();
    tampered.notificationItems[0].NotificationRequestItem.amount.value = 1;
    await expect(p.parseWebhook({ headers: new Headers(), body: JSON.stringify(tampered) })).rejects.toBeInstanceOf(WebhookSignatureError);
    const other = await signed();
    other.notificationItems[0].NotificationRequestItem.merchantAccountCode = "SomeoneElse";
    other.notificationItems[0].NotificationRequestItem.additionalData.hmacSignature = await adyenSign(HMAC, other.notificationItems[0].NotificationRequestItem);
    await expect(p.parseWebhook({ headers: new Headers(), body: JSON.stringify(other) })).rejects.toBeInstanceOf(WebhookSignatureError);
    await expect(p.parseWebhook({ headers: new Headers(), body: "not json" })).rejects.toBeInstanceOf(WebhookSignatureError);
  });

  it("signs the fields in Adyen's order", () => {
    expect(adyenSigningString({ pspReference: "P", originalReference: "O", merchantAccountCode: "M", merchantReference: "R",
      amount: { value: 10, currency: "SAR" }, eventCode: "REFUND", eventDate: "", success: "true" })).toBe("P:O:M:R:10:SAR:REFUND:true");
  });

  it("refunds with the split reversed, and onboards a developer onto the balance platform", async () => {
    const { p, calls } = adyen({
      "/refunds": { pspReference: "RF1" },
      "/legalEntities": { id: "LE1" },
      "/accountHolders": { id: "AH1" },
      "/balanceAccounts": { id: "BA1" },
      "/onboardingLinks": { url: "https://balanceplatform-test.adyen.com/onboard/x" },
      "/accountHolders/AH1": { status: "active", capabilities: { sendToTransferInstrument: { allowed: true }, receiveFromPlatformPayments: { allowed: true } } },
    });
    expect(await p.refund({ purchaseId: "pur-1", providerRef: "PSP1", amount: { minor: 3750, currency: "SAR" },
      split: { maktaba: { minor: 562, currency: "SAR" }, developer: { minor: 3188, currency: "SAR" } },
      developer: { id: "d", email: "", country: "", accountRef: "AH1/BA1" }, reason: "buyer_request" })).toEqual({ providerRef: "RF1" });
    expect(calls[0]!.url).toBe("https://checkout-test.adyen.com/v71/payments/PSP1/refunds");
    expect(calls[0]!.body.splits[0]).toMatchObject({ account: "BA1", amount: { value: 3188 } });
    const o = await p.onboardDeveloper({ id: "dev-1", email: "d@example.com", country: "EG" }, "https://plugins.maktaba.tv/console/payouts");
    expect(o).toEqual({ url: "https://balanceplatform-test.adyen.com/onboard/x", accountRef: "AH1/BA1" });
    expect(calls.slice(1).map((c) => c.key)).toEqual(Array(4).fill("test-platform-key"));
    expect(await p.payoutStatus("AH1/BA1")).toBe("active");
    await expect(p.payout({ payoutId: "x", developer: { id: "d", email: "", country: "" }, amount: { minor: 1, currency: "USD" }, period: "2026-11" })).rejects.toThrow(/itself/);
    expect(p.capabilities).toMatchObject({ model: "marketplace", payouts: "provider_splits", tax: "store" });
  });

  it("goes live only with the account's URL prefix", () => {
    expect(adyenHosts({ mode: "live", liveUrlPrefix: "1797a841fbb37ca7-AdyenDemo" }).checkout).toBe(
      "https://1797a841fbb37ca7-AdyenDemo-checkout-live.adyenpayments.com/checkout/v71"
    );
    const reg = registry([{ id: "adyen", enabled: true, priority: 1, mode: "live", regions: [], currencies: [], resaleConfirmedAt: "2026-11-01", settings: { merchantAccount: "M", mode: "live" } }]);
    expect(reg.list()).toEqual([]);
    expect(reg.issues[0]).toMatch(/liveUrlPrefix/);
  });
});
