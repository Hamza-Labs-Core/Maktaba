"""Tests for the pipeline's /healthz event-loop heartbeat (Story 22.9)."""

from __future__ import annotations

import asyncio
import json
import urllib.error
import urllib.request

import pytest

from maktaba_pipeline.log.http import build_health_response, start_log_server
from maktaba_pipeline.log.liveness import (
    DEFAULT_STALE_SEC,
    LoopHeartbeat,
    run_heartbeat,
    set_heartbeat,
)


class _Clock:
    def __init__(self) -> None:
        self.t = 1000.0

    def __call__(self) -> float:
        return self.t


@pytest.mark.unit
def test_healthy_while_beating_and_503_once_stale() -> None:
    clock = _Clock()
    hb = LoopHeartbeat(stale_sec=60, clock=clock)
    assert build_health_response(hb)[0] == 200

    clock.t += 61
    status, ctype, body = build_health_response(hb)
    assert status == 503
    assert ctype == "application/json"
    doc = json.loads(body)
    assert doc["status"] == "fail"
    assert "no heartbeat for 61s" in doc["checks"]["event_loop"]["reason"]

    hb.beat()
    assert build_health_response(hb)[0] == 200


@pytest.mark.unit
def test_no_heartbeat_installed_is_alive() -> None:
    assert build_health_response(None)[0] == 200


@pytest.mark.unit
def test_stale_window_from_env(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("MAKTABA_PIPELINE_LOOP_STALE", "90")
    assert LoopHeartbeat().stale_sec == 90
    monkeypatch.setenv("MAKTABA_PIPELINE_LOOP_STALE", "nope")
    assert LoopHeartbeat().stale_sec == DEFAULT_STALE_SEC


# asyncio's event loop opens a self-pipe socket: integration tier.
@pytest.mark.integration
def test_run_heartbeat_beats_on_the_loop() -> None:
    clock = _Clock()
    hb = LoopHeartbeat(stale_sec=10, clock=clock)

    async def go() -> None:
        task = asyncio.create_task(run_heartbeat(hb, interval=0.01))
        await asyncio.sleep(0)
        clock.t += 100
        await asyncio.sleep(0.05)
        task.cancel()

    asyncio.run(go())
    assert hb.healthy()


@pytest.mark.integration
def test_http_server_serves_healthz() -> None:
    clock = _Clock()
    hb = LoopHeartbeat(stale_sec=10, clock=clock)
    set_heartbeat(hb)
    server = start_log_server("127.0.0.1:0")
    try:
        port = server.server_address[1]
        with urllib.request.urlopen(f"http://127.0.0.1:{port}/healthz", timeout=5) as resp:
            assert resp.status == 200
        clock.t += 11
        with pytest.raises(urllib.error.HTTPError) as err:
            urllib.request.urlopen(f"http://127.0.0.1:{port}/healthz", timeout=5)
        assert err.value.code == 503
    finally:
        server.shutdown()
        set_heartbeat(None)
