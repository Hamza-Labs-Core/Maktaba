"""THUMBNAIL stage adapter (Story 7.7).

Drives :func:`maktaba_pipeline.thumbnail.handler.thumbnail_handler`
against the shared ``StageDB`` fake (extended with the duration +
chapters + poster/sprite columns this stage touches) and a fake
generator injected via the ``run_generate`` seam, so no ffmpeg spawns.

Like the other handler tests these are NOT ``unit``-marked: ``asyncio.run``
opens the event loop self-pipe the unit-tier netguard forbids.
"""

from __future__ import annotations

import asyncio
from pathlib import Path
from typing import Any
from uuid import UUID, uuid4

from maktaba_pipeline.db.jobs import Stage
from maktaba_pipeline.thumbnail.generator import ThumbnailError, ThumbnailSet
from maktaba_pipeline.thumbnail.handler import thumbnail_handler
from tests.audio._fake_audio_db import _ProcessingJobRow, _Row

from ..pipeline.conftest import StageDB, make_job


class ThumbDB(StageDB):
    """StageDB + ``duration_sec``, chapters, and poster/sprite columns."""

    def __init__(self, *args: Any, **kwargs: Any) -> None:
        super().__init__(*args, **kwargs)
        self.video_durations: dict[UUID, float] = {}
        self.chapters: dict[UUID, list[tuple[int, float]]] = {}
        self.poster_paths: dict[UUID, str] = {}
        self.sprite_paths: dict[UUID, str] = {}

    def add_video(
        self,
        *,
        state: str = "indexed",
        library_id: UUID | None = None,
        path: str = "/library/clip.mkv",
        content_hash: str = "a" * 64,
        duration_sec: float = 400.0,
        chapters: list[tuple[int, float]] | None = None,
    ) -> UUID:
        vid = super().add_video(
            state=state, library_id=library_id, path=path, content_hash=content_hash
        )
        self.video_durations[vid] = duration_sec
        self.chapters[vid] = chapters or []
        return vid

    def _dispatch(self, s: str, args: tuple[Any, ...], *, many: bool) -> Any:
        # Source projection with duration — must precede StageDB's
        # narrower "SELECT path, content_hash FROM videos" match.
        if s.startswith("SELECT path, content_hash, duration_sec FROM videos"):
            vid = args[0]
            path = self.video_paths.get(vid)
            chash = self.video_hashes.get(vid)
            if path is None or chash is None:
                return None
            return _Row(
                {"path": path, "content_hash": chash, "duration_sec": self.video_durations.get(vid)}
            )

        if s.startswith("SELECT seq, start_sec FROM chapters"):
            vid = args[0]
            return [
                _Row({"seq": seq, "start_sec": start})
                for seq, start in self.chapters.get(vid, [])
            ]

        if s.startswith("UPDATE videos SET poster_path"):
            vid = args[0]
            self.poster_paths[vid] = args[1]
            self.sprite_paths[vid] = args[2]
            return None

        return super()._dispatch(s, args, many=many)


def _seed_claimed_job(db: ThumbDB, *, job_id: int, video_id: UUID) -> None:
    db.processing_jobs[job_id] = _ProcessingJobRow(
        id=job_id, video_id=video_id, stage=Stage.THUMBNAIL.value, state="running"
    )
    db._job_next_id = max(db._job_next_id, job_id + 1)  # noqa: SLF001


def _fake_set(content_hash: str, chapters: list[tuple[int, float]]) -> ThumbnailSet:
    base = Path("/cache/thumbnails") / content_hash
    return ThumbnailSet(
        poster=base / "poster.jpg",
        sprite=base / "sprite.jpg",
        chapter_thumbs={seq: base / f"chapter-{seq}.jpg" for seq, _ in chapters},
    )


def test_thumbnail_handler_persists_and_advances() -> None:
    db = ThumbDB(dialect="postgres")
    chapters = [(0, 0.0), (1, 120.0)]
    video_id = db.add_video(
        state="indexed", path="/lib/movie.mkv", content_hash="c" * 64, chapters=chapters
    )
    job = make_job(job_id=1, video_id=video_id, stage=Stage.THUMBNAIL)
    _seed_claimed_job(db, job_id=1, video_id=video_id)

    seen: dict[str, Any] = {}

    async def fake_generate(
        src: str, *, duration_sec: float, content_hash: str, chapters: Any
    ) -> ThumbnailSet:
        seen["src"] = src
        seen["duration_sec"] = duration_sec
        seen["chapters"] = list(chapters)
        return _fake_set(content_hash, list(chapters))

    asyncio.run(thumbnail_handler(db, job, run_generate=fake_generate))

    # The generator was driven against the resolved source + duration +
    # chapter starts.
    assert seen["src"] == "/lib/movie.mkv"
    assert seen["duration_sec"] == 400.0
    assert seen["chapters"] == [(0, 0.0), (1, 120.0)]
    # poster/sprite persisted on the video.
    assert db.poster_paths[video_id].endswith("poster.jpg")
    assert db.sprite_paths[video_id].endswith("sprite.jpg")
    # FSM advanced INDEXED -> THUMBNAILED.
    assert db.videos[video_id].state == "thumbnailed"
    # the job itself is done.
    assert db.processing_jobs[1].state == "done"


def test_thumbnail_handler_no_chapters_still_succeeds() -> None:
    db = ThumbDB(dialect="postgres")
    video_id = db.add_video(state="indexed", chapters=[])
    job = make_job(job_id=2, video_id=video_id, stage=Stage.THUMBNAIL)
    _seed_claimed_job(db, job_id=2, video_id=video_id)

    async def fake_generate(
        src: str, *, duration_sec: float, content_hash: str, chapters: Any
    ) -> ThumbnailSet:
        assert list(chapters) == []
        return _fake_set(content_hash, [])

    asyncio.run(thumbnail_handler(db, job, run_generate=fake_generate))
    assert db.videos[video_id].state == "thumbnailed"
    assert db.processing_jobs[2].state == "done"


def test_thumbnail_handler_missing_source_fails_terminally() -> None:
    db = ThumbDB(dialect="postgres")
    video_id = uuid4()  # no videos row
    job = make_job(job_id=3, video_id=video_id, stage=Stage.THUMBNAIL)
    _seed_claimed_job(db, job_id=3, video_id=video_id)

    async def fake_generate(*_a: Any, **_k: Any) -> ThumbnailSet:  # pragma: no cover
        raise AssertionError("generator must not run with no source")

    asyncio.run(thumbnail_handler(db, job, run_generate=fake_generate))
    assert db.processing_jobs[3].state == "failed"


def test_thumbnail_handler_ffmpeg_failure_is_retryable() -> None:
    db = ThumbDB(dialect="postgres")
    video_id = db.add_video(state="indexed")
    job = make_job(job_id=4, video_id=video_id, stage=Stage.THUMBNAIL)
    _seed_claimed_job(db, job_id=4, video_id=video_id)

    async def fake_generate(*_a: Any, **_k: Any) -> ThumbnailSet:
        raise ThumbnailError("ffmpeg_thumbnail", returncode=1, stderr_tail="boom")

    asyncio.run(thumbnail_handler(db, job, run_generate=fake_generate))
    # A transient ffmpeg failure does not advance the FSM.
    assert db.videos[video_id].state == "indexed"
    assert db.processing_jobs[4].state in {"pending", "failed"}


def test_thumbnail_handler_replay_after_thumbnailed_is_noop_advance() -> None:
    # Video already past INDEXED (a retried job after the first run
    # advanced it): commit must not try the INDEXED->THUMBNAILED edge.
    db = ThumbDB(dialect="postgres")
    video_id = db.add_video(state="thumbnailed")
    job = make_job(job_id=5, video_id=video_id, stage=Stage.THUMBNAIL)
    _seed_claimed_job(db, job_id=5, video_id=video_id)

    async def fake_generate(
        src: str, *, duration_sec: float, content_hash: str, chapters: Any
    ) -> ThumbnailSet:
        return _fake_set(content_hash, [])

    asyncio.run(thumbnail_handler(db, job, run_generate=fake_generate))
    # State unchanged (no illegal re-advance), job still completes.
    assert db.videos[video_id].state == "thumbnailed"
    assert db.processing_jobs[5].state == "done"
