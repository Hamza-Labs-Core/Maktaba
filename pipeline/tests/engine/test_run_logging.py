"""A failed stage writes a log line, not only its DB rows (error-logging audit)."""

from __future__ import annotations

import pytest
from structlog.testing import capture_logs

from maktaba_pipeline.db.jobs import DBConn, Job
from maktaba_pipeline.engine.context import StageContext, StageFailure
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import StageResult
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_video
from tests.engine.toy import Recorder, drain, setup


@pytest.mark.asyncio
async def test_stage_failure_is_logged_with_its_outcome(db: Database) -> None:
    rec = Recorder()
    rt = await setup(db, rec)
    v = await make_video(db, await make_library(db))

    async def boom(db_: DBConn, job: Job, ctx: StageContext) -> StageResult:
        raise StageFailure("bad_input", "cannot read", retryable=False)

    rec.hooks["t_a"] = boom
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    with capture_logs() as logs:
        await drain(db, rt, max_jobs=1)

    failed = [e for e in logs if e["event"] == "stage_failed"]
    assert len(failed) == 1
    f = failed[0]
    assert f["log_level"] == "warning"
    assert f["stage"] == "t_a" and str(f["subject_id"]) == str(v)
    assert f["kind"] == "bad_input" and f["retryable"] is False
    assert f["error"] == "cannot read"

    outcome = [e for e in logs if e["event"] == "stage_failure_outcome"]
    assert len(outcome) == 1
    o = outcome[0]
    assert o["job_id"] == f["job_id"] and o["stage"] == "t_a"
    assert o["state"] == "failed" and o["outcome"] == "terminal"
    assert o["not_before"] is None and o["parked_on"] is None
