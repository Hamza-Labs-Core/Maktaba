"""Shared in-memory FakeDB for Story 6.3-6.8 unit tests.

Models the ``processing_jobs`` row shape and pattern-matches the SQL
fragments used by the new helpers (``tick_progress``,
``tick_heartbeat``, ``mark_*``, ``mark_failed_or_retry``,
``retry_failed``, ``read_flags``, ``reap_once``, the shutdown drain
queries) by their unique identifying keywords. A SQL edit that
changes the shape of a write surfaces as a test failure rather than
a silent misroute.
"""

from __future__ import annotations

import asyncio
import re
from contextlib import asynccontextmanager
from dataclasses import dataclass, field
from datetime import UTC, datetime, timedelta
from typing import Any
from uuid import UUID, uuid4


@dataclass
class FakeJobRow:
    id: int
    video_id: UUID
    stage: str
    state: str = "pending"
    priority: int = 100
    attempts: int = 0
    max_attempts: int = 3
    claimed_by: str | None = None
    claimed_at: datetime | None = None
    last_heartbeat_at: datetime | None = None
    not_before: datetime | None = None
    error: str | None = None
    total_duration_seconds: float | None = None
    processed_seconds: float = 0.0
    segments_completed: int = 0
    last_segment_end_sec: float = 0.0
    estimated_remaining_sec: float | None = None
    realtime_factor: float | None = None
    progress_updated_at: datetime | None = None
    pause_requested: bool = False
    cancel_requested: bool = False
    paused_at: datetime | None = None
    paused_at_sec: float | None = None
    paused_reason: str | None = None
    resumed_at: datetime | None = None
    resume_count: int = 0
    metrics: dict[str, Any] | None = None
    payload: dict[str, Any] | None = None
    created_at: datetime = field(default_factory=lambda: datetime.now(UTC))
    finished_at: datetime | None = None


class _Row(dict[str, Any]):
    """Mapping-shaped row, mimicking asyncpg/aiosqlite rows."""


_LIVE = {"claimed", "running", "resuming"}


@dataclass
class FakeDB:
    """Stand-in for the future Story 1.5 connection wrapper."""

    dialect: str = "postgres"
    rows: dict[int, FakeJobRow] = field(default_factory=dict)
    _next_id: int = 1
    _lock_obj: asyncio.Lock | None = None
    notifies: list[tuple[str, str]] = field(default_factory=list)
    pg_advisory_locked: bool = False
    _pg_advisory_holder: bool = False

    def add(self, **kwargs: Any) -> FakeJobRow:
        row = FakeJobRow(
            id=self._next_id,
            video_id=kwargs.pop("video_id", uuid4()),
            stage=kwargs.pop("stage", "probe"),
            **kwargs,
        )
        self.rows[row.id] = row
        self._next_id += 1
        return row

    def transaction(self) -> Any:
        @asynccontextmanager
        async def _tx() -> Any:
            yield self

        return _tx()

    def _lock(self) -> asyncio.Lock:
        if self._lock_obj is None:
            self._lock_obj = asyncio.Lock()
        return self._lock_obj

    @staticmethod
    def _now() -> datetime:
        return datetime.now(UTC)

    # ---- driver-shaped surface ------------------------------------------

    async def fetchrow(self, sql: str, *args: Any) -> _Row | None:
        s = " ".join(sql.split())
        async with self._lock():
            result = self._dispatch(s, args, many=False)
        if isinstance(result, list):
            first: _Row | None = result[0] if result else None
            return first
        if result is None:
            return None
        assert isinstance(result, _Row)
        return result

    async def fetch(self, sql: str, *args: Any) -> list[_Row]:
        s = " ".join(sql.split())
        async with self._lock():
            result = self._dispatch(s, args, many=True)
        if isinstance(result, list):
            return result
        return [result] if result is not None else []

    async def execute(self, sql: str, *args: Any) -> None:
        s = " ".join(sql.split())
        if "pg_notify" in s:
            channel, payload = args
            self.notifies.append((str(channel), str(payload)))
            return
        async with self._lock():
            self._dispatch(s, args, many=False)

    # ---- routing -------------------------------------------------------

    def _dispatch(
        self,
        s: str,
        args: tuple[Any, ...],
        *,
        many: bool,
    ) -> Any:
        # Advisory locks
        if "pg_try_advisory_lock" in s:
            if self.pg_advisory_locked:
                return _Row({"got": False})
            self.pg_advisory_locked = True
            self._pg_advisory_holder = True
            return _Row({"got": True})
        if "pg_advisory_unlock" in s:
            if self._pg_advisory_holder:
                self.pg_advisory_locked = False
                self._pg_advisory_holder = False
                return _Row({"released": True})
            return _Row({"released": False})
        if "pg_notify" in s:
            channel, payload = args
            self.notifies.append((str(channel), str(payload)))
            return None

        # Story 6.3 — progress tick (UPDATE w/ processed_seconds + progress_updated_at)
        if (
            s.startswith("UPDATE processing_jobs")
            and "processed_seconds" in s
            and "progress_updated_at" in s
        ):
            return self._exec_progress(args)

        # Story 6.3 — heartbeat-only tick
        if (
            s.startswith("UPDATE processing_jobs")
            and "last_heartbeat_at" in s
            and "processed_seconds" not in s
            and "paused_at" not in s
        ):
            return self._exec_heartbeat(args)

        # Story 6.4 — read flags
        if s.startswith("SELECT pause_requested, cancel_requested"):
            return self._exec_read_flags(args)

        # Story 6.6 reaper PG CTE
        if "WITH stale AS" in s:
            return self._exec_reap_pg(args)

        # Story 6.6 reaper SQLite SELECT
        if s.startswith("SELECT id, state AS prev_state"):
            return self._exec_reap_select_sqlite(args, many)

        # Story 6.6 reaper SQLite UPDATE (paused_reason = 'crash')
        if s.startswith("UPDATE processing_jobs") and "paused_reason = 'crash'" in s:
            return self._exec_reap_update_sqlite(args)

        # Story 6.8 — shutdown force pause (paused_reason = 'shutdown')
        if s.startswith("UPDATE processing_jobs") and "paused_reason = 'shutdown'" in s:
            return self._exec_shutdown_force_pause(args, many)

        # Story 6.8 — shutdown pause_requested UPDATE (WHERE claimed_by)
        if (
            s.startswith("UPDATE processing_jobs")
            and "pause_requested" in s
            and ("WHERE claimed_by = $1" in s or "WHERE claimed_by = ?" in s)
            and "RETURNING id" in s
        ):
            return self._exec_shutdown_pause(args, many)

        # Story 6.8 — count remaining
        if s.startswith("SELECT count(*)"):
            return self._exec_count_remaining(args)

        # Story 6.4 — mark_cancelled
        if s.startswith("UPDATE processing_jobs") and "= 'cancelled'" in s:
            return self._exec_mark_cancelled(args)

        # Story 6.4 — mark_done
        if s.startswith("UPDATE processing_jobs") and "= 'done'" in s:
            return self._exec_mark_done(args)

        # Story 6.4 — mark_paused (parametric reason; WHERE id =)
        if (
            s.startswith("UPDATE processing_jobs")
            and "paused_reason" in s
            and ("WHERE id = $1" in s or "WHERE id = ?" in s)
        ):
            return self._exec_mark_paused(args)

        # Story 6.5 — read attempts
        if s.startswith("SELECT attempts, max_attempts"):
            return self._exec_read_attempts(args)

        # Story 6.5 — retry_failed reset (state = 'pending', attempts = 0)
        if s.startswith("UPDATE processing_jobs") and "= 'pending'" in s and "attempts" in s:
            return self._exec_retry_failed(args)

        # Story 6.5 — fail/retry transition (state parametric, error column set)
        if s.startswith("UPDATE processing_jobs") and "not_before" in s and "error" in s:
            return self._exec_fail_or_retry(args)

        raise AssertionError(f"unexpected SQL in fake DB: {s!r}")

    # ---- per-handler ----------------------------------------------------

    def _exec_progress(self, args: tuple[Any, ...]) -> _Row | None:
        if self.dialect == "postgres":
            (
                row_id,
                processed_seconds,
                segs_delta,
                last_end,
                rt_factor,
                eta,
            ) = args
        else:
            (
                processed_seconds,
                segs_delta,
                last_end,
                rt_factor,
                eta,
                row_id,
            ) = args
        row = self.rows.get(int(row_id))
        if row is None or row.state not in _LIVE:
            return None
        now = self._now()
        row.processed_seconds = float(processed_seconds)
        row.segments_completed += int(segs_delta)
        if last_end is not None:
            row.last_segment_end_sec = float(last_end)
        if rt_factor is not None:
            row.realtime_factor = float(rt_factor)
        if eta is not None:
            row.estimated_remaining_sec = float(eta)
        row.progress_updated_at = now
        row.last_heartbeat_at = now
        return _Row(
            {
                "id": row.id,
                "video_id": row.video_id,
                "stage": row.stage,
                "state": row.state,
                "last_segment_end_sec": row.last_segment_end_sec,
                "processed_seconds": row.processed_seconds,
                "total_duration_seconds": row.total_duration_seconds,
                "segments_completed": row.segments_completed,
                "realtime_factor": row.realtime_factor,
                "estimated_remaining_sec": row.estimated_remaining_sec,
                "progress_updated_at": row.progress_updated_at,
            }
        )

    def _exec_heartbeat(self, args: tuple[Any, ...]) -> _Row | None:
        row = self.rows.get(int(args[0]))
        if row is None or row.state not in _LIVE:
            return None
        row.last_heartbeat_at = self._now()
        return _Row({"id": row.id, "stage": row.stage, "last_heartbeat_at": row.last_heartbeat_at})

    def _exec_read_flags(self, args: tuple[Any, ...]) -> _Row | None:
        row = self.rows.get(int(args[0]))
        if row is None:
            return None
        return _Row(
            {"pause_requested": row.pause_requested, "cancel_requested": row.cancel_requested}
        )

    def _exec_mark_paused(self, args: tuple[Any, ...]) -> _Row | None:
        if self.dialect == "postgres":
            row_id, at_sec, reason = args
        else:
            at_sec, reason, row_id = args
        row = self.rows.get(int(row_id))
        if row is None or row.state not in _LIVE:
            return None
        row.state = "paused"
        row.paused_at = self._now()
        row.paused_at_sec = float(at_sec)
        row.paused_reason = str(reason)
        row.pause_requested = False
        row.claimed_by = None
        return _Row({"id": row.id, "state": row.state, "paused_at_sec": row.paused_at_sec})

    def _exec_mark_cancelled(self, args: tuple[Any, ...]) -> _Row | None:
        row = self.rows.get(int(args[0]))
        allowed = {"claimed", "running", "resuming", "paused", "pending"}
        if row is None or row.state not in allowed:
            return None
        row.state = "cancelled"
        row.finished_at = self._now()
        row.claimed_by = None
        row.cancel_requested = False
        return _Row({"id": row.id, "state": row.state})

    def _exec_mark_done(self, args: tuple[Any, ...]) -> _Row | None:
        row = self.rows.get(int(args[0]))
        if row is None or row.state not in _LIVE:
            return None
        row.state = "done"
        row.finished_at = self._now()
        row.claimed_by = None
        return _Row({"id": row.id, "state": row.state})

    def _exec_read_attempts(self, args: tuple[Any, ...]) -> _Row | None:
        row = self.rows.get(int(args[0]))
        if row is None:
            return None
        return _Row({"attempts": row.attempts, "max_attempts": row.max_attempts})

    def _exec_fail_or_retry(self, args: tuple[Any, ...]) -> _Row | None:
        if self.dialect == "postgres":
            row_id, new_state, not_before, err_json, finished_at = args
        else:
            new_state, not_before_iso, err_json, finished_iso, row_id = args
            not_before = datetime.fromisoformat(not_before_iso) if not_before_iso else None
            finished_at = datetime.fromisoformat(finished_iso) if finished_iso else None
        row = self.rows.get(int(row_id))
        if row is None or row.state not in _LIVE:
            return None
        row.state = str(new_state)
        row.not_before = not_before
        row.claimed_by = None
        row.error = err_json
        row.finished_at = finished_at
        return _Row({"id": row.id, "state": row.state, "not_before": row.not_before})

    def _exec_retry_failed(self, args: tuple[Any, ...]) -> _Row | None:
        row = self.rows.get(int(args[0]))
        if row is None or row.state != "failed":
            return None
        row.state = "pending"
        row.attempts = 0
        row.not_before = None
        row.error = None
        row.finished_at = None
        row.claimed_by = None
        row.claimed_at = None
        return _Row({"id": row.id, "state": row.state})

    def _exec_reap_pg(self, args: tuple[Any, ...]) -> list[_Row]:
        stale_sec = float(args[0])
        return self._do_reap(stale_sec)

    def _exec_reap_select_sqlite(
        self,
        args: tuple[Any, ...],
        many: bool,
    ) -> Any:
        offset = str(args[0])
        m = re.match(r"-(\d+\.?\d*)", offset)
        if m is None:
            return [] if many else None
        stale_sec = float(m.group(1))
        cutoff = self._now() - timedelta(seconds=stale_sec)
        out: list[_Row] = []
        for row in self.rows.values():
            if row.state not in _LIVE:
                continue
            if row.last_heartbeat_at is None:
                continue
            hb = _aware(row.last_heartbeat_at)
            if hb < cutoff:
                out.append(
                    _Row(
                        {
                            "id": row.id,
                            "prev_state": row.state,
                            "last_segment_end_sec": row.last_segment_end_sec,
                            "last_heartbeat_at": row.last_heartbeat_at,
                            "video_id": row.video_id,
                            "stage": row.stage,
                        }
                    )
                )
        if many:
            return out
        return out[0] if out else None

    def _exec_reap_update_sqlite(self, args: tuple[Any, ...]) -> _Row | None:
        row = self.rows.get(int(args[0]))
        if row is None or row.state not in _LIVE:
            return None
        row.state = "paused"
        row.paused_at = self._now()
        row.paused_at_sec = row.last_segment_end_sec
        row.paused_reason = "crash"
        row.claimed_by = None
        row.pause_requested = False
        return _Row({"paused_at": row.paused_at, "paused_at_sec": row.paused_at_sec})

    def _do_reap(self, stale_sec: float) -> list[_Row]:
        cutoff = self._now() - timedelta(seconds=stale_sec)
        out: list[_Row] = []
        for row in list(self.rows.values()):
            if row.state not in _LIVE:
                continue
            if row.last_heartbeat_at is None:
                continue
            hb = _aware(row.last_heartbeat_at)
            if hb < cutoff:
                prev = row.state
                row.state = "paused"
                row.paused_at = self._now()
                row.paused_at_sec = row.last_segment_end_sec
                row.paused_reason = "crash"
                row.claimed_by = None
                row.pause_requested = False
                out.append(
                    _Row(
                        {
                            "id": row.id,
                            "video_id": row.video_id,
                            "stage": row.stage,
                            "prev_state": prev,
                            "paused_at_sec": row.paused_at_sec,
                            "paused_at": row.paused_at,
                            "last_heartbeat_at": row.last_heartbeat_at,
                        }
                    )
                )
        return out

    def _exec_shutdown_pause(
        self,
        args: tuple[Any, ...],
        many: bool,
    ) -> Any:
        worker_id = str(args[0])
        ids: list[_Row] = []
        for row in self.rows.values():
            if row.claimed_by == worker_id and row.state in _LIVE:
                row.pause_requested = True
                ids.append(_Row({"id": row.id}))
        if many:
            return ids
        return ids[0] if ids else None

    def _exec_count_remaining(self, args: tuple[Any, ...]) -> _Row | None:
        worker_id = str(args[0])
        n = sum(1 for r in self.rows.values() if r.claimed_by == worker_id and r.state in _LIVE)
        return _Row({"n": n})

    def _exec_shutdown_force_pause(
        self,
        args: tuple[Any, ...],
        many: bool,
    ) -> Any:
        worker_id = str(args[0])
        ids: list[_Row] = []
        for row in self.rows.values():
            if row.claimed_by == worker_id and row.state in _LIVE:
                row.state = "paused"
                row.paused_at = self._now()
                row.paused_at_sec = row.last_segment_end_sec
                row.paused_reason = "shutdown"
                row.pause_requested = False
                row.claimed_by = None
                ids.append(_Row({"id": row.id}))
        if many:
            return ids
        return ids[0] if ids else None


def _aware(dt: datetime) -> datetime:
    return dt if dt.tzinfo is not None else dt.replace(tzinfo=UTC)
