"""Process-wide error routes into structlog: stdlib logging, excepthooks, asyncio."""

from __future__ import annotations

import asyncio
import logging
import sys
import threading
from collections.abc import Iterator

import pytest
from structlog.testing import capture_logs

from maktaba_pipeline.log import hooks


@pytest.fixture(autouse=True)
def _installed() -> Iterator[None]:
    hooks.install()
    yield
    hooks.uninstall()


@pytest.mark.unit
def test_stdlib_warnings_reach_structlog_and_info_does_not() -> None:
    lib = logging.getLogger("grpc._server")
    with capture_logs() as logs:
        lib.info("chatter")
        lib.warning("peer %s went away", "10.0.0.2")
        try:
            raise ValueError("bad frame")
        except ValueError:
            lib.exception("handler crashed")
    events = [(e["event"], e["log_level"], e.get("logger")) for e in logs]
    assert ("chatter", "info", "grpc._server") not in events
    assert ("peer 10.0.0.2 went away", "warning", "grpc._server") in events
    crashed = next(e for e in logs if e["event"] == "handler crashed")
    assert crashed["log_level"] == "error" and crashed["exc_info"][0] is ValueError


@pytest.mark.unit
def test_install_is_idempotent() -> None:
    hooks.install()
    forwarders = [
        h for h in logging.getLogger().handlers if isinstance(h, hooks.StructlogForwarder)
    ]
    assert len(forwarders) == 1


@pytest.mark.unit
def test_uncaught_thread_exception_is_logged() -> None:
    def die() -> None:
        raise RuntimeError("worker died")

    with capture_logs() as logs:
        t = threading.Thread(target=die, name="share-listdir")
        t.start()
        t.join()
    rec = next(e for e in logs if e["event"] == "unhandled_exception")
    assert rec["source"] == "thread" and rec["thread"] == "share-listdir"
    assert rec["log_level"] == "error" and rec["exc_info"][0] is RuntimeError


@pytest.mark.unit
def test_sys_excepthook_logs_critical() -> None:
    assert sys.excepthook is hooks._excepthook
    try:
        raise KeyError("boom")
    except KeyError:
        info = sys.exc_info()
    with capture_logs() as logs:
        sys.excepthook(*info)
    rec = next(e for e in logs if e["event"] == "unhandled_exception")
    assert rec["source"] == "main" and rec["log_level"] == "critical"


@pytest.mark.asyncio
async def test_asyncio_handler_logs_unretrieved_task_exception() -> None:
    async def scenario() -> None:
        loop = asyncio.get_running_loop()
        loop.set_exception_handler(hooks.asyncio_exception_handler)

        async def fail() -> None:
            raise OSError("socket closed")

        task = asyncio.create_task(fail(), name="job-7-probe")
        await asyncio.sleep(0)
        await asyncio.sleep(0)
        loop.call_exception_handler(
            {
                "message": "Task exception was never retrieved",
                "exception": task.exception(),
                "task": task,
            }
        )

    with capture_logs() as logs:
        await scenario()
    rec = next(e for e in logs if e["event"] == "unhandled_exception")
    assert rec["source"] == "asyncio" and rec["task"] == "job-7-probe"
    assert rec["message"] == "Task exception was never retrieved"
    assert rec["exc_info"][0] is OSError
