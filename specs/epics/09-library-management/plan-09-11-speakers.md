# Plan 9.11 — Speakers, voiceprints, naming, merge — implementation

> Implementation plan for [story-09-11-speakers.md](story-09-11-speakers.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: depends on the diarization stage from
> [Plan 3.9](../03-transcription/plan-03-09-diarization.md) (we consume
> diarization-emitted speaker turns and per-turn audio); reuses the
> per-library settings surface from
> [Story 9.1](story-09-01-library-config-schema.md); the REST handlers
> for naming and merge are formally owned by Epic 7
> [Story 7.14](../07-api/story-07-14-collections-tags-speakers-crud.md),
> but this plan implements the Go handler bodies because the schema and
> matching logic live here. Voiceprint extraction is owned by the
> Pipeline Service (Python); the matching, naming, and merge endpoints
> are owned by the API Service (Go).

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Voiceprint = 512-dimensional d-vector**, L2-normalized, stored as raw `BYTEA` (`512 × 4 = 2048` bytes per row), not as `pgvector` and not as JSONB. | Story edge case: "a `d-vector` of 512 floats = 2 KiB per speaker; … Stored as `BYTEA`". | `pgvector` would be appealing for ANN, but the per-library speaker count is bounded (story estimates ≤ 10k → 20 MiB) and a linear cosine scan in Go over a few thousand 512-d vectors is sub-millisecond — we don't need an index. `BYTEA` is portable, has no extension dependency, and `numpy.frombuffer` / Go `binary.LittleEndian` decode it directly. JSONB would 5×–10× the storage. |
| D2 | **Matching threshold: cosine *distance* ≤ 0.35**, configurable per library as `library.settings.diarization.speaker_match_threshold`. Lower distance = more similar; we match the closest existing voiceprint within threshold and *do not update* the matched speaker's voiceprint (avoid drift, AC-2). | Story AC-1: "cosine distance > `speaker_match_threshold`, default 0.35" + AC-2: "voiceprint is *not* updated (avoid drift)". | The story is explicit. Drift on every match would slowly walk a speaker's centroid towards their most-recent recordings and break the assumption that two unknown speakers can later be merged based on the kept speaker's *original* embedding (AC-4). Per-library override gives operators a knob for noisier libraries. |
| D3 | **Match writer is the Pipeline Service (Python).** The same diarization worker that emits per-segment speaker turns (Plan 3.9) computes the d-vector and resolves it against `speakers` *inside the same transaction* that writes `segment_speakers`. The Go API never inserts into `speakers` from a request handler — it only renames and merges. | Architecture §5.1 (Pipeline owns the audio path); Story AC-1/AC-2 (matching happens at segment commit time). | Splitting the match between services would require shipping the embedding model (or its scores) over a queue and re-doing the cosine inside Go, which is wasted work. Atomicity matters: a half-written `segment_speakers` with a missing `speakers` row would orphan the segment. Pipeline already holds the audio bytes; Pipeline owns the write. |
| D4 | **`name = NULL` is the canonical "unknown" marker.** The `unknown-{n}` rendering happens at *read time* in the Go API serializer; we do not store `"unknown-1"` in the DB. The integer suffix is computed per response as `dense_rank() over (order by created_at) FILTER (WHERE name IS NULL)`. | Story AC-1: "`name = "unknown-{n}"` rendered in the UI; n is the count of unknowns + 1" + edge case: "next new unknown takes the lowest free index". | If we stored the literal string, every merge or rename would have to renumber every other unknown speaker to keep the indices contiguous, which is a maintenance footgun. Computing it at read time is cheap (SQL window function over a typically small set) and naturally backfills holes after a merge. |
| D5 | **Merge is a Go handler in a single `BEGIN; ... COMMIT;` transaction.** `POST /api/speakers/merge {keep, drop}` rewrites every `segment_speakers.speaker_id = drop` to `speaker_id = keep`, then deletes the `drop` row. We do *not* recompute the kept voiceprint (AC-4). The endpoint is idempotent: if `drop` does not exist, return 404; if `keep == drop`, return 422. | Story AC-4: "as in Epic 7 Story 7.14 AC-4: `segment_speakers` rows are rewritten in one transaction. The voiceprint of the merged speaker is *not* recomputed". | Single transaction guarantees no segment ever points at a deleted speaker. Not recomputing keeps the merge cheap (`O(N)` UPDATE) and matches the story's invariant. The API service is the right home because the user triggers it from the UI; no audio is involved. |
| D6 | **Cross-library isolation enforced at the schema level via composite uniqueness on `(library_id, voiceprint_hash)`** — but the `voiceprint_hash` is *only used for de-dup of accidental double-inserts*, not for matching (matching is cosine-similarity-based). Cross-library merge is rejected at the handler level (422 `type: cross-library-merge`). | Story AC-5: "the same person watched across libraries is two separate `speakers` rows. No cross-library merge in v1". | Unique-on-hash prevents two pipeline workers racing on the *exact same* voiceprint bytes from creating duplicate rows; in practice this can't happen because matching first looks for an existing within-threshold row, but defence-in-depth is cheap. The handler-level cross-library check is the user-facing enforcement. |

If D2's threshold is set too aggressively (e.g. 0.5): false-positive
matches collapse distinct speakers; the user must merge-correct
post-hoc. If too tight (e.g. 0.20): the same speaker across two
recordings becomes two unknowns; the user must merge to fix. The
default 0.35 is a calibration point against the d-vector model used in
Plan 3.9.

If D4 (NULL = unknown) is rejected and we store literal `"unknown-N"`:
a single rename of `unknown-1` → `"Khalid"` is fine, but renaming
`unknown-3` and then *later* renaming `unknown-1` would require
renumbering `unknown-2`, `unknown-3`, etc. in the DB to remain
contiguous. The read-time approach has no such write amplification.

---

## 1. Architecture diagram — diarization → match → segment_speakers; Go handlers for rename/merge

```
   ┌──────────────────────────────────────────────────────────────────────┐
   │ Pipeline Service (Python) — diarization stage tail                   │
   │                                                                      │
   │  for each diarization turn (start_sec, end_sec, audio_chunk):        │
   │    embedding = d_vector_model.embed(audio_chunk)   # 512-d, L2-norm  │
   │                                                                      │
   │    BEGIN;                                                            │
   │     SELECT id, voiceprint                                            │
   │       FROM speakers                                                  │
   │      WHERE library_id = $library_id                                  │
   │      FOR UPDATE                                                      │
   │     -- Python computes cosine distance for each row                  │
   │     best = min(rows, key=lambda r: cos_dist(r.voiceprint, emb))      │
   │     if best.dist <= threshold:                                       │
   │       speaker_id = best.id                                           │
   │     else:                                                            │
   │       INSERT INTO speakers (id, library_id, name, voiceprint)        │
   │            VALUES (gen_random_uuid(), $library_id, NULL, $emb_bytes) │
   │            RETURNING id;                                             │
   │       speaker_id = new_id                                            │
   │     INSERT INTO segment_speakers                                     │
   │            (segment_id, speaker_id, confidence)                      │
   │       VALUES ($seg, speaker_id, 1 - best.dist);                      │
   │    COMMIT;                                                           │
   └──────────────────────────────┬───────────────────────────────────────┘
                                  │
                                  ▼
   ┌──────────────────────────────────────────────────────────────────────┐
   │ Postgres tables (this story owns)                                    │
   │  speakers (id, library_id, name NULL, voiceprint BYTEA, created_at)  │
   │  segment_speakers (segment_id, speaker_id, confidence)               │
   └──────────────────────────────┬───────────────────────────────────────┘
                                  │
                                  ▼
   ┌──────────────────────────────────────────────────────────────────────┐
   │ API Service (Go) — handlers (Story 7.14 surface; bodies live here)   │
   │                                                                      │
   │  GET    /api/libraries/{id}/speakers       — list with unknown-N     │
   │           rendering computed at read time (D4)                       │
   │  PATCH  /api/speakers/{id}    {name}       — rename; AC-3            │
   │  POST   /api/speakers/merge   {keep,drop}  — single-xact rewrite     │
   │           of segment_speakers; AC-4                                  │
   │                                                                      │
   │  Cross-library merge → 422 type=cross-library-merge (D6)             │
   └──────────────────────────────────────────────────────────────────────┘
```

The match path is read-only-ish (one INSERT into `speakers` per truly
new voice, one INSERT into `segment_speakers` per turn). The merge path
is a pure DB rewrite — no audio, no embedding model, no cross-service
fan-out.

---

## 2. Detailed implementation

### 2.1 Schema migration — `speakers` and `segment_speakers`

```sql
-- shared/db/migrations/0042_speakers.sql
BEGIN;

CREATE TABLE speakers (
    id          UUID PRIMARY KEY,                      -- v7 (so created order is rank order)
    library_id  UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    name        TEXT,                                  -- NULL = unknown (D4)
    voiceprint  BYTEA NOT NULL,                        -- 512 × float32 = 2048 bytes (D1)
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (octet_length(voiceprint) = 2048),
    CHECK (name IS NULL OR length(name) BETWEEN 1 AND 256)
);

-- Per-library lookup; the cosine match scans all rows for one library.
CREATE INDEX speakers_library ON speakers (library_id, created_at);

-- D6 defence-in-depth: prevent accidental duplicate inserts of the same
-- voiceprint bytes by parallel pipeline workers.
CREATE UNIQUE INDEX speakers_library_voiceprint
    ON speakers (library_id, md5(voiceprint));

CREATE TABLE segment_speakers (
    segment_id  UUID NOT NULL REFERENCES segments(id) ON DELETE CASCADE,
    speaker_id  UUID NOT NULL REFERENCES speakers(id) ON DELETE RESTRICT,
    confidence  REAL NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    PRIMARY KEY (segment_id, speaker_id)
);

CREATE INDEX segment_speakers_speaker ON segment_speakers (speaker_id);

COMMIT;
```

`ON DELETE RESTRICT` on `speakers` means the merge handler must rewrite
`segment_speakers` *before* deleting the dropped speaker — exactly what
AC-4 mandates.

### 2.2 Pipeline (Python) — voiceprint match-or-insert

```python
# pipeline/src/maktaba_pipeline/diarization/voiceprint.py
"""Voiceprint extraction + match-or-insert for one diarization turn.

Called from the diarization stage (Plan 3.9) after each turn is emitted.
Stores voiceprints as raw little-endian float32 bytes (D1).
"""
from __future__ import annotations
import logging
from dataclasses import dataclass
from uuid import UUID, uuid4

import numpy as np

log = logging.getLogger(__name__)

VOICEPRINT_DIM = 512
VOICEPRINT_BYTES = VOICEPRINT_DIM * 4  # float32


@dataclass(frozen=True)
class MatchResult:
    speaker_id: UUID
    is_new: bool
    distance: float                           # 0..2, but realistically 0..1 after L2 norm


def _pack(vec: np.ndarray) -> bytes:
    if vec.dtype != np.float32:
        vec = vec.astype(np.float32)
    if vec.shape != (VOICEPRINT_DIM,):
        raise ValueError(f"voiceprint must be ({VOICEPRINT_DIM},), got {vec.shape}")
    norm = float(np.linalg.norm(vec))
    if norm == 0.0:
        raise ValueError("voiceprint has zero norm")
    return (vec / norm).astype(np.float32).tobytes(order="C")


def _unpack(b: bytes) -> np.ndarray:
    if len(b) != VOICEPRINT_BYTES:
        raise ValueError(f"voiceprint bytes len = {len(b)}, expected {VOICEPRINT_BYTES}")
    return np.frombuffer(b, dtype=np.float32)


def cosine_distance(a: np.ndarray, b: np.ndarray) -> float:
    # Both are L2-normalized at insert time; this is just (1 - dot).
    return float(1.0 - np.dot(a, b))


async def match_or_insert(
    *, conn, library_id: UUID, voiceprint: np.ndarray, threshold: float,
) -> MatchResult:
    """Inside an OPEN asyncpg transaction.

    1. SELECT all voiceprints for the library, FOR UPDATE.
    2. Find best cosine distance.
    3. If best <= threshold → return that speaker (no voiceprint update — D2/AC-2).
    4. Else INSERT a new speaker with name=NULL (unknown — D4).
    """
    rows = await conn.fetch(
        "SELECT id, voiceprint FROM speakers WHERE library_id = $1 FOR UPDATE",
        library_id,
    )
    emb = voiceprint.astype(np.float32) / max(float(np.linalg.norm(voiceprint)), 1e-12)

    best_id: UUID | None = None
    best_dist = float("inf")
    for r in rows:
        d = cosine_distance(emb, _unpack(r["voiceprint"]))
        if d < best_dist:
            best_dist = d
            best_id = r["id"]

    if best_id is not None and best_dist <= threshold:
        return MatchResult(speaker_id=best_id, is_new=False, distance=best_dist)

    new_id = uuid4()
    packed = _pack(emb)
    try:
        await conn.execute(
            """
            INSERT INTO speakers (id, library_id, name, voiceprint)
            VALUES ($1, $2, NULL, $3)
            """,
            new_id, library_id, packed,
        )
    except Exception as e:
        # D6: defence-in-depth on (library_id, md5(voiceprint)). If we hit
        # the unique constraint, a parallel worker just inserted the
        # same voiceprint — re-read and use that row.
        log.warning("speakers insert race: %s", e)
        row = await conn.fetchrow(
            "SELECT id FROM speakers WHERE library_id=$1 AND md5(voiceprint)=md5($2::bytea)",
            library_id, packed,
        )
        if row is None:
            raise
        return MatchResult(speaker_id=row["id"], is_new=False, distance=0.0)

    return MatchResult(speaker_id=new_id, is_new=True, distance=best_dist)


async def attach_speaker_to_segment(
    *, conn, segment_id: UUID, speaker_id: UUID, distance: float,
) -> None:
    confidence = max(0.0, 1.0 - distance)
    await conn.execute(
        """
        INSERT INTO segment_speakers (segment_id, speaker_id, confidence)
        VALUES ($1, $2, $3)
        ON CONFLICT (segment_id, speaker_id) DO UPDATE
            SET confidence = GREATEST(segment_speakers.confidence, EXCLUDED.confidence)
        """,
        segment_id, speaker_id, confidence,
    )
```

### 2.3 sqlc query stubs — Go side

```sql
-- internal/db/queries/speakers.sql

-- name: ListSpeakersForLibrary :many
SELECT id, library_id, name, created_at,
       row_number() OVER (PARTITION BY library_id
                          ORDER BY created_at, id)
         FILTER (WHERE name IS NULL) AS unknown_rank
  FROM speakers
 WHERE library_id = $1
 ORDER BY created_at, id;

-- name: GetSpeaker :one
SELECT id, library_id, name FROM speakers WHERE id = $1;

-- name: RenameSpeaker :one
UPDATE speakers SET name = $2 WHERE id = $1
RETURNING id, library_id, name;

-- name: ReassignSegmentSpeakers :exec
UPDATE segment_speakers
   SET speaker_id = $1
 WHERE speaker_id = $2;

-- name: DeleteSpeaker :exec
DELETE FROM speakers WHERE id = $1;
```

The `unknown_rank` on `ListSpeakersForLibrary` implements D4: rendering
`unknown-{n}` is `if name == nil { fmt.Sprintf("unknown-%d", rank) }`.

### 2.4 Go handler — speakers list (D4 rendering)

```go
// api/internal/handlers/speakers/list.go
package speakers

import (
    "encoding/json"
    "fmt"
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"

    "maktaba/api/internal/db"
)

type speakerDTO struct {
    ID        uuid.UUID `json:"id"`
    Name      string    `json:"name"`           // "unknown-N" rendered for NULLs
    IsUnknown bool      `json:"is_unknown"`
}

func ListByLibrary(q *db.Queries) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        libraryID, err := uuid.Parse(chi.URLParam(r, "libraryID"))
        if err != nil {
            http.Error(w, "bad library id", http.StatusBadRequest)
            return
        }
        rows, err := q.ListSpeakersForLibrary(r.Context(), libraryID)
        if err != nil {
            http.Error(w, "list failed", http.StatusInternalServerError)
            return
        }
        out := make([]speakerDTO, 0, len(rows))
        for _, row := range rows {
            d := speakerDTO{ID: row.ID}
            if row.Name.Valid {
                d.Name = row.Name.String
            } else {
                d.Name = fmt.Sprintf("unknown-%d", row.UnknownRank.Int64)
                d.IsUnknown = true
            }
            out = append(out, d)
        }
        w.Header().Set("content-type", "application/json")
        _ = json.NewEncoder(w).Encode(map[string]any{"speakers": out})
    }
}
```

### 2.5 Go handler — rename (AC-3)

```go
// api/internal/handlers/speakers/rename.go
package speakers

import (
    "encoding/json"
    "errors"
    "net/http"
    "strings"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"

    "maktaba/api/internal/db"
)

type renameReq struct {
    Name string `json:"name"`
}

func Rename(q *db.Queries) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        id, err := uuid.Parse(chi.URLParam(r, "speakerID"))
        if err != nil {
            http.Error(w, "bad speaker id", http.StatusBadRequest)
            return
        }
        var req renameReq
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            http.Error(w, "bad json", http.StatusBadRequest)
            return
        }
        name := strings.TrimSpace(req.Name)
        if name == "" || len(name) > 256 {
            http.Error(w, "name must be 1..256 chars", http.StatusUnprocessableEntity)
            return
        }
        out, err := q.RenameSpeaker(r.Context(), db.RenameSpeakerParams{
            ID:   id,
            Name: pgx.Text(name),
        })
        if errors.Is(err, pgx.ErrNoRows) {
            http.Error(w, "not found", http.StatusNotFound)
            return
        }
        if err != nil {
            http.Error(w, "rename failed", http.StatusInternalServerError)
            return
        }
        w.Header().Set("content-type", "application/json")
        _ = json.NewEncoder(w).Encode(out)
    }
}
```

### 2.6 Go handler — merge (AC-4, D5, D6)

```go
// api/internal/handlers/speakers/merge.go
package speakers

import (
    "context"
    "encoding/json"
    "errors"
    "net/http"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"

    "maktaba/api/internal/db"
)

type mergeReq struct {
    Keep uuid.UUID `json:"keep"`
    Drop uuid.UUID `json:"drop"`
}

type mergeResp struct {
    Keep              uuid.UUID `json:"keep"`
    Drop              uuid.UUID `json:"drop"`
    SegmentsRewritten int64     `json:"segments_rewritten"`
}

func Merge(pool *pgxpool.Pool) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req mergeReq
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            http.Error(w, "bad json", http.StatusBadRequest)
            return
        }
        if req.Keep == req.Drop {
            jsonErr(w, http.StatusUnprocessableEntity,
                "self-merge", "keep and drop must differ")
            return
        }
        result, err := mergeInTxn(r.Context(), pool, req.Keep, req.Drop)
        switch {
        case errors.Is(err, errKeepNotFound), errors.Is(err, errDropNotFound):
            jsonErr(w, http.StatusNotFound, "speaker-not-found", err.Error())
            return
        case errors.Is(err, errCrossLibrary):
            jsonErr(w, http.StatusUnprocessableEntity, "cross-library-merge",
                "speakers belong to different libraries")
            return
        case err != nil:
            jsonErr(w, http.StatusInternalServerError, "merge-failed", err.Error())
            return
        }
        w.Header().Set("content-type", "application/json")
        _ = json.NewEncoder(w).Encode(result)
    }
}

var (
    errKeepNotFound = errors.New("keep speaker not found")
    errDropNotFound = errors.New("drop speaker not found")
    errCrossLibrary = errors.New("cross-library merge")
)

func mergeInTxn(ctx context.Context, pool *pgxpool.Pool,
    keep, drop uuid.UUID) (mergeResp, error) {

    var resp mergeResp
    tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
    if err != nil {
        return resp, err
    }
    defer tx.Rollback(ctx) //nolint:errcheck

    var keepLib, dropLib uuid.UUID
    if err := tx.QueryRow(ctx,
        "SELECT library_id FROM speakers WHERE id = $1 FOR UPDATE", keep,
    ).Scan(&keepLib); err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return resp, errKeepNotFound
        }
        return resp, err
    }
    if err := tx.QueryRow(ctx,
        "SELECT library_id FROM speakers WHERE id = $1 FOR UPDATE", drop,
    ).Scan(&dropLib); err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return resp, errDropNotFound
        }
        return resp, err
    }
    if keepLib != dropLib {
        return resp, errCrossLibrary
    }

    // AC-4: rewrite segment_speakers; segments that already point at `keep`
    // skip the row to avoid PK conflict on (segment_id, speaker_id).
    tag, err := tx.Exec(ctx, `
        UPDATE segment_speakers ss
           SET speaker_id = $1
         WHERE speaker_id = $2
           AND NOT EXISTS (
               SELECT 1 FROM segment_speakers ss2
                WHERE ss2.segment_id = ss.segment_id
                  AND ss2.speaker_id = $1)
    `, keep, drop)
    if err != nil {
        return resp, err
    }
    resp.SegmentsRewritten = tag.RowsAffected()

    // Clean up rows where both `keep` and `drop` already pointed at the same segment.
    if _, err := tx.Exec(ctx,
        "DELETE FROM segment_speakers WHERE speaker_id = $1", drop); err != nil {
        return resp, err
    }
    if _, err := tx.Exec(ctx, "DELETE FROM speakers WHERE id = $1", drop); err != nil {
        return resp, err
    }
    if err := tx.Commit(ctx); err != nil {
        return resp, err
    }
    resp.Keep = keep
    resp.Drop = drop
    return resp, nil
}

func jsonErr(w http.ResponseWriter, code int, kind, msg string) {
    w.Header().Set("content-type", "application/problem+json")
    w.WriteHeader(code)
    _ = json.NewEncoder(w).Encode(map[string]string{
        "type": kind, "title": http.StatusText(code), "detail": msg,
    })
}
```

### 2.7 Router wiring

```go
// api/internal/handlers/router.go (excerpt)
r.Route("/api", func(r chi.Router) {
    r.Get("/libraries/{libraryID}/speakers", speakers.ListByLibrary(queries))
    r.Patch("/speakers/{speakerID}", speakers.Rename(queries))
    r.Post("/speakers/merge", speakers.Merge(pool))
})
```

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `shared/db/migrations/0042_speakers.sql` | `speakers`, `segment_speakers` tables; indexes | `TestMigrationCreatesSpeakerTables` |
| 2 | `pipeline/src/maktaba_pipeline/diarization/voiceprint.py` | `match_or_insert`, `attach_speaker_to_segment`, `cosine_distance`, `_pack`, `_unpack` | `pipeline/.../tests/test_voiceprint.py` |
| 3 | `api/internal/db/queries/speakers.sql` | sqlc queries listed in §2.3 | sqlc generation smoke test |
| 4 | `api/internal/handlers/speakers/list.go` | `ListByLibrary` handler, `speakerDTO` | `TestListSpeakersUnknownRendering` |
| 5 | `api/internal/handlers/speakers/rename.go` | `Rename` handler | `TestRenameSpeaker`, `TestRenameValidation` |
| 6 | `api/internal/handlers/speakers/merge.go` | `Merge`, `mergeInTxn`, sentinel errors | `TestMergeRewritesSegments`, `TestMergeCrossLibrary`, `TestMergeSelf` |
| 7 | `api/internal/handlers/router.go` (extend) | route registrations | route table test |
| 8 | `pipeline/.../diarization/tests/test_voiceprint.py` | unit + integration tests | (n/a) |
| 9 | `api/internal/handlers/speakers/speakers_test.go` | Go integration tests | (n/a) |

---

## 4. Test cases (keyed to story ACs)

### 4.1 `TestMigrationCreatesSpeakerTables` — schema

```go
func TestMigrationCreatesSpeakerTables(t *testing.T) {
    db := freshDB(t)
    applyMigration(t, db, "0042_speakers.sql")

    // octet_length CHECK enforces 2048-byte voiceprints (D1).
    _, err := db.Exec(ctx, `INSERT INTO speakers (id, library_id, name, voiceprint)
        VALUES ($1, $2, NULL, $3)`, uuid.New(), libID, make([]byte, 1024))
    require.Error(t, err, "short voiceprint must violate octet_length CHECK")

    // segment_speakers FK with ON DELETE RESTRICT
    var hasFK bool
    require.NoError(t, db.QueryRow(ctx, `
        SELECT confdeltype = 'r' FROM pg_constraint
         WHERE conname LIKE 'segment_speakers_speaker%'`).Scan(&hasFK))
    assert.True(t, hasFK)
}
```

### 4.2 `test_voiceprint_match_or_insert` — AC-1, AC-2 (Python)

```python
async def test_unknown_voice_creates_new_speaker(db, library):
    """AC-1: distance > threshold → new speakers row with name=NULL."""
    emb = unit_vector(seed=1)
    async with db.acquire() as conn, conn.transaction():
        result = await voiceprint.match_or_insert(
            conn=conn, library_id=library.id, voiceprint=emb, threshold=0.35)
    assert result.is_new is True
    row = await db.fetchrow("SELECT name FROM speakers WHERE id=$1", result.speaker_id)
    assert row["name"] is None  # D4: unknown is name=NULL


async def test_matching_voice_does_not_update_voiceprint(db, library):
    """AC-2: in-threshold match returns existing id; voiceprint NOT updated."""
    emb1 = unit_vector(seed=1)
    async with db.acquire() as conn, conn.transaction():
        first = await voiceprint.match_or_insert(
            conn=conn, library_id=library.id, voiceprint=emb1, threshold=0.35)
    original = await db.fetchval("SELECT voiceprint FROM speakers WHERE id=$1", first.speaker_id)

    emb2 = emb1 + 1e-3 * unit_vector(seed=99)  # nearly identical
    async with db.acquire() as conn, conn.transaction():
        second = await voiceprint.match_or_insert(
            conn=conn, library_id=library.id, voiceprint=emb2, threshold=0.35)
    assert second.speaker_id == first.speaker_id
    after = await db.fetchval("SELECT voiceprint FROM speakers WHERE id=$1", first.speaker_id)
    assert bytes(after) == bytes(original), "voiceprint must not drift on match"
```

### 4.3 `test_three_voices_three_speakers_then_merge` — story integration

```python
async def test_100_segments_3_voices_yields_3_then_merge_to_2(db, library, segments_factory):
    """Integration from the story: 100 segments from 3 voices → 3 speakers; merge two → 2."""
    voices = [unit_vector(seed=k) for k in range(3)]
    segs = segments_factory.bulk(library_id=library.id, count=100)
    async with db.acquire() as conn:
        for i, seg in enumerate(segs):
            emb = voices[i % 3] + 1e-4 * unit_vector(seed=i)
            async with conn.transaction():
                m = await voiceprint.match_or_insert(
                    conn=conn, library_id=library.id, voiceprint=emb, threshold=0.35)
                await voiceprint.attach_speaker_to_segment(
                    conn=conn, segment_id=seg.id, speaker_id=m.speaker_id, distance=m.distance)

    n = await db.fetchval("SELECT COUNT(*) FROM speakers WHERE library_id=$1", library.id)
    assert n == 3
    # Merge: tested in Go side; Python test asserts pre-condition only.
```

### 4.4 `TestMergeRewritesSegments` — AC-4 (Go)

```go
func TestMergeRewritesSegmentSpeakersInOneTxn(t *testing.T) {
    api, db := setup(t)
    keep := mkSpeaker(t, db, lib)
    drop := mkSpeaker(t, db, lib)
    for _, seg := range mkSegments(t, db, 50) {
        attach(t, db, seg, drop, 0.9)
    }

    res := api.POST("/api/speakers/merge",
        map[string]uuid.UUID{"keep": keep, "drop": drop}).
        ExpectStatus(200).JSON()
    assert.Equal(t, int64(50), res["segments_rewritten"])

    var dropCount, keepCount int
    db.QueryRow(ctx, `SELECT COUNT(*) FROM segment_speakers WHERE speaker_id=$1`, drop).Scan(&dropCount)
    db.QueryRow(ctx, `SELECT COUNT(*) FROM segment_speakers WHERE speaker_id=$1`, keep).Scan(&keepCount)
    assert.Equal(t, 0, dropCount)
    assert.Equal(t, 50, keepCount)

    // Voiceprint of `keep` must be unchanged (AC-4).
    var voiceprint []byte
    db.QueryRow(ctx, `SELECT voiceprint FROM speakers WHERE id=$1`, keep).Scan(&voiceprint)
    assert.Equal(t, originalKeepVoiceprint, voiceprint)

    // `drop` is gone (RESTRICT enforced our cleanup).
    var n int
    db.QueryRow(ctx, `SELECT COUNT(*) FROM speakers WHERE id=$1`, drop).Scan(&n)
    assert.Equal(t, 0, n)
}
```

### 4.5 `TestRenameSpeaker` — AC-3

```go
func TestRenameSpeakerLabelsAllSegments(t *testing.T) {
    api, db := setup(t)
    sp := mkSpeaker(t, db, lib)
    attachToSegments(t, db, sp, 50)

    api.PATCH(fmt.Sprintf("/api/speakers/%s", sp), map[string]string{"name": "Khalid"}).
        ExpectStatus(200)

    var name string
    db.QueryRow(ctx, "SELECT name FROM speakers WHERE id=$1", sp).Scan(&name)
    assert.Equal(t, "Khalid", name)

    // Read API returns the new name for every prior segment by reference.
    rows := api.GET(fmt.Sprintf("/api/libraries/%s/speakers", lib)).JSON()["speakers"].([]any)
    found := false
    for _, r := range rows {
        m := r.(map[string]any)
        if m["id"] == sp.String() {
            assert.Equal(t, "Khalid", m["name"])
            assert.Equal(t, false, m["is_unknown"])
            found = true
        }
    }
    assert.True(t, found)
}
```

### 4.6 `TestListSpeakersUnknownRendering` — D4 / AC-1 rendering

```go
func TestUnknownSpeakersRenderWithDenseRank(t *testing.T) {
    api, db := setup(t)
    s1 := mkSpeaker(t, db, lib)        // unknown-1
    s2 := mkSpeakerWithName(t, db, lib, "Khalid")
    s3 := mkSpeaker(t, db, lib)        // unknown-2

    rows := api.GET(fmt.Sprintf("/api/libraries/%s/speakers", lib)).JSON()["speakers"].([]any)
    names := map[uuid.UUID]string{}
    for _, r := range rows {
        m := r.(map[string]any)
        id := uuid.MustParse(m["id"].(string))
        names[id] = m["name"].(string)
    }
    assert.Equal(t, "unknown-1", names[s1])
    assert.Equal(t, "Khalid",    names[s2])
    assert.Equal(t, "unknown-2", names[s3])
}
```

### 4.7 `TestMergeCrossLibrary` — AC-5 / D6

```go
func TestMergeAcrossLibrariesReturns422(t *testing.T) {
    api, db := setup(t)
    libA, libB := mkLib(t, db), mkLib(t, db)
    a := mkSpeaker(t, db, libA)
    b := mkSpeaker(t, db, libB)
    api.POST("/api/speakers/merge",
        map[string]uuid.UUID{"keep": a, "drop": b}).
        ExpectStatus(422).JSON()
}
```

---

## 5. Edge cases and how the plan handles each

| #   | Edge case | Handled by |
|-----|-----------|------------|
| E1  | **Diarization disabled mid-library.** Pipeline stops calling `match_or_insert`; existing `speakers` and `segment_speakers` rows are preserved (no cascade). New segments arrive without a `segment_speakers` row. The list endpoint still serves the historical speakers. | No code change; ACL check sits on the diarization stage entry, not on the schema. |
| E2  | **Voiceprint storage size.** 10k speakers × 2048 bytes = 20 MiB; well under any reasonable per-library budget. The full-table scan inside `match_or_insert` reads at most this much per turn. | D1; capacity test asserts < 100 ms for 10k voiceprints. |
| E3  | **Two unknowns merged.** After merge, `unknown_rank` is recomputed at read time over the remaining `name IS NULL` rows; the `unknown-N` indices stay contiguous (D4). | `TestUnknownIndicesAfterMerge`. |
| E4  | **Concurrent pipeline workers race on the same library_id.** `FOR UPDATE` on the `SELECT` inside `match_or_insert` serializes them. The unique index `speakers_library_voiceprint` is a fallback if two workers somehow compute identical normalized vectors. | D6; tested via `test_concurrent_match_no_duplicate`. |
| E5  | **Self-merge** (`keep == drop`). Handler returns 422 `type=self-merge` before opening a transaction. | D5 test `TestMergeSelf`. |
| E6  | **Merging when both speakers point at the same segment.** The UPDATE's `NOT EXISTS` clause skips the duplicate; the subsequent DELETE removes the dropped row's now-orphaned (or already-collided) entries. Segment ends up with one row pointing at `keep`. | §2.6 SQL; `TestMergeWhenBothPointAtSameSegment`. |
| E7  | **Voiceprint with zero norm** (silent audio). `_pack` raises; the diarization caller treats it as "no diarization data for this turn" and skips. | §2.2 `_pack`. |
| E8  | **Library deleted while merge is pending.** `ON DELETE CASCADE` from `libraries` to `speakers` removes both rows; the in-flight merge transaction sees 0 rows and returns 404. No corruption. | Schema; `TestMergeAfterLibraryDelete`. |
| E9  | **Renaming to an empty / whitespace-only name.** Handler returns 422 before touching the DB. | §2.5; `TestRenameValidation`. |
| E10 | **Renaming to a name already used by another speaker in the same library.** Allowed in v1 (no uniqueness on `(library_id, name)`); the user can disambiguate by ID. v1.1 may add a soft warning. | Documented; not enforced. |
| E11 | **Voiceprint dim drift** (model swap from 512-d to 256-d). The `octet_length(voiceprint) = 2048` CHECK rejects the new bytes; the diarization stage must run a re-embed migration before the model swap lands. | Schema CHECK; story 3.9 owns the model-swap path. |

---

## 6. Acceptance checklist

- [ ] **A1** `shared/db/migrations/0042_speakers.sql` creates `speakers (id UUID PK, library_id UUID FK ON DELETE CASCADE, name TEXT NULL, voiceprint BYTEA NOT NULL CHECK octet_length=2048, created_at)` and `segment_speakers (segment_id UUID FK CASCADE, speaker_id UUID FK RESTRICT, confidence REAL CHECK [0,1], PK)`. (`TestMigrationCreatesSpeakerTables`)
- [ ] **A2** `pipeline/.../diarization/voiceprint.py` extracts a 512-d L2-normalized d-vector per diarization turn and packs it as little-endian float32 bytes. (`test_pack_unpack_roundtrip`)
- [ ] **A3** `match_or_insert` selects all voiceprints for the library `FOR UPDATE`, picks the closest by cosine distance, and either returns that speaker (if `dist <= threshold`) or inserts a new row with `name=NULL`. (`test_unknown_voice_creates_new_speaker`)
- [ ] **A4** A matched speaker's voiceprint is **never** rewritten by subsequent matches (drift prevention, AC-2). (`test_matching_voice_does_not_update_voiceprint`)
- [ ] **A5** `attach_speaker_to_segment` writes `segment_speakers (segment_id, speaker_id, confidence = 1 - distance)` inside the same transaction as the match. (`test_match_and_attach_atomic`)
- [ ] **A6** `GET /api/libraries/{id}/speakers` renders `name=NULL` rows as `unknown-{n}` where `n` is the per-library `dense_rank()` over `created_at` filtered to NULL names. (`TestUnknownSpeakersRenderWithDenseRank`)
- [ ] **A7** `PATCH /api/speakers/{id} {name}` sets the name (1..256 chars after trim); rejects empty/whitespace with 422. Read path immediately reflects the new name for every prior segment. (`TestRenameSpeakerLabelsAllSegments`, `TestRenameValidation`)
- [ ] **A8** `POST /api/speakers/merge {keep, drop}` runs in a single SERIALIZABLE transaction: rewrites every `segment_speakers.speaker_id = drop` to `keep` (skipping any segment that already references `keep`), deletes the dropped speaker, and returns `{keep, drop, segments_rewritten}`. (`TestMergeRewritesSegmentSpeakersInOneTxn`)
- [ ] **A9** Merge **does not** recompute `keep`'s voiceprint; the bytes on disk are byte-identical before and after. (Same test, byte comparison.)
- [ ] **A10** Cross-library merge returns 422 `type=cross-library-merge`. Self-merge returns 422 `type=self-merge`. Missing speaker returns 404. (`TestMergeCrossLibrary`, `TestMergeSelf`, `TestMergeNotFound`)
- [ ] **A11** After a merge of two unknowns, the remaining `unknown-{n}` indices are contiguous (`unknown-1`, `unknown-2`, …) on the next read. (`TestUnknownIndicesAfterMerge`)
- [ ] **A12** Diarization disabled mid-library does not delete or modify any existing rows; new segments simply have no `segment_speakers` entry. (Operational; covered by `test_disable_diarization_preserves_data`.)
