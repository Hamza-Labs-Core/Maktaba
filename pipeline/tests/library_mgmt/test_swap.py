"""Story 47.2: the swap order, crash recovery at every step, and Restore.

Each crash case ends with exactly one tracked, readable file and the same ``videos.id``.
Both dialects (the ``db`` fixture: SQLite always, Postgres with MAKTABA_TEST_PG_URL).
"""

from __future__ import annotations

from datetime import date
from pathlib import Path

import pytest

from maktaba_pipeline.identity.hasher import hash_file
from maktaba_pipeline.library_mgmt.ledger import get_action, list_actions
from maktaba_pipeline.library_mgmt.swap import (
    RestoreError,
    SwapError,
    SwapVerifyFailed,
    is_staged_name,
    recover,
    restore,
    staged_path,
    swap_in,
)
from maktaba_pipeline.runtime import Database
from tests.library_mgmt.epic47_world import (
    CrashInjected,
    bad_verify,
    crash_at,
    make_world,
    new_file,
    ok_verify,
    same,
)

DAY = date(2026, 10, 28)
OLD = b"MKV" + b"o" * 5000
NEW = b"MP4" + b"n" * 4000


def test_the_staged_name_is_a_dotfile_beside_the_target() -> None:
    assert staged_path("/lib/a/talk.mp4") == "/lib/a/.talk.maktaba-new.mp4"
    assert is_staged_name(".talk.maktaba-new.mp4")
    assert is_staged_name(".محاضرة.maktaba-new.mp4")
    assert not is_staged_name("talk.mp4")
    assert not is_staged_name(".DS_Store")


@pytest.mark.asyncio
async def test_a_swap_keeps_the_id_and_trashes_the_original(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path)
    vid, orig = await w.video("Show/talk.mkv", OLD)
    src = new_file(tmp_path, "talk.mp4", NEW)
    final = str(w.root / "Show" / "talk.mp4")
    res = await swap_in(
        db,
        library_id=w.library_id,
        kind="convert",
        source=str(src),
        final_path=final,
        verify=ok_verify,
        video_id=vid,
        job_id=7,
        to_local=same,
        day=DAY,
    )
    a = res.action
    assert a.state == "done" and a.video_id == vid and a.job_id == 7
    assert (a.old_path, a.new_path) == (str(orig), final)
    assert a.old_size == len(OLD) and a.new_size == len(NEW)
    assert a.new_hash == hash_file(final).content_hash
    assert a.verify == {"ok": True, "reasons": [], "measured": {"checked": ".talk.maktaba-new.mp4"}}
    assert a.purge_after is not None and a.updated_at is not None
    assert (a.purge_after - a.updated_at).days in (29, 30)
    trash = w.root / ".maktaba" / "trash" / "convert" / "2026-10-28" / "Show" / "talk.mkv"
    assert res.trash_path == a.trash_path == str(trash)
    assert trash.read_bytes() == OLD and not orig.exists()
    p = await w.assert_tracked(vid)
    assert p == Path(final) and p.read_bytes() == NEW
    row = await w.row(vid)
    assert row["filename"] == "talk.mp4"
    assert not w.staged_files()
    # the source in the work folder is the caller's to delete
    assert src.exists()


@pytest.mark.asyncio
async def test_a_same_name_swap(db: Database, tmp_path: Path) -> None:
    # an MP4 whose moov is at the end becomes a faststart MP4 under the same name
    w = await make_world(db, tmp_path)
    vid, orig = await w.video("talk.mp4", OLD)
    src = new_file(tmp_path, "talk.mp4", NEW)
    res = await swap_in(
        db,
        library_id=w.library_id,
        kind="convert",
        source=str(src),
        final_path=str(orig),
        video_id=vid,
        to_local=same,
        day=DAY,
    )
    assert res.action.state == "done"
    assert orig.read_bytes() == NEW
    assert [p.read_bytes() for p in w.trash_files()] == [OLD]
    await w.assert_tracked(vid)


@pytest.mark.asyncio
async def test_a_failed_verification_changes_nothing(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path)
    vid, orig = await w.video("talk.mkv", OLD)
    before = await w.row(vid)
    src = new_file(tmp_path, "talk.mp4", NEW)
    with pytest.raises(SwapVerifyFailed) as err:
        await swap_in(
            db,
            library_id=w.library_id,
            kind="convert",
            source=str(src),
            final_path=str(w.root / "talk.mp4"),
            verify=bad_verify,
            video_id=vid,
            to_local=same,
        )
    a = err.value.action
    assert a is not None and a.state == "failed"
    assert a.verify is not None and a.verify["reasons"] == ["decode errors: invalid NAL unit"]
    assert a.error is not None and "verification failed" in a.error
    assert orig.read_bytes() == OLD and not (w.root / "talk.mp4").exists()
    assert not w.staged_files() and not w.trash_files()
    assert dict(await w.row(vid)) == dict(before)


@pytest.mark.asyncio
async def test_a_swap_never_overwrites_another_file(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path)
    vid, _ = await w.video("talk.mkv", OLD)
    other = w.root / "talk.mp4"
    other.write_bytes(b"someone else's")
    with pytest.raises(SwapError):
        await swap_in(
            db,
            library_id=w.library_id,
            kind="convert",
            source=str(new_file(tmp_path, "t.mp4", NEW)),
            final_path=str(other),
            video_id=vid,
            to_local=same,
        )
    assert other.read_bytes() == b"someone else's"
    assert not await list_actions(db, library_id=w.library_id)


@pytest.mark.asyncio
async def test_a_hash_already_in_the_library_is_refused_before_any_rename(
    db: Database, tmp_path: Path
) -> None:
    w = await make_world(db, tmp_path)
    vid, orig = await w.video("talk.mkv", OLD)
    await w.video("other.mp4", NEW)  # the converted bytes are already a video here
    with pytest.raises(SwapError):
        await swap_in(
            db,
            library_id=w.library_id,
            kind="convert",
            source=str(new_file(tmp_path, "t.mp4", NEW)),
            final_path=str(w.root / "talk.mp4"),
            video_id=vid,
            to_local=same,
        )
    (a,) = await list_actions(db, library_id=w.library_id)
    assert a.state == "failed"
    assert orig.read_bytes() == OLD and not w.staged_files() and not w.trash_files()
    await w.assert_tracked(vid)


@pytest.mark.asyncio
async def test_a_new_download_is_placed_without_a_row(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path)
    final = str(w.root / "Channel" / "2026-01-01 Talk [abc].mp4")
    (w.root / "Channel").mkdir()
    res = await swap_in(
        db,
        library_id=w.library_id,
        kind="youtube_fetch",
        source=str(new_file(tmp_path, "abc.mp4", NEW)),
        final_path=final,
        verify=ok_verify,
        to_local=same,
    )
    assert res.action.state == "done" and res.trash_path is None
    assert Path(final).read_bytes() == NEW and not w.staged_files()


# ---------------------------------------------------------------- crash injection


async def _crashed_swap(db: Database, tmp_path: Path, step: str, *, same_name: bool = False):  # type: ignore[no-untyped-def]
    w = await make_world(db, tmp_path)
    vid, orig = await w.video("Show/talk.mp4" if same_name else "Show/talk.mkv", OLD)
    final = orig if same_name else w.root / "Show" / "talk.mp4"
    with pytest.raises(CrashInjected):
        await swap_in(
            db,
            library_id=w.library_id,
            kind="convert",
            source=str(new_file(tmp_path, "talk.mp4", NEW)),
            final_path=str(final),
            verify=ok_verify,
            video_id=vid,
            to_local=same,
            day=DAY,
            crash=crash_at(step),
        )
    (a,) = await list_actions(db, library_id=w.library_id)
    return w, vid, orig, final, a


@pytest.mark.asyncio
@pytest.mark.parametrize("step", ["staged", "verified"])
async def test_a_crash_before_the_swap_rolls_back(db: Database, tmp_path: Path, step: str) -> None:
    w, vid, orig, final, a = await _crashed_swap(db, tmp_path, step)
    assert a.state == step
    assert w.staged_files()  # the crash left it
    report = await recover(db, to_local=same)
    assert report.rolled_back == [a.id] and not report.failed
    after = await get_action(db, a.id)
    assert after is not None and after.state == "rolled_back"
    assert not w.staged_files() and not w.trash_files() and not final.exists()
    p = await w.assert_tracked(vid)
    assert p == orig and p.read_bytes() == OLD


@pytest.mark.asyncio
@pytest.mark.parametrize("same_name", [False, True])
@pytest.mark.parametrize("step", ["swapped", "trashed"])
async def test_a_crash_after_the_swap_is_finished(
    db: Database, tmp_path: Path, step: str, same_name: bool
) -> None:
    w, vid, orig, final, a = await _crashed_swap(db, tmp_path, step, same_name=same_name)
    assert a.state == "swapped"
    report = await recover(db, to_local=same)
    assert report.finished == [a.id] and not report.failed
    assert (await get_action(db, a.id)).state == "done"  # type: ignore[union-attr]
    p = await w.assert_tracked(vid)
    assert p == final and p.read_bytes() == NEW
    assert [t.read_bytes() for t in w.trash_files()] == [OLD]
    assert not w.staged_files()
    if not same_name:
        assert not orig.exists()


@pytest.mark.asyncio
async def test_a_swapped_row_whose_new_file_is_lost_goes_back(db: Database, tmp_path: Path) -> None:
    w, vid, orig, final, a = await _crashed_swap(db, tmp_path, "swapped")
    for s in w.staged_files():
        s.unlink()
    report = await recover(db, to_local=same)
    assert report.rolled_back == [a.id]
    p = await w.assert_tracked(vid)
    assert p == orig and p.read_bytes() == OLD
    assert not w.trash_files()


@pytest.mark.asyncio
async def test_a_trashed_original_comes_back_when_the_new_file_is_lost(
    db: Database, tmp_path: Path
) -> None:
    w, vid, orig, final, a = await _crashed_swap(db, tmp_path, "trashed")
    for s in w.staged_files():
        s.unlink()
    report = await recover(db, to_local=same)
    assert report.rolled_back == [a.id]
    p = await w.assert_tracked(vid)
    assert p == orig and p.read_bytes() == OLD and not w.trash_files()


@pytest.mark.asyncio
async def test_recovery_leaves_a_running_jobs_row_alone(db: Database, tmp_path: Path) -> None:
    w, vid, orig, final, a = await _crashed_swap(db, tmp_path, "verified")
    lib = str(w.library_id)
    job = await db.fetchval(
        "INSERT INTO processing_jobs (subject_kind, subject_id, video_id, library_id, stage,"
        " state) VALUES ('video', $1, $1, $2, 'access_check', 'running') RETURNING id",
        str(vid),
        lib,
    )
    await db.execute("UPDATE file_actions SET job_id = $1 WHERE id = $2", job, str(a.id))
    assert (await recover(db, to_local=same)).touched == 0
    # the stage's own retry (a new swap of the video) settles it first
    res = await swap_in(
        db,
        library_id=w.library_id,
        kind="convert",
        source=str(new_file(tmp_path, "again.mp4", NEW)),
        final_path=str(final),
        video_id=vid,
        to_local=same,
        day=DAY,
    )
    assert res.action.state == "done"
    assert (await get_action(db, a.id)).state == "rolled_back"  # type: ignore[union-attr]
    await w.assert_tracked(vid)


# ---------------------------------------------------------------- restore


@pytest.mark.asyncio
@pytest.mark.parametrize("same_name", [False, True])
async def test_restore_round_trips(db: Database, tmp_path: Path, same_name: bool) -> None:
    w = await make_world(db, tmp_path)
    vid, orig = await w.video("talk.mp4" if same_name else "talk.mkv", OLD)
    before = await w.row(vid)
    final = orig if same_name else w.root / "talk.mp4"
    res = await swap_in(
        db,
        library_id=w.library_id,
        kind="convert",
        source=str(new_file(tmp_path, "talk.mp4", NEW)),
        final_path=str(final),
        video_id=vid,
        to_local=same,
        day=DAY,
    )
    row = await restore(db, res.action.id, to_local=same, day=DAY)
    assert row.kind == "restore" and row.state == "done"
    assert (await get_action(db, res.action.id)).state == "rolled_back"  # type: ignore[union-attr]
    p = await w.assert_tracked(vid)
    assert p == orig and p.read_bytes() == OLD
    after = await w.row(vid)
    for k in ("path", "filename", "content_hash", "size_bytes"):
        assert after[k] == before[k]
    # the MP4 is in the trash now, under "restore", until its own purge
    (mp4,) = w.trash_files()
    assert mp4.read_bytes() == NEW and "restore" in mp4.parts
    assert row.trash_path == str(mp4) and row.old_size == len(NEW)
    with pytest.raises(RestoreError):
        await restore(db, res.action.id, to_local=same)


@pytest.mark.asyncio
async def test_a_restore_cut_short_is_finished_by_recovery(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path)
    vid, orig = await w.video("talk.mkv", OLD)
    res = await swap_in(
        db,
        library_id=w.library_id,
        kind="convert",
        source=str(new_file(tmp_path, "talk.mp4", NEW)),
        final_path=str(w.root / "talk.mp4"),
        video_id=vid,
        to_local=same,
        day=DAY,
    )

    def boom(a: str, b: str) -> None:
        raise CrashInjected("restore")

    with pytest.MonkeyPatch.context() as mp:
        mp.setattr("maktaba_pipeline.library_mgmt.swap.restore_from_trash", boom)
        with pytest.raises(CrashInjected):
            await restore(db, res.action.id, to_local=same, day=DAY)
    # the MP4 went to the trash, the original is still there too
    assert not (w.root / "talk.mp4").exists()
    report = await recover(db, to_local=same)
    assert len(report.finished) == 1 and not report.failed
    p = await w.assert_tracked(vid)
    assert p == orig and p.read_bytes() == OLD


@pytest.mark.asyncio
async def test_a_purged_or_open_row_cannot_be_restored(db: Database, tmp_path: Path) -> None:
    w, vid, orig, final, a = await _crashed_swap(db, tmp_path, "verified")
    with pytest.raises(RestoreError):
        await restore(db, a.id, to_local=same)
    await recover(db, to_local=same)
    with pytest.raises(RestoreError):
        await restore(db, a.id, to_local=same)
