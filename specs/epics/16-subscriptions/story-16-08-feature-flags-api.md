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
