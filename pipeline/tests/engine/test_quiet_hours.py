"""Quiet hours (Epic 59 story 59.02): heavy work waits for the window, Run now goes."""

from __future__ import annotations

from datetime import datetime
from typing import Any

import pytest

from maktaba_pipeline.engine import quiet
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.quiet import QuietHours, parse_window
from maktaba_pipeline.engine.sql import dumps, is_pg
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_video
from tests.engine.toy import Recorder, drain, rows, setup


def test_window_parsing_and_math() -> None:
    assert parse_window("01:00-06:00") == (60, 360)
    assert parse_window("22:00-06:00") == (1320, 360)
    for bad in ("", None, "1-6", "01:00", "25:00-06:00", "01:00-01:00", "1:0-06:00"):
        assert parse_window(bad) is None, bad
    q = QuietHours(60, 360)
    at = datetime(2026, 11, 10, 14, 0)
    assert not q.inside(at) and q.until_open(at) == 11 * 3600
    assert (
        q.inside(datetime(2026, 11, 10, 1, 0)) and q.until_open(datetime(2026, 11, 10, 1, 0)) == 0
    )
    assert not q.inside(datetime(2026, 11, 10, 6, 0))
    late = QuietHours(1320, 360)  # across midnight
    assert late.inside(datetime(2026, 11, 10, 23, 30)) and late.inside(
        datetime(2026, 11, 10, 5, 59)
    )
    assert not late.inside(datetime(2026, 11, 10, 12, 0))
    assert late.until_open(datetime(2026, 11, 10, 21, 0)) == 3600
    assert QuietHours.from_env(
        {"MAKTABA_QUIET_HOURS": "01:00-06:00", "MAKTABA_QUIET_SPRITES": "1"}
    ) == QuietHours(60, 360, sprites=True)
    assert QuietHours.from_env({}) is None
    assert q.start_text == "01:00"


@pytest.fixture
def heavy_toy(monkeypatch: pytest.MonkeyPatch) -> None:
    """The toy stage t_a counts as heavy; the window is 01:00-06:00."""
    monkeypatch.setenv("MAKTABA_QUIET_HOURS", "01:00-06:00")
    monkeypatch.setattr(quiet, "ALWAYS_HEAVY", frozenset({"t_a"}))


def clock(monkeypatch: pytest.MonkeyPatch, hour: int) -> None:
    monkeypatch.setattr(quiet, "_now", lambda: datetime(2026, 11, 10, hour, 0))


# T06: at 14:00 a heavy job is given back until 01:00, the queue can say why; Run now on
# it (run_now, its wait cleared) runs it at once.
@pytest.mark.asyncio
async def test_heavy_job_waits_and_run_now_runs_it(
    db: Database, heavy_toy: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    clock(monkeypatch, 14)
    rec = Recorder()
    rt = await setup(db, rec)
    v = await make_video(db, await make_library(db))
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt)
    assert rec.count("t_a") == 0
    job = (await rows(db, "t_a"))[0]
    assert job["state"] == "paused" and job["paused_reason"] == "quiet_hours:01:00"
    assert job["attempts"] <= 1 and job["not_before"] is not None

    # Run now (what the API's run-now does)
    payload = dumps({"run_now": True})
    if is_pg(db):
        await db.execute(
            "UPDATE processing_jobs SET not_before = NULL,"
            " payload = COALESCE(payload, '{}'::jsonb) || $2::jsonb WHERE id = $1",
            job["id"],
            payload,
        )
    else:
        await db.execute(
            "UPDATE processing_jobs SET not_before = NULL, payload = $2 WHERE id = $1",
            job["id"],
            payload,
        )
    await drain(db, rt)
    assert rec.count("t_a") == 1


# T07: inside the window the waiting job runs; light work never waits.
@pytest.mark.asyncio
async def test_inside_the_window_it_runs(
    db: Database, heavy_toy: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    clock(monkeypatch, 1)
    rec = Recorder()
    rt = await setup(db, rec)
    v = await make_video(db, await make_library(db))
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt)
    assert rec.count("t_a") == 1  # and t_b, which is not heavy, ran at once after it
    assert rec.count("t_b") == 1


# No window (Strong, or none set): nothing waits.
@pytest.mark.asyncio
async def test_no_window_nothing_waits(db: Database, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("MAKTABA_QUIET_HOURS", raising=False)
    monkeypatch.setattr(quiet, "ALWAYS_HEAVY", frozenset({"t_a"}))
    clock(monkeypatch, 14)
    rec = Recorder()
    rt = await setup(db, rec)
    v = await make_video(db, await make_library(db))
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt)
    assert rec.count("t_a") == 1


class _Job:
    def __init__(self, stage: str, library_id: object = None, payload: object = None) -> None:
        self.stage, self.library_id, self.payload = stage, library_id, payload


class _DB:
    """Answers the counts and the scan state is_heavy reads."""

    dialect = "sqlite"

    def __init__(self, waiting: int, scanned: bool) -> None:
        self.waiting, self.scanned = waiting, scanned

    async def fetchrow(self, sql: str, *args: object) -> dict[str, object] | None:
        if "library_scan_state" in sql:
            return {"last_scan_at": "2026-11-01"} if self.scanned else None
        return {"n": self.waiting}


def J(stage: str, library_id: object = None, payload: object = None) -> Any:
    return _Job(stage, library_id, payload)


def D(waiting: int, scanned: bool) -> Any:
    return _DB(waiting, scanned)


# Which jobs are heavy: a first scan (not a rescan), a backlog over 10 transcriptions (not
# one new file's), convert and dedup, sprites over 50 (or always on Light).
@pytest.mark.asyncio
async def test_what_is_heavy() -> None:
    q = QuietHours(60, 360)
    light = QuietHours(60, 360, sprites=True)
    assert await quiet.is_heavy(D(0, False), J("scan", library_id="l"), q)
    assert not await quiet.is_heavy(D(0, True), J("scan", library_id="l"), q)
    assert await quiet.is_heavy(D(11, True), J("transcribe"), q)
    assert not await quiet.is_heavy(D(1, True), J("transcribe"), q)
    assert await quiet.is_heavy(D(0, True), J("convert"), q)
    assert await quiet.is_heavy(D(0, True), J("dedup_scan"), q)
    assert await quiet.is_heavy(D(51, True), J("thumbnail"), q)
    assert not await quiet.is_heavy(D(3, True), J("thumbnail"), q)
    assert await quiet.is_heavy(D(3, True), J("thumbnail"), light)
    assert not await quiet.is_heavy(D(999, True), J("thumb_small"), light)
    at = datetime(2026, 11, 10, 14, 0)
    assert await quiet.hold_seconds(D(0, True), J("convert"), quiet=q, at=at) == 11 * 3600
    assert (
        await quiet.hold_seconds(
            D(0, True), J("convert", payload={"run_now": True}), quiet=q, at=at
        )
        == 0
    )
    assert (
        await quiet.hold_seconds(
            D(0, True), J("convert", payload='{"run_now": true}'), quiet=q, at=at
        )
        == 0
    )


# Story 59.06: on the Light tier (MAKTABA_QUIET_SPEECH) speech runs in quiet hours only, a
# single new file's too; elsewhere a single new file's transcript still runs at once.
@pytest.mark.asyncio
async def test_light_transcribes_in_quiet_hours_only() -> None:
    light = QuietHours.from_env(
        {
            "MAKTABA_QUIET_HOURS": "01:00-06:00",
            "MAKTABA_QUIET_SPRITES": "1",
            "MAKTABA_QUIET_SPEECH": "1",
        }
    )
    assert light == QuietHours(60, 360, sprites=True, speech=True)
    assert await quiet.is_heavy(D(1, True), J("transcribe"), light)
    standard = QuietHours.from_env(
        {"MAKTABA_QUIET_HOURS": "01:00-06:00", "MAKTABA_QUIET_SPEECH": "0"}
    )
    assert standard is not None and not standard.speech
    assert not await quiet.is_heavy(D(1, True), J("transcribe"), standard)
    at = datetime(2026, 11, 10, 14, 0)
    assert await quiet.hold_seconds(D(1, True), J("transcribe"), quiet=light, at=at) == 11 * 3600
    night = datetime(2026, 11, 10, 2, 0)
    assert await quiet.hold_seconds(D(1, True), J("transcribe"), quiet=light, at=night) == 0
