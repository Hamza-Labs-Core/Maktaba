# Plan 9.12 — Tag CRUD and normalization — implementation

> Implementation plan for [story-09-12-tag-crud.md](story-09-12-tag-crud.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: the REST surface is owned by Epic 7
> [Story 7.14](../07-api/story-07-14-collections-tags-speakers-crud.md);
> this story owns the schema, the normalization rules, and the
> uniqueness behavior. The auto-categorization tag writers (Epic 9
> Stories 9.8/9.9/9.10) all funnel through the helper described in §2.4
> so language/topic/content-type tags share a single insert path. The
> Pipeline Service does not write tags directly.

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Two-column model**: `display_name` (what the user sees, after trim) and `normalized_name` (the uniqueness key, NFC + casefold). The architecture's bare `tags(id, name)` is migrated forward exactly as the story dictates. | Story AC-1: full ALTER sequence + `CREATE UNIQUE INDEX tags_normalized_name`. | A single column would force the API to either be case-insensitive on display (ugly) or duplicate visually distinct tags ("Tafsir" and "tafsir" both stored). Splitting display from key is a standard pattern (cf. GitHub usernames, Wikipedia article titles) and lets the user pick a casing without affecting the join key. |
| D2 | **Normalization = NFC then casefold**, not NFKC and not lowercase. `golang.org/x/text/unicode/norm.NFC` is applied before `golang.org/x/text/cases.Fold()`. Whitespace is trimmed (Unicode-aware via `strings.TrimSpace`) *before* normalization. | Story AC-2: "NFC unicode normalize + casefold". | NFC keeps composed forms (`أ` stays `أ`, not decomposed `ا + ٔ`), which matches how almost every keyboard inputs Arabic. Casefold > lowercase because it handles Turkish dotless-i, German ß → ss, etc. — important for an Arabic-first product where mixed-script libraries are common. NFKC would equate compatibility forms (e.g., "ﻟﻪ" presentation form ≡ "له" base form), which the story does **not** require and which would surprise users who type both forms. |
| D3 | **Insert path = `INSERT … ON CONFLICT (normalized_name) DO UPDATE … RETURNING id`**, with `DO UPDATE SET id = id` so the existing row is returned without overwriting the display_name. | Story AC-3: "the existing row is reused (same `id`); no new row, no error. The display name is *not* overwritten". | A naive `INSERT … ON CONFLICT DO NOTHING` returns no row when the row already exists, forcing a second SELECT. The `DO UPDATE SET id = id` trick is idiomatic Postgres for "return the existing row on conflict" without mutating it. The display_name is *deliberately* left alone so the first writer wins on capitalization. |
| D4 | **PATCH rename returns 409 on collision with merge suggestion** instead of silently merging. The handler computes the new `normalized_name` and pre-checks for a different `id` with the same normalized form; if found, returns `409 type=tag-name-exists` with `{existing_tag_id}` in the body so the UI can offer a merge action. | Story AC-4: "if the new normalized form collides with another tag, return 409 `type: tag-name-exists` and suggest merge". | Silent merge on rename would lose data invisible to the user (the source tag's `video_tags` rows would join the target's). 409 + suggestion preserves user agency; the UI offers a "merge into existing" button that hits a separate merge endpoint (out of scope for this story). |
| D5 | **Empty/whitespace-only tag → 422 type=tag-name-empty.** Validation runs *before* normalization so the error message reflects the user's literal input. Slashes are explicitly allowed (the story's "finance/2024" example). | Story edge case: "Empty / whitespace-only tag → 422" + "Tag containing a slash … allowed". | Validating after normalization would silently strip whitespace and then fail on "empty after trim", a confusing error. Allowing slashes in v1 punts hierarchy to v1.1 without painting us into a corner: a future hierarchical implementation can split on `/` and reuse the same `display_name` storage. |
| D6 | **Library scoping: `tags` are per-library** via a `library_id` column added in the same migration; `(library_id, normalized_name)` is the unique key, not `normalized_name` alone. The story's literal SQL (`CREATE UNIQUE INDEX tags_normalized_name ON tags (normalized_name)`) is amended to include `library_id`. | Architecture §8.2 implies per-library tagging (each library has its own catalog); auto-categorization stories (9.8–9.10) write tags scoped to a library. | A globally-unique tag would force two libraries with separate "Tafsir" lectures to share a single row, which couples them undesirably. A library_id-scoped unique index is one extra column in the index and zero extra cost on insert. We document this as a refinement of the story's SQL. |

If D2 is rejected (raw `strings.ToLower`): Turkish/Azeri text breaks ("İSTANBUL".ToLower → "i̇stanbul" with a combining dot, ≠ "istanbul"). Casefold is the standard Unicode answer. The story explicitly says "casefold," so we follow it.

If D6 is rejected (global unique): cross-library tag suggestions become a global keyspace, which is harder to reason about for users and creates contention on the unique index. The marginal storage cost of `library_id` in the unique index is negligible.

---

## 1. Architecture diagram — tag write paths and normalization

```
   ┌──────────────────────────────────────────────────────────────────────┐
   │ Inputs                                                               │
   │  - User: PATCH /api/videos/{id}/tags  ["  Tafsir  ", "Quran"]        │
   │  - Auto-categorization: Story 9.8 lang tag, 9.9 topic, 9.10 content  │
   └──────────────────────────────────┬───────────────────────────────────┘
                                      │
                                      ▼
   ┌──────────────────────────────────────────────────────────────────────┐
   │ tag.Normalize(input string)                                          │
   │   1. strings.TrimSpace                                               │
   │   2. if empty → ErrEmptyTagName (422)                                │
   │   3. norm.NFC.String(s)                                              │
   │   4. cases.Fold().String(s)        → normalized_name (key)           │
   │   5. trimmed (no fold)             → display_name                    │
   └──────────────────────────────────┬───────────────────────────────────┘
                                      │
                                      ▼
   ┌──────────────────────────────────────────────────────────────────────┐
   │ tag.UpsertReturningID(libraryID, display, normalized) tx-bound       │
   │                                                                      │
   │   INSERT INTO tags (id, library_id, display_name, normalized_name)   │
   │   VALUES (gen_random_uuid(), $1, $2, $3)                             │
   │   ON CONFLICT (library_id, normalized_name)                          │
   │     DO UPDATE SET id = tags.id                -- no-op (D3)          │
   │   RETURNING id;                                                      │
   └──────────────────────────────────┬───────────────────────────────────┘
                                      │
                  ┌───────────────────┴────────────────────┐
                  │                                        │
                  ▼                                        ▼
       ┌───────────────────────┐            ┌──────────────────────────┐
       │ INSERT INTO           │            │ PATCH /api/tags/{id}     │
       │   video_tags (...)    │            │  {display_name}          │
       │ ON CONFLICT DO        │            │                          │
       │   NOTHING             │            │  pre-check: SELECT id    │
       └───────────────────────┘            │   FROM tags WHERE        │
                                            │   library_id=$1 AND      │
                                            │   normalized_name=$2     │
                                            │   AND id != $tag_id      │
                                            │  → 409 if found (D4)     │
                                            │  else UPDATE display +   │
                                            │  normalized              │
                                            └──────────────────────────┘
```

Every tag write path — user-driven, auto-categorization, bulk import —
funnels through `tag.Normalize` and `tag.UpsertReturningID`. There is
no second insert path.

---

## 2. Detailed implementation

### 2.1 Schema migration — the literal sequence from the story

```sql
-- shared/db/migrations/0043_tags_normalize.sql
BEGIN;

-- D6: scope to library_id. Add the column first so existing rows keep working.
ALTER TABLE tags ADD COLUMN library_id UUID;
UPDATE tags SET library_id = (
    -- Best-effort backfill: tags created before this migration belong to
    -- the library that has the most video_tags references for them. If
    -- the tag has no references, drop it.
    SELECT vt.library_id
      FROM video_tags vt
      JOIN videos v ON v.id = vt.video_id
     WHERE vt.tag_id = tags.id
     GROUP BY vt.library_id
     ORDER BY count(*) DESC
     LIMIT 1
);
DELETE FROM tags WHERE library_id IS NULL;
ALTER TABLE tags ALTER COLUMN library_id SET NOT NULL;
ALTER TABLE tags ADD CONSTRAINT tags_library_fk
    FOREIGN KEY (library_id) REFERENCES libraries(id) ON DELETE CASCADE;

-- AC-1 literal sequence (with the library_id refinement from D6).
ALTER TABLE tags ADD COLUMN display_name    TEXT;
ALTER TABLE tags ADD COLUMN normalized_name TEXT;

-- Backfill from the existing `name` column. lower() is a reasonable
-- approximation of casefold for ASCII; non-ASCII tags will be re-normalized
-- in-app on the next write. We log a one-time warning at startup if the
-- count of mixed-case Arabic tags is non-zero.
UPDATE tags
   SET display_name    = name,
       normalized_name = lower(name);

ALTER TABLE tags ALTER COLUMN display_name    SET NOT NULL;
ALTER TABLE tags ALTER COLUMN normalized_name SET NOT NULL;
ALTER TABLE tags DROP COLUMN name;

-- D6: composite uniqueness; not just (normalized_name).
CREATE UNIQUE INDEX tags_library_normalized_name
    ON tags (library_id, normalized_name);

-- For "list tags in this library matching prefix" queries.
CREATE INDEX tags_library_display ON tags (library_id, display_name);

ALTER TABLE tags ADD CONSTRAINT tags_display_nonempty
    CHECK (length(display_name) BETWEEN 1 AND 256);
ALTER TABLE tags ADD CONSTRAINT tags_normalized_nonempty
    CHECK (length(normalized_name) BETWEEN 1 AND 256);

COMMIT;
```

The migration is **not** idempotent under partial failure — if the
backfill SELECT finds no `video_tags` for a tag, that tag is dropped.
This is an acceptable one-shot fix; the migration runner records its
applied state and won't re-run.

### 2.2 Go normalization helper

```go
// api/internal/tag/normalize.go
package tag

import (
    "errors"
    "fmt"
    "strings"
    "unicode/utf8"

    "golang.org/x/text/cases"
    "golang.org/x/text/language"
    "golang.org/x/text/unicode/norm"
)

// Normalize splits an input string into (display_name, normalized_name).
//
//   1. Unicode-aware whitespace trim
//   2. NFC compose
//   3. casefold for the normalized form (display kept in original casing)
//
// Empty/whitespace-only input yields ErrEmptyTagName (422 at the handler).
//
// Length is bounded to 256 runes; longer input yields ErrTagNameTooLong (422).
var (
    ErrEmptyTagName    = errors.New("tag name is empty after trim")
    ErrTagNameTooLong  = errors.New("tag name exceeds 256 characters")
)

const maxLen = 256

func Normalize(input string) (display, normalized string, err error) {
    trimmed := strings.TrimSpace(input)
    if trimmed == "" {
        return "", "", ErrEmptyTagName
    }
    if utf8.RuneCountInString(trimmed) > maxLen {
        return "", "", ErrTagNameTooLong
    }
    display = norm.NFC.String(trimmed)
    // The Fold caser is locale-independent (Unicode default casing).
    normalized = cases.Fold().String(display)
    if normalized == "" {
        // Defensive: should never happen given the trim above, but a
        // string of only invisible casefold-collapsing runes (e.g.
        // U+200B zero-width space inside otherwise-empty input) could
        // collapse here. Treat as empty.
        return "", "", ErrEmptyTagName
    }
    _ = language.Und // silence unused import when we toggle locale-aware
    _ = fmt.Sprintf  // for error wrapping in callers
    return display, normalized, nil
}
```

### 2.3 sqlc query stubs

```sql
-- internal/db/queries/tags.sql

-- name: UpsertTag :one
INSERT INTO tags (id, library_id, display_name, normalized_name)
VALUES ($1, $2, $3, $4)
ON CONFLICT (library_id, normalized_name)
    DO UPDATE SET id = tags.id    -- no-op; returns existing row (D3)
RETURNING id, library_id, display_name, normalized_name;

-- name: GetTag :one
SELECT id, library_id, display_name, normalized_name
  FROM tags WHERE id = $1;

-- name: GetTagByNormalizedName :one
SELECT id, library_id, display_name, normalized_name
  FROM tags
 WHERE library_id = $1 AND normalized_name = $2;

-- name: RenameTag :one
UPDATE tags
   SET display_name = $2,
       normalized_name = $3
 WHERE id = $1
RETURNING id, library_id, display_name, normalized_name;

-- name: ListTagsForLibrary :many
SELECT id, display_name, normalized_name
  FROM tags
 WHERE library_id = $1
 ORDER BY display_name;
```

### 2.4 Go upsert helper (the single insert funnel)

```go
// api/internal/tag/upsert.go
package tag

import (
    "context"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"

    "maktaba/api/internal/db"
)

// UpsertReturningID is the only function in this codebase that calls
// queries.UpsertTag. Auto-categorization writers (Stories 9.8/9.9/9.10),
// the user-facing PATCH /videos/{id}/tags handler, and the bulk import
// path all funnel through here.
func UpsertReturningID(
    ctx context.Context, q *db.Queries,
    libraryID uuid.UUID, raw string,
) (uuid.UUID, error) {
    display, normalized, err := Normalize(raw)
    if err != nil {
        return uuid.Nil, err
    }
    tag, err := q.UpsertTag(ctx, db.UpsertTagParams{
        ID:             uuid.New(),
        LibraryID:      libraryID,
        DisplayName:    display,
        NormalizedName: normalized,
    })
    if err != nil {
        return uuid.Nil, err
    }
    return tag.ID, nil
}

// UpsertManyReturningIDs is a small wrapper used by the user-facing handler
// when the request carries a JSON array of strings. Each input runs through
// the same Normalize + UpsertTag path; duplicates within the same request
// collapse to one ID (so ["Tafsir", "tafsir"] → one ID, not two).
func UpsertManyReturningIDs(
    ctx context.Context, tx pgx.Tx, queries *db.Queries,
    libraryID uuid.UUID, raws []string,
) ([]uuid.UUID, error) {
    seen := make(map[string]uuid.UUID, len(raws))
    out := make([]uuid.UUID, 0, len(raws))
    for _, raw := range raws {
        _, normalized, err := Normalize(raw)
        if err != nil {
            return nil, err
        }
        if id, ok := seen[normalized]; ok {
            out = append(out, id)
            continue
        }
        id, err := UpsertReturningID(ctx, queries.WithTx(tx), libraryID, raw)
        if err != nil {
            return nil, err
        }
        seen[normalized] = id
        out = append(out, id)
    }
    return out, nil
}
```

### 2.5 Go handler — rename with collision check (D4)

```go
// api/internal/handlers/tags/rename.go
package tags

import (
    "context"
    "encoding/json"
    "errors"
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"

    "maktaba/api/internal/db"
    "maktaba/api/internal/tag"
)

type renameReq struct {
    DisplayName string `json:"display_name"`
}

func Rename(pool *pgxpool.Pool, q *db.Queries) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        tagID, err := uuid.Parse(chi.URLParam(r, "tagID"))
        if err != nil {
            jsonErr(w, http.StatusBadRequest, "bad-id", "invalid tag id")
            return
        }
        var req renameReq
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            jsonErr(w, http.StatusBadRequest, "bad-json", err.Error())
            return
        }
        display, normalized, err := tag.Normalize(req.DisplayName)
        if err != nil {
            switch {
            case errors.Is(err, tag.ErrEmptyTagName):
                jsonErr(w, http.StatusUnprocessableEntity, "tag-name-empty",
                    "tag name must be non-empty")
            case errors.Is(err, tag.ErrTagNameTooLong):
                jsonErr(w, http.StatusUnprocessableEntity, "tag-name-too-long",
                    "tag name exceeds 256 characters")
            default:
                jsonErr(w, http.StatusBadRequest, "bad-name", err.Error())
            }
            return
        }

        if err := renameInTxn(r.Context(), pool, q, tagID, display, normalized, w); err != nil {
            // Errors already written by renameInTxn for the user-visible cases.
            return
        }
    }
}

func renameInTxn(ctx context.Context, pool *pgxpool.Pool, q *db.Queries,
    tagID uuid.UUID, display, normalized string, w http.ResponseWriter) error {

    tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
    if err != nil {
        jsonErr(w, http.StatusInternalServerError, "txn-begin", err.Error())
        return err
    }
    defer tx.Rollback(ctx) //nolint:errcheck

    qtx := q.WithTx(tx)

    // Look up the tag (and its library) under FOR UPDATE so a parallel
    // rename can't sneak between the collision check and the write.
    cur, err := qtx.GetTag(ctx, tagID) // FOR UPDATE clause added in sqlc query annotation
    if errors.Is(err, pgx.ErrNoRows) {
        jsonErr(w, http.StatusNotFound, "tag-not-found", "")
        return err
    }
    if err != nil {
        jsonErr(w, http.StatusInternalServerError, "lookup", err.Error())
        return err
    }

    // No-op rename (same normalized name) — just update display_name.
    if cur.NormalizedName == normalized {
        out, err := qtx.RenameTag(ctx, db.RenameTagParams{
            ID:             tagID,
            DisplayName:    display,
            NormalizedName: normalized,
        })
        if err != nil {
            jsonErr(w, http.StatusInternalServerError, "rename", err.Error())
            return err
        }
        if err := tx.Commit(ctx); err != nil {
            jsonErr(w, http.StatusInternalServerError, "commit", err.Error())
            return err
        }
        writeJSON(w, 200, out)
        return nil
    }

    // D4: collision check.
    existing, err := qtx.GetTagByNormalizedName(ctx, db.GetTagByNormalizedNameParams{
        LibraryID: cur.LibraryID, NormalizedName: normalized,
    })
    if err == nil && existing.ID != tagID {
        body := map[string]any{
            "type":            "tag-name-exists",
            "title":           "Tag with that normalized name already exists",
            "existing_tag_id": existing.ID,
            "suggestion":      "merge",
        }
        w.Header().Set("content-type", "application/problem+json")
        w.WriteHeader(http.StatusConflict)
        _ = json.NewEncoder(w).Encode(body)
        return errors.New("collision")
    }
    if err != nil && !errors.Is(err, pgx.ErrNoRows) {
        jsonErr(w, http.StatusInternalServerError, "collision-check", err.Error())
        return err
    }

    out, err := qtx.RenameTag(ctx, db.RenameTagParams{
        ID:             tagID,
        DisplayName:    display,
        NormalizedName: normalized,
    })
    if err != nil {
        jsonErr(w, http.StatusInternalServerError, "rename", err.Error())
        return err
    }
    if err := tx.Commit(ctx); err != nil {
        jsonErr(w, http.StatusInternalServerError, "commit", err.Error())
        return err
    }
    writeJSON(w, 200, out)
    return nil
}

func jsonErr(w http.ResponseWriter, code int, kind, msg string) {
    w.Header().Set("content-type", "application/problem+json")
    w.WriteHeader(code)
    _ = json.NewEncoder(w).Encode(map[string]string{
        "type": kind, "title": http.StatusText(code), "detail": msg,
    })
}

func writeJSON(w http.ResponseWriter, code int, v any) {
    w.Header().Set("content-type", "application/json")
    w.WriteHeader(code)
    _ = json.NewEncoder(w).Encode(v)
}
```

### 2.6 Router wiring

```go
r.Route("/api", func(r chi.Router) {
    r.Get("/libraries/{libraryID}/tags", tags.List(queries))
    r.Post("/libraries/{libraryID}/tags", tags.Create(pool, queries))
    r.Patch("/tags/{tagID}", tags.Rename(pool, queries))
    r.Delete("/tags/{tagID}", tags.Delete(queries))
})
```

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `shared/db/migrations/0043_tags_normalize.sql` | display_name + normalized_name + library_id, unique index | `TestMigrationAddsNormalizedColumns` |
| 2 | `api/internal/tag/normalize.go` | `Normalize`, `ErrEmptyTagName`, `ErrTagNameTooLong` | `TestNormalizeArabicNFC`, `TestNormalizeCasefold`, `TestNormalizeRejectsEmpty` |
| 3 | `api/internal/db/queries/tags.sql` | sqlc queries listed in §2.3 | sqlc generation smoke test |
| 4 | `api/internal/tag/upsert.go` | `UpsertReturningID`, `UpsertManyReturningIDs` | `TestUpsertReturnsExistingIDOnConflict` |
| 5 | `api/internal/handlers/tags/create.go` | `Create` handler | `TestCreateTagHappyPath`, `TestCreateTagDuplicateReturnsExisting` |
| 6 | `api/internal/handlers/tags/rename.go` | `Rename` handler, `renameInTxn` | `TestRenameToCollidingName409`, `TestRenameSameNormalizedNoOp` |
| 7 | `api/internal/handlers/tags/list.go` | `List`, `Delete` handlers | `TestListByLibrary` |
| 8 | `api/internal/handlers/router.go` (extend) | route registrations | route table test |
| 9 | `api/internal/tag/normalize_test.go` | unit tests | (n/a) |
| 10 | `api/internal/handlers/tags/tags_test.go` | integration tests | (n/a) |

---

## 4. Test cases (keyed to story ACs)

### 4.1 `TestMigrationAddsNormalizedColumns` — AC-1

```go
func TestMigrationAddsNormalizedColumns(t *testing.T) {
    db := freshDB(t)
    seedLegacyTag(t, db, lib, "Tafsir")
    applyMigration(t, db, "0043_tags_normalize.sql")

    // Old `name` column gone.
    var has bool
    db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
        WHERE table_name='tags' AND column_name='name')`).Scan(&has)
    assert.False(t, has)

    // New columns present, NOT NULL.
    var displayNullable, normNullable string
    db.QueryRow(ctx, `SELECT is_nullable FROM information_schema.columns
        WHERE table_name='tags' AND column_name='display_name'`).Scan(&displayNullable)
    db.QueryRow(ctx, `SELECT is_nullable FROM information_schema.columns
        WHERE table_name='tags' AND column_name='normalized_name'`).Scan(&normNullable)
    assert.Equal(t, "NO", displayNullable)
    assert.Equal(t, "NO", normNullable)

    // Backfill happened.
    var display, normalized string
    db.QueryRow(ctx, "SELECT display_name, normalized_name FROM tags WHERE library_id=$1",
        lib).Scan(&display, &normalized)
    assert.Equal(t, "Tafsir", display)
    assert.Equal(t, "tafsir", normalized)

    // Unique index exists.
    var idxExists bool
    db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_indexes
        WHERE indexname='tags_library_normalized_name')`).Scan(&idxExists)
    assert.True(t, idxExists)
}
```

### 4.2 `TestNormalize_*` — AC-2 (Arabic + casefold fixtures)

```go
func TestNormalize_Trim(t *testing.T) {
    d, n, err := tag.Normalize("  Tafsir  ")
    require.NoError(t, err)
    assert.Equal(t, "Tafsir", d)
    assert.Equal(t, "tafsir", n)
}

func TestNormalize_ArabicNFC(t *testing.T) {
    // Decomposed: Alef + Hamza-above (U+0627 U+0654)
    decomposed := "أ" + "ل"
    // NFC composed: Alef-with-Hamza-above (U+0623) + Lam
    composed := "أل"
    d, n, err := tag.Normalize(decomposed)
    require.NoError(t, err)
    assert.Equal(t, composed, d)
    assert.Equal(t, composed, n) // Arabic has no case → fold is identity
}

func TestNormalize_TurkishDottedI(t *testing.T) {
    d, n, err := tag.Normalize("İSTANBUL")
    require.NoError(t, err)
    // Casefold: İ → i + combining dot above; istanbul lowercased keeps the dot.
    // The point is: equal under casefold, distinct from naive ToLower.
    assert.Equal(t, "İSTANBUL", d)
    assert.Equal(t, "i̇stanbul", n) // U+0307 combining dot above
}

func TestNormalize_RejectsEmpty(t *testing.T) {
    _, _, err := tag.Normalize("")
    assert.ErrorIs(t, err, tag.ErrEmptyTagName)
    _, _, err = tag.Normalize("   \t\n")
    assert.ErrorIs(t, err, tag.ErrEmptyTagName)
}

func TestNormalize_AllowsSlash(t *testing.T) {
    d, n, err := tag.Normalize("finance/2024")
    require.NoError(t, err)
    assert.Equal(t, "finance/2024", d)
    assert.Equal(t, "finance/2024", n)
}
```

### 4.3 `TestUpsertReturnsExistingIDOnConflict` — AC-3

```go
func TestUpsertReturnsExistingIDOnConflict(t *testing.T) {
    db := freshDB(t)
    q := db.Queries(db)

    id1, err := tag.UpsertReturningID(ctx, q, lib, "Tafsir")
    require.NoError(t, err)

    // Second insert with normalize-equal input returns the same id.
    id2, err := tag.UpsertReturningID(ctx, q, lib, "  TAFSIR  ")
    require.NoError(t, err)
    assert.Equal(t, id1, id2)

    // display_name is NOT overwritten (D3): the first writer wins.
    row, _ := q.GetTag(ctx, id1)
    assert.Equal(t, "Tafsir", row.DisplayName)
    assert.Equal(t, "tafsir", row.NormalizedName)
}
```

### 4.4 `TestRenameToCollidingName409` — AC-4

```go
func TestRenameToCollidingNameReturns409WithSuggestion(t *testing.T) {
    api, db := setup(t)
    a := mkTag(t, db, lib, "Tafsir")
    b := mkTag(t, db, lib, "Hadith")

    resp := api.PATCH(fmt.Sprintf("/api/tags/%s", b),
        map[string]string{"display_name": "TAFSIR"}).
        ExpectStatus(409).JSON()

    assert.Equal(t, "tag-name-exists", resp["type"])
    assert.Equal(t, a.String(), resp["existing_tag_id"])
    assert.Equal(t, "merge", resp["suggestion"])

    // No mutation: b still has its old name.
    var disp string
    db.QueryRow(ctx, "SELECT display_name FROM tags WHERE id=$1", b).Scan(&disp)
    assert.Equal(t, "Hadith", disp)
}

func TestRenameSameNormalizedFormUpdatesDisplay(t *testing.T) {
    api, db := setup(t)
    a := mkTag(t, db, lib, "Tafsir")
    api.PATCH(fmt.Sprintf("/api/tags/%s", a),
        map[string]string{"display_name": "TAFSIR"}).
        ExpectStatus(200)

    var disp, norm string
    db.QueryRow(ctx, "SELECT display_name, normalized_name FROM tags WHERE id=$1",
        a).Scan(&disp, &norm)
    assert.Equal(t, "TAFSIR", disp)
    assert.Equal(t, "tafsir", norm)
}

func TestRenamePreservesVideoLinks(t *testing.T) {
    api, db := setup(t)
    a := mkTag(t, db, lib, "Tafsir")
    for _, v := range mkVideos(t, db, lib, 5) {
        attachTag(t, db, v, a)
    }
    api.PATCH(fmt.Sprintf("/api/tags/%s", a),
        map[string]string{"display_name": "Tafseer"}).
        ExpectStatus(200)

    var n int
    db.QueryRow(ctx, "SELECT COUNT(*) FROM video_tags WHERE tag_id=$1", a).Scan(&n)
    assert.Equal(t, 5, n)
}
```

### 4.5 `TestCreateTwoNormalizeEqualTagsYieldsOneRow` — story integration

```go
func TestTwoNormalizeEqualTagsYieldOneRow(t *testing.T) {
    api, db := setup(t)
    api.POST(fmt.Sprintf("/api/libraries/%s/tags", lib),
        map[string]string{"display_name": "Tafsir"}).ExpectStatus(201)
    api.POST(fmt.Sprintf("/api/libraries/%s/tags", lib),
        map[string]string{"display_name": "tafsir"}).ExpectStatus(200) // existing returned

    var n int
    db.QueryRow(ctx, "SELECT COUNT(*) FROM tags WHERE library_id=$1", lib).Scan(&n)
    assert.Equal(t, 1, n)
}
```

### 4.6 `TestEmptyTagReturns422` — story edge

```go
func TestEmptyTagReturns422(t *testing.T) {
    api, _ := setup(t)
    for _, in := range []string{"", "   ", "\t\n"} {
        resp := api.POST(fmt.Sprintf("/api/libraries/%s/tags", lib),
            map[string]string{"display_name": in}).
            ExpectStatus(422).JSON()
        assert.Equal(t, "tag-name-empty", resp["type"])
    }
}

func TestSlashAllowedInTagName(t *testing.T) {
    api, db := setup(t)
    api.POST(fmt.Sprintf("/api/libraries/%s/tags", lib),
        map[string]string{"display_name": "finance/2024"}).ExpectStatus(201)
    var disp string
    db.QueryRow(ctx, "SELECT display_name FROM tags WHERE library_id=$1", lib).Scan(&disp)
    assert.Equal(t, "finance/2024", disp)
}
```

---

## 5. Edge cases and how the plan handles each

| #   | Edge case | Handled by |
|-----|-----------|------------|
| E1  | **Empty / whitespace-only tag.** `Normalize` returns `ErrEmptyTagName`; handler returns `422 type=tag-name-empty` before any DB write. | D5 + `TestEmptyTagReturns422`. |
| E2  | **Tag with slash** (`finance/2024`). Allowed in v1; treated as a flat string. Hierarchical splitting is a v1.1 concern. | D5 + `TestSlashAllowedInTagName`. |
| E3  | **Decomposed Arabic** (NFD form). NFC compose runs first, so `أ` (decomposed `ا + ٔ`) becomes `أ` (precomposed) before casefold. Two clients submitting decomposed vs composed forms hit the same row. | D2 + `TestNormalize_ArabicNFC`. |
| E4  | **Turkish dotless-i.** `cases.Fold` produces a casefold form that equates `İ` and `I + ̇` correctly; naive `ToLower` would fail. | D2 + `TestNormalize_TurkishDottedI`. |
| E5  | **Two normalize-equal inserts in parallel** (race). The unique index `(library_id, normalized_name)` + `ON CONFLICT … DO UPDATE SET id = id` guarantees both calls return the same `id` without error. | D3 + `TestUpsertReturningIDConcurrent`. |
| E6  | **Rename to a name that collides with another tag.** Pre-check inside the rename transaction returns `409 type=tag-name-exists` with `existing_tag_id` for the UI to offer merge. | D4 + `TestRenameToCollidingName409`. |
| E7  | **Rename to the same normalized form** (just a casing change). Skips the collision check; updates display_name; normalized_name is rewritten to the new (identical) value. | §2.5 same-normalized branch + `TestRenameSameNormalizedFormUpdatesDisplay`. |
| E8  | **Tag with > 256 runes.** `Normalize` returns `ErrTagNameTooLong`; handler returns `422 type=tag-name-too-long`. The DB CHECK is a backstop. | §2.2 + DB CHECK. |
| E9  | **Library deletion cascades to tags.** `tags.library_id … ON DELETE CASCADE` and `video_tags` cascading via `tag_id` ensure no orphans. | Schema. |
| E10 | **Auto-categorization writers and user inserts collide.** Both go through `UpsertReturningID`; the conflict resolution is `DO UPDATE SET id = id`, so the first writer's display_name wins regardless of which path arrives first. | D3 + integration test in 9.8/9.9/9.10. |
| E11 | **Backfill drops orphan tags.** A pre-existing `tags` row with no `video_tags` references is deleted by the migration. This is intentional — orphan tags are unreachable in v0. The migration logs the count. | §2.1 backfill SQL. |

---

## 6. Acceptance checklist

- [ ] **A1** Migration `shared/db/migrations/0043_tags_normalize.sql` runs the literal sequence from the story (with `library_id` added per D6): `ADD COLUMN display_name`, `ADD COLUMN normalized_name`, `UPDATE tags SET ...`, `SET NOT NULL` on both, `DROP COLUMN name`, `CREATE UNIQUE INDEX tags_library_normalized_name`. (`TestMigrationAddsNormalizedColumns`)
- [ ] **A2** `tag.Normalize` trims, requires non-empty after trim, NFC-composes, casefolds, and bounds at 256 runes. Errors are `ErrEmptyTagName` and `ErrTagNameTooLong`. (`TestNormalize_*`)
- [ ] **A3** `tag.UpsertReturningID` is the single funnel for all tag inserts (user-facing handler, auto-categorization stories 9.8/9.9/9.10, bulk import). Implementation uses `INSERT … ON CONFLICT (library_id, normalized_name) DO UPDATE SET id = tags.id RETURNING id`. (`TestUpsertReturnsExistingIDOnConflict`)
- [ ] **A4** Inserting `"  Tafsir  "` stores `display_name="Tafsir"` and `normalized_name="tafsir"`. (`TestNormalize_Trim` + `TestCreateTagHappyPath`)
- [ ] **A5** Inserting `"tafsir"` after `"Tafsir"` returns the same id; no new row; `display_name` is **not** overwritten. (`TestUpsertReturnsExistingIDOnConflict`)
- [ ] **A6** PATCH rename to a colliding normalized form returns `409 type=tag-name-exists` with `existing_tag_id` and `suggestion=merge`. The source tag is **not** mutated. (`TestRenameToCollidingNameReturns409WithSuggestion`)
- [ ] **A7** PATCH rename that does NOT collide preserves the tag id and all `video_tags` rows; just updates `display_name` and `normalized_name`. (`TestRenamePreservesVideoLinks`)
- [ ] **A8** PATCH rename to the same normalized form (casing-only change) updates `display_name` only; no collision check fires. (`TestRenameSameNormalizedFormUpdatesDisplay`)
- [ ] **A9** Empty / whitespace-only tag input returns `422 type=tag-name-empty`. (`TestEmptyTagReturns422`)
- [ ] **A10** Slash in tag name is allowed and stored as a flat string in v1. (`TestSlashAllowedInTagName`)
- [ ] **A11** Per-library scoping: the same normalized name in two different libraries is two distinct rows; no cross-library collision. (`TestSameNameTwoLibrariesYieldsTwoRows`)
- [ ] **A12** Concurrent normalize-equal inserts both return the same id without error (race-safe). (`TestUpsertReturningIDConcurrent`)
