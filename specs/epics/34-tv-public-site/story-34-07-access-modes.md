# Story 34.7 — Access modes

## Description

*As the owner, I want to choose who may watch: everyone, everyone who is not a bot, only people I
invite, or people with a Maktaba account — and switch between these without touching code.*

Design: [design.md](design.md) §7; README R7.

## Acceptance criteria

- **AC1** `tv_public.access_mode` ∈ `public | turnstile | access | accounts`, default `turnstile`.
- **AC2** `turnstile`: the first play in a browser session shows the Turnstile widget (managed mode); the
  Worker validates the token with siteverify (`secret`, `response`, `remoteip`, `idempotency_key`) and
  sets an HttpOnly, Secure, SameSite=Lax `__Host-mtv` cookie (12 h, HMAC-signed); `play` without a valid
  cookie returns 403 `challenge_required`. Guide and lineup stay open.
- **AC3** `access`: the deploy job creates/updates a Cloudflare Access self-hosted application for the
  site hostnames with an allow policy for `access_emails` (One-time PIN by default); the Worker verifies
  `Cf-Access-Jwt-Assertion` (signature with the team's certs, `aud`, `exp`) on every API call and treats
  a missing/invalid assertion as 403. The whole site is behind Access.
- **AC4** `accounts` (phase D): "Sign in with Maktaba" via the Epic 25 cloud (OAuth 2.0 + PKCE / OIDC);
  the Worker verifies the ID token and the hashed `sub` against `allowed_accounts` in `site.json`
  (published by the home from users linked to cloud accounts); sign-out clears the cookie.
- **AC5** Changing the mode runs the deploy job's config step only (widget/app/vars), not a code deploy,
  and applies in < 2 min; `indexable` is forced off in `access` and `accounts`.
- **AC6** The embed page inherits the mode (Turnstile shown in the frame; Access/accounts show a
  "Watch on <site>" link because third-party frames cannot complete those sign-ins reliably).
- **AC7** Staging in `public` mode still gets a Turnstile gate (design §10.5).

## Test cases

- **TC1** `turnstile.test.ts` with Cloudflare's test site keys (pass, fail, replayed token).
- **TC2** `access-jwt.test.ts` (valid, wrong aud, expired, missing).
- **TC3** `accounts.test.ts` (fake OIDC provider; allowed and not allowed).
- **TC4** Go `TestDeployAccessApp`, `TestDeployTurnstileWidget` (fake CF API).

## Edge cases

- **EC1** Turnstile unavailable (network): the widget retries; after 3 failures the page explains and
  offers retry; `play` is never granted without validation.
- **EC2** Access seat limit reached (Zero Trust plan): the deploy job reports it; the mode stays on the
  previous value.
- **EC3** Switching from `access` back to `turnstile` deletes the Access app after the new mode is live.

## Dependencies

34.4, 34.10 (deploy job); Epic 25 cloud accounts (phase D only).
