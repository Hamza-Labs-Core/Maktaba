"""A video a scan finds gets its poster, its small thumbnail and its transcript's turn (Epic 59).

The poster (``thumbnail``), the small copy (``thumb_small``, which falls back to the poster)
and ``transcribe`` follow ``content_hash``, and only the mediaintel import ever queued
``content_hash``: on a fresh install a scanned library got no poster at all. Its first probe
now asks for it, once; a later probe (after an MP4 conversion) does not hash the file again.
The reconciler queues it once for each video probed before this release that never had one.

ffprobe and the frame maker are stand-ins; every file lives under ``tmp_path``.
"""

from __future__ import annotations

import sys
from collections.abc import Iterator
from pathlib import Path
from typing import Any
from uuid import UUID

import pytest
from maktaba_fp_thumbnails.stages import thumbnail as thumbnail_stage

from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.media import ProcResult, reset_runner, set_runner
from maktaba_pipeline.engine.reconcile import IDENTITY_SWEEP_WHY, _identity_sweep
from maktaba_pipeline.engine.sql import dumps, is_pg, loads
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.thumbnail import small as small_mod
from maktaba_pipeline.thumbnail.generator import ThumbnailSet
from tests.engine.factory import make_video
from tests.stages.world import core_runtime, drain_core

JPEG = b"\xff\xd8\xff\xe0small\xff\xd9"
CHAIN = {"scan", "probe", "content_hash", "thumb_hash", "thumbnail", "thumb_small"}


@pytest.fixture(autouse=True)
def _media(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> Iterator[None]:
    probe_mod = sys.modules["maktaba_pipeline.audio.probe"]

    async def fake_ffprobe(path: str, **_: Any) -> dict[str, Any]:
        return {
            "streams": [{"index": 0, "codec_type": "video", "codec_name": "h264",
                         "width": 640, "height": 360, "avg_frame_rate": "25/1"}],
            "format": {"format_name": "mov,mp4", "duration": "4.0"},
        }  # fmt: skip

    async def fake_frames(
        src: str, *, duration_sec: float, content_hash: str, chapters: Any = (), **_: Any
    ) -> ThumbnailSet:
        assert duration_sec == 4.0  # the probe ran first: the poster is not taken blind
        d = tmp_path / "cache" / "thumbnails" / content_hash
        d.mkdir(parents=True, exist_ok=True)
        poster, sprite = d / "poster.jpg", d / "sprite.jpg"
        poster.write_bytes(b"poster")
        sprite.write_bytes(b"sprite")
        return ThumbnailSet(poster=poster, sprite=sprite)

    async def runner(args: list[str], timeout: float) -> ProcResult:
        if "image2pipe" in args:  # the small copy, from the poster
            return ProcResult(JPEG, b"", 0)
        return ProcResult(b"", b"unsupported in fake", 1)

    monkeypatch.setattr(probe_mod, "run_ffprobe", fake_ffprobe)
    monkeypatch.setattr(thumbnail_stage, "generate", fake_frames)
    monkeypatch.setattr(small_mod, "ROOT", str(tmp_path / "cache" / "thumbnails"))
    set_runner(runner)
    yield
    reset_runner()


async def _library(db: Database, root: Path) -> UUID:
    lib = UUID(int=5915)
    roots = [str(root)]
    await db.execute(
        "INSERT INTO libraries (id, name, roots) VALUES ($1, 'مكتبة', $2)",
        str(lib),
        roots if is_pg(db) else dumps(roots),
    )
    return lib


async def _jobs(db: Database, stage: str) -> list[Any]:
    return list(
        await db.fetch(
            "SELECT subject_id, state, outcome, output_summary, reasons FROM processing_jobs"
            " WHERE stage = $1 ORDER BY id",
            stage,
        )
    )


@pytest.mark.asyncio
async def test_a_scanned_video_gets_its_poster_and_small_thumbnail(
    db: Database, tmp_path: Path
) -> None:
    if not is_pg(db):
        pytest.skip("the probe's commit is Postgres's (the home's database)")
    root = tmp_path / "Media"
    for name in ("درس الفجر.mp4", "talk.mkv"):
        (root / name).parent.mkdir(parents=True, exist_ok=True)
        (root / name).write_bytes(f"video {name}".encode() * 64)
    rt = await core_runtime(db)
    lib = await _library(db, root)
    await enqueue_job(db, subject_kind="library", subject_id=lib, stage="scan")
    await drain_core(db, rt, only=CHAIN)

    videos = await db.fetch("SELECT id, poster_path FROM videos ORDER BY path")
    assert len(videos) == 2
    hashes = await _jobs(db, "content_hash")
    assert sorted(str(r["subject_id"]) for r in hashes) == sorted(str(v["id"]) for v in videos)
    assert all(r["state"] == "done" for r in hashes), hashes
    for v in videos:
        assert v["poster_path"], v  # the poster is made and saved
    smalls = await _jobs(db, "thumb_small")
    made = {str(r["subject_id"]) for r in smalls if loads(r["output_summary"]).get("small")}
    assert made == {str(v["id"]) for v in videos}, [r["output_summary"] for r in smalls]

    # a later probe (an MP4 conversion asks for one) does not hash the file again
    for v in videos:
        await enqueue_job(
            db, subject_kind="video", subject_id=UUID(str(v["id"])), stage="probe", force=True
        )
    await drain_core(db, rt, only=CHAIN)
    assert len(await _jobs(db, "content_hash")) == 2
    # and nothing is left for the reconciler's sweep
    assert await _identity_sweep(db, rt.graph) == 0


async def _probed(db: Database, lib: UUID, name: str) -> UUID:
    vid = await make_video(db, lib, filename=name)
    await db.execute(
        "INSERT INTO processing_jobs (subject_kind, subject_id, video_id, stage, state,"
        " outcome, stage_version, input_hash, output_hash) VALUES"
        " ('video', $1, $1, 'probe', 'done', 'changed', 1, 'i', 'o')",
        str(vid),
    )
    return vid


@pytest.mark.asyncio
async def test_the_sweep_queues_identity_once_for_videos_probed_before(
    db: Database, tmp_path: Path
) -> None:
    rt = await core_runtime(db)
    lib = await _library(db, tmp_path)
    before = await _probed(db, lib, "before.mp4")  # scanned before this release
    imported = await _probed(db, lib, "imported.mp4")
    await enqueue_job(db, subject_kind="video", subject_id=imported, stage="content_hash")
    gone = await _probed(db, lib, "gone.mp4")
    await db.execute("UPDATE videos SET deleted_at = CURRENT_TIMESTAMP WHERE id = $1", str(gone))
    await make_video(db, lib, filename="never_probed.mp4")

    assert await _identity_sweep(db, rt.graph) == 1
    queued = [r for r in await _jobs(db, "content_hash") if str(r["subject_id"]) == str(before)]
    assert len(queued) == 1 and IDENTITY_SWEEP_WHY in str(queued[0]["reasons"])
    assert await _identity_sweep(db, rt.graph) == 0  # once: it ends by itself
