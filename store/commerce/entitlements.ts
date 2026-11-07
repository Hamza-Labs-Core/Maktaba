// Plugin entitlements, signed by the store (story 54.19 §6).
//
// The store signs with its entitlement key E (story 54.14: `use: "entitlement"` in
// store-keys.json, under the plugin root every server embeds), as a signed envelope of kind
// "entitlement" (store/signing, the same format and message as every other signed object
// of the chain). The home checks it offline (shared/plugins/pluginsig, VerifyEntitlement)
// and never learns which payment provider took the sale: an entitlement names a purchase,
// a plugin and a home, nothing else.

import { KIND, type Signer, sha256Hex, fromB64, signEnvelope } from "../signing/sig.js";
import type { DB } from "./db.js";

/** One entitlement's signed object (version 1). */
export interface EntitlementBody {
  v: 1;
  /** the store's home id (never a cloud id) */
  home_id: string;
  /** sha256 (hex) of the home store key's raw public key, so a copy on another home is refused */
  home_key_fp: string;
  plugin_id: string;
  product: "one_time" | "yearly";
  purchase_id: string;
  valid_from: string;
  valid_until: string;
  issued_at: string;
}

/** The days an entitlement lasts before the home must refresh it (Epic 16's numbers). */
export const ENTITLEMENT_DAYS = 31;

/** RFC 3339 in UTC without fractions of a second, as the chain writes every time. */
export const rfc3339 = (d: Date) => d.toISOString().replace(/\.\d{3}Z$/, "Z");

/** The fingerprint the entitlement names: sha256 (hex) of the home store key (base64 raw). */
export async function homeKeyFingerprint(publicKeyB64: string): Promise<string> {
  return sha256Hex(fromB64(publicKeyB64));
}

/** One signed entitlement envelope (the canonical JSON text). */
export async function signEntitlement(signer: Signer, body: EntitlementBody): Promise<string> {
  return signEnvelope(KIND.entitlement, body, signer);
}

const addDays = (d: Date, n: number) => new Date(d.getTime() + n * 86_400_000);

/**
 * The entitlements a home holds now (GET /v1/homes/{id}/entitlements, refreshed daily): one
 * signed envelope per purchase that is paid and not ended. A one-time purchase rolls 31
 * days ahead at every refresh, forever while it is not refunded; a yearly one runs to its
 * period end, at most 31 days ahead.
 */
export async function homeEntitlements(
  db: DB,
  homeId: string,
  homeKeyFP: string,
  signer: Signer,
  now: Date,
): Promise<string[]> {
  const { results } = await db
    .prepare(
      `SELECT e.purchase_id, e.plugin_id, e.product, e.period_end, p.paid_at
         FROM entitlements e JOIN purchases p ON p.id = e.purchase_id
        WHERE e.home_id = ? AND e.ended_at IS NULL AND p.status IN ('paid', 'disputed')
        ORDER BY e.plugin_id, e.purchase_id`,
    )
    .bind(homeId)
    .all<{ purchase_id: string; plugin_id: string; product: "one_time" | "yearly"; period_end: string | null; paid_at: string }>();
  const out: string[] = [];
  for (const r of results) {
    let until = addDays(now, ENTITLEMENT_DAYS);
    if (r.product === "yearly" && r.period_end) {
      const end = new Date(r.period_end);
      if (end <= now) continue;
      if (end < until) until = end;
    }
    out.push(
      await signEntitlement(signer, {
        v: 1,
        home_id: homeId,
        home_key_fp: homeKeyFP,
        plugin_id: r.plugin_id,
        product: r.product,
        purchase_id: r.purchase_id,
        valid_from: rfc3339(new Date(r.paid_at)),
        valid_until: rfc3339(until),
        issued_at: rfc3339(now),
      }),
    );
  }
  return out;
}
