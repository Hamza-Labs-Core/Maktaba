"""One job of a first-party stage through its runner (Epic 54, stories 54.23 §2, 54.25 §3):
the worker's ``run_native`` and the runner's server on a real unix socket, the job's
progress and log relayed to the worker, statements through the SDK's fake API."""

from __future__ import annotations

import asyncio
import contextlib
import tempfile
from collections.abc import AsyncIterator
from pathlib import Path
from typing import Any

import pytest
from maktaba_plugin_sdk.native.local import LocalTransport

from maktaba_pipeline.engine.dispatch import EngineRuntime
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import bound_graph
from maktaba_pipeline.engine.sync import sync_graph
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_video
from tests.plugins.firstparty.conftest import toy_table

pytestmark = pytest.mark.asyncio


def toy_graph() -> dict[str, Any]:
    from maktaba_pipeline.plugins import firstparty

    graph = {s.name: s for s in bound_graph()}
    graph["fp_toy"] = firstparty.stage_def("fp_toy")
    return graph


async def toy_job(db: Database) -> tuple[Any, Any, list[Any]]:
    from maktaba_pipeline.engine.claim import claim_for_pool

    graph = toy_graph()
    await sync_graph(db, list(graph.values()))
    await toy_table(db)
    lib = await make_library(db, "مكتبة")
    vids = [await make_video(db, lib, title=f"فيديو {i}", size=100 + i) for i in range(3)]
    await enqueue_job(db, subject_kind="library", subject_id=lib, stage="fp_toy")
    job = await claim_for_pool(
        db, worker_id="test", pool="cpu", stages=["fp_toy"], scope="per_worker"
    )
    assert job is not None
    return job, EngineRuntime(graph=graph), vids


@contextlib.asynccontextmanager
async def runner(db: Database) -> AsyncIterator[Path]:
    from maktaba_pipeline.plugins import firstparty
    from maktaba_plugin_runner.server import Runner

    reg = firstparty.registry("tv.maktaba.toy")
    r = Runner(
        group="light",
        api_socket="",
        stages={"fp_toy": "fp_toy.stage:IMPL"},
        plugin_of={"fp_toy": "tv.maktaba.toy"},
        bundle=firstparty.bundle_digest(),
        transport=lambda _token: LocalTransport(reg, db),
    )
    with tempfile.TemporaryDirectory() as d:
        sock = Path(d) / "runner-light.sock"
        task = asyncio.create_task(r.serve(sock))
        for _ in range(100):
            if sock.exists():
                break
            await asyncio.sleep(0.02)
        try:
            yield sock
        finally:
            task.cancel()
            with contextlib.suppress(asyncio.CancelledError):
                await task


async def test_the_runner_writes_what_the_worker_writes(
    db: Database, toy: bytes, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    from maktaba_pipeline.plugins import native

    job, rt, vids = await toy_job(db)
    ctx = await rt.context_for(db, job)

    async def mint(_job_id: int) -> dict[str, Any]:
        return {"plugin_id": "tv.maktaba.toy", "invocation_id": "inv_1", "token": "t"}

    ended: list[str] = []

    async def end(i: str) -> None:
        ended.append(i)

    monkeypatch.setattr(native, "_mint", mint)
    monkeypatch.setattr(native, "_end", end)
    async with runner(db) as sock:
        res = await native.run_native(
            db, job, ctx, connect=lambda: asyncio.open_unix_connection(str(sock))
        )
    plugin_rows = sorted(
        tuple(r) for r in await db.fetch("SELECT video_id, title, n FROM fp_toy_out")
    )
    from fp_toy.stage import IMPL

    await db.execute("DELETE FROM fp_toy_out")
    inproc = await IMPL.run(db, job, ctx)
    inproc_rows = sorted(
        tuple(r) for r in await db.fetch("SELECT video_id, title, n FROM fp_toy_out")
    )
    assert plugin_rows == inproc_rows and len(plugin_rows) == 3
    assert (res.output_hash, res.summary) == (inproc.output_hash, inproc.summary)
    assert ended == ["inv_1"]
    # the job's log line went through the worker (job_events is the engine's)
    msgs = [
        r["message"]
        for r in await db.fetch("SELECT message FROM job_events WHERE job_id = $1", job.id)
    ]
    assert "toy wrote 3 rows" in msgs
    # the runner's own log names the job it ran (the Windows end to end reads it)
    assert job_lines(capsys) == [("light", "fp_toy", job.id, "ok")]


def job_lines(capsys: pytest.CaptureFixture[str]) -> list[tuple[Any, ...]]:
    import json

    out = []
    for line in capsys.readouterr().err.splitlines():
        with contextlib.suppress(ValueError):
            d = json.loads(line)
            if isinstance(d, dict) and d.get("event") == "plugin_runner_job":
                assert isinstance(d["ms"], float) and d["ms"] >= 0
                out.append((d["group"], d["stage"], d["job_id"], d["status"]))
    return out


async def test_a_failure_comes_back_as_the_handler_raised_it(
    db: Database, toy: bytes, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    from fp_toy import stage

    from maktaba_pipeline.engine.context import StageFailure
    from maktaba_pipeline.plugins import native

    job, rt, _ = await toy_job(db)
    ctx = await rt.context_for(db, job)

    async def mint(_job_id: int) -> dict[str, Any]:
        return {"plugin_id": "tv.maktaba.toy", "invocation_id": "inv_1", "token": "t"}

    async def boom(db: Any, job: Any, ctx: Any) -> Any:
        raise StageFailure("toy_broken", "the toy broke", retryable=False)

    monkeypatch.setattr(native, "_mint", mint)
    monkeypatch.setattr(native, "_end", lambda _i: asyncio.sleep(0))
    from dataclasses import replace

    monkeypatch.setattr(stage, "IMPL", replace(stage.IMPL, run=boom))
    async with runner(db) as sock:
        with pytest.raises(StageFailure) as e:
            await native.run_native(
                db, job, ctx, connect=lambda: asyncio.open_unix_connection(str(sock))
            )
    assert (e.value.kind, e.value.message, e.value.retryable) == (
        "toy_broken",
        "the toy broke",
        False,
    )
    assert job_lines(capsys) == [("light", "fp_toy", job.id, "failure")]


async def test_an_unregistered_statement_runs_the_job_in_process(
    db: Database, toy: bytes, monkeypatch: pytest.MonkeyPatch
) -> None:
    from fp_toy import stage

    from maktaba_pipeline.plugins import native

    job, rt, _ = await toy_job(db)
    ctx = await rt.context_for(db, job)

    async def sneaky(db: Any, job: Any, ctx: Any) -> Any:
        await db.fetch("SELECT * FROM home_secrets")
        raise AssertionError("never")

    async def mint(_job_id: int) -> dict[str, Any]:
        return {"plugin_id": "tv.maktaba.toy", "invocation_id": "inv_1", "token": "t"}

    monkeypatch.setattr(native, "_mint", mint)
    monkeypatch.setattr(native, "_end", lambda _i: asyncio.sleep(0))
    from dataclasses import replace

    monkeypatch.setattr(stage, "IMPL", replace(stage.IMPL, run=sneaky))
    async with runner(db) as sock:
        with pytest.raises(native.Unregistered):
            await native.run_native(
                db, job, ctx, connect=lambda: asyncio.open_unix_connection(str(sock))
            )


@pytest.mark.parametrize("wrapped", [False, True])
async def test_a_file_the_sandbox_refused_runs_the_job_in_process(
    db: Database, toy: bytes, monkeypatch: pytest.MonkeyPatch, wrapped: bool
) -> None:
    """A folder added since the runner started (its sandbox allows the old areas until it
    restarts): the refusal, raised bare or as a StageFailure's cause, comes back as
    sandbox_refused and the worker runs the job itself (engine/run.py)."""
    from fp_toy import stage

    from maktaba_pipeline.engine.context import StageFailure
    from maktaba_pipeline.plugins import native

    job, rt, _ = await toy_job(db)
    ctx = await rt.context_for(db, job)

    async def refused(db: Any, job: Any, ctx: Any) -> Any:
        try:
            raise PermissionError(1, "Operation not permitted", "/new/library/video.mkv")
        except PermissionError as exc:
            if wrapped:
                raise StageFailure("share_unreadable", str(exc), retryable=True) from exc
            raise

    async def mint(_job_id: int) -> dict[str, Any]:
        return {"plugin_id": "tv.maktaba.toy", "invocation_id": "inv_1", "token": "t"}

    monkeypatch.setattr(native, "_mint", mint)
    monkeypatch.setattr(native, "_end", lambda _i: asyncio.sleep(0))
    from dataclasses import replace

    monkeypatch.setattr(stage, "IMPL", replace(stage.IMPL, run=refused))
    async with runner(db) as sock:
        with pytest.raises(native.SandboxRefused):
            await native.run_native(
                db, job, ctx, connect=lambda: asyncio.open_unix_connection(str(sock))
            )


async def test_a_refusal_is_found_through_the_chain() -> None:
    import errno

    from maktaba_pipeline.plugins.native import sandbox_refused

    assert sandbox_refused(PermissionError("x"))
    assert sandbox_refused(OSError(errno.EPERM, "x"))
    assert not sandbox_refused(OSError(errno.ENOENT, "x"))
    try:
        try:
            raise PermissionError("deep")
        except PermissionError as inner:
            raise RuntimeError("outer") from inner
    except RuntimeError as outer:
        assert sandbox_refused(outer)
    assert not sandbox_refused(ValueError("x"))


async def test_a_runner_that_died_gives_the_job_back(
    db: Database, toy: bytes, monkeypatch: pytest.MonkeyPatch
) -> None:
    from maktaba_pipeline.engine.context import JobDeferred
    from maktaba_pipeline.plugins import native

    job, rt, _ = await toy_job(db)
    ctx = await rt.context_for(db, job)

    async def mint(_job_id: int) -> dict[str, Any]:
        return {"plugin_id": "tv.maktaba.toy", "invocation_id": "inv_1", "token": "t"}

    async def gone() -> Any:
        raise ConnectionRefusedError("no runner")

    monkeypatch.setattr(native, "_mint", mint)
    monkeypatch.setattr(native, "_end", lambda _i: asyncio.sleep(0))
    with pytest.raises(JobDeferred) as e:
        await native.run_native(db, job, ctx, connect=gone)
    assert e.value.reason.startswith("plugin_runner_unavailable")


def test_a_runner_is_idle_between_jobs_whatever_the_health_checks(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """A shared group's runner exits once idle (Epic 54: an idle home runs none). The
    supervisor's hello is not work; a run is, and a job still running keeps it busy."""
    import time

    from maktaba_plugin_runner.__main__ import idle_seconds
    from maktaba_plugin_runner.server import Runner

    monkeypatch.delenv("MAKTABA_RUNNER_IDLE_SEC", raising=False)
    assert idle_seconds() == 0.0  # ml and index: never
    monkeypatch.setenv("MAKTABA_RUNNER_IDLE_SEC", "600")
    assert idle_seconds() == 600.0
    monkeypatch.setenv("MAKTABA_RUNNER_IDLE_SEC", "soon")
    assert idle_seconds() == 0.0
    r = Runner(group="light", api_socket="", stages={}, plugin_of={}, bundle="x")
    r.last_active = time.monotonic() - 700
    assert r.idle_for() >= 700
    r.running[1] = None  # type: ignore[assignment]
    assert r.idle_for() == 0.0
    r.running.clear()
    assert r.idle_for() < 1.0


@pytest.mark.asyncio
async def test_the_worker_asks_for_a_runner_that_is_not_up(monkeypatch: pytest.MonkeyPatch) -> None:
    """A job whose runner is not up runs in the worker, which asks the supervisor for the
    runner, at most once a minute per group, and never without a supervisor."""
    import asyncio
    import urllib.request

    from maktaba_pipeline.plugins import native

    asked: list[str] = []

    class _Res:
        def __enter__(self) -> _Res:
            return self

        def __exit__(self, *a: object) -> None:
            return None

        def read(self) -> bytes:
            return b""

    def urlopen(req: urllib.request.Request, timeout: float = 0) -> _Res:
        asked.append(f"{req.get_method()} {req.full_url}")
        return _Res()

    monkeypatch.setattr(urllib.request, "urlopen", urlopen)
    native._ASKED.clear()
    monkeypatch.delenv("MAKTABA_SUPERVISOR_URL", raising=False)
    native.want_runner("light")
    monkeypatch.setenv("MAKTABA_SUPERVISOR_URL", "http://127.0.0.1:9/")
    native.want_runner("light")
    native.want_runner("light")  # asked a moment ago: not again
    native.want_runner("files")
    await asyncio.gather(*list(native._ASKING))
    assert sorted(asked) == [
        "POST http://127.0.0.1:9/runners/files/start",
        "POST http://127.0.0.1:9/runners/light/start",
    ]
    native._ASKED.clear()


def test_the_job_line_names_what_the_broker_answered(capsys: pytest.CaptureFixture[str]) -> None:
    """On Windows with a share brokered, the runner's job line carries what the launcher's
    broker answered for the job (CI's runner-stages proof that a share was read through it);
    without one the line has no such field."""
    import json

    from maktaba_plugin_runner.server import job_line

    job_line("light", "thumb_small", 5, "ok", 0.02, {"stat": 2, "stat_missing": 1, "open": 1})
    job_line("light", "thumb_small", 6, "ok", 0.02, {}, ["C:\\x\\a.jpg"])
    first, second = (json.loads(x) for x in capsys.readouterr().err.splitlines())
    assert first["broker"] == {"stat": 2, "stat_missing": 1, "open": 1}
    assert "broker" not in second and "missing" not in first
    assert second["missing"] == ["C:\\x\\a.jpg"]


async def test_a_fingerprint_the_sandbox_refuses_is_answered_never_dropped(
    db: Database, toy: bytes, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    """A fingerprint that raises in the runner (on Windows, a share path every runner's shim
    refuses) comes back as an error answer, sandbox_refused for a refusal, and is logged: a
    dropped connection gave the worker an IncompleteReadError it did not fall back on, and the
    job went back to pending."""
    import uuid

    from maktaba_pipeline.plugins import firstparty
    from maktaba_plugin_runner import server

    async def refused(_db: Any, _subject: Any) -> str:
        raise PermissionError(13, "a share this runner is not brokered", "\\\\nas\\m\\a.mp4")

    async def broken(_db: Any, _subject: Any) -> str:
        raise RuntimeError("boom")

    reg = firstparty.registry("tv.maktaba.toy")
    r = server.Runner(
        group="fp",
        api_socket="",
        stages={"fp_toy": "fp_toy.stage:IMPL"},
        plugin_of={"fp_toy": "tv.maktaba.toy"},
        bundle="x",
        transport=lambda _token: LocalTransport(reg, db),
    )
    msg = {
        "stage": "fp_toy",
        "kind": "input",
        "token": "t",
        "subjects": [["library", str(uuid.uuid4()), None]],
    }
    for fn, status in ((refused, "sandbox_refused"), (broken, "failed")):
        impl = type("Impl", (), {"input_fingerprint": staticmethod(fn), "output_fingerprint": None})
        monkeypatch.setattr(server, "load_impl", lambda _entry, impl=impl: impl)
        out = await r._fingerprints(msg)
        assert out["type"] == "error" and out["status"] == status
    lines = [
        x for x in capsys.readouterr().err.splitlines() if "plugin_runner_fingerprint_failed" in x
    ]
    assert len(lines) == 2 and '"sandbox_refused"' in lines[0] and '"failed"' in lines[1]


async def test_a_runner_that_closes_without_a_fingerprint_is_read_in_process(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """The worker reads the fingerprint itself when the runner closes without answering
    (an IncompleteReadError), as for any refusal, instead of failing the job."""
    from maktaba_pipeline.plugins import native

    async def closed(*_a: Any, **_k: Any) -> Any:
        raise asyncio.IncompleteReadError(b"", 4)

    async def inproc(_db: Any, _subject: Any) -> str:
        return "in-process"

    monkeypatch.setattr(native, "fingerprints", closed)
    fn = native.fingerprint_fn("fp_toy", "input", inproc)
    assert await fn(None, object()) == "in-process"
