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
