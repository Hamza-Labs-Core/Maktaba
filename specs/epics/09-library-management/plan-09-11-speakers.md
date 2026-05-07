# Implementation Plan — Story 9.11 Speakers, Voiceprints, Naming, Merge

> Companion to [story-09-11-speakers.md](story-09-11-speakers.md).
> The story states *what* and *why*; this plan states *how*.
> Builds on the diarization stage (Pipeline §5.2 / Epic 3 Story 3.9)
> and Story 9.1's per-library `diarize` flag.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Voiceprint vector | 192-dim x-vector (pyannote `wespeaker-voxceleb-resnet34`) — the existing diarization model already produces this. Stored as `BYTEA` (192 × 4 = 768 bytes). |
| Threshold | Cosine distance > 0.35 → new speaker; else match. Configurable per library (`speaker_match_threshold`); validated in Story 9.1 schema. |
| Tables | `speakers (id BIGSERIAL PK, library_id UUID FK, name, voiceprint BYTEA, updated_at)`; `segment_speakers (segment_id BIGINT FK transcript_segments(id), speaker_id BIGINT FK speakers(id), confidence REAL, PRIMARY KEY (segment_id, speaker_id))` — canonical from architecture (`speakers.id BIGSERIAL`, `transcript_segments.id BIGINT`). Speaker IDs are `int64` end-to-end (Go and Python), not UUID. |
| Naming convention | `name = NULL` is the storage canonical; the API renders unknowns as `"unknown-{n}"` where `n` is the index of unknowns ordered by `created_at`. |
| Merge | `POST /api/speakers/merge {keep, drop}` (Epic 7 Story 7.14) → one tx that re-points `segment_speakers.speaker_id` from `drop` to `keep`, then DELETEs the dropped row. ON CONFLICT (segment_id, speaker_id) DO NOTHING handles the case where the segment was already linked to both. |
| Cross-library | No global merge in v1; speakers scope is per library. The `library_id` column is the boundary. |
| Out of scope | Diarization itself; the actual x-vector extraction; the merge endpoint's auth check (Epic 10); the UI. |

## 1. Architecture diagram

```
   Diarization stage (Epic 3 Story 3.9) emits per-segment turns with x-vectors.
        ↓
   speaker_matcher.commit_segment(library_id, segment_id, voiceprint)
        ├─ existing = SELECT id, voiceprint FROM speakers
        │              WHERE library_id = $1
        ├─ best_id, best_dist = nearest_speaker(voiceprint, existing)
        ├─ if best_dist > settings.speaker_match_threshold:
        │     # new unknown speaker
        │     speaker_id = INSERT speakers (library_id, name=NULL, voiceprint)
        │ else:
        │     speaker_id = best_id   # voiceprint NOT updated (avoid drift)
        ├─ INSERT INTO segment_speakers (segment_id, speaker_id, confidence)
        │   VALUES ($1, $2, $3)
        │   ON CONFLICT DO NOTHING
        └─ publish(VIDEO_SPEAKERS_UPDATED, {video_id})  # constant per 09-01 §2.5

   Merge endpoint:
     POST /api/speakers/merge {keep_id, drop_id}
        BEGIN TX
          UPDATE segment_speakers SET speaker_id=keep_id
           WHERE speaker_id=drop_id
             AND NOT EXISTS (SELECT 1 FROM segment_speakers
                              WHERE segment_id = segment_speakers.segment_id
                                AND speaker_id = keep_id)
          DELETE FROM segment_speakers WHERE speaker_id=drop_id
          DELETE FROM speakers WHERE id=drop_id
        COMMIT
        publish(LIBRARY_SPEAKERS_MERGED, {keep_id, drop_id, library_id})  # 09-01 §2.5

   Rename endpoint (Epic 7 Story 7.14):
     PATCH /api/speakers/{id}  body: {name}
        UPDATE speakers SET name=$2, updated_at=now() WHERE id=$1
        publish(SPEAKER_RENAMED, {speaker_id, name})  # constant per 09-01 §2.5
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `pipeline/src/maktaba_pipeline/speakers/__init__.py` | Re-exports. |
| `pipeline/src/maktaba_pipeline/speakers/matcher.py` | `nearest_speaker`, `match_or_create`. |
| `pipeline/src/maktaba_pipeline/speakers/commit.py` | `commit_segment_with_speaker(...)` — used by diarization. |
| `pipeline/src/maktaba_pipeline/speakers/voiceprint.py` | Pack/unpack BYTEA, normalize, cosine_distance. |
| `pipeline/tests/speakers/test_matcher.py` | Unit tests per §6.1. |
| `pipeline/tests/speakers/test_commit.py` | Integration tests per §6.2. |
| `api/internal/handlers/speakers/merge.go` | Merge endpoint. |
| `api/internal/handlers/speakers/rename.go` | Rename endpoint. |
| `api/internal/handlers/speakers/list.go` | List + render unknowns helper. |
| `api/internal/handlers/speakers/*_test.go` | Handler tests per §6.3. |
| `shared/db/migrations/0039_speakers.sql` | Tables + indexes. |
| `shared/db/queries/speakers.sql` | sqlc input. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/diarize/commit.py` | Per turn, call `commit_segment_with_speaker(...)`. |
| `pipeline/src/maktaba_pipeline/db/pubsub.py` | The canonical channel-name registry (09-01 §2.5) already declares `SPEAKER_RENAMED`, `LIBRARY_SPEAKERS_MERGED`, `VIDEO_SPEAKERS_UPDATED`. This plan only consumes them. |
| `api/internal/router.go` | Wire `/api/speakers/merge`, `/api/speakers/{id}` PATCH, `/api/libraries/{id}/speakers` GET. |
| `specs/epics/09-library-management/README.md` | Tick story 9.11. |

### 2.3 Type definitions

```python
# pipeline/src/maktaba_pipeline/speakers/voiceprint.py
import numpy as np

VOICEPRINT_DIM = 192            # pyannote wespeaker-voxceleb-resnet34
VOICEPRINT_BYTES = VOICEPRINT_DIM * 4

def pack(v: np.ndarray) -> bytes: ...
def unpack(b: bytes) -> np.ndarray: ...

def cosine_distance(a: np.ndarray, b: np.ndarray) -> float:
    # Both unit-normalized; dist = 1 - dot. Range [0, 2]; threshold ~0.35.
    return 1.0 - float(np.dot(a, b))
```

```python
# pipeline/src/maktaba_pipeline/speakers/matcher.py
from dataclasses import dataclass
import numpy as np

@dataclass(slots=True, frozen=True)
class MatchResult:
    speaker_id: int            # speakers.id is BIGSERIAL (int64)
    distance: float            # 0.0 if newly created (synthetic)
    is_new: bool
```

## 3. Database migration

`shared/db/migrations/0039_speakers.sql`:

```sql
-- +goose Up
-- +goose StatementBegin

-- Canonical from architecture: speakers.id BIGSERIAL,
-- transcript_segments.id BIGINT. Foreign keys must match by type.
CREATE TABLE speakers (
    id              BIGSERIAL PRIMARY KEY,
    library_id      UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    name            TEXT,
    voiceprint      BYTEA NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT speakers_voiceprint_len_chk CHECK (octet_length(voiceprint) = 768),
    CONSTRAINT speakers_name_len_chk
        CHECK (name IS NULL OR (char_length(name) BETWEEN 1 AND 80))
);

CREATE INDEX speakers_library_lookup
    ON speakers (library_id, created_at);

CREATE TABLE segment_speakers (
    segment_id      BIGINT NOT NULL REFERENCES transcript_segments(id) ON DELETE CASCADE,
    speaker_id      BIGINT NOT NULL REFERENCES speakers(id) ON DELETE CASCADE,
    confidence      REAL NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    PRIMARY KEY (segment_id, speaker_id)
);

CREATE INDEX segment_speakers_speaker
    ON segment_speakers (speaker_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS segment_speakers;
DROP TABLE IF EXISTS speakers;
-- +goose StatementEnd
```

SQLite variant: `INTEGER PRIMARY KEY AUTOINCREMENT` for `speakers.id`,
`INTEGER` for `segment_speakers.{segment,speaker}_id` matching the
SQLite analogue of `BIGSERIAL`/`BIGINT`; `BLOB` for voiceprint;
constraints translate directly.

`shared/db/queries/speakers.sql`:

```sql
-- name: ListSpeakersInLibrary :many
SELECT id, name, voiceprint, created_at,
       (SELECT COUNT(*) FROM segment_speakers ss WHERE ss.speaker_id = s.id)
         AS segment_count
  FROM speakers s
 WHERE library_id = $1
 ORDER BY created_at;

-- name: InsertSpeaker :one
-- speakers.id is BIGSERIAL — let the DB auto-assign; do NOT pass id.
INSERT INTO speakers (library_id, voiceprint, name)
VALUES ($1, $2, $3)
RETURNING id;

-- name: RenameSpeaker :exec
UPDATE speakers SET name = $2, updated_at = now() WHERE id = $1;

-- name: MergeSpeakers :exec
WITH moved AS (
    UPDATE segment_speakers
       SET speaker_id = $1
     WHERE speaker_id = $2
       AND NOT EXISTS (
           SELECT 1 FROM segment_speakers ss2
            WHERE ss2.segment_id = segment_speakers.segment_id
              AND ss2.speaker_id = $1
       )
    RETURNING segment_id
)
DELETE FROM segment_speakers WHERE speaker_id = $2;

-- name: DeleteSpeaker :exec
DELETE FROM speakers WHERE id = $1;
```

## 4. Code scaffolding

### 4.1 Matcher

```python
# pipeline/src/maktaba_pipeline/speakers/matcher.py
import numpy as np

from .voiceprint import unpack, cosine_distance


async def match_or_create(db, *, library_id, voiceprint: np.ndarray,
                          threshold: float) -> MatchResult:
    rows = await db.fetch(
        "SELECT id, voiceprint FROM speakers WHERE library_id=$1",
        library_id,
    )
    if rows:
        existing = np.stack([unpack(r["voiceprint"]) for r in rows])
        dots = existing @ voiceprint  # both unit-norm
        best = int(np.argmax(dots))
        best_dist = 1.0 - float(dots[best])
        if best_dist <= threshold:
            return MatchResult(speaker_id=int(rows[best]["id"]),
                               distance=best_dist, is_new=False)

    # speakers.id is BIGSERIAL — let the DB assign and return.
    new_id = await db.fetchval(
        "INSERT INTO speakers (library_id, voiceprint, name) "
        "VALUES ($1, $2, NULL) RETURNING id",
        library_id, pack(voiceprint),
    )
    return MatchResult(speaker_id=int(new_id), distance=0.0, is_new=True)
```

### 4.2 Per-segment commit

```python
# pipeline/src/maktaba_pipeline/speakers/commit.py
async def commit_segment_with_speaker(
    db, *,
    library_id, segment_id,
    voiceprint: np.ndarray, confidence: float,
    threshold: float,
) -> MatchResult:
    res = await match_or_create(db, library_id=library_id,
                                voiceprint=voiceprint,
                                threshold=threshold)
    await db.execute(
        "INSERT INTO segment_speakers (segment_id, speaker_id, confidence) "
        "VALUES ($1, $2, $3) "
        "ON CONFLICT (segment_id, speaker_id) DO NOTHING",
        segment_id, res.speaker_id, confidence,
    )
    return res
```

### 4.3 Go endpoints

```go
// api/internal/handlers/speakers/list.go
type SpeakerView struct {
    ID           int64     `json:"id"`             // speakers.id BIGSERIAL
    Name         string    `json:"name"`           // rendered (unknown-N if null)
    Raw          *string   `json:"raw_name"`       // null if not user-set
    SegmentCount int       `json:"segment_count"`
    CreatedAt    time.Time `json:"created_at"`
}

func ListHandler(d *handlers.Deps) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        libID, _ := uuid.Parse(chi.URLParam(r, "id"))
        rows, err := d.Queries.ListSpeakersInLibrary(r.Context(), libID)
        if err != nil { handlers.WriteError(w, 500, "db-error", err.Error()); return }

        unknownIdx := 0
        out := make([]SpeakerView, 0, len(rows))
        for _, s := range rows {
            v := SpeakerView{
                ID: s.ID,                        // int64 (BIGSERIAL)
                SegmentCount: int(s.SegmentCount),
                CreatedAt: s.CreatedAt,
            }
            if s.Name.Valid {
                v.Name = s.Name.String
                v.Raw = &s.Name.String
            } else {
                unknownIdx++
                v.Name = fmt.Sprintf("unknown-%d", unknownIdx)
            }
            out = append(out, v)
        }
        handlers.WriteJSON(w, 200, out)
    }
}
```

```go
// api/internal/handlers/speakers/merge.go
// Speaker IDs are BIGSERIAL int64 — JSON-encoded as numbers.
type MergeRequest struct {
    Keep int64 `json:"keep"`
    Drop int64 `json:"drop"`
}

func MergeHandler(d *handlers.Deps) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req MergeRequest
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            handlers.WriteError(w, 400, "bad-body", err.Error()); return
        }
        if req.Keep == req.Drop {
            handlers.WriteError(w, 422, "merge-self",
                "keep and drop must differ"); return
        }
        // Cross-library protection: same library_id.
        keep, _ := d.Queries.GetSpeaker(r.Context(), req.Keep)
        drop, _ := d.Queries.GetSpeaker(r.Context(), req.Drop)
        if keep.LibraryID != drop.LibraryID {
            handlers.WriteError(w, 422, "merge-cross-library", ""); return
        }

        tx, _ := d.Pool.Begin(r.Context())
        defer tx.Rollback(r.Context())
        q := d.Queries.WithTx(tx)
        if err := q.MergeSpeakers(r.Context(), db.MergeSpeakersParams{
            Keep: req.Keep, Drop: req.Drop,
        }); err != nil {
            handlers.WriteError(w, 500, "merge-failed", err.Error()); return
        }
        if err := q.DeleteSpeaker(r.Context(), req.Drop); err != nil {
            handlers.WriteError(w, 500, "drop-delete-failed", err.Error()); return
        }
        if err := tx.Commit(r.Context()); err != nil {
            handlers.WriteError(w, 500, "commit-failed", err.Error()); return
        }
        d.WS.Broadcast("library:speakers:merged", map[string]any{
            "keep": req.Keep, "drop": req.Drop, "library_id": keep.LibraryID,
        })
        w.WriteHeader(204)
    }
}
```

## 5. Test plan

### 5.1 Matcher unit tests (`test_matcher.py`)

| Test | What it pins |
|---|---|
| `test_first_voiceprint_creates_unknown_speaker` | Empty `speakers` → `is_new=True`; `name IS NULL` in DB. AC-1. |
| `test_close_voiceprint_matches_existing` | Insert speaker with vector V; call with V + tiny noise (dist < 0.1) → returns existing id; `is_new=False`. AC-2. |
| `test_far_voiceprint_creates_new` | Insert speaker with V; call with orthogonal vector (dist ≈ 1.0) → new speaker. AC-1. |
| `test_match_does_not_update_existing_voiceprint` | After match, the existing speaker's stored bytes are unchanged. AC-2 ("voiceprint not updated"). |
| `test_threshold_respected` | Pre-state with V; call with vector at dist 0.34 (just under default 0.35) → match; at 0.36 → new. |
| `test_per_library_isolation` | Same V in libA and libB → two distinct speaker rows; matcher in libA never returns libB's id. AC-5. |

### 5.2 Commit integration (`test_commit.py`)

| Test | What it pins |
|---|---|
| `test_segment_speakers_row_written` | After commit, `(segment_id, speaker_id, confidence)` exists with the input confidence. |
| `test_concurrent_commits_for_same_segment` | Two diarization workers commit the same segment → ON CONFLICT DO NOTHING leaves a single row; `confidence` is the first writer's. |
| `test_unknown_count_advances` | Insert 3 unknown speakers → list endpoint renders `unknown-1`, `unknown-2`, `unknown-3` ordered by `created_at`. |
| `test_unknown_count_can_decrease_after_merge` | Merge `unknown-2` into `unknown-1` → next list shows `unknown-1`, `unknown-2` (formerly unknown-3). Edge case from story. |

### 5.3 Endpoint tests

`test_merge.go`:

| Test | What it pins |
|---|---|
| `TestMerge_HappyPath` | Speakers A and B in library L, with 50 and 30 segments → merge → A has 80 (or 80-overlap) segments; B is gone; `library:speakers:merged` broadcast fires. |
| `TestMerge_VoiceprintNotRecomputed` | After merge, A's voiceprint bytes are unchanged. AC-4. |
| `TestMerge_CrossLibraryReject` | A in L1, B in L2 → 422 `merge-cross-library`. AC-5. |
| `TestMerge_SelfReject` | keep == drop → 422 `merge-self`. |
| `TestMerge_OverlapSegmentsHandled` | Both A and B linked to segment S → after merge, exactly one `(S, A)` row; the `(S, B)` is gone. |

`test_rename.go`:

| Test | What it pins |
|---|---|
| `TestRename_PersistsAndBroadcasts` | PATCH `{name: "Imam Tariq"}` → DB row updated; UI sees the change in the next list call. AC-3. |
| `TestRename_LengthCap` | 200-char body → 422 `name-too-long`. (Schema CHECK 1..80.) |
| `TestRename_EmptyResetsToNull` | `{name: null}` → `speakers.name = NULL`; rendered as `unknown-N` again. |
| `TestRename_BroadcastsOnSuccess` | `speaker:renamed` event with the new name. |

### 5.4 Storage-size sanity

`test_voiceprint_storage`:

| Test | What it pins |
|---|---|
| `test_voiceprint_size_192_floats` | `octet_length(voiceprint) = 768` enforced by CHECK; off-size raises. |
| `test_10k_speakers_under_20mib` | Synthesize 10k rows; SUM(octet_length) < 20 MiB. From story. |

## 6. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Diarization disabled mid-library | Existing `speakers` and `segment_speakers` rows preserved; new segments simply do not write to `segment_speakers`. No data loss. | Documented; tested by `test_diarize_off_preserves_speakers` |
| Voiceprint storage size | 768 B per speaker × 10k speakers ≈ 8 MB; well below 20 MiB even with overhead. | `test_10k_speakers_under_20mib` |
| Two unknown speakers later turn out to be the same | Merge (AC-4); the count of unknowns can go down; the next new unknown takes the lowest free index because rendering is `unknown-{enumerate}` over `created_at`. | `test_unknown_count_can_decrease_after_merge` |
| User renames to empty string | API rejects (CHECK 1..80); to clear, the user sends `null`. | `TestRename_EmptyResetsToNull` |
| Concurrent rename + merge | Last-writer wins; the merge tx targets `keep`, so the rename to `keep` survives; a rename to `drop` between merge and commit is rolled back by FK CASCADE on the row. | Race covered by tx isolation; test deferred. |
| Race: two diarize workers commit the same segment turn (e.g., retry) | ON CONFLICT DO NOTHING ensures a single row; `match_or_create`'s INSERT race is bounded by the lack of a unique key on `voiceprint` — which is intentional. We accept that two near-identical runs may create two unknown speakers; the user merges them. | `test_concurrent_commits_for_same_segment` |
| Threshold tuning per library | Surfaced as `speaker_match_threshold` in `library_settings.schema.json` (Story 9.1 schema's reserved future-key slot accepts it; this story adds the explicit schema entry). | Schema test |

## 7. Configuration

| Key | Default | Effect |
|---|---|---|
| `speaker_match_threshold` (per library) | 0.35 | Cosine distance cutoff. |
| `voiceprint_dim` (constant) | 192 | Fixed by the diarization model. |
| `library_settings.diarize` | False | When True, the matcher runs per turn. |

## 8. Dependencies

| Dep | Source | Why |
|---|---|---|
| Diarization x-vector output | Pipeline Story 3.9 | Source of `voiceprint`. |
| Story 9.1 `EffectiveLibrarySettings` | required | `diarize`, `speaker_match_threshold`. |
| `numpy` | already pinned | Vector ops. |

No new heavy deps.

## 9. Acceptance checklist

**Code**
- [ ] `pipeline/src/maktaba_pipeline/speakers/` package created.
- [ ] `commit_segment_with_speaker` invoked once per diarization turn.
- [ ] Go endpoints for list, merge, rename wired in `router.go`.

**Migration**
- [ ] `speakers` and `segment_speakers` tables created with constraints and indexes.

**Behaviour (story acceptance criteria)**
- [ ] AC-1: voiceprints above threshold create a new unknown row.
- [ ] AC-2: matched speakers do not get their voiceprint overwritten.
- [ ] AC-3: PATCH name persists; future segments display the new name.
- [ ] AC-4: merge re-points links and deletes the dropped row in one tx.
- [ ] AC-5: speakers across libraries never collide.

**Observability**
- [ ] Counter `speaker_match_total{outcome=new|matched}`.
- [ ] Counter `speaker_merges_total`.
- [ ] Histogram `speaker_match_distance{library_id}`.

**Docs**
- [ ] `specs/epics/09-library-management/README.md` ticks story 9.11.
- [ ] API docs explain the unknown-N rendering and the merge contract.
