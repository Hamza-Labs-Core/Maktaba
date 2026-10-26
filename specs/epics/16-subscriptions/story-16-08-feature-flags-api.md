# Story 16.8 — API: feature-flag resolution endpoint

**Status:** **NEW** — added in response to
[REVIEW §3.2](../../REVIEW.md): Story 16.6 referenced
`GET /api/me/flags` with no implementation owner. This story owns the
endpoint, the resolver, and the signed-flag scheme that prevents
client-side tampering.

## AC

### Schema

- New table `feature_flag_overrides` (admin-controllable):
  - `id UUID PRIMARY KEY`
  - `flag_key TEXT NOT NULL`
  - `scope TEXT NOT NULL CHECK (scope IN ('global','tier','user','cohort'))`
  - `scope_value TEXT` (NULL for global; tier name; user_id; cohort
    name)
  - `value JSONB NOT NULL`
  - `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
  - `expires_at TIMESTAMPTZ`
  - `created_by UUID REFERENCES users(id)`
- New table `beta_cohorts`:
  - `user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE`
  - `cohort TEXT NOT NULL`
  - `joined_at TIMESTAMPTZ NOT NULL DEFAULT now()`
  - `PRIMARY KEY (user_id, cohort)`
- Migration owner: this story.

### Resolver

The flag set returned to a user is the merged result of:

1. **Defaults** baked into the binary (each flag declares its default).
2. **Tier overrides** from the active license (`free`, `home`, `pro`).
3. **Cohort overrides** from `beta_cohorts` rows for this user.
4. **User overrides** from `feature_flag_overrides` with
   `scope = 'user' AND scope_value = user_id`.

Higher-numbered overrides win.

### Endpoint

- `GET /api/me/flags` →
  ```
  200 {
    flags: { <flag_key>: <value>, ... },
    signature: "<Ed25519 over canonicalized flags JSON>",
    issued_at, expires_at
  }
  ```
  - Signed by the server's long-term Ed25519 key (Epic 10 Story 10.6)
    so that clients can detect local-cache tampering as required by
    Story 16.6 EC.
  - Cache TTL = 60 s (matches Story 16.6).
- `POST /api/admin/flags/overrides` (admin) — create an override.
- `PATCH /api/admin/flags/overrides/{id}` (admin) — modify.
- `DELETE /api/admin/flags/overrides/{id}` (admin) — remove.
- `GET /api/admin/flags` (admin) — see the resolution for any user.
- `POST /api/admin/cohorts/{cohort}/users {user_ids: []}` (admin) — add
  users to a cohort.
- `DELETE /api/admin/cohorts/{cohort}/users/{user_id}` (admin) — remove.
- User can opt into cohorts they're allowed to see via
  `POST /api/me/cohorts {cohort}` (only for cohorts marked
  `user_opt_in = true`).

### Caching & invalidation

- Server-side resolution is in-memory cached per
  `(user_id, license_state_version)` for 60 s.
- A `flags_changed` Postgres `LISTEN` channel notifies replicas to
  invalidate when an override is created/modified/deleted.

### Audit

- Every admin write to `feature_flag_overrides` writes an `audit_log`
  row with `category = 'flags'`.

## TC

- New user on `home` tier: `GET /api/me/flags` returns the `home`-tier
  defaults, signed.
- Add a user override `analytics: true`: next call returns
  `analytics: true` for that user; other users unchanged.
- Tamper with the local cache (rewrite `flags.relay = true`): next
  signed-action call detects the signature mismatch and refuses.
- Admin moves a user out of a beta cohort: next call (within 60 s)
  reflects the change.
- Token rotated: signatures with the old key are still accepted for
  one cycle (the response includes `kid`); after that they fail.

## EC

- Conflicting overrides at the same scope (two user-scoped rows for the
  same flag): the most recently created wins; the older row is
  considered stale and warned in the admin UI.
- License lapsed mid-session: the next refresh returns the `free`-tier
  resolution; the signed bundle's `expires_at` enforces the cutover
  even if the cache is stale.
- A flag that's never declared in the binary's defaults: ignored by the
  client (forward-compat); admin UI warns "unknown flag".
- Cohort has 100k users: the admin-add endpoint accepts batched IDs in
  chunks of 1k; larger requests return `413`.

## Implementation

Built in `api/internal/flags` (declarations, resolver, store, signed
bundle) and `api/internal/handlers/flags` (routes), wired by
`router.MountFlags` from `main.go` over the one entitlement store
(`router.EntitlementStore`). Migration slot **0157**
(`0157_feature_flags.sql` + `.sqlite.sql`): `feature_flag_overrides`,
`beta_cohorts`, and `flags` added to `audit_log_category_check`.

Routes (every role rule enforced server-side, with role tests):
`GET /api/me/flags`, `GET /api/me/flags/keys` (the identity JWKS),
`GET|POST /api/me/cohorts`, `DELETE /api/me/cohorts/{cohort}` (any
signed-in user; 401 without a credential); `GET /api/admin/flags[?user_id=]`,
`POST /api/admin/flags/overrides`, `PATCH|DELETE /api/admin/flags/overrides/{id}`,
`POST /api/admin/cohorts/{cohort}/users`, `DELETE /api/admin/cohorts/{cohort}/users/{user_id}`
(admin only: no credential 401, viewer and play-scoped cloud sign-in 403).

Differences from the spec, following the repo:

- **Paid flags are the license.** `cloud_relay`, `multi_user`,
  `cloud_metadata_backup`, `advanced_analytics` and `federation` are
  `Entitlements.Allows(...)` of the shared store, so the bundle never
  disagrees with the server's gates. An override can turn one off, never
  on (the license alone grants a paid feature). Flag names follow the
  `subscriptions.Feature` constants, not the plan's `relay`/`backup`.
- **Resolution order** adds a `global` scope (the schema has it) before
  tier: default → global → tier → cohort → user; within a scope the most
  recently created row wins and the admin view marks the older one
  `stale`. Admin-only flags (`flags_admin`) are dropped from a
  non-admin's bundle. Declared flags are booleans.
- **Signing key**: the Story 10.18 identity (`auth/serverkeys`) was not
  loaded anywhere; `router.LoadServerIdentity` now loads it
  (`MAKTABA_SERVER_IDENTITY_PRIVATE_PEM`, else sealed with the home
  secrets key under `MAKTABA_STATE_DIR/identity` or beside the secrets
  key file). If it cannot load, a per-process key signs (warned). The
  signature is base64url Ed25519 over the sorted-key JSON of
  `{expires_at, flags, issued_at, kid, tier}`; the response carries `kid`
  and the previous key verifies through the 72 h rotation overlap. A
  self-hosted box has its own key, so clients fetch it from
  `/api/me/flags/keys` instead of a build-time bundle.
- **Cache invalidation**: the repo has no generic LISTEN helper and a
  home runs one API process, so there is no `flags_changed` trigger.
  The 60 s cache (keyed by user, role and the entitlement snapshot, so a
  license apply/lapse shows at once) is dropped in process on every
  write; a write made outside the API shows within 60 s.
- **License lapse**: `expires_at` is always issue + 60 s (the license's
  own expiry is not read: the subscriptions package is being rewritten
  in parallel and only `Current`/`Tier`/`Allows` are used).
- **Cohorts** are declared in the binary (`early_access`, user opt-in);
  admins may also use undeclared cohort names. No extra table.
  `POST /api/me/cohorts` for anything but a declared opt-in cohort is 403;
  members cannot leave an admin-assigned cohort.
- **Unknown flags**: an override for an undeclared key is stored and
  returned with `unknown: true` in the admin view, but left out of
  bundles. Past `expires_at` at create/patch is refused (422).
- Batch add: at most 1000 ids (413 above); unknown user ids → 422.
- Audit: `audit_log` rows with `category = 'flags'`
  (`flags.override.created|updated|deleted`, `flags.cohort.added|removed`).
- No admin UI page was built (the API is complete; `flags_admin` is
  there for one).
