# Implementation Plan — Story 11.13 PAT Management API

> Companion to [story-11-13-pat-management-api.md](story-11-13-pat-management-api.md).
> New table `personal_access_tokens` + endpoints + verifier.
> Story 11.6 embeds the UI; this plan owns the backend + UI components.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Migration | `shared/db/migrations/0030_personal_access_tokens.sql` (Postgres) + `0030_personal_access_tokens.sqlite.sql`. |
| Model & repo | `api/internal/auth/pat/` (Go) — `model.go`, `repo.go`, `service.go`. |
| Verifier | `api/internal/auth/pat/verifier.go`; wired into the bearer middleware (Epic 10 Story 10.1) before the JWT verifier when token starts `mkt_pat_`. |
| Endpoints | `api/internal/http/me_pats.go`. Routes under `/api/me/pats` and `/api/users/{id}/pats` (architecture §9.7.1 canonical). |
| Token format | `mkt_pat_<32 base32 random chars>` (Crockford, no padding). 8-char prefix stored separately. |
| Hash | Argon2id (16 KiB memory, 1 iteration, 4 lanes — minimum to keep verify ≤ 50 ms). |
| Rate limit | 10/hour per user on `POST /api/me/pats` via Story 21 limiter. |
| Audit | `audit_log` writes for issuance, revoke, admin enumeration (`category = 'pat'`). |
| Web UI | Embedded into Story 11.6 Account section (`<TokensManager>`). |
| Out of scope | OAuth (separate story); machine-to-machine secret rotation. |

## 1. Schema

```sql
-- 0030_personal_access_tokens.sql
CREATE TABLE personal_access_tokens (
  id            UUID PRIMARY KEY,
  user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name          TEXT NOT NULL,
  prefix        TEXT NOT NULL,              -- first 8 chars of the token (printable)
  hash          BYTEA NOT NULL,             -- argon2id(token)
  scopes        TEXT[] NOT NULL DEFAULT '{}',
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_used_at  TIMESTAMPTZ,
  expires_at    TIMESTAMPTZ,
  revoked_at    TIMESTAMPTZ,
  UNIQUE (user_id, name)
);

CREATE INDEX pat_user_active_idx ON personal_access_tokens (user_id, revoked_at);
CREATE INDEX pat_prefix_idx      ON personal_access_tokens (prefix);
```

SQLite analogue: `BYTEA` → `BLOB`, `TEXT[]` → JSON array stored as `TEXT` with a `CHECK (json_valid(scopes))`.

## 2. Token format

```
prefix  ::= 8 chars (e.g., "5T7K9X2A")
random  ::= 32 chars  (Crockford base32 of 20 random bytes)
plaintext ::= "mkt_pat_" || prefix || random
```

We store `prefix` for display. The full plaintext (after `mkt_pat_`) is hashed with Argon2id; we never store the plaintext.

## 3. Endpoints

### `POST /api/me/pats`

Request: `{ name, scopes?, expires_at? }`. Server defaults: `scopes = []`, `expires_at = now() + 1 year`.

Flow:
1. Rate-limit gate (10/hour per user) — 429 on exceed.
2. Generate plaintext + hash.
3. Insert row; if `(user_id, name)` collides → 409 `name-conflict`.
4. Audit log: `category='pat', action='create'`.
5. Return `201 { id, name, scopes, expires_at, prefix, token: <plaintext> }` once.

### `GET /api/me/pats`

Returns active + revoked-within-30-days. No `hash`, no plaintext.

```sql
SELECT id, name, prefix, scopes, created_at, last_used_at, expires_at, revoked_at
FROM personal_access_tokens
WHERE user_id = $1
  AND (revoked_at IS NULL OR revoked_at > now() - interval '30 days')
ORDER BY created_at DESC;
```

### `DELETE /api/me/pats/{id}`

Sets `revoked_at = now()`; idempotent (returns `204` even if already revoked); audit log.

### `GET /api/users/{id}/pats` (admin)

Lists any user's tokens; requires `admin` scope or `is_admin = true`. Audit log row written per call.

## 4. Verifier

```go
// pat/verifier.go
const Prefix = "mkt_pat_"

func (v *Verifier) Verify(ctx context.Context, token string) (*Identity, error) {
    if !strings.HasPrefix(token, Prefix) { return nil, ErrNotPat }
    raw := token[len(Prefix):]
    if len(raw) < 8 { return nil, ErrMalformed }
    prefix := raw[:8]
    rows, err := v.repo.FindByPrefix(ctx, prefix)
    if err != nil { return nil, err }

    for _, r := range rows {
        if argon2id.Verify([]byte(raw), r.Hash) {
            if r.RevokedAt != nil { return nil, ErrRevoked }
            if r.ExpiresAt != nil && r.ExpiresAt.Before(time.Now()) { return nil, ErrExpired }
            v.touchLastUsed(ctx, r.ID)              // debounced; ≤ 1/min/token
            return &Identity{ UserID: r.UserID, Scopes: r.Scopes, Source: "pat", PATID: r.ID }, nil
        }
    }
    return nil, ErrInvalid
}
```

Bearer middleware:

```go
if strings.HasPrefix(token, pat.Prefix) {
    ident, err := patVerifier.Verify(ctx, token)
    // map errors → 401 with problem+json `token-revoked|token-expired|invalid-token`
} else {
    ident, err := jwtVerifier.Verify(ctx, token)
}
```

`touchLastUsed` debounces with an in-process LRU keyed by token id; `last_used_at` written at most once per minute per token.

## 5. Security & redaction

- `mkt_pat_` strings are masked by the redaction filter from Epic 21 Story 21.1. Adding a regex `\bmkt_pat_[A-Z0-9]+\b` to the redaction list is the integration point.
- HSTS already enforced platform-wide; we add a header check on the issuance endpoint for paranoia.
- Scope enforcement helper:

```go
func RequireScope(s string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            ident := auth.IdentityFrom(r)
            if !ident.HasScope(s) { problem.Write(w, 403, "insufficient-scope"); return }
            next.ServeHTTP(w, r)
        })
    }
}
```

## 6. UI integration (Story 11.6)

`<TokensManager>` (in Settings → Account → Tokens):

```tsx
const tokens = useQuery(['me','pats'], fetchTokens);   // GET /api/me/pats
const create = useMutation(createToken, { onSuccess: (data) => setNewlyCreated(data) });

return (<>
  <Table data={tokens.data} columns={[name, prefix, scopes, created, lastUsed, expires, status]}/>
  <Button onClick={() => setOpen(true)}>{t('settings.tokens.new')}</Button>
  <TokenCreateDialog open={open} onSubmit={(form) => create.mutate(form)}/>
  {newlyCreated && <PlaintextOnceDialog token={newlyCreated.token} onClose={() => setNewlyCreated(null)}/>}
</>);
```

`<PlaintextOnceDialog>` includes copy-to-clipboard + an "I've saved it" gate before close.

## 7. Edge cases

| Case | Handling |
|---|---|
| Two PATs with same `prefix` | Verifier iterates all matching rows; picks the Argon2id-validating row. |
| Force-expire (admin sets `expires_at = now()`) | Next use → 401 `token-expired`. |
| User deleted | Cascade removes PATs in same transaction; no dangling refs. |
| `last_used_at > 90 days` PAT | Email reminder is Epic 22; out of v1 scope here. |
| Leaked PAT used from a different IP | Story 21.6 logs unusual IP; no auto-block in v1. |

## 8. Test cases

### 8.1 Unit (Go)

| Test | Asserts |
|---|---|
| `verify happy path` | Returns Identity; `last_used_at` updated. |
| `verify revoked → ErrRevoked` | Sets `revoked_at`; verify returns error mapping to 401 token-revoked. |
| `verify expired → ErrExpired` | Past `expires_at`; verify returns error. |
| `verify wrong hash` | Returns ErrInvalid; no Identity. |
| `last_used_at debounced` | 100 verifies in a minute → 1 DB write. |
| `rate limit 10/hour` | 11th create returns 429. |
| `name uniqueness per user` | Second create with same name → 409. |
| `cascade delete` | Deleting user removes PATs in same TX. |

### 8.2 Integration (HTTP)

| Test | Asserts |
|---|---|
| `POST /api/me/pats issues plaintext once` | First response has `token`; subsequent GETs lack it. |
| `Bearer mkt_pat_xxx on GET /api/videos` | 200 with `read` scope. |
| `Bearer mkt_pat_xxx on POST /api/libraries lacking admin` | 403 `insufficient-scope`. |
| `DELETE /api/me/pats/{id}` | 204; reuse → 401 `token-revoked`. |

### 8.3 Audit

| Test | Asserts |
|---|---|
| `create writes audit row` | `category='pat', action='create'`. |
| `admin enumerate writes audit row` | `category='pat', action='admin-list'`. |

## 9. Performance

- Argon2id verify ≤ 50 ms target on commodity hardware.
- `prefix` index keeps lookup ≤ 1 ms even at 1M rows.
- Debounced `last_used_at` keeps DB writes negligible.

## 10. Dependencies

- Epic 10 Story 10.1 (bearer middleware).
- Epic 21 Story 21.1 (redaction).
- REVIEW §1.1.f (canonical `audit_log`).
