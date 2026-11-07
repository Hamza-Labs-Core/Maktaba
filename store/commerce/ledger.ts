// The store's own ledger (story 54.19 §3–§7): purchases, signed movements per party and
// the provider events that caused them. Provider-neutral: a ProviderEvent from any module
// is applied the same way, keyed by (provider id, event id) so each is applied once.

import type { Money, ProviderEvent } from "../payments/provider.js";
import type { DB, Stmt } from "./db.js";
import { split } from "./split.js";

export interface NewPurchase {
  id: string;
  item: "plugin" | "seller_fee";
  accountId: string;
  homeId: string;
  pluginId?: string;
  product?: "one_time" | "yearly";
  developerId: string;
  providerId: string;
  price: Money;
  now: Date;
}

export interface Purchase {
  id: string;
  item: "plugin" | "seller_fee";
  account_id: string;
  home_id: string;
  plugin_id: string | null;
  product: "one_time" | "yearly" | null;
  developer_id: string;
  provider_id: string;
  provider_ref: string;
  currency: string;
  amount_minor: number;
  maktaba_minor: number;
  developer_minor: number;
  status: "pending" | "paid" | "failed" | "refunded" | "disputed" | "lost";
  period_end: string | null;
  created_at: string;
  paid_at: string | null;
}

/** The seller fee is the store's own sale: all of it is Maktaba's (54.19 §3). */
export const SELLER_FEE: Money = { minor: 2500, currency: "USD" };

export const developerParty = (id: string) => `developer:${id}`;

/** Records a sale before checkout, with its split (the seller fee: all the store's). */
export async function createPurchase(db: DB, p: NewPurchase): Promise<Purchase> {
  const s = p.item === "seller_fee" ? { maktaba: p.price, developer: { minor: 0, currency: p.price.currency } } : split(p.price);
  await db
    .prepare(
      `INSERT INTO purchases (id, item, account_id, home_id, plugin_id, product, developer_id, provider_id,
         currency, amount_minor, maktaba_minor, developer_minor, status, created_at)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?)`
    )
    .bind(
      p.id,
      p.item,
      p.accountId,
      p.homeId,
      p.pluginId ?? null,
      p.product ?? null,
      p.developerId,
      p.providerId,
      p.price.currency,
      p.price.minor,
      s.maktaba.minor,
      s.developer.minor,
      p.now.toISOString()
    )
    .run();
  return (await getPurchase(db, p.id))!;
}

export async function getPurchase(db: DB, id: string): Promise<Purchase | null> {
  return db.prepare(`SELECT * FROM purchases WHERE id = ?`).bind(id).first<Purchase>();
}

export async function setProviderRef(db: DB, id: string, ref: string): Promise<void> {
  await db.prepare(`UPDATE purchases SET provider_ref = ? WHERE id = ?`).bind(ref, id).run();
}

export interface Applied {
  applied: number;
  duplicates: number;
  /** events about something the store has no record of (logged, never an error) */
  unknown: number;
}

const yearAfter = (iso: string) => {
  const d = new Date(iso);
  d.setUTCFullYear(d.getUTCFullYear() + 1);
  return d.toISOString();
};

/**
 * Applies a provider's events, each once, each in one transaction with its record in
 * provider_events: a webhook answered 200 only after this returns, so the provider sends
 * again anything that did not commit.
 */
export async function applyEvents(db: DB, providerId: string, events: ProviderEvent[], now: Date): Promise<Applied> {
  const out: Applied = { applied: 0, duplicates: 0, unknown: 0 };
  for (const e of events) {
    const seen = await db
      .prepare(`SELECT 1 AS x FROM provider_events WHERE provider_id = ? AND event_id = ?`)
      .bind(providerId, e.eventId)
      .first();
    if (seen) {
      out.duplicates++;
      continue;
    }
    const record = db
      .prepare(`INSERT INTO provider_events (provider_id, event_id, type, received_at) VALUES (?, ?, ?, ?)`)
      .bind(providerId, e.eventId, e.type, now.toISOString());
    const stmts = await statementsFor(db, providerId, e, now);
    if (stmts === null) out.unknown++;
    else out.applied++;
    try {
      await db.batch([record, ...(stmts ?? [])]);
    } catch (err) {
      // the same event committed by a concurrent delivery: the unique key refused this one
      if (/UNIQUE|PRIMARY KEY|constraint/i.test(String(err))) {
        out.duplicates++;
        if (stmts === null) out.unknown--;
        else out.applied--;
        continue;
      }
      throw err;
    }
  }
  return out;
}

/** The writes one event makes; null when it is about nothing the store knows. */
async function statementsFor(db: DB, providerId: string, e: ProviderEvent, now: Date): Promise<Stmt[] | null> {
  const when = new Date(e.occurredAt);
  const at = (Number.isNaN(when.getTime()) ? now : when).toISOString();
  if (e.type === "payout.paid" || e.type === "payout.failed") {
    if (!e.payoutId) return null;
    const p = await db
      .prepare(`SELECT * FROM payouts WHERE id = ? AND provider_id = ?`)
      .bind(e.payoutId, providerId)
      .first<{ id: string; developer_id: string; currency: string; amount_minor: number; status: string }>();
    if (!p) return null;
    if (e.type === "payout.paid") return [db.prepare(`UPDATE payouts SET status = 'paid' WHERE id = ?`).bind(p.id)];
    if (p.status === "failed") return [];
    return [
      db.prepare(`UPDATE payouts SET status = 'failed' WHERE id = ?`).bind(p.id),
      // the money is the developer's again, for the next run
      ledger(db, { payoutId: p.id, providerId, kind: "payout_failed", party: developerParty(p.developer_id), minor: p.amount_minor, currency: p.currency, eventId: e.eventId, at }),
    ];
  }
  if (e.type === "account.updated") {
    if (!e.accountRef || !e.accountStatus) return null;
    return [
      db
        .prepare(`UPDATE provider_accounts SET status = ?, updated_at = ? WHERE provider_id = ? AND account_ref = ?`)
        .bind(e.accountStatus, at, providerId, e.accountRef),
    ];
  }
  if (!e.purchaseId) return null;
  const p = await getPurchase(db, e.purchaseId);
  if (!p || p.provider_id !== providerId) return null;
  const both = (kind: string, sign: 1 | -1) => [
    ledger(db, { purchaseId: p.id, providerId, kind, party: "maktaba", minor: sign * p.maktaba_minor, currency: p.currency, eventId: e.eventId, at }),
    ...(p.developer_minor
      ? [ledger(db, { purchaseId: p.id, providerId, kind, party: developerParty(p.developer_id), minor: sign * p.developer_minor, currency: p.currency, eventId: e.eventId, at })]
      : []),
  ];
  const status = (s: Purchase["status"]) => db.prepare(`UPDATE purchases SET status = ? WHERE id = ?`).bind(s, p.id);
  const endEntitlement = (why: string) =>
    db.prepare(`UPDATE entitlements SET ended_at = ?, ended_why = ? WHERE purchase_id = ? AND ended_at IS NULL`).bind(at, why, p.id);
  switch (e.type) {
    case "payment.succeeded": {
      if (p.status !== "pending" && p.status !== "failed") return [];
      if (e.amount && (e.amount.minor !== p.amount_minor || e.amount.currency !== p.currency)) return null;
      const out = [
        db
          .prepare(`UPDATE purchases SET status = 'paid', paid_at = ?, period_end = ?, provider_ref = COALESCE(NULLIF(provider_ref, ''), ?) WHERE id = ?`)
          .bind(at, p.product === "yearly" ? yearAfter(at) : null, e.providerRef, p.id),
        ...both("sale", 1),
      ];
      if (p.item === "seller_fee")
        out.push(db.prepare(`INSERT OR IGNORE INTO seller_fees (developer_id, purchase_id, paid_at) VALUES (?, ?, ?)`).bind(p.developer_id, p.id, at));
      else
        out.push(
          db
            .prepare(`INSERT OR IGNORE INTO entitlements (purchase_id, home_id, plugin_id, product, period_end) VALUES (?, ?, ?, ?, ?)`)
            .bind(p.id, p.home_id, p.plugin_id, p.product, p.product === "yearly" ? yearAfter(at) : null)
        );
      return out;
    }
    case "payment.failed":
      return p.status === "pending" ? [status("failed")] : [];
    case "refund.succeeded":
      if (p.status !== "paid" && p.status !== "disputed") return [];
      return [status("refunded"), ...both("refund", -1), endEntitlement("refunded")];
    case "refund.failed":
      return [];
    case "dispute.opened":
      return p.status === "paid" ? [status("disputed")] : [];
    case "dispute.won":
      return p.status === "disputed" ? [status("paid")] : [];
    case "dispute.lost":
      if (p.status !== "paid" && p.status !== "disputed") return [];
      return [status("lost"), ...both("dispute_lost", -1), endEntitlement("dispute_lost")];
    default:
      return null;
  }
}

function ledger(
  db: DB,
  x: { purchaseId?: string; payoutId?: string; providerId: string; kind: string; party: string; minor: number; currency: string; eventId?: string; at: string }
): Stmt {
  return db
    .prepare(
      `INSERT INTO ledger_entries (purchase_id, payout_id, provider_id, kind, party, amount_minor, currency, provider_event_id, at)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
    )
    .bind(x.purchaseId ?? null, x.payoutId ?? null, x.providerId, x.kind, x.party, x.minor, x.currency, x.eventId ?? null, x.at);
}

/** What a party holds, per currency and provider (the ledger's sum). */
export async function balances(db: DB, party: string, until?: Date) {
  const { results } = await db
    .prepare(
      `SELECT provider_id, currency, SUM(amount_minor) AS minor FROM ledger_entries
        WHERE party = ? AND at <= ? GROUP BY provider_id, currency ORDER BY provider_id, currency`
    )
    .bind(party, until ? until.toISOString() : "9999-12-31T23:59:59.999Z")
    .all<{ provider_id: string; currency: string; minor: number }>();
  return results;
}
