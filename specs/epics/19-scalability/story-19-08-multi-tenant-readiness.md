# Story 19.8 — Multi-tenant readiness (deferred capacity)

Single-user is v1; the schema and identity surfaces must not preclude
multi-user later.

## Acceptance criteria

- AC1. Every user-scoped row (watch progress, collections-by-user) has
  a `user_id` column; single-user mode uses a sentinel `user_id =
  '00000000-0000-0000-0000-000000000001'` so no schema migration is
  needed to flip on multi-user.
- AC2. The API's auth layer treats single-user mode as "all requests
  authorized as the sentinel user," with a feature flag to require
  real auth. The `MAKTABA_ADMIN_TOKEN` bypass path described in
  [Story 23.1](../23-security/story-23-01-authentication.md) AC5 maps
  the synthetic admin's `user_id` to **the same** sentinel UUID; this
  linkage is documented and asserted in tests so single-user data
  produced via admin-token bypass is identical to single-user data
  produced via the auth layer.
- AC3. Per-library ACL rows live in a `library_acl(library_id, user_id,
  role)` table (FK to `libraries(id)` and `users(id)`, `role` constrained
  to `admin|editor|viewer`). The migration owner is this story; it
  introduces the table and the partial unique index on
  `(library_id, user_id)`. In single-user mode the row for the sentinel
  user is implicit (a synthetic `(library_id, sentinel, 'admin')` is
  treated as present without a row write), and the multi-user feature
  flag flip materializes the implicit rows in a backfill.
- AC4. A migration plan from single-user → multi-user is documented
  and tested by an integration test that flips the flag and asserts
  data continuity, including the implicit-ACL-row backfill.

## Test cases

- TC1. Schema audit: every user-bearing table has `user_id NOT NULL`
  with the sentinel as default for v1.
- TC2. Flag flip: enable multi-user mode on a seeded single-user DB;
  log in as the sentinel-mapped account; all collections and watch
  state are visible. The backfill creates the
  `library_acl(library_id, sentinel, 'admin')` rows.
- TC3. ACL: in multi-user mode, a non-owner user cannot list videos in
  another user's library.
- TC4. Admin-token sentinel link: with `MAKTABA_ADMIN_TOKEN` set, every
  authenticated request resolves to `user_id = sentinel`; rows written
  via admin-token are indistinguishable from rows written via the
  auth-layer single-user path.

## Edge cases

- EC1. Pre-existing rows without `user_id` after an external import —
  the migration backfills with the sentinel and logs the count.
- EC2. JWT subject mismatch with a watch-progress row — the read
  succeeds (publicly readable in single-user) but the write is
  rejected.
- EC3. The sentinel UUID conflicting with a real user — forbidden by
  a check constraint; documented.
