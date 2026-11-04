"""Shared setup for the component runner tests (Epic 51, story 51.06)."""

from __future__ import annotations

from typing import Any

from maktaba_pipeline.engine.sql import is_pg, now, to_ts


async def set_want(db: Any, cid: str, want: str, *, hold: bool = False) -> None:
    """Write ``cid``'s desired row on the home (and the hold, when ``hold``)."""
    ts = to_ts(db, now())
    await db.execute(
        "INSERT INTO components (id, want, hold_by, hold_at, updated_by)"
        " VALUES ($1, $2, $3, $4, 'test')"
        " ON CONFLICT (id) DO UPDATE SET want = EXCLUDED.want, hold_by = EXCLUDED.hold_by,"
        " hold_at = EXCLUDED.hold_at",
        cid,
        want,
        "admin:test" if hold else None,
        ts if hold else None,
    )


async def stage_row(db: Any, stage: str) -> tuple[bool, str | None]:
    r = await db.fetchrow("SELECT enabled, disabled_reason FROM job_stages WHERE stage = $1", stage)
    assert r is not None
    return bool(r["enabled"]), r["disabled_reason"]


async def set_stage(db: Any, stage: str, enabled: bool, reason: str | None) -> None:
    await db.execute(
        "UPDATE job_stages SET enabled = $2, disabled_reason = $3 WHERE stage = $1",
        stage,
        enabled if is_pg(db) else int(enabled),
        reason,
    )
