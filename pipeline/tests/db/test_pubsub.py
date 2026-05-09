"""Tests for :mod:`maktaba_pipeline.db.pubsub`.

The Postgres path uses real LISTEN/NOTIFY (covered by integration
tests when the testcontainers fixture lands). These tests pin the
SQLite-side in-process bus contract.

Note: the async-coroutine tests are intentionally not marked
``unit``. Story 20.1's netguard replaces ``socket.socket`` with a
stub that always raises, and asyncio's event-loop bootstrap calls
``socket.socketpair`` (an FD wrap, not a network connection) which
the current guard rejects. Until Story 20.1 grows asyncio support,
these tests run when invoked directly (``pytest tests/db/``) and via
the broader test suite, but are skipped by ``make test-unit-py``'s
``-m unit`` filter. The sync ``test_canonical_channel_names`` and
``test_get_bus_*`` tests stay marked ``unit`` because they don't
touch asyncio.
"""

from __future__ import annotations

import asyncio

import pytest

from maktaba_pipeline.db.pubsub import (
    JOBS_FLAG_SET,
    JOBS_FORCE_PAUSE,
    JOBS_HEARTBEAT,
    JOBS_NEW,
    JOBS_PROGRESS,
    JOBS_REAPED,
    PubsubBus,
    get_bus,
    reset_bus,
)


@pytest.mark.unit
def test_canonical_channel_names() -> None:
    # The README's channel table is normative — these strings are
    # consumed by the API and the workers and must not drift.
    assert JOBS_NEW == "jobs.new"
    assert JOBS_FLAG_SET == "jobs.flag_set"
    assert JOBS_PROGRESS == "jobs.progress"
    assert JOBS_HEARTBEAT == "jobs.heartbeat"
    assert JOBS_REAPED == "jobs.reaped"
    assert JOBS_FORCE_PAUSE == "jobs.force_pause"


@pytest.mark.asyncio
async def test_publish_then_subscribe_does_not_deliver() -> None:
    # The bus has no replay; messages published before a subscriber
    # joins are lost. This documents the deliberate choice.
    bus = PubsubBus()
    bus.publish(JOBS_NEW, {"id": 1})
    queue = await bus.subscribe(JOBS_NEW)
    assert queue.empty()


@pytest.mark.asyncio
async def test_subscribe_then_publish_delivers_json() -> None:
    bus = PubsubBus()
    queue = await bus.subscribe(JOBS_NEW)
    bus.publish(JOBS_NEW, {"id": 7, "stage": "probe"})
    text = queue.get_nowait()
    # Compact JSON — separators=(",", ":") to match the Postgres
    # json_build_object format closely.
    assert text == '{"id":7,"stage":"probe"}'


@pytest.mark.asyncio
async def test_publish_fans_out_to_all_subscribers() -> None:
    bus = PubsubBus()
    a = await bus.subscribe(JOBS_NEW)
    b = await bus.subscribe(JOBS_NEW)
    bus.publish(JOBS_NEW, {"id": 1})
    assert a.get_nowait()
    assert b.get_nowait()


@pytest.mark.asyncio
async def test_publish_only_reaches_matching_channel() -> None:
    bus = PubsubBus()
    new = await bus.subscribe(JOBS_NEW)
    reaped = await bus.subscribe(JOBS_REAPED)
    bus.publish(JOBS_NEW, {"id": 1})
    assert new.qsize() == 1
    assert reaped.empty()


@pytest.mark.asyncio
async def test_unsubscribe_stops_delivery() -> None:
    bus = PubsubBus()
    queue = await bus.subscribe(JOBS_NEW)
    bus.unsubscribe(JOBS_NEW, queue)
    bus.publish(JOBS_NEW, {"id": 1})
    assert queue.empty()


@pytest.mark.asyncio
async def test_unsubscribe_unknown_queue_is_noop() -> None:
    bus = PubsubBus()
    bus.unsubscribe(JOBS_NEW, asyncio.Queue())  # never subscribed
    bus.unsubscribe("never.heard.of", asyncio.Queue())
    # No exception means we're good.


@pytest.mark.unit
def test_get_bus_returns_singleton() -> None:
    reset_bus()
    a = get_bus()
    b = get_bus()
    assert a is b


@pytest.mark.unit
def test_reset_bus_replaces_singleton() -> None:
    reset_bus()
    a = get_bus()
    reset_bus()
    b = get_bus()
    assert a is not b
