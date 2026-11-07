// The payment provider interface (Epic 54, story 54.19 §2).
//
// Payment providers are pluggable: the store may run several at once, chosen per buyer
// region, currency or the store admin's order (registry.ts). Everything a provider does is
// behind this interface, and nothing outside `payments/providers/<id>/` knows a provider:
// purchases, the ledger, entitlements and payouts are the store's own, in its own schema
// (commerce/), and each provider's records are mapped into them here, as ProviderEvents.
// The home never sees a provider at all: it sees only entitlements the store signs.
//
// Amounts are integers in the currency's minor unit (cents, halalas, piastres), never
// floats.

/** An ISO 4217 code ("USD", "SAR", "AED", "EGP"). */
export type Currency = string;

/** An amount in a currency's minor unit. */
export interface Money {
  minor: number;
  currency: Currency;
}

/** Who sells to the buyer, which decides who files the tax (README §6.5, story 54.19 §1). */
export type SellerModel =
  /** a merchant of record sells for the store and pays the store; the store pays developers */
  | "merchant_of_record"
  /** the store sells and the provider splits each payment to the developer's sub-account */
  | "marketplace"
  /** the developer sells on their own checkout; the store invoices its share */
  | "developer_direct";

/** What a provider can do. The registry chooses only among providers that can do the job. */
export interface Capabilities {
  /** the module's id: lower case, the folder under payments/providers/ */
  id: string;
  /** the name an admin reads */
  label: string;
  model: SellerModel;
  /**
   * How developers get their 85 %: `provider_splits` (the provider splits each payment and
   * pays the developer's account itself), `store_batches` (the store sends a monthly payout
   * batch through the provider), `none` (developer_direct: the developer is paid directly).
   */
  payouts: "provider_splits" | "store_batches" | "none";
  /** yearly prices renewed by the provider */
  subscriptions: boolean;
  /** partial refunds, or full ones only */
  refunds: "full" | "partial";
  /** the provider reports disputes (chargebacks) and takes evidence */
  disputes: boolean;
  /** who works out the tax on a sale */
  tax: "provider" | "store" | "developer";
  /** ISO 3166-1 alpha-2 buyer countries it sells to ("*": everywhere it is allowed) */
  regions: string[];
  /** the currencies it charges in */
  currencies: Currency[];
}

/** A developer as a provider sees them. */
export interface DeveloperRef {
  /** the store's developer id */
  id: string;
  email: string;
  country: string;
  /** this provider's account for the developer, once onboarded */
  accountRef?: string;
}

export interface CheckoutRequest {
  /** the store's purchase id: the provider carries it back on every event */
  purchaseId: string;
  buyer: { accountId: string; email: string; country: string };
  /** the store's own home id (54.15 §5), never a cloud id */
  homeId: string;
  /** the plugin and product sold, or the seller fee (54.19 §3) */
  item:
    | { kind: "plugin"; pluginId: string; product: "one_time" | "yearly" }
    | { kind: "seller_fee"; developerId: string };
  price: Money;
  /** the split of `price` (commerce/split.ts): it sums to the price */
  split: { maktaba: Money; developer: Money };
  /** the developer paid (a plugin sale), with this provider's account for them */
  developer?: DeveloperRef;
  /** where the buyer comes back to (plugins.maktaba.tv/buy/<plugin>/done) */
  returnUrl: string;
  /** the tax the store worked out, when the store files (Capabilities.tax = "store") */
  tax?: TaxQuote;
}

export interface CheckoutSession {
  /** the provider's id for the session or payment */
  providerRef: string;
  /** the hosted page the buyer pays on */
  url: string;
  expiresAt: string;
}

/** What a webhook said, in the store's own words. */
export type ProviderEventType =
  | "payment.succeeded"
  | "payment.failed"
  | "refund.succeeded"
  | "refund.failed"
  | "dispute.opened"
  | "dispute.won"
  | "dispute.lost"
  | "payout.paid"
  | "payout.failed"
  | "account.updated";

export interface ProviderEvent {
  /** the provider's own event id: the store processes each once (provider_events) */
  eventId: string;
  type: ProviderEventType;
  /** the store's purchase id the event is about (payments, refunds, disputes) */
  purchaseId?: string;
  /** the store's payout id (payout events) */
  payoutId?: string;
  /** the provider's reference of the payment, refund, dispute or payout */
  providerRef: string;
  amount?: Money;
  /** the developer's provider account (account.updated) */
  accountRef?: string;
  accountStatus?: PayoutStatus;
  occurredAt: string;
}

/** A webhook request as the Worker received it: the raw body, before any parsing. */
export interface WebhookRequest {
  headers: Headers;
  body: string;
}

/** Whether a developer can sell through this provider. */
export type PayoutStatus = "active" | "pending" | "restricted";

export interface RefundRequest {
  purchaseId: string;
  /** the payment's provider reference */
  providerRef: string;
  amount: Money;
  /** the split being reversed (the developer's share and the store's) */
  split: { maktaba: Money; developer: Money };
  developer?: DeveloperRef;
  reason: "buyer_request" | "store_admin" | "revoked_malware";
}

export interface PayoutInstruction {
  /** the store's payout id */
  payoutId: string;
  developer: DeveloperRef;
  amount: Money;
  /** the month it pays, "2026-11" */
  period: string;
}

export interface TaxQuote {
  /** tax included in the price shown */
  inclusive: boolean;
  tax: Money;
  /** a rate in basis points (1500 = 15 %) */
  rateBp: number;
  /** who files it */
  filedBy: "provider" | "store" | "developer";
}

/** A refusal a provider answers: the store says why without naming the provider. */
export class ProviderError extends Error {
  constructor(
    readonly code:
      | "unsupported"
      | "not_onboarded"
      | "declined"
      | "unavailable"
      | "bad_response"
      | "config",
    message: string
  ) {
    super(message);
    this.name = "ProviderError";
  }
}

/** A webhook whose signature does not verify: the Worker answers 401 and stores nothing. */
export class WebhookSignatureError extends Error {
  constructor(message = "webhook signature does not verify") {
    super(message);
    this.name = "WebhookSignatureError";
  }
}

/** One payment provider, as one module implements it. */
export interface PaymentProvider {
  readonly capabilities: Capabilities;

  /** Starts the developer's identity and payout onboarding: the URL they go to. */
  onboardDeveloper(dev: DeveloperRef, returnUrl: string): Promise<{ url: string; accountRef: string }>;

  /** Whether the developer can be paid through this provider. */
  payoutStatus(accountRef: string): Promise<PayoutStatus>;

  /** A hosted checkout for one item. */
  createCheckout(req: CheckoutRequest): Promise<CheckoutSession>;

  /**
   * Verifies a webhook's signature with this provider's secret and answers what it said.
   * Throws WebhookSignatureError when it does not verify. Never trusts the body before.
   */
  parseWebhook(req: WebhookRequest): Promise<ProviderEvent[]>;

  /** The body the provider expects back once the events are stored ("" for none). */
  webhookAck(): string;

  /** Refunds a payment, reversing the developer's share. */
  refund(req: RefundRequest): Promise<{ providerRef: string }>;

  /** Sends one developer's payout (Capabilities.payouts = "store_batches" only). */
  payout(p: PayoutInstruction): Promise<{ providerRef: string }>;

  /** Tax hook: the provider's tax on a price, when it calculates tax (else undefined). */
  quoteTax?(price: Money, buyerCountry: string): Promise<TaxQuote | undefined>;

  /** Dispute flow: sends the store's evidence (purchase, entitlement, download records). */
  submitDisputeEvidence?(providerRef: string, evidence: DisputeEvidence): Promise<void>;
}

export interface DisputeEvidence {
  purchaseId: string;
  purchasedAt: string;
  /** when the home first downloaded the package with its entitlement */
  firstDownloadAt?: string;
  buyerAccountCreatedAt: string;
  /** plain text, English */
  notes: string;
}

/**
 * What a provider's module exports (payments/providers/<id>/index.ts). Adding a provider is
 * one module, one line in providers/index.ts and its configuration: no change anywhere else.
 */
export interface ProviderModule<Config = Record<string, unknown>> {
  id: string;
  /** the Worker secrets it needs, by name (PAYMENTS_<ID>_<NAME>); the owner sets them */
  secrets: readonly string[];
  /** checks the admin's configuration of this provider; throws ProviderError("config") */
  parseConfig(raw: Record<string, unknown>): Config;
  create(config: Config, secrets: Record<string, string>, deps: ProviderDeps): PaymentProvider;
}

/** What a provider may use: the network and the clock, both injected (tests use fakes). */
export interface ProviderDeps {
  fetch: typeof fetch;
  now: () => Date;
}

/** The Worker secret name of a provider's secret. */
export function secretName(providerId: string, name: string): string {
  return `PAYMENTS_${providerId.toUpperCase()}_${name}`;
}
