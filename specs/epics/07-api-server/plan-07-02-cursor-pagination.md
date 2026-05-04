# Implementation Plan — Story 7.2 Cursor Pagination Primitive

> Companion to [story-07-02-cursor-pagination.md](story-07-02-cursor-pagination.md).
> Defines the single cursor format every list endpoint in Epic 7 must use.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Encoding | base64url (no padding) of a JSON `{u, i, v}` blob; `v=1`. Versioned so a v2 future can reject silently rather than mis-decode. |
| Sort key | Default `(updated_at DESC, id DESC)`. Endpoints that need `created_at` or `position` provide a sort-spec; the cursor primitive is parameterised. |
| Database | Postgres + SQLite parity. Same SQL works on both with `<` / `<=` predicates; `LIMIT n+1` decides whether `next` exists. |
| Out of scope | Per-endpoint sort registries (each later story registers its own); GraphQL `Connection`/`Edge` types (Story 7.17 wraps this). |

## 1. Architecture diagram

```
   GET /api/videos?limit=20&cursor=eyJ1Ijoi...
                       │
                       ▼
   ┌────────────────────────────────────────────────────────┐
   │ paginate.Decode(raw, &SortSpec{...})                   │
   │   → Cursor{UpdatedAt: 2026-05-04T10:00Z, ID: …, V: 1}  │
   │   → returns httperror.InvalidCursor on bad base64/JSON │
   │   → returns httperror.CursorUnsupportedVersion if v>1  │
   └────────────────┬───────────────────────────────────────┘
                    ▼
   ┌────────────────────────────────────────────────────────┐
   │ Handler builds query:                                  │
   │   WHERE (updated_at, id) < ($cur_u, $cur_i)            │
   │   ORDER BY updated_at DESC, id DESC                    │
   │   LIMIT $n + 1                                         │
   │ Slices off the (n+1)-th row to compute `next_cursor`. │
   └────────────────┬───────────────────────────────────────┘
                    ▼
   ┌────────────────────────────────────────────────────────┐
   │ paginate.Page{Items, NextCursor *string}               │
   │   serialised as { items: [...], next: "..." | null }   │
   └────────────────────────────────────────────────────────┘
```

## 2. New files

| Path | Purpose |
|---|---|
| `api/internal/paginate/cursor.go` | `Cursor`, `Encode`, `Decode`, `SortSpec`, `Page[T]`. |
| `api/internal/paginate/limit.go` | `ParseLimit(query, def, max)` returning `(int, *httperror.Error)`. |
| `api/internal/paginate/sql.go` | `BuildWhere(spec, cur)` generating the placeholder fragment + args. |
| `api/internal/paginate/cursor_test.go` | Unit tests per §6.1. |
| `api/internal/paginate/limit_test.go` | Unit tests per §6.2. |
| `api/internal/paginate/integration_test.go` | Stable-iteration test against a live DB. |

## 3. Go code scaffolding

### 3.1 Cursor

```go
// api/internal/paginate/cursor.go
package paginate

import (
    "encoding/base64"
    "encoding/json"
    "errors"
    "time"

    "github.com/google/uuid"

    "maktaba/api/internal/httperror"
)

const currentVersion = 1

// Cursor is the wire format. Field names are short to keep the base64 string
// well under 128 bytes (AC-1).
type Cursor struct {
    Updated time.Time `json:"u"`
    ID      uuid.UUID `json:"i"`
    Version int       `json:"v"`
}

// SortSpec describes the ordering of a list endpoint. Today every endpoint
// uses (TimeCol DESC, IDCol DESC); the spec exists so a future endpoint
// can swap to ASC or to a different secondary column without forking
// Encode/Decode.
type SortSpec struct {
    TimeCol string // e.g. "updated_at"
    IDCol   string // e.g. "id"
    Desc    bool   // true == DESC
}

func Encode(c Cursor) string {
    c.Version = currentVersion
    raw, _ := json.Marshal(c)
    return base64.RawURLEncoding.EncodeToString(raw)
}

func Decode(raw string) (Cursor, *httperror.Error) {
    if raw == "" {
        return Cursor{}, nil
    }
    bytes, err := base64.RawURLEncoding.DecodeString(raw)
    if err != nil {
        return Cursor{}, httperror.BadRequest("invalid cursor encoding").
            With(httperror.TypeInvalidCursor)
    }
    var c Cursor
    if err := json.Unmarshal(bytes, &c); err != nil {
        return Cursor{}, httperror.BadRequest("invalid cursor body").
            With(httperror.TypeInvalidCursor)
    }
    if c.Version > currentVersion {
        return Cursor{}, &httperror.Error{
            Type: httperror.TypeCursorUnsupported, Title: "unsupported cursor version",
            Status: 400, Detail: "this client cannot decode this cursor",
        }
    }
    if c.Version < 1 || c.ID == uuid.Nil {
        return Cursor{}, httperror.BadRequest("malformed cursor").
            With(httperror.TypeInvalidCursor)
    }
    return c, nil
}

var ErrEmptyPage = errors.New("paginate: empty page")
```

### 3.2 Page wrapper (generic)

```go
// api/internal/paginate/page.go
package paginate

type Page[T any] struct {
    Items []T     `json:"items"`
    Next  *string `json:"next"`
}

// Bound trims `items` to `limit` and computes `next` from the (limit+1)-th
// row, if it was returned.
type Cursorable interface {
    PageCursor() Cursor
}

func Bound[T Cursorable](items []T, limit int) Page[T] {
    if len(items) <= limit {
        return Page[T]{Items: items, Next: nil}
    }
    last := items[limit-1]
    enc := Encode(last.PageCursor())
    return Page[T]{Items: items[:limit], Next: &enc}
}
```

### 3.3 Limit parser

```go
// api/internal/paginate/limit.go
package paginate

import (
    "net/url"
    "strconv"

    "maktaba/api/internal/httperror"
)

const (
    DefaultLimit = 50
    MaxLimit     = 200
)

func ParseLimit(q url.Values) (int, *httperror.Error) {
    raw := q.Get("limit")
    if raw == "" {
        return DefaultLimit, nil
    }
    n, err := strconv.Atoi(raw)
    if err != nil {
        return 0, httperror.InvalidQuery("limit must be an integer")
    }
    if n < 1 || n > MaxLimit {
        return 0, httperror.InvalidQuery("limit must be in [1,200]")
    }
    return n, nil
}
```

### 3.4 SQL fragment builder

```go
// api/internal/paginate/sql.go
package paginate

import "fmt"

// Where returns "" or "(<time> <op> $n OR (<time> = $n AND <id> < $n+1))"
// plus the args slice. `args` is appended to whatever the caller has.
// op is "<" for DESC, ">" for ASC. Tuple comparisons with (a, b) < ($1, $2)
// would be cleaner, but Postgres requires parens and SQLite doesn't support
// row-value comparison consistently; the OR form works on both.
func Where(spec SortSpec, cur Cursor, nextPlaceholder int) (frag string, args []any) {
    if cur.ID == (uuid.UUID{}) {
        return "", nil
    }
    op := "<"
    if !spec.Desc {
        op = ">"
    }
    p1 := fmt.Sprintf("$%d", nextPlaceholder)
    p2 := fmt.Sprintf("$%d", nextPlaceholder+1)
    frag = fmt.Sprintf("(%s %s %s OR (%s = %s AND %s %s %s))",
        spec.TimeCol, op, p1, spec.TimeCol, p1, spec.IDCol, op, p2)
    args = []any{cur.Updated, cur.ID}
    return
}
```

The `nextPlaceholder` argument lets the caller append the cursor params
after their other filters without renumbering. SQLite call sites swap
`$n`-style placeholders for `?`-style via the project's existing
dialect-aware helper.

## 4. Example call site (used by Story 7.4)

```go
// In the videos handler:
limit, perr := paginate.ParseLimit(r.URL.Query())
if perr != nil { httperror.Write(w, r, perr); return }

cur, perr := paginate.Decode(r.URL.Query().Get("cursor"))
if perr != nil { httperror.Write(w, r, perr); return }

frag, curArgs := paginate.Where(spec, cur, len(args)+1)
sql := "SELECT id, updated_at, ... FROM videos WHERE 1=1"
if frag != "" {
    sql += " AND " + frag
    args = append(args, curArgs...)
}
sql += " ORDER BY updated_at DESC, id DESC LIMIT $" +
       strconv.Itoa(len(args)+1)
args = append(args, limit+1)

rows, err := db.Query(ctx, sql, args...)
// ...
page := paginate.Bound(items, limit)
```

## 5. Test plan

### 5.1 Unit — `cursor_test.go`

| Test | What it pins |
|---|---|
| `TestEncodeDecodeRoundTrip` | A `Cursor{u, id, 1}` round-trips byte-for-byte; final base64 length < 128 chars. |
| `TestDecodeRejectsBase64` | `"%%%"` → `InvalidCursor` 400. |
| `TestDecodeRejectsMalformedJSON` | base64 of `"not json"` → `InvalidCursor` 400. |
| `TestDecodeFutureVersion` | `Cursor{Version:2}` → `CursorUnsupported` 400. |
| `TestDecodeEmptyReturnsZero` | `Decode("")` returns `Cursor{}, nil`. |
| `TestEncodeShape` | `json.RawMessage(decoded).keys` is exactly `["u","i","v"]` (no padding fields like `_extra`). |

### 5.2 Unit — `limit_test.go`

| Test | What it pins |
|---|---|
| `TestLimitDefault` | Missing param → 50. |
| `TestLimitValid` | `?limit=20` → 20. |
| `TestLimitTooLow` | `?limit=0` → 400 "must be in [1,200]". |
| `TestLimitTooHigh` | `?limit=201` → 400. |
| `TestLimitNonNumeric` | `?limit=abc` → 400. |

### 5.3 Integration — `integration_test.go`

| Test | What it pins |
|---|---|
| `TestStableIterationOver1000Rows` | Seed 1000 videos, page through with `limit=50` → 20 pages, no duplicates (collected via set), no skips, last page's `next == nil`. |
| `TestNewInsertDoesNotAppearInResume` | Page once (page 1), insert 5 fresh videos, page 2 with the cursor → none of the 5 fresh ones appear. |
| `TestNewInsertAppearsInFreshList` | Same setup, but a fresh `?cursor=` (no cursor) → fresh ones appear at the top. |
| `TestTieBreakOnEqualUpdatedAt` | Insert two rows in one TX (identical `updated_at`) → page returns both, in `id DESC` order. |
| `TestDeletedRowSkipped` | Page once, delete the first row of page 2, page 2 → returns the rows that *would* have followed; no error. |

### 5.4 Property-based (`cursor_property_test.go`, optional but recommended)

| Test | What it pins |
|---|---|
| `TestRoundTripFuzz` | `quick.Check` over `(time, uuid)` pairs → `Decode(Encode(c)) == c` for any input. |

## 6. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Two rows share `updated_at` to the microsecond | Tie broken by `id DESC` (UUID v7 is monotonic in time, so this is also chronological). | `TestTieBreakOnEqualUpdatedAt` |
| Pointed-at row deleted between page fetches | `<` predicate skips it silently; the next page starts from the next row. | `TestDeletedRowSkipped` |
| Cursor `updated_at` is in the future (clock skew between writers) | The query returns no rows; `next` is `null`. | Integration test `TestFutureCursor` |
| `?limit=200&cursor=...` near the end of the dataset | The `LIMIT n+1` may return fewer than `n+1`; `Bound` correctly returns no `next`. | `TestStableIterationOver1000Rows` (last page) |
| Garbage cursor (`?cursor=garbage`) | 400 `invalid-cursor`, no leak of internal error. | `TestDecodeRejectsBase64` |
| Future v2 cursor in production | 400 `cursor-unsupported-version`; the client knows to retry without a cursor. | `TestDecodeFutureVersion` |
| Cursor reused across endpoints (e.g. videos cursor used on libraries) | The decode succeeds (it's just `(time, uuid)`), but the SQL query may return surprising rows. Acceptable: cursors are opaque to the client; the docs do not promise cross-endpoint validity. | Documented in API reference. |
| Sort spec changed in a deploy (e.g. `updated_at` → `last_event_at`) | Old cursors keep working temporarily because the column type matches; the new column will eventually invalidate them. Mitigated by the `v` field — a server-side bump rejects v1 cursors. | Documented in `cursor.go`. |
| Negative limit | 400 `invalid-query-parameter`. | `TestLimitTooLow` |

## 7. Dependencies

| Dep | Version | Why |
|---|---|---|
| `github.com/google/uuid` | already pinned | `uuid.UUID` JSON marshal. |
| `encoding/base64` | stdlib | RawURLEncoding (no padding). |

No new deps.

## 8. Acceptance checklist

**AC-1 — opaque cursor encoding**
- [ ] `Cursor` JSON keys are `u, i, v` (short).
- [ ] Encoded form is base64url, no padding.
- [ ] Encoded length < 128 bytes for all test cursors.

**AC-2 — stable iteration**
- [ ] `TestStableIterationOver1000Rows` passes.
- [ ] `TestNewInsertDoesNotAppearInResume` + `TestNewInsertAppearsInFreshList` both pass.

**AC-3 — limit and bounds**
- [ ] `ParseLimit` returns 50 default, accepts 1..200, rejects others with `invalid-query-parameter`.
- [ ] Invalid `limit` produces a 400 problem+json (not 500).

**Forward-compat**
- [ ] v2 cursor is rejected with `cursor-unsupported-version`.
- [ ] Bumping `currentVersion` is a one-line change with a unit-test guard.

**Docs**
- [ ] API reference documents the cursor format as opaque.
- [ ] `paginate.Where` is exported and used by every list handler in Stories 7.3+.
- [ ] `specs/epics/07-api-server/README.md` ticks story 7.2.
