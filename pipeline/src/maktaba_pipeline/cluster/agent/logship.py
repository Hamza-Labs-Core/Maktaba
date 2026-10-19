"""The log shipper: a structlog sink that copies records to the agent (design-agent.md §3).

Records at or above the session's level (``welcome.settings.log_level``, then ``cmd
log_level``; ``MAKTABA_CLUSTER_LOG_LEVEL`` before the first welcome, default ``info``) go
into a bounded buffer (10 000 records; the oldest go first and are counted) that the agent
ships as ``logs`` batches every 250 ms, and to this process's log file on the share.

The sink runs on whatever thread logged, so it only builds a small dict and appends under
a lock: cheap whether the agent is connected or not. The agent's own transport messages
below ``warning`` are never shipped (logging about shipping would feed itself).
"""

from __future__ import annotations

import json
import logging
import threading
from collections import deque
from collections.abc import Callable, Mapping
from datetime import UTC, datetime
from typing import Any

from ...log import LEVEL_NUMBERS, add_sink, remove_sink, set_sink_level

__all__ = ["AGENT_LOGGER", "CAPACITY", "LogShipper", "make_record", "normalize_level"]

#: the ``logger`` field the agent's own messages carry
AGENT_LOGGER = "cluster.agent"
CAPACITY = 10_000
#: a field value longer than this is cut (an exception text keeps more)
VALUE_CHARS = 2_000
EXCEPTION_CHARS = 8_000
MAX_RECORD_BYTES = 64 * 1024
_SKIP = frozenset(
    {"ts", "timestamp", "level", "event", "msg", "logger", "service", "version", "env"}
)
_LEVELS = ("debug", "info", "warning", "error", "critical")


def normalize_level(name: str | None, default: str = "info") -> str:
    n = (name or "").strip().lower()
    if n == "warn":
        n = "warning"
    if n == "fatal":
        n = "critical"
    return n if n in _LEVELS else default


def _safe(value: Any, limit: int = VALUE_CHARS) -> Any:
    if value is None or isinstance(value, bool | int | float):
        return value
    if isinstance(value, str):
        return value if len(value) <= limit else value[:limit] + "..."
    if isinstance(value, list | tuple) and len(value) <= 20:
        return [_safe(v, 200) for v in value]
    if isinstance(value, Mapping) and len(value) <= 20:
        return {str(k): _safe(v, 200) for k, v in value.items()}
    text = str(value)
    return text if len(text) <= limit else text[:limit] + "..."


def _now() -> str:
    return datetime.now(UTC).isoformat(timespec="seconds").replace("+00:00", "Z")


def make_record(event_dict: Mapping[str, Any]) -> dict[str, Any]:
    """``{ts, level, event, logger, fields}`` from a structlog event dict."""
    ts = event_dict.get("ts") or event_dict.get("timestamp")
    if not isinstance(ts, str):
        ts = datetime.now(UTC).isoformat(timespec="milliseconds").replace("+00:00", "Z")
    event = event_dict.get("event", event_dict.get("msg", ""))
    fields = {
        str(k): _safe(v, EXCEPTION_CHARS if k == "exception" else VALUE_CHARS)
        for k, v in event_dict.items()
        if k not in _SKIP
    }
    rec: dict[str, Any] = {
        "ts": ts,
        "level": normalize_level(str(event_dict.get("level", "info"))),
        "event": _safe(event if isinstance(event, str) else str(event)),
        "logger": str(event_dict.get("logger") or "pipeline"),
        "fields": fields,
    }
    if len(json.dumps(fields, default=str)) > MAX_RECORD_BYTES:
        # a record over 64 KiB has its fields cut (section 4.7)
        rec["fields"] = {k: _safe(v, 200) for k, v in list(fields.items())[:50]}
        if len(json.dumps(rec["fields"], default=str)) > MAX_RECORD_BYTES:
            rec["fields"] = {}
        rec["truncated"] = True
    return rec


class LogShipper:
    def __init__(
        self,
        level: str = "info",
        *,
        capacity: int = CAPACITY,
        file_sink: Callable[[dict[str, Any]], None] | None = None,
    ) -> None:
        self._lock = threading.Lock()
        self._buf: deque[dict[str, Any]] = deque(maxlen=capacity)
        self.capacity = capacity
        self.dropped = 0
        self.dropped_since: str | None = None
        self.file_sink = file_sink
        self._guard = threading.local()
        self.level = normalize_level(level)
        self._level_no = LEVEL_NUMBERS[self.level]
        self._installed = False

    # -- configuration -----------------------------------------------------------

    def set_level(self, name: str) -> str:
        self.level = normalize_level(name, self.level)
        self._level_no = LEVEL_NUMBERS[self.level]
        if self._installed:
            set_sink_level(min(self._level_no, logging.INFO))
        return self.level

    def install(self) -> None:
        add_sink(self.sink)
        self._installed = True
        set_sink_level(min(self._level_no, logging.INFO))

    def uninstall(self) -> None:
        remove_sink(self.sink)
        if self._installed:
            set_sink_level(logging.INFO)
        self._installed = False

    # -- the sink ------------------------------------------------------------------

    def sink(self, event_dict: Mapping[str, Any]) -> None:
        number = LEVEL_NUMBERS.get(str(event_dict.get("level", "info")).lower(), logging.INFO)
        if number < self._level_no:
            return
        if number < logging.WARNING and event_dict.get("logger") == AGENT_LOGGER:
            return
        if getattr(self._guard, "busy", False):
            return
        self._guard.busy = True
        try:
            rec = make_record(event_dict)
            with self._lock:
                if len(self._buf) == self.capacity:
                    self._drop(1)
                self._buf.append(rec)
            if self.file_sink is not None:
                self.file_sink(rec)
        finally:
            self._guard.busy = False

    # -- the agent's side ------------------------------------------------------------

    def take(self, limit: int = 1_000) -> list[dict[str, Any]]:
        with self._lock:
            n = min(limit, len(self._buf))
            return [self._buf.popleft() for _ in range(n)]

    def put_back(self, records: list[dict[str, Any]]) -> None:
        """Records taken for a batch that could not be sent go back in front (as many
        as fit; the rest are counted as dropped)."""
        with self._lock:
            room = self.capacity - len(self._buf)
            keep = records[-room:] if room > 0 else []
            if len(records) > len(keep):
                self._drop(len(records) - len(keep))
            self._buf.extendleft(reversed(keep))

    def _drop(self, n: int) -> None:
        if not self.dropped:
            self.dropped_since = _now()
        self.dropped += n

    def take_dropped(self) -> int:
        return self.take_dropped_span()[0]

    def take_dropped_span(self) -> tuple[int, str | None]:
        """``(records dropped, since when)``, reset (the ``dropped`` frame)."""
        with self._lock:
            n, since = self.dropped, self.dropped_since
            self.dropped, self.dropped_since = 0, None
            return n, since

    def __len__(self) -> int:
        return len(self._buf)
