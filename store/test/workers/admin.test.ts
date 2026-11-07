// Story 54.15 §4, §7: store admin and reviewers on the store's own host. Passkeys required,
// the one-time bootstrap, and the 7-day hold on an MFA reset.
import { describe, expect, it } from "vitest";
import { finishMfaResets } from "../../web/admin";
import { setClockOffset } from "../../web/env";
import { E, account, addPasskey, addTotp, ageFactor, call, developer, grant, mailTo } from "./helpers";

describe("store admin", () => {
  it("a reviewer with only TOTP gets 403 passkey_required; with a passkey, the page's data", async () => {
    const r = await account();
    await grant(r.id, "store_admin");
    await addTotp(r.cookie);
    expect((await call("GET", "/v1/admin/audit", { cookie: r.cookie })).body.error).toBe("passkey_required");
    const { cookie } = await addPasskey(r.cookie);
    expect((await call("GET", "/v1/admin/audit", { cookie })).status).toBe(200);
  });

  it("an admin decision needs a passkey within 5 minutes", async () => {
    const r = await account();
    await grant(r.id, "store_admin");
    const { cookie } = await addPasskey(r.cookie);
    await ageFactor(cookie, 6);
    const res = await call("POST", "/v1/admin/revocations", {
      cookie,
      body: { plugin_id: "dev.x.y", versions: "*", action: "warn", reason_code: "policy" },
    });
    expect(res).toMatchObject({ status: 401, body: { error: "reauth_required" } });
  });

  it("the bootstrap token makes one store admin, once", async () => {
    const a = await account();
    const { cookie } = await addPasskey(a.cookie);
    expect((await call("POST", "/v1/admin/bootstrap", { cookie, body: { token: "wrong-token-wrong-token-wrong-token" } })).status).toBe(
      403,
    );
    expect((await call("POST", "/v1/admin/bootstrap", { cookie, body: { token: "test-bootstrap-token-0123456789abcdef" } })).status).toBe(
      200,
    );
    const b = await account();
    const second = await addPasskey(b.cookie);
    expect(
      (await call("POST", "/v1/admin/bootstrap", { cookie: second.cookie, body: { token: "test-bootstrap-token-0123456789abcdef" } })).body
        .error,
    ).toBe("bootstrap_used");
  });

  it("an MFA reset waits 7 days, mails at the start and the end, and suspends the keys until a new factor", async () => {
    const admin = await account();
    await grant(admin.id, "store_admin");
    const { cookie } = await addPasskey(admin.cookie);
    const dev = await developer();
    const start = await call("POST", `/v1/admin/accounts/${dev.id}/mfa-reset`, { cookie });
    expect(start.status).toBe(202);
    expect(await mailTo(dev.email, "mfa_reset_started")).not.toBeNull();
    expect(await finishMfaResets(E)).toBe(0);
    setClockOffset(7 * 24 * 3600_000 + 60_000);
    try {
      expect(await finishMfaResets(E)).toBe(1);
    } finally {
      setClockOffset(0);
    }
    expect(await mailTo(dev.email, "mfa_reset_done")).not.toBeNull();
    const f = await E.DB_ACCOUNTS.prepare("SELECT count(*) AS n FROM account_mfa WHERE account = ?").bind(dev.id).first<{ n: number }>();
    expect(f!.n).toBe(0);
    const k = await E.DB_CATALOG.prepare("SELECT suspended_at FROM developer_keys WHERE id = ?")
      .bind(dev.key.id)
      .first<{ suspended_at: string | null }>();
    expect(k!.suspended_at).not.toBeNull();
  });
});
