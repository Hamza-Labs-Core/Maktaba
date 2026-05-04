# Implementation Plan — Story 10.1 User store + argon2id passwords

> Companion to [story-10-01-user-store.md](story-10-01-user-store.md).
> The story states *what* and *why*; this plan states *how*.
> Schema follows [architecture.md §8.5](../../architecture.md) and the
> additional columns spelled out in [README.md](README.md). Argon2id
> tunables come from `[auth]` in [architecture.md §11.2](../../architecture.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Migration file | `shared/db/migrations/0020_users.sql` (Postgres) and `0020_users.sqlite.sql` (SQLite). Numbering jumps from the pipeline-side range (00xx) to leave `0010..0019` to Epic 6 and reserve a clean prefix for Epic 10 schema. |
| Hasher package | `api/internal/auth/argon2.go` — pure-Go wrapper around `golang.org/x/crypto/argon2`, encoding/decoding the standard PHC string `$argon2id$v=19$m=…,t=…,p=…$<salt>$<hash>`. |
| User store | `api/internal/auth/users.go` — typed methods on top of sqlc-generated queries in `api/internal/db/users.sql.go`. |
| HTTP handlers | `api/internal/http/users.go` — chi routes mounted under `/api/users`. |
| CLI | `api/cmd/api/adduser.go` — Cobra subcommand `maktaba-api adduser <username>` invoked by `main.go`'s root command. |
| Out of scope | Login/cookie/JWT (Stories 10.2–10.4); lockout counter writes (Story 10.11 owns the increment, this story owns the columns and the unlock endpoint). |

## 1. Architecture diagram

```
┌──────────────────┐    POST /api/users     ┌──────────────────────────┐
│ Admin web/native │ ─────────────────────► │ http/users.go            │
└──────────────────┘                        │  - authn middleware      │
                                            │  - is_admin guard        │
                                            │  - JSON decode + validate│
                                            └──────────┬───────────────┘
                                                       │ Create(ctx, params)
                                                       ▼
┌──────────────────┐                        ┌──────────────────────────┐
│ argon2.go        │ ◄──── HashPassword ─── │ auth/users.go            │
│  - PHC encode    │                        │  - case-fold uniqueness  │
│  - constant-time │       Verify  ────────►│  - last-admin guard      │
│    compare       │                        │  - tx wrap on cascades   │
└──────────────────┘                        └──────────┬───────────────┘
                                                       │ sqlc.Queries
                                                       ▼
                                            ┌──────────────────────────┐
                                            │ db/users.sql.go          │
                                            │  - InsertUser            │
                                            │  - GetUserByUsername     │
                                            │  - UpdateUser            │
                                            │  - DeleteUser            │
                                            │  - CountAdmins           │
                                            │  - UnlockUser            │
                                            │  - DeleteWebSession      │
                                            └──────────┬───────────────┘
                                                       │ pgx
                                                       ▼
                                            ┌──────────────────────────┐
                                            │ Postgres / SQLite        │
                                            │  users (UNIQUE lower)    │
                                            │  + ON DELETE CASCADE     │
                                            └──────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `shared/db/migrations/0020_users.sql` | Postgres table, sentinel insert, `lower()`-unique index, lockout columns. |
| `shared/db/migrations/0020_users.sqlite.sql` | SQLite variant (text PK, `COLLATE NOCASE` index). |
| `shared/db/queries/users.sql` | sqlc input — see §5. |
| `api/internal/auth/argon2.go` | `Hash`, `Verify`, `Params`, PHC encode/decode. |
| `api/internal/auth/users.go` | `Store` struct: `Create`, `GetByID`, `GetByUsername`, `Update`, `Delete`, `Unlock`, `RevokeWebSession`. |
| `api/internal/http/users.go` | chi handlers; problem+json error envelope. |
| `api/cmd/api/adduser.go` | `adduser` CLI: prompt, hash, insert. |
| `api/internal/auth/argon2_test.go` | Hash/verify unit tests. |
| `api/internal/auth/users_test.go` | Store-level table-driven tests. |
| `api/internal/http/users_test.go` | HTTP handler tests via `httptest.Server`. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/config/config.go` | Add `Auth.Argon2Memory`, `Auth.Argon2Time`, `Auth.Argon2Parallelism`, `Auth.PasswordMaxLen` with §11.2 defaults. |
| `api/cmd/api/main.go` | Register `adduser` subcommand; wire `Store` into router builder. |
| `api/internal/http/router.go` | Mount `/api/users/*` routes (admin-only). |
| `specs/epics/10-auth-security/README.md` | Tick story 10.1 once landed. |

### 2.3 Type definitions

```go
// api/internal/auth/argon2.go
package auth

type Argon2Params struct {
    Memory      uint32 // KiB
    Time        uint32 // iterations
    Parallelism uint8
    SaltLen     uint32 // bytes
    KeyLen      uint32 // bytes
}

// Defaults from architecture.md §11.2 — also encoded in the PHC string,
// so a future change to the params does not invalidate existing hashes.
var DefaultArgon2 = Argon2Params{
    Memory: 65536, Time: 2, Parallelism: 1, SaltLen: 16, KeyLen: 32,
}

const PasswordMaxLen = 256 // story AC; mitigates argon2 DoS at 1024 chars.
```

```go
// api/internal/auth/users.go
package auth

import (
    "context"
    "github.com/google/uuid"
    "time"
)

type User struct {
    ID             uuid.UUID
    Username       string
    IsAdmin        bool
    CreatedAt      time.Time
    FailedAttempts int32
    LockedUntil    *time.Time
    // pw_hash deliberately not exposed on this struct.
}

type CreateParams struct {
    Username string
    Password string
    IsAdmin  bool
}

type UpdateParams struct {
    ID       uuid.UUID
    Username *string
    Password *string // nil = unchanged
    IsAdmin  *bool
}

type Store interface {
    Create(ctx context.Context, p CreateParams) (User, error)
    GetByID(ctx context.Context, id uuid.UUID) (User, error)
    GetByUsername(ctx context.Context, username string) (User, string, error) // returns hash for verify
    Update(ctx context.Context, p UpdateParams) (User, error)
    Delete(ctx context.Context, id uuid.UUID) error
    Unlock(ctx context.Context, id uuid.UUID) error
    RevokeWebSession(ctx context.Context, userID, sessionID uuid.UUID) error
}
```

### 2.4 Function signatures

```go
// api/internal/auth/argon2.go
func Hash(password string, p Argon2Params) (string, error)             // returns PHC
func Verify(password, encoded string) (ok bool, needsRehash bool, err error)
func parsePHC(encoded string) (Argon2Params, []byte /*salt*/, []byte /*key*/, error)
```

`needsRehash` returns true when the stored hash was produced with weaker
params than the current `DefaultArgon2`; `auth.Verify` is used by the
login flow (Story 10.2) to opportunistically upgrade hashes on
successful login.

## 3. Database migration — Postgres

`shared/db/migrations/0020_users.sql`:

```sql
-- +goose Up
-- +goose StatementBegin

-- Story 10.1 owns the columns added on top of architecture.md §8.5:
--   - failed_attempts / locked_until (Story 10.11 brute-force protection)
--   - case-insensitive uniqueness via a lower() functional index
--   - sentinel admin row for single-user mode (Story 10.9)

CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username        TEXT NOT NULL,
    pw_hash         TEXT NOT NULL,
    is_admin        BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    failed_attempts INTEGER NOT NULL DEFAULT 0,
    locked_until    TIMESTAMPTZ,
    CONSTRAINT users_failed_chk CHECK (failed_attempts >= 0)
);

-- Case-insensitive uniqueness: display preserves casing, lookup folds it.
CREATE UNIQUE INDEX users_username_lower_unique
    ON users (lower(username));

-- Helper for the single-user-mode bypass: the sentinel row exists so that
-- audit_log.actor_user_id and any FK that points at users(id) resolves
-- when the admin token path is taken (Story 10.9 AC-1).
INSERT INTO users (id, username, pw_hash, is_admin)
VALUES (
    '00000000-0000-0000-0000-000000000001',
    'admin',
    '$argon2id$disabled$bootstrap-sentinel',  -- never matches any password
    true
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS users;
-- +goose StatementEnd
```

The sentinel `pw_hash` deliberately uses a non-PHC magic string. The
verifier rejects any value that doesn't begin with `$argon2id$v=`; the
sentinel can never authenticate via the password path even if someone
pastes the literal string into a login form.

### 3.1 Migration — SQLite variant

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE users (
    id              TEXT PRIMARY KEY,                -- UUID as text
    username        TEXT NOT NULL,
    pw_hash         TEXT NOT NULL,
    is_admin        INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT (CURRENT_TIMESTAMP),
    failed_attempts INTEGER NOT NULL DEFAULT 0,
    locked_until    TEXT,
    CHECK (failed_attempts >= 0)
);

-- SQLite lacks a Unicode-aware lower(); use COLLATE NOCASE (ASCII fold).
-- Application-side casefold (Go strings.ToLower with Unicode tables)
-- still runs before INSERT to catch non-ASCII collisions.
CREATE UNIQUE INDEX users_username_lower_unique
    ON users (username COLLATE NOCASE);

INSERT INTO users (id, username, pw_hash, is_admin)
VALUES (
    '00000000-0000-0000-0000-000000000001',
    'admin',
    '$argon2id$disabled$bootstrap-sentinel',
    1
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS users;
-- +goose StatementEnd
```

The Go layer applies `strings.ToLower` (which uses Unicode case folding
for `unicode.SimpleFold` letters) before inserting and before the
GetByUsername lookup, so the SQLite ASCII collation is defense in
depth — not the only check.

## 4. Argon2id implementation

```go
// api/internal/auth/argon2.go
package auth

import (
    "crypto/rand"
    "crypto/subtle"
    "encoding/base64"
    "errors"
    "fmt"
    "strings"

    "golang.org/x/crypto/argon2"
)

var (
    ErrInvalidHash         = errors.New("auth: invalid PHC encoding")
    ErrIncompatibleVersion = errors.New("auth: argon2 version mismatch")
    ErrPasswordTooLong     = errors.New("auth: password exceeds max length")
)

func Hash(password string, p Argon2Params) (string, error) {
    if len(password) > PasswordMaxLen {
        return "", ErrPasswordTooLong
    }
    salt := make([]byte, p.SaltLen)
    if _, err := rand.Read(salt); err != nil {
        return "", err
    }
    key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Parallelism, p.KeyLen)
    b64salt := base64.RawStdEncoding.EncodeToString(salt)
    b64key  := base64.RawStdEncoding.EncodeToString(key)
    return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
        argon2.Version, p.Memory, p.Time, p.Parallelism, b64salt, b64key), nil
}

func Verify(password, encoded string) (ok bool, needsRehash bool, err error) {
    if len(password) > PasswordMaxLen {
        return false, false, ErrPasswordTooLong
    }
    p, salt, want, err := parsePHC(encoded)
    if err != nil {
        return false, false, err
    }
    got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Parallelism, uint32(len(want)))
    // Constant-time compare; ConstantTimeCompare returns 1 on match.
    if subtle.ConstantTimeCompare(got, want) != 1 {
        return false, false, nil
    }
    needsRehash = p.Memory < DefaultArgon2.Memory ||
                  p.Time < DefaultArgon2.Time ||
                  p.Parallelism < DefaultArgon2.Parallelism
    return true, needsRehash, nil
}

func parsePHC(encoded string) (Argon2Params, []byte, []byte, error) {
    // Format: $argon2id$v=19$m=65536,t=2,p=1$<salt-b64>$<key-b64>
    parts := strings.Split(encoded, "$")
    if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
        return Argon2Params{}, nil, nil, ErrInvalidHash
    }
    var version int
    if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
        return Argon2Params{}, nil, nil, ErrInvalidHash
    }
    if version != argon2.Version {
        return Argon2Params{}, nil, nil, ErrIncompatibleVersion
    }
    var p Argon2Params
    if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d",
        &p.Memory, &p.Time, &p.Parallelism); err != nil {
        return Argon2Params{}, nil, nil, ErrInvalidHash
    }
    salt, err := base64.RawStdEncoding.DecodeString(parts[4])
    if err != nil {
        return Argon2Params{}, nil, nil, ErrInvalidHash
    }
    key, err := base64.RawStdEncoding.DecodeString(parts[5])
    if err != nil {
        return Argon2Params{}, nil, nil, ErrInvalidHash
    }
    p.SaltLen = uint32(len(salt))
    p.KeyLen  = uint32(len(key))
    return p, salt, key, nil
}
```

`subtle.ConstantTimeCompare` is required by AC-2; do not collapse the
verify into a `bytes.Equal`.

## 5. sqlc queries

`shared/db/queries/users.sql`:

```sql
-- name: InsertUser :one
INSERT INTO users (id, username, pw_hash, is_admin)
VALUES ($1, $2, $3, $4)
RETURNING id, username, is_admin, created_at, failed_attempts, locked_until;

-- name: GetUserByID :one
SELECT id, username, pw_hash, is_admin, created_at, failed_attempts, locked_until
FROM users WHERE id = $1;

-- name: GetUserByUsername :one
SELECT id, username, pw_hash, is_admin, created_at, failed_attempts, locked_until
FROM users WHERE lower(username) = lower($1);

-- name: UpdateUserUsername :exec
UPDATE users SET username = $2 WHERE id = $1;

-- name: UpdateUserPassword :exec
UPDATE users SET pw_hash = $2 WHERE id = $1;

-- name: UpdateUserIsAdmin :exec
UPDATE users SET is_admin = $2 WHERE id = $1;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = $1;

-- name: CountAdmins :one
SELECT count(*) FROM users WHERE is_admin = true;

-- name: UnlockUser :exec
UPDATE users SET failed_attempts = 0, locked_until = NULL WHERE id = $1;

-- name: DeleteWebSessionForUser :exec
UPDATE web_sessions SET revoked_at = now()
WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL;
```

The `web_sessions` table is created in Story 10.2; the sqlc query above
is generated lazily — it lives in this file for cohesion but compiles
only after 0021 lands. The compiler (sqlc) resolves both migrations
together at codegen time, so this is fine in CI.

## 6. HTTP handlers

`api/internal/http/users.go`:

```go
package http

import (
    "encoding/json"
    "errors"
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"

    "maktaba/api/internal/auth"
)

type userResponse struct {
    ID        uuid.UUID  `json:"id"`
    Username  string     `json:"username"`
    IsAdmin   bool       `json:"is_admin"`
    CreatedAt string     `json:"created_at"`
    Locked    *string    `json:"locked_until,omitempty"`
}

func MountUsers(r chi.Router, s auth.Store) {
    // All routes require an admin caller; the requireAdmin middleware
    // is provided by Story 10.13's permission-model package and gates
    // every method below.
    r.Route("/users", func(r chi.Router) {
        r.Use(requireAdmin)
        r.Post("/", createUser(s))
        r.Get("/{id}", getUser(s))
        r.Patch("/{id}", patchUser(s))
        r.Delete("/{id}", deleteUser(s))
        r.Post("/{id}/unlock", unlockUser(s))
        r.Delete("/{id}/sessions/{session_id}", revokeSession(s))
    })
}

func createUser(s auth.Store) http.HandlerFunc {
    type req struct {
        Username string `json:"username"`
        Password string `json:"password"`
        IsAdmin  bool   `json:"is_admin"`
    }
    return func(w http.ResponseWriter, r *http.Request) {
        var body req
        if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
            problem(w, http.StatusBadRequest, "invalid-json", err.Error())
            return
        }
        if len(body.Password) > auth.PasswordMaxLen {
            problem(w, http.StatusUnprocessableEntity, "password-too-long",
                "password exceeds 256 chars")
            return
        }
        u, err := s.Create(r.Context(), auth.CreateParams{
            Username: body.Username, Password: body.Password, IsAdmin: body.IsAdmin,
        })
        if err != nil {
            switch {
            case errors.Is(err, auth.ErrUsernameTaken):
                problem(w, http.StatusConflict, "username-exists", "")
            case errors.Is(err, auth.ErrPasswordTooLong):
                problem(w, http.StatusUnprocessableEntity, "password-too-long", "")
            default:
                problem(w, http.StatusInternalServerError, "internal", "")
            }
            return
        }
        writeJSON(w, http.StatusCreated, toUserResponse(u))
    }
}
```

`patchUser` rejects `is_admin` self-promotion (`ctx user.ID == path id
&& body.IsAdmin != nil`) with 403 `type: self-promote-forbidden`.
`deleteUser` runs in a transaction:

```go
func (s *store) Delete(ctx context.Context, id uuid.UUID) error {
    return s.db.WithTx(ctx, func(q *db.Queries) error {
        n, _ := q.CountAdmins(ctx)
        target, _ := q.GetUserByID(ctx, id)
        if target.IsAdmin && n <= 1 {
            return ErrLastAdmin
        }
        // CASCADE on playback_state, saved_searches, web_sessions, refresh_tokens
        // is enforced by the FK definitions in their owning migrations.
        return q.DeleteUser(ctx, id)
    })
}
```

## 7. CLI bootstrap

`api/cmd/api/adduser.go`:

```go
package main

import (
    "context"
    "fmt"

    "github.com/spf13/cobra"
    "golang.org/x/term"

    "maktaba/api/internal/auth"
)

func newAddUserCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "adduser <username>",
        Short: "Create the first admin user (no-echo password prompt)",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            store, _ := bootStore(cmd.Context())
            fmt.Print("Password: ")
            pw1, err := term.ReadPassword(0)
            fmt.Println()
            if err != nil { return err }
            fmt.Print("Confirm:  ")
            pw2, err := term.ReadPassword(0)
            fmt.Println()
            if err != nil { return err }
            if string(pw1) != string(pw2) {
                return fmt.Errorf("passwords do not match")
            }
            _, err = store.Create(cmd.Context(), auth.CreateParams{
                Username: args[0],
                Password: string(pw1),
                IsAdmin:  true,
            })
            if err != nil { return err }
            fmt.Printf("Created admin user %q\n", args[0])
            return nil
        },
    }
}
```

The CLI never reads the password from argv (which would surface in
`ps`), never echoes, and zeroes the buffer after Hash returns by relying
on `golang.org/x/term`'s no-echo path.

## 8. Test plan

### 8.1 Argon2id (`argon2_test.go`)

| Test | What it pins |
|---|---|
| `TestHashRoundTrip` | `Hash(p)` then `Verify(p, encoded)` returns `(true, false, nil)`; `Verify("wrong", encoded)` returns `(false, false, nil)`. |
| `TestHashSaltRandomness` | `Hash("p", default)` twice produces two distinct strings (salt differs). |
| `TestHashEncodesParams` | The PHC string contains the exact `m=65536,t=2,p=1` for the default params; an upgraded run with `m=131072` produces a string parseable back to those params. |
| `TestVerifyNeedsRehash` | A hash produced with `Memory=8192, Time=1` verifies but reports `needsRehash=true` against `DefaultArgon2`. |
| `TestVerifyConstantTime` | Verify two passwords that differ only in the last byte; assert no measurable timing difference (sample 1000 runs each, two-sample t-test, p > 0.05). |
| `TestPasswordTooLongRejected` | A 1024-char password to `Hash` and `Verify` both return `ErrPasswordTooLong`; no allocation of the 1024-char argon2 input. |
| `TestParsePHCRejectsBadFormat` | Inputs `""`, `"foo"`, `"$argon2i$..."` (wrong variant), `"$argon2id$v=99$..."` (wrong version) → `ErrInvalidHash` or `ErrIncompatibleVersion`. |
| `TestVerifySentinelRejected` | The literal `"$argon2id$disabled$bootstrap-sentinel"` (the seeded sentinel) → `ErrInvalidHash`, never `ok=true`. |

### 8.2 Store (`users_test.go`)

| Test | What it pins |
|---|---|
| `TestCreateInsertsRowAndHashesPassword` | Create returns `User` with no `pw_hash`; DB row has a `$argon2id$v=…` string starting prefix. |
| `TestCreateUsernameCollisionCaseFolded` | Insert `Alice`; Create `alice` → `ErrUsernameTaken` (409). |
| `TestCreateUnicodeCaseFold` | Insert `Müller`; Create `MÜLLER` → `ErrUsernameTaken` (Unicode `unicode.SimpleFold`). |
| `TestUpdatePasswordChangesHash` | Update with new password → `pw_hash` differs; Verify against the old password fails. |
| `TestSelfPromoteRejected` | Update by user X with `IsAdmin=true` against own ID → handler returns 403 `self-promote-forbidden`. |
| `TestDeleteLastAdminBlocked` | Delete last admin → `ErrLastAdmin` (409 `type: last-admin`). |
| `TestDeleteUserCascades` | After Create + insert into `playback_state`, `saved_searches`; Delete → those rows are gone (FK CASCADE). |
| `TestUnlockClearsCounter` | Set `failed_attempts=5, locked_until=now()+1h`; Unlock → both reset to 0/NULL. |
| `TestRevokeWebSessionFlipsRevokedAt` | Insert a `web_sessions` row; RevokeWebSession → `revoked_at` set; second call against same id is a no-op. |
| `TestRevokeWebSessionScopedToUser` | A session belonging to user A cannot be revoked via user B's id (returns 0 rows affected). |

### 8.3 HTTP (`users_test.go` in http package)

| Test | What it pins |
|---|---|
| `TestPostUserAsAdmin` | 201 with body excluding `pw_hash`. |
| `TestPostUserAsNonAdmin` | 403 `forbidden`. |
| `TestPostUserPasswordTooLong` | 422 `password-too-long`. |
| `TestPostUserDuplicateUsername` | 409 `username-exists`. |
| `TestDeleteLastAdminReturns409` | 409 `last-admin`. |
| `TestUnlockReturns204AndClears` | 204; subsequent login (Story 10.2) succeeds. |
| `TestDeleteSessionReturns204` | 204; the row's `revoked_at` is set. |

### 8.4 CLI

| Test | What it pins |
|---|---|
| `TestAddUserPromptsAndInserts` | Wire `term.ReadPassword` to a fake; assert one row inserted with `is_admin=true` and a hashed `pw_hash`. |
| `TestAddUserConfirmMismatchAborts` | Two different prompted values → exit non-zero, no DB write. |

## 9. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Username with leading/trailing whitespace | Trimmed via `strings.TrimSpace` in the handler before insert; `"  alice  "` and `"alice"` collide. | `TestCreateUsernameCollisionCaseFolded` |
| Unicode confusables (Cyrillic `а` vs Latin `a`) | NOT folded — `lower()` only does case folding, not script normalization. Operators with mixed alphabets accept the surface; v2 may add NFKC. Documented in operations. | n/a |
| Password exactly 256 chars | Accepted. 257+ → 422. The boundary is `> PasswordMaxLen`, not `>=`. | `TestPostUserPasswordTooLong` |
| Empty password | Allowed by argon2 itself, but rejected at the handler with 422 `type: password-empty` (defense in depth — the UX layer should never POST empty). | `TestPostUserEmptyPasswordRejected` |
| Sentinel UUID write attempts | The migration inserts the sentinel; a subsequent INSERT with that exact UUID raises a PK violation. The handler maps PK violations to 409. | Migration test |
| Concurrent CreateUser races | Unique index on `lower(username)` makes one INSERT win; the loser maps `pgerrcode.UniqueViolation` to `ErrUsernameTaken`. | `TestCreateUsernameRaceProducesOne409` |
| Delete with active sessions | CASCADE wipes `web_sessions` and `refresh_tokens` rows; in-flight access JWTs continue to verify until `exp` (Story 10.5 AC-2 trade-off). | `TestDeleteUserCascades` |
| Sentinel admin counted in `CountAdmins` | The sentinel is `is_admin=true` and counts toward "last admin" — deleting all real admin rows still leaves the sentinel, so `CountAdmins() >= 1` always. The handler additionally refuses to delete the sentinel UUID outright (`type: sentinel-locked`). | `TestDeleteSentinelRejected` |
| Username `'admin'` clashes with sentinel | The sentinel is pre-seeded with username `admin`, so `Create("admin", ...)` collides and returns 409. The CLI's `adduser admin` is therefore a no-op on a fresh install; operators are guided to pick a different name. | Migration + create test |

## 10. Dependencies

| Dep | Version | Why |
|---|---|---|
| `golang.org/x/crypto/argon2` | latest | Pure-Go argon2id; no cgo. |
| `golang.org/x/term` | latest | No-echo password prompt for `adduser`. |
| `github.com/spf13/cobra` | already in repo | CLI subcommand wiring. |
| `github.com/google/uuid` | already | UUID parsing/generation. |
| `github.com/jackc/pgx/v5` | already | Postgres driver. |
| `sqlc` (dev-only) | already | Codegen. |

## 11. Acceptance checklist

**Migration**
- [ ] `0020_users.sql` applies cleanly; sentinel row exists with the documented UUID.
- [ ] `users_username_lower_unique` is enforced (case-insensitive duplicate INSERT raises).
- [ ] `failed_attempts` and `locked_until` columns exist with the documented defaults.

**Code**
- [ ] `auth.Hash` produces a parseable PHC string with the default params.
- [ ] `auth.Verify` uses `subtle.ConstantTimeCompare`.
- [ ] `Store.Create` rejects passwords > 256 chars with `ErrPasswordTooLong`.
- [ ] `Store.Delete` blocks deletion of the last real admin with `ErrLastAdmin`.
- [ ] `Store.Unlock` clears both columns atomically.

**HTTP**
- [ ] All `/api/users/*` routes require admin (`requireAdmin` middleware).
- [ ] Response bodies never include `pw_hash`.
- [ ] Self-promote (a user PATCHing their own `is_admin`) returns 403.

**CLI**
- [ ] `maktaba-api adduser <name>` prompts twice (no echo), inserts admin user.
- [ ] Mismatched confirmation aborts with non-zero exit and no DB write.

**Tests**
- [ ] All tests in §8 pass on Postgres and SQLite via the dialect-parametrized fixture.

**Docs**
- [ ] `specs/epics/10-auth-security/README.md` ticks story 10.1.
- [ ] CLI help (`maktaba-api adduser -h`) describes the bootstrap path.
