// Story 54.15 §4 and T08, T09, T10, T11 (lockout): store accounts in Miniflare.
import { describe, expect, it } from "vitest";
import { E, account, addPasskey, addTotp, ageFactor, call, grant, mailTo, passkeySignIn, uniqueEmail } from "./helpers";

describe("sign-up and email verification", () => {
  it("signs up, mails a link in English and Arabic, and only a verified account becomes a developer", async () => {
    const a = await account({ verified: false });
    const r = await call("POST", "/v1/developers", { cookie: a.cookie, body: { agreement_version: 1 } });
    expect(r).toMatchObject({ status: 403, body: { error: "email_unverified" } });
    const m = await mailTo(a.email, "verify_email");
    expect(m!.subject).toContain("Check your email to finish signing up");
    expect(m!.subject).toContain("تحقّق من بريدك الإلكتروني لإكمال التسجيل");
    const token = m!.params.link!.split("#")[1];
    expect((await call("POST", "/v1/accounts/verify", { body: { token } })).status).toBe(200);
    // single use
    expect((await call("POST", "/v1/accounts/verify", { body: { token } })).body.error).toBe("token_expired");
    expect((await call("POST", "/v1/developers", { cookie: a.cookie, body: { agreement_version: 1 } })).status).toBe(200);
  });

  it("an email that has an account answers the same as a new one", async () => {
    const a = await account();
    const again = await call("POST", "/v1/accounts", { body: { email: a.email, password: "another password!" } });
    expect(again.status).toBe(202);
    expect(again.cookie).toBeUndefined();
  });
});

describe("sign-in lockout (T11)", () => {
  it("5 failed sign-ins lock the account: the right password is refused and the owner is told", async () => {
    const a = await account();
    for (let i = 0; i < 5; i++) {
      const r = await call("POST", "/v1/sessions", { body: { email: a.email, password: "wrong password" } });
      expect(r.status).toBe(i < 4 ? 401 : 429);
    }
    const sixth = await call("POST", "/v1/sessions", { body: { email: a.email, password: a.password } });
    expect(sixth).toMatchObject({ status: 429, body: { error: "signin_locked" } });
    expect(await mailTo(a.email, "signin_locked")).not.toBeNull();
  });

  it("a name with no account locks the same way, so a lockout tells nothing", async () => {
    const email = uniqueEmail("ghost");
    for (let i = 0; i < 5; i++) await call("POST", "/v1/sessions", { body: { email, password: "x".repeat(12) } });
    expect((await call("POST", "/v1/sessions", { body: { email, password: "x".repeat(12) } })).body.error).toBe("signin_locked");
  });
});

describe("factors (T09)", () => {
  it("TOTP: a sign-in needs the code, a used code is refused a second time, ±1 step", async () => {
    const a = await account();
    const t = await addTotp(a.cookie);
    expect((await call("POST", "/v1/sessions", { body: { email: a.email, password: a.password } })).body.error).toBe("code_required");
    const c = await t.codeAt(0);
    expect((await call("POST", "/v1/sessions", { body: { email: a.email, password: a.password, code: c } })).status).toBe(200);
    expect((await call("POST", "/v1/sessions", { body: { email: a.email, password: a.password, code: c } })).body.error).toBe("code_wrong");
    expect((await call("POST", "/v1/sessions", { body: { email: a.email, password: a.password, code: await t.codeAt(1) } })).status).toBe(
      200,
    );
    expect(
      (await call("POST", "/v1/sessions", { body: { email: a.email, password: a.password, code: await t.codeAt(5) } })).body.error,
    ).toBe("code_wrong");
  });

  it("ten recovery codes are shown once, each works once", async () => {
    const a = await account();
    const t = await addTotp(a.cookie);
    expect(t.recovery).toHaveLength(10);
    const code = t.recovery[3]!;
    expect((await call("POST", "/v1/sessions", { body: { email: a.email, password: a.password, code } })).status).toBe(200);
    expect((await call("POST", "/v1/sessions", { body: { email: a.email, password: a.password, code } })).body.error).toBe("code_wrong");
  });

  it("a passkey signs in alone; a wrong origin, no user verification or a counter going back is refused", async () => {
    const a = await account();
    const { auth } = await addPasskey(a.cookie);
    expect(await passkeySignIn(auth)).toBeTruthy();
    for (const opts of [{ origin: "https://plugins.maktaba.tv.evil.example" }, { flags: 0x01 }, { replayCount: 1 }]) {
      const o = await call("POST", "/v1/sessions/passkey/options", {});
      const r = await call("POST", "/v1/sessions/passkey", {
        body: {
          challenge_id: o.body.challenge_id,
          credential: await auth.assert((o.body.publicKey as { challenge: string }).challenge, opts),
        },
      });
      expect(r.status, JSON.stringify(opts)).toBe(401);
    }
    // a challenge is used once
    const o = await call("POST", "/v1/sessions/passkey/options", {});
    const ch = (o.body.publicKey as { challenge: string }).challenge;
    // (the fixture's counter moved back above: on past what the store saw)
    expect(
      (
        await call("POST", "/v1/sessions/passkey", {
          body: { challenge_id: o.body.challenge_id, credential: await auth.assert(ch, { replayCount: 100 }) },
        })
      ).status,
    ).toBe(200);
    expect(
      (await call("POST", "/v1/sessions/passkey", { body: { challenge_id: o.body.challenge_id, credential: await auth.assert(ch) } })).body
        .error,
    ).toBe("challenge_expired");
  });
});

describe("re-authentication (T10)", () => {
  it("each sensitive route answers 200-class at 4 minutes and 401 reauth_required at 6", async () => {
    const a = await account();
    const t = await addTotp(a.cookie);
    const routes: [string, string, () => unknown][] = [
      ["POST", "/v1/mfa/totp/begin", () => ({})],
      ["POST", "/v1/accounts/password", () => ({ password: "a brand new password" })],
      ["POST", "/v1/accounts/email", () => ({ email: uniqueEmail("new") })],
      ["POST", "/v1/developers", () => ({ agreement_version: 1 })],
      ["POST", "/v1/homes/link", () => ({ user_code: "AAAA-AAAA" })],
    ];
    for (const [m, path, b] of routes) {
      await ageFactor(a.cookie, 6);
      const late = await call(m, path, { cookie: a.cookie, body: b() });
      expect(late, path).toMatchObject({ status: 401, body: { error: "reauth_required" } });
      await ageFactor(a.cookie, 4);
      const fresh = await call(m, path, { cookie: a.cookie, body: b() });
      expect(fresh.body.error, path).not.toBe("reauth_required");
    }
    // confirming a factor makes it fresh again
    await ageFactor(a.cookie, 10);
    expect((await call("POST", "/v1/reauth", { cookie: a.cookie, body: { code: await t.codeAt(1) } })).status).toBe(200);
    expect((await call("POST", "/v1/mfa/totp/begin", { cookie: a.cookie })).status).toBe(200);
  });

  it("with a second factor, a password alone does not re-authenticate", async () => {
    const a = await account();
    await addTotp(a.cookie);
    await ageFactor(a.cookie, 10);
    expect((await call("POST", "/v1/reauth", { cookie: a.cookie, body: { password: a.password } })).status).toBe(401);
  });
});

describe("independent of Maktaba Cloud (T08)", () => {
  it("a cloud cookie, a cloud bearer token and a Domain=.maktaba.tv cookie are signed out", async () => {
    const a = await account();
    await grant(a.id, "developer");
    for (const headers of <Record<string, string>[]>[
      { Cookie: `maktaba_session=${a.cookie}` },
      { Authorization: `Bearer ${a.cookie}` },
      { Authorization: "Bearer eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJjbG91ZCJ9.c2ln" },
      { Cookie: `mks_session=${a.cookie}` },
    ]) {
      const r = await call("GET", "/v1/me", { headers });
      expect(r, JSON.stringify(headers)).toMatchObject({ status: 401, body: { error: "signed_out" } });
    }
    expect((await call("GET", "/v1/me", { cookie: a.cookie })).status).toBe(200);
  });

  it("a state-changing request from another origin is refused", async () => {
    const a = await account();
    const r = await call("POST", "/v1/sessions/end", { cookie: a.cookie, headers: { Origin: "https://maktaba.tv" }, body: {} });
    expect(r).toMatchObject({ status: 403, body: { error: "cross_origin" } });
  });
});

describe("sessions", () => {
  it("sign out everywhere ends every session", async () => {
    const a = await account();
    const second = await call("POST", "/v1/sessions", { body: { email: a.email, password: a.password } });
    expect((await call("POST", "/v1/sessions/end", { cookie: a.cookie, body: { everywhere: true } })).status).toBe(200);
    expect((await call("GET", "/v1/me", { cookie: second.cookie })).status).toBe(401);
  });

  it("a developer's session ends after 12 idle hours", async () => {
    const a = await account();
    await grant(a.id, "developer");
    await E.DB_ACCOUNTS.prepare("UPDATE sessions SET last_seen = ? WHERE account = ?")
      .bind(new Date(Date.now() - 13 * 3600_000).toISOString(), a.id)
      .run();
    expect((await call("GET", "/v1/me", { cookie: a.cookie })).status).toBe(401);
  });
});
