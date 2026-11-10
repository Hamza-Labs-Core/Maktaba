"""A library that is read-only for Maktaba (Epic 59, story 59.03).

T04: the subtitles stage keeps its files in the database (``subtitle_files.content``) and
writes nothing in the library; a stage that writes the library (convert) is refused, once,
as ``library_read_only``; no scan runs on a library Maktaba cannot list.
"""

from __future__ import annotations

import json
import os
import sys
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.engine import library_access
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.runtime import Database
from tests.stages.test_transcribe import FakeTranscriber, fake  # noqa: F401
from tests.stages.world import core_runtime, drain_core, make_show


async def _one_video(db: Database, tmp_path: Path) -> tuple[Any, Path]:
    """One episode whose file is in its own folder (the database stays writable)."""
    w = await make_show(db, episodes=1, loose=0)
    vid = w.episodes[0]
    folder = tmp_path / "Library"
    folder.mkdir()
    media = folder / "درس السيرة 01.mp4"
    media.write_bytes(b"\x00" * 64)
    await db.execute("UPDATE videos SET path = $2 WHERE id = $1", str(vid), str(media))
    return vid, media


needs_modes = pytest.mark.skipif(
    sys.platform == "win32" or (hasattr(os, "geteuid") and os.geteuid() == 0),
    reason="file modes do not bind this user",
)


async def _detail(db: Database, library: Any) -> dict[str, Any]:
    raw = await db.fetchval("SELECT access_detail FROM libraries WHERE id = $1", str(library))
    return json.loads(raw) if isinstance(raw, str) else dict(raw or {})


@needs_modes
@pytest.mark.asyncio
@pytest.mark.parametrize("access", ["ro", None])
async def test_subtitles_on_a_read_only_library_go_to_the_database(
    db: Database,
    tmp_path: Path,
    fake: FakeTranscriber,  # noqa: F811 - the transcribe tests' fixture
    access: str | None,
) -> None:
    rt = await core_runtime(db)
    vid, media = await _one_video(db, tmp_path)
    lib = await db.fetchval("SELECT library_id FROM videos WHERE id = $1", str(vid))
    await db.execute("UPDATE libraries SET access = $2 WHERE id = $1", lib, access)
    os.chmod(media.parent, 0o555)
    try:
        await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
        ran = await drain_core(db, rt, only={"transcribe", "second_listen", "subtitle_gen"})
    finally:
        os.chmod(media.parent, 0o755)
    assert "subtitle_gen" in ran
    assert sorted(p.name for p in media.parent.iterdir()) == [media.name]  # nothing written
    rows = await db.fetch(
        "SELECT format, content, is_external FROM subtitle_files WHERE video_id = $1"
        " AND deleted_at IS NULL ORDER BY format",
        str(vid),
    )
    assert [r["format"] for r in rows] == ["srt", "vtt"]
    assert "00:00:00,500 --> 00:00:02,250" in rows[0]["content"]
    assert rows[1]["content"].startswith("WEBVTT")
    assert not any(r["is_external"] for r in rows)
    state = await db.fetchval(
        "SELECT state FROM processing_jobs WHERE stage = 'subtitle_gen' ORDER BY id DESC LIMIT 1"
    )
    assert state == "done"
    failures = (await _detail(db, lib)).get("write_failures") or []
    if access == "ro":
        assert failures == []  # what the library's access already says
    else:
        # a library thought writable: the refused write is recorded, and checked again
        assert failures and failures[0]["path"].endswith(".ar.srt")
        assert (await _detail(db, lib)).get("recheck_after")


@pytest.mark.asyncio
async def test_a_writing_stage_is_refused_on_a_read_only_library(
    db: Database, tmp_path: Path
) -> None:
    rt = await core_runtime(db)
    vid, media = await _one_video(db, tmp_path)
    lib = await db.fetchval("SELECT library_id FROM videos WHERE id = $1", str(vid))
    await db.execute("UPDATE libraries SET access = 'ro' WHERE id = $1", lib)
    assert library_access.writes_library("convert_mp4")
    assert not library_access.writes_library("subtitle_gen")
    await enqueue_job(db, subject_kind="library", subject_id=lib, stage="convert_mp4")
    await drain_core(db, rt, only={"convert_mp4"})
    row = await db.fetchrow(
        "SELECT state, error FROM processing_jobs WHERE stage = 'convert_mp4'"
        " ORDER BY id DESC LIMIT 1"
    )
    assert row is not None and row["state"] == "failed" and "library_read_only" in str(row["error"])
    assert media.read_bytes() == b"\x00" * 64


@pytest.mark.asyncio
async def test_no_scan_on_a_library_it_cannot_list(db: Database, tmp_path: Path) -> None:
    rt = await core_runtime(db)
    vid, _ = await _one_video(db, tmp_path)
    lib = await db.fetchval("SELECT library_id FROM videos WHERE id = $1", str(vid))
    await db.execute("UPDATE libraries SET access = 'none' WHERE id = $1", lib)
    assert await library_access.refusal(
        db, stage="scan", subject_kind="library", subject_id=lib
    ) == ("library_no_access", "Maktaba cannot list this library's folders")
    await db.execute("UPDATE libraries SET access = 'ro' WHERE id = $1", lib)
    assert (
        await library_access.refusal(db, stage="scan", subject_kind="library", subject_id=lib)
        is None
    )
    del rt


def test_write_failures_keep_the_newest_five() -> None:
    from datetime import UTC, datetime

    doc: dict[str, Any] = {"roots": [{"path": "/m"}]}
    for i in range(7):
        doc = library_access._merge_failure(doc, f"/m/{i}.srt", datetime(2026, 11, 10, tzinfo=UTC))
    doc = library_access._merge_failure(
        json.dumps(doc), "/m/3.srt", datetime(2026, 11, 11, tzinfo=UTC)
    )
    assert [f["path"] for f in doc["write_failures"]] == [
        "/m/3.srt",
        "/m/6.srt",
        "/m/5.srt",
        "/m/4.srt",
        "/m/2.srt",
    ]
    assert doc["roots"] == [{"path": "/m"}] and doc["recheck_after"].startswith("2026-11-11")
