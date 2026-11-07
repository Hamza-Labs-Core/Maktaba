"""The TV release code's statements are fixed texts (Epic 54 wave 7, story 54.34): each
constant written out for that agrees with what it stands for, and a change's update sets
any of its columns through one statement."""

from __future__ import annotations

import pytest

from maktaba_pipeline.runtime import Database
from maktaba_pipeline.tvrelease import gc, snapshot, store


def marks(xs: tuple[str, ...]) -> str:
    return ",".join(f"'{s}'" for s in xs)


def test_the_written_out_lists_agree() -> None:
    assert ", ".join(snapshot.DEF_COLUMNS) == snapshot.DEF_SELECT
    assert marks(gc.ENDED) == gc._ENDED_MARKS
    assert marks(store.IN_FLIGHT) == gc._IN_FLIGHT_MARKS
    assert marks((*store.IN_FLIGHT, "draft")) == gc._IN_FLIGHT_OR_DRAFT
    assert "IN (" + ", ".join(f"'{s}'" for s in store.IN_FLIGHT) + ")" in store._IN_FLIGHT_SQL
    assert "IN (" + ", ".join(f"'{s}'" for s in store.OPEN) + ")" in store._OPEN_SQL


@pytest.mark.asyncio
async def test_an_update_may_set_every_column_but_the_identity(db: Database) -> None:
    if db.dialect == "postgres":
        rows = await db.fetch(
            "SELECT column_name AS c FROM information_schema.columns"
            " WHERE table_name = 'tv_changes' AND table_schema = current_schema()"
        )
    else:
        rows = await db.fetch("SELECT name AS c FROM pragma_table_info('tv_changes')")
    cols = {str(r["c"]) for r in rows} - {"id", "number", "created_at", "updated_at"}
    assert set(store.UPDATE_FIELDS) == cols


@pytest.mark.asyncio
async def test_an_update_sets_what_it_is_given_and_keeps_the_rest(db: Database) -> None:
    cid = await store.create_change(
        db, state="draft", title="t", source={"kind": "manual"}, pending={}
    )
    await store.update_change(db, cid, title="new", verify_report={"ok": True})
    ch = await store.get_change(db, cid)
    assert ch is not None
    assert ch.row["title"] == "new" and ch.row["verify_report"] == {"ok": True}
    assert ch.state == "draft" and ch.source == {"kind": "manual"}
    with pytest.raises(ValueError, match="no writable column"):
        await store.update_change(db, cid, nonsense=1)
