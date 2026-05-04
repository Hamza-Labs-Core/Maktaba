# Implementation Plan — Story 7.9 Saved Searches

> Companion to [story-07-09-saved-searches.md](story-07-09-saved-searches.md).
> Trivial CRUD on top of Story 7.8. Reused by Smart Collections (Epic 9 Story 9.14).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Routes | `POST /api/search/save`, `GET /api/search/saved`, `DELETE /api/search/saved/{id}`. The DELETE is added even though the story doesn't list it explicitly — without it, a saved-search list grows unbounded. |
| Storage | New `saved_searches` table; FK to `users.id`. |
| Schema | Stores the full `Request` JSON (Story 7.8) verbatim; replay re-deserializes. |
| Forward-compat | Unknown filter keys ignored on replay (logged at debug), per AC. |
| Out of scope | The actual search execution (Story 7.8); the Smart Collection consumer (Epic 9). |

## 1. Architecture diagram

```
   POST /api/search/save  { name, query }
        │
        ▼
   ┌──────────────────────────────────────────────────────┐
   │ INSERT INTO saved_searches (id, user_id, name, query)│
   │ ON CONFLICT (user_id, name) DO NOTHING               │
   │   RETURNING id                                        │
   │ If no row returned → 409 saved-search-name-exists     │
   └──────────────────────────────────────────────────────┘

   GET /api/search/saved
        │
        ▼
   ┌──────────────────────────────────────────────────────┐
   │ SELECT * FROM saved_searches                         │
   │  WHERE user_id = $current                            │
   │  ORDER BY created_at DESC                            │
   └──────────────────────────────────────────────────────┘

   DELETE /api/search/saved/{id}
        │
        ▼
   ┌──────────────────────────────────────────────────────┐
   │ DELETE FROM saved_searches                           │
   │  WHERE id = $id AND user_id = $current               │
   │ rows == 0 → 404                                      │
   └──────────────────────────────────────────────────────┘
```

## 2. New files

| Path | Purpose |
|---|---|
| `api/internal/savedsearch/handler.go` | All three routes. |
| `api/internal/savedsearch/types.go` | DTOs. |
| `api/internal/savedsearch/handler_test.go` | Integration. |
| `shared/db/queries/saved_searches.sql` | sqlc inputs. |
| `shared/db/migrations/0015_saved_searches.sql` | Schema (Postgres + SQLite mirror). |

## 3. SQL — schema

The `saved_searches` table is canonical in architecture §8 with the
columns `(id, user_id, name, kind, query, created_at, updated_at)`.
Architecture does NOT enforce `(user_id, name)` uniqueness, but the
handler's create path requires it for "name already in use" semantics.
This migration is therefore an ADD-ONLY migration: it adds the
unique constraint and the user/created index. `kind` and `updated_at`
already exist canonically.

`shared/db/migrations/0015_saved_searches.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
-- saved_searches(table, id, user_id, name, kind, query, created_at,
-- updated_at) is created by the canonical schema migration. Here we add:
--  * the unique-per-user-name constraint the handler relies on
--  * the supporting index
ALTER TABLE saved_searches
  ADD CONSTRAINT saved_searches_user_name_uniq UNIQUE (user_id, name);

CREATE INDEX IF NOT EXISTS saved_searches_user_idx
    ON saved_searches (user_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS saved_searches_user_idx;
ALTER TABLE saved_searches DROP CONSTRAINT IF EXISTS saved_searches_user_name_uniq;
-- +goose StatementEnd
```

The SQLite mirror swaps `ADD CONSTRAINT` for an additional `CREATE UNIQUE
INDEX saved_searches_user_name_uniq ON saved_searches (user_id, name)`
(SQLite's `ALTER TABLE ADD CONSTRAINT` does not support UNIQUE).

## 4. Type definitions

```go
// api/internal/savedsearch/types.go
package savedsearch

import (
    "encoding/json"
    "time"
    "github.com/google/uuid"
)

type SavedSearch struct {
    ID        uuid.UUID       `json:"id"`
    Name      string          `json:"name"`
    Query     json.RawMessage `json:"query"`     // pass-through; replay client-side
    Kind      string          `json:"kind"`      // "user" | "smart_collection"
    CreatedAt time.Time       `json:"created_at"`
    UpdatedAt time.Time       `json:"updated_at"`
}

type CreateInput struct {
    Name  string          `json:"name"  validate:"required,min=1,max=128"`
    Query json.RawMessage `json:"query" validate:"required"`
}
```

## 5. Handler scaffolding

```go
// api/internal/savedsearch/handler.go
package savedsearch

import (
    "encoding/json"
    "errors"
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"

    "maktaba/api/internal/httperror"
)

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
    user := userFromCtx(r.Context())
    var in CreateInput
    if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
        httperror.Write(w, r, httperror.BadRequest("invalid json"))
        return
    }
    if err := validate(in); err != nil { httperror.Write(w, r, err); return }
    if !json.Valid(in.Query) {
        httperror.Write(w, r, httperror.BadRequest("query is not valid JSON"))
        return
    }

    row, err := h.db.CreateSavedSearch(r.Context(), CreateSavedSearchParams{
        ID: uuid.Must(uuid.NewV7()), UserID: user.ID,
        Name: in.Name, Query: in.Query, Kind: "user",
    })
    if errors.Is(err, pgx.ErrNoRows) {
        httperror.Write(w, r, &httperror.Error{
            Type: TypeNameExists, Title: "saved-search-name-exists",
            Status: 409, Detail: "name already in use for this user",
        })
        return
    }
    if err != nil { httperror.Write(w, r, httperror.Internal("save failed")); return }

    w.WriteHeader(http.StatusCreated)
    json.NewEncoder(w).Encode(toDTO(row))
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
    user := userFromCtx(r.Context())
    rows, err := h.db.ListSavedSearches(r.Context(), user.ID)
    if err != nil { httperror.Write(w, r, httperror.Internal("list failed")); return }
    out := make([]SavedSearch, 0, len(rows))
    for _, row := range rows { out = append(out, toDTO(row)) }
    json.NewEncoder(w).Encode(map[string]any{"items": out})
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request) {
    user := userFromCtx(r.Context())
    id, err := uuid.Parse(chi.URLParam(r, "id"))
    if err != nil { httperror.Write(w, r, httperror.BadRequest("invalid id")); return }

    n, err := h.db.DeleteSavedSearch(r.Context(), DeleteSavedSearchParams{
        ID: id, UserID: user.ID,
    })
    if err != nil { httperror.Write(w, r, httperror.Internal("delete failed")); return }
    if n == 0 {
        httperror.Write(w, r, httperror.NotFound("saved search"))
        return
    }
    w.WriteHeader(http.StatusNoContent)
}
```

## 6. SQL — sqlc inputs

`shared/db/queries/saved_searches.sql`:

```sql
-- name: CreateSavedSearch :one
INSERT INTO saved_searches (id, user_id, name, query, kind)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id, name) DO NOTHING
RETURNING *;

-- name: ListSavedSearches :many
SELECT * FROM saved_searches
 WHERE user_id = $1
 ORDER BY created_at DESC;

-- name: DeleteSavedSearch :execrows
DELETE FROM saved_searches
 WHERE id = $1 AND user_id = $2;
```

## 7. Test plan

### 7.1 Unit

| Test | What it pins |
|---|---|
| `TestCreateInputValidation` | Empty `name` → 422; missing `query` → 422; `query` not JSON → 400. |

### 7.2 Integration (`handler_test.go`)

| Test | What it pins |
|---|---|
| `TestSaveAndList` | POST a saved search → GET shows it. |
| `TestNameUniquePerUser` | POST twice with same name → second is 409 `saved-search-name-exists`. |
| `TestSameNameDifferentUsers` | User A and B both POST `name="archive"` → both succeed, each only sees their own. |
| `TestUnknownFilterKeyStored` | POST `{filters:{future_key:"x", language:["ar"]}}` → stored verbatim; on GET the JSON is returned exactly as POSTed. |
| `TestReplayUnknownKeyIgnored` | Pass the stored JSON to `POST /api/search` → request succeeds (Story 7.8 ignores unknown keys). |
| `TestDeleteOwn` | DELETE a saved search → 204; GET no longer returns it. |
| `TestDeleteOtherUser404` | User B tries to DELETE user A's saved search → 404 (does not leak existence). |
| `TestUserDeletedCascades` | Delete user A → all of A's saved searches gone (FK cascade). |
| `TestSmartCollectionRow` | Insert a row with `kind='smart_collection'` directly → GET still returns it (Story 9.14 reads via this same store). |

## 8. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Two POSTs with the same name from the same user | First wins; second 409. | `TestNameUniquePerUser` |
| User deletes their account (Epic 10) | FK `ON DELETE CASCADE` removes all of their saved searches. | `TestUserDeletedCascades` |
| Stored JSON contains a `mode` value the API doesn't recognize | The validator on `/api/search` rejects with 400 at replay time; the saved row is not removed (the user can fix it). | Documented |
| Saved search with extreme filter (e.g. 1000 tag values) | Query column is JSONB; size capped by Postgres TOAST. We do not enforce a size limit at this layer; Story 7.19 caps the body to 16 KB on POST. | Story 7.19 |
| User renames the saved search | Out of scope here — we do not expose PATCH. If the user wants a rename they delete + recreate. | Documented |
| Smart Collection row deleted from `saved_searches` directly | Epic 9 Story 9.14 owns the cleanup; this story is forward-compat. | Documented |

## 9. Acceptance checklist

- [ ] POST persists name + query; per-user uniqueness enforced.
- [ ] GET returns only the requesting user's rows.
- [ ] Cascade deletes on user removal.
- [ ] Forward-compat replay tolerates unknown keys.
- [ ] All `Test*` cases pass.
- [ ] `specs/epics/07-api-server/README.md` ticks story 7.9.
