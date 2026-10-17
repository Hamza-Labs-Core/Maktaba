"""thumb_small (the app's small thumbnail cache) and review_pages (one page per library)."""

from __future__ import annotations

from collections.abc import Iterator
from pathlib import Path
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.engine.claim import claim_for_pool
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import GRAPH, POOLS
from maktaba_pipeline.engine.media import ProcResult, reset_runner, set_runner
from maktaba_pipeline.engine.sql import loads
from maktaba_pipeline.engine.stages import review_pages, thumb_small
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_series_and_season, make_video
from tests.extensions.harness import add_episode
from tests.stages.world import core_runtime, drain_core

JPEG = b"\xff\xd8\xff\xe0fake-jpeg\xff\xd9"


@pytest.fixture(autouse=True)
def _ffmpeg() -> Iterator[dict[str, int]]:
    calls = {"n": 0}

    async def runner(args: list[str], timeout: float) -> ProcResult:
        calls["n"] += 1
        if "image2pipe" in args and f"scale={thumb_small.WIDTH}:-2" in args:
            return ProcResult(JPEG, b"", 0)
        return ProcResult(b"", b"unsupported in fake", 1)

    set_runner(runner)
    yield calls
    reset_runner()


async def _rows(db: Database, stage: str) -> list[Any]:
    return list(await db.fetch("SELECT * FROM processing_jobs WHERE stage = $1 ORDER BY id", stage))


async def _hash(db: Database, v: UUID) -> str:
    return str(await db.fetchval("SELECT content_hash FROM videos WHERE id = $1", str(v)))


# ------------------------------------------------------------------ graph


@pytest.mark.unit
def test_review_pages_has_its_own_pool_and_nothing_waits_for_it() -> None:
    by = {s.name: s for s in GRAPH}
    rp, ts = by["review_pages"], by["thumb_small"]
    assert rp.pool == "reports" and rp.edges == () and rp.entry_for == ("library",)
    assert "reports" in {p.pool for p in POOLS}
    assert not any(s.pool == "reports" for s in GRAPH if s.name != "review_pages")
    assert not any("review_pages" in s.may_request for s in GRAPH)
    assert ts.subject_kinds == ("video",) and ts.pool == "nas_read"
    assert ("review_pages", "library_of") in {(e.to, e.map) for e in ts.edges}
    for src in ("thumbnail", "thumb_hash"):
        assert ("thumb_small", "same") in {(e.to, e.map) for e in by[src].edges}
    assert ("review_pages", "same") in {(e.to, e.map) for e in by["detect"].edges}


# ------------------------------------------------------------------ thumb_small


@pytest.mark.asyncio
async def test_thumb_small_copies_the_sidecar_once(
    db: Database, tmp_path: Path, _ffmpeg: dict[str, int]
) -> None:
    rt = await core_runtime(db)
    lib = await make_library(db)
    (tmp_path / "e1.mp4").write_bytes(b"v")
    (tmp_path / "e1.jpg").write_bytes(b"big")
    v = await make_video(db, lib, filename="e1.mp4", path=str(tmp_path / "e1.mp4"))
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="thumb_small")
    await drain_core(db, rt, only={"thumb_small"})
    dst = thumb_small.small_path(await _hash(db, v))
    assert dst.read_bytes() == JPEG and dst.name == "small.jpg"
    assert dst.parent.name == await _hash(db, v)  # the same cache folder as the poster
    assert not list(dst.parent.glob("*.part.jpg"))
    job = (await _rows(db, "thumb_small"))[0]
    assert job["state"] == "done" and loads(job["output_summary"])["source"] == "e1.jpg"
    # unchanged video: skipped, ffmpeg not run again
    n = _ffmpeg["n"]
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="thumb_small")
    await drain_core(db, rt, only={"thumb_small"})
    assert (await _rows(db, "thumb_small"))[-1]["outcome"] == "skipped_unchanged"
    assert _ffmpeg["n"] == n


@pytest.mark.asyncio
async def test_thumb_small_adopts_a_copy_already_on_disk(
    db: Database, tmp_path: Path, _ffmpeg: dict[str, int]
) -> None:
    rt = await core_runtime(db)
    lib = await make_library(db)
    v = await make_video(db, lib, filename="e1.mp4", path=str(tmp_path / "e1.mp4"))
    dst = thumb_small.small_path(await _hash(db, v))
    dst.parent.mkdir(parents=True)
    dst.write_bytes(JPEG)
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="thumb_small")
    await drain_core(db, rt, only={"thumb_small"})
    job = (await _rows(db, "thumb_small"))[0]
    assert job["state"] == "done" and loads(job["output_summary"])["adopted"] is True
    assert _ffmpeg["n"] == 0  # no share read


@pytest.mark.asyncio
async def test_thumb_small_without_a_thumbnail_and_with_an_unreadable_one(
    db: Database, tmp_path: Path
) -> None:
    rt = await core_runtime(db)
    lib = await make_library(db)
    (tmp_path / "a.mp4").write_bytes(b"v")
    a = await make_video(db, lib, filename="a.mp4", path=str(tmp_path / "a.mp4"))
    await enqueue_job(db, subject_kind="video", subject_id=a, stage="thumb_small")
    await drain_core(db, rt, only={"thumb_small"})
    first = (await _rows(db, "thumb_small"))[0]
    assert loads(first["output_summary"])["reason"] == "no_thumbnail"

    async def bad(args: list[str], timeout: float) -> ProcResult:
        return ProcResult(b"", b"Invalid data found when processing input", 1)

    set_runner(bad)
    (tmp_path / "b.mp4").write_bytes(b"v")
    (tmp_path / "b.webp").write_bytes(b"junk")
    b = await make_video(db, lib, filename="b.mp4", path=str(tmp_path / "b.mp4"))
    await enqueue_job(db, subject_kind="video", subject_id=b, stage="thumb_small")
    await drain_core(db, rt, only={"thumb_small"}, max_jobs=1)
    j = (await _rows(db, "thumb_small"))[-1]
    assert j["state"] in ("pending", "failed") and j["error_cause"] == "ffmpeg_decode"
    assert not thumb_small.small_path(await _hash(db, b)).exists()


# ------------------------------------------------------------------ review_pages


async def _library_with_season(db: Database, tmp_path: Path) -> tuple[UUID, list[UUID]]:
    lib = await make_library(db, "قناة الاختبار")
    series, _season = await make_series_and_season(db, lib, name="كنوز السيرة")
    vids = []
    for i in (1, 2):
        (tmp_path / f"e{i}.mp4").write_bytes(b"v")
        v = await make_video(
            db, lib, filename=f"e{i}.mp4", path=str(tmp_path / f"e{i}.mp4"), title=f"الحلقة {i} <b>"
        )
        await add_episode(db, series, v, episode=i)
        vids.append(v)
    (tmp_path / "e1.jpg").write_bytes(b"big")  # only the first has a thumbnail
    return lib, vids


@pytest.mark.asyncio
async def test_review_page_links_small_thumbnails_relatively(db: Database, tmp_path: Path) -> None:
    rt = await core_runtime(db)
    lib, vids = await _library_with_season(db, tmp_path)
    for v in vids:
        await enqueue_job(db, subject_kind="video", subject_id=v, stage="thumb_small")
    await drain_core(db, rt, only={"thumb_small"})
    # thumb_small -> review_pages (library_of) queued the library's page
    assert {str(r["subject_id"]) for r in await _rows(db, "review_pages")} == {str(lib)}
    await drain_core(db, rt, only={"review_pages"})
    page = review_pages.page_path(lib)
    text = page.read_text(encoding="utf-8")
    h1 = await _hash(db, vids[0])
    assert f'src="../thumbnails/{h1}/small.jpg"' in text
    assert (page.parent / f"../thumbnails/{h1}/small.jpg").resolve().is_file()
    assert "الحلقة 1 &lt;b&gt;" in text and "<b>1</b>" in text  # escaped titles, numbered
    assert text.count('class="none"') == 1  # the second episode has no thumbnail
    job = (await _rows(db, "review_pages"))[-1]
    s = loads(job["output_summary"])
    assert (s["seasons"], s["episodes"], s["with_thumbnail"]) == (1, 2, 1)


@pytest.mark.asyncio
async def test_review_page_is_skipped_until_seasons_or_thumbnails_move(
    db: Database, tmp_path: Path
) -> None:
    rt = await core_runtime(db)
    lib, vids = await _library_with_season(db, tmp_path)
    await enqueue_job(db, subject_kind="library", subject_id=lib, stage="review_pages")
    await drain_core(db, rt, only={"review_pages"})
    await enqueue_job(db, subject_kind="library", subject_id=lib, stage="review_pages")
    await drain_core(db, rt, only={"review_pages"})
    assert (await _rows(db, "review_pages"))[-1]["outcome"] == "skipped_unchanged"
    await enqueue_job(db, subject_kind="video", subject_id=vids[0], stage="thumb_small")
    await drain_core(db, rt, only={"thumb_small", "review_pages"})
    last = (await _rows(db, "review_pages"))[-1]
    assert last["state"] == "done" and last["outcome"] != "skipped_unchanged"


@pytest.mark.asyncio
async def test_a_running_review_page_never_holds_a_slot_writes_need(db: Database) -> None:
    await core_runtime(db)
    lib = await make_library(db)
    await enqueue_job(db, subject_kind="library", subject_id=lib, stage="review_pages")
    await enqueue_job(db, subject_kind="library", subject_id=lib, stage="detect")
    await db.execute("UPDATE processing_jobs SET not_before = NULL")
    page = await claim_for_pool(db, worker_id="w", pool="reports", stages=["review_pages"])
    assert page is not None and page.stage == "review_pages"
    # with the page job claimed, the cpu pool still hands out detect
    other = await claim_for_pool(
        db, worker_id="w", pool="cpu", stages=["detect"], scope="per_worker"
    )
    assert other is not None and other.stage == "detect"
