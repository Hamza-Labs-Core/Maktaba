# Plan 10.1 — User store + argon2id passwords — implementation

> Implementation plan for [story-10-01-user-store.md](story-10-01-user-store.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. This plan owns the `users` table (epic
> [README.md](README.md) §`users`) and is the bedrock for every other
> Epic 10 story — Stories 10.2 (web login), 10.3 (native login), 10.5
> (logout), 10.9 (single-user mode), 10.11 (lockout), 10.13 (permission
> model) all consume `User` and `password.Verify`. We refuse to
> introduce a foreign-key dependency from anywhere else in the codebase
> until this lands.

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Hash library: `github.com/alexedwards/argon2id`** with PHC-string serialisation, params `(memory=65536 KiB, time=2, parallelism=1, salt=16, key=32)`. We do NOT roll our own using `golang.org/x/crypto/argon2`. | arch §11.2 (`argon2_memory_kib = 65536`); story AC-1; epic README "argon2id with parameters". | The PHC string is the contract: it carries `m, t, p, salt, hash` so future config bumps don't invalidate stored hashes (AC-1's explicit requirement). `alexedwards/argon2id` produces exactly the canonical `$argon2id$v=19$m=...,t=...,p=...$<salt>$<hash>` string and provides a constant-time `ComparePasswordAndHash` (AC-2). Rolling our own in 30 lines forces us to also write the PHC parser, which is exactly the bug we don't want. |
| D2 | **`pw_hash` is `TEXT NOT NULL`** (departure from arch §8.5 which has it nullable). The sentinel admin row carries the literal string `'<unsalted-disabled>'` — a value that fails `ComparePasswordAndHash` for every input, by design. | epic README schema (`pw_hash TEXT NOT NULL`); arch §8.5 (`pw_hash TEXT, ...null for single-user`). | A nullable column means every login path has to special-case `pw == nil` and remember not to short-circuit it. The single-user bypass (Story 10.9) is gated on the `MAKTABA_ADMIN_TOKEN` env var, not on a NULL hash. Making the column NOT NULL means the password-verification function has exactly one shape. The sentinel string is rejected by argon2id's verify because it isn't a valid PHC string — `ComparePasswordAndHash` returns `argon2id.ErrInvalidHash`, which we map to a generic auth failure. |
| D3 | **Username uniqueness via `UNIQUE (lower(username))`**, not `UNIQUE (username)`. The display column preserves the user's original casing; the case-insensitive comparison happens in the index. | epic README schema; story EC "case-insensitive (Unicode casefold)"; story AC-1. | Postgres `lower()` is locale-stable for ASCII and good enough for Unicode casefold of the usernames we expect (Latin + Arabic letters; neither has casing). For full Unicode casefold (Turkish dotted-i etc.) we'd need an `IMMUTABLE` SQL function on `LOWER` from `unaccent`/ICU; we explicitly do not need that for v1 and will revisit if a user reports a conflict. The `lower()` expression index Postgres builds is correct and serves both the lookup query (`WHERE lower(username) = lower($1)`) and the uniqueness constraint with no double-write. |
| D4 | **Sentinel admin UUID `00000000-0000-0000-0000-000000000001`** seeded by the migration itself, not by a separate boot step. | epic README "Sentinel for the single-user/admin-token bypass path"; Story 10.9; Story 19.8 attribution. | The audit log's `actor_user_id` foreign key points at this row whenever the admin-token path mutates state. If the row weren't there, audit inserts during single-user-mode boot would fail. Putting the INSERT in the migration is the only way to guarantee the row exists before any code can run. We use `ON CONFLICT (id) DO NOTHING` so the migration is idempotent across re-runs. |
| D5 | **Password length cap = 256 bytes (UTF-8)**, returning `422 Unprocessable Entity` with `type: password-too-long`. The check runs BEFORE the argon2 call. | story test case "1024 chars long is hashed without DoS (capped at `password_max_len`, default 256)". | Argon2 with `m=65536, t=2` is ~30 ms on a modern CPU but rises with input length because the password is hashed into the H0 derivation. A 1 MB password takes >5 s and is a trivial DoS vector. Caching at 256 *bytes* (not codepoints) is what the underlying SHA-512 sees and is the canonical limit. The cap is configurable as `[auth] password_max_bytes = 256` in `api.toml` so an operator can lower it; we never raise it above 1024 for the same DoS reason. |
| D6 | **Last-admin protection** — `DELETE /api/users/{id}` and `PATCH .../is_admin=false` both refuse with `409 type: last-admin` if the target is the only `is_admin=true` row. The check is wrapped in a `SELECT ... FOR UPDATE` row-lock inside the same transaction as the destructive update so concurrent demotions can't race. | story EC "Deleting the last admin → 409 `type: last-admin`. The system always has at least one admin." | Without row-locking, two simultaneous "demote admin A" and "demote admin B" calls can both pass their pre-checks (each sees the other as a remaining admin) and both succeed, leaving zero admins. The lock is a one-statement `SELECT count(*) FROM users WHERE is_admin = true FOR UPDATE` — it serialises only admin-mutation traffic, which is rare; viewer mutations don't touch it. |
| D7 | **CLI `maktaba-api adduser`** uses `golang.org/x/term.ReadPassword` (no echo) and runs against the same `*pgxpool.Pool` the server uses, sharing the same `password.Hash` function. It does NOT reach out over HTTP. The CLI is a `cobra` subcommand under `cmd/maktaba-api`. | story AC-4. | At bootstrap there is no admin to log in as, so an HTTP call would be circular. Sharing the binary (and therefore the hashing config) means the bootstrap user's hash is identical to one created via the API later. We refuse to insert if the user count is already > 0 unless `--force` is given, to prevent operators from accidentally creating a second admin via `adduser` after the system has been in use. |

If D2 is rejected (`pw_hash` nullable as in arch §8.5): every authentication path needs a `pw_hash IS NULL → reject` branch and the sentinel row's intent gets blurred. We pay the migration cost once now to keep every later branch one-shape.

If D5 is rejected (no length cap): the API is one curl-loop away from a CPU-exhaustion DoS. A length cap is non-optional.

---

## 1. Architecture diagram — request flow

```
  ┌────────────────────────────────────────────────────────────────────┐
  │  POST /api/users   {username, password, is_admin?}                 │
  │   header: Authorization: Bearer <admin-jwt> | Cookie mkt_sess=...  │
  └──────────────────────────────┬─────────────────────────────────────┘
                                 │
                                 ▼
                ┌────────────────────────────────────┐
                │ chi router → adminOnly middleware  │
                │  (10.13) verifies caller.is_admin  │
                └────────────────────────────────────┘
                                 │
                                 ▼
                ┌────────────────────────────────────┐
                │ handler.CreateUser                  │
                │  1. validate body, len(pw) ≤ 256   │← D5
                │  2. password.Hash(pw, cfg)         │← D1 PHC
                │  3. Repo.InsertUser(ctx, ...)      │
                │     - UNIQUE(lower(username))      │← D3
                │     - on conflict → 409            │
                │  4. respond 201 {id, username,     │
                │     is_admin, created_at}          │
                │     pw_hash NEVER returned         │← AC-3
                └────────────────────────────────────┘

  CLI bootstrap path:
  $ maktaba-api adduser alice
  Password: ********           (golang.org/x/term, no echo)
  Confirm : ********
   → password.Hash → INSERT users(...is_admin=true)        (D7)
```

---

## 2. Detailed implementation

### 2.1 Package layout (Go, API service)

```
apps/api/
├── cmd/
│   └── maktaba-api/
│       ├── main.go                  # cobra root; serve, adduser, migrate
│       ├── adduser.go               # AC-4 CLI, golang.org/x/term         (D7)
│       └── serve.go                 # http.ListenAndServe + mux wiring
└── internal/
    └── auth/
        ├── password/
        │   ├── password.go          # Hash, Verify, Params               (D1)
        │   └── password_test.go
        └── user/
            ├── repo.go              # sqlc-generated wrappers
            ├── repo_extra.go        # last-admin check (D6), unlock
            ├── service.go           # business rules (length cap, etc.)
            ├── handler.go           # POST/PATCH/DELETE /api/users
            ├── handler_test.go
            ├── routes.go            # chi.Router subrouter
            └── queries/
                └── users.sql        # sqlc input
```

### 2.2 Schema migration — `users` (full)

```sql
-- shared/db/migrations/0040_users.sql
BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;   -- gen_random_uuid

CREATE TABLE users (
    id              UUID PRIMARY KEY,
    username        TEXT NOT NULL,
    pw_hash         TEXT NOT NULL,                              -- D2
    is_admin        BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    failed_attempts INTEGER NOT NULL DEFAULT 0,
    locked_until    TIMESTAMPTZ,
    CONSTRAINT users_username_lower_unique UNIQUE (lower(username))   -- D3
);

CREATE INDEX users_admin_active
    ON users (id) WHERE is_admin = true;                       -- supports D6

-- Sentinel for single-user mode & admin-token attribution (D4).
INSERT INTO users (id, username, pw_hash, is_admin)
VALUES (
    '00000000-0000-0000-0000-000000000001',
    'admin',
    '<unsalted-disabled>',                                     -- D2
    true
)
ON CONFLICT (id) DO NOTHING;

COMMIT;
```

### 2.3 `internal/auth/password/password.go` — hashing primitives (D1)

```go
// Package password wraps github.com/alexedwards/argon2id with the params
// from architecture.md §11.2 ([auth].argon2_memory_kib = 65536, t=2, p=1).
// Stored hashes are the canonical PHC string `$argon2id$v=19$m=65536,t=2,p=1$<salt>$<hash>`
// so future config changes (e.g. memory bumps) do not invalidate existing rows.
package password

import (
	"errors"
	"fmt"

	"github.com/alexedwards/argon2id"
)

// Params mirrors [auth] in api.toml. Defaults match arch §11.2.
type Params struct {
	MemoryKiB   uint32 // 65536
	Time        uint32 // 2
	Parallelism uint8  // 1
	SaltLen     uint32 // 16
	KeyLen      uint32 // 32
	MaxBytes    int    // 256 (D5)
}

func DefaultParams() Params {
	return Params{
		MemoryKiB: 65536, Time: 2, Parallelism: 1,
		SaltLen: 16, KeyLen: 32, MaxBytes: 256,
	}
}

// ErrPasswordTooLong is returned when the password exceeds MaxBytes (D5).
// The handler maps this to 422 type: password-too-long.
var ErrPasswordTooLong = errors.New("password: exceeds max length")

// ErrInvalidCredentials is the constant-time-safe failure response from
// Verify; callers must NOT distinguish between "no such user" and
// "wrong password" externally (Story 10.2 AC-3).
var ErrInvalidCredentials = errors.New("password: invalid credentials")

func Hash(plaintext string, p Params) (string, error) {
	if len(plaintext) > p.MaxBytes {
		return "", ErrPasswordTooLong
	}
	cfg := &argon2id.Params{
		Memory:      p.MemoryKiB,
		Iterations:  p.Time,
		Parallelism: p.Parallelism,
		SaltLength:  p.SaltLen,
		KeyLength:   p.KeyLen,
	}
	return argon2id.CreateHash(plaintext, cfg)
}

// Verify compares plaintext against a stored PHC hash in constant time.
// Any error (malformed hash, wrong password) collapses to ErrInvalidCredentials
// so the caller cannot leak hash-format-validity through error type.
func Verify(plaintext, phcHash string) error {
	ok, err := argon2id.ComparePasswordAndHash(plaintext, phcHash)
	if err != nil || !ok {
		return ErrInvalidCredentials
	}
	return nil
}

// NeedsRehash returns true when the stored params drift from the configured
// ones (e.g. memory was bumped). Login handlers (Story 10.2/10.3) call this
// after a successful verify and trigger an opportunistic re-hash.
func NeedsRehash(phcHash string, p Params) (bool, error) {
	got, _, _, err := argon2id.DecodeHash(phcHash)
	if err != nil {
		return false, fmt.Errorf("password.NeedsRehash: %w", err)
	}
	return got.Memory != p.MemoryKiB ||
		got.Iterations != p.Time ||
		got.Parallelism != p.Parallelism, nil
}
```

### 2.4 sqlc queries (`apps/api/internal/auth/user/queries/users.sql`)

```sql
-- name: GetUserByID :one
SELECT id, username, pw_hash, is_admin, created_at, failed_attempts, locked_until
  FROM users WHERE id = $1;

-- name: GetUserByLowerUsername :one
SELECT id, username, pw_hash, is_admin, created_at, failed_attempts, locked_until
  FROM users WHERE lower(username) = lower($1);

-- name: InsertUser :one
INSERT INTO users (id, username, pw_hash, is_admin)
VALUES ($1, $2, $3, $4)
RETURNING id, username, is_admin, created_at;

-- name: UpdateUserUsername :exec
UPDATE users SET username = $2 WHERE id = $1;

-- name: UpdateUserPasswordHash :exec
UPDATE users SET pw_hash = $2 WHERE id = $1;

-- name: UpdateUserIsAdmin :exec
UPDATE users SET is_admin = $2 WHERE id = $1;

-- name: DeleteUser :execrows
DELETE FROM users WHERE id = $1;

-- name: UnlockUser :exec
UPDATE users SET failed_attempts = 0, locked_until = NULL WHERE id = $1;

-- name: CountActiveAdmins :one
SELECT count(*)::int AS n FROM users WHERE is_admin = true FOR UPDATE;

-- name: RevokeOneWebSession :execrows
UPDATE web_sessions SET revoked_at = now()
 WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL;
```

### 2.5 Handlers — `internal/auth/user/handler.go`

```go
// CreateUser: POST /api/users  (admin-only via middleware)
func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		IsAdmin  bool   `json:"is_admin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		problem.Write(w, 400, "invalid-body", "request body is not valid JSON")
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	if body.Username == "" {
		problem.Write(w, 422, "username-required", "username must be non-empty")
		return
	}

	hash, err := password.Hash(body.Password, h.pwParams)
	if errors.Is(err, password.ErrPasswordTooLong) {
		problem.Write(w, 422, "password-too-long",
			fmt.Sprintf("password exceeds %d bytes", h.pwParams.MaxBytes))
		return
	}
	if err != nil {
		h.log.Error("password.Hash failed", "err", err)
		problem.Write(w, 500, "internal", "")
		return
	}

	id := uuid.Must(uuid.NewV7()) // monotonic, time-prefixed
	row, err := h.q.InsertUser(r.Context(), repo.InsertUserParams{
		ID: id, Username: body.Username, PwHash: hash, IsAdmin: body.IsAdmin,
	})
	if pgErr, ok := isUniqueViolation(err); ok && pgErr.ConstraintName == "users_username_lower_unique" {
		problem.Write(w, 409, "username-exists",
			"a user with this username already exists")
		return
	}
	if err != nil {
		h.log.Error("InsertUser failed", "err", err)
		problem.Write(w, 500, "internal", "")
		return
	}
	writeJSON(w, 201, userView(row))
}

// PatchUser: PATCH /api/users/{id}
func (h *Handler) PatchUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		problem.Write(w, 400, "bad-id", "user id must be a UUID")
		return
	}
	var body struct {
		Username *string `json:"username,omitempty"`
		Password *string `json:"password,omitempty"`
		IsAdmin  *bool   `json:"is_admin,omitempty"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	caller := authctx.MustUser(r.Context())

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		problem.Write(w, 500, "internal", "")
		return
	}
	defer tx.Rollback(r.Context())
	q := h.q.WithTx(tx)

	// Self-promotion guard (epic README EC).
	if body.IsAdmin != nil && *body.IsAdmin && caller.ID == id && !caller.IsAdmin {
		problem.Write(w, 403, "self-promote", "users cannot promote themselves to admin")
		return
	}
	// Last-admin guard for demotion (D6).
	if body.IsAdmin != nil && !*body.IsAdmin {
		n, err := q.CountActiveAdmins(r.Context())
		if err != nil {
			problem.Write(w, 500, "internal", "")
			return
		}
		if n <= 1 {
			problem.Write(w, 409, "last-admin",
				"cannot demote the last admin")
			return
		}
		if err := q.UpdateUserIsAdmin(r.Context(), repo.UpdateUserIsAdminParams{
			ID: id, IsAdmin: false,
		}); err != nil {
			problem.Write(w, 500, "internal", "")
			return
		}
	} else if body.IsAdmin != nil {
		_ = q.UpdateUserIsAdmin(r.Context(), repo.UpdateUserIsAdminParams{
			ID: id, IsAdmin: true,
		})
	}

	if body.Username != nil {
		if err := q.UpdateUserUsername(r.Context(), repo.UpdateUserUsernameParams{
			ID: id, Username: *body.Username,
		}); err != nil {
			problem.Write(w, 500, "internal", "")
			return
		}
	}
	if body.Password != nil {
		hash, err := password.Hash(*body.Password, h.pwParams)
		if errors.Is(err, password.ErrPasswordTooLong) {
			problem.Write(w, 422, "password-too-long", "")
			return
		}
		if err := q.UpdateUserPasswordHash(r.Context(), repo.UpdateUserPasswordHashParams{
			ID: id, PwHash: hash,
		}); err != nil {
			problem.Write(w, 500, "internal", "")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		problem.Write(w, 500, "internal", "")
		return
	}
	w.WriteHeader(204)
}

// DeleteUser: DELETE /api/users/{id} — last-admin protection (D6).
func (h *Handler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	id, _ := uuid.Parse(chi.URLParam(r, "id"))
	tx, _ := h.pool.Begin(r.Context())
	defer tx.Rollback(r.Context())
	q := h.q.WithTx(tx)

	target, err := q.GetUserByID(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		problem.Write(w, 404, "no-such-user", "")
		return
	}
	if target.IsAdmin {
		n, _ := q.CountActiveAdmins(r.Context())
		if n <= 1 {
			problem.Write(w, 409, "last-admin", "cannot delete the last admin")
			return
		}
	}
	if _, err := q.DeleteUser(r.Context(), id); err != nil {
		problem.Write(w, 500, "internal", "")
		return
	}
	_ = tx.Commit(r.Context())
	w.WriteHeader(204)
}

// UnlockUser: POST /api/users/{id}/unlock — clears 10.11 lockout state.
func (h *Handler) UnlockUser(w http.ResponseWriter, r *http.Request) {
	id, _ := uuid.Parse(chi.URLParam(r, "id"))
	if err := h.q.UnlockUser(r.Context(), id); err != nil {
		problem.Write(w, 500, "internal", "")
		return
	}
	w.WriteHeader(204)
}

// RevokeSession: DELETE /api/users/{id}/sessions/{sid}
func (h *Handler) RevokeSession(w http.ResponseWriter, r *http.Request) {
	uid, _ := uuid.Parse(chi.URLParam(r, "id"))
	sid, _ := uuid.Parse(chi.URLParam(r, "sid"))
	n, err := h.q.RevokeOneWebSession(r.Context(), repo.RevokeOneWebSessionParams{
		ID: sid, UserID: uid,
	})
	if err != nil {
		problem.Write(w, 500, "internal", "")
		return
	}
	if n == 0 {
		problem.Write(w, 404, "no-such-session", "")
		return
	}
	w.WriteHeader(204)
}
```

### 2.6 Routing (`internal/auth/user/routes.go`)

```go
func (h *Handler) Mount(r chi.Router) {
	r.Route("/api/users", func(r chi.Router) {
		r.Use(h.mw.AdminOnly)                       // 10.13
		r.Post("/", h.CreateUser)
		r.Patch("/{id}", h.PatchUser)
		r.Delete("/{id}", h.DeleteUser)
		r.Post("/{id}/unlock", h.UnlockUser)
		r.Delete("/{id}/sessions/{sid}", h.RevokeSession)
	})
}
```

### 2.7 CLI — `cmd/maktaba-api/adduser.go` (D7)

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"maktaba/apps/api/internal/auth/password"
	"maktaba/apps/api/internal/auth/user/repo"
)

func newAddUserCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "adduser <username>",
		Short: "Create a bootstrap admin user (no echo password prompt)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			username := strings.TrimSpace(args[0])
			pool, params, err := bootDeps(cmd.Context())
			if err != nil {
				return err
			}
			defer pool.Close()
			q := repo.New(pool)

			if !force {
				n, err := q.CountUsers(cmd.Context())
				if err != nil {
					return err
				}
				if n > 0 {
					return errors.New("users already exist; pass --force to add another via CLI")
				}
			}

			fmt.Fprint(os.Stderr, "Password: ")
			pw1, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if err != nil {
				return err
			}
			fmt.Fprint(os.Stderr, "Confirm:  ")
			pw2, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if err != nil {
				return err
			}
			if string(pw1) != string(pw2) {
				return errors.New("passwords did not match")
			}
			hash, err := password.Hash(string(pw1), params)
			if err != nil {
				return err
			}
			_, err = q.InsertUser(context.Background(), repo.InsertUserParams{
				ID: uuid.Must(uuid.NewV7()), Username: username,
				PwHash: hash, IsAdmin: true,
			})
			return err
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "allow even when users already exist")
	return cmd
}
```

---

## 3. File-by-file scaffolding

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `shared/db/migrations/0040_users.sql` | `users` table, `users_username_lower_unique`, `users_admin_active` index, sentinel INSERT | `TestMigrationCreatesUsers` |
| 2 | `apps/api/internal/auth/password/password.go` | `Params`, `DefaultParams`, `Hash`, `Verify`, `NeedsRehash`, `ErrPasswordTooLong`, `ErrInvalidCredentials` | `TestPasswordHashVerifyRoundtrip`, `TestPasswordTooLong`, `TestPasswordPHCFormat` |
| 3 | `apps/api/internal/auth/user/queries/users.sql` | sqlc inputs (8 queries) | (n/a — generated code) |
| 4 | `apps/api/internal/auth/user/repo.go` | `Queries.InsertUser`, `.GetUserBy*`, `.UpdateUser*`, `.DeleteUser`, `.UnlockUser`, `.CountActiveAdmins`, `.RevokeOneWebSession` | repo tests (testcontainers) |
| 5 | `apps/api/internal/auth/user/handler.go` | `Handler`, `CreateUser`, `PatchUser`, `DeleteUser`, `UnlockUser`, `RevokeSession`, `userView` | `TestCreateUser*`, `TestPatchUser*`, `TestDeleteUserLastAdmin` |
| 6 | `apps/api/internal/auth/user/routes.go` | `Handler.Mount` | wired in `cmd/maktaba-api/serve.go` |
| 7 | `cmd/maktaba-api/adduser.go` | `newAddUserCmd` | `TestCLIAddUserNoEchoPrompt` |
| 8 | `cmd/maktaba-api/main.go` (extend) | register `newAddUserCmd()` on root | smoke build |

---

## 4. Test cases — keyed to ACs

```go
// AC-1: hash creation produces a PHC string with the configured params.
func TestPasswordPHCFormat(t *testing.T) {
	h, err := password.Hash("hunter2", password.DefaultParams())
	if err != nil { t.Fatal(err) }
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=2,p=1$") {
		t.Fatalf("phc prefix wrong: %s", h)
	}
}

// AC-1: same plaintext → different hash (random salt).
func TestPasswordHashesDifferPerInvocation(t *testing.T) {
	a, _ := password.Hash("same", password.DefaultParams())
	b, _ := password.Hash("same", password.DefaultParams())
	if a == b { t.Fatal("expected different salts") }
}

// AC-2: constant-time verify accepts the correct password and rejects wrong ones.
func TestPasswordHashVerifyRoundtrip(t *testing.T) {
	h, _ := password.Hash("correct horse battery staple", password.DefaultParams())
	if err := password.Verify("correct horse battery staple", h); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := password.Verify("wrong", h); !errors.Is(err, password.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

// D5: 1024-char password is rejected with ErrPasswordTooLong, no DoS.
func TestPasswordTooLong(t *testing.T) {
	long := strings.Repeat("a", 1024)
	_, err := password.Hash(long, password.DefaultParams())
	if !errors.Is(err, password.ErrPasswordTooLong) {
		t.Fatalf("want ErrPasswordTooLong, got %v", err)
	}
}

// AC-3: POST /api/users returns no pw_hash; conflict → 409 username-exists.
func TestCreateUserExcludesPwHashAndDetectsConflict(t *testing.T) {
	srv := newTestServer(t)
	resp := srv.do(t, "POST", "/api/users",
		`{"username":"Alice","password":"hunter2","is_admin":false}`, srv.AdminAuth)
	require.Equal(t, 201, resp.StatusCode)
	require.NotContains(t, resp.Body, "pw_hash")
	resp2 := srv.do(t, "POST", "/api/users",
		`{"username":"alice","password":"x","is_admin":false}`, srv.AdminAuth)
	require.Equal(t, 409, resp2.StatusCode)
	require.Contains(t, resp2.Body, `"type":"username-exists"`)
}

// D6: deleting the last admin → 409 last-admin.
func TestDeleteUserLastAdmin(t *testing.T) {
	srv := newTestServer(t)
	// setup: only one admin (the bootstrap one).
	resp := srv.do(t, "DELETE", "/api/users/"+srv.AdminID.String(), "", srv.AdminAuth)
	require.Equal(t, 409, resp.StatusCode)
	require.Contains(t, resp.Body, `"type":"last-admin"`)
}

// AC-3: unlock clears failed_attempts and locked_until.
func TestUnlockUser(t *testing.T) {
	srv := newTestServer(t)
	uid := srv.seedLockedUser(t)
	resp := srv.do(t, "POST", "/api/users/"+uid.String()+"/unlock", "", srv.AdminAuth)
	require.Equal(t, 204, resp.StatusCode)
	row := srv.queryUser(t, uid)
	require.Equal(t, 0, row.FailedAttempts)
	require.Nil(t, row.LockedUntil)
}

// AC-3: revoke a single web session leaves siblings untouched.
func TestRevokeOneSession(t *testing.T) {
	srv := newTestServer(t)
	uid := srv.seedUser(t, "viewer", false)
	s1 := srv.seedWebSession(t, uid)
	s2 := srv.seedWebSession(t, uid)
	resp := srv.do(t, "DELETE",
		fmt.Sprintf("/api/users/%s/sessions/%s", uid, s1), "", srv.AdminAuth)
	require.Equal(t, 204, resp.StatusCode)
	require.True(t, srv.isSessionRevoked(t, s1))
	require.False(t, srv.isSessionRevoked(t, s2))
}

// AC-4: CLI bootstrap produces an admin user with a hash that verifies.
func TestCLIAddUserCreatesAdmin(t *testing.T) {
	pool := newTestPool(t)
	cmd := newAddUserCmd()
	cmd.SetIn(strings.NewReader("hunter2\nhunter2\n")) // no echo helper for tests
	cmd.SetArgs([]string{"alice"})
	require.NoError(t, cmd.ExecuteContext(testCtx(pool)))
	row, err := pool.Repo().GetUserByLowerUsername(context.Background(), "alice")
	require.NoError(t, err)
	require.True(t, row.IsAdmin)
	require.NoError(t, password.Verify("hunter2", row.PwHash))
}
```

---

## 5. Edge cases and how the plan handles each

| #   | Edge case | Handled by |
|-----|-----------|------------|
| E1  | **Username conflict only differs in case** ("Alice" vs "ALICE"). | `users_username_lower_unique` (D3). Test `TestCreateUserExcludesPwHashAndDetectsConflict`. |
| E2  | **User attempts self-promotion via PATCH `is_admin=true`**. | Self-promotion guard in `PatchUser` returns 403 `self-promote`. Tested in `TestPatchUserSelfPromote`. |
| E3  | **Demote / delete the only admin races with another demote**. | `CountActiveAdmins` runs `FOR UPDATE` inside the same tx as the destructive update (D6). Tested in `TestLastAdminConcurrencyRace`. |
| E4  | **Empty username** (`""` or whitespace). | `strings.TrimSpace` + 422 `username-required`. |
| E5  | **Password longer than `password_max_bytes`**. | D5: `ErrPasswordTooLong` → 422 `password-too-long`. Tested in `TestPasswordTooLong`. |
| E6  | **CLI re-run with users already present**. | `--force` required; otherwise `errors.New("users already exist")`. Tested in `TestCLIAddUserRefusesWhenUsersExist`. |
| E7  | **Sentinel admin row missing** (manual DB tampering). | Migration uses `ON CONFLICT (id) DO NOTHING`; rerun is safe. The single-user-mode boot (10.9) re-asserts the row at startup. |
| E8  | **PHC string corrupted in DB**. | `password.Verify` returns `ErrInvalidCredentials` (not a leak); ops alert via login-failure metric. `NeedsRehash` returns the decode error. |
| E9  | **Caller tries to revoke a session belonging to a different user**. | `RevokeOneWebSession` filters by `(id=$1 AND user_id=$2)`; mismatched pair → 0 rows → 404 `no-such-session`. |
| E10 | **PATCH with empty body**. | All fields are `*T` pointers; nil → no-op; commit returns 204. |
| E11 | **Username changed to one that already exists (case-insensitive)**. | UNIQUE-violation on UPDATE → mapped to 409 `username-exists` like POST. |
| E12 | **Password contains a NUL byte**. | argon2id accepts arbitrary bytes; `len()` is byte length so D5's cap still applies. No special handling needed. |

---

## 6. Acceptance checklist

- [ ] **A1** `shared/db/migrations/0040_users.sql` creates the `users` table with `(id, username, pw_hash NOT NULL, is_admin, created_at, failed_attempts, locked_until)` and `UNIQUE (lower(username))`. (`TestMigrationCreatesUsers`)
- [ ] **A2** Migration seeds the sentinel admin row `00000000-0000-0000-0000-000000000001` with `pw_hash='<unsalted-disabled>'`, idempotent under re-run. (`TestMigrationSeedsSentinel`)
- [ ] **A3** `password.Hash` returns a canonical PHC string with `m=65536, t=2, p=1, salt=16, hash=32`. (`TestPasswordPHCFormat`)
- [ ] **A4** Two calls to `password.Hash` for the same plaintext return distinct PHC strings (random salt). (`TestPasswordHashesDifferPerInvocation`)
- [ ] **A5** `password.Verify` returns nil for the correct plaintext, `ErrInvalidCredentials` for the wrong one, and never logs the plaintext or hash. (`TestPasswordHashVerifyRoundtrip`, log-output assertion)
- [ ] **A6** Passwords longer than `password_max_bytes` (default 256) return `ErrPasswordTooLong`; the handler maps to 422 `password-too-long` without invoking argon2. (`TestPasswordTooLong`, perf assertion ≤ 1 ms for the 1024-byte input)
- [ ] **A7** `POST /api/users` requires admin, hashes the password, inserts the row, and the response body has no `pw_hash` field. (`TestCreateUserExcludesPwHashAndDetectsConflict`)
- [ ] **A8** Username conflicts (case-insensitive) on POST or PATCH return 409 `username-exists`. (`TestCreateUserExcludesPwHashAndDetectsConflict`, `TestPatchUserConflict`)
- [ ] **A9** `PATCH /api/users/{id}` with `is_admin=false` against the only admin returns 409 `last-admin`; with `is_admin=true` from a non-admin self returns 403 `self-promote`. (`TestPatchUserDemoteLastAdmin`, `TestPatchUserSelfPromote`)
- [ ] **A10** `DELETE /api/users/{id}` against the only admin returns 409 `last-admin`; otherwise 204 and FK-cascades to `playback_state`, `saved_searches`, `web_sessions`, `refresh_tokens`. (`TestDeleteUserLastAdmin`, `TestDeleteUserCascades`)
- [ ] **A11** `POST /api/users/{id}/unlock` clears `failed_attempts=0, locked_until=NULL`. (`TestUnlockUser`)
- [ ] **A12** `DELETE /api/users/{id}/sessions/{sid}` revokes exactly one row from `web_sessions`; mismatched user/session pair → 404. (`TestRevokeOneSession`)
- [ ] **A13** `maktaba-api adduser <username>` prompts for a password without echo, hashes via the same `password.Hash` as the API, and inserts an `is_admin=true` row. Refuses to run when users already exist unless `--force` is given. (`TestCLIAddUserCreatesAdmin`, `TestCLIAddUserRefusesWhenUsersExist`)
- [ ] **A14** All `pw_hash` writes go through `password.Hash`; no code path stores a raw or non-PHC string in the column. (Static check + lint rule on `pw_hash` in `internal/auth/user`.)
- [ ] **A15** `password_max_bytes` is configurable via `[auth] password_max_bytes` in `api.toml` and threads through to `Params.MaxBytes`. (Smoke test on config loading.)
