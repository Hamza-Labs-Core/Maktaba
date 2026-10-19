"""The log shipper: levels, its own messages, the bounded buffer, the structlog hook."""

from __future__ import annotations

import logging
from collections.abc import Iterator
from typing import Any

import pytest
import structlog

from maktaba_pipeline import log as mlog
from maktaba_pipeline.cluster.agent.logship import (
    AGENT_LOGGER,
    LogShipper,
    make_record,
    normalize_level,
)

pytestmark = pytest.mark.unit


def ev(level: str, event: str = "x", **fields: Any) -> dict[str, Any]:
    return {"level": level, "event": event, "ts": "2026-10-19T00:00:01Z", **fields}


def test_record_shape() -> None:
    rec = make_record(
        ev("warning", "disk low", logger="engine", free=12, service="pipeline", obj=object())
    )
    assert rec["ts"] == "2026-10-19T00:00:01Z"
    assert rec["level"] == "warning" and rec["event"] == "disk low"
    assert rec["logger"] == "engine"
    assert rec["fields"]["free"] == 12
    assert "service" not in rec["fields"]
    assert isinstance(rec["fields"]["obj"], str)
    assert make_record({"level": "info", "msg": "renamed"})["event"] == "renamed"


def test_levels() -> None:
    assert normalize_level("WARN") == "warning"
    assert normalize_level("nonsense", "error") == "error"
    s = LogShipper("info")
    s.sink(ev("debug"))
    s.sink(ev("info"))
    s.sink(ev("error"))
    assert [r["level"] for r in s.take()] == ["info", "error"]
    assert s.set_level("error") == "error"
    s.sink(ev("warning"))
    s.sink(ev("critical"))
    assert [r["level"] for r in s.take()] == ["critical"]


def test_the_agents_own_messages_are_not_shipped_below_warning() -> None:
    s = LogShipper("debug")
    s.sink(ev("info", "cluster_agent_connected", logger=AGENT_LOGGER))
    s.sink(ev("debug", "cluster_agent_unknown_frame", logger=AGENT_LOGGER))
    s.sink(ev("warning", "cluster_agent_refused", logger=AGENT_LOGGER))
    assert [r["event"] for r in s.take()] == ["cluster_agent_refused"]


def test_bounded_buffer_counts_drops_and_takes_back() -> None:
    files: list[dict[str, Any]] = []
    s = LogShipper("info", capacity=3, file_sink=files.append)
    for i in range(5):
        s.sink(ev("info", f"e{i}"))
    assert len(files) == 5  # the file gets every record
    assert s.take_dropped() == 2
    assert s.take_dropped() == 0
    batch = s.take(2)
    assert [r["event"] for r in batch] == ["e2", "e3"]
    s.sink(ev("info", "e5"))
    s.put_back(batch)  # a send failed: back in front, as many as fit
    assert [r["event"] for r in s.take()] == ["e3", "e4", "e5"]
    assert s.take_dropped() == 1


@pytest.fixture
def structlog_with_sinks() -> Iterator[None]:
    mlog.init(service="t", env="prod", ring_capacity=-1)
    yield
    mlog.set_sink_level(logging.INFO)
    # the configuration printed to this test's captured stdout: back to the defaults
    structlog.reset_defaults()
    structlog.contextvars.clear_contextvars()


def test_structlog_records_reach_the_shipper(
    structlog_with_sinks: None, capsys: pytest.CaptureFixture[str]
) -> None:
    s = LogShipper("info")
    s.install()
    try:
        log = structlog.get_logger()
        log.info("hello", n=1)
        log.debug("quiet")
        assert [r["event"] for r in s.take()] == ["hello"]
        # debug for the shipper: debug records reach it, the console stays at info
        s.set_level("debug")
        log.debug("detail", k="v")
        got = s.take()
        assert [r["event"] for r in got] == ["detail"]
        assert got[0]["fields"]["k"] == "v"
        out = capsys.readouterr().out
        assert "hello" in out and "detail" not in out
    finally:
        s.uninstall()
    structlog.get_logger().info("after")
    assert s.take() == []
