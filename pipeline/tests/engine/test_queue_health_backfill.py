"""Epic 40: today's failures moved into the new model (40.6), missing files at the scan (40.3)."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.engine.backfill import backfill_once
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.failure_classes import QueueHealthConfig
from maktaba_pipeline.engine.missing_files import confirm_missing
from maktaba_pipeline.engine.sql import is_pg, now, to_ts
from maktaba_pipeline.engine.watchers import watch_once
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_video
from tests.engine.toy import Recorder, rows, setup


async def old_failure(
    db: Database, video: UUID, kind: str, message: str, cause: str | None, attempts: int = 1
) -> int:
    """A failed row as the engine wrote it before Epic 40 (no class)."""
    res = await enqueue_job(db, subject_kind="video", subject_id=video, stage="t_a")
    assert res.job_id is not None
    await db.execute(
        "UPDATE processing_jobs SET state = 'failed', attempts = $2, error = $3,"
        " error_cause = $4, finished_at = $5 WHERE id = $1",
        res.job_id,
        attempts,
        json.dumps({"kind": kind, "message": message, "retryable": True}),
        cause,
        to_ts(db, now()),
    )
    return res.job_id


@pytest.mark.asyncio
async def test_old_failures_are_classified_parked_requeued_or_kept(db: Database) -> None:
    await setup(db, Recorder())
    lib = await make_library(db)
    model, nas, damaged = [await make_video(db, lib) for _ in range(3)]
    await old_failure(
        db, model, "model_unavailable", "x: the model did not answer", "model_unavailable"
    )
    await old_failure(db, nas, "import", "read failed: 2 frames", None)
    await old_failure(db, damaged, "ffmpeg", "moov atom not found", "ffmpeg_decode")
    got = await backfill_once(db, QueueHealthConfig())
    assert (got.classified, got.parked, got.requeued) == (3, 1, 1)
    by_video = {str(r["subject_id"]): r for r in await rows(db, "t_a") if r["state"] == "pending"}
    assert by_video[str(model)]["parked_on"] == "dependency:model:model"
    assert by_video[str(nas)]["parked_on"] is None
    assert str(damaged) not in by_video
    classes = {
        str(r["subject_id"]): r["failure_class"]
        for r in await rows(db, "t_a")
        if r["state"] == "failed"
    }
    assert classes == {
        str(model): "dependency:model:model",
        str(nas): "transient",
        str(damaged): "item_permanent",
    }
    assert (await backfill_once(db, QueueHealthConfig())).classified == 0  # idempotent


@pytest.mark.asyncio
async def test_a_superseded_failure_is_only_classified(db: Database) -> None:
    await setup(db, Recorder())
    v = await make_video(db, await make_library(db))
    await old_failure(db, v, "model_unavailable", "down", "model_unavailable")
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")  # a later run
    got = await backfill_once(db, QueueHealthConfig())
    assert (got.classified, got.parked) == (1, 0)


@pytest.mark.asyncio
async def test_the_watcher_round_moves_them_and_releases_them_when_the_model_answers(
    db: Database,
) -> None:
    await setup(db, Recorder())
    v = await make_video(db, await make_library(db))
    await old_failure(db, v, "model_unavailable", "down", "model_unavailable")

    async def probes(*_: Any) -> dict[str, tuple[bool, str]]:
        return {"dependency:model:model": (True, "answers")}

    report = await watch_once(db, {}, probes=probes)
    assert report.backfilled == 1 and report.released == {"dependency:model:model": 1}
    pending = [r for r in await rows(db, "t_a") if r["state"] == "pending"]
    assert len(pending) == 1 and pending[0]["parked_on"] is None


async def _missing_world(db: Database, root: Path) -> tuple[UUID, UUID, Path]:
    await setup(db, Recorder())
    lib = await make_library(db)
    await db.execute(
        "UPDATE libraries SET roots = $1 WHERE id = $2",
        [root.as_posix()] if is_pg(db) else json.dumps([root.as_posix()]),
        str(lib),
    )
    f = root / "gone.mp4"
    v = await make_video(db, lib, path=f.as_posix())
    await old_failure(db, v, "file_missing", f"{f}: not found", "file_missing")
    return lib, v, f


@pytest.mark.asyncio
async def test_the_scan_confirms_a_missing_file(db: Database, tmp_path: Path) -> None:
    lib, v, _ = await _missing_world(db, tmp_path)
    got = await confirm_missing(db, lib)
    assert (got.checked, got.healed, got.confirmed) == (True, 0, 1)
    at = await db.fetchval("SELECT missing_confirmed_at FROM videos WHERE id = $1", str(v))
    assert at is not None


@pytest.mark.asyncio
async def test_the_scan_heals_a_file_that_is_back(db: Database, tmp_path: Path) -> None:
    lib, v, f = await _missing_world(db, tmp_path)
    await confirm_missing(db, lib)
    f.write_bytes(b"back")
    got = await confirm_missing(db, lib)
    assert (got.healed, got.confirmed) == (1, 0)
    assert (
        await db.fetchval("SELECT missing_confirmed_at FROM videos WHERE id = $1", str(v)) is None
    )
    assert [r["state"] for r in await rows(db, "t_a")] == ["failed", "pending"]


@pytest.mark.asyncio
async def test_a_root_that_does_not_answer_confirms_nothing(db: Database, tmp_path: Path) -> None:
    lib, v, _ = await _missing_world(db, tmp_path / "offline")
    got = await confirm_missing(db, lib)
    assert got.checked is False
    assert (
        await db.fetchval("SELECT missing_confirmed_at FROM videos WHERE id = $1", str(v)) is None
    )
