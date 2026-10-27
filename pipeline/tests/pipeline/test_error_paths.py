"""Errors / logging / crashes audit (pipeline): failures that were silent, or hung.

- a job that heartbeats without progress is logged as ``job_stalled``;
- ffmpeg thumbnails are bounded by a timeout and cannot deadlock on a full stderr pipe;
- "older schema" fallbacks tell a missing table from a real failure;
- an STT backend whose health check raises says why;
- a background task that dies says so when it does;
- a stage context that cannot be built fails the job instead of leaving it claimed.
"""

from __future__ import annotations

import asyncio
import sys
from pathlib import Path
from types import SimpleNamespace
from typing import Any, cast

import pytest
from structlog.testing import capture_logs

from maktaba_pipeline import progress_clock
from maktaba_pipeline.cluster.agent import jobs as job_events
from maktaba_pipeline.db import schema_compat
from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.db.jobs_state import StageError
from maktaba_pipeline.engine import dispatch as dispatch_mod
from maktaba_pipeline.engine.dispatch import EngineRuntime, dispatch_job
from maktaba_pipeline.log import get_logger
from maktaba_pipeline.log.tasks import watch
from maktaba_pipeline.pipeline.heartbeat import DEFAULT_STALL_WARN_SEC, HeartbeatTask
from maktaba_pipeline.sharepath.migrate import _table_exists
from maktaba_pipeline.stt.protocol import BackendHealth
from maktaba_pipeline.stt.registry import BackendRegistry, NoBackendReady, pick_backend
from maktaba_pipeline.thumbnail.generator import ThumbnailError, _run_ffmpeg

# --- older schema --------------------------------------------------------------------


class _UndefinedTableError(Exception):
    sqlstate = "42P01"


class _PostgresConnectionError(Exception):
    pass


# asyncpg's class name is what connection_lost matches
_PostgresConnectionError.__name__ = "PostgresConnectionError"


@pytest.mark.unit
def test_older_schema_is_only_a_missing_table_or_column() -> None:
    assert schema_compat.older_schema(_UndefinedTableError("relation does not exist"))
    assert schema_compat.older_schema(Exception("no such table: storage_servers"))
    assert schema_compat.older_schema(Exception("no such column: paused_by"))
    assert not schema_compat.older_schema(Exception('syntax error at or near "FORM"'))
    assert not schema_compat.older_schema(PermissionError("permission denied"))


@pytest.mark.unit
def test_fallback_logs_at_the_level_its_cause_deserves() -> None:
    log = get_logger()
    with capture_logs() as logs:
        a = schema_compat.fallback(log, "x_unavailable", Exception("no such table: x"))
        b = schema_compat.fallback(log, "x_unavailable", _PostgresConnectionError("gone"))
        c = schema_compat.fallback(log, "x_unavailable", RuntimeError("syntax error"), table="t")
    assert (a, b, c) == ("older_schema", "connection_lost", "error")
    assert [e["log_level"] for e in logs] == ["debug", "info", "warning"]
    real = logs[2]
    assert real["cause"] == "error" and real["table"] == "t"
    assert real["error"] == "RuntimeError: syntax error"
    assert isinstance(real["exc_info"], RuntimeError)


class _FetchRaises:
    def __init__(self, exc: BaseException) -> None:
        self.exc = exc

    async def fetch(self, *_: Any) -> list[Any]:
        raise self.exc


@pytest.mark.integration
def test_table_exists_raises_a_real_failure() -> None:
    """A failure taken for a missing table left that table out of a backup or a restore."""
    missing = asyncio.run(_table_exists(_FetchRaises(Exception("no such table: videos")), "videos"))
    assert missing is False
    with pytest.raises(RuntimeError, match="disk I/O error"):
        asyncio.run(_table_exists(_FetchRaises(RuntimeError("disk I/O error")), "videos"))


# --- stalled jobs --------------------------------------------------------------------


class _Clock:
    def __init__(self) -> None:
        self.t = 1000.0

    def __call__(self) -> float:
        return self.t


@pytest.mark.unit
def test_a_job_without_progress_is_logged_stalled_then_resumed() -> None:
    clock = _Clock()
    hb = HeartbeatTask(cast(Any, None), job_id=91, interval_sec=5, stall_warn_sec=60, clock=clock)
    progress_clock.forget(91)
    with capture_logs() as logs:
        clock.t += 59
        assert hb.check_stall() is None
        clock.t += 2  # 61 s without progress
        assert hb.check_stall() == 61
        clock.t += 30  # 91 s: still the first window, no second line
        assert hb.check_stall() is None
        clock.t += 30  # 121 s: the second window
        assert hb.check_stall() == 121
        progress_clock.note_progress(91, now=clock.t)
        clock.t += 1
        assert hb.check_stall() is None
    events = [(e["event"], e.get("idle_sec")) for e in logs]
    assert events == [
        ("job_stalled", 61),
        ("job_stalled", 121),
        ("job_progress_resumed", None),
    ]
    stalled = logs[0]
    assert stalled["log_level"] == "warning" and stalled["job_id"] == 91
    assert stalled["progress_reported"] is False
    progress_clock.forget(91)


@pytest.mark.unit
def test_progress_reported_through_the_tracker_resets_the_stall_clock() -> None:
    clock = _Clock()
    hb = HeartbeatTask(cast(Any, None), job_id=92, interval_sec=5, stall_warn_sec=60, clock=clock)
    clock.t += 50
    job_events.progress(92, step="decode", done=1, total=2)  # the real monotonic clock
    last = progress_clock.last_progress(92)
    assert last is not None
    progress_clock.note_progress(92, now=clock.t)  # pin it to the fake clock
    clock.t += 59
    with capture_logs() as logs:
        assert hb.check_stall() is None
    assert logs == []
    progress_clock.forget(92)


@pytest.mark.unit
def test_stall_warning_default_is_generous() -> None:
    """Detection only, and some stages report no progress: half an hour, not seconds."""
    assert DEFAULT_STALL_WARN_SEC >= 900


# --- ffmpeg thumbnails ------------------------------------------------------------------


@pytest.mark.integration
def test_thumbnail_ffmpeg_that_hangs_times_out(tmp_path: Path) -> None:
    out = tmp_path / "poster.jpg"
    out.write_bytes(b"partial")
    args = [sys.executable, "-c", "import time; time.sleep(30)", str(out)]
    with pytest.raises(ThumbnailError) as err:
        asyncio.run(_run_ffmpeg(args, timeout_sec=0.5))
    assert err.value.kind == "ffmpeg_timeout"
    assert "timed out" in str(err.value)
    assert not out.exists()  # a partial frame is never served


@pytest.mark.integration
def test_thumbnail_ffmpeg_with_a_flood_of_stderr_does_not_deadlock(tmp_path: Path) -> None:
    """wait() with stderr on a pipe hung once the pipe filled (a damaged file logs a line
    per bad frame); the job then heartbeat for ever."""
    out = tmp_path / "poster.jpg"
    script = "import sys\nfor _ in range(20000): sys.stderr.write('x' * 80 + '\\n')\nsys.exit(3)"
    args = [sys.executable, "-c", script, str(out)]
    with pytest.raises(ThumbnailError) as err:
        asyncio.run(_run_ffmpeg(args, timeout_sec=20))
    assert err.value.kind == "ffmpeg_thumbnail" and err.value.returncode == 3
    assert err.value.stderr_tail.endswith("x\n")
    assert len(err.value.stderr_tail) <= 2000


# --- STT backends --------------------------------------------------------------------


class _BrokenBackend:
    name = "whisper-broken"

    async def health(self) -> BackendHealth:
        raise RuntimeError("CUDA driver version is insufficient")


@pytest.mark.integration
def test_a_backend_whose_health_raises_says_why() -> None:
    reg = BackendRegistry()
    reg.register(cast(Any, _BrokenBackend()))
    with capture_logs() as logs:
        assert asyncio.run(reg.list_ready()) == []
        health = asyncio.run(reg.health_map())
        with pytest.raises(NoBackendReady):
            asyncio.run(pick_backend(reg, primary="whisper-broken"))
    assert health["whisper-broken"].ready is False
    failed = [e for e in logs if e["event"] == "stt_backend_health_failed"]
    assert len(failed) == 3
    assert failed[0]["backend"] == "whisper-broken"
    assert "CUDA driver version is insufficient" in failed[0]["error"]
    assert failed[0]["log_level"] == "warning"


# --- background tasks ----------------------------------------------------------------


@pytest.mark.integration
def test_a_background_task_that_dies_is_logged_when_it_does() -> None:
    async def die() -> None:
        raise ValueError("reconcile query broke")

    async def finish() -> None:
        return None

    async def go() -> None:
        dead = watch(asyncio.create_task(die(), name="reconciler"))
        fine = watch(asyncio.create_task(finish(), name="pruner"))
        stopped = watch(asyncio.create_task(asyncio.sleep(30), name="watchers"))
        await asyncio.sleep(0.01)
        stopped.cancel()
        await asyncio.gather(dead, fine, stopped, return_exceptions=True)

    with capture_logs() as logs:
        asyncio.run(go())
    failed = [e for e in logs if e["event"] == "background_task_failed"]
    assert len(failed) == 1
    assert failed[0]["task"] == "reconciler" and failed[0]["log_level"] == "error"
    assert failed[0]["error"] == "ValueError: reconcile query broke"


# --- stage context -------------------------------------------------------------------


@pytest.mark.integration
def test_a_stage_context_that_cannot_be_built_fails_the_job(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    failed: list[tuple[int, StageError]] = []

    async def record(db: Any, *, job_id: int, error: StageError) -> Any:
        failed.append((job_id, error))
        return SimpleNamespace(state="pending")

    async def context_for(db: Any, job: Any) -> Any:
        raise ConnectionResetError("database went away")

    monkeypatch.setattr(dispatch_mod, "mark_failed_or_retry", record)
    runtime = SimpleNamespace(graph={"tag": SimpleNamespace(legacy=False)}, context_for=context_for)
    job = cast(Job, SimpleNamespace(id=7, stage="tag"))
    with capture_logs() as logs:
        asyncio.run(
            dispatch_job(object(), job, runtime=cast(EngineRuntime, runtime), legacy_handlers={})
        )
    assert len(failed) == 1
    job_id, err = failed[0]
    assert job_id == 7 and err.kind == "stage_context_failed" and err.retryable
    assert err.message == "ConnectionResetError: database went away"
    assert [e["event"] for e in logs if e["log_level"] == "error"] == ["stage_context_failed"]
