# ruff: noqa: E501  (long SQL strings read better unwrapped)
"""The deterministic Postgres seed workstream B loads in its integration tests."""

from __future__ import annotations

from pathlib import Path

import pytest

from maktaba_pipeline.runtime import Database

SEED = Path(__file__).resolve().parents[1] / "fixtures" / "queue_seed.sql"


@pytest.mark.asyncio
async def test_seed_loads_on_the_migrated_schema_and_covers_every_state(db: Database) -> None:
    if db.dialect != "postgres":
        pytest.skip("the seed is Postgres SQL (jsonb, generate_series)")
    await db.execute(SEED.read_text(encoding="utf-8"))

    states = {r["state"] for r in await db.fetch("SELECT DISTINCT state FROM processing_jobs")}
    assert states == {"pending", "claimed", "running", "paused", "done", "failed", "cancelled"}
    outcomes = {
        r["outcome"]
        for r in await db.fetch(
            "SELECT DISTINCT outcome FROM processing_jobs WHERE outcome IS NOT NULL"
        )
    }
    assert outcomes == {
        "changed", "unchanged", "skipped_unchanged", "imported", "awaiting_review", "waiting_inputs",
    }  # fmt: skip

    # open issues = subjects whose LATEST row for the stage is failed (plan section 6.3)
    groups = await db.fetch(
        "SELECT stage, error_cause, count(*) AS n FROM ("
        " SELECT DISTINCT ON (subject_kind, subject_id, stage) stage, state, error_cause"
        "   FROM processing_jobs ORDER BY subject_kind, subject_id, stage, id DESC) latest"
        " WHERE state = 'failed' GROUP BY stage, error_cause ORDER BY stage, error_cause"
    )
    assert [(g["stage"], g["error_cause"], g["n"]) for g in groups] == [
        ("fingerprint", "nas_timeout", 2),
        ("style_sample", "nas_timeout", 1),
        ("tag", "graph_violation", 1),
        ("youtube_sync", "youtube_bot_check", 1),
    ]
    # the retried thumb_hash failure (job 21) is not an open issue: job 22 superseded it
    assert not [g for g in groups if g["stage"] == "thumb_hash"]

    assert await db.fetchval("SELECT count(*) FROM health_samples") == 150
    assert await db.fetchval("SELECT count(*) FROM workers") == 2
    # a new job takes an id above the seeded range
    nxt = await db.fetchval(
        "INSERT INTO processing_jobs (video_id, stage) VALUES ('00000000-0000-0000-0000-000000000b06', 'thumbnail') RETURNING id"
    )
    assert nxt >= 1000
