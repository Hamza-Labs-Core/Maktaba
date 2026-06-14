# Plan 27.1 — Channel definition (CRUD) — implementation

> Implementation plan for [story-27-01-channel-definition.md](story-27-01-channel-definition.md).
> Self-contained. Cross-links: the channel record is the root entity the
> scheduler ([Plan 27.2](plan-27-02-program-scheduler.md)) reads, the
> live engine ([Plan 27.3](plan-27-03-live-stream-engine.md)) serves, and
> the guide ([Plan 27.4](plan-27-04-epg-generation.md)) lists. Reuses the
> `smart_query` filter shape (Story 7.14), the `libraryacl` middleware
> (slot 0072), and the thumbnail/logo re-encode path (Epic 8). Writes
> slot 0081 (`channels`).

---

## 0. Decisions

| #  | Decision | Rationale |
|----|----------|-----------|
| D1 | **`channels` is one table; mode-specific config is `mode_config` JSONB**, not a column-per-mode. | The four modes have disjoint config shapes; a JSONB blob validated by a per-mode schema keeps the table stable and lets the scheduler own the config contract. |
| D2 | **`source_filter` reuses the existing `smart_query` JSON shape** (Story 7.14) verbatim. | Shuffle/smart-mix need "which videos" — exactly what the collection smart-query evaluator already answers; no second filter language. |
| D3 | **`number` unique within scope via a partial unique index**, where scope = `library_id` or the null (multi-library) bucket. | Story AC2. A partial unique index on `(coalesce(library_id, sentinel), number)` enforces it in the DB, not app code. |
| D4 | **`slug` is generated once and stable**; rename doesn't change it unless cleared. | Story AC3 — XMLTV/M3U bind external guides to the slug; a drifting slug breaks Plex/Jellyfin guide mapping. |
| D5 | **Mutating mode/rule/source enqueues a regen but is next-boundary**, never a hard kill of a live session. | Story AC6/AC7 — a watching user must not be yanked between programs by an edit. |
| D6 | **Logo goes through the existing thumbnail re-encode path**; raw upload is never stored. | Story AC9 — image-bomb / content-type safety reuses Epic 8's proven path. |
| D7 | **CRUD lives in a new `api/internal/handlers/channels/` package**, wired into the existing router + `libraryacl`. | Mirrors `handlers/collections`; keeps channel authz on the established ACL middleware. |

---

## 1. Package layout (API Service, Go)

```
api/internal/handlers/channels/
├── channels.go         # CRUD handlers (list/get/create/patch/delete/reorder/logo)
├── modeconfig.go       # per-mode mode_config validation (D1)
├── slug.go             # stable slug derivation + collision suffixing (D4)
├── repo.go             # SQL access to `channels`
├── dto.go              # request/response shapes
└── channels_test.go
```

Wired in `api/internal/router` behind `libraryacl`; logo upload reuses
the thumbnail handler's re-encode helper.

## 2. Mode-config validation (`modeconfig.go`, D1)

```go
// ValidateModeConfig checks the JSONB against the schema for `mode`.
func ValidateModeConfig(mode string, cfg json.RawMessage) error {
    switch mode {
    case "shuffle":   return validateShuffle(cfg)   // optional filter, reshuffle period
    case "marathon":  return validateMarathon(cfg)  // series_id|source, order, loop
    case "schedule":  return validateSchedule(cfg)  // ≥1 slot {days,start,end,source}, fill
    case "smart_mix": return validateSmartMix(cfg)  // daypart_profile, weights, diversity
    default:          return ErrUnknownMode
    }
}
```

The same schemas are mirrored in the Python scheduler
([Plan 27.2](plan-27-02-program-scheduler.md)); the contract lives in
this story's `dto.go` and is documented as the source of truth.

## 3. Data model — migration slot 0081

`shared/db/migrations/0081_channels.sql` (+ `.sqlite.sql`):

```sql
-- +goose Up
-- +goose StatementBegin
-- Slot 0081 (Epic 27 / Story 27.1) — virtual channel definitions.
CREATE TABLE IF NOT EXISTS channels (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id    UUID        REFERENCES libraries(id) ON DELETE CASCADE,  -- null = multi-library
    number        INTEGER     NOT NULL,
    name          TEXT        NOT NULL,
    slug          TEXT        NOT NULL,
    logo_path     TEXT,
    category      TEXT        NOT NULL DEFAULT 'general',
    mode          TEXT        NOT NULL
                              CHECK (mode IN ('shuffle','marathon','schedule','smart_mix')),
    mode_config   JSONB       NOT NULL DEFAULT '{}'::jsonb,
    source_filter JSONB,                                  -- smart_query shape (D2)
    transition    TEXT        NOT NULL DEFAULT 'cut'
                              CHECK (transition IN ('cut','crossfade')),
    enabled       BOOLEAN     NOT NULL DEFAULT true,
    sort_order    INTEGER     NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Number unique within scope: a fixed sentinel UUID stands in for the
-- multi-library (null) bucket so the partial index covers it too (D3).
CREATE UNIQUE INDEX IF NOT EXISTS channels_scope_number_uniq
    ON channels (COALESCE(library_id, '00000000-0000-0000-0000-000000000000'::uuid), number);
CREATE UNIQUE INDEX IF NOT EXISTS channels_scope_slug_uniq
    ON channels (COALESCE(library_id, '00000000-0000-0000-0000-000000000000'::uuid), slug);
CREATE INDEX IF NOT EXISTS channels_enabled_idx ON channels (enabled, sort_order, number);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS channels;
-- +goose StatementEnd
```

The `.sqlite.sql` variant uses `TEXT` for `UUID`/`JSONB`/`TIMESTAMPTZ`
per the existing convention, and emulates the scope-uniqueness with a
generated/`COALESCE` expression index (or an app-level guard where SQLite
expression-index support is limited — match the pattern used by prior
slots). Register slot 0081 in
[`MANIFEST.md`](../../../shared/db/migrations/MANIFEST.md) under Epic 27.

## 4. API contract

```
GET    /api/channels?library_id=&category=&enabled=   → [Channel + now_playing summary]
POST   /api/channels            {name,number,mode,mode_config,...}  → 201 Channel
GET    /api/channels/{id}                              → Channel (full)
PATCH  /api/channels/{id}        {partial}             → Channel  (regen if rule changed, D5)
DELETE /api/channels/{id}                              → 204 (cascade + teardown)
POST   /api/channels/reorder     [{id,number}]         → 200 (all-or-nothing, D3)
POST   /api/channels/{id}/logo   (multipart image)     → {logo_path} (re-encoded, D6)
```

`now_playing` on list/get is a cheap join to the current
`channel_programs` block ([Plan 27.4](plan-27-04-epg-generation.md)'s
"now" query), **not** a transcode.

## 5. Files to create / modify

**Create:** everything under `api/internal/handlers/channels/`, the
migration pair, DTO/contract doc.

**Modify:**
- `api/internal/router` — mount the channels routes behind `libraryacl`.
- `shared/db/migrations/MANIFEST.md` — register slot 0081.
- The thumbnail handler — export/reuse its re-encode helper for logos (if
  not already reusable).

## 6. Dependencies

- Slot 0001 (`libraries`/`videos`), slot 0072 (`libraryacl`), Story 7.14
  (smart-query evaluator, for `source_filter` validation reuse). No
  dependency on other Epic 27 stories — this is the root the rest build
  on. Regen enqueue (D5) targets [27.2](plan-27-02-program-scheduler.md)
  but degrades to "marked stale" if the scheduler isn't yet present.

## 7. Test strategy

Handler tests for each AC (validation, scope-uniqueness, slug stability,
reorder atomicity, logo re-encode, ACL). A migration test asserts the
partial unique indexes reject in-scope collisions and allow cross-scope
duplicates.
