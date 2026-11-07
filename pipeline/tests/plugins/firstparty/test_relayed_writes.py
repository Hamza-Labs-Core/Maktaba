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
    job = SimpleNamespace(library_id=lib, subject=Subject(kind=kind, id=subject or lib))
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
