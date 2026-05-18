"""Story 3.6 — atomic per-segment commit.

The hot path: every segment the backend yields is committed in a single
transaction that:

1. Inserts a row into ``transcript_segments`` with a monotonic ``seq``.
2. Updates the owning ``processing_jobs`` row with new progress markers
   (``last_segment_end_sec``, ``processed_seconds``,
   ``segments_completed``, EWMA ``realtime_factor``,
   ``estimated_remaining_sec``, ``progress_updated_at``,
   ``last_heartbeat_at``).

On Postgres :func:`commit_segment` calls the SQL function
``commit_segment(...)`` (migration 0013) which fuses both writes. On
SQLite there's no PL/pgSQL, so we issue the same INSERT + UPDATE
inside an explicit transaction.

The post-commit invariant is documented in Story 3.6:

    last(transcript_segments.end_sec) == processing_jobs.last_segment_end_sec

The reorder buffer for out-of-order segments lives in
:class:`ReorderBuffer`. The orchestrator feeds backend output into the
buffer; the buffer flushes monotonic prefixes back to
:func:`commit_segment`.
"""

from __future__ import annotations

import heapq
import time
from collections.abc import Iterable
from dataclasses import dataclass, field
from typing import Any, Protocol
from uuid import UUID

from ..db.pubsub import get_bus
from .protocol import Segment, Word

__all__ = [
    "SEGMENTS_COMMITTED",
    "CommitResult",
    "ReorderBuffer",
    "commit_segment",
]

SEGMENTS_COMMITTED = "segments.committed"


class _DBConn(Protocol):
    dialect: str

    def transaction(self) -> Any: ...

    async def fetchrow(self, sql: str, *args: Any) -> Any: ...

    async def execute(self, sql: str, *args: Any) -> Any: ...


@dataclass(slots=True, frozen=True)
class CommitResult:
    """One row's worth of after-commit telemetry for the worker."""

    segment_id: int | None
    seq: int
    end_sec: float
    accepted: bool  # False when ON CONFLICT swallowed the insert (retry path)
    words_committed: int = 0  # transcript_words rows persisted for this segment


_PG_CALL_FN = "SELECT commit_segment($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) AS id"

_SQLITE_INSERT = """
INSERT INTO transcript_segments
       (transcript_id, seq, start_sec, end_sec, text, speaker, confidence)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (transcript_id, seq) DO NOTHING
"""

_SQLITE_LASTROWID = "SELECT last_insert_rowid() AS id"

_SQLITE_PROGRESS = """
UPDATE processing_jobs
   SET last_segment_end_sec    = MAX(last_segment_end_sec, ?),
       processed_seconds       = processed_seconds + ?,
       segments_completed      = segments_completed + 1,
       realtime_factor         = ?,
       estimated_remaining_sec = ?,
       progress_updated_at     = (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
       last_heartbeat_at       = (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
 WHERE id = ?
"""

_SQLITE_PREV_END = """
SELECT last_segment_end_sec, COALESCE(realtime_factor, 0) AS realtime_factor
  FROM processing_jobs
 WHERE id = ?
"""

# Story 3.6-1.3 — optional per-word timing. Inserted in the SAME
# transaction as the owning segment so word rows can never outlive (or
# precede) a rolled-back segment. ON CONFLICT keeps a replayed seq
# idempotent exactly like the segment insert. ``segment_id`` is the row
# id ``commit_segment`` (PG fn) / the SQLite INSERT just returned.
_PG_WORD_INSERT = """
INSERT INTO transcript_words (segment_id, seq, start_sec, end_sec, text, confidence)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (segment_id, seq) DO NOTHING
"""

_SQLITE_WORD_INSERT = """
INSERT INTO transcript_words (segment_id, seq, start_sec, end_sec, text, confidence)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (segment_id, seq) DO NOTHING
"""


async def commit_segment(
    db: _DBConn,
    *,
    transcript_id: UUID,
    job_id: int,
    segment: Segment,
    total_duration_sec: float,
    ewma_alpha: float = 0.2,
) -> CommitResult:
    """Commit one segment + update the owning job's progress.

    The Postgres path delegates to the ``commit_segment`` SQL function
    (migration 0013) so the invariants are enforced near the rows.
    The SQLite path mirrors the same logic in Python.
    """
    audio_sec_default = segment.end_sec - segment.start_sec
    audio_sec_in_seg = max(
        0.0,
        segment.audio_sec if segment.audio_sec is not None else audio_sec_default,
    )
    wall_sec_in_seg = (
        segment.wall_sec if segment.wall_sec is not None else max(audio_sec_in_seg, 0.001)
    )

    if db.dialect == "postgres":
        words_committed = 0
        async with db.transaction():
            row = await db.fetchrow(
                _PG_CALL_FN,
                transcript_id,
                job_id,
                segment.seq,
                segment.start_sec,
                segment.end_sec,
                segment.text,
                segment.speaker,
                segment.confidence,
                audio_sec_in_seg,
                wall_sec_in_seg,
                total_duration_sec,
                ewma_alpha,
            )
            seg_id = int(row["id"]) if row is not None and row["id"] is not None else None
            # The PG fn returns NULL when ON CONFLICT swallowed the
            # segment (replayed seq): the original commit already wrote
            # its words, so re-inserting them would be redundant — and we
            # have no segment_id to attach to anyway. Only persist words
            # for a freshly-inserted segment, in THIS transaction so a
            # rollback drops segment + words together (Story 3.6-2).
            if seg_id is not None and segment.words:
                words_committed = await _insert_words(
                    db, _PG_WORD_INSERT, segment_id=seg_id, words=segment.words
                )
        return CommitResult(
            segment_id=seg_id,
            seq=segment.seq,
            end_sec=segment.end_sec,
            accepted=seg_id is not None,
            words_committed=words_committed,
        )

    # SQLite path — replicate the function's body in Python.
    async with db.transaction():
        prev = await db.fetchrow(_SQLITE_PREV_END, job_id)
        prev_end = float(prev["last_segment_end_sec"]) if prev is not None else 0.0
        prev_factor = float(prev["realtime_factor"]) if prev is not None else 0.0

        await db.execute(
            _SQLITE_INSERT,
            str(transcript_id),
            segment.seq,
            segment.start_sec,
            segment.end_sec,
            segment.text,
            segment.speaker,
            segment.confidence,
        )
        # ``rowid`` is 0 when ON CONFLICT swallowed the insert; we
        # detect that with a follow-up SELECT against the unique key.
        row = await db.fetchrow(_SQLITE_LASTROWID)
        seg_id = int(row["id"]) if row is not None else 0

        if seg_id == 0:
            return CommitResult(
                segment_id=None,
                seq=segment.seq,
                end_sec=segment.end_sec,
                accepted=False,
            )

        # EWMA + ETA, same formula as the SQL function.
        if wall_sec_in_seg > 0:
            sample = audio_sec_in_seg / wall_sec_in_seg
            factor = prev_factor * (1 - ewma_alpha) + sample * ewma_alpha
        else:
            factor = prev_factor
        if factor > 0 and total_duration_sec > 0:
            remaining = total_duration_sec - min(segment.end_sec, total_duration_sec)
            eta = max(0.0, remaining / factor)
        else:
            eta = None

        processed_delta = max(0.0, segment.end_sec - max(prev_end, segment.start_sec))
        await db.execute(
            _SQLITE_PROGRESS,
            segment.end_sec,
            processed_delta,
            factor,
            eta,
            job_id,
        )

        # Story 3.6-1.3 — per-word rows in the SAME transaction as the
        # segment so a rollback drops both (Story 3.6-2).
        words_committed = 0
        if segment.words:
            words_committed = await _insert_words(
                db, _SQLITE_WORD_INSERT, segment_id=seg_id, words=segment.words
            )

    # SQLite has no NOTIFY; publish on the in-process bus so listeners
    # (incremental indexer, live VTT renderer) see the same shape. The
    # payload carries ``last_segment_end_sec`` — the live-indexer /
    # VTT-renderer contract (Story 3.6-4 / Epic 5.5) keys off the job's
    # advanced watermark, not this segment's raw end. For the
    # straight-through monotonic path they're equal; the explicit name
    # matches the PG ``segments.committed`` NOTIFY payload (migration
    # 0062) so both dialects emit the identical shape.
    get_bus().publish(
        SEGMENTS_COMMITTED,
        {
            "transcript_id": str(transcript_id),
            "segment_id": seg_id,
            "seq": segment.seq,
            "last_segment_end_sec": segment.end_sec,
        },
    )
    return CommitResult(
        segment_id=seg_id,
        seq=segment.seq,
        end_sec=segment.end_sec,
        accepted=True,
        words_committed=words_committed,
    )


async def _insert_words(
    db: _DBConn,
    sql: str,
    *,
    segment_id: int,
    words: tuple[Word, ...],
) -> int:
    """Insert per-word timing rows for one segment; return the count.

    Story 3.6-1.3. The word ``seq`` is the word's index within its
    owning segment (0-based, monotonic) — the natural ordering the
    backend emitted; combined with ``segment_id`` it's the
    ``transcript_words`` unique key, so a replayed segment commit
    re-issuing the same words is an idempotent no-op (ON CONFLICT). The
    caller invokes this INSIDE the segment's transaction so the two
    writes commit/rollback together (Story 3.6-2).
    """
    for word_seq, word in enumerate(words):
        await db.execute(
            sql,
            segment_id,
            word_seq,
            word.start_sec,
            word.end_sec,
            word.text,
            word.confidence,
        )
    return len(words)


# --- reorder buffer ---------------------------------------------------


@dataclass(slots=True)
class ReorderBuffer:
    """Buffer + reorder out-of-order segments before commit.

    The buffer admits any :class:`Segment`. :meth:`drain_ready` returns
    every segment whose ``seq`` is in monotonic order from the
    expected next ``seq``; segments that arrived too far ahead stay
    queued. When the buffer's oldest queued segment exceeds
    ``window_sec`` of waiting (real time), it's force-flushed and
    logged.

    Concrete behaviour matches Story 3.1 / 3.6: backends that emit
    out-of-order are absorbed; the orchestrator never sees them.
    """

    window_sec: float = 30.0
    next_seq: int = 0
    _heap: list[tuple[int, float, Segment]] = field(default_factory=list)
    _admitted_at: dict[int, float] = field(default_factory=dict)

    def admit(self, segment: Segment, *, clock: float | None = None) -> None:
        if segment.seq < self.next_seq:
            # Late arrival of a seq we already drained — drop.
            return
        admitted_at = clock if clock is not None else time.monotonic()
        heapq.heappush(self._heap, (segment.seq, admitted_at, segment))
        self._admitted_at.setdefault(segment.seq, admitted_at)

    def drain_ready(self, *, clock: float | None = None) -> Iterable[Segment]:
        """Yield monotonic-order segments ready for commit."""
        out: list[Segment] = []
        now = clock if clock is not None else time.monotonic()
        while self._heap:
            seq, admitted_at, seg = self._heap[0]
            if seq == self.next_seq:
                heapq.heappop(self._heap)
                self._admitted_at.pop(seq, None)
                out.append(seg)
                self.next_seq += 1
                continue
            # Force-flush after the window expires — this drops the gap.
            if now - admitted_at > self.window_sec:
                heapq.heappop(self._heap)
                self._admitted_at.pop(seq, None)
                self.next_seq = seq + 1
                out.append(seg)
                continue
            break
        return out

    def pending(self) -> int:
        return len(self._heap)
