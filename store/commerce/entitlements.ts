// Plugin entitlements, signed by the store (story 54.19 §6).
//
// The store signs with its entitlement key E (story 54.14: `use: "entitlement"` in
// store-keys.json, under the plugin root every server embeds). The home checks the
// signature offline and never learns which payment provider took the sale: an entitlement
// names a purchase, a plugin and a home, nothing else. The format is 54.14's signature file:
// {key_id, sig (base64 Ed25519 over the canonical JSON of `signed`), signed}.

import { fromBase64, toBase64, utf8 } from "../payments/crypto.js";
import type { DB } from "./db.js";

/** One entitlement's signed body (version 1). */
export interface EntitlementBody {
  v: 1;
  /** the store's home id (never a cloud id) */
  home_id: string;
  /** sha256 (hex) of the home's store key, so a copy on another home is refused */
  home_key_fp: string;
  plugin_id: string;
  product: "one_time" | "yearly";
  purchase_id: string;
  valid_from: string;
  valid_until: string;
  issued_at: string;
}

export interface SignedEntitlement {
  key_id: string;
  sig: string;
  signed: EntitlementBody;
}

/** The days an entitlement lasts before the home must refresh it (Epic 16's numbers). */
export const ENTITLEMENT_DAYS = 31;

/** Canonical JSON (54.14): keys sorted, no spaces. */
export function canonical(v: unknown): string {
  if (v === null || typeof v !== "object") return JSON.stringify(v);
  if (Array.isArray(v)) return `[${v.map(canonical).join(",")}]`;
  const o = v as Record<string, unknown>;
  return `{${Object.keys(o)
    .filter((k) => o[k] !== undefined)
    .sort()
    .map((k) => `${JSON.stringify(k)}:${canonical(o[k])}`)
    .join(",")}}`;
}

export interface Signer {
  keyId: string;
  sign(message: Uint8Array): Promise<Uint8Array>;
}

/** The entitlement key E, from its Worker secret (a PKCS #8 Ed25519 private key, base64). */
export async function ed25519Signer(keyId: string, pkcs8Base64: string): Promise<Signer> {
  const key = await crypto.subtle.importKey("pkcs8", fromBase64(pkcs8Base64), { name: "Ed25519" }, false, ["sign"]);
  return {
    keyId,
    sign: async (m) => new Uint8Array(await crypto.subtle.sign({ name: "Ed25519" }, key, m)),
  };
}

export async function signEntitlement(signer: Signer, body: EntitlementBody): Promise<SignedEntitlement> {
  const sig = await signer.sign(utf8(canonical(body)));
  return { key_id: signer.keyId, sig: toBase64(sig), signed: body };
}

/** The home's store key's fingerprint, from store-installs' homes (54.15 §5). */
export type HomeKeyFingerprint = (homeId: string) => Promise<string | null>;

const addDays = (d: Date, n: number) => new Date(d.getTime() + n * 86_400_000);

/**
 * The entitlements a home holds now (GET /v1/homes/{id}/entitlements, refreshed daily):
 * one signed body per purchase that is paid and not ended. A one-time purchase rolls 31
 * days ahead at every refresh, forever while it is not refunded; a yearly one runs to its
 * period end, at most 31 days ahead.
 */
export async function homeEntitlements(
  db: DB,
  homeId: string,
  fingerprint: HomeKeyFingerprint,
  signer: Signer,
  now: Date
): Promise<SignedEntitlement[]> {
  const fp = await fingerprint(homeId);
  if (!fp) return [];
  const { results } = await db
    .prepare(
      `SELECT e.purchase_id, e.plugin_id, e.product, e.period_end, p.paid_at
         FROM entitlements e JOIN purchases p ON p.id = e.purchase_id
        WHERE e.home_id = ? AND e.ended_at IS NULL AND p.status IN ('paid', 'disputed')
        ORDER BY e.plugin_id, e.purchase_id`
    )
    .bind(homeId)
    .all<{ purchase_id: string; plugin_id: string; product: "one_time" | "yearly"; period_end: string | null; paid_at: string }>();
  const out: SignedEntitlement[] = [];
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
        home_key_fp: fp,
        plugin_id: r.plugin_id,
        product: r.product,
        purchase_id: r.purchase_id,
        valid_from: r.paid_at,
        valid_until: until.toISOString(),
        issued_at: now.toISOString(),
      })
    );
  }
  return out;
}
