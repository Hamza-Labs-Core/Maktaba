// Story 54.19 §6, moving a purchase to another home, on the real store-commerce schema: the
// purchase and its entitlement follow the move, the old home's entitlements end at its next
// refresh, at most two moves in 365 days, and every refusal changes nothing.

import { describe, expect, it } from "vitest";
import { T0, testDB, testSigner } from "../testing/harness.js";
import { homeEntitlements } from "./entitlements.js";
import { MAX_MOVES, MoveRefused, accountPurchases, movePurchase } from "./moves.js";

const DAY = 86_400_000;
const at = (days: number) => new Date(T0.getTime() + days * DAY);

async function store() {
  const db = testDB();
  const run = (sql: string, ...v: unknown[]) =>
    db
      .prepare(sql)
      .bind(...v)
      .run();
  const purchase = async (id: string, o: { account?: string; home?: string; status?: string; item?: string } = {}) => {
    const item = o.item ?? "plugin";
    await run(
      `INSERT INTO purchases (id, item, account_id, home_id, plugin_id, product, developer_id, provider_id, currency,
                              amount_minor, maktaba_minor, developer_minor, status, created_at, paid_at)
       VALUES (?, ?, ?, ?, ?, ?, 'dev-1', 'fake', 'USD', 1000, 150, 850, ?, ?, ?)`,
      id,
      item,
      o.account ?? "acct-1",
      o.home ?? "home-1",
      item === "plugin" ? "dev.example.loudness" : null,
      item === "plugin" ? "one_time" : null,
      o.status ?? "paid",
      T0.toISOString(),
      T0.toISOString(),
    );
    if (item === "plugin") {
      await run(
        `INSERT INTO entitlements (purchase_id, home_id, plugin_id, product) VALUES (?, ?, 'dev.example.loudness', 'one_time')`,
        id,
        o.home ?? "home-1",
      );
    }
  };
  const homeOf = async (id: string) => {
    const p = await db.prepare(`SELECT home_id FROM purchases WHERE id = ?`).bind(id).first<{ home_id: string }>();
    const e = await db.prepare(`SELECT home_id FROM entitlements WHERE purchase_id = ?`).bind(id).first<{ home_id: string }>();
    return [p?.home_id, e?.home_id];
  };
  const moves = async () => (await db.prepare(`SELECT count(*) AS n FROM purchase_moves`).first<{ n: number }>())!.n;
  let n = 0;
  const move = (purchaseId: string, toHome: string, now = T0, accountId = "acct-1") =>
    movePurchase(db, { moveId: `mv-${n++}`, purchaseId, accountId, toHome, now });
  return { db, purchase, homeOf, moves, move };
}

const refusal = async (p: Promise<unknown>) => {
  try {
    await p;
  } catch (e) {
    if (e instanceof MoveRefused) return { code: e.code, detail: e.detail };
    throw e;
  }
  throw new Error("not refused");
};

describe("moving a purchase to another home", () => {
  it("the purchase and its entitlement move; the old home's next refresh drops it, the new home's has it", async () => {
    const { db, purchase, homeOf, move } = await store();
    await purchase("pur-1");
    const r = await move("pur-1", "home-2");
    expect(r).toEqual({ from_home: "home-1", to_home: "home-2", moves_left: 1 });
    expect(await homeOf("pur-1")).toEqual(["home-2", "home-2"]);
    const { signer } = await testSigner();
    expect(await homeEntitlements(db, "home-1", "ab".repeat(32), signer, T0)).toEqual([]);
    const ents = await homeEntitlements(db, "home-2", "cd".repeat(32), signer, T0);
    expect(ents).toHaveLength(1);
    expect(JSON.parse(ents[0]!).signed).toMatchObject({ home_id: "home-2", purchase_id: "pur-1" });
  });

  it(`at most ${MAX_MOVES} moves in 365 days; the third waits until the oldest leaves the window`, async () => {
    const { purchase, homeOf, moves, move } = await store();
    await purchase("pur-1");
    await move("pur-1", "home-2", at(0));
    expect((await move("pur-1", "home-1", at(10))).moves_left).toBe(0);
    const r = await refusal(move("pur-1", "home-3", at(20)));
    expect(r).toEqual({ code: "too_many_moves", detail: { max: 2, next_move_at: at(365).toISOString() } });
    expect(await homeOf("pur-1")).toEqual(["home-1", "home-1"]);
    expect(await moves()).toBe(2);
    // a day after the first move left the window, one move is free again
    expect((await move("pur-1", "home-3", at(366))).moves_left).toBe(0);
    expect(await homeOf("pur-1")).toEqual(["home-3", "home-3"]);
  });

  it("the count is per purchase: another purchase of the same account still moves", async () => {
    const { purchase, move } = await store();
    await purchase("pur-1");
    await purchase("pur-2");
    await move("pur-1", "home-2");
    await move("pur-1", "home-1");
    expect((await move("pur-2", "home-2")).moves_left).toBe(1);
  });

  it("refuses another account's purchase, a seller fee, a refunded or pending one and the same home, changing nothing", async () => {
    const { purchase, homeOf, moves, move } = await store();
    await purchase("pur-other", { account: "acct-2" });
    await purchase("pur-refunded", { status: "refunded" });
    await purchase("pur-pending", { status: "pending" });
    await purchase("fee-1", { item: "seller_fee" });
    await purchase("pur-1");
    expect((await refusal(move("pur-other", "home-2"))).code).toBe("not_found");
    expect((await refusal(move("fee-1", "home-2"))).code).toBe("not_found");
    expect((await refusal(move("pur-missing", "home-2"))).code).toBe("not_found");
    expect(await refusal(move("pur-refunded", "home-2"))).toEqual({ code: "not_movable", detail: { status: "refunded" } });
    expect((await refusal(move("pur-pending", "home-2"))).code).toBe("not_movable");
    expect((await refusal(move("pur-1", "home-1"))).code).toBe("same_home");
    expect(await moves()).toBe(0);
    expect(await homeOf("pur-other")).toEqual(["home-1", "home-1"]);
    expect(await homeOf("pur-1")).toEqual(["home-1", "home-1"]);
  });

  it("a disputed purchase still holds its entitlement, so it can move", async () => {
    const { purchase, homeOf, move } = await store();
    await purchase("pur-1", { status: "disputed" });
    await move("pur-1", "home-2");
    expect(await homeOf("pur-1")).toEqual(["home-2", "home-2"]);
  });

  it("two moves read at once: the batch's own count holds the limit", async () => {
    const { db, purchase, homeOf, moves, move } = await store();
    await purchase("pur-1");
    await move("pur-1", "home-2", at(0));
    // a second move recorded behind the first one's back (as a concurrent request would)
    await db
      .prepare(
        `INSERT INTO purchase_moves (id, purchase_id, account_id, from_home, to_home, at) VALUES ('mv-x', 'pur-1', 'acct-1', 'home-2', 'home-9', ?)`,
      )
      .bind(at(1).toISOString())
      .run();
    expect((await refusal(move("pur-1", "home-3", at(2)))).code).toBe("too_many_moves");
    expect(await homeOf("pur-1")).toEqual(["home-2", "home-2"]);
    expect(await moves()).toBe(2);
  });

  it("a purchase refunded between the read and the batch is not moved", async () => {
    const { db, purchase, homeOf, moves } = await store();
    await purchase("pur-1");
    // the first read sees it paid; the refund lands before the batch runs
    const racing: typeof db = {
      ...db,
      batch: async (stmts) => {
        await db.prepare(`UPDATE purchases SET status = 'refunded' WHERE id = 'pur-1'`).run();
        return db.batch(stmts);
      },
    };
    const r = await refusal(movePurchase(racing, { moveId: "mv-r", purchaseId: "pur-1", accountId: "acct-1", toHome: "home-2", now: T0 }));
    expect(r.code).toBe("not_movable");
    expect(await homeOf("pur-1")).toEqual(["home-1", "home-1"]);
    expect(await moves()).toBe(0);
  });

  it("the account's purchases: moves left and when the next one is free; pending and failed left out", async () => {
    const { purchase, move, db } = await store();
    await purchase("pur-1");
    await purchase("pur-2", { status: "refunded" });
    await purchase("pur-3", { status: "pending" });
    await purchase("pur-4", { account: "acct-2" });
    await move("pur-1", "home-2", at(0));
    await move("pur-1", "home-1", at(1));
    const list = await accountPurchases(db, "acct-1", at(2));
    expect(list.map((p) => [p.id, p.home_id, p.status, p.moves_left, p.next_move_at])).toEqual([
      ["pur-1", "home-1", "paid", 0, at(365).toISOString()],
      ["pur-2", "home-1", "refunded", 0, null],
    ]);
    expect((await accountPurchases(db, "acct-1", at(365.5)))[0]!.moves_left).toBe(1);
  });
});
