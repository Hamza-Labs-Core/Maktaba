# Story 10.1 — User store + argon2id passwords

`users` (§8.5) holds identities. Passwords are argon2id with
configurable memory/time per `[auth]` config (§11.2).

**AC-1 — Hash creation.**
- **Given** a request to set a password,
- **When** the hasher runs,
- **Then** the hash is argon2id with parameters `(memory=65536 KiB,
  time=2, parallelism=1, salt=16 random bytes, hash=32 bytes)` (defaults
  per §11.2). The stored string is the standard `$argon2id$...` PHC
  format including parameters, so future config changes don't invalidate
  existing hashes.

**AC-2 — Constant-time verify.**
- **Given** a stored hash and a candidate password,
- **When** verified,
- **Then** the comparison uses argon2id's built-in constant-time verify
  and never logs the password or hash.

**AC-3 — Admin user CRUD.**
- **Given** an admin,
- **When** `POST /api/users {username, password, is_admin?}` is sent,
- **Then** a row is inserted with the hashed password; the response
  excludes `pw_hash`.
- `PATCH /api/users/{id}` allows changing `username`, `password`,
  `is_admin`. `DELETE /api/users/{id}` cascades to `playback_state`,
  `saved_searches`, `web_sessions`, and `refresh_tokens`.
- `POST /api/users/{id}/unlock` (admin only) clears
  `failed_attempts=0, locked_until=NULL`. Required by Epic 10
  Story 10.11 EC.
- `DELETE /api/users/{id}/sessions/{session_id}` revokes a single
  web session.

**AC-4 — CLI for first user.**
- **Given** an empty `users` table,
- **When** `maktaba-api adduser <username>` is run,
- **Then** the password is prompted (no echo), hashed, and the user is
  inserted with `is_admin=true`. Used in the bootstrap path before any
  HTTP user exists.

**Test cases:**
- Unit: hash verifies; same password produces a different hash (random
  salt); a different password fails verify.
- Unit: argon2 parameters from config thread through to the stored hash
  string.
- Security: a password 1024 chars long is hashed without DoS (capped at
  `password_max_len`, default 256, returning 422 on overflow).
- Integration: `POST /api/users/{id}/unlock` clears the lock and
  `failed_attempts`.
- Integration: `DELETE /api/users/{id}/sessions/{session_id}` revokes
  exactly one session.

**Edge cases:**
- Username conflict — 409 `type: username-exists`. Compared
  case-insensitively (Unicode casefold) for the uniqueness check;
  display preserves original casing.
- `is_admin` change is allowed only by another admin; a user cannot
  promote themselves.
- Deleting the last admin → 409 `type: last-admin`. The system always
  has at least one admin.
