"""enqueue_job semantics, identical expectations on SQLite and Postgres (plan 06-11 section 13)."""

from __future__ import annotations

import asyncio
from datetime import timedelta
from uuid import uuid4

import pytest

from maktaba_pipeline.engine.enqueue import enqueue_job, merge_payload, prepend_reason
from maktaba_pipeline.engine.graph import CATALOG_ID
from maktaba_pipeline.engine.sql import loads, now, parse_ts, to_ts
from tests.engine.factory import make_library, make_series_and_season, make_video


async def _row(db, job_id: int):
    return await db.fetchrow("SELECT * FROM processing_jobs WHERE id = $1", job_id)


async def _count(db) -> int:
    return int(await db.fetchval("SELECT count(*) FROM processing_jobs"))


@pytest.mark.asyncio
async def test_insert_then_coalesce(db) -> None:
    lib = await make_library(db)
    v = await make_video(db, lib)
    first = await enqueue_job(
        db, subject_kind="video", subject_id=v, stage="probe", priority=100,
        reason={"why": "scan", "by": "system"},
    )  # fmt: skip
    assert first.outcome == "inserted" and first.job_id is not None
    second = await enqueue_job(
        db, subject_kind="video", subject_id=v, stage="probe", priority=50, force=True,
        reason={"why": "user", "by": "user:1"},
    )  # fmt: skip
    assert (second.outcome, second.job_id) == ("coalesced", first.job_id)
    row = await _row(db, first.job_id)
    assert row["priority"] == 50  # lowest wins
    assert bool(row["force"]) is True  # ORed
    assert row["enqueue_count"] == 2
    reasons = loads(row["reasons"])
    assert [r["why"] for r in reasons] == ["user", "scan"]  # newest first
    assert str(row["library_id"]) == str(lib)
    assert row["subject_kind"] == "video" and str(row["subject_id"]) == str(v)
    # a lower-urgency trigger never raises the number
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="probe", priority=200)
    assert (await _row(db, first.job_id))["priority"] == 50
    assert await _count(db) == 1


@pytest.mark.asyncio
async def test_reasons_are_capped_at_twenty(db) -> None:
    lib = await make_library(db)
    v = await make_video(db, lib)
    for i in range(25):
        r = await enqueue_job(
            db, subject_kind="video", subject_id=v, stage="probe", reason={"why": f"r{i}"}
        )
    reasons = loads((await _row(db, r.job_id))["reasons"])
    assert len(reasons) == 20
    assert reasons[0]["why"] == "r24"
    assert (await _row(db, r.job_id))["enqueue_count"] == 25


@pytest.mark.asyncio
async def test_debounce_extends_but_is_capped(db) -> None:
    lib = await make_library(db)
    first = await enqueue_job(db, subject_kind="library", subject_id=lib, stage="detect")
    row = await _row(db, first.job_id)
    created = parse_ts(row["created_at"])
    nb1 = parse_ts(row["not_before"])
    assert nb1 is not None and created is not None
    assert timedelta(seconds=25) < nb1 - created <= timedelta(seconds=31)  # detect: 30 s

    # pretend the row is 290 s old: the next trigger may push to 30 s from now
    # but never beyond created_at + 300 s
    old = now() - timedelta(seconds=290)
    await db.execute(
        "UPDATE processing_jobs SET created_at = $2 WHERE id = $1", first.job_id, to_ts(db, old)
    )
    again = await enqueue_job(db, subject_kind="library", subject_id=lib, stage="detect")
    assert again.outcome == "coalesced"
    nb2 = parse_ts((await _row(db, first.job_id))["not_before"])
    assert nb2 is not None
    assert abs((nb2 - (old + timedelta(seconds=300))).total_seconds()) < 1.5

    # an early trigger pushes it forward but within the cap
    fresh = await make_library(db)
    f1 = await enqueue_job(db, subject_kind="library", subject_id=fresh, stage="detect")
    nb_a = parse_ts((await _row(db, f1.job_id))["not_before"])
    await asyncio.sleep(0.05)
    await enqueue_job(db, subject_kind="library", subject_id=fresh, stage="detect")
    nb_b = parse_ts((await _row(db, f1.job_id))["not_before"])
    assert nb_a is not None and nb_b is not None and nb_b >= nb_a


@pytest.mark.asyncio
async def test_no_debounce_stage_has_no_not_before(db) -> None:
    lib = await make_library(db)
    v = await make_video(db, lib)
    r = await enqueue_job(db, subject_kind="video", subject_id=v, stage="probe")
    assert (await _row(db, r.job_id))["not_before"] is None


@pytest.mark.asyncio
@pytest.mark.parametrize("state", ["claimed", "running", "resuming", "paused"])
async def test_trigger_on_active_row_requests_rerun(db, state: str) -> None:
    lib = await make_library(db)
    v = await make_video(db, lib)
    first = await enqueue_job(db, subject_kind="video", subject_id=v, stage="probe", priority=100)
    await db.execute("UPDATE processing_jobs SET state = $2 WHERE id = $1", first.job_id, state)
    second = await enqueue_job(
        db, subject_kind="video", subject_id=v, stage="probe", priority=10, force=True,
        reason={"why": "changed mid-run"},
    )  # fmt: skip
    assert (second.outcome, second.job_id) == ("rerun_requested", first.job_id)
    row = await _row(db, first.job_id)
    assert bool(row["rerun_requested"]) is True
    assert bool(row["rerun_force"]) is True
    assert row["priority"] == 100  # an active row's own settings are untouched
    assert bool(row["force"]) is False
    assert [r["why"] for r in loads(row["rerun_reasons"])] == ["changed mid-run"]
    assert await _count(db) == 1


@pytest.mark.asyncio
async def test_finished_rows_do_not_block_a_new_one(db) -> None:
    lib = await make_library(db)
    v = await make_video(db, lib)
    first = await enqueue_job(db, subject_kind="video", subject_id=v, stage="probe")
    await db.execute("UPDATE processing_jobs SET state = 'done' WHERE id = $1", first.job_id)
    second = await enqueue_job(db, subject_kind="video", subject_id=v, stage="probe")
    assert second.outcome == "inserted" and second.job_id != first.job_id


@pytest.mark.asyncio
async def test_unknown_disabled_and_invalid(db) -> None:
    lib = await make_library(db)
    v = await make_video(db, lib)
    assert (await enqueue_job(db, subject_kind="video", subject_id=v, stage="nope")).outcome == (
        "unknown_stage"
    )
    # stage exists but not for this kind
    assert (
        await enqueue_job(db, subject_kind="library", subject_id=lib, stage="probe")
    ).outcome == ("invalid_subject")
    # subject does not exist
    assert (
        await enqueue_job(db, subject_kind="video", subject_id=uuid4(), stage="probe")
    ).outcome == "invalid_subject"
    # catalog only accepts the fixed id
    assert (
        await enqueue_job(db, subject_kind="catalog", subject_id=uuid4(), stage="plan_channels")
    ).outcome == "invalid_subject"
    ok = await enqueue_job(db, subject_kind="catalog", subject_id=CATALOG_ID, stage="plan_channels")
    assert ok.outcome == "inserted"
    await db.execute("UPDATE job_stages SET enabled = $1 WHERE stage = 'probe'", False)
    res = await enqueue_job(db, subject_kind="video", subject_id=v, stage="probe")
    assert (res.outcome, res.job_id) == ("disabled", None)
    assert await _count(db) == 1  # only the catalog row


@pytest.mark.asyncio
async def test_subject_columns_per_kind(db) -> None:
    lib = await make_library(db)
    series_id, season_id = await make_series_and_season(db, lib)
    r = await enqueue_job(db, subject_kind="season", subject_id=season_id, stage="tag")
    row = await _row(db, r.job_id)
    assert str(row["season_id"]) == str(season_id)
    assert row["video_id"] is None
    assert str(row["library_id"]) == str(lib)
    r2 = await enqueue_job(db, subject_kind="library", subject_id=lib, stage="youtube_sync")
    assert str((await _row(db, r2.job_id))["library_id"]) == str(lib)
    assert series_id  # series itself is not a stage subject (it expands to seasons)


@pytest.mark.asyncio
async def test_fifty_concurrent_enqueues_make_one_row(db) -> None:
    lib = await make_library(db)
    v = await make_video(db, lib)
    results = await asyncio.gather(
        *[
            enqueue_job(
                db, subject_kind="video", subject_id=v, stage="probe", reason={"why": f"t{i}"}
            )
            for i in range(50)
        ]
    )
    assert await _count(db) == 1
    assert sorted(r.outcome for r in results).count("inserted") == 1
    assert {r.job_id for r in results} == {results[0].job_id}
    assert (await _row(db, results[0].job_id))["enqueue_count"] == 50


@pytest.mark.asyncio
async def test_payload_seasons_are_unioned_other_keys_keep_first(db) -> None:
    lib = await make_library(db)
    v = await make_video(db, lib)
    a, b = str(uuid4()), str(uuid4())
    first = await enqueue_job(
        db, subject_kind="video", subject_id=v, stage="fingerprint",
        payload={"role": "sample", "seasons": [a]},
    )  # fmt: skip
    await enqueue_job(
        db, subject_kind="video", subject_id=v, stage="fingerprint",
        payload={"role": "candidate", "seasons": [b, a]},
    )  # fmt: skip
    payload = loads((await _row(db, first.job_id))["payload"])
    assert payload["role"] == "sample"
    assert sorted(payload["seasons"]) == sorted([a, b])


@pytest.mark.asyncio
async def test_job_events_record_the_trigger(db) -> None:
    lib = await make_library(db)
    v = await make_video(db, lib)
    r = await enqueue_job(
        db, subject_kind="video", subject_id=v, stage="probe", reason={"why": "x"}
    )
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="probe", reason={"why": "y"})
    kinds = [
        x["kind"]
        for x in await db.fetch(
            "SELECT kind FROM job_events WHERE job_id = $1 ORDER BY id", r.job_id
        )
    ]
    assert kinds == ["enqueued", "coalesced"]


@pytest.mark.unit
def test_pure_helpers() -> None:
    assert prepend_reason([{"a": 1}] * 20, {"b": 2})[0] == {"b": 2}
    assert len(prepend_reason([{"a": 1}] * 20, {"b": 2})) == 20
    assert merge_payload(None, {"x": 1}) == {"x": 1}
    assert merge_payload({"x": 1}, None) == {"x": 1}
    assert merge_payload({"x": 1, "seasons": ["a"]}, {"seasons": ["b"], "x": 2}) == {
        "x": 1,
        "seasons": ["a", "b"],
    }
