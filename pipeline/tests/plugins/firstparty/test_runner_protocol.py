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
    with tempfile.TemporaryDirectory(dir="/tmp") as d:
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
    db: Database, toy: bytes, monkeypatch: pytest.MonkeyPatch
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


async def test_a_failure_comes_back_as_the_handler_raised_it(
    db: Database, toy: bytes, monkeypatch: pytest.MonkeyPatch
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
