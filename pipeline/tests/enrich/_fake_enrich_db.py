"""In-memory fake connection for the Story 26.7 enrich/group tests.

Models the ``enrich_jobs`` + ``library_group_pending`` tables (slot
0079) and the ``media_parsed_titles`` upsert (slot 0073) by
pattern-matching the SQL fragments the helpers emit. It honours the
slot-0079 "at most one open job per video" partial unique index so the
idempotent-enqueue behaviour is exercised, and the claim's
"due (not_before <= now) pending/deferred, earliest first" ordering.
"""

from __future__ import annotations

from contextlib import asynccontextmanager
from dataclasses import dataclass, field
from datetime import datetime
from typing import Any


class _Row(dict[str, Any]):
    """Mapping-shaped row, mimicking asyncpg/aiosqlite rows."""


_OPEN = {"pending", "running", "deferred"}


@dataclass
class FakeEnrichDB:
    dialect: str = "postgres"
    jobs: dict[str, dict[str, Any]] = field(default_factory=dict)
    pending: dict[str, datetime] = field(default_factory=dict)
    parsed_titles: dict[str, tuple[Any, ...]] = field(default_factory=dict)

    def transaction(self) -> Any:
        @asynccontextmanager
        async def _tx() -> Any:
            yield self

        return _tx()

    async def fetchrow(self, sql: str, *args: Any) -> _Row | None:
        s = " ".join(sql.split())
        if "INSERT INTO enrich_jobs" in s:
            return self._enqueue(*args)
        if "UPDATE enrich_jobs SET status = 'running'" in s:
            return self._claim(*args)
        if "status = 'done'" in s:
            return self._set(args[0], status="done")
        if "status = 'deferred'" in s:
            return self._defer(*args)
        if "SET status = 'pending'" in s:
            return self._retry(*args)
        if "status = 'failed'" in s:
            return self._fail(*args)
        if "INSERT INTO library_group_pending" in s:
            self.pending[str(args[0])] = args[1]
            return _Row(library_id=args[0])
        if "DELETE FROM library_group_pending" in s:
            existed = self.pending.pop(str(args[0]), None)
            return _Row(library_id=args[0]) if existed is not None else None
        if "INSERT INTO media_parsed_titles" in s:
            self.parsed_titles[str(args[0])] = args
            return _Row(video_id=args[0])
        raise AssertionError(f"unmatched SQL: {s[:80]}")

    # --- enrich_jobs ---

    def _enqueue(self, job_id: str, video_id: str, force: bool, ts: datetime) -> _Row | None:
        # Honour the open-video unique index: skip if an open job exists.
        for j in self.jobs.values():
            if j["video_id"] == str(video_id) and j["status"] in _OPEN:
                return None
        self.jobs[str(job_id)] = {
            "id": str(job_id),
            "video_id": str(video_id),
            "status": "pending",
            "force": bool(force),
            "attempts": 0,
            "not_before": ts,
            "last_error": None,
        }
        return _Row(id=job_id)

    def _claim(self, ts: datetime) -> _Row | None:
        due = [
            j
            for j in self.jobs.values()
            if j["status"] in ("pending", "deferred") and j["not_before"] <= ts
        ]
        if not due:
            return None
        due.sort(key=lambda j: j["not_before"])
        j = due[0]
        j["status"] = "running"
        return _Row(
            id=j["id"],
            video_id=j["video_id"],
            status="running",
            force=j["force"],
            attempts=j["attempts"],
        )

    def _set(self, job_id: str, *, status: str) -> _Row | None:
        j = self.jobs.get(str(job_id))
        if j is None:
            return None
        j["status"] = status
        return _Row(id=job_id)

    def _defer(self, job_id: str, not_before: datetime, reason: str, ts: datetime) -> _Row | None:
        j = self.jobs.get(str(job_id))
        if j is None:
            return None
        j.update(status="deferred", not_before=not_before, last_error=reason)
        return _Row(id=job_id)

    def _retry(
        self, job_id: str, attempts: int, not_before: datetime, error: str, ts: datetime
    ) -> _Row | None:
        j = self.jobs.get(str(job_id))
        if j is None:
            return None
        j.update(status="pending", attempts=attempts, not_before=not_before, last_error=error)
        return _Row(id=job_id)

    def _fail(self, job_id: str, attempts: int, error: str, ts: datetime) -> _Row | None:
        j = self.jobs.get(str(job_id))
        if j is None:
            return None
        j.update(status="failed", attempts=attempts, last_error=error)
        return _Row(id=job_id)
