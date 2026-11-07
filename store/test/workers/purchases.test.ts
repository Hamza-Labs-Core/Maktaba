// Story 54.19 §6 through the Worker (T05's move routes): the buyer lists the account's
// purchases and moves one to another home linked to the same account. Another account, an
// unverified one, another account's home and an unlinked home are refused, and a refusal
// changes nothing.
import { env } from "cloudflare:workers";
import { describe, expect, it } from "vitest";
import { account, call } from "./helpers";

const db = () => (env as unknown as { DB_COMMERCE: D1Database }).DB_COMMERCE;

async function homeKey() {
  const k = (await crypto.subtle.generateKey({ name: "Ed25519" }, true, ["sign", "verify"])) as CryptoKeyPair;
  const raw = new Uint8Array((await crypto.subtle.exportKey("raw", k.publicKey)) as ArrayBuffer);
  return btoa(String.fromCharCode(...raw));
}

/** Links a new home to the account (pair, then link with confirm); answers its store id. */
async function link(cookie: string, name: string): Promise<string> {
  const p = await call("POST", "/v1/homes/pair", { body: { public_key: await homeKey(), name } });
  const ok = await call("POST", "/v1/homes/link", { cookie, body: { user_code: p.body.user_code, confirm: true } });
  expect(ok.status).toBe(200);
  return ok.body.home_id as string;
}

let n = 0;
/** A paid one-time purchase of the account's, on homeId, with its entitlement. */
async function purchase(accountId: string, homeId: string): Promise<string> {
  const id = `pur-move-${Date.now()}-${n++}`;
  const at = new Date().toISOString();
  await db().batch([
    db()
      .prepare(
        `INSERT INTO purchases (id, item, account_id, home_id, plugin_id, product, developer_id, provider_id, currency,
                                amount_minor, maktaba_minor, developer_minor, status, created_at, paid_at)
         VALUES (?, 'plugin', ?, ?, 'dev.example.moved', 'one_time', 'dev-move', 'fake', 'USD', 1000, 150, 850, 'paid', ?, ?)`,
      )
      .bind(id, accountId, homeId, at, at),
    db()
      .prepare(`INSERT INTO entitlements (purchase_id, home_id, plugin_id, product) VALUES (?, ?, 'dev.example.moved', 'one_time')`)
      .bind(id, homeId),
  ]);
  return id;
}

const homeOf = async (id: string) => [
  (await db().prepare("SELECT home_id FROM purchases WHERE id = ?").bind(id).first<{ home_id: string }>())?.home_id,
  (await db().prepare("SELECT home_id FROM entitlements WHERE purchase_id = ?").bind(id).first<{ home_id: string }>())?.home_id,
];
const moveCount = async (id: string) =>
  (await db().prepare("SELECT count(*) AS n FROM purchase_moves WHERE purchase_id = ?").bind(id).first<{ n: number }>())!.n;

describe("moving a purchase to another home (54.19 §6)", () => {
  it("lists the purchase on its home, moves it twice, then refuses a third move until the year is up", async () => {
    const buyer = await account();
    const den = await link(buyer.cookie, "Den");
    const cabin = await link(buyer.cookie, "Cabin");
    const id = await purchase(buyer.id, den);

    const list = await call("GET", "/v1/purchases", { cookie: buyer.cookie });
    expect(list.status).toBe(200);
    expect(list.body.purchases).toEqual([
      expect.objectContaining({
        id,
        plugin_id: "dev.example.moved",
        home_id: den,
        home_name: "Den",
        home_linked: true,
        moves_left: 2,
        next_move_at: null,
      }),
    ]);

    const m1 = await call("POST", `/v1/purchases/${id}/move`, { cookie: buyer.cookie, body: { home_id: cabin } });
    expect(m1).toMatchObject({ status: 200, body: { status: "moved", from_home: den, to_home: cabin, moves_left: 1 } });
    expect(await homeOf(id)).toEqual([cabin, cabin]);

    const m2 = await call("POST", `/v1/purchases/${id}/move`, { cookie: buyer.cookie, body: { home_id: den } });
    expect(m2).toMatchObject({ status: 200, body: { moves_left: 0 } });

    const m3 = await call("POST", `/v1/purchases/${id}/move`, { cookie: buyer.cookie, body: { home_id: cabin } });
    expect(m3).toMatchObject({ status: 409, body: { error: "too_many_moves", max: 2 } });
    expect(typeof m3.body.next_move_at).toBe("string");
    expect(await homeOf(id)).toEqual([den, den]);
    expect(await moveCount(id)).toBe(2);

    const after = await call("GET", "/v1/purchases", { cookie: buyer.cookie });
    expect((after.body.purchases as unknown[])[0]).toMatchObject({ home_id: den, moves_left: 0 });
  });

  it("refuses the same home, another account's home, an unlinked home and another account's purchase, changing nothing", async () => {
    const buyer = await account();
    const den = await link(buyer.cookie, "Den");
    const old = await link(buyer.cookie, "Old");
    const other = await account();
    const theirs = await link(other.cookie, "Theirs");
    const id = await purchase(buyer.id, den);
    expect((await call("DELETE", `/v1/homes/${old}`, { cookie: buyer.cookie })).status).toBe(200);

    const tries: [string, string, string, number, string][] = [
      ["the same home", buyer.cookie, den, 409, "same_home"],
      ["another account's home", buyer.cookie, theirs, 404, "home_not_linked"],
      ["an unlinked home", buyer.cookie, old, 404, "home_not_linked"],
      ["another account moving it to its own home", other.cookie, theirs, 404, "not_found"],
    ];
    for (const [what, cookie, to, status, code] of tries) {
      const r = await call("POST", `/v1/purchases/${id}/move`, { cookie, body: { home_id: to } });
      expect(r, what).toMatchObject({ status, body: { error: code } });
    }
    expect(await homeOf(id)).toEqual([den, den]);
    expect(await moveCount(id)).toBe(0);
    // the other account sees none of the buyer's purchases
    expect((await call("GET", "/v1/purchases", { cookie: other.cookie })).body.purchases).toEqual([]);
  });

  it("an unverified account cannot move; signed out, neither route answers", async () => {
    const u = await account({ verified: false });
    expect(await call("POST", "/v1/purchases/pur-x/move", { cookie: u.cookie, body: { home_id: "h_x" } })).toMatchObject({
      status: 403,
      body: { error: "email_unverified" },
    });
    expect((await call("GET", "/v1/purchases")).status).toBe(401);
    expect((await call("POST", "/v1/purchases/pur-x/move", { body: { home_id: "h_x" } })).status).toBe(401);
  });
});
