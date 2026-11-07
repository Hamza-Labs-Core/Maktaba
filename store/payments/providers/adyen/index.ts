// Adyen for Platforms: the store as seller with split payments (story 54.19 §1, model B).
//
// The one real-shaped adapter, proof that the interface fits a real provider. It is not
// wired to any keys: the owner opens the store's own Adyen platform account, confirms in
// writing that it allows the resale of third-party plugins, sets the secrets below and
// enables the provider (docs/plugins/payments.md). Everything Adyen-specific lives in this
// folder.
//
// - Checkout: a hosted Checkout session (`POST /sessions`, mode "hosted") with `splits`:
//   the developer's 85 % to their balance account, the store's 15 % as the commission.
// - Webhooks: standard notifications, each item signed with HMAC-SHA256 over
//   pspReference:originalReference:merchantAccountCode:merchantReference:value:currency:
//   eventCode:success under the hex HMAC key, base64 in additionalData.hmacSignature.
//   Adyen expects "[accepted]" back.
// - Refunds: `POST /payments/{pspReference}/refunds` with the same splits reversed.
// - Developers: a legal entity, an account holder and a balance account on the balance
//   platform, then a hosted onboarding link; Adyen pays out each balance account to the
//   developer's bank itself (payouts: provider_splits).
// - Tax: the store files (tax: "store"); Adyen calculates none.

import { fromBase64, fromHex, hmacSha256, safeEqual, toBase64 } from "../../crypto.js";
import {
  type Capabilities,
  type CheckoutRequest,
  type DeveloperRef,
  type PaymentProvider,
  type PayoutStatus,
  type ProviderDeps,
  type ProviderEvent,
  type ProviderEventType,
  type ProviderModule,
  type RefundRequest,
  type WebhookRequest,
  ProviderError,
  WebhookSignatureError,
} from "../../provider.js";

export interface AdyenConfig {
  /** the store's merchant account for plugin sales (not any cloud account) */
  merchantAccount: string;
  /** "test" (Adyen's test platform) or "live" */
  mode: "test" | "live";
  /** live only: the account's live URL prefix (Customer Area → Developers → API URLs) */
  liveUrlPrefix: string;
  /** buyer countries enabled on the account */
  regions: string[];
  currencies: string[];
}

/** The API bases for a mode. */
export function adyenHosts(cfg: Pick<AdyenConfig, "mode" | "liveUrlPrefix">) {
  if (cfg.mode === "live")
    return {
      checkout: `https://${cfg.liveUrlPrefix}-checkout-live.adyenpayments.com/checkout/v71`,
      lem: "https://kyc-live.adyen.com/lem/v3",
      bcl: "https://balanceplatform-api-live.adyen.com/bcl/v2",
    };
  return {
    checkout: "https://checkout-test.adyen.com/v71",
    lem: "https://kyc-test.adyen.com/lem/v3",
    bcl: "https://balanceplatform-api-test.adyen.com/bcl/v2",
  };
}

interface NotificationItem {
  additionalData?: Record<string, string>;
  amount: { value: number; currency: string };
  eventCode: string;
  eventDate: string;
  merchantAccountCode: string;
  merchantReference: string;
  originalReference?: string;
  pspReference: string;
  success: "true" | "false";
}

/** The string Adyen signs for one notification item. */
export function adyenSigningString(n: NotificationItem): string {
  return [
    n.pspReference,
    n.originalReference ?? "",
    n.merchantAccountCode,
    n.merchantReference,
    String(n.amount.value),
    n.amount.currency,
    n.eventCode,
    n.success,
  ].join(":");
}

export async function adyenSign(hexKey: string, n: NotificationItem): Promise<string> {
  return toBase64(await hmacSha256(fromHex(hexKey), adyenSigningString(n)));
}

/** Adyen's event codes, in the store's words (undefined: not one the store follows). */
function eventType(code: string, success: boolean): ProviderEventType | undefined {
  switch (code) {
    case "AUTHORISATION":
      return success ? "payment.succeeded" : "payment.failed";
    case "REFUND":
      return success ? "refund.succeeded" : "refund.failed";
    case "REFUND_FAILED":
      return "refund.failed";
    case "NOTIFICATION_OF_CHARGEBACK":
      return "dispute.opened";
    case "CHARGEBACK":
      return "dispute.lost";
    case "CHARGEBACK_REVERSED":
      return "dispute.won";
    default:
      return undefined;
  }
}

/** accountRef is "<accountHolderId>/<balanceAccountId>". */
function splitRef(ref: string): { holder: string; balance: string } {
  const [holder, balance] = ref.split("/");
  if (!holder || !balance) throw new ProviderError("not_onboarded", "the developer has no Adyen balance account");
  return { holder, balance };
}

class Adyen implements PaymentProvider {
  readonly capabilities: Capabilities;
  private readonly hosts: ReturnType<typeof adyenHosts>;

  constructor(
    private readonly cfg: AdyenConfig,
    private readonly secrets: Record<string, string>,
    private readonly deps: ProviderDeps
  ) {
    this.hosts = adyenHosts(cfg);
    this.capabilities = {
      id: "adyen",
      label: "Adyen for Platforms",
      model: "marketplace",
      payouts: "provider_splits",
      subscriptions: false,
      refunds: "partial",
      disputes: true,
      tax: "store",
      regions: cfg.regions,
      currencies: cfg.currencies,
    };
  }

  private async call<T>(url: string, body: unknown, key: string, method = "POST"): Promise<T> {
    let res: Response;
    try {
      res = await this.deps.fetch(url, {
        method,
        headers: { "Content-Type": "application/json", "X-API-Key": key },
        body: method === "GET" ? undefined : JSON.stringify(body),
      });
    } catch (e) {
      throw new ProviderError("unavailable", `Adyen unreachable: ${(e as Error).message}`);
    }
    const text = await res.text();
    if (res.status === 422 || res.status === 400) throw new ProviderError("declined", `Adyen ${res.status}`);
    if (!res.ok) throw new ProviderError("unavailable", `Adyen ${res.status}`);
    try {
      return JSON.parse(text) as T;
    } catch {
      throw new ProviderError("bad_response", "Adyen answered no JSON");
    }
  }

  private get checkoutKey() {
    return this.secrets.API_KEY ?? "";
  }
  private get platformKey() {
    return this.secrets.PLATFORM_API_KEY ?? "";
  }

  async onboardDeveloper(dev: DeveloperRef, returnUrl: string) {
    const le = await this.call<{ id: string }>(
      `${this.hosts.lem}/legalEntities`,
      { type: "individual", reference: dev.id, individual: { email: dev.email, residentialAddress: { country: dev.country } } },
      this.platformKey
    );
    const holder = await this.call<{ id: string }>(
      `${this.hosts.bcl}/accountHolders`,
      { legalEntityId: le.id, reference: dev.id, description: "Maktaba plugin developer" },
      this.platformKey
    );
    const balance = await this.call<{ id: string }>(
      `${this.hosts.bcl}/balanceAccounts`,
      { accountHolderId: holder.id, reference: dev.id },
      this.platformKey
    );
    const link = await this.call<{ url: string }>(
      `${this.hosts.lem}/legalEntities/${encodeURIComponent(le.id)}/onboardingLinks`,
      { redirectUrl: returnUrl },
      this.platformKey
    );
    return { url: link.url, accountRef: `${holder.id}/${balance.id}` };
  }

  async payoutStatus(accountRef: string): Promise<PayoutStatus> {
    const { holder } = splitRef(accountRef);
    const h = await this.call<{
      status?: string;
      capabilities?: Record<string, { allowed?: boolean; verificationStatus?: string }>;
    }>(`${this.hosts.bcl}/accountHolders/${encodeURIComponent(holder)}`, undefined, this.platformKey, "GET");
    if (h.status && h.status !== "active") return "restricted";
    const payout = h.capabilities?.sendToTransferInstrument;
    const receive = h.capabilities?.receiveFromPlatformPayments;
    if (payout?.allowed && receive?.allowed) return "active";
    if (payout?.verificationStatus === "invalid" || receive?.verificationStatus === "invalid") return "restricted";
    return "pending";
  }

  async createCheckout(req: CheckoutRequest) {
    if (req.item.kind === "plugin" && !req.developer?.accountRef)
      throw new ProviderError("not_onboarded", "the developer has no Adyen balance account");
    if (req.split.maktaba.minor + req.split.developer.minor !== req.price.minor)
      throw new ProviderError("config", "the split does not sum to the price");
    const splits =
      req.item.kind === "plugin"
        ? [
            {
              type: "BalanceAccount",
              account: splitRef(req.developer!.accountRef!).balance,
              amount: { value: req.split.developer.minor },
              reference: `${req.purchaseId}-developer`,
            },
            { type: "Commission", amount: { value: req.split.maktaba.minor }, reference: `${req.purchaseId}-store` },
          ]
        : [{ type: "Commission", amount: { value: req.price.minor }, reference: `${req.purchaseId}-store` }];
    const s = await this.call<{ id: string; url: string; expiresAt: string }>(
      `${this.hosts.checkout}/sessions`,
      {
        merchantAccount: this.cfg.merchantAccount,
        mode: "hosted",
        amount: { value: req.price.minor, currency: req.price.currency },
        reference: req.purchaseId,
        returnUrl: req.returnUrl,
        countryCode: req.buyer.country,
        shopperEmail: req.buyer.email,
        shopperReference: req.buyer.accountId,
        splits,
        metadata: { homeId: req.homeId },
      },
      this.checkoutKey
    );
    if (!s.url) throw new ProviderError("bad_response", "Adyen answered no hosted URL");
    return { providerRef: s.id, url: s.url, expiresAt: s.expiresAt };
  }

  async parseWebhook(req: WebhookRequest): Promise<ProviderEvent[]> {
    let doc: { notificationItems?: { NotificationRequestItem: NotificationItem }[] };
    try {
      doc = JSON.parse(req.body);
    } catch {
      throw new WebhookSignatureError("not a notification");
    }
    const items = (doc.notificationItems ?? []).map((i) => i.NotificationRequestItem);
    if (items.length === 0) throw new WebhookSignatureError("no notification items");
    const out: ProviderEvent[] = [];
    for (const n of items) {
      const sig = n.additionalData?.hmacSignature ?? "";
      let ok = false;
      try {
        ok = fromBase64(sig).length === 32 && safeEqual(sig, await adyenSign(this.secrets.HMAC_KEY ?? "", n));
      } catch {
        ok = false;
      }
      // one bad item refuses the whole delivery: Adyen sends it again
      if (!ok || n.merchantAccountCode !== this.cfg.merchantAccount) throw new WebhookSignatureError();
      const success = n.success === "true";
      const type = eventType(n.eventCode, success);
      if (!type) continue;
      out.push({
        eventId: `${n.pspReference}:${n.eventCode}:${n.success}`,
        type,
        purchaseId: n.merchantReference,
        providerRef: n.originalReference || n.pspReference,
        amount: { minor: n.amount.value, currency: n.amount.currency },
        occurredAt: new Date(n.eventDate).toISOString(),
      });
    }
    return out;
  }

  webhookAck() {
    return "[accepted]";
  }

  async refund(req: RefundRequest) {
    const splits = req.developer?.accountRef
      ? [
          {
            type: "BalanceAccount",
            account: splitRef(req.developer.accountRef).balance,
            amount: { value: req.split.developer.minor },
            reference: `${req.purchaseId}-developer-refund`,
          },
          { type: "Commission", amount: { value: req.split.maktaba.minor }, reference: `${req.purchaseId}-store-refund` },
        ]
      : undefined;
    const r = await this.call<{ pspReference: string }>(
      `${this.hosts.checkout}/payments/${encodeURIComponent(req.providerRef)}/refunds`,
      {
        merchantAccount: this.cfg.merchantAccount,
        amount: { value: req.amount.minor, currency: req.amount.currency },
        reference: `${req.purchaseId}-refund`,
        ...(splits ? { splits } : {}),
      },
      this.checkoutKey
    );
    return { providerRef: r.pspReference };
  }

  async payout(): Promise<{ providerRef: string }> {
    throw new ProviderError("unsupported", "Adyen pays each developer's balance account out itself");
  }

  async quoteTax() {
    return undefined;
  }

  async submitDisputeEvidence() {
    // Adyen's Disputes API takes documents per dispute; the store admin uploads them from the
    // dispute's page in the Customer Area until that API is wired (no automatic evidence yet).
    throw new ProviderError("unsupported", "dispute evidence is sent from Adyen's Customer Area");
  }
}

export const adyenModule: ProviderModule<AdyenConfig> = {
  id: "adyen",
  secrets: ["API_KEY", "PLATFORM_API_KEY", "HMAC_KEY"],
  parseConfig(raw) {
    const merchantAccount = typeof raw.merchantAccount === "string" ? raw.merchantAccount : "";
    if (!merchantAccount) throw new ProviderError("config", "adyen: merchantAccount is required");
    const mode = raw.mode === "live" ? "live" : "test";
    const liveUrlPrefix = typeof raw.liveUrlPrefix === "string" ? raw.liveUrlPrefix : "";
    if (mode === "live" && !/^[A-Za-z0-9-]+$/.test(liveUrlPrefix))
      throw new ProviderError("config", "adyen: live needs liveUrlPrefix");
    const list = (v: unknown, d: string[]) => (Array.isArray(v) ? v.map(String) : d);
    return {
      merchantAccount,
      mode,
      liveUrlPrefix,
      // the Gulf and Egypt first (SA, AE, KW, QA, BH, OM, EG), then wherever the account is enabled
      regions: list(raw.regions, ["*"]),
      currencies: list(raw.currencies, ["USD", "EUR", "GBP", "SAR", "AED", "EGP"]),
    };
  },
  create(cfg, secrets, deps) {
    return new Adyen(cfg, secrets, deps);
  },
};
