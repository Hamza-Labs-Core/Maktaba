"""Epic 40 (40.3): failures whose cause has passed are queued again without a scan."""

from __future__ import annotations

import asyncio
import json
import threading
from collections.abc import Callable
from datetime import timedelta
from pathlib import Path
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine import healer
from maktaba_pipeline.engine.dispatch import EngineRuntime
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import Edge, Request, StageDef, StageResult
from maktaba_pipeline.engine.healer import BATCH, HEAL_WHY, heal_due, heal_once
from maktaba_pipeline.engine.paths import PathMap, set_active_path_map
from maktaba_pipeline.engine.requeue import HEAL_PRIORITY, requeue_failed, requeue_stage
from maktaba_pipeline.engine.sql import is_pg, loads, now, parse_ts, to_ts
from maktaba_pipeline.engine.watchers import watch_once
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_video
from tests.engine.toy import Recorder, drain, rows, toy_graph
from tests.engine.toy import setup as toy_setup

UP: dict[str, tuple[bool, str]] = {}


@pytest.fixture(autouse=True)
def _reset_look_cursor() -> None:
    healer._look_after.clear()  # noqa: SLF001


async def setup(
    db: Database, rec: Recorder, graph: tuple[StageDef, ...] | None = None
) -> EngineRuntime:
    """The toy graph and a second video stage, ``t_d``."""
    more = StageDef("t_d", "D", ("video",), "cpu", "episodes", 2, 1)
    return await toy_setup(db, rec, graph or (*toy_graph(), more))


async def _library(db: Database, root: Path) -> UUID:
    lib = await make_library(db)
    await db.execute(
        "UPDATE libraries SET roots = $1 WHERE id = $2",
        [root.as_posix()] if is_pg(db) else json.dumps([root.as_posix()]),
        str(lib),
    )
    return lib


async def _fail(
    db: Database,
    video: UUID,
    cause: str = "file_missing",
    cls: str | None = "item_permanent",
    *,
    ago: timedelta = timedelta(days=15),
    stage: str = "t_a",
    max_attempts: int = 3,
) -> int:
    """``stage``'s latest job of the video failed for good, ``ago``."""
    res = await enqueue_job(db, subject_kind="video", subject_id=video, stage=stage)
    assert res.job_id is not None
    await _end(db, res.job_id, cause, cls, ago, max_attempts)
    return res.job_id


async def _end(
    db: Database,
    job: int,
    cause: str = "file_missing",
    cls: str | None = "item_permanent",
    ago: timedelta = timedelta(days=15),
    max_attempts: int = 3,
) -> None:
    await db.execute(
        "UPDATE processing_jobs SET state = 'failed', attempts = 1, error_cause = $2,"
        " failure_class = $3, finished_at = $4, not_before = NULL, max_attempts = $5 WHERE id = $1",
        job,
        cause,
        cls,
        to_ts(db, now() - ago),
        max_attempts,
    )


async def _pending(db: Database, stage: str = "t_a") -> list[Any]:
    return [r for r in await rows(db, stage) if r["state"] == "pending"]


async def _next_look(db: Database, job: int) -> timedelta | None:
    """How long until the failed row is looked at again."""
    nb = parse_ts(await db.fetchval("SELECT not_before FROM processing_jobs WHERE id = $1", job))
    return None if nb is None else nb - now()


def _none(asked: list[str]) -> Callable[[str], bool]:
    """A look that finds no file and notes what it was asked."""

    def exists(path: str) -> bool:
        asked.append(path)
        return False

    return exists


def _about(got: timedelta | None, want: timedelta) -> bool:
    return got is not None and abs(got - want) < timedelta(minutes=5)


@pytest.mark.asyncio
async def test_a_file_that_is_there_is_queued_again_behind_all_other_work(
    db: Database, tmp_path: Path
) -> None:
    await setup(db, Recorder())
    v = await make_video(db, await _library(db, tmp_path), path=(tmp_path / "a.mp4").as_posix())
    (tmp_path / "a.mp4").write_bytes(b"x")
    await _fail(db, v)
    await db.execute(
        "UPDATE videos SET missing_confirmed_at = $2 WHERE id = $1", str(v), to_ts(db, now())
    )
    assert await heal_once(db, UP) == 1
    (job,) = await _pending(db)
    assert job["priority"] == HEAL_PRIORITY
    assert loads(job["reasons"])[0]["why"] == HEAL_WHY
    assert (
        await db.fetchval("SELECT missing_confirmed_at FROM videos WHERE id = $1", str(v)) is None
    )
    assert await heal_once(db, UP) == 0  # its job is live


@pytest.mark.asyncio
async def test_the_stored_path_is_looked_for_where_this_machine_mounts_it(
    db: Database, tmp_path: Path
) -> None:
    await setup(db, Recorder())
    v = await make_video(db, await make_library(db), path="/Volumes/Originals/a.mp4")
    (tmp_path / "a.mp4").write_bytes(b"x")
    await _fail(db, v)
    set_active_path_map(PathMap((("/Volumes/Originals/", tmp_path.as_posix() + "/"),)))
    try:
        assert await heal_once(db, UP) == 1
    finally:
        set_active_path_map(PathMap())


@pytest.mark.asyncio
async def test_a_file_still_gone_is_looked_for_less_and_less_often(
    db: Database, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    await setup(db, Recorder())
    v = await make_video(db, await _library(db, tmp_path), path=(tmp_path / "gone.mp4").as_posix())
    job = await _fail(db, v, ago=timedelta(hours=5))
    assert await heal_once(db, UP) == 0
    # as long again as it has been failed
    assert _about(await _next_look(db, job), timedelta(hours=5))
    asked: list[str] = []
    monkeypatch.setattr(healer, "_exists", _none(asked))
    assert await heal_once(db, UP) == 0
    assert asked == []  # not due: not even a stat
    assert await heal_once(db, UP, now() + timedelta(hours=6)) == 0
    assert len(asked) == 1
    await _end(db, job, ago=timedelta(days=90))
    assert await heal_once(db, UP) == 0
    assert _about(await _next_look(db, job), healer.GONE_MAX)
    # nothing is queued, nothing confirmed: that is the scan's
    assert [r["state"] for r in await rows(db, "t_a")] == ["failed"]
    assert (
        await db.fetchval("SELECT missing_confirmed_at FROM videos WHERE id = $1", str(v)) is None
    )


@pytest.mark.asyncio
async def test_a_root_that_does_not_answer_says_nothing(
    db: Database, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    await setup(db, Recorder())
    v = await make_video(db, await _library(db, tmp_path), path=(tmp_path / "a.mp4").as_posix())
    (tmp_path / "a.mp4").write_bytes(b"x")
    job = await _fail(db, v)
    asked: list[str] = []
    monkeypatch.setattr(healer, "_exists", _none(asked))
    root = f"dependency:share:{tmp_path.as_posix()}"
    assert await heal_once(db, {root: (False, "offline")}) == 0
    assert asked == []
    assert await _next_look(db, job) is None  # not touched: due as soon as the root answers
    monkeypatch.undo()
    assert await heal_once(db, {root: (True, "answers")}) == 1


@pytest.mark.asyncio
async def test_a_down_root_takes_no_place_of_the_others(
    db: Database, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(healer, "BATCH", 3)
    await setup(db, Recorder())
    away = tmp_path / "Away"
    lost = await _library(db, away)
    for i in range(10):
        await _fail(db, await make_video(db, lost, path=f"{away.as_posix()}/{i}.mp4"))
    await _fail(
        db,
        await make_video(db, lost, path=f"{away.as_posix()}/db.mp4"),
        "plugin_database:driver: bad connection",
        "unknown",
        stage="t_d",
    )
    here = tmp_path / "Away2"  # the down root's name is not a prefix of it
    here.mkdir()
    (here / "a.mp4").write_bytes(b"x")
    v = await make_video(db, await _library(db, here), path=(here / "a.mp4").as_posix())
    await _fail(db, v)
    down = {f"dependency:share:{away.as_posix()}": (False, "offline")}
    # the one that is there, and the one whose cause says nothing of the file
    assert await heal_once(db, down) == 2
    assert [str(r["subject_id"]) for r in await _pending(db)] == [str(v)]
    assert len(await _pending(db, "t_d")) == 1


@pytest.mark.asyncio
async def test_a_file_no_root_holds_is_not_said_to_be_gone(db: Database, tmp_path: Path) -> None:
    """Roots and paths stored in two forms (smb:// and /Volumes): an unmounted share cannot
    be told from a missing file."""
    await setup(db, Recorder())
    lib = await _library(db, tmp_path)
    await db.execute(
        "UPDATE libraries SET roots = $1 WHERE id = $2",
        ["smb://nas/Media"] if is_pg(db) else json.dumps(["smb://nas/Media"]),
        str(lib),
    )
    f = tmp_path / "a.mp4"
    job = await _fail(db, await make_video(db, lib, path=f.as_posix()), ago=timedelta(days=60))
    assert await heal_once(db, UP) == 0
    assert _about(await _next_look(db, job), healer.UNSAID_MAX)
    f.write_bytes(b"x")
    assert await heal_once(db, UP, now() + timedelta(days=2)) == 1


@pytest.mark.asyncio
@pytest.mark.parametrize("cascade", [True, False])
async def test_the_job_is_asked_what_the_failed_one_was(
    db: Database, tmp_path: Path, cascade: bool
) -> None:
    await setup(db, Recorder())
    lib = await _library(db, tmp_path)
    (tmp_path / "a.mp4").write_bytes(b"x")

    async def ask(stage: str) -> UUID:
        v = await make_video(db, lib, path=(tmp_path / f"{stage}.mp4").as_posix())
        (tmp_path / f"{stage}.mp4").write_bytes(b"x")
        res = await enqueue_job(
            db,
            subject_kind="video",
            subject_id=v,
            stage=stage,
            force_cascade=cascade,
            payload={"seasons": ["s1"], "run_now": True},
        )
        assert res.job_id is not None
        await _end(db, res.job_id)
        return v

    await ask("t_a")
    assert await heal_once(db, UP) == 1
    (job,) = await _pending(db)
    # run_now passes the quiet hours: an admin said it of that job
    assert loads(job["payload"]) == {"seasons": ["s1"]}
    assert bool(job["force_cascade"]) is cascade
    # a scan's and an access check's requeue too
    assert await requeue_failed(db, await ask("t_d"), "why") == ["t_d"]
    (job,) = await _pending(db, "t_d")
    assert loads(job["payload"]) == {"seasons": ["s1"]}
    assert bool(job["force_cascade"]) is cascade


@pytest.mark.asyncio
async def test_one_row_that_cannot_be_queued_does_not_stop_the_others(
    db: Database, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    await setup(db, Recorder())
    lib = await _library(db, tmp_path)
    jobs: dict[str, int] = {}
    for n in ("a", "b", "c"):
        (tmp_path / f"{n}.mp4").write_bytes(b"x")
        v = await make_video(db, lib, path=(tmp_path / f"{n}.mp4").as_posix())
        jobs[str(v)] = await _fail(db, v)
    bad = list(jobs)[1]
    real = requeue_stage

    async def refuses(db_: Any, vid: UUID, *a: Any, **kw: Any) -> Any:
        if str(vid) == bad:
            raise RuntimeError("no")
        return await real(db_, vid, *a, **kw)

    monkeypatch.setattr(healer, "requeue_stage", refuses)
    assert await heal_once(db, UP) == 2
    assert _about(await _next_look(db, jobs[bad]), healer.UNSAID_MAX)


@pytest.mark.asyncio
async def test_a_retired_stage_stays_as_it_is(
    db: Database, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    await setup(db, Recorder())
    v = await make_video(db, await _library(db, tmp_path), path=(tmp_path / "a.mp4").as_posix())
    (tmp_path / "a.mp4").write_bytes(b"x")
    await _fail(db, v)
    with monkeypatch.context() as m:
        m.setattr(healer, "RETIRED_STAGES", frozenset({"t_a"}))
        assert await heal_once(db, UP) == 0
    assert await heal_once(db, UP) == 1


@pytest.mark.asyncio
async def test_one_slow_folder_puts_off_no_other(
    db: Database, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    await setup(db, Recorder())
    lib = await _library(db, tmp_path)
    job: dict[str, int] = {}
    for n in ("slow/1", "slow/2", "well/1"):
        path = f"{tmp_path.as_posix()}/{n}.mp4"
        job[n] = await _fail(db, await make_video(db, lib, path=path))
    lost = await make_video(db, lib)
    cause = "plugin_database:driver: bad connection"
    answer = threading.Event()

    def look(path: str) -> bool:
        if "/slow/" in path:
            answer.wait(10)
        return True

    monkeypatch.setattr(healer, "_exists", look)
    monkeypatch.setattr(healer, "LOOK_S", 0.05)
    assert await heal_once(db, UP) == 0
    for n in ("slow/1", "slow/2"):
        assert _about(await _next_look(db, job[n]), healer.AGAIN)
    assert await _next_look(db, job["well/1"]) is None
    # while the look hangs the database's causes go on, the files wait
    await _fail(db, lost, cause, "unknown")
    assert await heal_once(db, UP) == 1
    assert [str(r["subject_id"]) for r in await _pending(db)] == [str(lost)]
    answer.set()
    for _ in range(100):
        if not healer._looking.is_set():  # noqa: SLF001
            break
        await asyncio.sleep(0.02)
    assert await heal_once(db, UP) == 1


@pytest.mark.asyncio
async def test_sqlite_reads_the_failed_jobs_only(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    if is_pg(db):
        pytest.skip("SQLite's planner")
    asked: list[tuple[str, tuple[Any, ...]]] = []
    real = db.fetch

    async def fetch(sql: str, *args: Any) -> Any:
        asked.append((sql, args))
        return await real(sql, *args)

    monkeypatch.setattr(db, "fetch", fetch)
    await heal_once(db, UP)
    sql, args = next(a for a in asked if "by_database" in a[0])
    plan = [str(r["detail"]) for r in await real("EXPLAIN QUERY PLAN " + sql, *args)]
    (first,) = [d for d in plan if d.startswith(("SEARCH j ", "SCAN j"))]
    assert "(state=?)" in first, plan


@pytest.mark.asyncio
async def test_an_item_that_keeps_failing_stops_coming_back(db: Database, tmp_path: Path) -> None:
    await setup(db, Recorder())
    v = await make_video(db, await _library(db, tmp_path), path=(tmp_path / "a.mp4").as_posix())
    (tmp_path / "a.mp4").write_bytes(b"x")
    job = await _fail(db, v, ago=timedelta(minutes=10))
    t = now()
    healed_after: list[timedelta] = []
    for _ in healer.WAITS:
        assert await heal_once(db, UP, t) == 0  # too soon: put off to when it is due
        wait = await _next_look(db, job)
        assert wait is not None
        healed_after.append(wait)
        assert await heal_once(db, UP, t + wait + timedelta(minutes=10)) == 1
        (again,) = await _pending(db)
        job = int(again["id"])
        await _end(db, job, ago=timedelta(minutes=10))  # and it fails again
    assert all(
        _about(got, want - timedelta(minutes=10))
        for got, want in zip(healed_after, healer.WAITS, strict=True)
    )
    for days in (0, 8, 100):
        assert await heal_once(db, UP, t + timedelta(days=days)) == 0
    assert await _pending(db) == []
    # a success starts the count again
    res = await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a", force=True)
    await db.execute(
        "UPDATE processing_jobs SET state = 'done', outcome = 'changed' WHERE id = $1", res.job_id
    )
    await _fail(db, v)
    assert await heal_once(db, UP) == 1


@pytest.mark.asyncio
@pytest.mark.parametrize(
    ("cause", "cls", "heals"),
    [
        ("nas_partial_read", "transient", True),
        ("nas_timeout", "transient", True),
        ("nas_invalid_argument", "transient", True),
        ("nas_timeout", "item_permanent", False),  # an admin's class row says so
        ("plugin_database:driver: bad connection", "unknown", True),
        (
            "plugin_database:dial tcp <n>.<n>:<n>: connect: can't assign requested address",
            "unknown",
            True,
        ),
        ("plugin_tx:no open transaction <hex>", "unknown", True),
        ("DatabaseUnavailable:the database did not answer: eof", "unknown", True),
        (
            "plugin_database:<n>: insert or update on table <path> violates foreign key"
            " constraint <path>",
            "unknown",
            True,
        ),
        # slot 0237's failures: the transcription's own sweep brings them back
        ("plugin_database:<n>p<n>: cannot update table <path>", "unknown", False),
        ("plugin_database:<n>: duplicate key value", "unknown", False),
        ("ffmpeg_decode", "item_permanent", False),
        ("ffmpeg_rc", "transient", False),
        ("permission_denied", "item_permanent", False),
        ("db_error", "transient", False),
        ("KeyError:<path>", "unknown", False),
    ],
)
async def test_only_causes_that_pass_are_healed(
    db: Database, tmp_path: Path, cause: str, cls: str, heals: bool
) -> None:
    await setup(db, Recorder())
    v = await make_video(db, await _library(db, tmp_path), path=(tmp_path / "a.mp4").as_posix())
    (tmp_path / "a.mp4").write_bytes(b"x")
    await _fail(db, v, cause, cls)
    assert await heal_once(db, UP) == int(heals)


@pytest.mark.asyncio
async def test_what_is_not_open_or_not_wanted_is_left_alone(db: Database, tmp_path: Path) -> None:
    await setup(db, Recorder())
    lib = await _library(db, tmp_path)
    f = tmp_path / "a.mp4"
    f.write_bytes(b"x")

    async def failed(**kw: Any) -> UUID:
        v = await make_video(db, lib, path=f"{tmp_path.as_posix()}/{len(made)}.mp4")
        (tmp_path / f"{len(made)}.mp4").write_bytes(b"x")
        made.append(v)
        await _fail(db, v, **kw)
        return v

    made: list[UUID] = []
    await failed(max_attempts=1)  # runs once by design
    ignored = await failed()
    await db.execute(
        "INSERT INTO failure_ignores (subject_kind, subject_id, stage) VALUES ('video', $1, 't_a')",
        str(ignored),
    )
    deleted = await failed()
    await db.execute(
        "UPDATE videos SET deleted_at = $2 WHERE id = $1", str(deleted), to_ts(db, now())
    )
    superseded = await failed()
    await db.execute("UPDATE videos SET state = 'superseded' WHERE id = $1", str(superseded))
    cancelled = await failed()  # a later job was cancelled: it is not the latest
    res = await enqueue_job(db, subject_kind="video", subject_id=cancelled, stage="t_a")
    await db.execute("UPDATE processing_jobs SET state = 'cancelled' WHERE id = $1", res.job_id)
    assert await heal_once(db, UP) == 0
    await db.execute("UPDATE job_stages SET external_write = $1", True if is_pg(db) else 1)
    off = await failed()
    assert await heal_once(db, UP) == 0  # it changes files
    await db.execute("UPDATE job_stages SET external_write = $1", False if is_pg(db) else 0)
    await db.execute(
        "UPDATE job_stages SET enabled = $1 WHERE stage = 't_a'", False if is_pg(db) else 0
    )
    assert await heal_once(db, UP) == 0
    await db.execute(
        "UPDATE job_stages SET enabled = $1 WHERE stage = 't_a'", True if is_pg(db) else 1
    )
    assert await heal_once(db, UP) == 1
    assert [str(r["subject_id"]) for r in await _pending(db)] == [str(off)]


@pytest.mark.asyncio
async def test_a_large_backlog_is_fed_in_as_it_drains(
    db: Database, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(healer, "BATCH", 3)
    await setup(db, Recorder())
    lib = await _library(db, tmp_path)
    for i in range(8):
        f = tmp_path / f"{i}.mp4"
        f.write_bytes(b"x")
        await _fail(db, await make_video(db, lib, path=f.as_posix()))
    assert await heal_once(db, UP) == 3
    assert await heal_once(db, UP) == 0  # three wait: no more until they ran
    await db.execute("UPDATE processing_jobs SET state = 'done' WHERE state = 'pending'")
    assert await heal_once(db, UP) == 3
    assert BATCH == 250


@pytest.mark.asyncio
async def test_what_follows_a_healed_job_stays_behind_too(db: Database, tmp_path: Path) -> None:
    graph = (
        StageDef(
            "t_a", "A", ("video",), "cpu", "episodes", 1, 1,
            edges=(Edge("t_d", "same"),), may_request=("t_e",),
        ),
        StageDef("t_d", "D", ("video",), "cpu", "episodes", 2, 1),
        StageDef("t_e", "E", ("video",), "cpu", "episodes", 3, 1),
        *toy_graph()[1:],
    )  # fmt: skip
    rec = Recorder()

    async def asks(_db: Any, job: Job, _ctx: Any) -> StageResult:
        return StageResult(
            output_hash="out-1", requests=(Request("t_e", "video", job.subject.id, "asked"),)
        )

    rec.hooks["t_a"] = asks
    rt = await setup(db, rec, graph)
    v = await make_video(db, await _library(db, tmp_path), path=(tmp_path / "a.mp4").as_posix())
    (tmp_path / "a.mp4").write_bytes(b"x")
    await _fail(db, v)
    assert await heal_once(db, UP) == 1
    await drain(db, rt)
    assert [r["state"] for r in await rows(db, "t_a")] == ["failed", "done"]
    assert [(r["state"], r["priority"]) for r in await rows(db, "t_d")] == [("done", HEAL_PRIORITY)]
    # after any other job it is the default
    await enqueue_job(
        db, subject_kind="video", subject_id=v, stage="t_a", force=True, force_cascade=True
    )
    await drain(db, rt)
    for stage in ("t_d", "t_e"):
        assert [r["priority"] for r in await rows(db, stage)] == [HEAL_PRIORITY, 100]


@pytest.mark.asyncio
async def test_a_hanging_share_holds_neither_the_round_nor_more_than_one_thread(
    db: Database, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    await setup(db, Recorder())
    lib = await _library(db, tmp_path)
    jobs = [
        await _fail(db, await make_video(db, lib, path=f"{tmp_path.as_posix()}/{i}.mp4"))
        for i in range(2)
    ]
    answer = threading.Event()
    asked: list[str] = []

    def hang(path: str) -> bool:
        asked.append(path)
        answer.wait(10)
        return True

    monkeypatch.setattr(healer, "_exists", hang)
    monkeypatch.setattr(healer, "LOOK_S", 0.05)
    assert await heal_once(db, UP) == 0
    for job in jobs:  # nothing is said of them
        assert _about(await _next_look(db, job), healer.AGAIN)
    assert await heal_once(db, UP, now() + timedelta(hours=2)) == 0
    assert len(asked) == 1  # the second round did not look while the first look hangs
    answer.set()
    for _ in range(100):
        if not healer._looking.is_set():  # noqa: SLF001
            break
        await asyncio.sleep(0.02)
    assert len(asked) == 1  # told to stop: the rest is not asked about
    assert await heal_once(db, UP, now() + timedelta(hours=2)) == 2


@pytest.mark.asyncio
async def test_the_leader_round_heals_now_and_then(
    db: Database, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(healer, "_last", [])
    await setup(db, Recorder())
    lib = await _library(db, tmp_path)
    (tmp_path / "a.mp4").write_bytes(b"x")
    await _fail(db, await make_video(db, lib, path=(tmp_path / "a.mp4").as_posix()))

    async def probes(*_: Any) -> dict[str, tuple[bool, str]]:
        return {f"dependency:share:{tmp_path.as_posix()}": (True, "answers")}

    assert (await watch_once(db, {}, probes=probes)).healed == 1
    (tmp_path / "b.mp4").write_bytes(b"x")
    await _fail(db, await make_video(db, lib, path=(tmp_path / "b.mp4").as_posix()))
    assert (await watch_once(db, {}, probes=probes)).healed == 0  # not every round

    async def broken(*_: Any, **__: Any) -> int:
        raise RuntimeError("the database went away")

    monkeypatch.setattr(healer, "_last", [])
    monkeypatch.setattr(healer, "heal_once", broken)
    assert await heal_due(db, UP, now()) == 0  # the health round goes on


@pytest.mark.asyncio
async def test_a_look_no_thread_was_free_for_does_not_stop_the_next(
    db: Database, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    await setup(db, Recorder())
    lib = await _library(db, tmp_path)
    (tmp_path / "a.mp4").write_bytes(b"x")
    await _fail(db, await make_video(db, lib, path=(tmp_path / "a.mp4").as_posix()))

    async def none_free(_fn: Callable[[], None]) -> None:
        await asyncio.sleep(10)

    with monkeypatch.context() as m:
        m.setattr(healer, "LOOK_S", 0.05)
        m.setattr(asyncio, "to_thread", none_free)
        assert await heal_once(db, UP) == 0
    assert await heal_once(db, UP, now() + timedelta(hours=2)) == 1


@pytest.mark.asyncio
async def test_a_hung_listing_does_not_mark_a_non_ascii_path_as_gone(
    db: Database, tmp_path: Path
) -> None:
    import unicodedata

    from maktaba_pipeline.engine import paths

    await setup(db, Recorder())
    lib = await _library(db, tmp_path)
    folder = tmp_path / "share"
    folder.mkdir()
    name_nfd = unicodedata.normalize("NFD", "أبو.mp4")
    name_nfc = unicodedata.normalize("NFC", "أبو.mp4")
    # store a mixed path so NFC/NFD full-path stat both miss and _walk consults _listing
    sub_nfc = unicodedata.normalize("NFC", "قناة أ")
    sub_nfd = unicodedata.normalize("NFD", "قناة أ")
    (folder / sub_nfc).mkdir()
    stored = f"{folder.as_posix()}/{sub_nfd}/{name_nfd}"
    job = await _fail(db, await make_video(db, lib, path=stored), ago=timedelta(days=30))

    paths.forget_listings()
    with paths._listings_lock:  # noqa: SLF001
        paths._listing_now.add(folder.as_posix())  # noqa: SLF001
        paths._listing_now.add(f"{folder.as_posix()}/{sub_nfc}")  # noqa: SLF001
        paths._listing_now.add(f"{folder.as_posix()}/{sub_nfd}")  # noqa: SLF001
    try:
        assert await heal_once(db, UP) == 0
        # hung listing is inconclusive: put off an hour, never up to 7 days
        assert _about(await _next_look(db, job), healer.AGAIN)
    finally:
        with paths._listings_lock:  # noqa: SLF001
            paths._listing_now.discard(folder.as_posix())  # noqa: SLF001
            paths._listing_now.discard(f"{folder.as_posix()}/{sub_nfc}")  # noqa: SLF001
            paths._listing_now.discard(f"{folder.as_posix()}/{sub_nfd}")  # noqa: SLF001
        paths.forget_listings()

    (folder / sub_nfc / name_nfc).write_bytes(b"x")
    assert await heal_once(db, UP, now() + timedelta(hours=2)) == 1


@pytest.mark.asyncio
async def test_seven_or_more_slow_folders_do_not_starve_other_shares(
    db: Database, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(healer, "_look_after", [])
    await setup(db, Recorder())
    lib = await _library(db, tmp_path)
    for i in range(8):
        path = f"{tmp_path.as_posix()}/s{i}/1.mp4"
        await _fail(db, await make_video(db, lib, path=path))
    good_path = f"{tmp_path.as_posix()}/z_good/1.mp4"
    good = await make_video(db, lib, path=good_path)
    await _fail(db, good)

    release = threading.Event()

    def look(path: str) -> bool:
        if "/z_good/" in path:
            return True
        release.wait(10)
        return False

    monkeypatch.setattr(healer, "_exists", look)
    monkeypatch.setattr(healer, "LOOK_S", 0.03)
    t = now()
    healed_good = False
    for step in range(10):
        release.clear()
        n = await heal_once(db, UP, t + timedelta(minutes=10 * step))
        release.set()
        for _ in range(100):
            if not healer._looking.is_set():  # noqa: SLF001
                break
            await asyncio.sleep(0.01)
        if n > 0:
            healed_good = True
            break
    assert healed_good
    assert [str(r["subject_id"]) for r in await _pending(db)] == [str(good)]


@pytest.mark.asyncio
async def test_a_down_root_with_non_ascii_capitals_is_excluded_in_sql(
    db: Database, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(healer, "BATCH", 2)
    await setup(db, Recorder())
    away = tmp_path / "Été"
    lost = await _library(db, away)
    for i in range(5):
        await _fail(db, await make_video(db, lost, path=f"{away.as_posix()}/{i}.mp4"))
    here = tmp_path / "Other"
    here.mkdir()
    (here / "a.mp4").write_bytes(b"x")
    v = await make_video(db, await _library(db, here), path=(here / "a.mp4").as_posix())
    await _fail(db, v)
    down = {f"dependency:share:{away.as_posix()}": (False, "offline")}
    assert await heal_once(db, down) == 1
    assert [str(r["subject_id"]) for r in await _pending(db)] == [str(v)]


@pytest.mark.asyncio
async def test_python_down_root_guard_catches_case_mismatch_sql_cannot_fold(
    db: Database, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Guard test (:180): root stored lowercase 'été' while video path has capital 'Été'.
    SQLite and bundled PostgreSQL (--locale=C) lower() fold ASCII only, so the Python
    down-root check must catch it without calling _exists."""
    await setup(db, Recorder())
    low_root = tmp_path / "été"
    low_lib = await _library(db, low_root)
    vid = await make_video(db, low_lib, path=f"{(tmp_path / 'Été').as_posix()}/only.mp4")
    job = await _fail(db, vid)
    asked: list[str] = []

    def would_heal(path: str) -> bool:
        asked.append(path)
        return True

    monkeypatch.setattr(healer, "_exists", would_heal)
    down = {f"dependency:share:{low_root.as_posix()}": (False, "offline")}
    assert await heal_once(db, down) == 0
    assert asked == []
    if await db.fetchval("SELECT lower('É') = 'É'"):
        assert _about(await _next_look(db, job), healer.AGAIN)


@pytest.mark.asyncio
async def test_an_exception_from_exists_does_not_stop_the_other_files(
    db: Database, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    await setup(db, Recorder())
    lib = await _library(db, tmp_path)
    jobs: dict[str, int] = {}
    for name in ("a", "b", "c"):
        v = await make_video(db, lib, path=f"{tmp_path.as_posix()}/{name}.mp4")
        jobs[name] = await _fail(db, v)

    def flaky_exists(path: str) -> bool:
        if path.endswith("/b.mp4"):
            raise OSError("stale NFS handle")
        return True

    monkeypatch.setattr(healer, "_exists", flaky_exists)
    assert await heal_once(db, UP) == 2
    assert _about(await _next_look(db, jobs["b"]), healer.AGAIN)


@pytest.mark.asyncio
async def test_mid_run_trigger_on_healed_dedup_scan_does_not_inherit_heal_priority(
    db: Database, tmp_path: Path
) -> None:
    from maktaba_pipeline.engine.graph import EPIC47_PRIORITIES

    graph = (
        StageDef("dedup_scan", "Dedup scan", ("video",), "cpu", "episodes", 1, 1),
        *toy_graph(),
    )
    rec = Recorder()
    triggered = False

    async def trigger_mid_run(db_: Any, job: Job, _ctx: Any) -> StageResult:
        nonlocal triggered
        if not triggered:
            triggered = True
            await enqueue_job(
                db_,
                subject_kind="video",
                subject_id=job.subject.id,
                stage="dedup_scan",
                priority=EPIC47_PRIORITIES["dedup_scan"],
            )
        return StageResult(output_hash=f"out-{job.id}")

    rec.hooks["dedup_scan"] = trigger_mid_run
    rt = await setup(db, rec, graph)
    v = await make_video(db, await _library(db, tmp_path), path=(tmp_path / "a.mp4").as_posix())
    (tmp_path / "a.mp4").write_bytes(b"x")
    await _fail(db, v, stage="dedup_scan")
    assert await heal_once(db, UP) == 1
    await drain(db, rt)
    seen = await rows(db, "dedup_scan")
    # failed original (100), healed run (950), and mid-run follow-up (150, NOT 950)
    assert [(r["state"], r["priority"]) for r in seen] == [
        ("failed", 100),
        ("done", HEAL_PRIORITY),
        ("done", EPIC47_PRIORITIES["dedup_scan"]),
    ]


@pytest.mark.asyncio
async def test_healed_relisten_job_is_counted_in_relisten_attempts(
    db: Database, tmp_path: Path
) -> None:
    graph = (
        StageDef("second_listen", "Second listen", ("video",), "cpu", "speech", 2, 1),
        *toy_graph(),
    )
    await setup(db, Recorder(), graph)
    lib = await _library(db, tmp_path)
    (tmp_path / "a.mp4").write_bytes(b"x")
    v = await make_video(db, lib, path=(tmp_path / "a.mp4").as_posix())
    res = await enqueue_job(
        db,
        subject_kind="video",
        subject_id=v,
        stage="second_listen",
        payload={"relisten": True},
    )
    assert res.job_id is not None
    await _end(db, res.job_id)
    past = to_ts(db, now() - timedelta(hours=2))
    await db.execute(
        "INSERT INTO relisten_attempts (video_id, attempts, last_job_id, last_at, next_at,"
        " ended_at) VALUES ($1, 2, $2, $3, $3, $3)",
        str(v),
        res.job_id,
        past,
    )
    assert await heal_once(db, UP) == 1
    (job,) = await _pending(db, "second_listen")
    tries = await db.fetchrow(
        "SELECT attempts, last_job_id, ended_at, stopped_at FROM relisten_attempts"
        " WHERE video_id = $1",
        str(v),
    )
    assert tries is not None
    assert tries["attempts"] == 3
    assert tries["last_job_id"] == job["id"]
    assert tries["ended_at"] is None and tries["stopped_at"] is None


@pytest.mark.asyncio
@pytest.mark.parametrize("forced", [False, True])
async def test_healed_job_keeps_force_flag(db: Database, tmp_path: Path, forced: bool) -> None:
    await setup(db, Recorder())
    lib = await _library(db, tmp_path)
    (tmp_path / "a.mp4").write_bytes(b"x")
    v = await make_video(db, lib, path=(tmp_path / "a.mp4").as_posix())
    res = await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a", force=forced)
    assert res.job_id is not None
    await _end(db, res.job_id)
    assert await heal_once(db, UP) == 1
    (job,) = await _pending(db)
    assert bool(job["force"]) is forced


@pytest.mark.asyncio
async def test_access_check_and_deleted_library_are_not_healed(
    db: Database, tmp_path: Path
) -> None:
    graph = (
        StageDef("access_check", "Access check", ("video",), "cpu", "media", 0, 1),
        *toy_graph(),
    )
    await setup(db, Recorder(), graph)
    lib = await _library(db, tmp_path)
    (tmp_path / "a.mp4").write_bytes(b"x")
    v_ac = await make_video(db, lib, path=(tmp_path / "a.mp4").as_posix())
    await _fail(db, v_ac, stage="access_check", max_attempts=3)

    del_lib = await _library(db, tmp_path)
    (tmp_path / "b.mp4").write_bytes(b"x")
    v_del = await make_video(db, del_lib, path=(tmp_path / "b.mp4").as_posix())
    await _fail(db, v_del, stage="t_a")
    await db.execute(
        "UPDATE libraries SET deleted_at = $2 WHERE id = $1", str(del_lib), to_ts(db, now())
    )

    assert await heal_once(db, UP) == 0
    assert await _pending(db, "access_check") == []
    assert await _pending(db, "t_a") == []


@pytest.mark.asyncio
async def test_unqueueable_stage_result_leaves_row_for_left(
    db: Database, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    from maktaba_pipeline.engine.enqueue import EnqueueResult

    await setup(db, Recorder())
    lib = await _library(db, tmp_path)
    (tmp_path / "a.mp4").write_bytes(b"x")
    v = await make_video(db, lib, path=(tmp_path / "a.mp4").as_posix())
    job = await _fail(db, v)

    async def none_enqueued(*_a: Any, **_kw: Any) -> EnqueueResult:
        return EnqueueResult(None, "disabled")

    monkeypatch.setattr(healer, "requeue_stage", none_enqueued)
    assert await heal_once(db, UP) == 0
    assert _about(await _next_look(db, job), healer.LEFT)
