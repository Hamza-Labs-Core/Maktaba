"""Canonical NOTIFY channel-name constants and SQLite pubsub shim.

Postgres callers use ``LISTEN``/``NOTIFY`` directly; SQLite has no
equivalent so we route through :class:`PubsubBus`. Subscribers
``await bus.subscribe(channel)`` and the producer side calls
``bus.publish(channel, payload)`` after a successful commit.

The channel-name constants are the canonical spelling used everywhere
in the codebase — never inline a string elsewhere. The plural form
(``jobs.*``, ``videos.*``, etc.) is the convention resolved by
``specs/epics/06-job-queue/README.md`` (REVIEW §2.3.a).
"""

from __future__ import annotations

import asyncio
import contextlib
import json
from collections import defaultdict
from typing import Any

__all__ = [
    "JOBS_FLAG_SET",
    "JOBS_FORCE_PAUSE",
    "JOBS_HEARTBEAT",
    "JOBS_NEW",
    "JOBS_PROGRESS",
    "JOBS_REAPED",
    "PubsubBus",
    "get_bus",
    "reset_bus",
]


# Canonical job-queue channel names. Story 6.1 produces JOBS_NEW; the
# remaining channels are produced by Stories 6.3/6.4/6.6 and listed
# here so refactors of a name only happen in one place.
JOBS_NEW = "jobs.new"
JOBS_FLAG_SET = "jobs.flag_set"
JOBS_PROGRESS = "jobs.progress"
JOBS_HEARTBEAT = "jobs.heartbeat"
JOBS_REAPED = "jobs.reaped"
JOBS_FORCE_PAUSE = "jobs.force_pause"


class PubsubBus:
    """In-process fanout for SQLite (and tests). One bus per process.

    The publisher serializes the payload to JSON once, then enqueues
    the resulting string on every subscriber's queue. Subscribers
    deserialize on read, mirroring the Postgres ``LISTEN``/``NOTIFY``
    contract where the payload is always a string the listener parses.

    The bus is intentionally minimal — no persistence, no replay, no
    backpressure. A subscriber that never drains its queue will leak
    memory; callers are expected to drain via ``await queue.get()``
    inside their normal event loop.
    """

    def __init__(self) -> None:
        self._subs: dict[str, list[asyncio.Queue[str]]] = defaultdict(list)

    def publish(self, channel: str, payload: dict[str, Any]) -> None:
        """Enqueue ``payload`` (as JSON) on every subscriber of ``channel``.

        Non-blocking; uses ``put_nowait`` so a wedged subscriber raises
        :class:`asyncio.QueueFull` instead of stalling the producer.
        """
        text = json.dumps(payload, separators=(",", ":"), default=str)
        for q in self._subs.get(channel, ()):
            q.put_nowait(text)

    async def subscribe(self, channel: str) -> asyncio.Queue[str]:
        """Register a fresh queue for ``channel`` and return it.

        The queue is unbounded; callers that need bounded backpressure
        should wrap the returned queue in their own bounded buffer.
        """
        q: asyncio.Queue[str] = asyncio.Queue()
        self._subs[channel].append(q)
        return q

    def unsubscribe(self, channel: str, queue: asyncio.Queue[str]) -> None:
        """Drop ``queue`` from ``channel``'s subscriber list."""
        if channel in self._subs:
            with contextlib.suppress(ValueError):
                self._subs[channel].remove(queue)


_BUS: PubsubBus | None = None


def get_bus() -> PubsubBus:
    """Return the process-wide PubsubBus, creating it on first call."""
    global _BUS
    if _BUS is None:
        _BUS = PubsubBus()
    return _BUS


def reset_bus() -> None:
    """Discard the process-wide bus. Test-only helper.

    Production code should never call this; the bus is meant to live
    for the lifetime of the process so subscribers and publishers
    share state.
    """
    global _BUS
    _BUS = None
