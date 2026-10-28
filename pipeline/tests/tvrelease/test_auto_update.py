"""Epic 35 story 35.6: the automatic updater's TV change of the streaming binary is a
change of its own (no update joins it, it joins no one's), staged as a binary-only change
promoted without an approval."""

from __future__ import annotations

from datetime import UTC, datetime, timedelta

import pytest

from maktaba_pipeline.engine.sql import parse_ts
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.tvrelease.config import MaktabaTvStagingConfig, auto_rule
from maktaba_pipeline.tvrelease.stage import collapse_open, stage_change
from maktaba_pipeline.tvrelease.store import create_change, get_change, open_change, open_changes
from tests.tvrelease.helpers import NOW, add_channel, library_with, set_staging

pytestmark = pytest.mark.integration


_SHA = "ab" * 32


def test_only_manual_changes_need_approval() -> None:
    cfg = MaktabaTvStagingConfig(auto_promote_binary="approval", auto_promote_lineup="approval")
    assert auto_rule(cfg, ["binary"], "auto_update") == "immediate"
    assert auto_rule(cfg, ["binary"], "manual") == "approval"
    assert auto_rule(cfg, ["binary"]) == "approval"
    # automatic: a stored "approval" from before the owner's rule reads as immediate
    assert auto_rule(cfg, ["lineup", "binary"], "auto_update") == "immediate"
    quiet = MaktabaTvStagingConfig(
        auto_promote_lineup="quiet_window", auto_promote_binary="quiet_window"
    )
    assert auto_rule(quiet, ["lineup", "binary"], "auto_update") == "quiet_window"
    assert auto_rule(quiet, ["binary"], "auto_update") == "immediate"


@pytest.mark.asyncio
async def test_the_updaters_change_is_its_own(db: Database) -> None:
    upd = await create_change(
        db, state="draft", title="Maktaba 1.5.0", source={"kind": "auto_update", "run_id": "r1"},
        pending={"streaming": ["v1.5.0", _SHA]}, actor_source="auto_update",
    )  # fmt: skip
    assert await open_change(db) is None  # an update's save would open a change of its own
    mine = await create_change(
        db, state="draft", title="TV settings", source={"kind": "settings"},
        pending={"settings": {"tv": {"guide_hours": 6}}}, actor_source="settings",
    )  # fmt: skip
    assert [c.id for c in await open_changes(db)] == [mine]
    got = await collapse_open(db)
    assert got is not None and got.id == mine
    still = await get_change(db, upd)
    assert still is not None and still.state == "draft"
    assert still.pending == {"streaming": ["v1.5.0", _SHA]}


@pytest.mark.asyncio
async def test_the_updaters_change_stages_binary_only(db: Database) -> None:
    await set_staging(db, enabled=True)
    lib = await library_with(db, 4)
    await add_channel(db, lib, 101)
    upd = await create_change(
        db, state="draft", title="Maktaba 1.5.0", source={"kind": "auto_update", "run_id": "r1"},
        pending={"streaming": ["v1.5.0", _SHA]}, actor_source="auto_update",
    )  # fmt: skip
    assert await stage_change(db, upd, clock=NOW) == "staged"
    ch = await get_change(db, upd)
    assert ch is not None and ch.row["kinds"] == ["binary"]
    assert ch.row["auto_rule"] == "immediate"
    rel = await db.fetchrow(
        "SELECT streaming_version, streaming_sha256 FROM tv_releases WHERE id = $1",
        ch.row["release_id"],
    )
    assert rel is not None
    assert (rel["streaming_version"], rel["streaming_sha256"]) == ("v1.5.0", _SHA)


@pytest.mark.asyncio
async def test_staged_at_is_when_the_change_became_staged(db: Database) -> None:
    """The 0.3.36 update (2026-10-27): its tv_stage job began at 21:24:48 and staged the
    change at 21:29:17, but staged_at said 21:24:48 (the job's clock). The controller
    times the staging slot's start from staged_at, so it failed the slot on its first
    look. staged_at is when the change became staged."""
    await set_staging(db, enabled=True)
    lib = await library_with(db, 4)
    await add_channel(db, lib, 101)
    upd = await create_change(
        db, state="draft", title="Maktaba 0.3.36", source={"kind": "auto_update", "run_id": "r1"},
        pending={"streaming": ["v0.3.36", _SHA]}, actor_source="auto_update",
    )  # fmt: skip
    job_began = datetime.now(UTC) - timedelta(minutes=4, seconds=30)  # a long build
    before = datetime.now(UTC)
    assert await stage_change(db, upd, clock=job_began) == "staged"
    ch = await get_change(db, upd)
    assert ch is not None
    staged_at = parse_ts(ch.row["staged_at"])
    assert staged_at is not None
    assert staged_at >= before - timedelta(seconds=1), (staged_at, job_began)
