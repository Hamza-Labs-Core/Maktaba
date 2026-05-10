"""Dead-letter buffer for units that fail vector-store writes.

When Chroma (or whatever vector backend) rejects a batch, the indexer
calls :func:`enqueue_dead_letter` to stash the offending unit ids.
A periodic drain (:func:`drain_dead_letter`) re-reads those rows,
re-attempts indexing via the supplied indexer, and either deletes the
row on success or bumps the attempt counter on failure. Rows that
exceed ``max_attempts`` are considered "given up" — they stay in the
table for operators to inspect but are no longer retried.
"""

from __future__ import annotations

from contextlib import AbstractAsyncContextManager
from typing import Any, Protocol
from uuid import UUID

__all__ = ["drain_dead_letter", "enqueue_dead_letter"]


class _Row(Protocol):
    def __getitem__(self, key: str) -> Any: ...


class _DBConn(Protocol):
    dialect: str

    def transaction(self) -> AbstractAsyncContextManager[Any]: ...

    async def fetchrow(self, sql: str, *args: Any) -> _Row | None: ...

    async def execute(self, sql: str, *args: Any) -> Any: ...

    async def fetch(self, sql: str, *args: Any) -> list[_Row]: ...


class _Indexer(Protocol):
    async def index_unit_batch(self, unit_ids: list[int]) -> None: ...


_UPSERT_SQL = """
INSERT INTO vector_index_dead_letter
       (unit_id, library_id, transcript_id, attempts, last_error)
VALUES ($1, $2, $3, 1, $4)
ON CONFLICT (unit_id) DO UPDATE
   SET attempts = vector_index_dead_letter.attempts + 1,
       last_error = EXCLUDED.last_error,
       last_attempted_at = now()
"""

_SELECT_DUE_SQL = """
SELECT unit_id, library_id, transcript_id, attempts
  FROM vector_index_dead_letter
 WHERE attempts < $1
 ORDER BY last_attempted_at ASC NULLS FIRST
 LIMIT $2
"""

_DELETE_SQL = "DELETE FROM vector_index_dead_letter WHERE unit_id = $1"

_BUMP_SQL = """
UPDATE vector_index_dead_letter
   SET attempts = attempts + 1,
       last_error = $2,
       last_attempted_at = now()
 WHERE unit_id = $1
"""


async def enqueue_dead_letter(
    db: _DBConn,
    *,
    unit_id: int,
    library_id: UUID,
    transcript_id: int,
    error: str,
) -> None:
    """Insert (or bump) a dead-letter row for ``unit_id``.

    The UPSERT increments ``attempts`` on conflict so callers don't
    have to read the current attempt count before writing — the
    counter is owned by SQL. Callers should still respect their own
    retry budgets; the table is the durable record, not the policy.
    """
    async with db.transaction():
        await db.execute(_UPSERT_SQL, unit_id, library_id, transcript_id, error)


async def drain_dead_letter(
    db: _DBConn,
    *,
    indexer: _Indexer,
    max_attempts: int = 5,
    batch_size: int = 32,
) -> dict[str, int]:
    """Re-attempt the oldest pending dead-letter rows.

    Returns a counts dict:

    - ``reindexed`` — rows where ``index_unit_batch`` returned
      cleanly; the row was deleted.
    - ``still_failing`` — rows where the retry raised; attempts
      bumped and ``last_error`` updated.
    - ``given_up`` — rows whose attempt count is at or above
      ``max_attempts`` and were therefore skipped this drain.

    The drain processes each row in its own micro-transaction so a
    pathological failure mid-batch doesn't roll back the successes
    that came before it.
    """
    rows = await db.fetch(_SELECT_DUE_SQL, max_attempts, batch_size)
    reindexed = 0
    still_failing = 0
    for row in rows:
        unit_id = int(row["unit_id"])
        attempts = int(row["attempts"])
        try:
            await indexer.index_unit_batch([unit_id])
        except Exception as exc:  # noqa: BLE001 — log via error column
            async with db.transaction():
                await db.execute(_BUMP_SQL, unit_id, repr(exc))
            still_failing += 1
            continue
        async with db.transaction():
            await db.execute(_DELETE_SQL, unit_id)
        reindexed += 1
        # Reference unused to satisfy strict — kept for log/debug:
        _ = attempts

    given_up_row = await db.fetchrow(
        "SELECT COUNT(*) AS n FROM vector_index_dead_letter WHERE attempts >= $1",
        max_attempts,
    )
    given_up = int(given_up_row["n"]) if given_up_row is not None else 0

    return {
        "reindexed": reindexed,
        "still_failing": still_failing,
        "given_up": given_up,
    }
