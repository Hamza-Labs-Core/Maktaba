# Plan 5.5 — Incremental indexing — implementation

> Implementation plan for [story-05-05-incremental-indexing.md](story-05-05-incremental-indexing.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: subscribes to the `segments.committed`
> NOTIFY channel produced by [Plan 3.6](../03-transcription/plan-03-06-segment-commit.md);
> calls the unit chunker from [Story 5.1](story-05-01-unit-chunking.md);
> upserts FTS rows per [Story 5.2](story-05-02-fts-tsvector.md); and
> upserts vector rows per [Story 5.3](story-05-03-chroma-vector.md) at
> the post-transcribe stage. Reads pause/resume state owned by
> [Plan 3.7](../03-transcription/plan-03-07-pause-resume.md). The active
> transcript invariant is owned by
> [Story 3.5](../03-transcription/story-03-05-backend-registry.md).

---

## 0. Decisions and departures from `architecture.md` and the story

| # | Decision | Source | Rationale |
|---|----------|--------|-----------|
| D1 | The live indexer runs as an **in-process asyncio task** inside the same Pipeline worker that runs the transcribe stage, **not** as a separate OS process. It is started once at worker boot (`maktaba_pipeline.runner.boot()`) and lives for the worker's lifetime; it is not spawned per-job. | Refines the story (which is silent on process topology). | A separate process needs its own asyncpg pool, model registry, and supervision; the live indexer's only inputs are the DB and the unit chunker (no GPU, no Chroma during live phase per AC). Embedding it in-process keeps the pause/cancel observability cheap (the transcribe loop and live indexer share `pause_requested` reads via the same DB pool) and avoids a second process to monitor. The chunker is pure Python and Postgres I/O — no contention with the STT loop. |
| D2 | NOTIFY events are **debounced into 500 ms windows per (transcript_id, library_id)**, not processed one-by-one. A burst of 100 segments committed in 2 s collapses into ~4 chunker invocations. | Refines the story (which says "indexes only the units whose `unit.indexed_at IS NULL`" but doesn't dictate batching). | At 20 segments/min steady-state the savings are small, but a fast backend (faster-whisper-int8 on a fast Mac) emits 5–15 segments/sec during catch-up after a pause; without debouncing the indexer would re-chunk the same growing tail dozens of times. 500 ms is below the human "live" perception threshold (the AC says "within 10 s" so we have 20× headroom). The window is per-transcript so two videos transcribing in parallel don't block each other. |
| D3 | The unit "claim" query is **`UPDATE … RETURNING`** on `transcript_units WHERE indexed_at IS NULL` against a freshly-rechunked unit set, **not** a `SELECT … FOR UPDATE` loop. Re-chunking is idempotent on `(transcript_id, seq)` (it `INSERT … ON CONFLICT (transcript_id, seq) DO UPDATE`) so a unit that's been re-emitted by the chunker after a pause/resume is updated in place. | Refines Story 5.1 acceptance "the partial index `transcript_units_indexed_at_null` supports the claim query" (which is silent on locking). | A `FOR UPDATE` loop over units works but doesn't prevent two indexer ticks from racing if D2's debounce ever fires twice (e.g., a NOTIFY arrives mid-chunk). Idempotent upsert + `indexed_at IS NULL` claim makes the indexer safely re-entrant: a duplicate run is a no-op. |
| D4 | Catch-up after worker outage uses **`transcripts.last_indexed_segment_seq`** (a new INTEGER NOT NULL DEFAULT 0 column added in this story's migration), **not** the segment timestamp. On boot, the indexer scans `transcript_segments WHERE transcript_id = T AND seq > last_indexed_segment_seq` per non-archived transcript and triggers a chunk pass before subscribing to NOTIFY. | Refines Story 5.5 edge case "Crash during live indexing. `unit.indexed_at IS NULL` is the resume key". | The story's resume key (`indexed_at IS NULL`) tells us *which units* still need indexing, but not which *segments* still need chunking into units. After a crash, the unit corresponding to the last 30 s of segments may not exist yet — we need a watermark on segments, not units. `last_indexed_segment_seq` is monotonic and lock-free to read; segment timestamps would tie us to wall-clock and complicate replays. |
| D5 | Vector index failures during the **post-transcribe** `index` stage do **not** fail the FTS write or roll back unit `indexed_at`. The indexer commits the FTS update + sets `indexed_at = now()`, then attempts the Chroma upsert in a separate step; on Chroma failure the unit is **moved to a per-library dead-letter queue** (`vector_index_dead_letter` table) for retry by the nightly reaper. | Refines Story 5.3 (Chroma failures) and the AC ordering. | Chroma is single-writer and an external dependency; it can be down or rebuilding its HNSW index for ~30 s while the rest of the pipeline is fine. Coupling FTS to vector means a Chroma blip blocks all live-search reads. The DLQ pattern means search degrades to FTS-only for the affected units (acceptable by AC since live indexing is FTS-only anyway) until the reaper drains it. |
| D6 | Lock granularity is **per `(transcript_id)`** via `pg_advisory_xact_lock(hashtextextended('idx:' || transcript_id::text, 0))`, not global. The advisory lock is acquired inside the unit-write transaction and released on commit/rollback. | Refines the story (silent on locking). | A global lock would serialize indexers across all transcribes, killing live-search throughput on a multi-video workload. Per-transcript locking is enough because the only contention is the chunker overwriting the tail unit when a new segment lands. Advisory locks are cheap (no row scan), and `xact_lock` flavor frees automatically on transaction end. |
| D7 | The live indexer **does not** write to Chroma. Vector embeddings are batched and written in the post-transcribe `index` stage (which runs after `transcribe.state == done`), where it can amortize embedding cost over all units of the transcript at once. | Story acceptance: "writes them to FTS only (Chroma is deferred to the post-transcribe stage to amortize embedding cost)". | E5-large embedding throughput is GPU-limited at ~200 units/sec batched; per-unit live embedding would burn GPU time on a unit that gets *replaced* on the next segment commit (because the chunker re-emits the open tail unit as new segments extend it). Batch-at-end means each unit gets embedded exactly once, and no live GPU contention with the STT model. |
| D8 | The pause/cancel observation in the live indexer is a **DB read of `processing_jobs.state` and `pause_requested`** done at the start of each debounce-fire — not a NOTIFY/in-memory signal. If the parent transcribe job state ∈ {`paused`, `paused-pending`, `cancelled`}, the indexer **drops the buffered events** for that transcript and skips this fire. It re-arms on the next NOTIFY. | Story acceptance: "stops chunking as soon as it observes `processing_jobs.state ∈ {paused, paused-pending, cancelled}`". | Reading the job state from the DB is the source of truth shared with the API and the transcribe worker (which set it). A separate in-memory pause signal would have a window where the indexer keeps chunking after the API set `paused-pending`. The DB read is one indexed lookup per fire (~0.5 ms) and the debounce already coalesces fires, so the cost is negligible. |
| D9 | Re-processing a video (model upgrade, new active transcript per Story 3.5) deletes the **old transcript's units** in the same transaction that flips `is_active = false`, via a deferred trigger — and the indexer's **old-transcript NOTIFY subscriptions** are torn down by a `transcripts.is_active = false` second NOTIFY channel (`transcripts.active_changed`) that the indexer also listens on. | Story acceptance: "Re-processing … the old transcript's units are deleted from FTS and Chroma in the same transaction that flips `is_active = false`". | The CASCADE on `transcripts → transcript_units → transcripts_fts` (via the FTS trigger) handles FTS automatically. Chroma needs an explicit `collection.delete(where={"transcript_id": OLD.id})` issued from a Python hook subscribed to `transcripts.active_changed`. Doing this purely from a SQL trigger is impossible (Chroma is external). |
| D10 | The NOTIFY payload schema is fixed at `{"transcript_id": str(uuid), "video_id": str(uuid), "library_id": str(uuid), "last_segment_end_sec": float, "seq": int}` — adding `library_id` and `video_id` to the Plan 3.6 baseline so the indexer can debounce-key without an extra DB lookup per event. | Refines Plan 3.6 D5 (whose payload is `{transcript_id, last_segment_end_sec, seq}`). | The indexer must dispatch debounce buckets by `(transcript_id)` and look up the library/video for the chunker call; carrying both IDs in the payload saves one round-trip per NOTIFY. The Plan 3.6 trigger function is updated to populate them via a `JOIN` on `transcripts → videos` (one statement, one lookup at trigger time, paid by the writer not the reader). |

If D5 is rejected (vector failure rolls back FTS), §2 changes (the
indexer wraps both writes in one transaction and the DLQ table is
removed) and the story's "search returns live partial results"
acceptance is at risk during Chroma outages. Correctness is otherwise
unaffected.

If D2 is rejected (no debounce), §2.5 changes (the dispatcher fires
per-NOTIFY) and the catch-up burst test in §4 fails the latency budget
on hosts with slow chunker performance.

---

## 1. Architecture diagram — incremental indexing flow

```
┌───────────────────────────────────────────────────────────────────────┐
│  Pipeline worker process boot (runner.boot)                           │
│   ┌───────────────────────────────────────────────────────────────┐   │
│   │  IndexerSupervisor.start()                                    │   │
│   │   1. Open dedicated asyncpg connection (long-lived)           │   │
│   │   2. CATCH-UP PASS — for every transcript with                │   │
│   │      last_indexed_segment_seq < MAX(transcript_segments.seq)  │   │
│   │      and is_active = true and parent_job.state != cancelled:  │   │
│   │        → enqueue debounce event (forces immediate fire)       │   │
│   │   3. LISTEN segments.committed                                │   │
│   │   4. LISTEN transcripts.active_changed                        │   │
│   │   5. spawn Dispatcher() task                                  │   │
│   └───────────────────────────────────────────────────────────────┘   │
└───────────────────────────────────────────────────────────────────────┘
                              │
                              ▼  (asyncpg add_listener callbacks)
┌───────────────────────────────────────────────────────────────────────┐
│  IndexerDispatcher                                                    │
│   - asyncio.Queue of NotifyEvent                                      │
│   - debounce_buffer: dict[transcript_id, _Bucket(latest_seq, due_at)] │
│   - tick loop @ 100 Hz:                                               │
│       for tid, bucket in due_buckets():                               │
│         spawn IncrementalIndexJob(tid, bucket).run()                  │
│         buckets.pop(tid)                                              │
│   - on segments.committed event:                                      │
│       merge into bucket; due_at = now() + 500 ms (D2)                 │
│   - on transcripts.active_changed event:                              │
│       drop pending bucket for old transcript                          │
│       enqueue Chroma deletion task for old transcript_id              │
└───────────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌───────────────────────────────────────────────────────────────────────┐
│  IncrementalIndexJob.run(transcript_id, latest_seq)                   │
│   1. Read parent job state — if paused / cancelled (D8) → return.    │
│   2. BEGIN; pg_advisory_xact_lock(hash('idx:'||transcript_id))  (D6) │
│   3. SELECT seq, start_sec, end_sec, text                             │
│        FROM transcript_segments                                       │
│       WHERE transcript_id = $1                                        │
│         AND seq > (SELECT last_indexed_segment_seq                    │
│                      FROM transcripts WHERE id = $1)                  │
│       ORDER BY seq;                                                   │
│   4. recompute_units(transcript_id, fetched_segments)                 │
│        → produces UnitDelta { unsealed_tail, new_units_to_persist }   │
│   5. INSERT INTO transcript_units (...)                               │
│        ON CONFLICT (transcript_id, seq) DO UPDATE SET                 │
│          text = EXCLUDED.text, end_sec = EXCLUDED.end_sec,            │
│          segment_ids = EXCLUDED.segment_ids,                          │
│          indexed_at = NULL  (forces FTS re-trigger)                   │
│   6. (Postgres) FTS is GENERATED column on transcript_units.tsv —     │
│      no extra write needed.                                           │
│      (SQLite) DELETE+INSERT on transcripts_fts via existing trigger.  │
│   7. UPDATE transcript_units SET indexed_at = now()                   │
│        WHERE transcript_id = $1 AND indexed_at IS NULL;               │
│   8. UPDATE transcripts                                               │
│        SET last_indexed_segment_seq = max_seq_we_chunked              │
│      WHERE id = $1                                                    │
│        AND last_indexed_segment_seq < max_seq_we_chunked;             │
│   9. COMMIT;  (advisory lock released)                                │
│  10. (live phase: NO Chroma write — D7)                               │
└───────────────────────────────────────────────────────────────────────┘

┌───────────────────────────────────────────────────────────────────────┐
│  Post-transcribe `index` stage (runs once when job.state → done)      │
│   - Same recompute_units pass for any tail (caught up by claim query) │
│   - Then: Chroma upsert for all units with indexed_at_in_chroma IS    │
│     NULL (a separate bool column added in this migration), batch 64.  │
│   - On Chroma error → INSERT INTO vector_index_dead_letter(unit_id,   │
│     attempts, last_error). Reaper retries every 5 minutes.            │
└───────────────────────────────────────────────────────────────────────┘
```

---

## 2. Detailed implementation

### 2.1 Package layout — Python (Pipeline Service)

```
pipeline/src/maktaba_pipeline/
├── search/
│   ├── __init__.py             # public surface: IndexerSupervisor
│   ├── chunker.py              # owned by Story 5.1 — re-used here
│   ├── fts/
│   │   └── writer.py           # owned by Story 5.2 — re-used here
│   ├── chroma/
│   │   └── writer.py           # owned by Story 5.3 — used by post-stage only
│   ├── live/
│   │   ├── __init__.py
│   │   ├── supervisor.py       # IndexerSupervisor — boot + listener
│   │   ├── dispatcher.py       # IndexerDispatcher — debounce + tick loop
│   │   ├── job.py              # IncrementalIndexJob — the SQL hot path
│   │   ├── recompute.py        # recompute_units(transcript_id, segments)
│   │   ├── catch_up.py         # boot-time catch-up scan
│   │   ├── notify.py           # NotifyEvent dataclass + parser
│   │   ├── deadletter.py       # vector DLQ helpers (used by index stage)
│   │   ├── errors.py           # IndexerError, IndexerPaused, IndexerStopped
│   │   └── tests/
│   │       ├── conftest.py
│   │       ├── test_supervisor_catch_up.py
│   │       ├── test_dispatcher_debounce.py
│   │       ├── test_dispatcher_pause_drop.py
│   │       ├── test_job_idempotent_upsert.py
│   │       ├── test_job_advisory_lock.py
│   │       ├── test_job_watermark_advances.py
│   │       ├── test_recompute_handles_tail_unit.py
│   │       ├── test_listener_disconnect_reconnect.py
│   │       ├── test_active_transcript_swap.py
│   │       └── test_vector_dlq_writes.py
│   └── stages/
│       └── index.py            # post-transcribe stage; calls Chroma + DLQ
└── runner.py                   # boot hook: spawn IndexerSupervisor.start()
```

### 2.2 Schema migration — `0021_incremental_indexing.sql`

```sql
-- 0021_incremental_indexing.sql
-- Owns:
--   - transcripts.last_indexed_segment_seq
--   - transcript_units.indexed_at_in_chroma
--   - vector_index_dead_letter table
--   - transcripts.active_changed NOTIFY trigger
--   - segments_committed payload upgrade (adds video_id, library_id) (D10)
-- Dependencies: 0007 transcripts (Story 3.5),
--               0011 segment_commit_function (Plan 3.6),
--               000X transcript_units (Story 5.1).
-- Idempotent.

BEGIN;

-- D4: catch-up watermark on the transcript row.
ALTER TABLE transcripts
    ADD COLUMN IF NOT EXISTS last_indexed_segment_seq INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS transcripts_active_for_indexer
    ON transcripts (is_active, last_indexed_segment_seq)
    WHERE is_active = true;

-- D7: vector-side watermark on the unit row.
ALTER TABLE transcript_units
    ADD COLUMN IF NOT EXISTS indexed_at_in_chroma TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS transcript_units_pending_chroma
    ON transcript_units (transcript_id)
    WHERE indexed_at_in_chroma IS NULL;

-- D5: dead-letter table for vector failures.
CREATE TABLE IF NOT EXISTS vector_index_dead_letter (
    id           BIGSERIAL PRIMARY KEY,
    unit_id      BIGINT NOT NULL REFERENCES transcript_units(id) ON DELETE CASCADE,
    library_id   UUID   NOT NULL,
    transcript_id BIGINT NOT NULL,
    attempts     INT    NOT NULL DEFAULT 0,
    next_retry_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error   TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (unit_id)
);

CREATE INDEX IF NOT EXISTS vector_dlq_due
    ON vector_index_dead_letter (next_retry_at);

-- D9: NOTIFY when a transcript's is_active flips. Picked up by the
-- IndexerSupervisor to tear down old buckets and trigger Chroma deletion.
CREATE OR REPLACE FUNCTION transcripts_active_changed_notify()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF (TG_OP = 'UPDATE' AND NEW.is_active IS DISTINCT FROM OLD.is_active) THEN
        PERFORM pg_notify(
            'transcripts.active_changed',
            json_build_object(
                'transcript_id', NEW.id,
                'video_id',      NEW.video_id,
                'library_id',    (SELECT library_id FROM videos WHERE id = NEW.video_id),
                'is_active',     NEW.is_active
            )::text
        );
    END IF;
    RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS trg_transcripts_active_changed ON transcripts;
CREATE TRIGGER trg_transcripts_active_changed
    AFTER UPDATE ON transcripts
    FOR EACH ROW
    EXECUTE FUNCTION transcripts_active_changed_notify();

-- D10: upgrade segments_committed payload to include video_id + library_id.
-- The Plan 3.6 function is replaced (CREATE OR REPLACE — name unchanged).
CREATE OR REPLACE FUNCTION segments_notify_committed()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    v_video_id   UUID;
    v_library_id UUID;
BEGIN
    SELECT t.video_id, v.library_id
      INTO v_video_id, v_library_id
      FROM transcripts t
      JOIN videos v ON v.id = t.video_id
     WHERE t.id = NEW.transcript_id;

    PERFORM pg_notify(
        'segments.committed',
        json_build_object(
            'transcript_id',         NEW.transcript_id,
            'video_id',              v_video_id,
            'library_id',            v_library_id,
            'last_segment_end_sec',  NEW.end_sec,
            'seq',                   NEW.seq
        )::text
    );
    RETURN NEW;
END $$;
-- The trigger trg_segments_committed itself was created in 0011; CREATE OR
-- REPLACE FUNCTION above swaps the body in place without re-creating it.

COMMIT;
```

SQLite shim (lives in `shared/db/migrations/sqlite/0021_*.sql`):
- `last_indexed_segment_seq` and `indexed_at_in_chroma` columns are
  added by `ALTER TABLE`.
- `vector_index_dead_letter` table mirrors the schema (no UUID type;
  use TEXT).
- The `pg_notify` substitute uses the `segment_notify_log` polling
  pattern from Plan 3.6 D5; a parallel `transcript_active_change_log`
  table is added for active-flip events. The polling listener
  (`maktaba_pipeline.search.live.notify.PollingListener`) tails both
  tables every `LIVE_INDEX_POLL_INTERVAL_SEC` (default 5 s, per the
  story's SQLite acceptance).

### 2.3 `notify.py` — event parsing

```python
"""NotifyEvent — typed parser for segments.committed and transcripts.active_changed."""
from __future__ import annotations
import json
import logging
from dataclasses import dataclass
from enum import Enum
from typing import Any
from uuid import UUID

log = logging.getLogger(__name__)


class NotifyKind(Enum):
    SEGMENT_COMMITTED = "segments.committed"
    ACTIVE_CHANGED = "transcripts.active_changed"


@dataclass(frozen=True)
class NotifyEvent:
    kind: NotifyKind
    transcript_id: int
    video_id: UUID
    library_id: UUID
    # SEGMENT_COMMITTED only:
    last_segment_end_sec: float | None = None
    seq: int | None = None
    # ACTIVE_CHANGED only:
    is_active: bool | None = None

    @classmethod
    def parse(cls, channel: str, payload: str) -> "NotifyEvent | None":
        try:
            kind = NotifyKind(channel)
        except ValueError:
            log.warning("notify_unknown_channel", extra={"channel": channel})
            return None
        try:
            data: dict[str, Any] = json.loads(payload)
        except (ValueError, TypeError):
            log.warning(
                "notify_payload_unparseable",
                extra={"channel": channel, "payload_head": payload[:120]})
            return None

        try:
            transcript_id = int(data["transcript_id"])
            video_id = UUID(str(data["video_id"]))
            library_id = UUID(str(data["library_id"]))
        except (KeyError, TypeError, ValueError) as e:
            log.warning(
                "notify_payload_missing_fields",
                extra={"channel": channel, "err": str(e)})
            return None

        if kind == NotifyKind.SEGMENT_COMMITTED:
            return cls(
                kind=kind,
                transcript_id=transcript_id,
                video_id=video_id,
                library_id=library_id,
                last_segment_end_sec=float(data.get("last_segment_end_sec", 0.0)),
                seq=int(data["seq"]),
            )
        return cls(
            kind=kind,
            transcript_id=transcript_id,
            video_id=video_id,
            library_id=library_id,
            is_active=bool(data.get("is_active", False)),
        )
```

### 2.4 `recompute.py` — the chunker invocation

```python
"""Re-chunk the segments past the watermark into search units.

This is the only place that calls Story 5.1's chunker from the live
indexer. Returns a UnitDelta describing what to write to the DB.
"""
from __future__ import annotations
from dataclasses import dataclass
from typing import Sequence

from maktaba_pipeline.search.chunker import chunk_segments_to_units, Unit

# How many seconds back from the watermark to RE-READ on every fire,
# so a unit straddling the watermark gets recomputed (sentence boundaries
# may shift when a new segment lands).
TAIL_OVERLAP_SEC = 30.0


@dataclass(frozen=True)
class SegmentRow:
    id: int
    seq: int
    start_sec: float
    end_sec: float
    text: str


@dataclass(frozen=True)
class UnitDelta:
    units: tuple[Unit, ...]    # all units in the recomputed range, in seq order
    starting_seq: int          # the seq value of units[0]
    max_segment_seq: int       # the highest segment seq considered (watermark)


def recompute_units(
    *,
    transcript_id: int,
    fetched_segments: Sequence[SegmentRow],
    language: str,
    existing_max_unit_seq: int,
) -> UnitDelta:
    """Run the chunker over the fetched segments. Returns the units to upsert.

    `existing_max_unit_seq` is the highest unit seq currently in the DB for
    this transcript; new units start at `existing_max_unit_seq + 1` minus
    any tail-overlap units we're recomputing. The rule:

      - If the lowest segment seq we fetched corresponds to a segment
        already inside an existing unit (because the indexer fetched
        TAIL_OVERLAP_SEC of overlap), we recompute starting at the
        unit covering that segment, REPLACING it.
      - The first new-or-replaced unit's seq is the seq of the unit
        we're replacing (or existing_max_unit_seq + 1 if there's no
        overlap).
    """
    if not fetched_segments:
        return UnitDelta(units=(), starting_seq=existing_max_unit_seq + 1,
                         max_segment_seq=0)

    units = chunk_segments_to_units(
        [s.__dict__ for s in fetched_segments],
        language=language,
        target_chars=200,
        hard_cap_chars=400,
    )
    max_seg_seq = max(s.seq for s in fetched_segments)
    # The starting_seq is computed by the writer (it knows the existing
    # max in the DB after taking the advisory lock). We pass through here.
    return UnitDelta(
        units=tuple(units),
        starting_seq=existing_max_unit_seq + 1,
        max_segment_seq=max_seg_seq,
    )
```

### 2.5 `dispatcher.py` — debounce + tick loop (D2)

```python
"""IndexerDispatcher — coalesce NOTIFY events and fire indexer jobs."""
from __future__ import annotations
import asyncio
import logging
import time
from dataclasses import dataclass

from maktaba_pipeline.search.live.notify import NotifyEvent, NotifyKind
from maktaba_pipeline.search.live.job import IncrementalIndexJob
from maktaba_pipeline.search.live.errors import IndexerPaused, IndexerStopped

log = logging.getLogger(__name__)

DEBOUNCE_WINDOW_SEC = 0.5      # D2
TICK_INTERVAL_SEC = 0.01       # 100 Hz
MAX_INFLIGHT_PER_TRANSCRIPT = 1


@dataclass
class _Bucket:
    transcript_id: int
    video_id: object
    library_id: object
    latest_seq: int
    first_event_at: float
    due_at: float


class IndexerDispatcher:
    def __init__(self, db_pool, *, chroma_writer):
        self._pool = db_pool
        self._chroma_writer = chroma_writer
        self._buckets: dict[int, _Bucket] = {}
        self._inflight: set[int] = set()
        self._stop = asyncio.Event()
        self._tick_task: asyncio.Task | None = None

    def submit(self, event: NotifyEvent) -> None:
        """Called by the listener callback. Lock-free; never awaits."""
        if event.kind == NotifyKind.ACTIVE_CHANGED:
            # Drop pending bucket; a separate task handles cleanup
            # (Chroma deletion of OLD transcript) — see supervisor.
            self._buckets.pop(event.transcript_id, None)
            return
        now = time.monotonic()
        bucket = self._buckets.get(event.transcript_id)
        if bucket is None:
            self._buckets[event.transcript_id] = _Bucket(
                transcript_id=event.transcript_id,
                video_id=event.video_id,
                library_id=event.library_id,
                latest_seq=event.seq or 0,
                first_event_at=now,
                due_at=now + DEBOUNCE_WINDOW_SEC,
            )
        else:
            bucket.latest_seq = max(bucket.latest_seq, event.seq or 0)
            bucket.due_at = now + DEBOUNCE_WINDOW_SEC

    def submit_force(self, transcript_id: int, video_id, library_id) -> None:
        """Catch-up entry — bypass debounce; fire immediately on next tick."""
        now = time.monotonic()
        self._buckets[transcript_id] = _Bucket(
            transcript_id=transcript_id, video_id=video_id, library_id=library_id,
            latest_seq=0,                    # will be discovered by the job
            first_event_at=now, due_at=now,  # already due
        )

    async def run(self) -> None:
        self._tick_task = asyncio.current_task()
        try:
            while not self._stop.is_set():
                await asyncio.sleep(TICK_INTERVAL_SEC)
                await self._tick()
        except asyncio.CancelledError:
            pass

    def stop(self) -> None:
        self._stop.set()

    async def _tick(self) -> None:
        now = time.monotonic()
        due_ids = [
            tid for tid, b in list(self._buckets.items())
            if b.due_at <= now and tid not in self._inflight
        ]
        for tid in due_ids:
            bucket = self._buckets.pop(tid)
            self._inflight.add(tid)
            asyncio.create_task(self._run_one(bucket))

    async def _run_one(self, bucket: _Bucket) -> None:
        try:
            job = IncrementalIndexJob(
                self._pool,
                transcript_id=bucket.transcript_id,
                video_id=bucket.video_id,
                library_id=bucket.library_id,
            )
            await job.run()
        except IndexerPaused:
            log.info("indexer_paused", extra={"transcript_id": bucket.transcript_id})
        except IndexerStopped:
            log.info("indexer_stopped", extra={"transcript_id": bucket.transcript_id})
        except Exception as e:
            log.exception(
                "indexer_job_unhandled",
                extra={"transcript_id": bucket.transcript_id, "err": str(e)})
        finally:
            self._inflight.discard(bucket.transcript_id)
```

### 2.6 `job.py` — the SQL hot path (D3, D6, D8)

```python
"""IncrementalIndexJob — one indexer fire for one transcript."""
from __future__ import annotations
import json
import logging
from typing import Sequence

import asyncpg

from maktaba_pipeline.search.live.errors import IndexerPaused, IndexerStopped
from maktaba_pipeline.search.live.recompute import (
    SegmentRow, recompute_units, TAIL_OVERLAP_SEC)

log = logging.getLogger(__name__)

PAUSED_STATES = frozenset({"paused", "paused-pending", "cancelled"})


class IncrementalIndexJob:
    def __init__(self, pool, *, transcript_id: int, video_id, library_id):
        self._pool = pool
        self._transcript_id = transcript_id
        self._video_id = video_id
        self._library_id = library_id

    async def run(self) -> None:
        async with self._pool.acquire() as conn:
            # 1. Pause/cancel observability (D8).
            row = await conn.fetchrow("""
                SELECT j.state, j.pause_requested, j.cancel_requested,
                       t.is_active, t.language, t.last_indexed_segment_seq
                  FROM transcripts t
                  JOIN processing_jobs j ON j.id = t.transcribe_job_id
                 WHERE t.id = $1
            """, self._transcript_id)
            if row is None:
                log.warning("indexer_transcript_missing",
                            extra={"transcript_id": self._transcript_id})
                return
            if not row["is_active"]:
                log.info("indexer_transcript_inactive_skip",
                         extra={"transcript_id": self._transcript_id})
                raise IndexerStopped()
            if row["state"] in PAUSED_STATES or row["pause_requested"] \
                    or row["cancel_requested"]:
                raise IndexerPaused()

            language = row["language"]
            watermark_seq = row["last_indexed_segment_seq"]

            # 2-9. Hold the per-transcript advisory lock (D6) inside one xact.
            async with conn.transaction():
                lock_key = await conn.fetchval(
                    "SELECT hashtextextended('idx:' || $1::text, 0)",
                    str(self._transcript_id))
                await conn.execute("SELECT pg_advisory_xact_lock($1)", lock_key)

                # Re-check state after lock — a paused job could have flipped.
                state = await conn.fetchval(
                    "SELECT state FROM processing_jobs "
                    "WHERE id = (SELECT transcribe_job_id FROM transcripts "
                    "             WHERE id = $1)",
                    self._transcript_id)
                if state in PAUSED_STATES:
                    raise IndexerPaused()

                # 3. Fetch segments past watermark (with TAIL_OVERLAP_SEC backoff).
                segments = await self._fetch_segments(conn, watermark_seq)
                if not segments:
                    log.debug("indexer_no_new_segments",
                              extra={"transcript_id": self._transcript_id})
                    return

                # 4. Re-chunk.
                existing_max_unit_seq = await conn.fetchval(
                    "SELECT COALESCE(MAX(seq), 0) FROM transcript_units "
                    "WHERE transcript_id = $1",
                    self._transcript_id)
                # Determine which existing unit we'll start replacing from.
                replace_from_seq = await self._find_replace_start_unit_seq(
                    conn, segments[0].id, existing_max_unit_seq)

                delta = recompute_units(
                    transcript_id=self._transcript_id,
                    fetched_segments=segments,
                    language=language,
                    existing_max_unit_seq=replace_from_seq - 1,
                )

                if not delta.units:
                    return

                # 5. Idempotent upsert (D3).
                await self._upsert_units(conn, delta.units, replace_from_seq)

                # 6. (Postgres) tsv is a GENERATED column on transcript_units —
                #    no extra write needed. SQLite triggers handle FTS sync
                #    inside the same transaction.

                # 7. Mark units indexed_at = now() (FTS-only — Chroma deferred).
                await conn.execute("""
                    UPDATE transcript_units
                       SET indexed_at = now()
                     WHERE transcript_id = $1 AND indexed_at IS NULL
                """, self._transcript_id)

                # 8. Advance the watermark (D4).
                await conn.execute("""
                    UPDATE transcripts
                       SET last_indexed_segment_seq = GREATEST(
                              last_indexed_segment_seq, $2)
                     WHERE id = $1
                """, self._transcript_id, delta.max_segment_seq)
            # Transaction commits here; advisory lock released.

    async def _fetch_segments(
        self, conn, watermark_seq: int,
    ) -> list[SegmentRow]:
        """Read all segments past watermark, plus an overlap window
        for unit-tail recomputation."""
        rows = await conn.fetch("""
            WITH watermark AS (
                SELECT MAX(end_sec) AS w_end
                  FROM transcript_segments
                 WHERE transcript_id = $1 AND seq <= $2
            )
            SELECT id, seq, start_sec, end_sec, text
              FROM transcript_segments, watermark
             WHERE transcript_id = $1
               AND (seq > $2
                    OR end_sec >= COALESCE(watermark.w_end, 0) - $3)
             ORDER BY seq
        """, self._transcript_id, watermark_seq, TAIL_OVERLAP_SEC)
        return [SegmentRow(
            id=r["id"], seq=r["seq"],
            start_sec=r["start_sec"], end_sec=r["end_sec"],
            text=r["text"]) for r in rows]

    async def _find_replace_start_unit_seq(
        self, conn, oldest_segment_id: int, existing_max_unit_seq: int,
    ) -> int:
        """Find the seq of the existing unit that contains oldest_segment_id."""
        if existing_max_unit_seq == 0:
            return 1
        seq = await conn.fetchval("""
            SELECT seq FROM transcript_units
             WHERE transcript_id = $1
               AND segment_ids @> $2::jsonb
             ORDER BY seq
             LIMIT 1
        """, self._transcript_id, json.dumps([oldest_segment_id]))
        return seq if seq is not None else existing_max_unit_seq + 1

    async def _upsert_units(
        self, conn, units: Sequence, starting_seq: int,
    ) -> None:
        # First, delete units >= starting_seq (covers unit-count shrink case).
        await conn.execute("""
            DELETE FROM transcript_units
             WHERE transcript_id = $1 AND seq >= $2
        """, self._transcript_id, starting_seq)
        for i, u in enumerate(units):
            seq = starting_seq + i
            await conn.execute("""
                INSERT INTO transcript_units
                    (transcript_id, seq, start_sec, end_sec, text, language,
                     segment_ids, indexed_at, metadata)
                VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, NULL, $8::jsonb)
                ON CONFLICT (transcript_id, seq) DO UPDATE SET
                    start_sec   = EXCLUDED.start_sec,
                    end_sec     = EXCLUDED.end_sec,
                    text        = EXCLUDED.text,
                    segment_ids = EXCLUDED.segment_ids,
                    indexed_at  = NULL,
                    metadata    = EXCLUDED.metadata
            """,
                self._transcript_id, seq,
                u.start_sec, u.end_sec, u.text, u.language,
                json.dumps(u.segment_ids), json.dumps(u.metadata))
```

### 2.7 `supervisor.py` — boot, listen, catch-up

```python
"""IndexerSupervisor — long-lived. Listens, dispatches, manages teardown."""
from __future__ import annotations
import asyncio
import logging
from uuid import UUID

import asyncpg

from maktaba_pipeline.search.live.dispatcher import IndexerDispatcher
from maktaba_pipeline.search.live.notify import NotifyEvent, NotifyKind

log = logging.getLogger(__name__)

LISTENER_RECONNECT_DELAY_SEC = 1.0
LISTENER_RECONNECT_MAX_DELAY_SEC = 60.0


class IndexerSupervisor:
    def __init__(self, db_pool, *, chroma_writer):
        self._pool = db_pool
        self._chroma_writer = chroma_writer
        self._dispatcher = IndexerDispatcher(db_pool, chroma_writer=chroma_writer)
        self._listener_conn: asyncpg.Connection | None = None
        self._listener_task: asyncio.Task | None = None
        self._dispatcher_task: asyncio.Task | None = None
        self._stop = asyncio.Event()

    async def start(self) -> None:
        await self._catch_up()
        self._dispatcher_task = asyncio.create_task(self._dispatcher.run())
        self._listener_task = asyncio.create_task(self._listener_loop())

    async def stop(self) -> None:
        self._stop.set()
        self._dispatcher.stop()
        for t in (self._listener_task, self._dispatcher_task):
            if t is not None:
                t.cancel()
        if self._listener_conn is not None:
            try:
                await self._listener_conn.close()
            except Exception:
                pass

    async def _catch_up(self) -> None:
        """Fire one job per transcript that has segments past its watermark."""
        async with self._pool.acquire() as conn:
            rows = await conn.fetch("""
                SELECT t.id AS transcript_id, t.video_id, v.library_id
                  FROM transcripts t
                  JOIN videos v ON v.id = t.video_id
                  JOIN processing_jobs j ON j.id = t.transcribe_job_id
                 WHERE t.is_active = true
                   AND j.state NOT IN ('cancelled', 'failed')
                   AND EXISTS (
                       SELECT 1 FROM transcript_segments s
                        WHERE s.transcript_id = t.id
                          AND s.seq > t.last_indexed_segment_seq
                       LIMIT 1
                   )
            """)
        for r in rows:
            self._dispatcher.submit_force(
                r["transcript_id"], r["video_id"], r["library_id"])
        log.info("indexer_catch_up_enqueued", extra={"count": len(rows)})

    async def _listener_loop(self) -> None:
        """Reconnect-with-backoff LISTEN loop."""
        delay = LISTENER_RECONNECT_DELAY_SEC
        while not self._stop.is_set():
            try:
                await self._listener_session()
                delay = LISTENER_RECONNECT_DELAY_SEC
            except asyncio.CancelledError:
                return
            except Exception as e:
                log.warning("indexer_listener_disconnect",
                            extra={"err": str(e), "backoff_sec": delay})
                # On reconnect, we may have missed NOTIFYs — catch up.
                try:
                    await self._catch_up()
                except Exception as ce:
                    log.exception("indexer_catch_up_after_reconnect_failed",
                                  extra={"err": str(ce)})
                await asyncio.sleep(delay)
                delay = min(delay * 2, LISTENER_RECONNECT_MAX_DELAY_SEC)

    async def _listener_session(self) -> None:
        self._listener_conn = await asyncpg.connect(
            **self._pool.get_settings().connect_kwargs())
        await self._listener_conn.add_listener(
            "segments.committed", self._on_notify)
        await self._listener_conn.add_listener(
            "transcripts.active_changed", self._on_notify)
        # Block here until the connection drops (asyncpg emits
        # InterfaceError on disconnect).
        while not self._stop.is_set():
            await asyncio.sleep(1.0)
            if self._listener_conn.is_closed():
                raise ConnectionError("listener connection closed")

    def _on_notify(self, _conn, _pid, channel, payload) -> None:
        evt = NotifyEvent.parse(channel, payload)
        if evt is None:
            return
        if evt.kind == NotifyKind.ACTIVE_CHANGED and evt.is_active is False:
            asyncio.create_task(self._handle_old_transcript(evt))
        self._dispatcher.submit(evt)

    async def _handle_old_transcript(self, evt: NotifyEvent) -> None:
        """When is_active flips false, delete the old transcript's vectors."""
        try:
            await self._chroma_writer.delete_transcript(
                library_id=evt.library_id, transcript_id=evt.transcript_id)
            log.info("indexer_old_transcript_chroma_cleared",
                     extra={"transcript_id": evt.transcript_id})
        except Exception as e:
            log.exception("indexer_old_transcript_chroma_clear_failed",
                          extra={"transcript_id": evt.transcript_id,
                                 "err": str(e)})
            # Best-effort; nightly reaper picks up orphaned vectors.
```

### 2.8 `runner.py` — boot wiring

```python
# pipeline/src/maktaba_pipeline/runner.py  (excerpt)

from maktaba_pipeline.search.live.supervisor import IndexerSupervisor
from maktaba_pipeline.search.chroma.writer import ChromaWriter

async def boot(ctx) -> None:
    # ... existing pool, registry, etc.
    chroma = ChromaWriter.from_config(ctx.cfg.search)
    ctx.indexer = IndexerSupervisor(ctx.db_pool, chroma_writer=chroma)
    await ctx.indexer.start()
    ctx.add_shutdown_hook(ctx.indexer.stop)
```

### 2.9 Post-transcribe `index` stage (vector + DLQ — D5, D7)

```python
# pipeline/src/maktaba_pipeline/search/stages/index.py

import logging
from maktaba_pipeline.search.live.deadletter import dlq_record_failure

log = logging.getLogger(__name__)

CHROMA_BATCH_SIZE = 64
CHROMA_RETRY_PER_BATCH = 2


async def run_index_stage(ctx, claimed_job, *, transcript_id, library_id):
    """Post-transcribe stage. Drains pending Chroma upserts for this transcript."""
    # Defensive re-fire of the live indexer to capture any tail units the
    # debounce missed in the final 500 ms before transcribe → done.
    from maktaba_pipeline.search.live.job import IncrementalIndexJob
    job = IncrementalIndexJob(
        ctx.db_pool,
        transcript_id=transcript_id,
        video_id=claimed_job.video_id,
        library_id=library_id,
    )
    await job.run()

    # Now batch-embed and upsert into Chroma.
    async with ctx.db_pool.acquire() as conn:
        units = await conn.fetch("""
            SELECT id, seq, transcript_id, start_sec, end_sec, text, language
              FROM transcript_units
             WHERE transcript_id = $1 AND indexed_at_in_chroma IS NULL
             ORDER BY seq
        """, transcript_id)

    for batch_start in range(0, len(units), CHROMA_BATCH_SIZE):
        batch = units[batch_start:batch_start + CHROMA_BATCH_SIZE]
        ok_ids: list[int] = []
        try:
            await ctx.chroma.upsert_batch(library_id=library_id, units=batch)
            ok_ids = [u["id"] for u in batch]
        except Exception as e:
            log.warning("chroma_batch_failed",
                        extra={"transcript_id": transcript_id, "err": str(e)})
            # Per-unit retry inside the batch to isolate poison units.
            for u in batch:
                try:
                    await ctx.chroma.upsert_batch(
                        library_id=library_id, units=[u])
                    ok_ids.append(u["id"])
                except Exception as ue:
                    await dlq_record_failure(
                        ctx.db_pool, unit_id=u["id"],
                        library_id=library_id, transcript_id=transcript_id,
                        error=str(ue))

        if ok_ids:
            async with ctx.db_pool.acquire() as conn:
                await conn.execute("""
                    UPDATE transcript_units
                       SET indexed_at_in_chroma = now()
                     WHERE id = ANY($1::bigint[])
                """, ok_ids)
```

### 2.10 `deadletter.py`

```python
"""DLQ helpers for vector index failures (D5)."""
from __future__ import annotations

INSERT_DLQ_SQL = """
INSERT INTO vector_index_dead_letter
    (unit_id, library_id, transcript_id, attempts, last_error, next_retry_at)
VALUES ($1, $2, $3, 1, $4, now() + interval '5 minutes')
ON CONFLICT (unit_id) DO UPDATE SET
    attempts      = vector_index_dead_letter.attempts + 1,
    last_error    = EXCLUDED.last_error,
    next_retry_at = now() + LEAST(
        interval '1 hour',
        interval '5 minutes' * (2 ^ vector_index_dead_letter.attempts))
"""


async def dlq_record_failure(pool, *, unit_id, library_id, transcript_id, error):
    async with pool.acquire() as conn:
        await conn.execute(
            INSERT_DLQ_SQL, unit_id, library_id, transcript_id, error[:512])


async def dlq_drain_due(pool, chroma, *, batch=32):
    """Reaper task — runs every 5 minutes from a separate scheduled task."""
    async with pool.acquire() as conn:
        rows = await conn.fetch("""
            SELECT d.id AS dlq_id, d.unit_id, d.library_id, d.transcript_id,
                   u.text, u.start_sec, u.end_sec, u.language, u.seq
              FROM vector_index_dead_letter d
              JOIN transcript_units u ON u.id = d.unit_id
             WHERE d.next_retry_at <= now()
             ORDER BY d.next_retry_at
             LIMIT $1
        """, batch)
    for r in rows:
        try:
            await chroma.upsert_batch(library_id=r["library_id"], units=[r])
            async with pool.acquire() as conn:
                async with conn.transaction():
                    await conn.execute(
                        "DELETE FROM vector_index_dead_letter WHERE id = $1",
                        r["dlq_id"])
                    await conn.execute(
                        "UPDATE transcript_units SET indexed_at_in_chroma = now() "
                        "WHERE id = $1", r["unit_id"])
        except Exception as e:
            await dlq_record_failure(
                pool, unit_id=r["unit_id"],
                library_id=r["library_id"], transcript_id=r["transcript_id"],
                error=str(e))
```

### 2.11 SQLite polling fallback

For SQLite (no LISTEN/NOTIFY) the `IndexerSupervisor._listener_loop`
branch swaps to a `PollingListener` task that:

1. Polls `segment_notify_log` and `transcript_active_change_log` every
   `LIVE_INDEX_POLL_INTERVAL_SEC` (default 5 s — matches the story AC).
2. Uses an in-memory `last_seen_id` watermark per channel; reads
   `WHERE id > last_seen_id ORDER BY id`.
3. Submits each row to `IndexerDispatcher.submit(...)` exactly as the
   asyncpg listener does. Debouncing applies the same way.

The poll-interval default of 5 s is well within the AC's 10 s search
latency budget (5 s poll + 0.5 s debounce + chunker time).

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `pipeline/src/maktaba_pipeline/search/live/__init__.py` | re-exports | (n/a) |
| 2 | `pipeline/src/maktaba_pipeline/search/live/errors.py` | `IndexerError`, `IndexerPaused`, `IndexerStopped` | (n/a) |
| 3 | `pipeline/src/maktaba_pipeline/search/live/notify.py` | `NotifyEvent`, `NotifyKind`, `NotifyEvent.parse` | `test_notify_parse_valid`, `test_notify_parse_truncated`, `test_notify_parse_unknown_channel` |
| 4 | `shared/db/migrations/0021_incremental_indexing.sql` | watermark column, DLQ table, NOTIFY trigger upgrade | migration applies cleanly on fresh + populated DB; idempotent on re-run |
| 5 | `shared/db/migrations/sqlite/0021_*.sql` | parallel ALTER TABLEs + log tables for SQLite | sqlite test fixture loads |
| 6 | `pipeline/src/maktaba_pipeline/search/live/recompute.py` | `SegmentRow`, `UnitDelta`, `recompute_units`, `TAIL_OVERLAP_SEC` | `test_recompute_handles_tail_unit`, `test_recompute_empty_segments` |
| 7 | `pipeline/src/maktaba_pipeline/search/live/deadletter.py` | `dlq_record_failure`, `dlq_drain_due` | `test_vector_dlq_writes`, `test_vector_dlq_backoff` |
| 8 | `pipeline/src/maktaba_pipeline/search/live/job.py` | `IncrementalIndexJob`, `PAUSED_STATES` | `test_job_idempotent_upsert`, `test_job_advisory_lock`, `test_job_watermark_advances`, `test_job_pause_check_skips` |
| 9 | `pipeline/src/maktaba_pipeline/search/live/dispatcher.py` | `IndexerDispatcher`, `_Bucket`, `DEBOUNCE_WINDOW_SEC` | `test_dispatcher_debounce`, `test_dispatcher_pause_drop`, `test_dispatcher_active_change_drops_bucket` |
| 10 | `pipeline/src/maktaba_pipeline/search/live/supervisor.py` | `IndexerSupervisor`, `LISTENER_RECONNECT_*` | `test_supervisor_catch_up`, `test_listener_disconnect_reconnect`, `test_active_transcript_swap` |
| 11 | `pipeline/src/maktaba_pipeline/search/stages/index.py` | `run_index_stage` (post-transcribe vector phase) | `test_index_stage_drains_chroma`, `test_index_stage_isolates_poison_unit` |
| 12 | `pipeline/src/maktaba_pipeline/runner.py` (edit) | wire `IndexerSupervisor.start()` into worker boot | `test_runner_boots_indexer` |

---

## 4. Test cases

All under `pipeline/src/maktaba_pipeline/search/live/tests/`. Use
`pytest-asyncio` and the existing `db_fixture` from Epic 6. The
`synth_backend` from Plan 3.6's tests provides scripted segment streams.

### 4.1 `test_single_segment_triggers_unit_update_within_budget` (story AC: live FTS in 10 s)

```python
async def test_single_segment_commit_indexes_within_budget(
    db, library, video, transcript, indexer_supervisor,
):
    """One segment commits → its unit is in FTS within 10 s."""
    # Indexer is already running (autoused fixture).
    seg = make_segment(seq=1, start=0.0, end=5.0,
                       text="الحمد لله رب العالمين")
    t0 = time.monotonic()
    await commit_segment_via_committer(db, transcript.id, seg)

    deadline = t0 + 10.0
    while time.monotonic() < deadline:
        rows = await db.fetch(
            "SELECT * FROM transcript_units "
            "WHERE transcript_id = $1 AND indexed_at IS NOT NULL",
            transcript.id)
        if rows:
            break
        await asyncio.sleep(0.1)
    else:
        pytest.fail("unit not indexed within 10 s budget")

    # FTS: query for the text → result returned.
    fts_hits = await fts_query(db, library.id, "الحمد لله")
    assert any(h["unit_id"] == rows[0]["id"] for h in fts_hits)
```

### 4.2 `test_burst_coalesces_into_single_pass` (D2 acceptance)

```python
async def test_100_segment_burst_collapses_to_few_indexer_fires(
    db, library, video, transcript, indexer_supervisor, indexer_metrics,
):
    """100 segments committed in 2 s → indexer fires ≤ 8 times for that transcript."""
    for i in range(100):
        await commit_segment_via_committer(
            db, transcript.id,
            make_segment(seq=i + 1, start=i * 5.0, end=(i + 1) * 5.0,
                         text=f"sentence {i}."))
        await asyncio.sleep(0.02)  # 50 Hz ≈ 100 segments in 2 s

    # Wait for the dispatcher to drain.
    await indexer_supervisor.dispatcher_drained()

    fires = indexer_metrics.fires_for(transcript.id)
    assert 1 <= fires <= 8, f"expected ≤ 8 fires, got {fires}"

    # All segments are reflected in units.
    units = await db.fetch(
        "SELECT * FROM transcript_units WHERE transcript_id = $1 "
        "ORDER BY seq", transcript.id)
    last_unit = units[-1]
    assert last_unit["end_sec"] == pytest.approx(500.0)
```

### 4.3 `test_worker_restart_catches_up_via_watermark` (D4)

```python
async def test_restart_indexes_segments_committed_while_down(
    db, library, video, transcript, indexer_factory,
):
    """Crash indexer mid-stream; on boot the catch-up scan picks up the gap."""
    indexer = await indexer_factory.start()

    # Commit 5 segments while indexer is up — they're indexed.
    for i in range(5):
        await commit_segment_via_committer(
            db, transcript.id,
            make_segment(seq=i + 1, start=i * 5, end=(i + 1) * 5,
                         text=f"a {i}."))
    await indexer.dispatcher_drained()
    pre_restart_count = await db.fetchval(
        "SELECT COUNT(*) FROM transcript_units WHERE transcript_id = $1",
        transcript.id)
    assert pre_restart_count > 0

    # Stop indexer; commit 10 more segments (no listener).
    await indexer.stop()
    for i in range(5, 15):
        await commit_segment_via_committer(
            db, transcript.id,
            make_segment(seq=i + 1, start=i * 5, end=(i + 1) * 5,
                         text=f"b {i}."))

    # Boot a new indexer — its catch-up scan should re-process.
    indexer2 = await indexer_factory.start()
    await indexer2.dispatcher_drained()

    post_restart_count = await db.fetchval(
        "SELECT COUNT(*) FROM transcript_units WHERE transcript_id = $1",
        transcript.id)
    assert post_restart_count > pre_restart_count

    last_seq = await db.fetchval(
        "SELECT last_indexed_segment_seq FROM transcripts WHERE id = $1",
        transcript.id)
    assert last_seq == 15
```

### 4.4 `test_vector_failure_does_not_lose_fts_update` (D5)

```python
async def test_chroma_outage_during_index_stage_keeps_fts(
    db, library, video, transcript, indexer_supervisor, fake_chroma,
):
    """Chroma raises on every upsert → unit is in FTS, in DLQ, NOT in Chroma."""
    fake_chroma.fail_all_with(RuntimeError("simulated chroma outage"))

    for i in range(3):
        await commit_segment_via_committer(
            db, transcript.id,
            make_segment(seq=i + 1, start=i * 5, end=(i + 1) * 5,
                         text=f"x {i}."))
    await indexer_supervisor.dispatcher_drained()

    # Live phase: FTS has the units, Chroma was never called.
    units = await db.fetch(
        "SELECT id, indexed_at, indexed_at_in_chroma "
        "  FROM transcript_units WHERE transcript_id = $1", transcript.id)
    assert all(u["indexed_at"] is not None for u in units)
    assert all(u["indexed_at_in_chroma"] is None for u in units)
    assert fake_chroma.upsert_call_count == 0

    # Now run the post-transcribe index stage (which DOES call Chroma).
    from maktaba_pipeline.search.stages.index import run_index_stage
    await run_index_stage(
        ctx=fake_ctx(db, fake_chroma),
        claimed_job=fake_job(video_id=video.id),
        transcript_id=transcript.id,
        library_id=library.id)

    # FTS is still intact.
    units2 = await db.fetch(
        "SELECT indexed_at, indexed_at_in_chroma "
        "  FROM transcript_units WHERE transcript_id = $1", transcript.id)
    assert all(u["indexed_at"] is not None for u in units2)
    # And the units are in DLQ.
    dlq = await db.fetch(
        "SELECT unit_id, attempts, last_error FROM vector_index_dead_letter "
        "WHERE transcript_id = $1", transcript.id)
    assert len(dlq) == len(units)
    assert all("simulated chroma outage" in r["last_error"] for r in dlq)
```

### 4.5 `test_dispatcher_pause_drop` (D8 + story AC pause-aware chunking)

```python
async def test_indexer_skips_when_parent_job_paused(
    db, library, video, transcript, processing_job,
    indexer_supervisor, indexer_metrics,
):
    """Set pause_requested → next NOTIFY drops the bucket; no fire."""
    await db.execute(
        "UPDATE processing_jobs SET state = 'paused-pending' WHERE id = $1",
        processing_job.id)

    await commit_segment_via_committer(
        db, transcript.id,
        make_segment(seq=1, start=0.0, end=5.0, text="hello."))
    await asyncio.sleep(1.5)  # wait past debounce + tick

    # No new units (the job ran, observed paused, raised IndexerPaused).
    units = await db.fetchval(
        "SELECT COUNT(*) FROM transcript_units WHERE transcript_id = $1",
        transcript.id)
    assert units == 0
    assert indexer_metrics.paused_for(transcript.id) >= 1

    # Resume → next NOTIFY indexes both segments.
    await db.execute(
        "UPDATE processing_jobs SET state = 'running' WHERE id = $1",
        processing_job.id)
    await commit_segment_via_committer(
        db, transcript.id,
        make_segment(seq=2, start=5.0, end=10.0, text="world."))
    await indexer_supervisor.dispatcher_drained()
    units = await db.fetchval(
        "SELECT COUNT(*) FROM transcript_units WHERE transcript_id = $1",
        transcript.id)
    assert units >= 1  # both segments rolled into one or two units
```

### 4.6 `test_active_transcript_swap` (D9)

```python
async def test_reindex_replaces_old_transcript(
    db, library, video, transcript_v1, indexer_supervisor, fake_chroma,
):
    """Flip is_active false on v1 + create v2 → v1's units & vectors are removed."""
    # Populate some units on v1.
    await seed_units(db, transcript_v1.id, count=5)
    await fake_chroma.add_batch(
        library_id=library.id, transcript_id=transcript_v1.id, count=5)

    # Now create v2 + flip in one transaction.
    async with db.transaction():
        v2 = await db.fetchrow("""
            INSERT INTO transcripts (video_id, language, is_active)
            VALUES ($1, 'ar', true)
            RETURNING *""", video.id)
        await db.execute(
            "UPDATE transcripts SET is_active = false WHERE id = $1",
            transcript_v1.id)

    # Wait for the active_changed listener.
    await asyncio.sleep(0.5)

    # v1's units are gone (CASCADE on transcripts → transcript_units).
    cnt = await db.fetchval(
        "SELECT COUNT(*) FROM transcript_units WHERE transcript_id = $1",
        transcript_v1.id)
    assert cnt == 0

    # v1's Chroma vectors are gone.
    assert fake_chroma.count_for(transcript_v1.id) == 0
```

### 4.7 `test_listener_disconnect_reconnect` (E2)

```python
async def test_listener_reconnects_and_runs_catch_up(
    db, library, transcript, indexer_supervisor,
):
    """Kill the listener connection; commits during the gap are still indexed."""
    # Force the connection closed.
    await indexer_supervisor.force_listener_close_for_test()

    # Commit during the disconnect window (no listener).
    await commit_segment_via_committer(
        db, transcript.id,
        make_segment(seq=1, start=0, end=5, text="gap segment."))

    # Wait for reconnect (backoff is 1 s in tests).
    await asyncio.wait_for(
        indexer_supervisor.wait_listener_reconnected(), timeout=10.0)
    await indexer_supervisor.dispatcher_drained()

    # The catch-up triggered by reconnect picks up the commit.
    rows = await db.fetch(
        "SELECT * FROM transcript_units WHERE transcript_id = $1",
        transcript.id)
    assert len(rows) >= 1
```

### 4.8 `test_recompute_handles_tail_unit` (D3 + chunker tail edit)

```python
def test_recompute_replaces_tail_unit_when_segment_appended():
    """Two passes: pass 1 = 3 segments, pass 2 = +1 segment.
    The unit covering the boundary is REPLACED, not duplicated."""
    seg1 = SegmentRow(id=1, seq=1, start_sec=0, end_sec=5, text="hello world.")
    seg2 = SegmentRow(id=2, seq=2, start_sec=5, end_sec=10,
                      text="how are you")     # no terminator → unsealed
    seg3 = SegmentRow(id=3, seq=3, start_sec=10, end_sec=15, text="today?")

    # Pass 1: chunk segments 1-3.
    delta1 = recompute_units(
        transcript_id=1,
        fetched_segments=[seg1, seg2, seg3],
        language="en",
        existing_max_unit_seq=0)
    assert len(delta1.units) == 2
    assert delta1.units[0].text == "hello world."
    assert delta1.units[1].text == "how are you today?"

    # Pass 2: a new segment lands. The tail unit's text grows.
    seg4 = SegmentRow(id=4, seq=4, start_sec=15, end_sec=20,
                      text=" Fine, thanks.")
    # Simulate replace_from_seq = 2 (the tail unit's seq).
    delta2 = recompute_units(
        transcript_id=1,
        fetched_segments=[seg2, seg3, seg4],   # overlap window
        language="en",
        existing_max_unit_seq=1)               # we'll start replacing from seq 2
    # The chunker produces TWO units now (the boundary moved).
    assert delta2.units[0].text == "how are you today?"
    assert delta2.units[1].text == "Fine, thanks."
```

### 4.9 `test_job_advisory_lock` (D6)

```python
async def test_two_concurrent_indexer_runs_serialize(db, transcript):
    """Two IncrementalIndexJob.run() in parallel → second waits for first."""
    barrier = asyncio.Event()
    log_order: list[str] = []

    async def slow_job(label):
        async with db.acquire() as conn:
            await conn.execute("BEGIN")
            await conn.execute(
                "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))",
                f"idx:{transcript.id}")
            log_order.append(f"{label}-acquired")
            await asyncio.sleep(0.2)
            log_order.append(f"{label}-releasing")
            await conn.execute("COMMIT")

    await asyncio.gather(slow_job("A"), slow_job("B"))
    # B's acquire happens AFTER A's release.
    assert log_order.index("B-acquired") > log_order.index("A-releasing")
```

### 4.10 `test_notify_parse_truncated`

```python
def test_parse_truncated_payload_returns_none():
    """A NOTIFY with payload past Postgres's 8000-byte cap arrives
    truncated; we don't crash."""
    bad = '{"transcript_id": 1, "video_id": "abc"'  # no closing brace
    evt = NotifyEvent.parse("segments.committed", bad)
    assert evt is None  # log warning and drop
```

### 4.11 `test_index_stage_isolates_poison_unit` (D5 per-unit retry)

```python
async def test_one_bad_unit_does_not_block_others(
    db, library, transcript, fake_chroma,
):
    """If unit #3 fails Chroma upsert, units #1, #2, #4-N still index."""
    await seed_units(db, transcript.id, count=5)
    poison_id = (await db.fetch(
        "SELECT id FROM transcript_units WHERE transcript_id=$1 ORDER BY seq",
        transcript.id))[2]["id"]
    fake_chroma.fail_when_unit(poison_id, RuntimeError("simulated"))

    await run_index_stage(
        ctx=fake_ctx(db, fake_chroma),
        claimed_job=fake_job(video_id=transcript.video_id),
        transcript_id=transcript.id, library_id=library.id)

    indexed = await db.fetch(
        "SELECT id, indexed_at_in_chroma FROM transcript_units "
        "WHERE transcript_id = $1 ORDER BY seq", transcript.id)
    not_indexed = [u for u in indexed if u["indexed_at_in_chroma"] is None]
    assert [u["id"] for u in not_indexed] == [poison_id]
    dlq = await db.fetchval(
        "SELECT unit_id FROM vector_index_dead_letter WHERE transcript_id=$1",
        transcript.id)
    assert dlq == poison_id
```

### 4.12 `test_supervisor_catch_up`

```python
async def test_boot_catch_up_processes_pre_existing_gap(
    db, library, video, transcript, indexer_factory,
):
    """Seed segments without an indexer → start indexer → units appear."""
    for i in range(3):
        await commit_segment_via_committer(
            db, transcript.id,
            make_segment(seq=i + 1, start=i * 5, end=(i + 1) * 5,
                         text=f"hi {i}."))
    # Reset watermark to simulate "pre-indexer" state.
    await db.execute(
        "UPDATE transcripts SET last_indexed_segment_seq = 0 WHERE id = $1",
        transcript.id)
    cnt_before = await db.fetchval(
        "SELECT COUNT(*) FROM transcript_units WHERE transcript_id = $1",
        transcript.id)

    indexer = await indexer_factory.start()
    await indexer.dispatcher_drained()

    cnt_after = await db.fetchval(
        "SELECT COUNT(*) FROM transcript_units WHERE transcript_id = $1",
        transcript.id)
    assert cnt_after >= cnt_before + 1
    last_seq = await db.fetchval(
        "SELECT last_indexed_segment_seq FROM transcripts WHERE id = $1",
        transcript.id)
    assert last_seq == 3
```

### 4.13 `test_dispatcher_debounce` (unit; pure timing)

```python
async def test_debounce_collapses_rapid_events_into_one_fire(
    fake_pool, fake_chroma,
):
    dispatcher = IndexerDispatcher(fake_pool, chroma_writer=fake_chroma)
    dispatcher_task = asyncio.create_task(dispatcher.run())
    try:
        for seq in range(1, 11):
            dispatcher.submit(NotifyEvent(
                kind=NotifyKind.SEGMENT_COMMITTED,
                transcript_id=42, video_id=uuid4(), library_id=uuid4(),
                last_segment_end_sec=seq * 5.0, seq=seq,
            ))
            await asyncio.sleep(0.02)  # well within DEBOUNCE_WINDOW_SEC

        await asyncio.sleep(DEBOUNCE_WINDOW_SEC + 0.2)

        assert fake_pool.indexer_runs_for(42) == 1
    finally:
        dispatcher.stop()
        await dispatcher_task
```

### 4.14 `test_vector_dlq_writes` + `test_vector_dlq_backoff`

```python
async def test_dlq_record_failure_increments_attempts(db, unit):
    await dlq_record_failure(
        db.pool, unit_id=unit.id,
        library_id=unit.library_id, transcript_id=unit.transcript_id,
        error="x")
    await dlq_record_failure(
        db.pool, unit_id=unit.id,
        library_id=unit.library_id, transcript_id=unit.transcript_id,
        error="y")
    row = await db.fetchrow(
        "SELECT attempts, next_retry_at, last_error "
        "FROM vector_index_dead_letter WHERE unit_id = $1", unit.id)
    assert row["attempts"] == 2
    assert row["last_error"] == "y"


async def test_dlq_backoff_caps_at_one_hour(db, unit):
    for _ in range(20):
        await dlq_record_failure(
            db.pool, unit_id=unit.id,
            library_id=unit.library_id, transcript_id=unit.transcript_id,
            error="x")
    delta = await db.fetchval(
        "SELECT next_retry_at - now() FROM vector_index_dead_letter "
        "WHERE unit_id = $1", unit.id)
    assert delta <= timedelta(hours=1, seconds=5)
```

---

## 5. Edge cases and how the plan handles each

| # | Edge case | Handled by |
|---|-----------|------------|
| E1 | NOTIFY payload truncation. Postgres caps `pg_notify` payload at 8000 bytes; a malformed JSON arrives. | `NotifyEvent.parse()` (`notify.py`) wraps `json.loads` in `try/except`; on `ValueError` it logs `notify_payload_unparseable` with a 120-byte head and returns `None`. The dispatcher ignores `None` events. The catch-up scan then fills the gap on the next NOTIFY (or on the worker restart). (`test_notify_parse_truncated`) |
| E2 | Listener disconnect / reconnect. The asyncpg LISTEN connection drops (server restart, network blip, idle timeout). | `IndexerSupervisor._listener_loop` is a reconnect loop with exponential backoff (1 s → 60 s cap). On every reconnect it re-runs `_catch_up()` to capture commits that happened during the gap. The watermark column (D4) ensures correctness regardless of how many NOTIFYs were missed. (`test_listener_disconnect_reconnect`) |
| E3 | Segment edited not just appended (e.g., a backend that rewrites the last segment's text after a confidence boost). | The `recompute_units` overlap window (`TAIL_OVERLAP_SEC = 30 s`) re-reads the last 30 s of segments on every fire. The unit covering the edited segment is regenerated; the upsert (`ON CONFLICT … DO UPDATE`) replaces it in place AND sets `indexed_at = NULL`, which triggers the FTS sync trigger to re-index the row. (`test_recompute_handles_tail_unit`) |
| E4 | Transcript paused mid-batch — the pause flag flips between `_fetch_segments` and the upsert. | The job re-checks `processing_jobs.state` *after* acquiring the per-transcript advisory lock (D6). If the state is in `PAUSED_STATES`, the transaction is rolled back via `IndexerPaused`, the watermark is unchanged, and the next NOTIFY post-resume picks it up. The story AC says "stops chunking as soon as it observes" — D8 + the post-lock recheck makes "as soon as" mean within one fire. (`test_dispatcher_pause_drop`) |
| E5 | Video deleted while indexing. `DELETE FROM videos` cascades to `transcripts → transcript_units` (Story 5.1's ON DELETE CASCADE). | The indexer's `_fetch_segments` returns an empty result (no segments under that transcript anymore); `recompute_units` returns an empty `UnitDelta`; the job is a no-op. The advisory lock is held during this null run, but it releases on COMMIT and the next NOTIFY (which would have to come from a recreated transcript) is unaffected. The DLQ table cascades on `unit_id`, so leftover DLQ rows for the deleted units vanish. |
| E6 | Two indexer processes run concurrently (rare; misconfiguration). | The per-transcript advisory lock (D6) serializes them at the SQL layer. The slower process's transaction blocks until the faster one commits, then re-reads the watermark and finds nothing to do. No duplicate units, no broken FTS. (`test_job_advisory_lock`) |
| E7 | NOTIFY arrives for a transcript whose row was hard-deleted (race on the active_changed flow). | `IncrementalIndexJob.run` checks `if row is None` after the initial join; logs `indexer_transcript_missing` and returns. No upsert is attempted. |
| E8 | A unit's `text` would change *length* across re-fires (chunker's sentence boundary moves because more text arrived). | The job's `_upsert_units` does `DELETE FROM transcript_units WHERE seq >= starting_seq` before re-inserting, so a 5→4 unit shrink (e.g., chunker merges two short sentences as more context arrives) is reflected. The FTS trigger fires on DELETE+INSERT; the FTS row count drops too. |
| E9 | Catch-up scan on a giant 50,000-segment transcript. | The scan runs ONE indexer fire per transcript (not per segment). The `_fetch_segments` query reads only segments past the watermark; if the watermark is 0 it reads all, which on 50,000 segments is ~5 MB and ~150 ms with the `(transcript_id, seq)` index. The chunker is O(N); ~50,000 segments → ~10,000 units → ~2 s of CPU. Acceptable on a startup, and the per-transcript advisory lock means parallel videos catch up in parallel. |
| E10 | DLQ grows unbounded because Chroma is permanently broken. | The DLQ has an exponential backoff capped at 1 hour (per row). The reaper task (`dlq_drain_due`) is bounded at 32 rows per run, every 5 min. A surfaced metric `indexer_dlq_depth` lets ops decide when to disable Chroma upserts entirely (the FTS path is unaffected). |
| E11 | A unit straddles a paused→resumed seam (story-named edge case). | The pause-aware skip (D8) means the indexer never emits a unit during the pause. After resume, the chunker re-reads the committed segments (including any post-resume ones via the overlap window) and produces a unit whose boundaries are clean. The upsert idempotency means even if two debounce fires hit the seam, the second one converges on the correct unit. |
| E12 | Re-processing a video while live indexing is mid-fire on the OLD transcript. | The `transcripts.active_changed` NOTIFY (D9) reaches the dispatcher; `submit` drops the pending bucket for the old transcript_id. Any in-flight job for the old transcript completes its current commit (advisory lock), then the FTS trigger's CASCADE deletes its units when `is_active` flips. Chroma deletion runs in the supervisor's `_handle_old_transcript` task. The new transcript's NOTIFYs route to a fresh bucket. (`test_active_transcript_swap`) |
| E13 | Worker process killed *during* a job's transaction. | The transaction rolls back; advisory lock releases; watermark is unchanged. On reboot, the catch-up scan re-fires the job. The chunker is deterministic on the same input, so the result is identical. |
| E14 | NOTIFY storm from a backend that emits many micro-segments (1 segment per word). | The 500 ms debounce caps fire rate at 2 Hz per transcript. The `_fetch_segments` overlap window means each fire re-chunks ~30 s of segment text — even at 1 word/segment that's < 200 segments per fire, well within the chunker's budget. |
| E15 | Library setting `embedding_model` changes mid-transcribe. | Out of scope for the live indexer (which doesn't touch Chroma). The post-transcribe stage observes the new setting and Story 5.3's "embedding model swap" edge case kicks in (full library re-embed). The DLQ is unaffected. |
| E16 | The transcript row's `language` is `NULL` or unrecognized. | `recompute_units` is called with `language=row["language"]`. The chunker (Story 5.1) falls back to `simple` punctuation rules for unknown languages. The FTS layer (Story 5.2) maps unknown → `'simple'` regconfig. No crash. |

---

## 6. Acceptance checklist

Implementer marks each item with the test (or assertion) that proves it.

- [ ] **A1** Worker boot wires `IndexerSupervisor.start()`; the supervisor opens a dedicated asyncpg connection, runs the catch-up scan, then `LISTEN segments.committed` + `LISTEN transcripts.active_changed`. (`test_runner_boots_indexer`, `test_supervisor_catch_up`)
- [ ] **A2** A single segment commit results in a `transcript_units` row with `indexed_at IS NOT NULL` and an FTS row within 10 s of the commit. (`test_single_segment_commit_indexes_within_budget`)
- [ ] **A3** A burst of 100 segment commits within 2 s coalesces into ≤ 8 indexer fires for that transcript (D2 debounce). (`test_100_segment_burst_collapses_to_few_indexer_fires`)
- [ ] **A4** Worker restart picks up segments committed during the downtime via `transcripts.last_indexed_segment_seq`. (`test_restart_indexes_segments_committed_while_down`)
- [ ] **A5** Vector index failure during the post-transcribe `index` stage does **not** roll back the FTS update; the failing unit is added to `vector_index_dead_letter`. (`test_chroma_outage_during_index_stage_keeps_fts`, `test_one_bad_unit_does_not_block_others`)
- [ ] **A6** When the parent transcribe job's state is in `{paused, paused-pending, cancelled}`, the live indexer drops the bucket and does not emit any partial unit. On resume, the next NOTIFY indexes forward. (`test_indexer_skips_when_parent_job_paused`)
- [ ] **A7** Re-processing a video flips `is_active = false` on the old transcript; the `transcripts.active_changed` listener tears down the old transcript's units (CASCADE) and Chroma vectors (`_handle_old_transcript`). (`test_reindex_replaces_old_transcript`)
- [ ] **A8** The `segments.committed` NOTIFY payload includes `transcript_id`, `video_id`, `library_id`, `last_segment_end_sec`, and `seq` (D10 schema). The trigger function `segments_notify_committed` is updated in the migration. (`test_notify_parse_valid`)
- [ ] **A9** The unit upsert is idempotent on `(transcript_id, seq)`; replays of the same chunk produce no duplicates. (`test_job_idempotent_upsert`, `test_recompute_handles_tail_unit`)
- [ ] **A10** Per-transcript advisory lock (`pg_advisory_xact_lock(hashtextextended('idx:'||transcript_id, 0))`) serializes concurrent indexer runs on the same transcript; different transcripts run in parallel. (`test_two_concurrent_indexer_runs_serialize`)
- [ ] **A11** Listener disconnect triggers automatic reconnect with exponential backoff (1 s → 60 s cap) and a fresh catch-up scan on every reconnect. (`test_listener_reconnects_and_runs_catch_up`)
- [ ] **A12** Truncated/malformed NOTIFY payloads are dropped with a WARN log; the indexer does not crash. (`test_parse_truncated_payload_returns_none`)
- [ ] **A13** SQLite engines use a polling listener at 5 s intervals against `segment_notify_log` and `transcript_active_change_log`; the same dispatcher debouncing applies. (Cross-backend test runs the indexer suite against `--db=sqlite`.)
- [ ] **A14** During live transcription, Chroma collection size does **not** grow (D7); after the post-transcribe `index` stage, Chroma contains all units (minus DLQ rows). (`test_chroma_added_only_at_index_stage` from the story.)
- [ ] **A15** The DLQ reaper drains pending entries with exponential backoff capped at 1 hour. (`test_dlq_record_failure_increments_attempts`, `test_dlq_backoff_caps_at_one_hour`)
- [ ] **A16** Migration `0021_incremental_indexing.sql` applies cleanly on fresh + populated DBs and is idempotent on re-run.
- [ ] **A17** No code path in this story performs Chroma writes from the live (debounced) tick — Chroma writes occur **only** in `run_index_stage` and the DLQ reaper. (Static check: grep `chroma_writer.upsert` outside `stages/index.py` and `deadletter.py` returns nothing.)
- [ ] **A18** A unit edited (chunker boundary moved) by a re-fire is replaced in place; FTS reflects the new text within one fire. (`test_recompute_handles_tail_unit` plus a follow-up FTS query assertion.)
- [ ] **A19** Per-transcript debounce buckets are independent: paused transcript A does not block live transcript B. (Add `test_pause_on_one_transcript_does_not_block_other`.)
- [ ] **A20** Indexer metrics emitted: `indexer_fires_total{transcript_id}`, `indexer_paused_total{transcript_id}`, `indexer_dlq_depth`, `indexer_listener_reconnect_total`. Surfaces in `/health` JSON (the API stage already proxies pipeline metrics).
