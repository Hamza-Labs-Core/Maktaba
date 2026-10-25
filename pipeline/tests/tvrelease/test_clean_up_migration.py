"""Slot 0142: the TV changes the old rule left are cleaned up (story 33.12 AC3)."""

from __future__ import annotations

import pytest

from maktaba_pipeline.engine.sql import is_pg, now, to_ts
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.tvrelease.store import create_change, get_change, update_change

pytestmark = pytest.mark.integration


async def _change(db: Database, state: str, *, aired: bool = False) -> str:
    cid = await create_change(
        db, state="draft", title=state, source={"kind": "write_channels"}, pending={"sched": []}
    )
    await db.execute(
        "UPDATE tv_changes SET state = $2, swapped_at = $3 WHERE id = $1",
        cid,
        state,
        to_ts(db, now()) if aired else None,
    )
    return cid


async def _run(db: Database) -> None:
    from tests.engine.migrate import MIGRATIONS_DIR, up_statements

    suffix = ".sql" if is_pg(db) else ".sqlite.sql"
    sql = (MIGRATIONS_DIR / f"0142_tv_changes_clean_up{suffix}").read_text(encoding="utf-8")
    for stmt in up_statements(sql):
        await db.execute(stmt)


@pytest.mark.asyncio
async def test_slot_0142_cleans_up_the_old_changes(db: Database) -> None:
    """Failed and superseded changes that never aired go with their events; an older
    plan still waiting in ready is cancelled; live, what aired, an approved draft and
    the newest open draft are kept. A second run changes nothing."""
    failed = [await _change(db, "failed") for _ in range(2)]
    superseded = await _change(db, "superseded")
    aired = await _change(db, "superseded", aired=True)
    await update_change(db, aired, superseded_by=superseded)
    live = await _change(db, "live", aired=True)
    ready = await _change(db, "ready")
    approved_draft = await _change(db, "draft")
    await update_change(db, approved_draft, approved_by=["someone"])
    older_draft = await _change(db, "draft")
    draft = await _change(db, "draft")
    newer_approved = await _change(db, "draft")  # approved: not the open draft
    await update_change(db, newer_approved, approved_by=["x"])
    for _ in range(2):
        await _run(db)
        for gone in (*failed, superseded):
            assert await get_change(db, gone) is None
            assert (
                await db.fetchval(
                    "SELECT count(*) FROM tv_change_events WHERE change_id = $1", gone
                )
                == 0
            )
        kept = await get_change(db, aired)
        assert kept is not None and kept.state == "superseded"
        assert kept.row["superseded_by"] is None
        for cid, state in (
            (live, "live"),
            (approved_draft, "draft"),
            (older_draft, "draft"),
            (draft, "draft"),
            (newer_approved, "draft"),
        ):
            ch = await get_change(db, cid)
            assert ch is not None and ch.state == state
        old = await get_change(db, ready)
        assert old is not None and old.state == "cancelled"
        assert old.row["end_reason"] == "cleaned_up" and old.row["ended_at"] is not None
        assert (
            await db.fetchval(
                "SELECT count(*) FROM tv_change_events WHERE change_id = $1 AND action = 'cancel'",
                ready,
            )
            == 1
        )
