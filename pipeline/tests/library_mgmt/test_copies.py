"""Story 47.5: the scanner keeps every copy of a file (real migrated SQLite; Postgres with
MAKTABA_TEST_PG_URL).

Two byte-identical files of a library are one video and one ``video_copies`` row; the
row keeps its own path; removing either file keeps the video.
"""

from __future__ import annotations

import shutil
from pathlib import Path
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.engine.missing_files import confirm_missing
from maktaba_pipeline.log import get_logger
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.scanner import Scanner, SqlScanStore
from maktaba_pipeline.scanner.copies import promote_copy, reconcile_copies
from tests.library_mgmt.epic47_world import World, make_world


async def _scan(db: Database, w: World) -> Any:
    return await Scanner(SqlScanStore(db), log=get_logger()).run(w.library_id)


async def _videos(db: Database, w: World) -> list[Any]:
    return list(
        await db.fetch(
            "SELECT id, path, state FROM videos WHERE library_id = $1 ORDER BY path",
            str(w.library_id),
        )
    )


async def _copies(db: Database) -> list[tuple[str, str]]:
    return [
        (str(r["video_id"]), str(r["path"]))
        for r in await db.fetch("SELECT video_id, path FROM video_copies ORDER BY path")
    ]


def _write(root: Path, rel: str, data: bytes) -> Path:
    p = root / rel
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_bytes(data)
    return p


DATA = b"lecture one " * 4000


@pytest.mark.asyncio
async def test_two_identical_files_are_one_video_and_one_copy(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path)
    a = _write(w.root, "a/Lecture.mkv", DATA)
    b = _write(w.root, "b/Lecture (copy).mkv", DATA)
    res = await _scan(db, w)
    assert res.files_inserted == 1 and res.files_skipped == 1
    vids = await _videos(db, w)
    assert len(vids) == 1
    vid = str(vids[0]["id"])
    own = str(vids[0]["path"])
    copies = await _copies(db)
    assert len(copies) == 1 and copies[0][0] == vid
    assert {own, copies[0][1]} == {str(a), str(b)}

    # scanning again changes nothing: the row keeps its path, the copy stays one row
    again = await _scan(db, w)
    assert again.files_inserted == 0
    assert [str(r["path"]) for r in await _videos(db, w)] == [own]
    assert await _copies(db) == copies


@pytest.mark.asyncio
async def test_removing_the_copy_keeps_the_video(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path)
    _write(w.root, "a/Lecture.mkv", DATA)
    _write(w.root, "b/Lecture (copy).mkv", DATA)
    await _scan(db, w)
    vid = str((await _videos(db, w))[0]["id"])
    copy = (await _copies(db))[0][1]
    Path(copy).unlink()
    await _scan(db, w)
    rep = await reconcile_copies(db, w.library_id)
    assert rep.forgotten == [(UUID(vid), copy)] and rep.promoted == []
    vids = await _videos(db, w)
    assert [str(v["id"]) for v in vids] == [vid] and Path(str(vids[0]["path"])).is_file()
    assert await _copies(db) == []


@pytest.mark.asyncio
async def test_removing_the_rows_own_file_promotes_the_copy(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path)
    _write(w.root, "a/Lecture.mkv", DATA)
    _write(w.root, "b/Lecture (copy).mkv", DATA)
    await _scan(db, w)
    before = (await _videos(db, w))[0]
    copy = (await _copies(db))[0][1]
    Path(str(before["path"])).unlink()
    await _scan(db, w)
    # the scan moves the row when it hashes the copy again; else the reconcile does
    rep = await reconcile_copies(db, w.library_id)
    assert rep.promoted in ([], [(UUID(str(before["id"])), copy)])
    after = await _videos(db, w)
    assert len(after) == 1 and str(after[0]["id"]) == str(before["id"])
    assert str(after[0]["path"]) == copy
    assert await _copies(db) == []
    await w.assert_tracked(UUID(str(before["id"])))


@pytest.mark.asyncio
async def test_a_moved_file_moves_the_row_and_is_no_copy(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path)
    a = _write(w.root, "a/Lecture.mkv", DATA)
    await _scan(db, w)
    vid = str((await _videos(db, w))[0]["id"])
    dest = w.root / "moved" / "Lecture.mkv"
    dest.parent.mkdir()
    shutil.move(a, dest)
    await _scan(db, w)
    vids = await _videos(db, w)
    assert [(str(v["id"]), str(v["path"])) for v in vids] == [(vid, str(dest))]
    assert await _copies(db) == []


@pytest.mark.asyncio
async def test_an_unmounted_root_forgets_nothing(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path)
    _write(w.root, "a/Lecture.mkv", DATA)
    _write(w.root, "b/Lecture (copy).mkv", DATA)
    await _scan(db, w)
    copies = await _copies(db)
    gone = tmp_path / "unmounted"
    w.root.rename(gone)  # the share is gone: its files say nothing
    rep = await reconcile_copies(db, w.library_id)
    assert rep.forgotten == [] and rep.promoted == []
    assert await _copies(db) == copies


@pytest.mark.asyncio
async def test_a_superseded_row_is_not_promoted(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path)
    _write(w.root, "a/Lecture.mkv", DATA)
    _write(w.root, "b/Lecture (copy).mkv", DATA)
    await _scan(db, w)
    v = (await _videos(db, w))[0]
    await db.execute("UPDATE videos SET state = 'superseded' WHERE id = $1", str(v["id"]))
    Path(str(v["path"])).unlink()
    assert await promote_copy(db, str(v["id"])) is None
    rep = await reconcile_copies(db, w.library_id)
    assert rep.promoted == []


@pytest.mark.asyncio
async def test_a_missing_file_with_a_copy_is_healed(db: Database, tmp_path: Path) -> None:
    """Epic 40's confirm_missing: a file_missing failure whose video has a copy on disk
    moves the row to the copy instead of confirming the file missing."""
    w = await make_world(db, tmp_path)
    _write(w.root, "a/Lecture.mkv", DATA)
    _write(w.root, "b/Lecture (copy).mkv", DATA)
    await _scan(db, w)
    v = (await _videos(db, w))[0]
    copy = (await _copies(db))[0][1]
    Path(str(v["path"])).unlink()
    await db.execute(
        "INSERT INTO processing_jobs (subject_kind, subject_id, video_id, library_id, stage,"
        " state, error_cause) VALUES ('video', $1, $1, $2, 'probe', 'failed', 'file_missing')",
        str(v["id"]),
        str(w.library_id),
    )
    rep = await confirm_missing(db, w.library_id)
    assert rep.checked and rep.healed == 1 and rep.confirmed == 0
    row = await db.fetchrow("SELECT path FROM videos WHERE id = $1", str(v["id"]))
    assert str(row["path"]) == copy


@pytest.mark.asyncio
async def test_a_converted_videos_old_copy_becomes_a_video(db: Database, tmp_path: Path) -> None:
    """Story 47.4 swaps a converted file into the row (new bytes, same id): its copies
    of the old bytes are no copies any more; the next scan makes each a video."""
    from maktaba_pipeline.identity.hasher import hash_file

    w = await make_world(db, tmp_path)
    _write(w.root, "a/Lecture.mkv", DATA)
    _write(w.root, "b/Lecture (copy).mkv", DATA)
    await _scan(db, w)
    v = (await _videos(db, w))[0]
    copy = (await _copies(db))[0][1]
    own = Path(str(v["path"]))
    mp4 = own.with_suffix(".mp4")
    own.unlink()
    mp4.write_bytes(b"converted " * 3000)
    h = hash_file(mp4)
    await db.execute(
        "UPDATE videos SET path = $2, filename = $3, content_hash = $4, size_bytes = $5,"
        " full_hash = NULL WHERE id = $1",
        str(v["id"]),
        str(mp4),
        mp4.name,
        h.content_hash,
        h.size_bytes,
    )
    res = await _scan(db, w)
    assert res.files_inserted == 1
    assert await _copies(db) == []
    paths = {str(r["path"]) for r in await _videos(db, w)}
    assert paths == {str(mp4), copy}
