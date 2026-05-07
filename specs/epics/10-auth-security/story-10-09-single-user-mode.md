# Story 10.9 — Single-user mode (admin token bypass)

The zero-config path for self-hosters (§9.8): an env-configured admin
token bypasses the user table.

**AC-1 — Admin token presence enables bypass.**
- **Given** `MAKTABA_ADMIN_TOKEN` is set,
- **When** any request carries `Authorization: Bearer <that-token>` (or
  cookie `mkt_admin_token=<that-token>`),
- **Then** the request is treated as the synthetic user with the fixed
  sentinel UUID `00000000-0000-0000-0000-000000000001` (the same UUID
  pre-seeded into `users` per the schema in [README.md](README.md) and
  referenced by Epic 4 NFR Story 19.8). `is_admin=true`. No DB lookup
  is performed for authentication; the row exists only so audit and
  user-scoped FK references resolve.

**AC-2 — Constant-time compare.**
- **Given** a candidate token,
- **When** compared to the configured token,
- **Then** the comparison uses constant-time equality (no early exit on
  length or content).

**AC-3 — UI bootstrap.**
- **Given** the user has no other auth configured,
- **When** they first open the web UI,
- **Then** a one-time "paste your admin token" dialog stores the token
  in `localStorage` (and the SPA sends it as a cookie). The user can
  later create real user accounts from settings.

**AC-4 — Token rotation.**
- **Given** the admin restarts the API with a different value of
  `MAKTABA_ADMIN_TOKEN`,
- **When** an old-token request arrives,
- **Then** it is rejected as 401. There is no grace period for env-var
  rotation (this is operator-driven, not user-driven).

**AC-5 — Synthetic-user `lib[]`.**
- **Given** the admin-token bypass path mints a JWT for any internal
  consumer (e.g., the same minter as Story 10.8 used by an internal
  background task),
- **When** the JWT is built,
- **Then** the `lib` claim contains every library id in the system (the
  sentinel admin has full read access by definition).

**Test cases:**
- Integration: admin token bypass works; an empty env var means no
  bypass (random tokens cannot accidentally match).
- Security: a 1-char-different token is rejected (no early exit).
- Integration: an audit row written under the admin-token path uses the
  sentinel UUID as `actor_user_id` (proves the linkage).

**Edge cases:**
- Both admin token *and* user table populated — both authentication
  paths work; the admin-token path always lands on the sentinel admin
  user.
- A weak admin token (e.g. 8 chars) — refused at boot with `error:
  admin-token-too-short` (require ≥32 chars).
