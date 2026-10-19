# ruff: noqa: E501  (long SQL strings read better unwrapped)
"""Story 33.2: TV releases and snapshots (TC1, TC2, TC5) and the tv_staging defaults
(story 33.10 AC6)."""

from __future__ import annotations

import hashlib
from pathlib import Path

import pytest

from maktaba_pipeline import rules
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.tvrelease.build import apply_pending, bootstrap, build_release
from maktaba_pipeline.tvrelease.config import CHANGE_KINDS, MaktabaTvStagingConfig, auto_rule
from maktaba_pipeline.tvrelease.snapshot import content_digest, lineup_digest, live_snapshot
from tests.tvrelease.helpers import add_channel, library_with


def test_defaults_every_kind_needs_approval() -> None:
    cfg = MaktabaTvStagingConfig()
    assert cfg.enabled is True  # owner decision: staging on by default
    assert cfg.swap_wait == "boundary" and cfg.hot_standby is True
    for kind in CHANGE_KINDS:
        assert getattr(cfg, f"auto_promote_{kind}") == "approval"
    assert auto_rule(cfg, ["lineup", "schedule"]) == "approval"
    loose = MaktabaTvStagingConfig(auto_promote_lineup="immediate", auto_promote_art="quiet_window")
    assert auto_rule(loose, ["lineup"]) == "immediate"
    assert auto_rule(loose, ["lineup", "art"]) == "quiet_window"  # the strictest wins
    assert auto_rule(loose, ["lineup", "schedule"]) == "approval"
    assert "tv_staging" in {s["key"] for s in rules.schema()["sections"]}


def test_release_digest_stable() -> None:
    """TC1: same inputs, same digest; order-independent."""
    a = {"defs": {"x": {"id": "x", "number": 1, "mode_config": {"a": 1, "b": 2}},
                  "y": {"id": "y", "number": 2}},
         "art": {}, "settings": {"tv": {"k": 1}, "tv_output": {}}}  # fmt: skip
    b = {"settings": {"tv_output": {}, "tv": {"k": 1}}, "art": {},
         "defs": {"y": {"number": 2, "id": "y"},
                  "x": {"mode_config": {"b": 2, "a": 1}, "number": 1, "id": "x"}}}  # fmt: skip
    assert content_digest(a, ("1", "")) == content_digest(b, ("1", ""))
    assert content_digest(a, ("1", "")) != content_digest(a, ("2", ""))
    c = apply_pending(a, {"channels": {"x": {"number": 3}}})
    assert content_digest(c, ("1", "")) != content_digest(a, ("1", ""))


@pytest.mark.asyncio
async def test_release_bootstrap_from_live(db: Database) -> None:
    """TC2 / AC2: release 1 is what is live; tv_routing names it at generation 0."""
    lib = await library_with(db, 2)
    a = await add_channel(db, lib, 101)
    b = await add_channel(db, lib, 102)
    rid = await bootstrap(db)
    assert await bootstrap(db) == rid  # once
    row = await db.fetchrow(
        "SELECT live_release_id, live_gen, epoch FROM tv_routing WHERE scope = 'cluster'"
    )
    assert str(row["live_release_id"]) == rid and int(row["live_gen"]) == 0
    rel = await db.fetchrow("SELECT kinds, sched_gen FROM tv_releases WHERE id = $1", rid)
    assert int(rel["sched_gen"]) == 0 and "bootstrap" in str(rel["kinds"])
    got = {
        str(r["channel_id"])
        for r in await db.fetch(
            "SELECT channel_id FROM tv_release_channels WHERE release_id = $1", rid
        )
    }
    assert got == {a, b}


@pytest.mark.asyncio
async def test_noop_change_builds_no_release(db: Database) -> None:
    """Story 33.10 EC1: a change identical to live is a noop."""
    lib = await library_with(db, 2)
    cid = await add_channel(db, lib, 101)
    snap = await live_snapshot(db)
    rel, _, kinds = await build_release(db, {"channels": {cid: dict(snap["defs"][cid])}})
    assert rel is None and kinds == []
    assert await db.fetchval("SELECT count(*) FROM tv_releases") == 1  # the bootstrap only


@pytest.mark.asyncio
async def test_art_content_addressed(db: Database, tmp_path: Path) -> None:
    """TC5: art is identified by its content; a new file makes a new lineup digest, the
    old file stays for the old release."""
    lib = await library_with(db, 1)
    cid = await add_channel(db, lib, 101)
    old = tmp_path / "logo-aaaa.png"
    old.write_bytes(b"old picture")
    await db.execute(
        "INSERT INTO channel_art_overrides (channel_id, kind, path) VALUES ($1, 'logo', $2)",
        cid,
        str(old),
    )
    snap = await live_snapshot(db)
    assert snap["art"][cid]["logo"]["sha256"] == hashlib.sha256(b"old picture").hexdigest()
    d1 = lineup_digest(snap["defs"], snap["art"])
    new = tmp_path / "logo-bbbb.png"
    new.write_bytes(b"new picture")
    target = apply_pending(
        snap, {"art": {cid: {"logo": {"path": str(new), "settings": None, "base": None,
                                     "sha256": hashlib.sha256(b"new picture").hexdigest()}}}}
    )  # fmt: skip
    assert lineup_digest(target["defs"], target["art"]) != d1
    assert old.exists()  # nothing removes the picture the live release shows
    rel, diff, kinds = await build_release(db, {"art": {cid: {"logo": target["art"][cid]["logo"]}}})
    assert rel is not None and kinds == ["art"]
    assert diff["art"] and diff["art"][0]["id"] == cid
