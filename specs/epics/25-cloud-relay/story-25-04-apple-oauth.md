# Story 25.4 — Apple OAuth (Sign in with Apple)

> Epic 25 · Cloud relay · Phase 1 (identity)

## Description

Sign in with Apple is **mandatory** for the iOS App Store: any app that
offers third-party sign-in (Google, here) must also offer Apple. This
story implements it. The flow reuses the OAuth scaffolding from 25.3
with three Apple-specific quirks:

- **Profile data is sent only on first auth.** Apple does not return
  `name` or `email` on subsequent sign-ins; we must persist them on
  the very first callback or lose them forever. The first response
  is the only chance.
- **Private email relay.** Users may pick "Hide my email" — Apple
  returns a relay address (`xyz123@privaterelay.appleid.com`) that
  forwards to the user's real address. We treat the relay address
  as the canonical email and never try to "resolve" it.
- **Client secret is a JWT we mint.** Apple does not give a static
  client secret — we sign a JWT (`ES256`, audience
  `https://appleid.apple.com`, expiry ≤ 6 months) using the
  `AuthKey_*.p8` downloaded from the Apple Developer console.

UX matches Google:

1. User clicks "Continue with Apple". On native iOS we use
   `ASAuthorizationAppleIDProvider` which short-circuits the browser
   flow and returns an authorization code + identity token directly
   to the app; the app POSTs them to
   `POST /api/auth/oauth/apple/native`.
2. On non-Apple platforms (Android, web, Windows desktop), the
   web flow runs:
   `GET /api/auth/oauth/apple/start` →
   `https://appleid.apple.com/auth/authorize?...` →
   `POST /api/auth/oauth/apple/callback` (Apple uses POST, not GET,
   on form_post mode, which we use).
3. Cloud verifies the ID token against Apple's JWKS, exchanges the
   code for refresh+id tokens (we don't store Apple refresh tokens
   long-term — single-use only).
4. Find-or-create + link logic identical to 25.3.

## Acceptance criteria

- **Given** a user signs in with Apple for the first time and
  shares their real email,
  **when** the callback arrives,
  **then** a `cloud_users` row is created with the real email,
  `email_verified_at` set, and the response carries a JWT.
- **Given** a user signs in with Apple's "Hide my email",
  **when** the callback arrives,
  **then** a `cloud_users` row is created with the relay
  address as `email`, and `cloud_identities.email_at_provider`
  is also the relay address.
- **Given** a returning Apple user,
  **when** they sign in again,
  **then** Apple does **not** return `name`/`email` and the cloud
  finds them by `(provider='apple', provider_user_id=<sub>)`
  alone.
- **Given** the Apple client-secret JWT has expired,
  **when** the cloud tries to exchange a code,
  **then** the JWT is automatically re-minted from the cached
  `.p8` file; success.
- **Given** the iOS app calls
  `POST /api/auth/oauth/apple/native` with the
  `authorizationCode` and `identityToken`,
  **when** the cloud verifies the identity token,
  **then** the same find-or-create flow runs and the response
  body is `{access_token, refresh_token, expires_in, user}`.
- **Given** the user signed up via "Hide my email" but later
  switched to a real email at Apple's site,
  **when** they sign in again,
  **then** Apple returns the same `sub` and we sign them in
  unchanged; we do not overwrite `cloud_users.email`.
- **Given** the user revoked Sign in with Apple at appleid.apple.com,
  **when** Apple POSTs to our `notifications` webhook with
  `event=consent_revoked`,
  **then** all sessions for the linked user are revoked and
  the `cloud_identities` row is marked `revoked_at`.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | unit        | sign client-secret JWT with test `.p8` | verify against test public key | valid |
| T02 | unit        | mock identity token | verify | accepted, sub extracted |
| T03 | integration | first-time relay-email sign-in | full flow | user has `@privaterelay.appleid.com` email |
| T04 | integration | second sign-in (no email/name in response) | flow | found by sub, signed in |
| T05 | integration | apple notifications webhook, `email-disabled` | POST | identity revoked, session killed |
| T06 | integration | iOS native flow with `identityToken` | POST `/native` | tokens issued |
| T07 | unit        | identity token signed by stale JWKS key | verify | rejected, JWKS refresh triggered |
| T08 | unit        | client-secret JWT 7 months old | refresh | re-minted before request |
| T09 | regression  | email collision against existing verified user | callback | merge prompt, not auto-link |
| T10 | regression  | apple `email_verified=false` (rare; typed-in email path) | flow | rejected with `apple_email_not_verified` |

## Edge cases

- **Apple's `email_verified` claim.** Almost always `true`; rare
  cases when user typed an email manually. Treat `false` as a
  rejection.
- **Locale handling.** Apple does not return `locale`; we infer
  from `Accept-Language` at the redirect time.
- **`name` is fragile.** Returned only on first auth; if the user
  refused name disclosure or the app crashed on first redirect,
  we never see it. Default to "Apple User" and let the user set
  it from `PATCH /api/me`.
- **Relay forwarding.** Apple may stop forwarding our emails if
  the user disables forwarding at appleid.apple.com. Bounce
  handling from 25.2 (Postmark hard-bounce) covers this; we
  show a banner in app: "your provider stopped forwarding our
  email — switch to a real address".
- **Apple's `notifications` webhook.** Three events:
  `email-disabled`, `email-enabled`, `consent-revoked`,
  `account-delete`. We honor `consent-revoked` and
  `account-delete` by revoking sessions and severing the
  identity link. `email-disabled` we just record.
- **Multi-team Apple developer account.** Each Apple Developer
  Team has a different `team_id` baked into the client-secret
  JWT. Document which team owns Maktaba; rotation involves
  updating `team_id` and re-minting.
- **`.p8` rotation.** Operationally we rotate the AuthKey every
  12 months; the cloud reads the path from
  `/var/run/secrets/apple/AuthKey_<KID>.p8`. CD pipeline
  swaps the symlink and triggers a JWT cache flush.
- **App Store privacy nutrition labels.** Each datum we collect
  must be declared. Document the exact fields: `email`
  (identifier), `name` (display, optional), no others.

## Files / packages

- `cloud/internal/auth/oauth/apple.go` — provider impl.
- `cloud/internal/auth/oauth/apple_jwt.go` — client-secret minter.
- `cloud/internal/auth/oauth/apple_notifications.go` — webhook.
- `cloud/configs/cloud.example.toml` —
  `[oauth.apple] team_id=..., key_id=..., client_id=..., key_path=...`.

## Open questions

- **App Store-attached subscriptions.** If we accept Apple sign-in,
  do we have to offer Apple IAP for subscriptions? Apple's rules
  changed in 2024 — *if* we offer external billing alongside,
  we need a "select payment method" sheet. **Punt to 25.13**;
  this story does not enable purchases over Apple, just identity.
- **macOS native Sign in with Apple.** Tauri (Epic 13) has no
  first-class wrapper; we use the web flow on desktop. Acceptable.
