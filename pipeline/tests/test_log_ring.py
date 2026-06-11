"""Tests for the pipeline log ring buffer + /logs/recent HTTP endpoint."""

from __future__ import annotations

import json
from datetime import UTC, datetime, timedelta

import pytest

from maktaba_pipeline.log import get_logger, init
from maktaba_pipeline.log.http import build_recent_response
from maktaba_pipeline.log.ring import LogRingBuffer, get_ring, level_rank


@pytest.mark.unit
def test_init_installs_ring_and_captures_lines() -> None:
    init(service="pipeline", env="prod", version="t", ring_capacity=100)
    log = get_logger()
    log.info("hello_ring", k="v")

    ring = get_ring()
    assert ring is not None
    lines = ring.recent()
    assert any("hello_ring" in line for line in lines)
    # The captured copy must carry the Go field contract.
    rec = json.loads(lines[-1])
    for key in ("ts", "level", "service", "msg", "version", "env"):
        assert key in rec, f"missing {key}: {rec}"
    assert rec["msg"] == "hello_ring"
    assert rec["service"] == "pipeline"


@pytest.mark.unit
def test_ring_redacts_secrets() -> None:
    init(service="pipeline", env="prod", version="t", ring_capacity=100)
    log = get_logger()
    log.info("auth_attempt", password="hunter2", user_id="u1")

    ring = get_ring()
    assert ring is not None
    line = ring.recent()[-1]
    assert "hunter2" not in line
    assert "***REDACTED***" in line


@pytest.mark.unit
def test_ring_evicts_oldest() -> None:
    ring = LogRingBuffer(capacity=3)
    for i in range(5):
        ring.append(json.dumps({"level": "info", "service": "pipeline", "i": i}))
    lines = ring.recent()
    assert len(lines) == 3
    assert [json.loads(x)["i"] for x in lines] == [2, 3, 4]


@pytest.mark.unit
def test_ring_filters() -> None:
    ring = LogRingBuffer(capacity=50)
    now = datetime.now(UTC)
    ring.append(
        json.dumps({"ts": now.isoformat(), "level": "info", "service": "pipeline", "msg": "needle"})
    )
    ring.append(
        json.dumps({"ts": now.isoformat(), "level": "error", "service": "pipeline", "msg": "boom"})
    )

    assert len(ring.recent(min_level="error")) == 1
    assert len(ring.recent(search="NEEDLE")) == 1
    assert len(ring.recent(services=frozenset({"streaming"}))) == 0
    assert len(ring.recent(since=now + timedelta(hours=1))) == 0
    assert len(ring.recent(limit=1)) == 1


@pytest.mark.unit
def test_level_rank_handles_warn_and_warning() -> None:
    assert level_rank("warn") == level_rank("warning")
    assert level_rank("error") > level_rank("info") > level_rank("debug")


@pytest.mark.unit
def test_recent_response_routing_and_filtering() -> None:
    ring = LogRingBuffer(capacity=50)
    ring.append(json.dumps({"level": "info", "service": "pipeline", "msg": "http_line_info"}))
    ring.append(json.dumps({"level": "error", "service": "pipeline", "msg": "http_line_error"}))

    # Default ndjson, filtered to error.
    status, ct, body = build_recent_response("/logs/recent?level=error", ring)
    assert status == 200
    assert ct == "application/x-ndjson"
    lines = [ln for ln in body.decode().split("\n") if ln]
    assert len(lines) == 1 and "http_line_error" in lines[0]

    # JSON array form.
    status, ct, body = build_recent_response("/logs/recent?format=json", ring)
    assert status == 200 and ct == "application/json"
    assert json.loads(body)["count"] == 2

    # Unknown path → 404; nil ring → 503.
    assert build_recent_response("/nope", ring)[0] == 404
    assert build_recent_response("/logs/recent", None)[0] == 503
