// Story 54.15 T01 (and 54.16 T06's shape for the admin routes): every store route against
// each kind of caller. Refusals change nothing (the rows a refusal could touch are counted
// before and after).
import { describe, expect, it } from "vitest";
import { routeTable } from "../../web/index";
import { E, account, addPasskey, addTotp, call, developer, grant } from "./helpers";

// Routes open to anyone (no session): sign-up, verification, sign-in, pairing.
const PUBLIC = new Set([
  "POST /v1/accounts",
  "POST /v1/accounts/verify",
  "POST /v1/sessions",
  "POST /v1/sessions/passkey/options",
  "POST /v1/sessions/passkey",
  "POST /v1/homes/pair",
  "POST /v1/homes/pair/poll",
  // reading ratings is public (54.18 §5)
  "GET /v1/plugins/abc/ratings",
]);

const sample = (re: string) =>
  re
    .replace(/^\^/, "")
    .replace(/\$$/, "")
    .replace(/\\\//g, "/")
    .replace(/\(\[0-9a-f\]\{32\}\)/g, "0".repeat(32))
    .replace(/\(\\d\+\)/g, "1")
    .replace(/\([^)]*\)/g, "abc");

async function counts() {
  const q = async (db: D1Database, t: string) => (await db.prepare(`SELECT count(*) AS n FROM ${t}`).first<{ n: number }>())!.n;
  return [
    await q(E.DB_ACCOUNTS, "roles"),
    await q(E.DB_ACCOUNTS, "account_mfa"),
    await q(E.DB_CATALOG, "developer_keys"),
    await q(E.DB_CATALOG, "revocations"),
    await q(E.DB_CATALOG, "submissions"),
    await q(E.DB_INSTALLS, "homes"),
  ];
}

describe("role tests (T01)", () => {
  it("no one: every non-public route is 401 signed_out", async () => {
    for (const [m, re] of routeTable) {
      const path = sample(re);
      if (PUBLIC.has(`${m} ${path}`)) continue;
      const r = await call(m, path, { body: m === "GET" ? undefined : {} });
      expect(r.status, `${m} ${path}`).toBe(401);
    }
  });

  it("buyer, unverified, developer and reviewer without a passkey: every admin route refused, nothing changed", async () => {
    const unverified = await account({ verified: false });
    const buyer = await account();
    const dev = await developer();
    const totpReviewer = await account();
    await grant(totpReviewer.id, "store_reviewer");
    await addTotp(totpReviewer.cookie);
    const before = await counts();
    for (const [who, cookie, code] of [
      ["unverified", unverified.cookie, "email_unverified"],
      ["buyer", buyer.cookie, "forbidden"],
      ["developer", dev.cookie, "forbidden"],
      ["reviewer (TOTP only)", totpReviewer.cookie, "passkey_required"],
    ] as const) {
      for (const [m, re] of routeTable) {
        if (!re.includes("admin") || re.includes("bootstrap")) continue;
        const path = sample(re);
        const r = await call(m, path, {
          cookie,
          body:
            m === "GET"
              ? undefined
              : { account: buyer.id, role: "store_admin", plugin_id: "x.y.z", versions: "*", action: "disable", reason_code: "malware" },
        });
        expect(r.status, `${who} ${m} ${path}`).toBe(403);
        // a reviewer is below every store-admin route (forbidden) before its factor is looked at
        const want = who === "reviewer (TOTP only)" ? ["forbidden", "passkey_required"] : [code];
        expect(want, `${who} ${m} ${path}: ${r.body.error}`).toContain(r.body.error);
      }
    }
    expect(await counts()).toEqual(before);
  });

  it("a buyer cannot reach developer routes; a store admin with a passkey reaches the admin routes", async () => {
    const buyer = await account();
    for (const path of ["/v1/developers/keys", "/v1/submissions", "/v1/developers/prefixes"]) {
      expect((await call("POST", path, { cookie: buyer.cookie, body: {} })).body.error, path).toBe("not_developer");
    }
    const admin = await account();
    await grant(admin.id, "store_admin");
    const { cookie } = await addPasskey(admin.cookie);
    const r = await call("POST", "/v1/admin/roles", { cookie, body: { account: buyer.id, role: "store_reviewer" } });
    expect(r.status).toBe(200);
    const rev = await call("GET", "/v1/admin/audit", { cookie: buyer.cookie });
    expect(rev.status).toBe(403);
  });
});
