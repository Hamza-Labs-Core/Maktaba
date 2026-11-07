// Store accounts (story 54.15 §4): sign-up and email verification, sign-in with lockout,
// multi-factor sign-in (TOTP, passkeys, recovery codes), re-authentication, sessions.
import {
  type Account,
  type Ctx,
  type FactorKind,
  audit,
  confirmed,
  endSessions,
  hasMFA,
  limit,
  reauthed,
  signedIn,
  startSession,
  clearCookie,
} from "./auth";
import { b64url, fromB64url, hex, newId, randomBytes, seal, sha256hex, token, unseal } from "./crypto";
import { iso, now } from "./env";
import { body, json, optStr, refuse, str } from "./http";
import { sendMail } from "./mail";
import { MIN_PASSWORD, hashPassword, verifyPassword } from "./passwords";
import { otpauthURI, verifyCode } from "./totp";
import { type StoredKey, WebAuthnError, verifyAssertion, verifyRegistration } from "./webauthn";

const EMAIL = /^[^\s@]{1,64}@[^\s@]{1,190}\.[^\s@]{2,}$/;
const VERIFY_MS = 24 * 3600_000;

const normEmail = (e: string) => e.trim().toLowerCase();

async function accountByEmail(ctx: Ctx, email: string): Promise<Account | null> {
  return ctx.env.DB_ACCOUNTS.prepare("SELECT * FROM accounts WHERE email = ?").bind(email).first<Account>();
}

async function sendVerify(ctx: Ctx, account: string, email: string, purpose: "verify" | "change"): Promise<void> {
  const t = token();
  await ctx.env.DB_ACCOUNTS.prepare("INSERT INTO email_tokens (token_hash, account, purpose, email, expires) VALUES (?, ?, ?, ?, ?)")
    .bind(await sha256hex(t), account, purpose, email, iso(new Date(now().getTime() + VERIFY_MS)))
    .run();
  await sendMail(ctx.env, { to: email, template: "verify_email", params: { link: `${ctx.env.STORE_ORIGIN}/verify#${t}` } });
}

/** POST /v1/accounts: a new account (email and password, or a passkey added next). */
export async function signUp(ctx: Ctx): Promise<Response> {
  if (ctx.env.SIGNUP_OPEN !== "true") refuse(403, "signup_closed");
  await limit(ctx, "signup", 5, 3600_000);
  const b = await body(ctx.req);
  const email = normEmail(str(b.email, "email", 254));
  if (!EMAIL.test(email)) refuse(400, "email_invalid");
  const password = optStr(b.password, "password", 512);
  if (password !== undefined && password.length < MIN_PASSWORD) refuse(400, "password_too_short", { min: MIN_PASSWORD });
  const name = (optStr(b.display_name, "display_name", 80) ?? "").trim();
  const locale = b.locale === "ar" ? "ar" : "en";
  if (await accountByEmail(ctx, email)) {
    // the same answer as a new account: an email's existence is never told
    return json({ status: "check_email" }, 202);
  }
  const id = newId("a");
  await ctx.env.DB_ACCOUNTS.batch([
    ctx.env.DB_ACCOUNTS.prepare(
      "INSERT INTO accounts (id, email, display_name, password_hash, locale, created) VALUES (?, ?, ?, ?, ?, ?)",
    ).bind(id, email, name, password ? await hashPassword(password) : null, locale, iso(now())),
    ctx.env.DB_ACCOUNTS.prepare("INSERT INTO roles (account, role, granted_by, at) VALUES (?, 'buyer', 'signup', ?)").bind(id, iso(now())),
  ]);
  await sendVerify(ctx, id, email, "verify");
  await startSession(ctx, id, "password");
  await audit(ctx, id, "account.signup", id);
  return json({ status: "check_email" }, 202);
}

/** POST /v1/accounts/verify {token}: the link from the email (single use, 24 hours). */
export async function verifyEmail(ctx: Ctx): Promise<Response> {
  const b = await body(ctx.req);
  const t = str(b.token, "token", 100);
  const h = await sha256hex(t);
  const row = await ctx.env.DB_ACCOUNTS.prepare("SELECT * FROM email_tokens WHERE token_hash = ?")
    .bind(h)
    .first<{ account: string; purpose: string; email: string; expires: string; used_at: string | null }>();
  if (!row || row.used_at || Date.parse(row.expires) < now().getTime()) refuse(410, "token_expired");
  const at = iso(now());
  const stmts = [ctx.env.DB_ACCOUNTS.prepare("UPDATE email_tokens SET used_at = ? WHERE token_hash = ?").bind(at, h)];
  if (row!.purpose === "change") {
    if (await accountByEmail(ctx, row!.email)) refuse(409, "email_taken");
    stmts.push(
      ctx.env.DB_ACCOUNTS.prepare("UPDATE accounts SET email = ?, email_verified_at = ? WHERE id = ?").bind(row!.email, at, row!.account),
    );
  } else {
    stmts.push(
      ctx.env.DB_ACCOUNTS.prepare("UPDATE accounts SET email_verified_at = ? WHERE id = ? AND email = ?").bind(
        at,
        row!.account,
        row!.email,
      ),
    );
  }
  await ctx.env.DB_ACCOUNTS.batch(stmts);
  await audit(ctx, row!.account, `email.${row!.purpose}`, row!.account);
  return json({ status: "verified" });
}

/** A TOTP code or a recovery code of account: the factor kind it was, or null. */
async function checkCode(ctx: Ctx, account: string, code: string): Promise<FactorKind | null> {
  const c = code.replace(/[\s-]/g, "");
  if (/^\d{6}$/.test(c)) {
    const rows = await ctx.env.DB_ACCOUNTS.prepare("SELECT id, secret, last_step FROM account_mfa WHERE account = ? AND kind = 'totp'")
      .bind(account)
      .all<{ id: string; secret: string; last_step: number }>();
    for (const f of rows.results) {
      const step = await verifyCode(await unseal(ctx.env.MFA_ENC_KEY, f.secret), c, now(), f.last_step);
      if (step !== null) {
        // the step is written only if it is still newer: a code is used once, even by two
        // requests at the same moment
        const r = await ctx.env.DB_ACCOUNTS.prepare("UPDATE account_mfa SET last_step = ?, last_used = ? WHERE id = ? AND last_step < ?")
          .bind(step, iso(now()), f.id, step)
          .run();
        if (r.meta.changes === 1) return "totp";
      }
    }
    return null;
  }
  if (/^[a-z0-9]{10}$/i.test(c)) {
    const r = await ctx.env.DB_ACCOUNTS.prepare(
      "UPDATE recovery_codes SET used_at = ? WHERE account = ? AND code_hash = ? AND used_at IS NULL",
    )
      .bind(iso(now()), account, await sha256hex(c.toLowerCase()))
      .run();
    if (r.meta.changes === 1) return "recovery";
  }
  return null;
}

/** POST /v1/sessions {email, password, code?}: sign in; with a factor, its code too. */
export async function signIn(ctx: Ctx): Promise<Response> {
  await limit(ctx, "signin", 30, 15 * 60_000);
  const b = await body(ctx.req);
  const email = normEmail(str(b.email, "email", 254));
  const password = str(b.password, "password", 512);
  const guard = ctx.env.ACCOUNT_GUARD.get(ctx.env.ACCOUNT_GUARD.idFromName(email));
  const at = now().getTime();
  if ((await guard.check(at)).locked) refuse(429, "signin_locked");
  const a = await accountByEmail(ctx, email);
  const ok = (await verifyPassword(password, a?.password_hash ?? null)) && a !== null && a.status === "active";
  if (!ok) {
    const f = await guard.fail(at);
    if (f.newlyLocked && a)
      await sendMail(ctx.env, { to: a.email, template: "signin_locked", params: { until: new Date(f.until).toISOString() } });
    if (a) await audit(ctx, a.id, "session.signin", a.id, "bad_password");
    refuse(f.locked ? 429 : 401, f.locked ? "signin_locked" : "signin_failed");
  }
  const factors = await ctx.env.DB_ACCOUNTS.prepare("SELECT kind FROM account_mfa WHERE account = ?").bind(a!.id).all<{ kind: string }>();
  let kind: FactorKind = "password";
  if (factors.results.length > 0) {
    const code = optStr(b.code, "code", 20);
    if (!code) {
      return json(
        {
          error: "code_required",
          totp: factors.results.some((f) => f.kind === "totp"),
          passkey: factors.results.some((f) => f.kind === "passkey"),
        },
        401,
      );
    }
    const k = await checkCode(ctx, a!.id, code);
    if (!k) {
      await guard.fail(at);
      await audit(ctx, a!.id, "session.signin", a!.id, "bad_code");
      refuse(401, "code_wrong");
    }
    kind = k!;
  }
  await guard.succeed();
  await startSession(ctx, a!.id, kind);
  await audit(ctx, a!.id, "session.signin", a!.id);
  return json({ status: "signed_in" });
}

// ---- passkeys -------------------------------------------------------------------------------

async function newChallenge(
  ctx: Ctx,
  purpose: "register" | "signin" | "reauth",
  account: string | null,
): Promise<{ id: string; challenge: string }> {
  const id = newId("c");
  const challenge = b64url(randomBytes(32));
  await ctx.env.DB_ACCOUNTS.prepare("INSERT INTO webauthn_challenges (id, account, challenge, purpose, expires) VALUES (?, ?, ?, ?, ?)")
    .bind(id, account, challenge, purpose, iso(new Date(now().getTime() + 5 * 60_000)))
    .run();
  return { id, challenge };
}

/** Takes a challenge once (deleted on read), or refuses. */
async function takeChallenge(ctx: Ctx, id: string, purpose: string): Promise<{ challenge: string; account: string | null }> {
  const row = await ctx.env.DB_ACCOUNTS.prepare(
    "DELETE FROM webauthn_challenges WHERE id = ? AND purpose = ? RETURNING challenge, account, expires",
  )
    .bind(id, purpose)
    .first<{ challenge: string; account: string | null; expires: string }>();
  if (!row || Date.parse(row.expires) < now().getTime()) refuse(410, "challenge_expired");
  return row!;
}

const rp = (ctx: Ctx) => ({ id: new URL(ctx.env.STORE_ORIGIN).hostname, name: "Maktaba Plugins" });

/** POST /v1/sessions/passkey/options: a challenge for signing in with a passkey. */
export async function passkeySignInOptions(ctx: Ctx): Promise<Response> {
  await limit(ctx, "signin", 30, 15 * 60_000);
  const c = await newChallenge(ctx, "signin", null);
  return json({
    challenge_id: c.id,
    publicKey: { challenge: c.challenge, rpId: rp(ctx).id, userVerification: "required", timeout: 300000 },
  });
}

async function assertPasskey(ctx: Ctx, credential: Record<string, unknown>, challenge: string, account: string | null): Promise<string> {
  const credId = str(credential.id, "credential.id", 512);
  const f = await ctx.env.DB_ACCOUNTS.prepare(
    "SELECT id, account, secret, sign_count FROM account_mfa WHERE credential_id = ? AND kind = 'passkey'",
  )
    .bind(credId)
    .first<{ id: string; account: string; secret: string; sign_count: number }>();
  if (!f || (account && f.account !== account)) refuse(401, "passkey_unknown");
  try {
    const count = await verifyAssertion(
      credential as never,
      JSON.parse(f!.secret) as StoredKey,
      f!.sign_count,
      challenge,
      ctx.env.STORE_ORIGIN,
    );
    await ctx.env.DB_ACCOUNTS.prepare("UPDATE account_mfa SET sign_count = ?, last_used = ? WHERE id = ?")
      .bind(count, iso(now()), f!.id)
      .run();
  } catch (e) {
    if (e instanceof WebAuthnError) refuse(401, "passkey_invalid", { detail: e.message });
    throw e;
  }
  return f!.account;
}

/** POST /v1/sessions/passkey {challenge_id, credential}: a passkey alone signs in (two factors). */
export async function passkeySignIn(ctx: Ctx): Promise<Response> {
  const b = await body(ctx.req);
  const { challenge } = await takeChallenge(ctx, str(b.challenge_id, "challenge_id"), "signin");
  const account = await assertPasskey(ctx, (b.credential ?? {}) as Record<string, unknown>, challenge, null);
  const a = await ctx.env.DB_ACCOUNTS.prepare("SELECT status FROM accounts WHERE id = ?").bind(account).first<{ status: string }>();
  if (a?.status !== "active") refuse(401, "signin_failed");
  await startSession(ctx, account, "passkey");
  await audit(ctx, account, "session.signin", account, "ok:passkey");
  return json({ status: "signed_in" });
}

// ---- re-authentication -------------------------------------------------------------------

/** POST /v1/reauth {password?, code?} or {challenge_id, credential}: confirm a factor now. */
export async function reauth(ctx: Ctx): Promise<Response> {
  const a = signedIn(ctx);
  const b = await body(ctx.req);
  const guard = ctx.env.ACCOUNT_GUARD.get(ctx.env.ACCOUNT_GUARD.idFromName(a.email));
  const at = now().getTime();
  if ((await guard.check(at)).locked) refuse(429, "signin_locked");
  let kind: FactorKind | null = null;
  if (b.challenge_id) {
    const { challenge, account } = await takeChallenge(ctx, str(b.challenge_id, "challenge_id"), "reauth");
    if (account !== a.id) refuse(401, "passkey_unknown");
    await assertPasskey(ctx, (b.credential ?? {}) as Record<string, unknown>, challenge, a.id);
    kind = "passkey";
  } else if (b.code) {
    kind = await checkCode(ctx, a.id, str(b.code, "code", 20));
  } else if (b.password && !hasMFA(ctx)) {
    kind = (await verifyPassword(str(b.password, "password", 512), a.password_hash)) ? "password" : null;
  }
  if (!kind) {
    await guard.fail(at);
    await audit(ctx, a.id, "session.reauth", a.id, "failed");
    refuse(401, hasMFA(ctx) ? "code_wrong" : "signin_failed");
  }
  await confirmed(ctx, kind!);
  return json({ status: "confirmed", kind });
}

/** POST /v1/reauth/passkey/options: a challenge to confirm with a passkey. */
export async function reauthPasskeyOptions(ctx: Ctx): Promise<Response> {
  const a = signedIn(ctx);
  const c = await newChallenge(ctx, "reauth", a.id);
  const creds = await ctx.env.DB_ACCOUNTS.prepare("SELECT credential_id FROM account_mfa WHERE account = ? AND kind = 'passkey'")
    .bind(a.id)
    .all<{ credential_id: string }>();
  return json({
    challenge_id: c.id,
    publicKey: {
      challenge: c.challenge,
      rpId: rp(ctx).id,
      userVerification: "required",
      allowCredentials: creds.results.map((r) => ({ type: "public-key", id: r.credential_id })),
    },
  });
}

// ---- factors ----------------------------------------------------------------------------

async function recoveryCodesIfFirst(ctx: Ctx, account: string): Promise<string[] | undefined> {
  const n = await ctx.env.DB_ACCOUNTS.prepare("SELECT count(*) AS n FROM account_mfa WHERE account = ?")
    .bind(account)
    .first<{ n: number }>();
  if ((n?.n ?? 0) !== 1) return undefined;
  const codes = Array.from({ length: 10 }, () => hex(randomBytes(5)));
  await ctx.env.DB_ACCOUNTS.batch([
    ctx.env.DB_ACCOUNTS.prepare("DELETE FROM recovery_codes WHERE account = ?").bind(account),
    ...(await Promise.all(
      codes.map(async (c) =>
        ctx.env.DB_ACCOUNTS.prepare("INSERT INTO recovery_codes (account, code_hash) VALUES (?, ?)").bind(account, await sha256hex(c)),
      ),
    )),
  ]);
  return codes;
}

/** A factor change: the other sessions end, keys suspended by a reset come back. */
async function factorsChanged(ctx: Ctx, account: string, action: string, target: string): Promise<void> {
  await endSessions(ctx.env, account, ctx.session!.id_hash);
  await ctx.env.DB_CATALOG.prepare(
    "UPDATE developer_keys SET suspended_at = NULL WHERE account = ? AND suspended_at IS NOT NULL AND EXISTS (SELECT 1 FROM developers WHERE account = ? AND status = 'active')",
  )
    .bind(account, account)
    .run();
  await audit(ctx, account, action, target);
}

/** POST /v1/mfa/totp/begin: a new TOTP secret to confirm (re-auth applies). */
export async function totpBegin(ctx: Ctx): Promise<Response> {
  const a = signedIn(ctx);
  reauthed(ctx);
  const secret = randomBytes(20);
  const id = newId("f");
  // kept unconfirmed in the challenge table until a code from it is confirmed
  await ctx.env.DB_ACCOUNTS.prepare(
    "INSERT INTO webauthn_challenges (id, account, challenge, purpose, expires) VALUES (?, ?, ?, 'register', ?)",
  )
    .bind(id, a.id, await seal(ctx.env.MFA_ENC_KEY, secret), iso(new Date(now().getTime() + 10 * 60_000)))
    .run();
  return json({
    setup_id: id,
    otpauth: otpauthURI(secret, a.email),
    secret: otpauthURI(secret, a.email).split("secret=")[1]!.split("&")[0],
  });
}

/** POST /v1/mfa/totp/confirm {setup_id, code}: the TOTP factor is added. */
export async function totpConfirm(ctx: Ctx): Promise<Response> {
  const a = signedIn(ctx);
  reauthed(ctx);
  const b = await body(ctx.req);
  const { challenge: sealed, account } = await takeChallenge(ctx, str(b.setup_id, "setup_id"), "register");
  if (account !== a.id) refuse(410, "challenge_expired");
  const step = await verifyCode(await unseal(ctx.env.MFA_ENC_KEY, sealed), str(b.code, "code", 10), now(), 0);
  if (step === null) refuse(400, "code_wrong");
  const id = newId("f");
  await ctx.env.DB_ACCOUNTS.prepare(
    "INSERT INTO account_mfa (id, account, kind, name, secret, last_step, added) VALUES (?, ?, 'totp', ?, ?, ?, ?)",
  )
    .bind(id, a.id, optStr(b.name, "name", 60) ?? "Authenticator", sealed, step, iso(now()))
    .run();
  const codes = await recoveryCodesIfFirst(ctx, a.id);
  await factorsChanged(ctx, a.id, "mfa.add", id);
  await confirmed(ctx, "totp");
  return json({ id, recovery_codes: codes }, 201);
}

/** POST /v1/mfa/passkey/options: a registration challenge (re-auth applies). */
export async function passkeyOptions(ctx: Ctx): Promise<Response> {
  const a = signedIn(ctx);
  reauthed(ctx);
  const c = await newChallenge(ctx, "register", a.id);
  const existing = await ctx.env.DB_ACCOUNTS.prepare("SELECT credential_id FROM account_mfa WHERE account = ? AND kind = 'passkey'")
    .bind(a.id)
    .all<{ credential_id: string }>();
  return json({
    challenge_id: c.id,
    publicKey: {
      challenge: c.challenge,
      rp: rp(ctx),
      user: { id: b64url(new TextEncoder().encode(a.id)), name: a.email, displayName: a.display_name || a.email },
      pubKeyCredParams: [
        { type: "public-key", alg: -8 },
        { type: "public-key", alg: -7 },
      ],
      authenticatorSelection: { residentKey: "required", userVerification: "required" },
      attestation: "none",
      excludeCredentials: existing.results.map((r) => ({ type: "public-key", id: r.credential_id })),
      timeout: 300000,
    },
  });
}

/** POST /v1/mfa/passkey {challenge_id, credential, name}: the passkey is added. */
export async function passkeyRegister(ctx: Ctx): Promise<Response> {
  const a = signedIn(ctx);
  reauthed(ctx);
  const b = await body(ctx.req, 32 << 10);
  const { challenge, account } = await takeChallenge(ctx, str(b.challenge_id, "challenge_id"), "register");
  if (account !== a.id) refuse(410, "challenge_expired");
  let reg;
  try {
    reg = await verifyRegistration((b.credential ?? {}) as never, challenge, ctx.env.STORE_ORIGIN);
  } catch (e) {
    if (e instanceof WebAuthnError) refuse(400, "passkey_invalid", { detail: e.message });
    throw e;
  }
  const id = newId("f");
  await ctx.env.DB_ACCOUNTS.prepare(
    "INSERT INTO account_mfa (id, account, kind, name, credential_id, secret, alg, sign_count, added) VALUES (?, ?, 'passkey', ?, ?, ?, ?, ?, ?)",
  )
    .bind(
      id,
      a.id,
      optStr(b.name, "name", 60) ?? "Passkey",
      reg!.credentialId,
      JSON.stringify(reg!.key),
      reg!.key.alg,
      reg!.signCount,
      iso(now()),
    )
    .run();
  const codes = await recoveryCodesIfFirst(ctx, a.id);
  await factorsChanged(ctx, a.id, "mfa.add", id);
  await confirmed(ctx, "passkey");
  return json({ id, recovery_codes: codes }, 201);
}

/** DELETE /v1/mfa/{id}: removes a factor (re-auth applies). */
export async function removeFactor(ctx: Ctx, id: string): Promise<Response> {
  const a = signedIn(ctx);
  reauthed(ctx);
  const r = await ctx.env.DB_ACCOUNTS.prepare("DELETE FROM account_mfa WHERE id = ? AND account = ?").bind(id, a.id).run();
  if (r.meta.changes !== 1) refuse(404, "not_found");
  await factorsChanged(ctx, a.id, "mfa.remove", id);
  return json({ status: "removed" });
}

// ---- the account --------------------------------------------------------------------------

/** GET /v1/me: who is signed in, their roles, factors and sessions. */
export async function me(ctx: Ctx): Promise<Response> {
  const a = signedIn(ctx);
  const factors = await ctx.env.DB_ACCOUNTS.prepare(
    "SELECT id, kind, name, added, last_used FROM account_mfa WHERE account = ? ORDER BY added",
  )
    .bind(a.id)
    .all();
  const sessions = await ctx.env.DB_ACCOUNTS.prepare(
    "SELECT id_hash, created, last_seen, user_agent FROM sessions WHERE account = ? ORDER BY last_seen DESC",
  )
    .bind(a.id)
    .all<{ id_hash: string; created: string; last_seen: string; user_agent: string }>();
  return json({
    id: a.id,
    email: a.email,
    email_verified: !!a.email_verified_at,
    display_name: a.display_name,
    locale: a.locale,
    roles: [...ctx.roles],
    factors: factors.results,
    sessions: sessions.results.map((s) => ({
      id: s.id_hash.slice(0, 12),
      created: s.created,
      last_seen: s.last_seen,
      device: s.user_agent,
      current: s.id_hash === ctx.session!.id_hash,
    })),
    reauth_until: new Date(Date.parse(ctx.session!.mfa_at) + 5 * 60_000).toISOString(),
  });
}

/** POST /v1/sessions/end: sign out here; {everywhere: true}: every session. */
export async function signOut(ctx: Ctx): Promise<Response> {
  const a = signedIn(ctx);
  const b = await body(ctx.req);
  if (b.everywhere === true) await endSessions(ctx.env, a.id);
  else await ctx.env.DB_ACCOUNTS.prepare("DELETE FROM sessions WHERE id_hash = ?").bind(ctx.session!.id_hash).run();
  clearCookie(ctx);
  await audit(ctx, a.id, b.everywhere === true ? "session.end_all" : "session.end", a.id);
  return json({ status: "signed_out" });
}

/** POST /v1/accounts/email {email}: verify the new address, tell the old one. */
export async function changeEmail(ctx: Ctx): Promise<Response> {
  const a = signedIn(ctx);
  reauthed(ctx);
  await limit(ctx, "email", 10, 3600_000);
  const b = await body(ctx.req);
  const email = normEmail(str(b.email, "email", 254));
  if (!EMAIL.test(email)) refuse(400, "email_invalid");
  await sendVerify(ctx, a.id, email, "change");
  await sendMail(ctx.env, { to: a.email, template: "email_changed", params: { email } });
  await audit(ctx, a.id, "email.change_requested", a.id);
  return json({ status: "check_email" }, 202);
}

/** POST /v1/accounts/password {password}: every other session ends. */
export async function changePassword(ctx: Ctx): Promise<Response> {
  const a = signedIn(ctx);
  reauthed(ctx);
  const b = await body(ctx.req);
  const p = str(b.password, "password", 512);
  if (p.length < MIN_PASSWORD) refuse(400, "password_too_short", { min: MIN_PASSWORD });
  await ctx.env.DB_ACCOUNTS.prepare("UPDATE accounts SET password_hash = ? WHERE id = ?")
    .bind(await hashPassword(p), a.id)
    .run();
  await endSessions(ctx.env, a.id, ctx.session!.id_hash);
  await audit(ctx, a.id, "password.change", a.id);
  return json({ status: "changed" });
}

/** DELETE /v1/accounts/me: plugins delisted, keys revoked for new uploads; installed copies stay. */
export async function deleteAccount(ctx: Ctx): Promise<Response> {
  const a = signedIn(ctx);
  reauthed(ctx);
  const at = iso(now());
  await ctx.env.DB_ACCOUNTS.batch([
    ctx.env.DB_ACCOUNTS.prepare("UPDATE accounts SET status = 'deleted', email = ?, password_hash = NULL WHERE id = ?").bind(
      `deleted+${a.id}@invalid`,
      a.id,
    ),
    ctx.env.DB_ACCOUNTS.prepare("DELETE FROM account_mfa WHERE account = ?").bind(a.id),
    ctx.env.DB_ACCOUNTS.prepare("DELETE FROM sessions WHERE account = ?").bind(a.id),
  ]);
  await ctx.env.DB_CATALOG.batch([
    ctx.env.DB_CATALOG.prepare("UPDATE developers SET status = 'deleted' WHERE account = ?").bind(a.id),
    ctx.env.DB_CATALOG.prepare("UPDATE developer_keys SET revoked_at = COALESCE(revoked_at, ?) WHERE account = ?").bind(at, a.id),
    ctx.env.DB_CATALOG.prepare("UPDATE plugins SET status = 'delisted' WHERE developer = ? AND status = 'listed'").bind(a.id),
  ]);
  await ctx.env.Q_CATALOG.send({ kind: "regenerate", reason: "account deleted" });
  clearCookie(ctx);
  await audit(ctx, a.id, "account.delete", a.id);
  return json({ status: "deleted" });
}

export { fromB64url };
