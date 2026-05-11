"""Idempotency-key store (Epic 24 plan-24-02).

Pipeline jobs are retried under several conditions: worker crash, lease
expiry, explicit ``cancel + re-enqueue``. The key invariant is "the same
logical job, retried, produces the same effects in the DB" — meaning
inserts must be either upserts on a natural key, or guarded by a
short-lived idempotency key.

This module is the in-process side: callers hand the store an
``IdempotencyKey`` before doing the work; if the key has been seen
within ``ttl`` the result is replayed and the work is skipped.
"""

from __future__ import annotations

import dataclasses
import datetime as dt
import threading
from collections.abc import Callable
from typing import Any, Protocol


@dataclasses.dataclass(frozen=True)
class IdempotencyKey:
    """Composite key. ``job_id`` is the canonical anchor, ``op`` and
    ``args_hash`` distinguish sub-operations of the same job."""

    job_id: str
    op: str
    args_hash: str

    def serialize(self) -> str:
        return f"{self.job_id}:{self.op}:{self.args_hash}"


@dataclasses.dataclass
class IdempotencyRecord:
    key: IdempotencyKey
    result: Any
    stored_at: dt.datetime


class IdempotencyStore(Protocol):
    """Persistence interface. Production wraps a Postgres table; tests
    use :class:`MemoryIdempotencyStore`."""

    def lookup(self, key: IdempotencyKey) -> IdempotencyRecord | None: ...
    def store(self, key: IdempotencyKey, result: Any) -> None: ...
    def purge_older_than(self, cutoff: dt.datetime) -> int: ...


class MemoryIdempotencyStore:
    """In-memory store. Thread-safe; not process-safe."""

    def __init__(self, ttl_sec: float = 3600.0) -> None:
        self._ttl = ttl_sec
        self._data: dict[str, IdempotencyRecord] = {}
        self._lock = threading.Lock()
        self._now: Callable[[], dt.datetime] = lambda: dt.datetime.now(dt.UTC)

    def lookup(self, key: IdempotencyKey) -> IdempotencyRecord | None:
        with self._lock:
            rec = self._data.get(key.serialize())
            if rec is None:
                return None
            if (self._now() - rec.stored_at).total_seconds() > self._ttl:
                del self._data[key.serialize()]
                return None
            return rec

    def store(self, key: IdempotencyKey, result: Any) -> None:
        with self._lock:
            self._data[key.serialize()] = IdempotencyRecord(
                key=key, result=result, stored_at=self._now()
            )

    def purge_older_than(self, cutoff: dt.datetime) -> int:
        with self._lock:
            stale = [k for k, v in self._data.items() if v.stored_at < cutoff]
            for k in stale:
                del self._data[k]
            return len(stale)

    def size(self) -> int:
        with self._lock:
            return len(self._data)
