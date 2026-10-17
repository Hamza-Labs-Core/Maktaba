"""Engine core: skip check, change-driven propagation, follow-ups, failures (plan 06-11 D4-D6)."""

from __future__ import annotations

import dataclasses
import json

import pytest

from maktaba_pipeline.db.jobs import DBConn, Job
from maktaba_pipeline.engine.claim import claim_for_pool
from maktaba_pipeline.engine.context import StageContext
from maktaba_pipeline.engine.dispatch import dispatch_job
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import CATALOG_ID, Request, StageResult
from maktaba_pipeline.engine.sql import loads
from maktaba_pipeline.engine.sync import sync_graph
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_video
from tests.engine.toy import Recorder, drain, rows, setup, toy_graph


@pytest.mark.asyncio
async def test_changed_output_enqueues_exactly_the_declared_edges(db: Database) -> None:
    rec = Recorder()
    rt = await setup(db, rec)
    lib = await make_library(db)
    v = await make_video(db, lib)
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    assert await drain(db, rt) == 3  # a -> b -> c
    assert [c[0] for c in rec.calls] == ["t_a", "t_b", "t_c"]
    b = (await rows(db, "t_b"))[0]
    assert b["subject_kind"] == "library" and str(b["subject_id"]) == str(lib)
    assert b["triggered_by_stage"] == "t_a"
    assert loads(b["reasons"])[0]["why"] == "t_a output changed"
    c = (await rows(db, "t_c"))[0]
    assert str(c["subject_id"]) == str(CATALOG_ID)


@pytest.mark.asyncio
async def test_unchanged_output_enqueues_nothing(db: Database) -> None:
    rec = Recorder()
    rt = await setup(db, rec)
    v = await make_video(db, await make_library(db))
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt)
    before = len(await rows(db))
    # same output hash again: the stage reruns (no input fingerprint) but nothing propagates
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    assert await drain(db, rt) == 1
    assert len(await rows(db)) == before + 1
    last = (await rows(db, "t_a"))[-1]
    assert last["outcome"] == "unchanged"
    assert len(await rows(db, "t_b")) == 1


@pytest.mark.asyncio
async def test_output_change_propagates_again(db: Database) -> None:
    rec = Recorder()
    rt = await setup(db, rec)
    v = await make_video(db, await make_library(db))
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt)
    rec.outputs["t_a"] = "out-2"
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt)
    assert [r["outcome"] for r in await rows(db, "t_a")] == ["changed", "changed"]
    assert len(await rows(db, "t_b")) == 2


@pytest.mark.asyncio
async def test_skipped_unchanged_when_inputs_and_version_match(db: Database) -> None:
    rec = Recorder(inputs={"t_a": "in-1"})
    rt = await setup(db, rec)
    v = await make_video(db, await make_library(db))
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt)
    assert rec.count("t_a") == 1
    res = await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt)
    assert rec.count("t_a") == 1  # handler not called again
    skipped = (await rows(db, "t_a"))[-1]
    assert skipped["id"] == res.job_id
    assert skipped["outcome"] == "skipped_unchanged"
    assert skipped["state"] == "done" and skipped["propagated_at"] is not None
    assert skipped["output_hash"] == (await rows(db, "t_a"))[0]["output_hash"]
    assert len(await rows(db, "t_b")) == 1  # a skip never propagates
    # no SSE for skipped jobs: one job.started / job.finished per real run only
    n_started = await db.fetchval("SELECT count(*) FROM events WHERE type = 'job.started'")
    assert n_started == 3  # t_a, t_b, t_c from the first cascade


@pytest.mark.asyncio
async def test_changed_input_reruns(db: Database) -> None:
    rec = Recorder(inputs={"t_a": "in-1"})
    rt = await setup(db, rec)
    v = await make_video(db, await make_library(db))
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt)
    rec.inputs["t_a"] = "in-2"
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt)
    assert rec.count("t_a") == 2


@pytest.mark.asyncio
async def test_force_reruns_but_propagates_only_on_change(db: Database) -> None:
    rec = Recorder(inputs={"t_a": "in-1"})
    rt = await setup(db, rec)
    v = await make_video(db, await make_library(db))
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt)
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a", force=True)
    await drain(db, rt)
    assert rec.count("t_a") == 2
    assert (await rows(db, "t_a"))[-1]["outcome"] == "unchanged"
    assert len(await rows(db, "t_b")) == 1


@pytest.mark.asyncio
async def test_force_cascade_reaches_the_leaves_even_when_unchanged(db: Database) -> None:
    rec = Recorder(inputs={"t_a": "in-1", "t_b": "in-b", "t_c": "in-c"})
    rt = await setup(db, rec)
    v = await make_video(db, await make_library(db))
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt)
    assert [rec.count(s) for s in ("t_a", "t_b", "t_c")] == [1, 1, 1]
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a", force_cascade=True)
    await drain(db, rt)
    # every stage reran although every output was identical
    assert [rec.count(s) for s in ("t_a", "t_b", "t_c")] == [2, 2, 2]
    t_b = (await rows(db, "t_b"))[-1]
    assert bool(t_b["force_cascade"]) is True  # passed down "from the start"


@pytest.mark.asyncio
async def test_version_bump_reruns_once_per_subject(db: Database) -> None:
    rec = Recorder(inputs={"t_a": "in-1"})
    rt = await setup(db, rec)
    v = await make_video(db, await make_library(db))
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt)
    graph = tuple(dataclasses.replace(s, version=2) if s.name == "t_a" else s for s in toy_graph())
    rt2 = await setup(db, rec, graph)
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt2)
    assert rec.count("t_a") == 2
    assert (await rows(db, "t_a"))[-1]["stage_version"] == 2
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt2)
    assert rec.count("t_a") == 2  # now skipped again


@pytest.mark.asyncio
async def test_trigger_during_run_creates_exactly_one_follow_up(db: Database) -> None:
    rec = Recorder()
    rt = await setup(db, rec)
    v = await make_video(db, await make_library(db))

    async def during(db_: DBConn, job: Job, ctx: StageContext) -> StageResult:
        for i in range(3):  # three triggers while running coalesce into one rerun flag
            res = await enqueue_job(
                db_, subject_kind="video", subject_id=v, stage="t_a",
                force=(i == 2), reason={"why": f"mid{i}"},
            )  # fmt: skip
            assert res.outcome == "rerun_requested"
        return StageResult(output_hash="out-1")

    rec.hooks["t_a"] = during
    first = await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    job = await claim_for_pool(db, worker_id="w", pool="cpu", stages=["t_a"], scope="per_worker")
    assert job is not None
    await dispatch_job(db, job, runtime=rt, legacy_handlers={})
    a_rows = await rows(db, "t_a")
    assert len(a_rows) == 2
    follow = a_rows[1]
    assert follow["id"] != first.job_id and follow["state"] == "pending"
    assert bool(follow["force"]) is True  # rerun_force carried over
    assert follow["triggered_by"] == first.job_id
    assert loads(follow["reasons"])[0]["why"] == "changed while running"
    assert [r["why"] for r in loads(follow["reasons"])[0]["reasons"]] == ["mid2", "mid1", "mid0"]


@pytest.mark.asyncio
async def test_undeclared_request_fails_with_graph_violation(db: Database) -> None:
    rec = Recorder()
    rt = await setup(db, rec)
    lib = await make_library(db)
    v = await make_video(db, lib)

    async def bad(db_: DBConn, job: Job, ctx: StageContext) -> StageResult:
        # t_a declares no may_request at all
        return StageResult(
            output_hash="x",
            requests=(Request("t_b", "library", lib, "nope"),),
        )

    rec.hooks["t_a"] = bad
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt, max_jobs=1)
    row = (await rows(db, "t_a"))[0]
    assert row["state"] == "failed"
    assert row["error_cause"] == "graph_violation"
    assert await rows(db, "t_b") == []  # the transaction rolled back


@pytest.mark.asyncio
async def test_declared_request_is_enqueued(db: Database) -> None:
    rec = Recorder()
    rt = await setup(db, rec)
    lib = await make_library(db)
    v = await make_video(db, lib)

    async def ask(db_: DBConn, job: Job, ctx: StageContext) -> StageResult:
        return StageResult(
            output_hash="x",
            requests=(Request("t_a", "video", v, "sample of season", payload={"role": "sample"}),),
        )

    rec.hooks["t_b"] = ask
    await enqueue_job(db, subject_kind="library", subject_id=lib, stage="t_b")
    await drain(db, rt, max_jobs=1)
    a = (await rows(db, "t_a"))[0]
    assert a["triggered_by_stage"] == "t_b"
    assert loads(a["payload"]) == {"role": "sample"}


@pytest.mark.asyncio
async def test_waiting_inputs_never_propagates(db: Database) -> None:
    rec = Recorder()
    rt = await setup(db, rec)
    v = await make_video(db, await make_library(db))

    async def wait(db_: DBConn, job: Job, ctx: StageContext) -> StageResult:
        return StageResult(output_hash="x", outcome="waiting_inputs")

    rec.hooks["t_a"] = wait
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt)
    assert (await rows(db, "t_a"))[0]["outcome"] == "waiting_inputs"
    assert await rows(db, "t_b") == []


@pytest.mark.asyncio
async def test_retryable_failure_sets_cause_backoff_and_events(db: Database) -> None:
    rec = Recorder()
    rt = await setup(db, rec)
    v = await make_video(db, await make_library(db))

    async def boom(db_: DBConn, job: Job, ctx: StageContext) -> StageResult:
        raise OSError("[WinError 121] The semaphore timeout period has expired: Y:/a/b.mp4")

    rec.hooks["t_a"] = boom
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt, max_jobs=1)
    row = (await rows(db, "t_a"))[0]
    assert row["state"] == "pending" and row["not_before"] is not None
    assert row["error_cause"] == "nas_timeout"
    kinds = [e["kind"] for e in await db.fetch("SELECT kind FROM job_events ORDER BY id")]
    assert "retry_scheduled" in kinds
    failed = await db.fetchrow("SELECT payload FROM events WHERE type = 'job.failed'")
    assert loads(failed["payload"])["will_retry"] is True


@pytest.mark.asyncio
async def test_terminal_failure_with_pending_trigger_gets_a_follow_up(db: Database) -> None:
    rec = Recorder()
    rt = await setup(db, rec)
    v = await make_video(db, await make_library(db))

    async def boom(db_: DBConn, job: Job, ctx: StageContext) -> StageResult:
        await enqueue_job(db_, subject_kind="video", subject_id=v, stage="t_a")
        from maktaba_pipeline.engine.context import StageFailure

        raise StageFailure("bad_input", "cannot read", retryable=False)

    rec.hooks["t_a"] = boom
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt, max_jobs=1)
    a = await rows(db, "t_a")
    assert [r["state"] for r in a] == ["failed", "pending"]
    assert a[0]["error_cause"] == "bad_input:cannot read"


@pytest.mark.asyncio
async def test_job_lifecycle_events_are_published(db: Database) -> None:
    rec = Recorder()
    rt = await setup(db, rec)
    v = await make_video(db, await make_library(db), title="Episode 1")
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt)
    types = [r["type"] for r in await db.fetch("SELECT type FROM events ORDER BY id")]
    assert types.count("job.started") == 3 and types.count("job.finished") == 3
    fin = await db.fetchrow("SELECT payload FROM events WHERE type = 'job.finished' ORDER BY id")
    p = json.loads(fin["payload"]) if isinstance(fin["payload"], str) else fin["payload"]
    assert p["job"]["stage"] == "t_a" and p["job"]["subject_label"] == "Episode 1"
    assert p["triggered"] == [{"stage": "t_b", "count": 1}]
    assert p["duration_ms"] >= 0


@pytest.mark.asyncio
async def test_disabled_stage_is_never_claimed(db: Database) -> None:
    rec = Recorder()
    rt = await setup(db, rec)
    v = await make_video(db, await make_library(db))
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await db.execute("UPDATE job_stages SET paused = $1 WHERE stage = 't_a'", True)
    assert await drain(db, rt) == 0
    await db.execute("UPDATE job_stages SET paused = $1 WHERE stage = 't_a'", False)
    assert await drain(db, rt) == 3


@pytest.mark.asyncio
async def test_sync_preserves_operator_columns_and_reasons(db: Database) -> None:
    rec = Recorder()
    rt = await setup(db, rec)
    await db.execute(
        "UPDATE job_stages SET enabled = $1, disabled_reason = 'operator', paused = $2,"
        " write_mode = 'review' WHERE stage = 't_a'",
        False,
        True,
    )
    await sync_graph(db, tuple(rt.graph.values()))
    r = await db.fetchrow(
        "SELECT enabled, disabled_reason, paused, write_mode FROM job_stages WHERE stage='t_a'"
    )
    assert not r["enabled"] and r["disabled_reason"] == "operator" and r["paused"]
    assert r["write_mode"] == "review"
    # a worker-set reason may be flipped back by the next sync
    await db.execute(
        "UPDATE job_stages SET enabled = $1, disabled_reason = 'not_configured'"
        " WHERE stage = 't_b'",
        False,
    )
    await sync_graph(db, tuple(rt.graph.values()))
    r = await db.fetchrow("SELECT enabled, disabled_reason FROM job_stages WHERE stage='t_b'")
    assert r["enabled"] and r["disabled_reason"] is None


@pytest.mark.asyncio
async def test_a_follow_up_keeps_what_arrived_mid_run(db: Database) -> None:
    """The follow-up takes the payload merged into the running row and a force cascade."""
    rec = Recorder()
    rt = await setup(db, rec)
    v = await make_video(db, await make_library(db))

    async def during(db_: DBConn, job: Job, ctx: StageContext) -> StageResult:
        res = await enqueue_job(
            db_, subject_kind="video", subject_id=v, stage="t_a", force_cascade=True,
            payload={"seasons": ["B"]}, reason={"why": "mid"},
        )  # fmt: skip
        assert res.outcome == "rerun_requested"
        return StageResult(output_hash="out-1")

    rec.hooks["t_a"] = during
    await enqueue_job(
        db, subject_kind="video", subject_id=v, stage="t_a", payload={"seasons": ["A"]}
    )
    job = await claim_for_pool(db, worker_id="w", pool="cpu", stages=["t_a"], scope="per_worker")
    assert job is not None
    await dispatch_job(db, job, runtime=rt, legacy_handlers={})
    follow = (await rows(db, "t_a"))[1]
    assert sorted(loads(follow["payload"])["seasons"]) == ["A", "B"]
    assert bool(follow["force_cascade"]) is True


@pytest.mark.asyncio
async def test_a_retry_carries_a_force_that_arrived_mid_run(db: Database) -> None:
    rec = Recorder()
    rt = await setup(db, rec)
    v = await make_video(db, await make_library(db))

    async def boom(db_: DBConn, job: Job, ctx: StageContext) -> StageResult:
        await enqueue_job(db_, subject_kind="video", subject_id=v, stage="t_a", force=True)
        raise OSError("[WinError 121] The semaphore timeout period has expired: Y:/a/b.mp4")

    rec.hooks["t_a"] = boom
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt, max_jobs=1)
    row = (await rows(db, "t_a"))[0]
    assert row["state"] == "pending"  # the retry
    assert bool(row["force"]) is True and bool(row["rerun_force"]) is False
