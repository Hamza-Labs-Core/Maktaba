"""In-memory ring buffer for the troubleshooting log-export feature.

Mirrors ``shared/log/go``'s ring sink: a bounded, thread-safe store of
the most recent structured log lines (as JSON strings). A structlog
processor (:func:`make_ring_processor`) snapshots every emitted event
into the process-global ring, and the API's diagnostics-export endpoint
proxies it over HTTP via :mod:`maktaba_pipeline.log.http`.

The ring lives behind a lock rather than relying on the GIL because the
HTTP server that drains it runs on a separate daemon thread from the
asyncio worker loop that fills it.
"""

from __future__ import annotations

import json
import threading
from collections import deque
from collections.abc import Mapping
from datetime import datetime
from typing import Any

__all__ = [
    "DEFAULT_RING_CAPACITY",
    "LogRingBuffer",
    "get_ring",
    "level_rank",
    "make_ring_processor",
    "set_ring",
]

#: Default number of lines retained — matches the Go DefaultRingCapacity.
DEFAULT_RING_CAPACITY = 10_000

#: Level ordering shared with the Go side. structlog emits "warning";
#: the export query may pass either "warn" or "warning".
_LEVEL_RANK: dict[str, int] = {
    "debug": -4,
    "info": 0,
    "warn": 4,
    "warning": 4,
    "error": 8,
    "critical": 12,
    "fatal": 12,
}


def level_rank(level: str | None) -> int:
    """Map a level string to its numeric rank (unknown sorts as info)."""
    if not level:
        return 0
    return _LEVEL_RANK.get(level.strip().lower(), 0)


class LogRingBuffer:
    """Bounded, thread-safe store of JSON log lines."""

    def __init__(self, capacity: int = DEFAULT_RING_CAPACITY) -> None:
        if capacity <= 0:
            capacity = DEFAULT_RING_CAPACITY
        self._dq: deque[str] = deque(maxlen=capacity)
        self._lock = threading.Lock()

    def append(self, line: str) -> None:
        with self._lock:
            self._dq.append(line)

    def __len__(self) -> int:
        with self._lock:
            return len(self._dq)

    def recent(
        self,
        *,
        since: datetime | None = None,
        min_level: str | None = None,
        services: frozenset[str] | None = None,
        search: str | None = None,
        limit: int | None = None,
    ) -> list[str]:
        """Return matching lines oldest→newest as raw JSON strings.

        A line that fails to parse as JSON is skipped — a corrupt entry
        must never abort a diagnostics pull.
        """
        with self._lock:
            snapshot = list(self._dq)

        floor = level_rank(min_level) if min_level else None
        search_l = search.lower() if search else None
        out: list[str] = []
        for line in snapshot:
            try:
                rec = json.loads(line)
            except (ValueError, TypeError):
                continue
            if floor is not None and level_rank(rec.get("level")) < floor:
                continue
            if since is not None and not _ts_at_or_after(rec.get("ts"), since):
                continue
            if services and rec.get("service") not in services:
                continue
            if search_l is not None and search_l not in line.lower():
                continue
            out.append(line)
        if limit and limit > 0 and len(out) > limit:
            out = out[-limit:]
        return out


def _ts_at_or_after(ts: Any, since: datetime) -> bool:
    if not isinstance(ts, str) or not ts:
        return True  # un-timestamped lines are kept rather than dropped
    try:
        parsed = datetime.fromisoformat(ts.replace("Z", "+00:00"))
    except ValueError:
        return True
    return parsed >= since


_ring: LogRingBuffer | None = None
_ring_lock = threading.Lock()


def get_ring() -> LogRingBuffer | None:
    """Return the process-global ring, or None if logging was configured
    with the ring disabled."""
    return _ring


def set_ring(ring: LogRingBuffer | None) -> None:
    """Install (or clear) the process-global ring. Called by log.init."""
    global _ring
    with _ring_lock:
        _ring = ring


def make_ring_processor(ring: LogRingBuffer) -> Any:
    """Build a structlog processor that snapshots each event into ring.

    Placed immediately before the terminal renderer so the captured copy
    reflects the post-redaction, post-truncation event dict. The event
    dict is copied and ``event`` is normalised to ``msg`` (the Go field
    name) for the buffered line only — the dict passed downstream to the
    console/JSON renderer is left untouched.
    """

    def processor(
        _logger: Any,
        _name: str,
        event_dict: Mapping[str, Any],
    ) -> Mapping[str, Any]:
        try:
            snapshot = dict(event_dict)
            if "event" in snapshot and "msg" not in snapshot:
                snapshot["msg"] = snapshot.pop("event")
            ring.append(json.dumps(snapshot, default=str))
        except Exception:  # noqa: BLE001 — a logging sink must never raise
            pass
        return event_dict

    return processor
