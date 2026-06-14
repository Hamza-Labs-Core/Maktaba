# Plan 27.10 — Filler & bumper system — implementation

> Implementation plan for [story-27-10-filler-bumper-system.md](story-27-10-filler-bumper-system.md).
> Self-contained. Cross-links: filler items are consumed by the scheduler
> packer ([Plan 27.2](plan-27-02-program-scheduler.md), D6/AC7) and the
> live engine ([Plan 27.3](plan-27-03-live-stream-engine.md)); collapsed
> in the guide ([Plan 27.4](plan-27-04-epg-generation.md), AC10); managed
> in the admin UI ([Plan 27.8](plan-27-08-channel-admin-ui.md), AC6).
> Filler items are ordinary `videos` (ACL-bound). Writes slot 0085
> (`filler_pools`, `filler_items`, `channel_filler`).

---

## 0. Decisions

| #  | Decision | Rationale |
|----|----------|-----------|
| D1 | **Filler items are references to library `videos`**, not separate uploads. | Reuse the library, the probe duration, and (critically) the ACL — filler obeys the same access rules as any content. |
| D2 | **Pools are library-scoped; scope is `global` or per-channel-assigned.** | Story AC2/EC6 — keeps ACL simple; no cross-library global pool. |
| D3 | **Padding/fit/coalesce logic lives in the scheduler packer**, driven by the channel's `channel_filler.policy`. | Padding is a scheduling decision (27.2 owns contiguity); this story owns the *pools + policy*, not the packing. |
| D4 | **Auto "up next" is a discrete inserted block** (v1), referencing the next program's `title_snapshot`; an engine overlay is a documented future option. | Story EC7 — a block is simpler, shows in the timeline, and reuses the existing engine path. |
| D5 | **`kind` is advisory; fit uses the real probed duration.** | Story EC5 — a mis-tagged 2 h "bumper" simply won't fit a small gap. |
| D6 | **No-filler fallback is the packer's up-next-card / tail-replay.** | Story AC8 — filler is an enhancer, never required for a valid schedule. |

---

## 1. Package / file layout

```
api/internal/handlers/filler/          # pools + items + assignment CRUD (Go)
├── filler.go
├── repo.go
└── filler_test.go

pipeline/src/maktaba_pipeline/channels/filler.py   # load_filler() + selection used by packer (D3)
```

The pipeline `packer.py` ([27.2](plan-27-02-program-scheduler.md))
imports the selection helpers; this story adds the tables + the API + the
selection logic, 27.2 calls them.

## 2. Selection logic (`filler.py`, D3/D5)

```python
def fill_gap(gap_ms, pools, policy):
    items = eligible_items(pools)                 # global + channel pools, deduped (AC9)
    if policy.prefer_fit:
        items = sorted(items, key=lambda i: abs(i.duration_ms - gap_ms))  # D5 real duration
    chosen, used = [], 0
    while used < gap_ms and items and len(chosen) < policy.max_consecutive:
        it = pick_next(items, gap_ms - used)
        if it is None: break
        chosen.append(it); used += it.duration_ms
    # coalesce a long gap filled by short clips into a looping filler block (AC4a/EC8)
    return coalesce(chosen, gap_ms)               # may return a single looping block
    # caller (packer) falls back to up-next/tail-replay for any remainder (D6/AC8)
```

## 3. Data model — migration slot 0085

`shared/db/migrations/0085_channel_filler.sql` (+ `.sqlite.sql`):

```sql
-- +goose Up
-- +goose StatementBegin
-- Slot 0085 (Epic 27 / Story 27.10) — filler/bumper pools + items + channel assignment.
CREATE TABLE IF NOT EXISTS filler_pools (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id   UUID        NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    name         TEXT        NOT NULL,
    scope        TEXT        NOT NULL DEFAULT 'channel'
                             CHECK (scope IN ('global','channel')),  -- D2
    kind_default TEXT        NOT NULL DEFAULT 'interstitial',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS filler_items (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    pool_id     UUID        NOT NULL REFERENCES filler_pools(id) ON DELETE CASCADE,
    video_id    UUID        NOT NULL REFERENCES videos(id) ON DELETE CASCADE,  -- D1
    kind        TEXT        NOT NULL DEFAULT 'interstitial'
                            CHECK (kind IN ('station_id','bumper','interstitial','up_next')),
    duration_ms INTEGER     NOT NULL,                 -- from probe (advisory kind, D5)
    weight      INTEGER     NOT NULL DEFAULT 1,
    UNIQUE (pool_id, video_id)
);

CREATE TABLE IF NOT EXISTS channel_filler (
    channel_id  UUID        NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    pool_id     UUID        NOT NULL REFERENCES filler_pools(id) ON DELETE CASCADE,
    policy      JSONB       NOT NULL DEFAULT '{}'::jsonb,   -- max_consecutive, prefer_fit, auto_up_next
    PRIMARY KEY (channel_id, pool_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS filler_items_pool_idx ON filler_items (pool_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS channel_filler;
DROP TABLE IF EXISTS filler_items;
DROP TABLE IF EXISTS filler_pools;
-- +goose StatementEnd
```

`.sqlite.sql` per convention. Register slot 0085 in `MANIFEST.md`.

## 4. API contract (Go)

```
GET    /api/filler/pools?library_id=                 → pools
POST   /api/filler/pools          {name,scope,...}   → pool
PATCH  /api/filler/pools/{id}
DELETE /api/filler/pools/{id}
POST   /api/filler/pools/{id}/items  {video_ids,kind} → items (duration from probe)
DELETE /api/filler/items/{id}
PATCH  /api/channels/{id}/filler  {pools, policy}    → channel assignment + policy
```

All ACL-gated (filler videos obey their library ACL, D1).

## 5. Files to create / modify

**Create:** `api/internal/handlers/filler/*`,
`pipeline/.../channels/filler.py`, the migration pair.

**Modify:**
- `pipeline/.../channels/packer.py` ([27.2](plan-27-02-program-scheduler.md))
  — call `fill_gap` for sub-slot padding; fall back to up-next/tail-replay
  (D6).
- `api/internal/router` — mount filler routes.
- The admin UI ([27.8](plan-27-08-channel-admin-ui.md)) — `FillerManager`
  consumes these endpoints.
- `shared/db/migrations/MANIFEST.md` — register slot 0085.

## 6. Dependencies

- **27.1** (`channels`), **27.2** (packer integration — the consumer),
  slot 0001 (`videos`/`libraries`), slot 0072 (`libraryacl`). 27.4
  consumes the `kind` for guide collapsing.

## 7. Test strategy

Pool/item CRUD + scope, assignment + policy persistence, `fill_gap`
fit-preference + max-consecutive + coalesce, auto-up-next block insertion
(+ degrade to station-ID at horizon end), no-filler fallback, delete-video
repair, ACL scoping, global+channel pool composition/dedup.
