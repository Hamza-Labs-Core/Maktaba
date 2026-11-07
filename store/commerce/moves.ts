// Moving a purchase to another home (story 54.19 §6): the buyer's store account moves a
// plugin purchase to another of its linked homes, at most MAX_MOVES times in MOVE_WINDOW_DAYS.
// The store rewrites the purchase's and its entitlement's home; the old home drops the
// entitlement at its next daily refresh (the home replaces its plugin_entitlements with what
// the store returns), and the new home gets it at its next one. Which homes are linked to the
// account is the caller's check (store-installs is another database).

import type { DB } from "./db.js";

/** The moves a purchase may make in any MOVE_WINDOW_DAYS. */
export const MAX_MOVES = 2;
export const MOVE_WINDOW_DAYS = 365;

const DAY = 86_400_000;

/** One of the account's plugin purchases, as the console's Purchases section shows it. */
export interface AccountPurchase {
  id: string;
  plugin_id: string;
  product: "one_time" | "yearly";
  home_id: string;
  status: string;
  currency: string;
  amount_minor: number;
  paid_at: string | null;
  period_end: string | null;
  /** moves left now (0 to MAX_MOVES); 0 for a purchase that cannot move at all */
  moves_left: number;
  /** when the oldest counted move leaves the window, if no move is left; else null */
  next_move_at: string | null;
}

/** The purchase states a move is allowed in: the ones that hold an entitlement. */
const MOVABLE = ["paid", "disputed"];

/** The moves counted against a purchase now: those in the last MOVE_WINDOW_DAYS. */
const windowStart = (now: Date) => new Date(now.getTime() - MOVE_WINDOW_DAYS * DAY).toISOString();

/** The account's plugin purchases (pending and failed ones left out), newest first. */
export async function accountPurchases(db: DB, accountId: string, now: Date): Promise<AccountPurchase[]> {
  const { results } = await db
    .prepare(
      `SELECT p.id, p.plugin_id, p.product, p.home_id, p.status, p.currency, p.amount_minor, p.paid_at, p.period_end,
              (SELECT count(*) FROM purchase_moves m WHERE m.purchase_id = p.id AND m.at > ?) AS moves,
              (SELECT min(m.at) FROM purchase_moves m WHERE m.purchase_id = p.id AND m.at > ?) AS oldest
         FROM purchases p
        WHERE p.account_id = ? AND p.item = 'plugin' AND p.status NOT IN ('pending', 'failed')
        ORDER BY p.paid_at DESC, p.id`,
    )
    .bind(windowStart(now), windowStart(now), accountId)
    .all<Omit<AccountPurchase, "moves_left" | "next_move_at"> & { moves: number; oldest: string | null }>();
  return results.map(({ moves, oldest, ...p }) => {
    const left = MOVABLE.includes(p.status) ? Math.max(0, MAX_MOVES - moves) : 0;
    const next =
      MOVABLE.includes(p.status) && left === 0 && oldest ? new Date(Date.parse(oldest) + MOVE_WINDOW_DAYS * DAY).toISOString() : null;
    return { ...p, moves_left: left, next_move_at: next };
  });
}

/** Why a move was refused: each is the API's error code. */
export type MoveRefusal = "not_found" | "not_movable" | "same_home" | "too_many_moves";

export class MoveRefused extends Error {
  constructor(
    readonly code: MoveRefusal,
    readonly detail: Record<string, unknown> = {},
  ) {
    super(code);
  }
}

/**
 * Moves the account's purchase to toHome, which the caller has checked is linked to the
 * account. All or nothing in one D1 batch (one transaction): the move row is written only
 * while the purchase is still on its home and under the limit, and the two home rewrites run
 * only if that row was written, so two moves at once cannot both pass the count.
 */
export async function movePurchase(
  db: DB,
  o: { moveId: string; purchaseId: string; accountId: string; toHome: string; now: Date },
): Promise<{ from_home: string; to_home: string; moves_left: number }> {
  const p = await db
    .prepare(`SELECT home_id, status FROM purchases WHERE id = ? AND account_id = ? AND item = 'plugin'`)
    .bind(o.purchaseId, o.accountId)
    .first<{ home_id: string; status: string }>();
  // another account's purchase is as unknown as a missing one
  if (!p) throw new MoveRefused("not_found");
  if (!MOVABLE.includes(p.status)) throw new MoveRefused("not_movable", { status: p.status });
  if (p.home_id === o.toHome) throw new MoveRefused("same_home");
  const since = windowStart(o.now);
  const at = o.now.toISOString();
  const [ins] = await db.batch([
    db
      .prepare(
        `INSERT INTO purchase_moves (id, purchase_id, account_id, from_home, to_home, at)
         SELECT ?, id, account_id, home_id, ?, ? FROM purchases
          WHERE id = ? AND account_id = ? AND home_id = ? AND status IN ('paid', 'disputed')
            AND (SELECT count(*) FROM purchase_moves WHERE purchase_id = ? AND at > ?) < ?`,
      )
      .bind(o.moveId, o.toHome, at, o.purchaseId, o.accountId, p.home_id, o.purchaseId, since, MAX_MOVES),
    db
      .prepare(`UPDATE purchases SET home_id = ? WHERE id = ? AND EXISTS (SELECT 1 FROM purchase_moves WHERE id = ?)`)
      .bind(o.toHome, o.purchaseId, o.moveId),
    db
      .prepare(`UPDATE entitlements SET home_id = ? WHERE purchase_id = ? AND EXISTS (SELECT 1 FROM purchase_moves WHERE id = ?)`)
      .bind(o.toHome, o.purchaseId, o.moveId),
  ]);
  if (!ins || ins.meta.changes !== 1) {
    const n = await db
      .prepare(`SELECT count(*) AS n, min(at) AS oldest FROM purchase_moves WHERE purchase_id = ? AND at > ?`)
      .bind(o.purchaseId, since)
      .first<{ n: number; oldest: string | null }>();
    if ((n?.n ?? 0) >= MAX_MOVES) {
      throw new MoveRefused("too_many_moves", {
        max: MAX_MOVES,
        next_move_at: n?.oldest ? new Date(Date.parse(n.oldest) + MOVE_WINDOW_DAYS * DAY).toISOString() : null,
      });
    }
    // the purchase changed under us (moved or refunded at the same moment)
    throw new MoveRefused("not_movable");
  }
  const n = await db
    .prepare(`SELECT count(*) AS n FROM purchase_moves WHERE purchase_id = ? AND at > ?`)
    .bind(o.purchaseId, since)
    .first<{ n: number }>();
  return { from_home: p.home_id, to_home: o.toHome, moves_left: Math.max(0, MAX_MOVES - (n?.n ?? 0)) };
}
