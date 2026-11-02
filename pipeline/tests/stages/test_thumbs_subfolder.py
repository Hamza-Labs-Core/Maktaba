"""A video's downloaded thumbnail in its folder's ``.thumbs/`` (2026-11).

The owner's NAS keeps the thumbnails of 69 channel folders in a hidden subfolder
(``<channel>/.thumbs/<stem>.jpg`` beside ``<channel>/<stem>.mp4``). One rule finds a
video's own image (:func:`find_sidecar_thumbnail`): next to the video first, then in
``.thumbs/``. ``thumb_hash`` and ``thumb_small`` use it and fingerprint a ``.thumbs/``
find, and the reconciler queues the videos answered from the poster once, so the ones
already processed pick their thumbnail up. Every file lives under ``tmp_path``.
"""

from __future__ import annotations

import hashlib
from collections.abc import Iterator
from pathlib import Path
from typing import Any, cast
from uuid import UUID

import pytest

from maktaba_pipeline.discovery.ytdlp_sidecars import THUMBS_DIR, find_sidecar_thumbnail
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import THUMB_SMALL_VERSION, Subject
from maktaba_pipeline.engine.hashing import canonical_hash
from maktaba_pipeline.engine.media import ProcResult, reset_runner, set_runner
from maktaba_pipeline.engine.reconcile import _thumbs_dir_sweep
from maktaba_pipeline.engine.sql import dumps, loads
from maktaba_pipeline.engine.stages import thumb_hash, thumb_small
from maktaba_pipeline.engine.stages._common import load_video
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.scanner.walker import DEFAULT_IGNORE_DIRNAMES
from maktaba_pipeline.thumbnail.dhash import DHASH_VERSION
from maktaba_pipeline.watcher.service import LibraryObserver, WatcherConfig
from tests.engine.factory import make_library, make_video
from tests.stages.world import core_runtime, drain_core

CHANNEL = "Abdul Basit- Mojawwad"
STEM = "Abasa： 80"  # a fullwidth colon, as yt-dlp writes a title's ":"
ARABIC = "سورة الأعلى： ٨٧"
JPEG = b"\xff\xd8\xff\xe0fake-jpeg\xff\xd9"


@pytest.fixture(autouse=True)
def _ffmpeg() -> Iterator[list[str]]:
    """ffmpeg stand-in: the image each call read, a gray 9x8 frame that depends on it
    (so the poster and the sidecar hash apart) and a small JPEG."""
    inputs: list[str] = []

    async def runner(args: list[str], timeout: float) -> ProcResult:
        src = args[args.index("-i") + 1]
        inputs.append(src)
        if "rawvideo" in args:
            return ProcResult((hashlib.sha256(src.encode()).digest() * 3)[:72], b"", 0)
        if "image2pipe" in args:
            return ProcResult(JPEG + src.encode(), b"", 0)
        return ProcResult(b"", b"unsupported in fake", 1)

    set_runner(runner)
    yield inputs
    reset_runner()


def _touch(p: Path, data: bytes = b"x") -> Path:
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_bytes(data)
    return p


def _channel(tmp_path: Path) -> Path:
    return tmp_path / CHANNEL


# ------------------------------------------------------------------ the one rule


@pytest.mark.unit
@pytest.mark.parametrize("stem", [STEM, ARABIC])
def test_a_sidecar_next_to_the_video_is_found(tmp_path: Path, stem: str) -> None:
    video = _touch(_channel(tmp_path) / f"{stem}.mp4")
    side = _touch(_channel(tmp_path) / f"{stem}.webp")
    assert find_sidecar_thumbnail(str(video)) == str(side)
    assert thumb_hash.find_thumbnail(str(video), None) == str(side)


@pytest.mark.unit
@pytest.mark.parametrize("stem", [STEM, ARABIC])
def test_a_sidecar_in_thumbs_is_found(tmp_path: Path, stem: str) -> None:
    video = _touch(_channel(tmp_path) / f"{stem}.mp4")
    side = _touch(_channel(tmp_path) / THUMBS_DIR / f"{stem}.jpg")
    _touch(_channel(tmp_path) / THUMBS_DIR / "another video.jpg")
    assert find_sidecar_thumbnail(str(video)) == str(side)
    poster = _touch(tmp_path / "cache" / "poster.jpg")
    assert thumb_hash.find_thumbnail(str(video), str(poster)) == str(side)


@pytest.mark.unit
def test_the_sidecar_next_to_the_video_wins_over_thumbs(tmp_path: Path) -> None:
    video = _touch(_channel(tmp_path) / f"{STEM}.mp4")
    _touch(_channel(tmp_path) / THUMBS_DIR / f"{STEM}.webp")
    beside = _touch(_channel(tmp_path) / f"{STEM}.png")
    assert find_sidecar_thumbnail(str(video)) == str(beside)


@pytest.mark.unit
def test_without_a_sidecar_the_poster_is_used(tmp_path: Path) -> None:
    video = _touch(_channel(tmp_path) / f"{STEM}.mp4")
    _touch(_channel(tmp_path) / THUMBS_DIR / "Abasa： 81.jpg")  # another video's
    _touch(_channel(tmp_path) / THUMBS_DIR / f"{STEM}.mp4.jpg")  # not this rule's name
    poster = _touch(tmp_path / "cache" / "poster.jpg")
    assert find_sidecar_thumbnail(str(video)) is None
    assert thumb_hash.find_thumbnail(str(video), str(poster)) == str(poster)
    assert thumb_hash.find_thumbnail(str(video), str(tmp_path / "gone.jpg")) is None


# ------------------------------------------------------------------ fingerprints


async def _video(db: Database, tmp_path: Path, stem: str = STEM) -> tuple[UUID, Path]:
    lib = await make_library(db)
    video = _touch(_channel(tmp_path) / f"{stem}.mp4", b"video")
    vid = await make_video(db, lib, filename=video.name, path=str(video))
    return vid, video


async def _legacy_fingerprint(db: Database, vid: UUID, version: int) -> str:
    """The fingerprint as both stages computed it before 2026-11."""
    v = await load_video(db, vid)
    assert v is not None
    return canonical_hash(
        {"size": v.size_bytes, "mtime": v.mtime, "poster": v.poster_path, "v": version}
    )


@pytest.mark.asyncio
async def test_a_thumbs_image_appearing_gives_a_new_fingerprint(
    db: Database, tmp_path: Path
) -> None:
    vid, video = await _video(db, tmp_path)
    subject = Subject("video", vid)
    before_hash = await thumb_hash.input_fingerprint(db, subject)
    before_small = await thumb_small.input_fingerprint(db, subject)
    # nothing found: exactly the fingerprint of before, so done jobs stay done
    assert before_hash == await _legacy_fingerprint(db, vid, DHASH_VERSION)
    assert before_small == await _legacy_fingerprint(db, vid, THUMB_SMALL_VERSION)

    _touch(video.parent / THUMBS_DIR / f"{STEM}.jpg")
    after_hash = await thumb_hash.input_fingerprint(db, subject)
    after_small = await thumb_small.input_fingerprint(db, subject)
    assert after_hash != before_hash and after_small != before_small

    # a sidecar next to the video answers as before (it wins): the old fingerprint
    _touch(video.with_suffix(".webp"))
    assert await thumb_hash.input_fingerprint(db, subject) == before_hash
    assert await thumb_small.input_fingerprint(db, subject) == before_small


# ------------------------------------------------------------------ already processed


async def _rows(db: Database, stage: str) -> list[Any]:
    return list(await db.fetch("SELECT * FROM processing_jobs WHERE stage = $1 ORDER BY id", stage))


async def _with_poster(db: Database, vid: UUID) -> Path:
    content_hash = str(await db.fetchval("SELECT content_hash FROM videos WHERE id = $1", str(vid)))
    poster = _touch(thumb_small.small_path(content_hash).parent / "poster.jpg", b"poster")
    await db.execute("UPDATE videos SET poster_path = $2 WHERE id = $1", str(vid), str(poster))
    return poster


async def _as_before_the_release(db: Database) -> None:
    """The thumb_hash results as the old rule recorded them (no ``thumbs_dir``)."""
    for r in await _rows(db, "thumb_hash"):
        summary = loads(r["output_summary"])
        if isinstance(summary, dict) and summary.pop("thumbs_dir", None):
            await db.execute(
                "UPDATE processing_jobs SET output_summary = $2 WHERE id = $1",
                int(r["id"]),
                dumps(summary),
            )


@pytest.mark.asyncio
async def test_a_video_answered_from_the_poster_picks_up_its_thumbs_image(
    db: Database, tmp_path: Path, _ffmpeg: list[str]
) -> None:
    rt = await core_runtime(db)
    vid, video = await _video(db, tmp_path)
    poster = await _with_poster(db, vid)
    plain, _ = await _video(db, tmp_path, stem=ARABIC)  # no thumbnail of its own, ever
    await _with_poster(db, plain)
    for v in (vid, plain):
        await enqueue_job(db, subject_kind="video", subject_id=v, stage="thumb_hash")
    await drain_core(db, rt, only={"thumb_hash", "thumb_small"})
    first = {str(r["subject_id"]): r for r in await _rows(db, "thumb_hash")}
    assert loads(first[str(vid)]["output_summary"])["source"] == "poster.jpg"
    small = thumb_small.small_path(
        str(await db.fetchval("SELECT content_hash FROM videos WHERE id = $1", str(vid)))
    )
    assert small.read_bytes() == JPEG + str(poster).encode()

    # the thumbnail was in .thumbs/ all along; this release looks there
    side = _touch(video.parent / THUMBS_DIR / f"{STEM}.jpg", b"own")
    assert await _thumbs_dir_sweep(db, rt.graph) == 0  # results of this release: left alone
    await _as_before_the_release(db)
    assert await _thumbs_dir_sweep(db, rt.graph) == 2
    assert await _thumbs_dir_sweep(db, rt.graph) == 0  # queued already
    _ffmpeg.clear()
    await drain_core(db, rt, only={"thumb_hash", "thumb_small"})

    hashes = await _rows(db, "thumb_hash")
    mine = [r for r in hashes if str(r["subject_id"]) == str(vid)][-1]
    assert mine["outcome"] == "changed"
    assert loads(mine["output_summary"])["source"] == f"{STEM}.jpg"
    other = [r for r in hashes if str(r["subject_id"]) == str(plain)][-1]
    assert other["outcome"] == "skipped_unchanged"  # a few stats, no read
    # the dhash changed, so thumb_small ran: it made a new copy from the sidecar rather
    # than adopting the one it had made from the poster
    last_small = [r for r in await _rows(db, "thumb_small") if str(r["subject_id"]) == str(vid)][-1]
    assert loads(last_small["output_summary"])["source"] == f"{STEM}.jpg"
    assert small.read_bytes() == JPEG + str(side).encode()
    assert _ffmpeg == [str(side), str(side)]  # hashed and copied, nothing else read

    # each video is queued once by the sweep: it ends by itself
    assert await _thumbs_dir_sweep(db, rt.graph) == 0


@pytest.mark.asyncio
async def test_the_sweep_leaves_imported_and_sidecar_results_alone(
    db: Database, tmp_path: Path
) -> None:
    rt = await core_runtime(db)
    vid, video = await _video(db, tmp_path)
    _touch(video.with_suffix(".jpg"))
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="thumb_hash")
    await drain_core(db, rt, only={"thumb_hash"})
    imported, _ = await _video(db, tmp_path, stem=ARABIC)
    await db.execute(
        "INSERT INTO processing_jobs (subject_kind, subject_id, video_id, stage, state,"
        " outcome, stage_version, input_hash, output_hash) VALUES"
        " ('video', $1, $1, 'thumb_hash', 'done', 'imported', 1, 'i', 'o')",
        str(imported),
    )
    await _as_before_the_release(db)
    assert await _thumbs_dir_sweep(db, rt.graph) == 0


# ------------------------------------------------------------------ the watcher


@pytest.mark.unit
def test_the_watcher_ignores_thumbs_and_synology_index_folders(tmp_path: Path) -> None:
    assert "@eaDir" in DEFAULT_IGNORE_DIRNAMES
    obs = LibraryObserver(
        library_id=UUID(int=1),
        roots=[str(tmp_path)],
        observer=cast(Any, None),
        debouncer=cast(Any, None),
        cfg=WatcherConfig(),
        log=cast(Any, None),
    )
    chan = _channel(tmp_path)
    assert obs._is_eligible(str(chan / f"{STEM}.mp4"), allow_no_ext=False)
    for p in (
        chan / THUMBS_DIR / f"{STEM}.mp4",
        chan / "@eaDir" / f"{STEM}.mp4" / "x.mp4",
        chan / THUMBS_DIR / f"{STEM}.jpg",
    ):
        assert not obs._is_eligible(str(p), allow_no_ext=True), p
