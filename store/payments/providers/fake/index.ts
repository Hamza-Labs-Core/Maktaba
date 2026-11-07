// The fake provider: CI's and staging's (story 54.19 §2). It talks to no network: a
// checkout is a URL on the store itself, webhooks are JSON signed with HMAC-SHA256 (hex,
// header x-fake-signature) under the secret PAYMENTS_FAKE_WEBHOOK_SECRET, and refunds and
// payouts answer at once. Its capabilities are set by its configuration, so one fake plays
// a merchant of record (store_batches payouts) or a marketplace (provider_splits).

import { hmacSha256, safeEqual, toHex, utf8 } from "../../crypto.js";
import {
  type Capabilities,
  type CheckoutRequest,
  type PaymentProvider,
  type ProviderDeps,
  type ProviderEvent,
  type ProviderModule,
  ProviderError,
  WebhookSignatureError,
} from "../../provider.js";

export interface FakeConfig {
  model: Capabilities["model"];
  payouts: Capabilities["payouts"];
  regions: string[];
  currencies: string[];
  /** developers whose payouts are restricted (payoutStatus) */
  restricted: string[];
}

export const FAKE_SIGNATURE_HEADER = "x-fake-signature";

/** Signs a fake webhook body (the tests' provider side). */
export async function signFake(secret: string, body: string): Promise<string> {
  return toHex(await hmacSha256(utf8(secret), body));
}

class Fake implements PaymentProvider {
  readonly capabilities: Capabilities;
  constructor(
    private readonly cfg: FakeConfig,
    private readonly secret: string,
    private readonly deps: ProviderDeps
  ) {
    this.capabilities = {
      id: "fake",
      label: "Test provider",
      model: cfg.model,
      payouts: cfg.payouts,
      subscriptions: true,
      refunds: "partial",
      disputes: true,
      tax: cfg.model === "merchant_of_record" ? "provider" : "store",
      regions: cfg.regions,
      currencies: cfg.currencies,
    };
  }

  async onboardDeveloper(dev: { id: string }, returnUrl: string) {
    return { url: `${returnUrl}?fake_onboarded=${encodeURIComponent(dev.id)}`, accountRef: `fake_acct_${dev.id}` };
  }

  async payoutStatus(accountRef: string) {
    return this.cfg.restricted.some((d) => accountRef === `fake_acct_${d}`) ? ("restricted" as const) : ("active" as const);
  }

  async createCheckout(req: CheckoutRequest) {
    if (req.split.maktaba.minor + req.split.developer.minor !== req.price.minor)
      throw new ProviderError("config", "the split does not sum to the price");
    const expires = new Date(this.deps.now().getTime() + 30 * 60_000);
    return {
      providerRef: `fake_pay_${req.purchaseId}`,
      url: `https://plugins.maktaba.tv/test-checkout/${encodeURIComponent(req.purchaseId)}`,
      expiresAt: expires.toISOString(),
    };
  }

  async parseWebhook(req: { headers: Headers; body: string }): Promise<ProviderEvent[]> {
    const sig = req.headers.get(FAKE_SIGNATURE_HEADER) ?? "";
    if (!safeEqual(sig, await signFake(this.secret, req.body))) throw new WebhookSignatureError();
    const doc = JSON.parse(req.body) as { events: ProviderEvent[] };
    return doc.events;
  }

  webhookAck() {
    return "";
  }

  async refund(req: { purchaseId: string }) {
    return { providerRef: `fake_refund_${req.purchaseId}` };
  }

  async payout(p: { payoutId: string }) {
    if (this.cfg.payouts !== "store_batches") throw new ProviderError("unsupported", "the provider pays developers itself");
    return { providerRef: `fake_payout_${p.payoutId}` };
  }

  async quoteTax(price: { minor: number; currency: string }, country: string) {
    if (this.capabilities.tax !== "provider") return undefined;
    // a flat 15 % included, as Saudi VAT, to exercise the hook
    const tax = Math.round((price.minor * 15) / 115);
    return { inclusive: true, tax: { minor: tax, currency: price.currency }, rateBp: country === "SA" ? 1500 : 0, filedBy: "provider" as const };
  }

  async submitDisputeEvidence() {}
}

export const fakeModule: ProviderModule<FakeConfig> = {
  id: "fake",
  secrets: ["WEBHOOK_SECRET"],
  parseConfig(raw) {
    const list = (v: unknown, d: string[]) => (Array.isArray(v) ? v.map(String) : d);
    const model = (raw.model as Capabilities["model"]) ?? "marketplace";
    return {
      model,
      payouts: (raw.payouts as Capabilities["payouts"]) ?? (model === "marketplace" ? "provider_splits" : "store_batches"),
      regions: list(raw.regions, ["*"]),
      currencies: list(raw.currencies, ["USD", "EUR", "GBP", "SAR", "AED", "EGP"]),
      restricted: list(raw.restricted, []),
    };
  },
  create(cfg, secrets, deps) {
    return new Fake(cfg, secrets.WEBHOOK_SECRET ?? "", deps);
  },
};
