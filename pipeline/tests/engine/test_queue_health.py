"""Epic 40: failure classes, parking without spending attempts, dependency watchers and the
access check (stories 40.1, 40.2, part of 40.3)."""

from __future__ import annotations

import dataclasses
import json
import urllib.error
from datetime import timedelta
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.db.jobs import DBConn, Job
from maktaba_pipeline.engine.claim import claim_for_pool
from maktaba_pipeline.engine.context import StageContext, StageFailure
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.failure_classes import (
    QueueHealthConfig,
    class_for,
    is_parking,
    valid_class,
)
from maktaba_pipeline.engine.failure_policy import budget
from maktaba_pipeline.engine.graph import StageResult, get_stage
from maktaba_pipeline.engine.sql import dumps, is_pg, loads, now, parse_ts, to_ts
from maktaba_pipeline.engine.stages import access_check
from maktaba_pipeline.engine.stages.registry import register_impl
from maktaba_pipeline.engine.watchers import (
    model_probe,
    release,
    set_model_fetcher,
    watch_once,
)
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_video
from tests.engine.toy import Recorder, drain, rows, setup, toy_graph

# --------------------------------------------------------------------- the table


@pytest.mark.parametrize(
    ("cause", "cls"),
    [
        ("model_unavailable", "dependency:model"),
        ("file_missing", "item_permanent"),
        ("shm_full", "resource:shm"),
        ("disk_full", "resource:disk"),
        ("nas_timeout", "transient"),
        ("import:read failed: <n> frames", "transient"),
        ("ffmpeg_rc", "transient"),
        ("ffmpeg_decode", "item_permanent"),
        ("permission_denied", "item_permanent"),
        ("boom:something new", "unknown"),
    ],
)
def test_the_default_table(cause: str, cls: str) -> None:
    assert class_for(cause) == cls


def test_overrides_come_first_and_a_bad_class_is_skipped() -> None:
    rows_ = (("boom", "transient"), ("nas_timeout", "nonsense"))
    assert class_for("boom:something", rows_) == "transient"
    assert class_for("nas_timeout", rows_) == "transient"  # the bad row is skipped
    assert class_for("BOOM:x", (("boom", "dependency:vpn"),)) == "dependency:vpn"


def test_valid_classes() -> None:
    assert valid_class("transient") and valid_class("resource:disk")
    assert valid_class("dependency:model:tagcheck")
    assert not valid_class("dependency:") and not valid_class("whatever")
    assert is_parking("dependency:x") and is_parking("resource:shm")
    assert not is_parking("transient")


def test_budgets() -> None:
    cfg = QueueHealthConfig()
    assert budget("transient", 3, cfg) == 5
    assert budget("transient", 1, cfg) == 1  # a stage that runs once keeps one try
    assert budget("unknown", 3, cfg) == 3
    assert budget("item_permanent", 3, cfg) == 0


# --------------------------------------------------------------- through the engine


async def _fail_with(
    db: Database, exc: Exception, *, attempts: int = 0, max_attempts: int = 3
) -> tuple[Any, Any]:
    rec = Recorder()
    rt = await setup(db, rec)
    lib = await make_library(db)
    v = await make_video(db, lib)

    async def boom(db_: DBConn, job: Job, ctx: StageContext) -> StageResult:
        raise exc

    rec.hooks["t_a"] = boom
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    if attempts:
        await db.execute(
            "UPDATE processing_jobs SET attempts = $1, max_attempts = $2", attempts, max_attempts
        )
    await drain(db, rt, max_jobs=1)
    return (await rows(db, "t_a"))[0], v


@pytest.mark.asyncio
async def test_a_down_model_parks_the_job_without_spending_an_attempt(db: Database) -> None:
    row, _ = await _fail_with(db, StageFailure("model_unavailable", "the model did not answer"))
    assert row["state"] == "pending"
    assert row["parked_on"] == "dependency:model:model"
    assert row["failure_class"] == "dependency:model:model"
    assert row["attempts"] == 0
    nb = parse_ts(row["not_before"])
    assert nb is not None and nb > now() + timedelta(hours=23)
    ev = await db.fetchrow("SELECT kind, data FROM job_events WHERE kind = 'paused'")
    assert ev is not None and loads(ev["data"])["parked_on"] == "dependency:model:model"


@pytest.mark.asyncio
async def test_a_model_that_answers_its_probe_is_not_the_cause(db: Database) -> None:
    await db.execute(
        "INSERT INTO dependency_health (name, healthy, message, checked_at, since)"
        " VALUES ($1, $2, 'ok', $3, $3)",
        "dependency:model:model",
        True if is_pg(db) else 1,
        to_ts(db, now()),
    )
    row, _ = await _fail_with(db, StageFailure("model_unavailable", "bad json"))
    assert row["parked_on"] is None and row["failure_class"] == "unknown"
    assert row["state"] == "pending" and row["attempts"] == 1  # an ordinary retry


@pytest.mark.asyncio
async def test_a_transient_failure_gets_the_class_budget(db: Database) -> None:
    err = OSError("[WinError 121] The semaphore timeout period has expired")
    row, _ = await _fail_with(db, err, attempts=2)  # the 3rd try of a max_attempts 3 stage
    assert row["state"] == "pending" and row["failure_class"] == "transient"
    assert row["max_attempts"] == 5  # the class budget is the row's from now on


@pytest.mark.asyncio
async def test_a_spent_transient_budget_fails(db: Database) -> None:
    err = OSError("[WinError 121] The semaphore timeout period has expired")
    row, _ = await _fail_with(db, err, attempts=4, max_attempts=5)
    assert row["state"] == "failed" and row["failure_class"] == "transient"


@pytest.mark.asyncio
async def test_a_damaged_file_fails_at_once(db: Database) -> None:
    row, _ = await _fail_with(db, StageFailure("ffmpeg", "moov atom not found"))
    assert row["state"] == "failed" and row["failure_class"] == "item_permanent"
    assert row["attempts"] == 1


@pytest.mark.asyncio
async def test_an_override_changes_the_class(db: Database) -> None:
    await db.execute(
        "INSERT INTO app_settings (key, value) VALUES ($1, $2)",
        "rules.queue_health",
        dumps({"class_overrides": [["RuntimeError:boom", "item_permanent"]]}),
    )
    row, _ = await _fail_with(db, RuntimeError("boom"))
    assert row["state"] == "failed" and row["failure_class"] == "item_permanent"


@pytest.mark.asyncio
async def test_a_missing_file_on_a_share_that_does_not_answer_waits_for_it(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    from maktaba_pipeline.engine import failure_policy

    # the share does not answer (the factory's root "/lib" is a real folder on Linux)
    async def probe(root: str, timeout_s: float) -> bool:
        return False

    monkeypatch.setattr(failure_policy, "root_answers", probe)
    row, _ = await _fail_with(
        db, StageFailure("file_missing", "/lib/a.mp4: not found", retryable=False)
    )
    assert row["state"] == "pending" and row["parked_on"] == "dependency:share:/lib"


@pytest.mark.asyncio
async def test_a_worker_without_the_share_never_probes_it(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The index worker (``--no-share``): a missing file is the item's, and no library
    root is looked at (a stalled share would hold the probe's thread)."""
    from maktaba_pipeline.engine import failure_policy

    async def probe(root: str, timeout_s: float) -> bool:
        raise AssertionError("a worker without the share must not probe it")

    monkeypatch.setattr(failure_policy, "root_answers", probe)
    monkeypatch.setattr(failure_policy, "_share_probes", True)
    failure_policy.set_share_probes(False)
    row, _ = await _fail_with(
        db, StageFailure("file_missing", "/lib/a.mp4: not found", retryable=False)
    )
    assert row["state"] == "failed" and row["failure_class"] == "item_permanent"


@pytest.mark.asyncio
async def test_a_missing_file_on_a_share_that_answers_needs_you(
    db: Database, tmp_path: Path
) -> None:
    (tmp_path / "x").write_text("x")
    rec = Recorder()
    rt = await setup(db, rec)
    lib = await make_library(db)
    root = tmp_path.as_posix()
    await db.execute(
        "UPDATE libraries SET roots = $1 WHERE id = $2",
        [root] if is_pg(db) else json.dumps([root]),
        str(lib),
    )
    v = await make_video(db, lib, path=f"{root}/gone.mp4")

    async def boom(db_: DBConn, job: Job, ctx: StageContext) -> StageResult:
        raise StageFailure("file_missing", "not found", retryable=False)

    rec.hooks["t_a"] = boom
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt, max_jobs=1)
    row = (await rows(db, "t_a"))[0]
    assert row["state"] == "failed" and row["failure_class"] == "item_permanent"


@pytest.mark.asyncio
async def test_shared_memory_parks_and_marks_the_resource(db: Database) -> None:
    msg = "DiskFullError: could not resize shared memory segment to 1048576 bytes"
    row, _ = await _fail_with(db, RuntimeError(msg))
    assert row["parked_on"] == "resource:shm" and row["attempts"] == 0
    dep = await db.fetchrow("SELECT healthy FROM dependency_health WHERE name = 'resource:shm'")
    assert dep is not None and not bool(dep["healthy"])


# ------------------------------------------------------------------- the watchers


async def _parked_model_job(db: Database) -> Any:
    row, _ = await _fail_with(db, StageFailure("model_unavailable", "down"))
    assert row["parked_on"] == "dependency:model:model"
    return row


@pytest.mark.asyncio
async def test_a_healthy_dependency_releases_its_parked_jobs(db: Database) -> None:
    await _parked_model_job(db)

    async def probes(*_: Any) -> dict[str, tuple[bool, str]]:
        return {"dependency:model:model": (True, "answers")}

    report = await watch_once(db, {}, probes=probes)
    assert report.leader and report.released == {"dependency:model:model": 1}
    row = (await rows(db, "t_a"))[0]
    nb = parse_ts(row["not_before"])
    assert row["parked_on"] is None and nb is not None and nb <= now()
    claimed = await claim_for_pool(db, worker_id="w", pool="cpu", stages=["t_a"])
    assert claimed is not None and claimed.attempts == 1  # the refund held


@pytest.mark.asyncio
async def test_a_down_dependency_releases_nothing(db: Database) -> None:
    await _parked_model_job(db)

    async def probes(*_: Any) -> dict[str, tuple[bool, str]]:
        return {"dependency:model:model": (False, "not answering")}

    report = await watch_once(db, {}, probes=probes)
    assert report.released == {}
    assert (await rows(db, "t_a"))[0]["parked_on"] == "dependency:model:model"
    dep = await db.fetchrow("SELECT healthy, message FROM dependency_health")
    assert not bool(dep["healthy"]) and dep["message"] == "not answering"


@pytest.mark.asyncio
async def test_the_claim_clears_a_parked_job_taken_by_the_backstop(db: Database) -> None:
    await _parked_model_job(db)
    await db.execute("UPDATE processing_jobs SET not_before = $1", to_ts(db, now()))
    claimed = await claim_for_pool(db, worker_id="w", pool="cpu", stages=["t_a"])
    assert claimed is not None
    assert (await rows(db, "t_a"))[0]["parked_on"] is None


@pytest.mark.asyncio
async def test_release_is_by_name(db: Database) -> None:
    await _parked_model_job(db)
    assert await release(db, "dependency:model:other") == 0
    assert await release(db, "dependency:model:model") == 1


@pytest.mark.asyncio
async def test_the_shared_memory_cooldown(db: Database) -> None:
    from maktaba_pipeline.engine.watchers import probe_all

    old = now() - timedelta(minutes=30)
    await db.execute(
        "INSERT INTO dependency_health (name, healthy, message, checked_at, since)"
        " VALUES ('resource:shm', $1, 'full', $2, $2)",
        False if is_pg(db) else 0,
        to_ts(db, old),
    )
    got = await probe_all(db, {}, QueueHealthConfig(), now())
    assert got["resource:shm"][0] is True
    got = await probe_all(db, {}, QueueHealthConfig(), old + timedelta(minutes=5))
    assert "resource:shm" not in got  # still cooling down


@pytest.mark.asyncio
async def test_the_model_probe() -> None:
    def tags(models: list[str]) -> Any:
        return lambda url, timeout: json.dumps({"models": [{"name": m} for m in models]}).encode()

    def down(url: str, timeout: float) -> bytes:
        raise urllib.error.URLError("connection refused")

    try:
        set_model_fetcher(tags(["qwen2.5:7b", "llama3:8b"]))
        assert (await model_probe("http://m:11434", "qwen2.5:7b", 1.0))[0] is True
        set_model_fetcher(tags(["llama3:8b"]))
        ok, why = await model_probe("http://m:11434", "qwen2.5:7b", 1.0)
        assert not ok and "qwen2.5:7b" in why
        set_model_fetcher(down)
        ok, why = await model_probe("http://m:11434", "qwen2.5:7b", 1.0)
        assert not ok and "not answering" in why
    finally:
        set_model_fetcher(None)


# ------------------------------------------------------------------ access check


@pytest.mark.asyncio
async def test_a_readable_file_requeues_the_videos_failures(db: Database, tmp_path: Path) -> None:
    stage = get_stage("access_check")
    assert stage is not None
    graph = (*toy_graph(), dataclasses.replace(stage, pool="cpu"))
    rec = Recorder()
    rt = await setup(db, rec, graph)
    register_impl("access_check", access_check.IMPL)
    f = tmp_path / "a.mp4"
    f.write_bytes(b"x" * 100)
    v = await make_video(db, await make_library(db), path=f.as_posix())

    async def boom(db_: DBConn, job: Job, ctx: StageContext) -> StageResult:
        raise StageFailure("ffmpeg", "moov atom not found")

    rec.hooks["t_a"] = boom
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt, max_jobs=1)
    assert (await rows(db, "t_a"))[0]["state"] == "failed"
    rec.hooks.pop("t_a")
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="access_check")
    await drain(db, rt, max_jobs=1)
    chk = await db.fetchrow("SELECT ok, error FROM item_checks")
    assert bool(chk["ok"]) and chk["error"] is None
    assert [r["state"] for r in await rows(db, "t_a")] == ["failed", "pending"]


@pytest.mark.asyncio
async def test_an_unreadable_file_is_recorded(db: Database, tmp_path: Path) -> None:
    stage = get_stage("access_check")
    assert stage is not None
    rt = await setup(db, Recorder(), (*toy_graph(), dataclasses.replace(stage, pool="cpu")))
    register_impl("access_check", access_check.IMPL)
    v = await make_video(db, await make_library(db), path=(tmp_path / "nope.mp4").as_posix())
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="access_check")
    await drain(db, rt, max_jobs=1)
    chk = await db.fetchrow("SELECT ok, error FROM item_checks")
    assert not bool(chk["ok"]) and "FileNotFoundError" in chk["error"]
    assert (await rows(db, "access_check"))[0]["state"] == "done"
