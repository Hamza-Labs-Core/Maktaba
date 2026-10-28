"""A TV held on its old streaming software (the owner's rule of 2026-10-28, #404: when
only an update's TV check fails, the update stays installed and the TV stays on its old
version until the update's Retry passes or a later release's own binary change passes its
check).

On 2026-10-28 the live TV was held on v0.3.39 while the pipeline of 0.3.40 built every
schedule release with streaming_version "0.3.40": staging it ran the new binary, and its
swap would have moved the held TV onto the software whose check had just failed. A release
that brings no binary of its own records what the live TV runs."""

from __future__ import annotations

import pytest

from maktaba_pipeline.runtime import Database
from maktaba_pipeline.tvrelease.snapshot import live_streaming, names_binary, same_streaming
from maktaba_pipeline.tvrelease.stage import stage_change
from maktaba_pipeline.tvrelease.store import create_change, get_change
from tests.tvrelease.helpers import NOW, add_channel, library_with, set_staging

_SHA = "cd" * 32


async def _live_slot(db: Database, version: str, state: str = "healthy") -> None:
    await db.execute(
        "INSERT INTO tv_slots (host_id, slot, role, role_epoch, state, version)"
        " VALUES ('h1', 'green', 'live', 1, $1, $2)"
        " ON CONFLICT (host_id, slot)"
        " DO UPDATE SET version = EXCLUDED.version, state = EXCLUDED.state",
        state,
        version,
    )


async def _schedule_change(db: Database, channel: str) -> tuple[list[str], str, str]:
    ch_id = await create_change(
        db, state="draft", title="regenerate", source={"kind": "schedule"},
        pending={"sched": [channel]}, actor_source="schedule",
    )  # fmt: skip
    assert await stage_change(db, ch_id, clock=NOW) == "staged"
    ch = await get_change(db, ch_id)
    assert ch is not None
    rel = await db.fetchrow(
        "SELECT streaming_version, streaming_sha256 FROM tv_releases WHERE id = $1",
        ch.row["release_id"],
    )
    assert rel is not None
    return list(ch.row["kinds"]), str(rel["streaming_version"]), str(rel["streaming_sha256"] or "")


@pytest.mark.asyncio
async def test_a_schedule_change_during_a_hold_stages_on_the_held_version(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    # 1.5.0 is installed (its pipeline), the live TV held on 1.4.0
    monkeypatch.setenv("MAKTABA_STREAMING_VERSION", "1.5.0")
    await set_staging(db, enabled=True)
    lib = await library_with(db, 4)
    cid = await add_channel(db, lib, 101)
    await _live_slot(db, "v1.4.0")
    kinds, version, _ = await _schedule_change(db, cid)
    assert version == "v1.4.0", "a schedule change during a hold would move the held TV"
    assert "binary" not in kinds


@pytest.mark.asyncio
async def test_after_the_retry_passes_the_next_change_uses_the_new_version(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("MAKTABA_STREAMING_VERSION", "1.5.0")
    await set_staging(db, enabled=True)
    lib = await library_with(db, 4)
    cid = await add_channel(db, lib, 101)
    await _live_slot(db, "v1.4.0")
    # the update's binary change, staged against the held 1.4.0: a binary change
    upd = await create_change(
        db, state="draft", title="Maktaba 1.5.0", source={"kind": "auto_update", "run_id": "r1"},
        pending={"streaming": ["v1.5.0", _SHA]}, actor_source="auto_update",
    )  # fmt: skip
    assert await stage_change(db, upd, clock=NOW) == "staged"
    got = await get_change(db, upd)
    assert got is not None and got.row["kinds"] == ["binary"]
    # its Retry passed and swapped: the live slot runs 1.5.0
    await _live_slot(db, "v1.5.0")
    assert await live_streaming(db) == ("v1.5.0", _SHA)
    await db.execute("UPDATE tv_changes SET state = 'live' WHERE id = $1", upd)
    kinds, version, sha = await _schedule_change(db, cid)
    assert (version, sha) == ("v1.5.0", _SHA)
    assert "binary" not in kinds


@pytest.mark.asyncio
async def test_live_streaming_without_a_slot_report(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("MAKTABA_STREAMING_VERSION", "1.5.0")
    monkeypatch.setenv("MAKTABA_STREAMING_SHA256", "")
    # no TV slot has run: this machine's
    assert await live_streaming(db) == ("1.5.0", "")
    # a slot that reports no release version ("unknown", a source build) is passed over
    await _live_slot(db, "unknown")
    assert await live_streaming(db) == ("1.5.0", "")
    # a stopped live slot's last report still says what the TV was held on
    await _live_slot(db, "v1.4.0", state="stopped")
    assert await live_streaming(db) == ("v1.4.0", "")


def test_version_spellings() -> None:
    assert same_streaming(("v1.4.0", ""), ("1.4.0", _SHA))
    assert not same_streaming(("v1.4.0", "aa"), ("1.4.0", "bb"))
    assert not same_streaming(("1.4.0", ""), ("1.5.0", ""))
    assert names_binary("v0.3.39") and names_binary("0.3.40")
    assert not names_binary("0.0.1") and not names_binary("") and not names_binary("unknown")
