"""What only the engine writes, asked of the worker by a plugin's job (Epic 54 wave 5,
plugins/native.py): queuing a job, a duplicate group's merge, a restored video's state. The
worker checks each against the job; a request outside it is refused (plugin_scope)."""

from __future__ import annotations

from types import SimpleNamespace
from typing import Any

import pytest

from maktaba_pipeline.engine.graph import Subject, get_stage
from maktaba_pipeline.plugins import native
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_video

pytestmark = pytest.mark.asyncio


def _ctx(stage: str, lib: Any, subject: Any = None, kind: str = "library") -> Any:
    job = SimpleNamespace(library_id=lib, subject=Subject(kind=kind, id=subject or lib))  # type: ignore[arg-type]
    return SimpleNamespace(job=job, stage=get_stage(stage))


async def test_a_plugin_queues_its_neighbour_for_a_video_of_its_library(db: Database) -> None:
    from maktaba_pipeline.engine.graph import bound_graph
    from maktaba_pipeline.engine.sync import sync_graph

    await sync_graph(db, bound_graph())
    lib = await make_library(db)
    v = await make_video(db, lib)
    got = await native._relayed_enqueue(
        db, _ctx("dedup_scan", lib), {"stage": "convert_mp4", "subject_kind": "video",
                                       "subject_id": str(v)},
    )  # fmt: skip
    assert got["job_id"] is not None


async def test_a_stage_it_may_not_queue_or_another_librarys_video_is_refused(db: Database) -> None:
    lib, other = await make_library(db), await make_library(db)
    v = await make_video(db, other)
    with pytest.raises(native.EnqueueRefused, match="plugin_scope"):
        await native._relayed_enqueue(
            db, _ctx("dedup_scan", lib), {"stage": "transcribe", "subject_kind": "video",
                                           "subject_id": str(v)},
        )  # fmt: skip
    with pytest.raises(native.EnqueueRefused, match="not of this job's library"):
        await native._relayed_enqueue(
            db, _ctx("dedup_scan", lib), {"stage": "dedup_scan", "subject_kind": "video",
                                           "subject_id": str(v)},
        )  # fmt: skip


async def test_a_merge_of_another_group_or_of_strangers_is_refused(db: Database) -> None:
    lib = await make_library(db)
    a, b = await make_video(db, lib), await make_video(db, lib)
    gid = "00000000-0000-4000-8000-000000000054"
    ctx = _ctx("dedup_apply", lib, subject=gid, kind="dup_group")
    with pytest.raises(native.EnqueueRefused, match="not this job's"):
        await native._relayed_dedup_commit(
            db, ctx, {"group_id": "00000000-0000-4000-8000-000000000055", "keeper": str(a),
                      "losers": [str(b)], "keeper_library": str(lib)},
        )  # fmt: skip
    with pytest.raises(native.EnqueueRefused, match="not the members"):
        await native._relayed_dedup_commit(
            db, ctx, {"group_id": gid, "keeper": str(a), "losers": [str(b)],
                      "keeper_library": str(lib)},
        )  # fmt: skip


async def test_a_merge_moves_rows_only_to_the_keepers_own_library(db: Database) -> None:
    """README §15.12: keeper_library is where the losers' rows go; another library (or the
    keeper's in another form of its id) is checked as a uuid."""
    lib, other = await make_library(db), await make_library(db)
    a, b = await make_video(db, lib), await make_video(db, lib)
    gid = "00000000-0000-4000-8000-000000000056"
    await db.execute(
        "INSERT INTO duplicate_groups (id, library_id, tier, members_key)"
        " VALUES ($1, $2, 'exact', 'k')",
        gid,
        str(lib),
    )
    for v in (a, b):
        await db.execute(
            "INSERT INTO duplicate_members (group_id, video_id, path) VALUES ($1, $2, '/p')",
            gid,
            str(v),
        )
    ctx = _ctx("dedup_apply", lib, subject=gid, kind="dup_group")
    with pytest.raises(native.EnqueueRefused, match="not the keeper's library"):
        await native._relayed_dedup_commit(
            db, ctx, {"group_id": gid, "keeper": str(a), "losers": [str(b)],
                      "keeper_library": str(other)},
        )  # fmt: skip
    assert native._same_id(str(lib).upper(), lib) and not native._same_id(str(other), lib)


def _job_ctx(lib: Any, video: Any) -> Any:
    job = SimpleNamespace(
        id=7,
        subject_kind="video",
        subject=Subject(kind="video", id=video),
        library_id=lib,
        stage="transcribe",
    )
    return SimpleNamespace(job=job, stage=get_stage("transcribe"))


async def test_a_job_event_is_only_of_the_plugins_own_job() -> None:
    """README §15.12: the worker writes job events for a plugin's job; one naming another
    job, subject, library or stage is refused, and the job's own values are written."""
    lib, video = "8f0e7c38-6c2e-4b8f-9d61-1a2b3c4d5e6f", "1b2c3d4e-5f60-4718-9a2b-3c4d5e6f7081"
    ctx = _job_ctx(lib, video)
    own = {
        "job_id": 7, "subject_kind": "video", "subject_id": video, "library_id": lib,
        "stage": "transcribe", "level": "info", "kind": "step", "message": "m",
    }  # fmt: skip
    got = native._own_job_event(ctx, {**own, "subject_id": video.upper()})
    assert got["subject_id"] == video and got["job_id"] == 7
    for k, v in (
        ("job_id", 8),
        ("subject_id", "2b2c3d4e-5f60-4718-9a2b-3c4d5e6f7081"),
        ("library_id", "9f0e7c38-6c2e-4b8f-9d61-1a2b3c4d5e6f"),
        ("library_id", None),
        ("subject_kind", "library"),
        ("stage", "second_listen"),
    ):
        with pytest.raises(native.EnqueueRefused, match="plugin_scope"):
            native._own_job_event(ctx, {**own, k: v})


async def test_only_a_superseded_video_of_the_library_comes_back(db: Database) -> None:
    lib = await make_library(db)
    v = await make_video(db, lib)
    with pytest.raises(native.EnqueueRefused, match="not a superseded video"):
        await native._relayed_unsupersede(
            db, _ctx("file_restore", lib), {"video_id": str(v), "state": "ready"}
        )


async def test_a_download_starts_its_new_videos_pipeline(db: Database) -> None:
    """youtube_fetch queues probe for a video it downloaded, as the scanner does."""
    from maktaba_pipeline.engine.graph import bound_graph
    from maktaba_pipeline.engine.sync import sync_graph

    await sync_graph(db, bound_graph())
    lib = await make_library(db)
    v = await make_video(db, lib)
    got = await native._relayed_enqueue(
        db, _ctx("youtube_fetch", lib), {"stage": "probe", "subject_kind": "video",
                                          "subject_id": str(v)},
    )  # fmt: skip
    assert got["job_id"] is not None


async def test_only_tv_swap_commits_and_only_its_own_change(db: Database) -> None:
    """The swap commit and the rollback (story 54.34) are the worker's: asked by another
    stage they are refused, and the change is always the job's own payload's."""
    ctx = SimpleNamespace(
        job=SimpleNamespace(id=7, stage="tv_stage", payload={"change_id": "x"}),
    )
    with pytest.raises(native.EnqueueRefused, match="tv_commit is tv_swap's"):
        await native._relayed_tv(db, ctx, "tv_commit")
    with pytest.raises(native.EnqueueRefused, match="tv_rollback is tv_rollback's"):
        await native._relayed_tv(db, ctx, "tv_rollback")
    # tv_swap for a change that is not swapping: the lookup comes back, nothing is written
    ctx.job.stage = "tv_swap"
    ctx.job.payload = {"change_id": "00000000-0000-4000-8000-000000000077"}
    got = await native._relayed_tv(db, ctx, "tv_commit")
    assert set(got) == {"lookup"}
    ctx.job.payload = {}
    assert await native._relayed_tv(db, ctx, "tv_commit") == {
        "lookup": "no change_id in the job's payload"
    }


async def test_the_areas_carry_the_reports_and_the_channels_art(
    db: Database, tmp_path: Any, monkeypatch: pytest.MonkeyPatch
) -> None:
    """tv.maktaba.livetv (story 54.34) writes the plan's page to the reports folder and reads
    the channels' art: what an admin uploaded and the folders the planner's rules name."""
    import os

    from maktaba_pipeline.engine.sql import dumps

    monkeypatch.setenv("MAKTABA_REVIEWS_ROOT", str(tmp_path / "reviews"))
    monkeypatch.setenv("MAKTABA_DATA_DIR", str(tmp_path / "data"))
    await db.execute(
        "INSERT INTO app_settings (key, value) VALUES ('rules.planner', $1)",
        dumps({"dz_logo_dir": str(tmp_path / "logos"),
               "dz_watermark_file": str(tmp_path / "marks" / "corner.png")}),
    )  # fmt: skip
    doc = await native.areas_doc(db)
    real = os.path.realpath
    assert doc["data.review"] == real(tmp_path / "reviews")
    assert doc["data.channels"].split(";") == sorted(
        real(tmp_path / p) for p in ("data/channel-art", "logos", "marks")
    )


def _video_job(stage: str, lib: Any, video: Any) -> Any:
    job = SimpleNamespace(
        stage=stage, library_id=lib, video_id=video, subject=Subject(kind="video", id=video)
    )
    return SimpleNamespace(job=job, stage=get_stage(stage))


def _lib_job(stage: str, lib: Any) -> Any:
    job = SimpleNamespace(stage=stage, library_id=lib, subject=Subject(kind="library", id=lib))
    return SimpleNamespace(job=job, stage=get_stage(stage))


async def test_probe_advances_only_its_own_video_by_its_own_trigger(db: Database) -> None:
    """Only the engine writes ``videos.state`` (story 54.36): the worker applies probe's
    transition for the job's video; another video, or another stage's trigger, is refused
    and the state stays as it was."""
    if db.dialect != "postgres":
        pytest.skip("the runners, and so the relayed advance, need Postgres")
    lib = await make_library(db)
    v, other = await make_video(db, lib), await make_video(db, lib)
    ctx = _video_job("probe", lib, v)
    with pytest.raises(native.EnqueueRefused, match="plugin_scope: video"):
        await native._relayed_advance(
            db, ctx, {"video_id": str(other), "trigger": "probe", "outcome": "ok"}
        )
    with pytest.raises(native.EnqueueRefused, match="may not advance a video by 'thumbnail'"):
        await native._relayed_advance(
            db, ctx, {"video_id": str(v), "trigger": "thumbnail", "outcome": "ok"}
        )
    with pytest.raises(native.EnqueueRefused, match="link may not advance"):
        await native._relayed_advance(
            db, _video_job("link", lib, v),
            {"video_id": str(v), "trigger": "probe", "outcome": "ok"},
        )  # fmt: skip
    states = await db.fetch("SELECT DISTINCT state FROM videos")
    assert [r["state"] for r in states] == ["discovered"]
    got = await native._relayed_advance(
        db, ctx, {"video_id": str(v), "trigger": "probe", "outcome": "ok"}
    )
    assert got == {"state": "probed"}
    assert await db.fetchval("SELECT state FROM videos WHERE id = $1", str(v)) == "probed"
    assert await db.fetchval("SELECT state FROM videos WHERE id = $1", str(other)) == "discovered"


async def test_a_failed_stage_is_queued_again_only_for_the_jobs_video_or_library(
    db: Database,
) -> None:
    """access_check queues its own video's failed stages again, scan a video of its library
    (a missing file found again); another library's video, or another stage, is refused."""
    from maktaba_pipeline.engine.graph import bound_graph
    from maktaba_pipeline.engine.sync import sync_graph

    await sync_graph(db, bound_graph())
    lib, far = await make_library(db), await make_library(db)
    v, stranger = await make_video(db, lib), await make_video(db, far)
    for vid in (v, stranger):
        await db.execute(
            "INSERT INTO processing_jobs (subject_kind, subject_id, video_id, stage, state)"
            " VALUES ('video', $1, $1, 'thumb_hash', 'failed')",
            str(vid),
        )
    with pytest.raises(native.EnqueueRefused, match="access_check may not queue"):
        await native._relayed_requeue_failed(
            db, _video_job("access_check", lib, v), {"video_id": str(stranger), "why": "x"}
        )
    with pytest.raises(native.EnqueueRefused, match="scan may not queue"):
        await native._relayed_requeue_failed(
            db, _lib_job("scan", lib), {"video_id": str(stranger), "why": "x"}
        )
    with pytest.raises(native.EnqueueRefused, match="link may not queue"):
        await native._relayed_requeue_failed(
            db, _video_job("link", lib, v), {"video_id": str(v), "why": "x"}
        )
    assert await db.fetchval("SELECT count(*) FROM processing_jobs WHERE state = 'pending'") == 0
    got = await native._relayed_requeue_failed(
        db, _video_job("access_check", lib, v), {"video_id": str(v), "why": "file_readable"}
    )
    assert got == ["thumb_hash"]
    # scan, for a video of its library: its latest thumb_hash is now the pending one, so
    # nothing is queued twice
    got = await native._relayed_requeue_failed(
        db, _lib_job("scan", lib), {"video_id": str(v), "why": "file_found_again"}
    )
    assert got == []
    pending = await db.fetch(
        "SELECT subject_id FROM processing_jobs WHERE state = 'pending' AND stage = 'thumb_hash'"
    )
    assert {str(r["subject_id"]) for r in pending} == {str(v)}


async def test_only_storage_canonicalizes_job_runs_the_path_conversion(db: Database) -> None:
    """The path conversion pauses stages and writes the share map: the worker runs it for
    storage_canonicalize's job only; with no conversion set up on this worker it skips."""
    from maktaba_pipeline.sharepath import auto

    ctx = SimpleNamespace(job=SimpleNamespace(stage="scan"))
    with pytest.raises(native.EnqueueRefused, match="storage_canonicalize is not scan's"):
        await native._relayed_storage_canonicalize(db, ctx)
    auto.set_stage_env(None)
    ctx.job.stage = "storage_canonicalize"
    got = await native._relayed_storage_canonicalize(db, ctx)
    assert got == {"phase": "skipped", "why": "not the home's own machine"}
