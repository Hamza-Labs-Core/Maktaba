"""Story 47.2: the trash (same-volume renames only), the ledger's transitions, the
``trash_purge`` stage (it respects ``purge_after`` and refuses a size mismatch), and the
readers that must never see a trashed or staged file."""

from __future__ import annotations

import errno
import os
import uuid
from datetime import UTC, date, datetime, timedelta
from pathlib import Path
from typing import Any
from unittest.mock import MagicMock

import pytest
from watchdog.events import FileCreatedEvent, FileMovedEvent

from maktaba_pipeline.engine.context import StageContext, StageFailure
from maktaba_pipeline.engine.graph import get_stage
from maktaba_pipeline.engine.sql import to_ts
from maktaba_pipeline.engine.stages import trash_purge
from maktaba_pipeline.library_mgmt import trash as trash_mod
from maktaba_pipeline.library_mgmt.ledger import (
    LedgerError,
    advance,
    create_action,
    get_action,
    list_actions,
    open_actions,
)
from maktaba_pipeline.library_mgmt.swap import swap_in
from maktaba_pipeline.library_mgmt.trash import (
    CrossDeviceError,
    TrashError,
    TrashSizeMismatch,
    move_to_trash,
    purge_due,
    root_for,
    trash_days,
    trash_path_for,
)
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.scanner.walker import WalkConfig, walk
from maktaba_pipeline.watcher.service import LibraryObserver, WatcherConfig
from tests.library_mgmt.epic47_world import make_world, new_file, same

DAY = date(2026, 10, 28)

# ---------------------------------------------------------------- paths and moves


@pytest.mark.unit
def test_the_trash_path() -> None:
    assert (
        trash_path_for("/v/Youtube/Ch", "/v/Youtube/Ch/2024/a.mkv", "convert", DAY)
        == "/v/Youtube/Ch/.maktaba/trash/convert/2026-10-28/2024/a.mkv"
    )
    assert (
        trash_path_for("smb://nas/share/Ch/", "smb://nas/share/Ch/a b.mkv", "dedup", DAY)
        == "smb://nas/share/Ch/.maktaba/trash/dedup/2026-10-28/a b.mkv"
    )
    with pytest.raises(TrashError):
        trash_path_for("/v/Ch", "/v/Other/a.mkv", "convert", DAY)
    with pytest.raises(TrashError):
        trash_path_for("/v/Ch", "/v/Ch/.maktaba/trash/x.mkv", "convert", DAY)


@pytest.mark.unit
def test_the_root_of_a_path_is_the_longest_that_holds_it() -> None:
    roots = ["/v/Ch", "/v/Ch/Sub", "/v/Chx"]
    assert root_for(roots, "/v/Ch/Sub/a.mkv") == "/v/Ch/Sub"
    assert root_for(roots, "/v/Ch/a.mkv") == "/v/Ch"
    assert root_for(roots, "/v/Chx/a.mkv") == "/v/Chx"
    assert root_for(roots, "/v/Chy/a.mkv") is None


@pytest.mark.unit
def test_a_trash_move_is_a_rename_that_never_overwrites(tmp_path: Path) -> None:
    src = tmp_path / "a.mkv"
    src.write_bytes(b"orig")
    dest = tmp_path / ".maktaba" / "trash" / "convert" / "2026-10-28" / "a.mkv"
    ino = src.stat().st_ino
    move_to_trash(str(src), str(dest))
    assert not src.exists() and dest.read_bytes() == b"orig"
    assert dest.stat().st_ino == ino  # the same file: renamed, not copied
    src.write_bytes(b"again")
    with pytest.raises(TrashError):
        move_to_trash(str(src), str(dest))
    assert dest.read_bytes() == b"orig" and src.read_bytes() == b"again"
    assert trash_mod.free_trash_path(str(dest)).endswith("a~2.mkv")


@pytest.mark.unit
def test_a_move_across_volumes_is_refused_not_copied(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    src = tmp_path / "a.mkv"
    src.write_bytes(b"orig")
    dest = tmp_path / "trash" / "a.mkv"
    devices = iter([1, 2])
    monkeypatch.setattr(trash_mod, "device_of", lambda p: next(devices))
    with pytest.raises(CrossDeviceError):
        move_to_trash(str(src), str(dest))
    assert src.read_bytes() == b"orig" and not dest.exists()


@pytest.mark.unit
def test_an_exdev_rename_is_refused_not_copied(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    src = tmp_path / "a.mkv"
    src.write_bytes(b"orig")
    dest = tmp_path / "trash" / "a.mkv"

    def exdev(a: str, b: str) -> None:
        raise OSError(errno.EXDEV, "Cross-device link")

    monkeypatch.setattr(os, "rename", exdev)
    with pytest.raises(CrossDeviceError):
        move_to_trash(str(src), str(dest))
    assert src.read_bytes() == b"orig" and not dest.exists()


# ---------------------------------------------------------------- readers skip it


@pytest.mark.unit
def test_the_walker_skips_the_trash_and_staged_files(tmp_path: Path) -> None:
    (tmp_path / "Show").mkdir()
    (tmp_path / "Show" / "talk.mp4").write_bytes(b"x")
    (tmp_path / "Show" / ".talk.maktaba-new.mp4").write_bytes(b"x")
    t = tmp_path / ".maktaba" / "trash" / "convert" / "2026-10-28" / "Show"
    t.mkdir(parents=True)
    (t / "talk.mkv").write_bytes(b"x")
    found = [c.path for c in walk(tmp_path, WalkConfig(), MagicMock())]
    assert found == [str(tmp_path / "Show" / "talk.mp4")]


@pytest.mark.unit
def test_the_watcher_ignores_staged_and_trashed_files(tmp_path: Path) -> None:
    fed: list[Any] = []
    deb = MagicMock()
    deb.feed.side_effect = fed.append
    obs = LibraryObserver(
        library_id=uuid.uuid4(),
        roots=[str(tmp_path)],
        observer=MagicMock(),
        debouncer=deb,
        cfg=WatcherConfig(),
        log=MagicMock(),
    )
    staged = str(tmp_path / ".talk.maktaba-new.mp4")
    final = str(tmp_path / "talk.mp4")
    orig = str(tmp_path / "talk.mkv")
    trashed = str(tmp_path / ".maktaba" / "trash" / "convert" / "2026-10-28" / "talk.mkv")
    obs.on_created(FileCreatedEvent(staged))
    obs.on_created(FileCreatedEvent(trashed))
    assert fed == []
    # the original into the trash: its path goes away (the row already has the new one)
    obs.on_moved(FileMovedEvent(orig, trashed))
    # the staged file to its name: a new file there (the scanner finds the swapped row)
    obs.on_moved(FileMovedEvent(staged, final))
    assert [(e.op.value, e.path) for e in fed] == [("deleted", orig), ("create", final)]


# ---------------------------------------------------------------- the ledger


@pytest.mark.asyncio
async def test_the_ledger_moves_forward_only(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path)
    a = await create_action(db, library_id=w.library_id, kind="convert", old_path="/x")
    assert a.state == "planned" and a.is_open
    a = await advance(db, a.id, "staged", expect="planned", new_size=5, new_hash="h")
    assert (a.state, a.new_size, a.new_hash) == ("staged", 5, "h")
    with pytest.raises(LedgerError):
        await advance(db, a.id, "planned")
    with pytest.raises(LedgerError):
        await advance(db, a.id, "verified", expect="planned")
    with pytest.raises(LedgerError):
        await advance(db, a.id, "done")
    a = await advance(db, a.id, "verified", verify={"ok": True, "frames": 100})
    assert a.verify == {"ok": True, "frames": 100}
    assert [x.id for x in await open_actions(db, library_id=w.library_id)] == [a.id]
    a = await advance(db, a.id, "swapped", purge_after=datetime(2026, 11, 27, tzinfo=UTC))
    a = await advance(db, a.id, "done")
    assert a.purge_after == datetime(2026, 11, 27, tzinfo=UTC)
    assert not await open_actions(db, library_id=w.library_id)
    with pytest.raises(LedgerError):
        await advance(db, a.id, "swapped")
    with pytest.raises(LedgerError):
        await advance(db, a.id, "done", bogus=1)
    with pytest.raises(LedgerError):
        await create_action(db, library_id=w.library_id, kind="burn")  # type: ignore[arg-type]
    assert [x.id for x in await list_actions(db, library_id=w.library_id, kinds=["convert"])] == [
        a.id
    ]
    assert not await list_actions(db, library_id=w.library_id, states=["failed"])


@pytest.mark.asyncio
async def test_the_trash_days_come_from_the_library(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path / "a")
    assert await trash_days(db, w.library_id) == 30
    w7 = await make_world(db, tmp_path / "b", settings={"trash": {"days": 7}})
    assert await trash_days(db, w7.library_id) == 7
    wbad = await make_world(db, tmp_path / "c", settings={"trash": {"days": "soon"}})
    assert await trash_days(db, wbad.library_id) == 30


# ---------------------------------------------------------------- purge


async def _trashed(db: Database, tmp_path: Path, mark: bytes = b"O", **kw: Any):  # type: ignore[no-untyped-def]
    w = await make_world(db, tmp_path, **kw)
    vid, orig = await w.video("talk.mkv", mark * 3000)
    res = await swap_in(
        db,
        library_id=w.library_id,
        kind="convert",
        source=str(new_file(tmp_path, "talk.mp4", mark.lower() * 2000)),
        final_path=str(w.root / "talk.mp4"),
        video_id=vid,
        to_local=same,
        day=DAY,
    )
    assert res.trash_path is not None
    return w, vid, res.action, Path(res.trash_path)


@pytest.mark.asyncio
async def test_purge_waits_for_purge_after(db: Database, tmp_path: Path) -> None:
    w, vid, a, t = await _trashed(db, tmp_path, settings={"trash": {"days": 3}})
    assert a.purge_after is not None
    early = await purge_due(
        db, w.library_id, at=a.purge_after - timedelta(minutes=1), to_local=same
    )
    assert not early.purged and t.exists()
    report = await purge_due(
        db, w.library_id, at=a.purge_after + timedelta(seconds=1), to_local=same
    )
    assert report.purged == [a.id] and report.bytes_freed == 3000
    assert not t.exists()
    assert not (w.root / ".maktaba" / "trash" / "convert").exists()  # empty folders pruned
    got = await get_action(db, a.id)
    assert got is not None and got.purged_at is not None and got.state == "done"
    await w.assert_tracked(vid)  # the video itself is untouched
    again = await purge_due(db, w.library_id, at=a.purge_after + timedelta(days=9), to_local=same)
    assert not again.purged and not again.missing


@pytest.mark.asyncio
async def test_purge_refuses_a_size_mismatch(db: Database, tmp_path: Path) -> None:
    w, vid, a, t = await _trashed(db, tmp_path)
    t.write_bytes(b"changed")
    assert a.purge_after is not None
    with pytest.raises(TrashSizeMismatch):
        await purge_due(db, w.library_id, at=a.purge_after + timedelta(days=1), to_local=same)
    assert t.read_bytes() == b"changed"
    got = await get_action(db, a.id)
    assert got is not None and got.purged_at is None
    assert got.error is not None and "purge refused" in got.error


@pytest.mark.asyncio
async def test_a_file_already_gone_is_recorded_as_purged(db: Database, tmp_path: Path) -> None:
    w, vid, a, t = await _trashed(db, tmp_path)
    t.unlink()
    assert a.purge_after is not None
    report = await purge_due(db, w.library_id, at=a.purge_after + timedelta(days=1), to_local=same)
    assert report.missing == [a.id] and not report.purged
    got = await get_action(db, a.id)
    assert got is not None and got.purged_at is not None


@pytest.mark.asyncio
async def test_a_purged_duplicate_takes_its_superseded_row(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path)
    keeper, _ = await w.video("a.mp4", b"K" * 100)
    loser, lp = await w.video("copy/a.mp4", b"L" * 100)
    trash = Path(trash_path_for(str(w.root), str(lp), "dedup", DAY))
    move_to_trash(str(lp), str(trash))
    await db.execute(
        "UPDATE videos SET state = 'superseded', superseded_by = $2 WHERE id = $1",
        str(loser),
        str(keeper),
    )
    when = datetime.now(UTC) - timedelta(days=1)
    a = await create_action(
        db,
        library_id=w.library_id,
        kind="dedup",
        video_id=loser,
        state="done",
        old_path=str(lp),
        old_size=100,
        trash_path=str(trash),
        purge_after=when,
    )
    report = await purge_due(db, w.library_id, to_local=same)
    assert report.purged == [a.id] and report.rows_deleted == [loser]
    assert await w.row(loser) is None
    assert await w.row(keeper) is not None
    got = await get_action(db, a.id)
    assert got is not None and got.video_id is None and got.purged_at is not None


@pytest.mark.asyncio
async def test_the_trash_purge_stage(db: Database, tmp_path: Path) -> None:
    w, vid, a, t = await _trashed(db, tmp_path, settings={"trash": {"days": 0}})
    stage = get_stage("trash_purge")
    assert stage is not None
    job = MagicMock()
    job.id = 1
    job.subject.id = w.library_id
    ctx = MagicMock(spec=StageContext)
    ctx.local_path.side_effect = same

    async def nothing(*a: Any, **k: Any) -> None:
        return None

    ctx.check_cancel.side_effect = nothing
    ctx.progress.side_effect = nothing
    res = await trash_purge.run(db, job, ctx)
    assert res.outcome == "changed" and res.summary["purged"] == 1
    assert res.summary["bytes_freed"] == 3000 and not t.exists()
    again = await trash_purge.run(db, job, ctx)
    assert again.outcome == "unchanged"

    # a size mismatch fails the job, not retried
    w2, _, a2, t2 = await _trashed(db, tmp_path / "two", b"P", settings={"trash": {"days": 0}})
    t2.write_bytes(b"x")
    job.subject.id = w2.library_id
    with pytest.raises(StageFailure) as err:
        await trash_purge.run(db, job, ctx)
    assert err.value.kind == "trash_size_mismatch" and err.value.retryable is False
    assert t2.exists()


# ---------------------------------------------------------------- Restore is a queue job (47.11)


def _stage_ctx() -> Any:
    ctx = MagicMock(spec=StageContext)
    ctx.local_path.side_effect = same

    async def nothing(*a: Any, **k: Any) -> None:
        return None

    ctx.check_cancel.side_effect = nothing
    ctx.progress.side_effect = nothing
    return ctx


@pytest.mark.asyncio
async def test_the_file_restore_stage(db: Database, tmp_path: Path) -> None:
    from maktaba_pipeline.engine.stages import file_restore

    stage = get_stage("file_restore")
    assert stage is not None and stage.pool == "nas_write" and stage.subject_kinds == ("library",)
    w, vid, a, t = await _trashed(db, tmp_path)
    fetched = await create_action(
        db, library_id=w.library_id, kind="youtube_fetch", state="done", new_path="/x.mp4"
    )
    job = MagicMock()
    job.id = 7
    job.subject.id = w.library_id
    job.payload = {"action_ids": [str(a.id), str(fetched.id), "not-a-uuid"]}
    res = await file_restore.run(db, job, _stage_ctx())
    # the original is back, the MP4 is in the trash; a download has no undo
    assert res.outcome == "changed" and res.summary["restored"] == [str(a.id)]
    assert [r["id"] for r in res.summary["refused"]] == [str(fetched.id)]
    p = await w.assert_tracked(vid)
    assert p.suffix == ".mkv" and not t.exists()
    got = await get_action(db, a.id)
    assert got is not None and got.state == "rolled_back"

    # a follow-up that lists it again skips it
    job.payload = {"action_ids": [str(a.id)]}
    again = await file_restore.run(db, job, _stage_ctx())
    assert again.outcome == "unchanged" and again.summary["already_restored"] == [str(a.id)]

    # another library's row is refused; nothing restorable at all fails, not retried
    other = await make_world(db, tmp_path / "other")
    job.subject.id = other.library_id
    job.payload = {"action_ids": [str(fetched.id)]}
    with pytest.raises(StageFailure) as err:
        await file_restore.run(db, job, _stage_ctx())
    assert err.value.kind == "restore_refused" and err.value.retryable is False
    job.payload = {}
    with pytest.raises(StageFailure) as err:
        await file_restore.run(db, job, _stage_ctx())
    assert err.value.kind == "restore_bad_payload"


@pytest.mark.asyncio
async def test_the_reconciler_empties_the_trash_daily(db: Database, tmp_path: Path) -> None:
    from maktaba_pipeline.engine.graph import GRAPH
    from maktaba_pipeline.engine.reconcile import reconcile_once

    graph = {s.name: s for s in GRAPH}
    w, vid, a, t = await _trashed(db, tmp_path)
    assert a.purge_after is not None
    # nothing is due yet: no job
    rep = await reconcile_once(db, graph, at=a.purge_after - timedelta(hours=1))
    assert rep.trash_purge == 0
    # due: one trash_purge for the library at its priority, and only one a day
    rep = await reconcile_once(db, graph, at=a.purge_after + timedelta(hours=1))
    assert rep.trash_purge == 1
    job = await db.fetchrow(
        "SELECT subject_id, priority FROM processing_jobs WHERE stage = 'trash_purge'"
    )
    assert str(job["subject_id"]) == str(w.library_id) and job["priority"] == 200
    later = await reconcile_once(db, graph, at=a.purge_after + timedelta(hours=2))
    assert later.trash_purge == 0
    # it ran: the next one waits a day
    await db.execute(
        "UPDATE processing_jobs SET state = 'done', finished_at = $1 WHERE stage = 'trash_purge'",
        to_ts(db, a.purge_after + timedelta(hours=1)),
    )
    same_day = await reconcile_once(db, graph, at=a.purge_after + timedelta(hours=3))
    assert same_day.trash_purge == 0
    next_day = await reconcile_once(db, graph, at=a.purge_after + timedelta(hours=26))
    assert next_day.trash_purge == 1
