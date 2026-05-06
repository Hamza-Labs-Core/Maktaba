# Story 25.3 — Google OAuth sign-in

> Epic 25 · Cloud relay · Phase 1 (identity)

## Description

Users can register and sign in to Maktaba Cloud with their Google
account. The flow is OIDC authorization-code with PKCE; we trust
Google's `sub` and `email_verified=true` for first-party identity
proof. This story establishes the **provider-agnostic OAuth shape**
that 25.4 (Apple) reuses with provider-specific tweaks.

End-to-end UX:

1. User clicks "Continue with Google" on web/mobile.
2. Client opens `GET /api/auth/oauth/google/start?next=/dashboard` in
   the system browser (`ASWebAuthenticationSession` on iOS,
   `Custom Tabs` on Android, `window.location` on web). The cloud
   issues a state-parameter cookie (HMAC-signed, 10 min TTL,
   `SameSite=Lax`, `HttpOnly`, `Secure`) and redirects to Google.
3. After consent, Google redirects to
   `GET /api/auth/oauth/google/callback?code=...&state=...`.
4. Cloud verifies state cookie, exchanges `code` at Google's token
   endpoint with PKCE verifier, validates the ID token's `iss`,
   `aud`, `exp`, and signature against Google's JWKS.
5. Cloud finds-or-creates the user, issues the same
   `(access_token, refresh_token)` pair as 25.2.
6. Cloud redirects to `next` (validated against an allow-list of
   `*.maktaba.app` hosts and `app.maktaba.local`).

Account-collision behavior:

- **No existing user, new Google sub.** Create `cloud_users` and
  `cloud_identities` rows in one transaction; mark
  `email_verified_at = now()` (Google verified it for us if
  `email_verified=true`); skip the verification email.
- **Existing Google identity (`provider, provider_user_id`).**
  Just sign in.
- **No Google identity but email collides with an existing
  user.** Two cases:
  - Email is verified at our end → present an "account merge"
    confirmation: "We already have an account for foo@x.com.
    Continue to link Google to it?" If user confirms, insert
    `cloud_identities` linking the new Google sub to the existing
    user. Audit row `auth.identity_linked`.
  - Email is *not* verified at our end → reject the OAuth flow
    with a hint to log in with the password first; never auto-link
    onto an unverified email (claim-jacking risk).

## Acceptance criteria

- **Given** a user with no existing account,
  **when** they complete Google sign-in,
  **then** a new `cloud_users` row is created with
  `email_verified_at` set, a `cloud_identities` row links the
  Google `sub`, and the response is a 302 to `next`.
- **Given** a user has previously linked Google,
  **when** they sign in with Google again,
  **then** no new rows are created; sessions are issued.
- **Given** a Google profile with `email_verified=false`,
  **when** the callback arrives,
  **then** registration is refused with a 400 page
  `{"error":"google_email_not_verified"}` and no rows are
  created.
- **Given** a state cookie that does not match the callback
  state parameter,
  **when** the callback arrives,
  **then** the response is `400 invalid_state` and the
  abuse log records `kind=oauth_state_mismatch`.
- **Given** a code-exchange request that fails at Google,
  **when** the cloud retries,
  **then** the user sees a `502 oauth_provider_error` page
  with a "try again" button.
- **Given** the email returned by Google collides with an
  existing verified account,
  **when** the callback arrives,
  **then** the user is redirected to `/oauth/merge?token=...`
  where they confirm the link with their existing password.
- **Given** the `next` parameter points to
  `https://evil.example.com`,
  **when** the start endpoint validates it,
  **then** the request is rejected with `400 bad_redirect`.
- **Given** PKCE was started with `code_challenge=X`,
  **when** Google redirects without a matching `code_verifier` on
  exchange,
  **then** the exchange fails (Google returns 400; cloud
  surfaces it).

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | unit        | mock Google ID token signed by test JWKS | verify | passes |
| T02 | unit        | ID token with `aud != client_id` | verify | rejected `bad_aud` |
| T03 | integration | start endpoint hit directly without a UA cookie | call | 302 to `accounts.google.com` with state set |
| T04 | integration | full flow against Google staging | sign in | new rows created, redirect succeeds |
| T05 | integration | hit callback with stale `state` cookie (12 min) | call | 400 |
| T06 | integration | re-sign-in with same Google sub | call | no duplicate rows |
| T07 | integration | verified email collision | callback | 302 to `/oauth/merge?token=` |
| T08 | integration | unverified email collision | callback | 409 `verify_password_first` |
| T09 | unit        | `next=https://attacker.tld/x` | start | 400 `bad_redirect` |
| T10 | unit        | `next=https://my.maktaba.app/dashboard` | start | accepted |
| T11 | regression  | mobile flow with `ASWebAuthenticationSession` | sign in | tokens delivered to app via deep-link `maktaba://oauth/done?...` |
| T12 | integration | clock skew 30s vs Google JWKS | verify | within tolerance, accepted |

## Edge cases

- **Email change at Google.** A user changes their Gmail address.
  Google `sub` is stable, so the link survives; we update
  `cloud_identities.email_at_provider` for audit and never
  overwrite `cloud_users.email`.
- **Workspace user signed in to multiple Google accounts.** OIDC
  asks Google's account picker; this is Google's UX, not ours.
- **Google revokes our app.** `oauth_provider_error` 502 with a
  "log in with email instead" link; existing sessions remain
  valid until they expire.
- **Replay of an old code.** Google's code is single-use;
  the second exchange returns 400 from Google. We surface
  `oauth_provider_error` and clear the state cookie.
- **CSRF via deep link.** `state` cookie + parameter binding plus
  PKCE prevents both CSRF and authorization-code injection.
- **Account merge token.** The `/oauth/merge` token is HMAC-signed,
  carries `{provider, provider_user_id, exp=10min}`, single-use.
  The merge confirmation requires the user to enter their
  existing password (re-auth) — never just "click confirm".
- **Mobile redirect.** iOS/Android use `ASWebAuthenticationSession`
  / `Custom Tabs`; the redirect URI is a custom scheme
  (`maktaba://oauth/done`). Cloud accepts both
  `https://app.maktaba.app/oauth/done` (web) and the custom
  scheme; we sign tokens into a query fragment so they don't
  appear in server logs.
- **GDPR right-to-erasure for OAuth-only users.** Delete cascades
  to `cloud_identities`; we keep an audit row noting `provider=google,
  provider_user_id=<hash(sub)>` for fraud bookkeeping (90 days).
- **Locale.** Google supplies `locale=en-US`; we use it as the
  default if the user hasn't set one. User can override later.

## Files / packages

- `cloud/internal/auth/oauth/oauth.go` — provider-agnostic state.
- `cloud/internal/auth/oauth/google.go` — Google specifics.
- `cloud/internal/auth/oauth/merge.go` — collision resolution.
- `cloud/internal/auth/oauth/redirect_validator.go`.
- `cloud/configs/cloud.example.toml` — `[oauth.google]
  client_id=..., client_secret=ENV` placeholders.

## Open questions

- **Google one-tap.** Out for v1; standard button only.
- **Restricted to Workspace?** Personal + Workspace both allowed;
  we don't restrict by `hd` claim.
