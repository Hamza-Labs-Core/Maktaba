// The buyer's purchases on the account page (story 54.19 §6): GET /v1/purchases lists the
// account's plugin purchases with the home each is on; POST /v1/purchases/{id}/move
// {home_id} moves one to another home linked to the same account, after re-authentication,
// at most twice in 365 days (commerce/moves.ts). Nothing here names a payment provider.
import type { DB } from "../commerce/db";
import { MoveRefused, accountPurchases, movePurchase } from "../commerce/moves";
import { type Ctx, audit, limit, reauthed, signedIn, verified } from "./auth";
import { newId } from "./crypto";
import { now } from "./env";
import { body, json, refuse, str } from "./http";

const commerce = (ctx: Ctx): DB => {
  if (!ctx.env.DB_COMMERCE) refuse(503, "purchases_unavailable");
  return ctx.env.DB_COMMERCE as unknown as DB;
};

/** GET /v1/purchases: the account's purchases, each with its home's name (linked or not). */
export async function listPurchases(ctx: Ctx): Promise<Response> {
  const a = signedIn(ctx);
  const list = await accountPurchases(commerce(ctx), a.id, now());
  const { results } = await ctx.env.DB_INSTALLS.prepare("SELECT id, name, unlinked_at FROM homes WHERE account = ?")
    .bind(a.id)
    .all<{ id: string; name: string; unlinked_at: string | null }>();
  const homes = new Map(results.map((h) => [h.id, h]));
  return json({
    purchases: list.map((p) => ({
      ...p,
      home_name: homes.get(p.home_id)?.name ?? "",
      home_linked: !!homes.get(p.home_id) && !homes.get(p.home_id)!.unlinked_at,
    })),
  });
}

/** POST /v1/purchases/{id}/move {home_id}: to another home linked to this account. */
export async function movePurchaseRoute(ctx: Ctx, id: string): Promise<Response> {
  const a = verified(ctx);
  reauthed(ctx);
  await limit(ctx, "purchase_move", 20, 3600_000);
  const b = await body(ctx.req);
  const to = str(b.home_id, "home_id", 40);
  const linked = await ctx.env.DB_INSTALLS.prepare("SELECT 1 FROM homes WHERE id = ? AND account = ? AND unlinked_at IS NULL")
    .bind(to, a.id)
    .first();
  // another account's home, or an unlinked one, is not a place this purchase can go
  if (!linked) {
    await audit(ctx, a.id, "purchase.move", id, "home_not_linked");
    refuse(404, "home_not_linked");
  }
  try {
    const r = await movePurchase(commerce(ctx), { moveId: newId("mv"), purchaseId: id, accountId: a.id, toHome: to, now: now() });
    await audit(ctx, a.id, "purchase.move", `${id}:${r.from_home}>${r.to_home}`);
    return json({ status: "moved", purchase_id: id, ...r });
  } catch (e) {
    if (!(e instanceof MoveRefused)) throw e;
    await audit(ctx, a.id, "purchase.move", id, e.code);
    const status = e.code === "not_found" ? 404 : 409;
    return refuse(status, e.code, e.detail);
  }
}
