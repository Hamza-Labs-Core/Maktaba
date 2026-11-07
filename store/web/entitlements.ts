// GET /v1/homes/{id}/entitlements (story 54.19 §5, §6): the entitlements a linked home holds,
// signed now by the store's entitlement key E. The home asks with its own signature
// (MKS-Home, 54.15 §5) daily, and every 15 s for 15 minutes after a Buy it started; it
// checks each offline (shared/plugins/pluginsig, CheckEntitlement). Nothing here names a
// payment provider.

import { homeEntitlements, homeKeyFingerprint } from "../commerce/entitlements";
import type { DB } from "../commerce/db";
import { Signer } from "../signing/sig";
import { type Env, now } from "./env";
import { signedHome } from "./homes";
import { json, refuse } from "./http";

export async function entitlements(env: Env, req: Request, id: string): Promise<Response> {
  const raw = new Uint8Array(await req.arrayBuffer());
  if (raw.length > 0) refuse(400, "body_not_empty");
  const home = await signedHome(env, req, raw);
  if (!home || home.id !== id) refuse(401, "home_signature_invalid");
  if (!env.ENTITLEMENT_SIGNING_KEY || !env.DB_COMMERCE) refuse(503, "entitlements_unavailable");
  const signer = await Signer.fromSecret(env.ENTITLEMENT_SIGNING_KEY!);
  const at = now();
  const list = await homeEntitlements(env.DB_COMMERCE as unknown as DB, home!.id, await homeKeyFingerprint(home!.public_key), signer, at);
  return json({ home_id: home!.id, issued_at: at.toISOString().replace(/\.\d{3}Z$/, "Z"), entitlements: list.map((e) => JSON.parse(e)) });
}
