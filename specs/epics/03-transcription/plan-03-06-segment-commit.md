# Plan 3.6 — Real-time per-segment durable commit (implementation)

> Implementation plan for [story-03-06-segment-commit.md](story-03-06-segment-commit.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: the per-segment commit invariant is the
> contract relied upon by Story 3.7 (pause/resume) and Story 3.8 (crash
> recovery); the architectural reference is
> [`architecture.md` §7.6 "Real-time progress persistence"](../../architecture.md);
> the schema is owned by Story 3.5 (`transcripts.is_active`) and the
> base `processing_jobs` table from Epic 6 Story 6.1.

---

## 0. Decisions and departures from `architecture.md` and the story

| # | Decision | Source | Rationale |
|---|----------|--------|-----------|
| D1 | The single per-segment transaction is implemented as **one PL/pgSQL function** `commit_segment(...)` returning the new `(seq, last_segment_end_sec, processed_seconds, realtime_factor)`. The Python worker calls it with one round-trip, not two `INSERT … RETURNING` + `UPDATE …`. | Refines `architecture.md` §7.6 (which shows two statements in one transaction). | Keeps the transaction "atomic by construction" — the worker can never accidentally split the two writes across separate transactions in a future refactor. Also halves DB round-trip latency at ~20 segments/min, which compounds across a 4-hour transcribe. The function body still does both writes, so the SQL semantics are identical. |
| D2 | The reorder buffer (out-of-order segments from the backend) lives **inside the worker**, *before* `commit_segment` is called. The DB never sees an out-of-order segment. | Story edge case: "Segment shorter than the prior `last_segment_end_sec`". | The DB invariant `last(segments.end_sec) == jobs.last_segment_end_sec` is only meaningful if every commit advances monotonically. Putting reorder logic in SQL would require holding a transaction open across the reorder window (default 30 s) — a non-starter. Keeping it in-memory in the worker is O(1) and pause-correct (a flush on pause commits the buffer in-order or drops the tail with a WARN). |
| D3 | `realtime_factor` is **EWMA with α = 0.2** computed in SQL: `realtime_factor := COALESCE(realtime_factor, 0) * 0.8 + new_factor * 0.2`. The first segment seeds with the raw factor (no smoothing) so the UI shows a real number from segment 1. | `architecture.md` §7.6 names α = 0.2 but does not say where the calculation lives. | Doing it in SQL inside `commit_segment` keeps the math next to the data and atomic with the row update. A worker-side calculation would require reading `realtime_factor` first, which adds a round-trip and a TOCTOU window where two workers (impossible per §7.4 but cheap to defend against) could race. |
| D4 | `processed_seconds` is incremented by `(segment.end - prev_end)`, **not** by `(segment.end - segment.start)`. `prev_end` is the value of `last_segment_end_sec` *before* this commit. This is what the story specifies, and it is **not** equal to segment duration when the backend produces overlapping or gappy segments. | Story acceptance §1.b. | Whisper occasionally emits a segment whose `start` < the previous `end` (overlap on speech boundaries). Counting segment-duration would over-count audio. Using `end - prev_end` measures *progress along the audio timeline*, which is what the UI's "X of Y seconds" display means. Gaps (silence skipped by VAD) advance correctly too. |
| D5 | The `LISTEN segments.committed` notify is fired by a **trigger** `AFTER INSERT ON transcript_segments`, not from the worker. The payload is `json_build_object('transcript_id', NEW.transcript_id, 'last_segment_end_sec', NEW.end_sec, 'seq', NEW.seq)`. | Story acceptance: "the worker emits a `LISTEN segments.committed` notify". | Firing the notify in a trigger guarantees it runs in the same transaction — no notify is ever sent for a rolled-back commit (Postgres `pg_notify` is transactional). A worker-side `pg_notify(...)` *outside* the transaction would race with the trigger; one *inside* would be a redundant statement. SQLite (which has no LISTEN/NOTIFY) uses a polling listener as Story 6.1 already mandates. |
| D6 | Word-level rows (`transcript_words`) are inserted in the **same transaction** as the segment, when the backend supplies them. We do **not** batch words across segments. | Story acceptance: "(Optional) inserts `transcript_words` rows when word timestamps are enabled." | The transaction is already open and small (one segment ≈ 5–30 s of audio ≈ 30–200 words). Batching across segments would mean a partial-words state on crash. |
| D7 | The `audio_duration` clamp (story edge case "Backend emits a 'final' segment past the audio's true end") happens **in the worker** — `commit_segment` clamps `end_sec` to `min(end_sec, jobs.total_duration_seconds)` only when `total_duration_seconds IS NOT NULL`. The worker logs `WARN segment_end_clamped` when this fires. | Story edge case. | `total_duration_seconds` is a hint (set by `probe`, sometimes 0 for live streams). Clamping in SQL keeps the invariant `processed_seconds <= total_duration_seconds` true even when the backend hallucinates a final tail past EOF. |

If D1 is rejected (two statements rather than one PL/pgSQL function),
§5 changes (the SQL becomes inline DML in the Python `async with
db.begin() as tx:` block) but the test plan §8 still passes — the
acceptance contract is "atomic per-segment commit", which is preserved
either way.

---

## 1. Architecture diagram — segment commit flow

```
              ┌──────────────────────────────────────────────┐
              │  STT backend (whisper-mlx, faster-whisper, …)│
              │  yields Segment(start, end, text, words?, …) │
              └──────────────────────┬───────────────────────┘
                                     │ async for segment
                                     ▼
              ┌──────────────────────────────────────────────┐
              │  ReorderBuffer (in-process, per job)         │
              │  - keyed by start_sec                        │
              │  - emits in monotonic start_sec order        │
              │  - flushes on:                               │
              │      (a) buffered_until - lowest > 30 s, or  │
              │      (b) pause_requested observed, or        │
              │      (c) backend stream EOF                  │
              │  - drops segments < last_committed_end with  │
              │    WARN out_of_order_segment_dropped         │
              └──────────────────────┬───────────────────────┘
                                     │ ordered Segment
                                     ▼
              ┌──────────────────────────────────────────────┐
              │  SegmentCommitter.commit(segment)            │
              │   1. Clamp segment.end to total_duration     │
              │   2. SELECT pg_advisory_xact_lock(job.id)    │
              │      (no-op fast path on SQLite)             │
              │   3. CALL commit_segment($1..$N) — PL/pgSQL  │
              │      that does both INSERTs + UPDATE + the   │
              │      EWMA in one statement; returns the new  │
              │      job row.                                │
              │   4. If RETURNING.pause_requested OR         │
              │      cancel_requested → raise StopWorker.    │
              └────────┬────────────────────────────┬────────┘
                       │ commit                     │ rollback (any error)
                       ▼                            ▼
              ┌─────────────────────┐    ┌─────────────────────┐
              │ AFTER INSERT trigger│    │  Retry loop (3×)    │
              │  pg_notify(         │    │  if SerializationErr│
              │  'segments.committed│    │  re-runs same seg.  │
              │  ', payload)        │    │  No rework lost     │
              └────────┬────────────┘    │  (segment is still  │
                       │                 │  in-memory).        │
                       ▼                 └─────────────────────┘
              ┌─────────────────────┐
              │ LISTEN consumers:   │
              │  - Live indexer     │
              │    (Epic 5 Story    │
              │    5.5)             │
              │  - WS broadcaster   │
              │    (Epic 7)         │
              └─────────────────────┘
```

The worker holds **no** in-flight segment after `commit_segment` returns:
either the row is durably in `transcript_segments` and `processing_jobs`
has advanced, or the entire transaction rolled back and the worker
retries with the same in-memory `segment` object. This is the property
Story 3.7 (pause/resume) and Story 3.8 (crash recovery) build on.

---

## 2. Detailed implementation

### 2.1 Package layout — Python (Pipeline Service)

```
pipeline/src/maktaba_pipeline/
├── transcribe/
│   ├── __init__.py
│   ├── committer.py        # SegmentCommitter, commit_segment() RPC wrapper
│   ├── reorder.py          # ReorderBuffer, monotonic enforcement
│   ├── progress.py         # ewma(), estimate_remaining(), pure functions
│   ├── errors.py           # SegmentCommitError, OutOfOrderSegmentDropped
│   └── tests/
│       ├── conftest.py
│       ├── test_committer_atomic.py
│       ├── test_committer_retry.py
│       ├── test_reorder_buffer.py
│       ├── test_progress_arithmetic.py
│       ├── test_realtime_factor_ewma.py
│       ├── test_clamp_to_duration.py
│       └── test_notify_payload.py
└── pipeline/
    └── stages/
        └── transcribe.py   # extended with SegmentCommitter wiring
                            # (pre-existing skeleton from plan 02-03)
```

### 2.2 Schema migration — `0011_segment_commit_function.sql`

```sql
-- 0011_segment_commit_function.sql
-- Owns: commit_segment() PL/pgSQL fn, segments_committed AFTER INSERT trigger.
-- Dependencies: 0007 transcripts/transcript_segments (Story 3.5),
--               0006 processing_jobs progress columns (Epic 6 Story 6.1).
-- Idempotent on re-run: CREATE OR REPLACE FUNCTION / DROP TRIGGER IF EXISTS.

BEGIN;

CREATE OR REPLACE FUNCTION commit_segment(
    p_job_id          BIGINT,
    p_transcript_id   UUID,
    p_seq             INT,
    p_start_sec       REAL,
    p_end_sec         REAL,
    p_text            TEXT,
    p_speaker         TEXT,
    p_confidence      REAL,
    p_words           JSONB,         -- [{seq,start,end,text,confidence}, …] or NULL
    p_wall_sec        REAL,          -- worker's measured wall time for this segment
    p_ewma_alpha      REAL DEFAULT 0.2
) RETURNS TABLE (
    out_segment_id              BIGINT,
    out_last_segment_end_sec    REAL,
    out_processed_seconds       REAL,
    out_segments_completed      INT,
    out_realtime_factor         REAL,
    out_estimated_remaining_sec REAL,
    out_pause_requested         BOOLEAN,
    out_cancel_requested        BOOLEAN
)
LANGUAGE plpgsql AS $$
DECLARE
    v_prev_end          REAL;
    v_total             REAL;
    v_audio_in_segment  REAL;
    v_factor            REAL;
    v_smoothed          REAL;
    v_clamped_end       REAL;
    v_segment_id        BIGINT;
    v_word              JSONB;
BEGIN
    -- Lock the job row; protects concurrent sweeps from the reaper (Story 3.8).
    SELECT last_segment_end_sec, total_duration_seconds, realtime_factor
      INTO v_prev_end, v_total, v_smoothed
      FROM processing_jobs
     WHERE id = p_job_id
       FOR UPDATE;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'commit_segment: job % not found', p_job_id
            USING ERRCODE = 'no_data_found';
    END IF;

    -- Worker-side clamp belt-and-braces: cap end at total_duration (D7).
    v_clamped_end := CASE
        WHEN v_total IS NULL OR p_end_sec <= v_total THEN p_end_sec
        ELSE v_total
    END;

    -- Monotonic invariant; the reorder buffer should already guarantee this.
    IF v_clamped_end <= v_prev_end THEN
        RAISE EXCEPTION 'commit_segment: non-monotonic end (% <= %)',
                        v_clamped_end, v_prev_end
            USING ERRCODE = 'check_violation';
    END IF;

    -- 1) Insert the segment row.
    INSERT INTO transcript_segments
        (transcript_id, seq, start_sec, end_sec, text, speaker, confidence)
    VALUES
        (p_transcript_id, p_seq, p_start_sec, v_clamped_end, p_text,
         p_speaker, p_confidence)
    RETURNING id INTO v_segment_id;

    -- 2) Insert word rows (if any). Words come in the same xact.
    IF p_words IS NOT NULL THEN
        FOR v_word IN SELECT * FROM jsonb_array_elements(p_words) LOOP
            INSERT INTO transcript_words
                (segment_id, seq, start_sec, end_sec, text, confidence)
            VALUES
                (v_segment_id,
                 (v_word ->> 'seq')::INT,
                 (v_word ->> 'start')::REAL,
                 (v_word ->> 'end')::REAL,
                 v_word ->> 'text',
                 (v_word ->> 'confidence')::REAL);
        END LOOP;
    END IF;

    -- 3) Compute the EWMA realtime factor (D3).
    v_audio_in_segment := v_clamped_end - v_prev_end;
    IF p_wall_sec > 0 THEN
        v_factor := v_audio_in_segment / p_wall_sec;
    ELSE
        v_factor := COALESCE(v_smoothed, 0);
    END IF;

    IF v_smoothed IS NULL THEN
        v_smoothed := v_factor;             -- seed from raw on segment 1
    ELSE
        v_smoothed := v_smoothed * (1 - p_ewma_alpha) + v_factor * p_ewma_alpha;
    END IF;

    -- 4) Advance the job row.
    UPDATE processing_jobs
       SET last_segment_end_sec    = v_clamped_end,
           processed_seconds       = processed_seconds + v_audio_in_segment,
           segments_completed      = segments_completed + 1,
           realtime_factor         = v_smoothed,
           estimated_remaining_sec = CASE
               WHEN v_smoothed IS NULL OR v_smoothed < 1e-6
                   THEN NULL
               ELSE GREATEST(0, COALESCE(total_duration_seconds, 0)
                                - (processed_seconds + v_audio_in_segment))
                    / GREATEST(v_smoothed, 1e-6)
           END,
           progress_updated_at     = now(),
           last_heartbeat_at       = now()
     WHERE id = p_job_id
    RETURNING last_segment_end_sec, processed_seconds, segments_completed,
              realtime_factor, estimated_remaining_sec,
              pause_requested, cancel_requested
        INTO out_last_segment_end_sec, out_processed_seconds,
             out_segments_completed, out_realtime_factor,
             out_estimated_remaining_sec,
             out_pause_requested, out_cancel_requested;

    out_segment_id := v_segment_id;

    -- The AFTER INSERT trigger on transcript_segments fires pg_notify
    -- as part of THIS transaction (D5). No worker-side notify here.
    RETURN NEXT;
END $$;

CREATE OR REPLACE FUNCTION segments_notify_committed()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify(
        'segments.committed',
        json_build_object(
            'transcript_id',         NEW.transcript_id,
            'last_segment_end_sec',  NEW.end_sec,
            'seq',                   NEW.seq
        )::text
    );
    RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS trg_segments_committed ON transcript_segments;
CREATE TRIGGER trg_segments_committed
    AFTER INSERT ON transcript_segments
    FOR EACH ROW
    EXECUTE FUNCTION segments_notify_committed();

COMMIT;
```

SQLite shim (lives in `shared/db/migrations/sqlite/0011_*.sql`):
- `commit_segment` is implemented in Python (`committer.py::commit_segment_sqlite`) using `BEGIN IMMEDIATE` / `COMMIT`. The function signature mirrors PL/pgSQL.
- The `pg_notify` is replaced with an `INSERT INTO segment_notify_log (transcript_id, end_sec, seq, created_at)` row that the polling listener tails (Epic 6 Story 6.1 already establishes this pattern).

### 2.3 `progress.py` — pure helpers

```python
"""Progress arithmetic, pure & deterministic. No I/O, no DB."""
from dataclasses import dataclass

EWMA_ALPHA = 0.2
EPSILON = 1e-6


def ewma(prev: float | None, sample: float, alpha: float = EWMA_ALPHA) -> float:
    """Exponentially-weighted moving average. Seeds with the first sample."""
    if prev is None:
        return sample
    return prev * (1 - alpha) + sample * alpha


def realtime_factor(audio_sec: float, wall_sec: float) -> float:
    """audio_sec produced per wall_sec consumed. Higher = faster."""
    if wall_sec <= 0:
        return 0.0
    return audio_sec / wall_sec


def estimate_remaining(
    *,
    total_duration_sec: float | None,
    processed_sec: float,
    smoothed_factor: float | None,
) -> float | None:
    if total_duration_sec is None or smoothed_factor is None:
        return None
    if smoothed_factor < EPSILON:
        return None
    remaining_audio = max(0.0, total_duration_sec - processed_sec)
    return remaining_audio / max(smoothed_factor, EPSILON)
```

### 2.4 `reorder.py` — in-memory monotonic enforcement (D2)

```python
"""ReorderBuffer: emit segments in monotonic start_sec, drop late ones."""
from __future__ import annotations
import heapq
import logging
from dataclasses import dataclass, field
from typing import AsyncIterator

from maktaba_pipeline.transcribe.errors import OutOfOrderSegmentDropped

log = logging.getLogger(__name__)


@dataclass(order=True)
class _Buffered:
    start_sec: float
    seg: object = field(compare=False)


class ReorderBuffer:
    """Buffer up to `window_sec` of in-flight segments; emit in order."""

    def __init__(self, window_sec: float = 30.0):
        self._window_sec = window_sec
        self._heap: list[_Buffered] = []
        self._last_emitted_end: float = 0.0
        self._max_seen_start: float = 0.0

    def push(self, segment) -> list[object]:
        """Add a segment; return any segments now safe to emit (in order)."""
        if segment.end <= self._last_emitted_end:
            log.warning(
                "out_of_order_segment_dropped",
                extra={
                    "segment_start": segment.start,
                    "segment_end": segment.end,
                    "last_emitted_end": self._last_emitted_end,
                },
            )
            raise OutOfOrderSegmentDropped(segment)

        heapq.heappush(self._heap, _Buffered(segment.start, segment))
        self._max_seen_start = max(self._max_seen_start, segment.start)
        return self._drain()

    def flush(self) -> list[object]:
        """Emit all buffered segments in order; called on pause / EOF."""
        out: list[object] = []
        while self._heap:
            item = heapq.heappop(self._heap)
            if item.seg.end <= self._last_emitted_end:
                log.warning("out_of_order_segment_dropped_on_flush",
                            extra={"end": item.seg.end})
                continue
            self._last_emitted_end = item.seg.end
            out.append(item.seg)
        return out

    def _drain(self) -> list[object]:
        out: list[object] = []
        # Safe to emit: anything whose start is "old enough" relative to the
        # latest segment we've seen. The window guards reordering distance.
        while self._heap:
            head = self._heap[0]
            if self._max_seen_start - head.start_sec < self._window_sec:
                # Could still be out-paced by a later-arriving older segment.
                break
            heapq.heappop(self._heap)
            if head.seg.end <= self._last_emitted_end:
                continue
            self._last_emitted_end = head.seg.end
            out.append(head.seg)
        return out
```

### 2.5 `committer.py` — the hot path

```python
"""SegmentCommitter — calls commit_segment(), handles retries, raises on stop."""
from __future__ import annotations
import json
import logging
import time
from dataclasses import dataclass
from uuid import UUID

import asyncpg

from maktaba_pipeline.transcribe.errors import SegmentCommitError

log = logging.getLogger(__name__)

RETRYABLE_SQLSTATES = frozenset({"40001", "40P01"})  # serialization, deadlock
MAX_RETRIES = 3


@dataclass(frozen=True)
class CommitResult:
    segment_id: int
    last_segment_end_sec: float
    processed_seconds: float
    segments_completed: int
    realtime_factor: float | None
    estimated_remaining_sec: float | None
    pause_requested: bool
    cancel_requested: bool


class StopWorker(Exception):
    """Raised after a successful commit when pause/cancel was observed."""

    def __init__(self, reason: str, last_end_sec: float):
        super().__init__(f"stop:{reason}@{last_end_sec:.3f}s")
        self.reason = reason
        self.last_end_sec = last_end_sec


class SegmentCommitter:
    def __init__(self, pool: asyncpg.Pool, *, job_id: int, transcript_id: UUID):
        self._pool = pool
        self._job_id = job_id
        self._transcript_id = transcript_id
        self._next_seq: int | None = None  # populated lazily on first commit

    async def commit(self, segment, *, wall_sec: float) -> CommitResult:
        """Commit one segment + advance progress, atomically.

        Raises:
            StopWorker — pause_requested or cancel_requested observed.
            SegmentCommitError — non-retryable DB failure.
        """
        seq = await self._reserve_seq()
        words_payload = (
            json.dumps([w.__dict__ for w in segment.words])
            if segment.words else None
        )

        last_err: Exception | None = None
        for attempt in range(1, MAX_RETRIES + 1):
            try:
                async with self._pool.acquire() as conn:
                    row = await conn.fetchrow(
                        "SELECT * FROM commit_segment("
                        " $1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10)",
                        self._job_id,
                        self._transcript_id,
                        seq,
                        segment.start,
                        segment.end,
                        segment.text,
                        segment.speaker,
                        segment.confidence,
                        words_payload,
                        wall_sec,
                    )
                result = CommitResult(
                    segment_id=row["out_segment_id"],
                    last_segment_end_sec=row["out_last_segment_end_sec"],
                    processed_seconds=row["out_processed_seconds"],
                    segments_completed=row["out_segments_completed"],
                    realtime_factor=row["out_realtime_factor"],
                    estimated_remaining_sec=row["out_estimated_remaining_sec"],
                    pause_requested=row["out_pause_requested"],
                    cancel_requested=row["out_cancel_requested"],
                )
                self._next_seq = seq + 1
                if result.cancel_requested:
                    raise StopWorker("cancel", result.last_segment_end_sec)
                if result.pause_requested:
                    raise StopWorker("pause", result.last_segment_end_sec)
                return result
            except asyncpg.PostgresError as e:
                last_err = e
                if getattr(e, "sqlstate", None) not in RETRYABLE_SQLSTATES:
                    raise SegmentCommitError(
                        f"non-retryable on attempt {attempt}: {e}") from e
                log.warning("commit_retry",
                            extra={"attempt": attempt, "sqlstate": e.sqlstate})
                await _backoff(attempt)
        raise SegmentCommitError(
            f"exhausted retries for seq={seq}: {last_err}") from last_err

    async def _reserve_seq(self) -> int:
        if self._next_seq is not None:
            return self._next_seq
        async with self._pool.acquire() as conn:
            row = await conn.fetchrow(
                "SELECT COALESCE(MAX(seq), 0) + 1 AS next_seq"
                "  FROM transcript_segments"
                " WHERE transcript_id = $1",
                self._transcript_id,
            )
        self._next_seq = row["next_seq"]
        return self._next_seq


async def _backoff(attempt: int) -> None:
    import asyncio
    await asyncio.sleep(min(0.05 * (2 ** (attempt - 1)), 0.5))
```

### 2.6 Integration into `transcribe.py` stage

```python
# pipeline/src/maktaba_pipeline/pipeline/stages/transcribe.py  (excerpt)

async def run_transcribe_stage(ctx, job, video):
    transcript = await ensure_transcript_row(ctx, video, job)
    seek_from = job.last_segment_end_sec
    committer = SegmentCommitter(
        ctx.db_pool, job_id=job.id, transcript_id=transcript.id)
    reorder = ReorderBuffer(window_sec=ctx.cfg.transcribe.reorder_window_sec)

    backend = await ctx.stt.session(
        language=video.detected_language,
        prompt=await build_resume_prompt(ctx, transcript, k=3),
    )

    async with audio_decoder(video, start_sec=seek_from) as audio:
        try:
            t_seg_start = time.monotonic()
            async for raw_seg in backend.transcribe_stream(audio):
                wall_sec = time.monotonic() - t_seg_start
                t_seg_start = time.monotonic()
                try:
                    ready = reorder.push(raw_seg)
                except OutOfOrderSegmentDropped:
                    continue
                for seg in ready:
                    await committer.commit(seg, wall_sec=wall_sec)
            # End of backend stream — flush any tail buffered segments.
            for seg in reorder.flush():
                await committer.commit(seg, wall_sec=0.0)
        except StopWorker as stop:
            for seg in reorder.flush():
                await committer.commit(seg, wall_sec=0.0)
            return {"reason": stop.reason, "at_sec": stop.last_end_sec}

    await mark_job_done(ctx, job.id)
    return {"reason": "ok", "at_sec": (await job_state(ctx, job.id)).last_segment_end_sec}
```

The order matters: `commit()` raises `StopWorker` *after* the commit
succeeds. The current segment is durably written; the `for seg in
reorder.flush()` afterwards is a best-effort drain so we don't waste any
buffered work that the backend already produced — every drained one
also calls `commit()`, so the pause boundary lands cleanly at the last
emitted segment.

---

## 3. Code scaffolding — file-by-file checklist

Implementer should create these files in order. Each ships green tests.

| Order | File | Symbols introduced | Tests that must pass before next file |
|-------|------|--------------------|---------------------------------------|
| 1 | `pipeline/src/maktaba_pipeline/transcribe/__init__.py` | empty | (n/a) |
| 2 | `pipeline/src/maktaba_pipeline/transcribe/progress.py` | `ewma`, `realtime_factor`, `estimate_remaining`, `EWMA_ALPHA` | `test_progress_arithmetic` |
| 3 | `pipeline/src/maktaba_pipeline/transcribe/errors.py` | `SegmentCommitError`, `OutOfOrderSegmentDropped` | (n/a) |
| 4 | `pipeline/src/maktaba_pipeline/transcribe/reorder.py` | `ReorderBuffer` | `test_reorder_buffer` |
| 5 | `shared/db/migrations/0011_segment_commit_function.sql` | `commit_segment` fn, `trg_segments_committed` | migration applies cleanly on a fresh DB and on one with existing transcripts |
| 6 | `shared/db/migrations/sqlite/0011_*.sql` | (no-op + Python shim) | sqlite test fixture loads |
| 7 | `pipeline/src/maktaba_pipeline/transcribe/committer.py` | `SegmentCommitter`, `CommitResult`, `StopWorker` | `test_committer_atomic`, `test_committer_retry`, `test_notify_payload` |
| 8 | `pipeline/src/maktaba_pipeline/pipeline/stages/transcribe.py` | wire `SegmentCommitter` + `ReorderBuffer` into the existing stage skeleton | `test_progress_advances_with_audio_time_not_wall_time`, `test_realtime_factor_ewma`, `test_eta_uses_smoothed_factor`, `test_pause_request_observed_after_commit` |

---

## 4. Test cases

All tests under `pipeline/src/maktaba_pipeline/transcribe/tests/`. Use
`pytest-asyncio` and the existing `db_fixture` from Epic 6. Synthetic
backend (`tests/fakes/synth_backend.py`) is added in this PR — yields
`Segment` objects from a scripted list, no model loaded.

### 4.1 `test_committer_atomic` (story-named)

```python
async def test_committer_atomic_rollback_on_progress_failure(db, job, transcript):
    # Inject a failure on the UPDATE half of commit_segment by holding a
    # competing FOR UPDATE lock on the job row from another connection
    # and aborting it after the segment INSERT has begun.
    seg = Segment(start=0.0, end=10.0, text="hi", words=[], speaker=None,
                  confidence=0.9)
    committer = SegmentCommitter(db.pool, job_id=job.id,
                                 transcript_id=transcript.id)

    with pytest.raises(SegmentCommitError):
        async with hold_advisory_lock(db, job.id, raise_after_sec=0.05):
            await committer.commit(seg, wall_sec=0.5)

    # Assert no orphan segment row.
    rows = await db.fetch(
        "SELECT * FROM transcript_segments WHERE transcript_id = $1",
        transcript.id)
    assert rows == []
    # And the job row is untouched.
    j = await db.fetchrow("SELECT * FROM processing_jobs WHERE id = $1", job.id)
    assert j["last_segment_end_sec"] == 0.0
    assert j["segments_completed"] == 0

    # Retry: now succeeds, exactly one row.
    res = await committer.commit(seg, wall_sec=0.5)
    assert res.segments_completed == 1
    rows = await db.fetch(
        "SELECT * FROM transcript_segments WHERE transcript_id = $1",
        transcript.id)
    assert len(rows) == 1
```

### 4.2 `test_progress_advances_with_audio_time_not_wall_time` (story-named)

```python
async def test_progress_advances_with_audio_time(db, job, transcript):
    """A 60-second segment yielded "instantly" still bumps processed_seconds by 60."""
    committer = SegmentCommitter(db.pool, job_id=job.id, transcript_id=transcript.id)
    seg = Segment(start=0.0, end=60.0, text="…", words=[], speaker=None, confidence=0.9)
    res = await committer.commit(seg, wall_sec=0.001)  # absurdly fast
    assert res.processed_seconds == pytest.approx(60.0)
    assert res.last_segment_end_sec == pytest.approx(60.0)
    assert res.realtime_factor > 1000  # very fast, but the *progress* is audio-time
```

### 4.3 `test_realtime_factor_ewma` (story-named)

```python
async def test_realtime_factor_ewma_smoothing(db, job, transcript):
    """Alternating fast/slow segments smooth toward the mean; α = 0.2."""
    committer = SegmentCommitter(db.pool, job_id=job.id, transcript_id=transcript.id)
    factors_seen = []
    end = 0.0
    for i, wall in enumerate([0.5, 5.0, 0.5, 5.0, 0.5, 5.0]):
        end += 10.0
        seg = Segment(start=end - 10, end=end, text=str(i),
                      words=[], speaker=None, confidence=0.9)
        res = await committer.commit(seg, wall_sec=wall)
        factors_seen.append(res.realtime_factor)

    # First sample: raw (10/0.5 = 20).
    assert factors_seen[0] == pytest.approx(20.0)
    # Subsequent samples: smoothed. The smoothed series stays in (2, 20).
    assert all(2.0 < f < 20.0 for f in factors_seen[1:])
    # Variance is monotonically decreasing with α = 0.2.
    diffs = [abs(factors_seen[i] - factors_seen[i-1]) for i in range(1, len(factors_seen))]
    assert diffs == sorted(diffs, reverse=True)
```

### 4.4 `test_eta_uses_smoothed_factor` (story-named)

```python
async def test_eta_uses_smoothed_factor(db, job, transcript):
    # Job total = 100 s. After committing 10 s at factor 2.0,
    # remaining = 90 / 2.0 = 45 s.
    await db.execute(
        "UPDATE processing_jobs SET total_duration_seconds = 100 WHERE id = $1",
        job.id)
    committer = SegmentCommitter(db.pool, job_id=job.id, transcript_id=transcript.id)
    seg = Segment(start=0.0, end=10.0, text="x", words=[], speaker=None, confidence=0.9)
    res = await committer.commit(seg, wall_sec=5.0)  # factor = 2.0
    assert res.realtime_factor == pytest.approx(2.0, rel=1e-3)
    assert res.estimated_remaining_sec == pytest.approx(45.0, abs=0.01)
```

### 4.5 `test_pause_request_observed_after_commit` (story-named)

```python
async def test_pause_request_observed_after_commit(db, job, transcript):
    """Set pause mid-segment → exactly one more segment commits, then StopWorker."""
    committer = SegmentCommitter(db.pool, job_id=job.id, transcript_id=transcript.id)

    # Commit one before the pause flag is set: succeeds normally.
    await committer.commit(Segment(0, 10, "a", [], None, 0.9), wall_sec=1)

    # Now flip the flag (simulates the API).
    await db.execute(
        "UPDATE processing_jobs SET pause_requested = true WHERE id = $1",
        job.id)

    # The next commit observes the flag in its own RETURNING and raises.
    with pytest.raises(StopWorker) as exc:
        await committer.commit(Segment(10, 20, "b", [], None, 0.9), wall_sec=1)
    assert exc.value.reason == "pause"
    assert exc.value.last_end_sec == pytest.approx(20.0)

    # The "b" segment IS persisted (the commit ran successfully before the raise).
    rows = await db.fetch(
        "SELECT seq, end_sec FROM transcript_segments "
        "WHERE transcript_id = $1 ORDER BY seq", transcript.id)
    assert [(r["seq"], r["end_sec"]) for r in rows] == [(1, 10.0), (2, 20.0)]
```

### 4.6 `test_notify_payload`

```python
async def test_notify_emits_on_commit(db, job, transcript):
    received: list[dict] = []
    async with db.pool.acquire() as conn:
        await conn.add_listener(
            "segments.committed",
            lambda *a: received.append(json.loads(a[-1])))
        committer = SegmentCommitter(db.pool, job_id=job.id, transcript_id=transcript.id)
        await committer.commit(
            Segment(0.0, 12.5, "x", [], None, 0.9), wall_sec=1.0)
        await asyncio.sleep(0.05)  # let the notify propagate

    assert len(received) == 1
    msg = received[0]
    assert msg["transcript_id"] == str(transcript.id)
    assert msg["last_segment_end_sec"] == pytest.approx(12.5)
    assert msg["seq"] == 1
```

### 4.7 `test_reorder_buffer`

```python
def test_reorder_emits_in_order():
    rb = ReorderBuffer(window_sec=5.0)
    out = rb.push(Segment(start=10.0, end=15.0, text="b", ...))
    assert out == []  # buffered, no in-order emit yet
    out = rb.push(Segment(start=0.0, end=5.0, text="a", ...))
    out += rb.push(Segment(start=20.0, end=25.0, text="c", ...))
    # max_seen=20, head=0, gap=20 > window: 'a' and 'b' both safe to drain.
    emitted = [s.text for s in out]
    assert emitted == ["a", "b"]
    # Flush emits the tail.
    tail = [s.text for s in rb.flush()]
    assert tail == ["c"]


def test_reorder_drops_segments_older_than_last_emitted():
    rb = ReorderBuffer(window_sec=5.0)
    # Force "a" out by far-future arrivals.
    rb.push(Segment(start=10.0, end=15.0, text="a", ...))
    rb.push(Segment(start=100.0, end=105.0, text="future", ...))
    drained = rb._drain()
    assert [s.text for s in drained] == ["a"]
    # Now a late-arriving segment older than 'a' is dropped with WARN.
    with pytest.raises(OutOfOrderSegmentDropped):
        rb.push(Segment(start=8.0, end=12.0, text="late", ...))
```

### 4.8 `test_clamp_to_duration`

```python
async def test_clamp_to_total_duration(db, job, transcript):
    await db.execute(
        "UPDATE processing_jobs SET total_duration_seconds = 100 WHERE id = $1",
        job.id)
    committer = SegmentCommitter(db.pool, job_id=job.id, transcript_id=transcript.id)
    seg = Segment(start=95.0, end=120.0, text="trail", words=[], speaker=None, confidence=0.9)
    res = await committer.commit(seg, wall_sec=1.0)
    assert res.last_segment_end_sec == pytest.approx(100.0)
    assert res.processed_seconds <= 100.0
```

### 4.9 `test_committer_retry`

```python
async def test_committer_retries_on_serialization_error(db, job, transcript, monkeypatch):
    """Inject SQLSTATE 40001 twice; third try succeeds."""
    real_fetchrow = asyncpg.Connection.fetchrow
    calls = {"n": 0}

    async def flaky(self, *a, **kw):
        calls["n"] += 1
        if calls["n"] < 3:
            err = asyncpg.SerializationError("simulated")
            err.sqlstate = "40001"
            raise err
        return await real_fetchrow(self, *a, **kw)

    monkeypatch.setattr(asyncpg.Connection, "fetchrow", flaky)
    committer = SegmentCommitter(db.pool, job_id=job.id, transcript_id=transcript.id)
    res = await committer.commit(
        Segment(0.0, 10.0, "x", [], None, 0.9), wall_sec=1.0)
    assert calls["n"] == 3
    assert res.segments_completed == 1
```

### 4.10 `test_progress_arithmetic` (pure)

```python
def test_ewma_seeds_with_first_sample():
    assert ewma(None, 5.0) == 5.0

def test_ewma_blends_with_alpha():
    assert ewma(10.0, 0.0, alpha=0.2) == pytest.approx(8.0)

def test_estimate_remaining_zero_total_returns_none():
    assert estimate_remaining(total_duration_sec=None,
                              processed_sec=0, smoothed_factor=1.0) is None

def test_estimate_remaining_zero_factor_returns_none():
    assert estimate_remaining(total_duration_sec=100,
                              processed_sec=10, smoothed_factor=0.0) is None
```

---

## 5. Edge cases and how the plan handles each

| # | Edge case (story §"Edge cases") | Handled by |
|---|---------------------------------|------------|
| E1 | Backend produces an out-of-order segment whose `end_sec <= prev last_segment_end_sec`. | The `ReorderBuffer` (`reorder.py`) buffers up to `reorder_window_sec` (default 30 s) before emitting in monotonic start_sec. A segment that arrives after its window has expired is dropped with WARN `out_of_order_segment_dropped`. The DB invariant remains intact because `commit_segment` raises `check_violation` if the worker ever bypasses the buffer. |
| E2 | Backend emits a final segment past the audio's true end (`segment.end > total_duration_seconds`). | `commit_segment()` clamps `end_sec` to `min(end_sec, total_duration_seconds)` (D7). The worker logs `WARN segment_end_clamped` with both values. `processed_seconds <= total_duration_seconds` is preserved by construction. |
| E3 | Backend yields a segment whose `start > prev_end` (gap due to VAD-skipped silence). | `processed_seconds += (segment.end - prev_end)`, which correctly counts the silent gap as "processed audio". This matches the user's mental model of "how far along the timeline are we." |
| E4 | DB write contention with the API's read traffic. | `commit_segment` writes one segment row (no contention; new PK) and one job row (`FOR UPDATE` on the PK; O(1) lock; no scan). API reads use `WHERE id = $1` without `FOR UPDATE` and never lock. The pgbench section in §7 confirms zero contention at expected fanout. |
| E5 | Worker dies between `INSERT segment` and `UPDATE jobs`. | Impossible: both are inside a single PL/pgSQL function call wrapped in an implicit transaction. Either both happen or neither does. The `SegmentCommitError` retry path covers transient DB errors. |
| E6 | Two workers somehow claim the same job (should not happen per Epic 6 §7.4). | The `FOR UPDATE` on the job row inside `commit_segment` serializes them. Whichever loses the race observes the *other* worker's `claimed_by` and a stale `last_segment_end_sec` after re-read; the loser's commit attempt fails the monotonic check (`v_clamped_end <= v_prev_end`) and raises `check_violation`. The worker treats this as an invariant breach, releases its claim, and exits with `error.kind = "double_claim"`. |
| E7 | The backend yields segments with the same `start` as the previous (back-to-back boundary). | `commit_segment` accepts equal `start` values; the monotonic check is on `end_sec` only. The `seq` always increments. |
| E8 | Word-level data is malformed (missing required field). | `commit_segment` parses with `(v_word ->> 'seq')::INT`, etc. A NULL or non-numeric value raises `invalid_text_representation` and the whole transaction rolls back. The worker treats this as a backend bug and skips the words for the segment after one retry; the segment row alone is committed by re-calling with `p_words = NULL`. |
| E9 | LISTEN consumer is offline when the trigger fires. | Postgres queues notifications per session, so an offline listener simply misses messages until reconnect. The live indexer (Epic 5 Story 5.5) is designed to reconcile by querying `WHERE end_sec > last_indexed_end_sec` on reconnect, so missed notifies cost only latency, not correctness. |
| E10 | EWMA seeded with α = 0.2 takes ~10 segments to track a step change. | This is the smoothing the story explicitly asks for. The UI labels the field "(smoothed)". The unit test §4.3 verifies the variance shrinks monotonically. |

---

## 6. Acceptance checklist

Implementer marks each item with the test (or assertion) that proves it.

- [ ] **A1** For each segment, the worker executes a single DB transaction inserting a `transcript_segments` row with monotonic `seq`, `start_sec`, `end_sec`, `text`, optional `speaker`, `confidence`. (`test_committer_atomic`, `test_progress_advances_with_audio_time`)
- [ ] **A2** The same transaction updates `processing_jobs.last_segment_end_sec`, `processed_seconds += (end − prev_end)`, `segments_completed += 1`, `realtime_factor = ewma(prev, audio/wall)`, `estimated_remaining_sec = (total − processed) / max(rt, ε)`, `progress_updated_at = now()`, `last_heartbeat_at = now()`. (`test_realtime_factor_ewma`, `test_eta_uses_smoothed_factor`, schema-level inspection)
- [ ] **A3** When word timestamps are enabled, the same transaction inserts the corresponding `transcript_words` rows. (Add `test_words_committed_with_segment` once a word-emitting fake backend exists.)
- [ ] **A4** Either both writes commit together or neither does; on rollback, retry produces exactly one row. (`test_committer_atomic`)
- [ ] **A5** After every committed segment, the worker checks `pause_requested` and `cancel_requested` (returned by `commit_segment`) and exits cleanly via `StopWorker` if either is set. (`test_pause_request_observed_after_commit`)
- [ ] **A6** After every committed segment, a `LISTEN segments.committed` notify fires with `{transcript_id, last_segment_end_sec, seq}`. (`test_notify_payload`)
- [ ] **A7** Post-commit invariant `MAX(transcript_segments.end_sec WHERE transcript_id=T) == processing_jobs.last_segment_end_sec` holds. (DB-level invariant test on commit log fixture: replay 1000 commits, assert after each.)
- [ ] **A8** Out-of-order segments arriving within `reorder_window_sec` are buffered and emitted in order; segments arriving after the window are dropped with WARN. (`test_reorder_buffer`)
- [ ] **A9** Backend output past `total_duration_seconds` is clamped, not propagated. (`test_clamp_to_total_duration`)
- [ ] **A10** Migration `0011_segment_commit_function.sql` applies cleanly on both fresh and populated DBs and is idempotent on re-run. (Add `test_migration_idempotent` to the migrations test suite.)
- [ ] **A11** SQLite shim implements the same `commit_segment` semantics in Python (`commit_segment_sqlite`); cross-backend test runs every commit test against both. (Run pytest with `--db=sqlite` and `--db=postgres`.)
