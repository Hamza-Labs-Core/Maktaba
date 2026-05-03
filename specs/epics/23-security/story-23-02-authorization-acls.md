# Story 23.2 — Authorization and ACLs

Library-level and action-level authorization at every API entry point.
"Single-user" is a special case of multi-user with one user.

The `library_ids` claim on streaming JWTs is canonical for offline
authorization at the streaming edge. This story owns the JWT shape
contract and the streaming-side check; the API-side minter (Epic 10)
is required to populate the claim. The previous Epic 10.3 / 10.8 JWT
shape (without `library_ids`) is superseded.

## Acceptance criteria

- AC1. Every authenticated REST/GraphQL handler runs through a single
  `authorize(action, resource)` call; missing the call fails a CI
  lint that scans handler functions.
- AC2. ACL roles: `admin`, `editor` (can ingest, edit metadata),
  `viewer` (read + watch only). Per-library ACL rows live in
  `library_acl` (owned by
  [Story 19.8](../19-scalability/story-19-08-multi-tenant-readiness.md));
  the row defaults to `admin` for the library creator.
- AC3. **JWT claim contract for streaming.** Manifest-bearing and
  signed-URL JWTs minted by the API include the following claims:
  - Standard: `iss`, `aud`, `sub` (user UUID for session JWTs;
    `session_id` for signed-URL JWTs), `iat`, `exp`, `jti`, `kid`.
  - Authorization: `usr` (user UUID, even on signed-URL JWTs), `lib`
    (array of library UUIDs the user is currently entitled to), and
    `is_admin`.
  - Audiences are one of `streaming`, `streaming-direct`,
    `streaming-static` (poster/sprite/subtitle endpoints; Epic 8.13
    middleware honors this audience).
  Streaming validates JWT signature, audience, expiry, and checks
  the `lib` claim against the requested resource's library before
  issuing a manifest, segment, poster, sprite, or subtitle. Expired
  JWTs produce a clear `403` (not 401).
- AC4. Revocation lag. Because access JWTs are short-lived (15 min),
  in-flight segments continue until the JWT expires even after the
  user's `library_acl` row is removed. The 15-min revocation lag is
  documented in the operator guide; for instant revocation the admin
  uses `EvictHashCache` and rotates the signing key.
- AC5. Admin-only routes (`/api/system/*`, `/api/auth/users`) require
  `is_admin=true`; tested per route.

## Test cases

- TC1. Lint: a new handler missing `authorize()` fails CI.
- TC2. Cross-tenant: a `viewer` on library A cannot read or stream a
  video in library B; both REST and gRPC paths covered.
- TC3. Privilege escalation: a `viewer` cannot promote themselves;
  `editor` cannot promote anyone; `admin` can.
- TC4. JWT shape: a manifest-mint endpoint produces a JWT containing
  `lib`, `usr`, `is_admin`; the streaming verifier rejects a JWT
  missing `lib`.
- TC5. Audience separation: a `streaming-static` JWT is accepted for
  poster/sprite/subtitle paths but rejected for segment paths; a
  `streaming` JWT is accepted for segments.

## Edge cases

- EC1. JWT `lib` claim that the user has since lost — the next
  manifest refresh detects the change; in-flight segments continue
  until the JWT expires (≤ 15 min).
- EC2. Admin removes the only admin — refused with a constraint
  error; documented.
- EC3. Library deleted while a user has an active session — open
  sessions error gracefully with `library_gone`; the UI returns to
  the home screen.
