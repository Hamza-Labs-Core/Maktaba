# Plan 26.6 — Enrichment review UI — implementation

> Implementation plan for [story-26-06-enrichment-ui.md](story-26-06-enrichment-ui.md).
> Self-contained. Cross-links: reads candidates from
> [Plan 26.5](plan-26-05-web-metadata-enrichment.md)
> (`media_metadata_enrichment`); promotes fields to `videos` with
> provenance; uses the library ACL
> ([`api/internal/handlers/libraryacl`](../../../api/internal/handlers/libraryacl));
> series batch-accept uses [Plan 26.3](plan-26-03-series-detection.md).
> Writes slot 0078 (`enrichment_decisions`, `media_field_provenance`).
> Endpoints in Go (`api/`), UI in React (`web/`).

---

## 0. Decisions

| #  | Decision | Rationale |
|----|----------|-----------|
| D1 | **Provenance table is the source of truth for "who owns this field".** `media_field_provenance(video_id, field, origin, set_at, prev_value)` where `origin ∈ {user, enrichment, parser}`. | Story AC: never overwrite user edits; every applied field reversible. A per-field provenance row is the minimal structure that supports both. |
| D2 | **Accept = transactional field promotion that skips user-owned fields.** Read candidate.mapped, for each mappable video field: if provenance.origin=='user' skip; else write value + upsert provenance(origin='enrichment', prev_value=old). | Story AC: applied vs skipped enumerated; reversible. |
| D3 | **Revert restores `prev_value` from provenance**, flipping the field back and clearing the enrichment provenance. | Story AC: "Revert to original". |
| D4 | **Dismiss is per-video (optionally per-`external_id`)**, stored on the candidate (`is_dismissed`) — no separate table; mirrors recommendation dismissals. | Lightweight; re-enrich/manual-search clears it. |
| D5 | **Series batch-accept iterates episodes, per-episode provenance**, returns a summary; skips episodes with a newer decision. | Story AC: per-episode protection; collisions reported. |
| D6 | **Optimistic concurrency on `videos.updated_at`.** Accept carries the version it saw; a stale accept returns 409. | Story AC: concurrent-edit safety. |
| D7 | **Auto-accept threshold pre-applies above-threshold matches but still records provenance** (so it's undoable). Default off. | Story AC: auto-applied is revertible. |

---

## 1. Data model — migration slot 0078

```sql
-- Slot 0078 (Epic 26 / Story 26.6)
CREATE TABLE IF NOT EXISTS media_field_provenance (
    video_id   UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    field      TEXT NOT NULL,           -- 'title' | 'description' | 'poster_path' | ...
    origin     TEXT NOT NULL CHECK (origin IN ('user','enrichment','parser')),
    prev_value TEXT,                     -- for revert (D3)
    source_id  TEXT,                     -- external_id when origin='enrichment'
    set_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (video_id, field)
);

CREATE TABLE IF NOT EXISTS enrichment_decisions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id    UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    external_id TEXT,
    action      TEXT NOT NULL CHECK (action IN ('accept','dismiss','revert','auto_accept')),
    actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    applied     JSONB NOT NULL DEFAULT '[]'::jsonb,   -- fields written
    skipped     JSONB NOT NULL DEFAULT '[]'::jsonb,   -- protected fields
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

`media_metadata_enrichment` (slot 0077) gains `is_dismissed BOOLEAN
DEFAULT false` (a small ALTER in this slot, D4).

> **Hooking user edits into provenance.** The existing video-edit
> endpoint (`PATCH /api/videos/{id}`) must upsert
> `media_field_provenance(origin='user')` for each edited field. This is
> the one change to existing write paths; without it the "never
> overwrite user edits" guarantee can't be enforced. Added in this slot's
> handler change, not a schema change.

## 2. API endpoints (Go, `api/internal/handlers/enrichment/`)

```
GET  /api/videos/{id}/enrichment            → ranked candidates + per-field {would_change, protected}
POST /api/videos/{id}/enrichment/accept     → {external_id, version} ; applies (D2,D6)
POST /api/videos/{id}/enrichment/dismiss    → {external_id?} (D4)
POST /api/videos/{id}/enrichment/search     → {query, year?, provider?} → fresh candidates (no apply)
POST /api/videos/{id}/enrichment/revert     → {field?} restores prev_value (D3)
POST /api/series/{id}/enrichment/accept-all → per-episode apply summary (D5)
POST /api/videos/{id}/enrichment/reenrich   → enqueue enrich job (delegates to 26.7)
```

Accept handler (D2/D6):

```go
func accept(video, externalID, version) (AcceptResult, error) {
    if video.UpdatedAt != version { return _, ErrConflict /*409*/ }
    cand := loadCandidate(video.ID, externalID)
    applied, skipped := []string{}, []string{}
    tx.Begin()
    for field, val := range mappableFields(cand.Mapped) {
        if provOrigin(video.ID, field) == "user" { skipped = append(skipped, field); continue }
        prev := currentValue(video, field)
        writeVideoField(tx, video.ID, field, val)
        upsertProvenance(tx, video.ID, field, "enrichment", prev, externalID)
        applied = append(applied, field)
    }
    markAccepted(tx, video.ID, externalID)        // is_accepted=true
    writeDecision(tx, video.ID, externalID, "accept", applied, skipped)
    tx.Commit()
    return AcceptResult{Applied: applied, Skipped: skipped}, nil
}
```

Manual search delegates to the 26.5 service (rate-limited, cached) and
writes fresh candidates without applying. Reenrich delegates to 26.7.

## 3. Web UI (React, `web/`)

- **VideoDetail enrichment panel** (`web/src/pages/VideoDetail.tsx`): when
  candidates exist, render the top candidate as "We found this might be
  **X (year)** — NN% match" with a **current vs. proposed diff** table;
  protected fields badged "won't change". Controls: Accept, Dismiss,
  Search manually (opens a small search form), and per-field Revert after
  accept.
- **Library review queue** (`web/src/pages/EnrichmentReview.tsx`, new
  route `/libraries/:id/review`): list of videos with pending
  high-confidence matches; keyboard-driven (`a` accept, `x` dismiss,
  `j/k` navigate); shows auto-applied items with an Undo affordance.
- **Series page batch-accept**: an "Accept all episode matches" button
  (26.10 series page) calling `accept-all`, rendering the per-episode
  summary.
- Uses design-system `Card`, `Button`, `Chip`, `Drawer`, `Toast`,
  `Skeleton`, `EmptyState`.

## 4. Files to create / modify

**Create:** `api/internal/handlers/enrichment/*`, migration pair,
`web/src/pages/EnrichmentReview.tsx` + test, enrichment panel component
+ test.

**Modify:**
- `api/internal/handlers/videos` — the `PATCH` edit path writes
  `origin='user'` provenance (§1 note).
- `api/internal/router` — register enrichment routes.
- `web/src/pages/VideoDetail.tsx` — mount the enrichment panel.
- `MANIFEST.md` — slot 0078 + the `is_dismissed` ALTER note on slot 0077.

## 5. Dependencies

- **26.5** candidates + the manual-search/refresh service.
- **26.3** series (batch-accept iterates episodes).
- **Epic 10** library ACL + auth (actor on decisions).
- **Epic 17** design system.

## 6. Test strategy

Go: accept applies+skips correctly, records provenance, 409 on stale
version, ACL 403 for read-only, revert restores `prev_value`,
batch-accept per-episode summary, auto-accept-threshold behaviour.
React: panel renders diff + controls; review queue keyboard flow;
empty/no-candidate states. The "user edit flips provenance to user-owned
then accept skips it" path is an integration test spanning the videos
PATCH + enrichment accept.
