"""Story 47.4: the ``convert_mp4`` stage, end to end through the engine (claim, dispatch,
failure policy) with real media made by ``ffmpeg -f lavfi`` and a real library on disk.

Each converted fixture keeps its ``videos.id``, has its original in the trash, a ``done``
ledger row and its transcript untouched. Both dialects (``db`` fixture)."""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import uuid
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.claim import claim_for_pool
from maktaba_pipeline.engine.context import StageContext, StageFailure
from maktaba_pipeline.engine.dispatch import EngineRuntime, dispatch_job
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import EPIC47_PRIORITIES, bound_graph, get_stage, validate
from maktaba_pipeline.engine.sql import dumps, is_pg, loads, uid
from maktaba_pipeline.engine.stages import convert_mp4, thumb_small
from maktaba_pipeline.engine.sync import sync_graph
from maktaba_pipeline.library_mgmt import convert as cv
from maktaba_pipeline.library_mgmt.ledger import list_actions
from maktaba_pipeline.library_mgmt.verify import moov_before_mdat, probe
from maktaba_pipeline.runtime import Database
from tests.library_mgmt.epic47_world import World, make_world
from tests.library_mgmt.media_fixtures import FFMPEG, mkv_h264_aac, mp4_moov_at_end, needs_ffmpeg

pytestmark = [pytest.mark.asyncio, needs_ffmpeg]


# ---------------------------------------------------------------- media


def ff(*args: str) -> None:
    assert FFMPEG is not None
    subprocess.run(
        [FFMPEG, "-hide_banner", "-v", "error", "-y", *args],
        check=True,
        capture_output=True,
        timeout=300,
    )


_V = ["-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25"]
_A = ["-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000"]
_X264 = ["-c:v", "libx264", "-preset", "ultrafast", "-g", "25", "-pix_fmt", "yuv420p"]


def webm_vp9_opus(path: Path, seconds: float = 3.0) -> Path:
    ff(*_V, *_A, "-t", str(seconds), "-c:v", "libvpx-vp9", "-deadline", "realtime",
       "-cpu-used", "8", "-b:v", "300k", "-c:a", "libopus", str(path))  # fmt: skip
    return path


def mkv_h264_opus(path: Path, seconds: float = 3.0) -> Path:
    ff(*_V, *_A, "-t", str(seconds), *_X264, "-c:a", "libopus", str(path))
    return path


def mkv_with_srt(path: Path, tmp: Path, seconds: float = 3.0) -> Path:
    srt = tmp / "subs.srt"
    srt.write_text(
        "1\n00:00:00,500 --> 00:00:01,500\nHello\n\n2\n00:00:02,000 --> 00:00:02,800\nWorld\n",
        encoding="utf-8",
    )
    ff(*_V, *_A, "-i", str(srt), "-t", str(seconds), "-map", "0:v", "-map", "1:a", "-map",
       "2:s", *_X264, "-c:a", "aac", "-c:s", "srt", "-metadata:s:s:0", "language=ara",
       str(path))  # fmt: skip
    return path


def long_for_reencode(path: Path) -> Path:
    """Long enough that a libx264 ``medium`` encode is still running when cancelled."""
    ff("-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=30", *_A, "-t", "60",
       "-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "8", "-b:v", "500k",
       "-c:a", "libopus", str(path))  # fmt: skip
    return path


# ---------------------------------------------------------------- engine


class Home:
    """A home worker on the real graph: claims and runs the ``convert`` pool."""

    def __init__(self, db: Any, rt: EngineRuntime) -> None:
        self.db = db
        self.rt = rt

    async def run_one(self) -> Job | None:
        job = await claim_for_pool(
            self.db, worker_id="home", pool="convert", stages=["convert_mp4"], scope="global"
        )
        if job is not None:
            await dispatch_job(self.db, job, runtime=self.rt, legacy_handlers={})
        return job

    async def drain(self, limit: int = 20) -> int:
        n = 0
        while n < limit and await self.run_one() is not None:
            n += 1
        return n


async def home(db: Any, rt_cls: type[EngineRuntime] = EngineRuntime) -> Home:
    graph = bound_graph()
    await sync_graph(db, graph)
    return Home(db, rt_cls(graph={s.name: s for s in graph}))


@pytest.fixture(autouse=True)
def _dirs(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(cv, "WORK_ROOT", str(tmp_path / "work-convert"))
    monkeypatch.setattr(thumb_small, "ROOT", str(tmp_path / "thumbs"))


async def track(w: World, src: Path, rel: str) -> tuple[uuid.UUID, Path]:
    """``src`` as a tracked library file at ``rel``, with an audio track and an active
    transcript (which a conversion must leave alone)."""
    vid, p = await w.video(rel, src.read_bytes())
    db = w.db
    await db.execute(
        "INSERT INTO audio_tracks (video_id, track_index, codec, language)"
        " VALUES ($1, 1, 'x', 'ar')",
        str(vid),
    )
    track_id = await db.fetchval("SELECT id FROM audio_tracks WHERE video_id = $1", str(vid))
    await db.execute(
        "INSERT INTO transcripts (id, video_id, audio_track_id, language, backend, model,"
        " is_active) VALUES ($1, $2, $3, 'ar', 'test', 'test', $4)",
        str(uuid.uuid4()),
        str(vid),
        track_id,
        True if is_pg(db) else 1,
    )
    return vid, p


async def transcripts(db: Any, vid: uuid.UUID) -> list[tuple[str, Any]]:
    rows = await db.fetch(
        "SELECT id, is_active FROM transcripts WHERE video_id = $1 ORDER BY id", uid(vid)
    )
    return [(str(r["id"]), bool(r["is_active"])) for r in rows]


async def jobs(db: Any, stage: str = "convert_mp4") -> list[Any]:
    return list(await db.fetch("SELECT * FROM processing_jobs WHERE stage = $1 ORDER BY id", stage))


async def convert_one(db: Any, vid: uuid.UUID, h: Home) -> Any:
    await enqueue_job(
        db,
        subject_kind="video",
        subject_id=vid,
        stage="convert_mp4",
        priority=EPIC47_PRIORITIES["convert_mp4_now"],
    )
    assert await h.run_one() is not None
    return (await jobs(db))[-1]


async def assert_converted(
    w: World, vid: uuid.UUID, original: Path, before: list[tuple[str, Any]], action: str
) -> Any:
    db = w.db
    job = (await jobs(db))[-1]
    assert job["state"] == "done", job["error"]
    assert job["outcome"] == "changed"
    summary = loads(job["output_summary"])
    assert summary["action"] == action and summary["result"] == "converted"
    new = await w.assert_tracked(vid)  # same id, the file on disk, its hash
    assert new.suffix == ".mp4" and moov_before_mdat(str(new)) is True
    assert not original.exists() or original == new
    trashed = w.trash_files()
    assert len(trashed) == 1 and trashed[0].name == original.name
    assert (await transcripts(db, vid)) == before
    rows = await list_actions(db, library_id=w.library_id, kinds=["convert"], states=["done"])
    assert [a.video_id for a in rows] == [vid]
    assert rows[0].verify is not None and rows[0].verify["ok"] is True
    assert w.staged_files() == []
    # probe reads the new container; nothing downstream of the words runs again
    asked = {r["stage"] for r in await db.fetch("SELECT stage FROM processing_jobs")}
    assert "probe" in asked
    assert not asked & {"transcribe", "index", "content_hash", "subtitle_gen"}
    assert not Path(cv.WORK_ROOT, str(job["id"])).exists()
    return summary


# ---------------------------------------------------------------- the graph


async def test_the_stage_is_wired_without_a_cascade() -> None:
    s = get_stage("convert_mp4")
    assert s is not None and not s.unreleased
    # one edge: the video's duplicate check after its conversion (Epic 47 pipeline order)
    assert [(e.to, e.map, e.when) for e in s.edges] == [("dedup_scan", "same_video", "always")]
    assert s.subject_kinds == ("video", "library")
    validate()


# ---------------------------------------------------------------- conversions


async def test_remux_an_h264_aac_mkv(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path)
    src = mkv_h264_aac(tmp_path / "in.mkv")
    vid, p = await track(w, src, "show/ep1.mkv")
    before = await transcripts(db, vid)
    # the thumbnail cache folder moves to the new hash
    old_hash = (await w.row(vid))["content_hash"]
    poster = Path(thumb_small.ROOT or "") / old_hash / "poster.jpg"
    poster.parent.mkdir(parents=True)
    poster.write_bytes(b"jpg")
    await db.execute("UPDATE videos SET poster_path = $2 WHERE id = $1", str(vid), str(poster))

    h = await home(db)
    await convert_one(db, vid, h)
    summary = await assert_converted(w, vid, p, before, "remux")
    assert summary["encoder"] is None and summary["thumbnails"] == "moved"
    row = await db.fetchrow("SELECT content_hash, poster_path FROM videos WHERE id = $1", str(vid))
    assert row["content_hash"] != old_hash
    assert row["poster_path"] == str(
        Path(thumb_small.ROOT or "") / row["content_hash"] / "poster.jpg"
    )
    assert Path(row["poster_path"]).read_bytes() == b"jpg" and not poster.parent.exists()
    info = await probe(str(w.root / "show" / "ep1.mp4"))
    assert info is not None and [a.codec_name for a in info.audio] == ["aac"]


async def test_audio_only_for_h264_opus(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path)
    vid, p = await track(w, mkv_h264_opus(tmp_path / "in.mkv"), "a.mkv")
    before = await transcripts(db, vid)
    await convert_one(db, vid, await home(db))
    await assert_converted(w, vid, p, before, "audio")
    info = await probe(str(w.root / "a.mp4"))
    assert info is not None and info.video[0].codec_name == "h264"
    assert [a.codec_name for a in info.audio] == ["aac"]
    codecs = {
        r["codec"]
        for r in await db.fetch("SELECT codec FROM audio_tracks WHERE video_id = $1", str(vid))
    }
    assert codecs == {"aac"}


async def test_vp9_is_skipped_unless_reencode_is_allowed(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path)
    vid, p = await track(w, webm_vp9_opus(tmp_path / "in.webm"), "v.webm")
    h = await home(db)
    job = await convert_one(db, vid, h)
    assert job["state"] == "done" and job["outcome"] == "unchanged"
    summary = loads(job["output_summary"])
    assert (summary["result"], summary["reason"]) == ("skipped", "needs_reencode")
    assert p.exists() and w.trash_files() == []
    assert await list_actions(db, library_id=w.library_id) == []


async def test_vp9_reencodes_to_h264_where_allowed(
    db: Database, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # libx264, as on CI (no VideoToolbox)
    monkeypatch.setattr(cv, "_encoders", {"libx264"})
    w = await make_world(db, tmp_path, settings={"convert": {"allow_reencode": True}})
    vid, p = await track(w, webm_vp9_opus(tmp_path / "in.webm"), "v.webm")
    before = await transcripts(db, vid)
    await convert_one(db, vid, await home(db))
    summary = await assert_converted(w, vid, p, before, "reencode")
    assert summary["encoder"] == "libx264"
    info = await probe(str(w.root / "v.mp4"))
    assert info is not None and info.video[0].codec_name == "h264"
    assert (info.video[0].width, info.video[0].height) == (320, 240)


async def test_text_subtitles_go_inside_and_beside(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path)
    vid, p = await track(w, mkv_with_srt(tmp_path / "in.mkv", tmp_path), "s.mkv")
    before = await transcripts(db, vid)
    summary = await assert_converted_after(db, w, vid, p, before)
    assert summary["srt"] == ["s.ara.srt"]
    side = w.root / "s.ara.srt"
    assert "Hello" in side.read_text(encoding="utf-8")
    info = await probe(str(w.root / "s.mp4"))
    assert info is not None
    assert [s.codec_name for s in info.streams if s.codec_type == "subtitle"] == ["mov_text"]


async def assert_converted_after(
    db: Any, w: World, vid: uuid.UUID, p: Path, before: list[tuple[str, Any]]
) -> Any:
    await convert_one(db, vid, await home(db))
    return await assert_converted(w, vid, p, before, "remux")


async def test_an_existing_sidecar_is_never_overwritten(tmp_path: Path) -> None:
    sub = cv.SubStream(0, "subrip", "en")
    final = str(tmp_path / "x.mp4")
    (tmp_path / "x.en.srt").write_text("mine")
    assert cv.sidecar_name(final, sub, os.path.lexists) == str(tmp_path / "x.en.0.srt")
    (tmp_path / "x.en.0.srt").write_text("mine too")
    assert cv.sidecar_name(final, sub, os.path.lexists) is None


async def test_image_subtitles_are_skipped(db: Database, tmp_path: Path) -> None:
    # ffmpeg cannot make a PGS stream from lavfi: the stored probe says there is one
    w = await make_world(db, tmp_path)
    vid, p = await track(w, mkv_h264_aac(tmp_path / "in.mkv"), "pgs.mkv")
    out = subprocess.run(
        ["ffprobe", "-v", "error", "-show_streams", "-show_format", "-of", "json", str(p)],
        capture_output=True,
        check=True,
    )
    doc = json.loads(out.stdout)
    doc["streams"].append({"index": 2, "codec_type": "subtitle", "codec_name": "hdmv_pgs_subtitle"})
    plan = cv.plan_for(cv.Probe(doc), str(p), allow_reencode=True, faststart=None)
    assert (plan.action, plan.reason) == ("skip", "image_subtitles")
    # the library's plan reads media_info
    await db.execute(
        "INSERT INTO media_info (video_id, container, raw_ffprobe) VALUES ($1, 'matroska', $2)",
        str(vid),
        dumps(doc),
    )
    h = await home(db)
    await enqueue_job(
        db, subject_kind="library", subject_id=w.library_id, stage="convert_mp4",
        payload={"dry_run": True},
    )  # fmt: skip
    await h.run_one()
    lib_job = (await jobs(db))[-1]
    s = loads(lib_job["output_summary"])
    assert s["reasons"] == {"image_subtitles": 1} and s["enqueued"] == 0


async def test_moov_at_end_is_remuxed_in_place(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path)
    src = mp4_moov_at_end(tmp_path / "in.mp4")
    assert moov_before_mdat(str(src)) is False
    vid, p = await track(w, src, "m.mp4")
    before = await transcripts(db, vid)
    await convert_one(db, vid, await home(db))
    await assert_converted(w, vid, p, before, "remux")
    assert p.exists() and moov_before_mdat(str(p)) is True  # same name


async def test_a_good_mp4_is_skipped_unchanged(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path)
    src = tmp_path / "ok.mp4"
    shutil.copy(mp4_moov_at_end(tmp_path / "raw.mp4"), src)
    ff("-i", str(tmp_path / "raw.mp4"), "-c", "copy", "-movflags", "+faststart", str(src))
    vid, p = await track(w, src, "ok.mp4")
    job = await convert_one(db, vid, await home(db))
    assert job["outcome"] == "skipped_unchanged"
    assert loads(job["output_summary"])["reason"] == "already_mp4"
    assert w.trash_files() == []


async def test_a_corrupt_input_fails_at_once(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path)
    vid, p = await w.video("bad.mkv", os.urandom(200_000))
    job = await convert_one(db, vid, await home(db))
    assert job["state"] == "failed"
    assert job["attempts"] == 1 and str(job["error_cause"]).startswith("ffmpeg_decode")
    assert job["failure_class"] == "item_permanent"
    assert p.exists() and w.trash_files() == []


async def test_a_failed_verification_is_retried_once_then_fails(
    db: Database, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    w = await make_world(db, tmp_path)
    vid, p = await track(w, mkv_h264_aac(tmp_path / "in.mkv"), "f.mkv")
    original = p.read_bytes()
    from maktaba_pipeline.library_mgmt.verify import VerifyResult

    calls: list[str] = []

    async def failing(new: str, *a: Any, **k: Any) -> VerifyResult:
        calls.append(new)
        # a reason that reads like a damaged input must not fail the job at once
        return VerifyResult(False, ["decode errors: Invalid data found"], {"mode": "remux"})

    monkeypatch.setattr(convert_mp4, "verify_media", failing)
    h = await home(db)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="convert_mp4")
    await h.run_one()
    job = (await jobs(db))[-1]
    assert job["state"] == "pending" and str(job["error_cause"]).startswith("convert_verify")
    await db.execute("UPDATE processing_jobs SET not_before = NULL WHERE id = $1", job["id"])
    await h.run_one()
    job = (await jobs(db))[-1]
    assert job["state"] == "failed" and job["attempts"] == 2
    assert p.read_bytes() == original and w.trash_files() == []
    failed = await list_actions(db, library_id=w.library_id, states=["failed"])
    assert len(failed) == 2 and all(a.verify and a.verify["ok"] is False for a in failed)
    assert len(calls) == 2


async def test_the_retry_after_a_failed_check_reencodes_where_allowed(tmp_path: Path) -> None:
    out = subprocess.run(
        ["ffprobe", "-v", "error", "-show_streams", "-show_format", "-of", "json",
         str(mkv_h264_aac(tmp_path / "in.mkv"))],
        capture_output=True, check=True,
    )  # fmt: skip
    probe_ = cv.Probe(json.loads(out.stdout))
    on = cv.plan_for(probe_, "x.mkv", allow_reencode=True, faststart=None, force_reencode=True)
    off = cv.plan_for(probe_, "x.mkv", allow_reencode=False, faststart=None, force_reencode=True)
    assert (on.action, off.action) == ("reencode", "remux")


async def test_cancel_kills_ffmpeg_and_leaves_nothing(
    db: Database, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(cv, "_encoders", {"libx264"})
    w = await make_world(db, tmp_path, settings={"convert": {"allow_reencode": True}})
    vid, p = await track(w, long_for_reencode(tmp_path / "long.webm"), "long.webm")
    original = p.read_bytes()
    seen: list[str] = []

    class CancellingCtx(StageContext):
        async def progress(self, step: str, done: int, total: int, *a: Any, **k: Any) -> None:
            await super().progress(step, done, total, *a, **k)
            if step == "convert" and done > 0 and not seen:
                seen.append(step)
                # an operator presses Cancel while ffmpeg runs
                await self.db.execute(
                    "UPDATE processing_jobs SET cancel_requested = $2 WHERE id = $1",
                    self.job.id,
                    True if is_pg(self.db) else 1,
                )

    class CancellingRuntime(EngineRuntime):
        async def context_for(self, db: Any, job: Job) -> StageContext:
            base = await super().context_for(db, job)
            return CancellingCtx(db=db, job=job, stage=base.stage, path_map=base.path_map)

    h = await home(db, CancellingRuntime)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="convert_mp4")
    await h.run_one()
    job = (await jobs(db))[-1]
    assert seen and job["state"] == "cancelled"
    assert not Path(cv.WORK_ROOT, str(job["id"])).exists()  # the partial MP4 is gone
    assert p.read_bytes() == original and w.trash_files() == [] and w.staged_files() == []
    assert not (w.root / "long.mp4").exists()
    assert await list_actions(db, library_id=w.library_id) == []


async def test_a_share_that_is_gone_parks_the_job(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path)
    vid, _ = await track(w, mkv_h264_aac(tmp_path / "in.mkv"), "gone.mkv")
    shutil.rmtree(w.root)  # the share's root does not answer
    job = await convert_one(db, vid, await home(db))
    assert job["state"] == "pending" and job["parked_on"] == f"dependency:share:{w.root}"


async def test_a_full_work_disk_parks_the_job(
    db: Database, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    w = await make_world(db, tmp_path)
    vid, p = await track(w, mkv_h264_aac(tmp_path / "in.mkv"), "full.mkv")
    monkeypatch.setattr(cv, "free_bytes", lambda path: 0)
    job = await convert_one(db, vid, await home(db))
    assert job["state"] == "pending" and job["parked_on"] == "resource:disk"
    assert p.exists() and w.trash_files() == []


@pytest.mark.parametrize(
    ("failure", "cls"),
    [
        (convert_mp4._disk_full("/w/1", "10 bytes free, 99 needed"), "resource:disk"),
        (StageFailure("file_missing", "file_missing: x"), "item_permanent"),
        (
            StageFailure(
                "convert_verify",
                "the MP4 failed its check locally: 1 problem(s), see file action a",
            ),
            "unknown",
        ),
        (
            StageFailure("ffmpeg_decode", "the original is damaged: Invalid data"),
            "item_permanent",
        ),
    ],
)
async def test_failures_land_in_their_epic40_class(failure: Any, cls: str) -> None:
    from maktaba_pipeline.engine.causes import classify
    from maktaba_pipeline.engine.failure_classes import class_for

    # file_missing is refined to dependency:share:<root> when the root does not answer
    # (failure_policy.decide); convert_verify (unknown) is retried within max_attempts 2
    assert class_for(classify(failure.kind, failure.message)) == cls


# ---------------------------------------------------------------- the library's job


async def test_a_library_dry_run_plans_and_touches_nothing(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path)
    a, pa = await track(w, mkv_h264_aac(tmp_path / "a.mkv"), "a.mkv")
    b, pb = await track(w, webm_vp9_opus(tmp_path / "b.webm"), "b.webm")
    c, pc = await track(w, mkv_h264_opus(tmp_path / "c.mkv"), "c.mkv")
    before = {p: p.read_bytes() for p in (pa, pb, pc)}
    h = await home(db)
    await enqueue_job(
        db, subject_kind="library", subject_id=w.library_id, stage="convert_mp4",
        payload={"dry_run": True}, priority=EPIC47_PRIORITIES["convert_mp4"],
    )  # fmt: skip
    assert await h.drain() == 1
    job = (await jobs(db))[-1]
    assert job["state"] == "done" and job["outcome"] == "unchanged"
    s = loads(job["output_summary"])
    assert s["dry_run"] is True and s["enqueued"] == 0 and s["videos"] == 3
    got = {e["video_id"]: (e["action"], e.get("reason")) for e in s["plan"]}
    assert got == {
        str(a): ("remux", None),
        str(b): ("skip", "needs_reencode"),
        str(c): ("audio", None),
    }
    assert {e["final_path"] for e in s["plan"] if "final_path" in e} == {
        str(w.root / "a.mp4"),
        str(w.root / "c.mp4"),
    }
    assert {p: p.read_bytes() for p in (pa, pb, pc)} == before
    assert w.trash_files() == [] and await list_actions(db, library_id=w.library_id) == []


async def test_a_library_run_enqueues_per_video_jobs(db: Database, tmp_path: Path) -> None:
    w = await make_world(db, tmp_path)
    a, pa = await track(w, mkv_h264_aac(tmp_path / "a.mkv"), "a.mkv")
    b, _ = await track(w, webm_vp9_opus(tmp_path / "b.webm"), "b.webm")
    h = await home(db)
    await enqueue_job(db, subject_kind="library", subject_id=w.library_id, stage="convert_mp4")
    assert await h.drain() == 2  # the library's job, then a's
    rows = await jobs(db)
    lib_job, video_job = rows
    assert loads(lib_job["output_summary"])["enqueued"] == 1
    assert str(video_job["subject_id"]) == str(a) and video_job["priority"] == 150
    assert video_job["triggered_by"] == lib_job["id"] and video_job["state"] == "done"
    await w.assert_tracked(a)
    assert (await w.row(a))["path"] == str(w.root / "a.mp4")
    assert (await w.row(b))["path"] == str(w.root / "b.webm")
