// Selling, refunding and paying developers (story 54.19 §3–§7), provider-neutral: each step
// asks the registry for a provider and records the result in the store's own ledger.

import type { ProviderRegistry } from "../payments/registry.js";
import { type DeveloperRef, type Money, ProviderError } from "../payments/provider.js";
import type { DB } from "./db.js";
import { SELLER_FEE, balances, createPurchase, developerParty, getPurchase, setProviderRef } from "./ledger.js";
import { split } from "./split.js";

/** Refusals the store API answers with (403 and the code). */
export class SaleRefused extends Error {
  constructor(
    readonly code:
      | "seller_fee_required"
      | "payouts_required"
      | "not_for_sale"
      | "no_provider_for_region"
      | "no_provider_for_currency"
      | "no_provider"
      | "refund_window_over"
      | "not_refundable"
      | "price_out_of_range"
      | "free_stays_free"
  ) {
    super(code);
    this.name = "SaleRefused";
  }
}

/** Store limits on a price: $1–$200, or the same in the other currencies (minor units). */
export const PRICE_RANGE: Record<string, [number, number]> = {
  USD: [100, 20000],
  EUR: [100, 20000],
  GBP: [100, 17000],
  SAR: [375, 75000],
  AED: [370, 73500],
  EGP: [5000, 1000000],
};

/** The smallest payout sent; less rolls over to the next month (54.19 §3, $25). */
export const PAYOUT_MINIMUM: Record<string, number> = {
  USD: 2500,
  EUR: 2500,
  GBP: 2000,
  SAR: 9400,
  AED: 9200,
  EGP: 120000,
};

export const REFUND_DAYS = 14;

/** A developer may price a plugin once the seller fee is paid and a provider can pay them. */
export async function checkCanSell(db: DB, developerId: string): Promise<void> {
  const fee = await db.prepare(`SELECT 1 AS x FROM seller_fees WHERE developer_id = ?`).bind(developerId).first();
  if (!fee) throw new SaleRefused("seller_fee_required");
  const acct = await db
    .prepare(`SELECT 1 AS x FROM provider_accounts WHERE developer_id = ? AND status = 'active'`)
    .bind(developerId)
    .first();
  if (!acct) throw new SaleRefused("payouts_required");
}

/** Sets a product's price; a listed free plugin never becomes paid (54.19 §4). */
export async function setPrice(
  db: DB,
  developerId: string,
  pluginId: string,
  kind: "one_time" | "yearly",
  price: Money,
  listedFree: boolean
): Promise<void> {
  if (listedFree) throw new SaleRefused("free_stays_free");
  await checkCanSell(db, developerId);
  const range = PRICE_RANGE[price.currency];
  if (!range || price.minor < range[0] || price.minor > range[1]) throw new SaleRefused("price_out_of_range");
  await db.batch([
    db
      .prepare(`INSERT INTO products (plugin_id, kind, developer_id, active) VALUES (?, ?, ?, 1)
                ON CONFLICT (plugin_id, kind) DO UPDATE SET active = 1`)
      .bind(pluginId, kind, developerId),
    db
      .prepare(`INSERT INTO prices (plugin_id, kind, currency, amount_minor) VALUES (?, ?, ?, ?)
                ON CONFLICT (plugin_id, kind, currency) DO UPDATE SET amount_minor = excluded.amount_minor`)
      .bind(pluginId, kind, price.currency, price.minor),
  ]);
}

async function developerRef(db: DB, developerId: string, providerId: string, email = "", country = ""): Promise<DeveloperRef> {
  const a = await db
    .prepare(`SELECT account_ref FROM provider_accounts WHERE developer_id = ? AND provider_id = ?`)
    .bind(developerId, providerId)
    .first<{ account_ref: string }>();
  return { id: developerId, email, country, ...(a ? { accountRef: a.account_ref } : {}) };
}

export interface Buyer {
  accountId: string;
  email: string;
  country: string;
}

/** Starts a plugin purchase for a linked home: the hosted checkout's URL. */
export async function startPurchase(
  db: DB,
  registry: ProviderRegistry,
  x: { purchaseId: string; buyer: Buyer; homeId: string; pluginId: string; kind: "one_time" | "yearly"; currency: string; returnUrl: string; now: Date }
): Promise<{ url: string; providerId: string }> {
  const prod = await db
    .prepare(
      `SELECT p.developer_id, r.amount_minor FROM products p JOIN prices r ON r.plugin_id = p.plugin_id AND r.kind = p.kind
        WHERE p.plugin_id = ? AND p.kind = ? AND p.active = 1 AND r.currency = ?`
    )
    .bind(x.pluginId, x.kind, x.currency)
    .first<{ developer_id: string; amount_minor: number }>();
  if (!prod) throw new SaleRefused("not_for_sale");
  const { results: accts } = await db
    .prepare(`SELECT provider_id FROM provider_accounts WHERE developer_id = ? AND status = 'active'`)
    .bind(prod.developer_id)
    .all<{ provider_id: string }>();
  const choice = registry.choose({
    country: x.buyer.country,
    currency: x.currency,
    subscription: x.kind === "yearly",
    developerProviders: accts.map((a) => a.provider_id),
  });
  if (typeof choice === "string") throw new SaleRefused(choice);
  const price = { minor: prod.amount_minor, currency: x.currency };
  const p = await createPurchase(db, {
    id: x.purchaseId,
    item: "plugin",
    accountId: x.buyer.accountId,
    homeId: x.homeId,
    pluginId: x.pluginId,
    product: x.kind,
    developerId: prod.developer_id,
    providerId: choice.config.id,
    price,
    now: x.now,
  });
  const tax = await choice.provider.quoteTax?.(price, x.buyer.country);
  const session = await choice.provider.createCheckout({
    purchaseId: p.id,
    buyer: x.buyer,
    homeId: x.homeId,
    item: { kind: "plugin", pluginId: x.pluginId, product: x.kind },
    price,
    split: split(price),
    developer: await developerRef(db, prod.developer_id, choice.config.id),
    returnUrl: x.returnUrl,
    ...(tax ? { tax } : {}),
  });
  await setProviderRef(db, p.id, session.providerRef);
  return { url: session.url, providerId: choice.config.id };
}

/** Starts the one-time seller fee (the store sells it, in every model). */
export async function startSellerFee(
  db: DB,
  registry: ProviderRegistry,
  x: { purchaseId: string; developerId: string; buyer: Buyer; returnUrl: string; now: Date }
): Promise<{ url: string; providerId: string }> {
  const choice = registry.choose({ country: x.buyer.country, currency: SELLER_FEE.currency });
  if (typeof choice === "string") throw new SaleRefused(choice);
  const p = await createPurchase(db, {
    id: x.purchaseId,
    item: "seller_fee",
    accountId: x.buyer.accountId,
    homeId: "",
    developerId: x.developerId,
    providerId: choice.config.id,
    price: SELLER_FEE,
    now: x.now,
  });
  const session = await choice.provider.createCheckout({
    purchaseId: p.id,
    buyer: x.buyer,
    homeId: "",
    item: { kind: "seller_fee", developerId: x.developerId },
    price: SELLER_FEE,
    split: { maktaba: SELLER_FEE, developer: { minor: 0, currency: SELLER_FEE.currency } },
    returnUrl: x.returnUrl,
  });
  await setProviderRef(db, p.id, session.providerRef);
  return { url: session.url, providerId: choice.config.id };
}

/** Starts a developer's onboarding with a provider ("Set up payouts"). */
export async function startOnboarding(
  db: DB,
  registry: ProviderRegistry,
  providerId: string,
  dev: { id: string; email: string; country: string },
  returnUrl: string,
  now: Date
): Promise<string> {
  const c = registry.get(providerId);
  if (!c) throw new SaleRefused("no_provider");
  const { url, accountRef } = await c.provider.onboardDeveloper(dev, returnUrl);
  await db
    .prepare(`INSERT INTO provider_accounts (developer_id, provider_id, account_ref, status, updated_at) VALUES (?, ?, ?, 'pending', ?)
              ON CONFLICT (developer_id, provider_id) DO UPDATE SET account_ref = excluded.account_ref, updated_at = excluded.updated_at`)
    .bind(dev.id, providerId, accountRef, now.toISOString())
    .run();
  return url;
}

/** Asks the provider for a developer's payout status and records it. */
export async function refreshPayoutStatus(db: DB, registry: ProviderRegistry, developerId: string, providerId: string, now: Date) {
  const c = registry.get(providerId);
  const a = await db
    .prepare(`SELECT account_ref FROM provider_accounts WHERE developer_id = ? AND provider_id = ?`)
    .bind(developerId, providerId)
    .first<{ account_ref: string }>();
  if (!c || !a) return "pending" as const;
  const status = await c.provider.payoutStatus(a.account_ref);
  await db
    .prepare(`UPDATE provider_accounts SET status = ?, updated_at = ? WHERE developer_id = ? AND provider_id = ?`)
    .bind(status, now.toISOString(), developerId, providerId)
    .run();
  return status;
}

/**
 * Asks the provider that took the sale for a full refund. Within 14 days on the buyer's
 * request; later only a store admin (`asAdmin`). The ledger moves when the provider's
 * refund event arrives, never before.
 */
export async function requestRefund(
  db: DB,
  registry: ProviderRegistry,
  purchaseId: string,
  reason: "buyer_request" | "store_admin" | "revoked_malware",
  now: Date
): Promise<void> {
  const p = await getPurchase(db, purchaseId);
  if (!p || (p.status !== "paid" && p.status !== "disputed") || !p.paid_at) throw new SaleRefused("not_refundable");
  if (reason === "buyer_request" && now.getTime() - new Date(p.paid_at).getTime() > REFUND_DAYS * 86_400_000)
    throw new SaleRefused("refund_window_over");
  const c = registry.get(p.provider_id);
  if (!c) throw new ProviderError("unavailable", "the provider that took this sale is not enabled");
  await c.provider.refund({
    purchaseId: p.id,
    providerRef: p.provider_ref,
    amount: { minor: p.amount_minor, currency: p.currency },
    split: { maktaba: { minor: p.maktaba_minor, currency: p.currency }, developer: { minor: p.developer_minor, currency: p.currency } },
    developer: await developerRef(db, p.developer_id, p.provider_id),
    reason,
  });
}

export interface PayoutLine {
  developerId: string;
  providerId: string;
  currency: string;
  minor: number;
  status: "draft" | "rolled_over" | "settled_by_provider";
}

/**
 * The monthly payout run (54.19 §3) up to the end of `period` ("2026-11"): for each
 * developer and store_batches provider, what the ledger says the developer holds (sales
 * less refunds, lost disputes and earlier payouts). Under the minimum it rolls over; else
 * a draft payout a store admin approves. A provider_splits provider paid the developer
 * itself: a report line only.
 */
export async function runPayouts(db: DB, registry: ProviderRegistry, period: string, now: Date): Promise<PayoutLine[]> {
  const [y, m] = period.split("-").map(Number) as [number, number];
  const end = new Date(Date.UTC(y, m, 1));
  const { results: devs } = await db
    .prepare(`SELECT DISTINCT substr(party, 11) AS id FROM ledger_entries WHERE party LIKE 'developer:%' ORDER BY id`)
    .all<{ id: string }>();
  const out: PayoutLine[] = [];
  const writes = [];
  for (const d of devs) {
    for (const b of await balances(db, developerParty(d.id), new Date(end.getTime() - 1))) {
      if (b.minor <= 0) continue;
      const c = registry.get(b.provider_id);
      const kind = c?.provider.capabilities.payouts;
      if (kind === "provider_splits" || kind === "none") {
        out.push({ developerId: d.id, providerId: b.provider_id, currency: b.currency, minor: b.minor, status: "settled_by_provider" });
        continue;
      }
      const status = b.minor < (PAYOUT_MINIMUM[b.currency] ?? Number.MAX_SAFE_INTEGER) ? "rolled_over" : "draft";
      out.push({ developerId: d.id, providerId: b.provider_id, currency: b.currency, minor: b.minor, status });
      writes.push(
        db
          .prepare(`INSERT INTO payouts (id, developer_id, provider_id, period, currency, amount_minor, status, created_at)
                    VALUES (?, ?, ?, ?, ?, ?, ?, ?)
                    ON CONFLICT (developer_id, provider_id, period, currency) DO UPDATE SET amount_minor = excluded.amount_minor,
                      status = excluded.status WHERE payouts.status IN ('draft', 'rolled_over')`)
          .bind(`po_${period}_${d.id}_${b.provider_id}_${b.currency}`, d.id, b.provider_id, period, b.currency, b.minor, status, now.toISOString())
      );
    }
  }
  if (writes.length) await db.batch(writes);
  return out;
}

/**
 * A store admin approves a draft payout (after re-auth, enforced by the route) and it is
 * sent: the ledger takes it from the developer at once; a payout.failed event gives it back.
 */
export async function approvePayout(db: DB, registry: ProviderRegistry, payoutId: string, adminId: string, now: Date): Promise<void> {
  const p = await db
    .prepare(`SELECT * FROM payouts WHERE id = ? AND status = 'draft'`)
    .bind(payoutId)
    .first<{ id: string; developer_id: string; provider_id: string; period: string; currency: string; amount_minor: number }>();
  if (!p) throw new SaleRefused("not_for_sale");
  const c = registry.get(p.provider_id);
  if (!c) throw new ProviderError("unavailable", "the provider is not enabled");
  const at = now.toISOString();
  await db.batch([
    db.prepare(`UPDATE payouts SET status = 'approved', approved_by = ? WHERE id = ?`).bind(adminId, p.id),
    db
      .prepare(`INSERT INTO ledger_entries (payout_id, provider_id, kind, party, amount_minor, currency, at) VALUES (?, ?, 'payout', ?, ?, ?, ?)`)
      .bind(p.id, p.provider_id, developerParty(p.developer_id), -p.amount_minor, p.currency, at),
  ]);
  const { providerRef } = await c.provider.payout({
    payoutId: p.id,
    developer: await developerRef(db, p.developer_id, p.provider_id),
    amount: { minor: p.amount_minor, currency: p.currency },
    period: p.period,
  });
  await db.prepare(`UPDATE payouts SET status = 'sent', provider_ref = ? WHERE id = ?`).bind(providerRef, p.id).run();
}
