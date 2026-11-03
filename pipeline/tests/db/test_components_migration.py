"""Slot 0173 (story 51.02) on both dialects: the component tables, the change signal
(revision and generation), the catch-up stage row, and the pipeline's store over them.
On Postgres also the guarded writes: a machine's login reports its own status and no
other machine's, and writes no component table directly."""

from __future__ import annotations

import asyncio
import uuid
from datetime import UTC, datetime
from typing import Any

import pytest

from maktaba_pipeline.components import store
from maktaba_pipeline.components.store import HOME, MAX_DETAIL_BYTES, Condition, Status
from maktaba_pipeline.engine.sql import is_pg, to_ts
from maktaba_pipeline.runtime import Database
from tests.engine.fixtures_db import PG_URL

NODE = "11111111-1111-1111-1111-111111111111"


async def _set(db: Database, cid: str, want: str) -> None:
    """A desired write the way the API makes it (the pipeline never writes these)."""
    await db.execute(
        "INSERT INTO components (id, want, updated_by) VALUES ($1, $2, 'test')"
        " ON CONFLICT (id) DO UPDATE SET want = excluded.want",
        cid,
        want,
    )


async def _node(db: Database, node_id: str, role: str | None = None) -> None:
    await db.execute(
        "INSERT INTO cluster_nodes (id, name, state, os, arch, node_pubkey, db_role)"
        " VALUES ($1, $2, 'approved', 'linux', 'amd64', $3, $4)",
        node_id,
        "n-" + node_id[:4],
        "k-" + node_id,
        role,
    )


@pytest.mark.asyncio
async def test_tables_meta_and_stage_row(db: Database) -> None:
    assert await db.fetchval("SELECT count(*) FROM component_meta") == 1
    assert await store.revision(db) == 0
    row = await db.fetchrow(
        "SELECT pool, placement, enabled, hidden, lane FROM job_stages"
        " WHERE stage = 'component_catchup'"
    )
    assert row is not None
    assert (row["pool"], row["placement"], row["lane"]) == ("cpu", "home", "media")
    assert bool(row["enabled"]) and bool(row["hidden"])
    with pytest.raises(Exception):  # noqa: B017 - either driver's check violation
        await db.execute("INSERT INTO components (id, want, updated_by) VALUES ('x', 'on', 't')")


@pytest.mark.asyncio
async def test_changes_move_revision_and_generation(db: Database) -> None:
    await _set(db, "transcription", "enabled")
    await _set(db, "transcription", "installed")
    got, rev = await store.desired(db)
    assert rev == 2
    assert got["transcription"].want == "installed"
    assert got["transcription"].generation == 2
    await db.execute("DELETE FROM components WHERE id = 'transcription'")
    assert await store.revision(db) == 3


@pytest.mark.asyncio
async def test_a_nodes_desired_state_merges_its_overrides(db: Database) -> None:
    await _node(db, NODE)
    await _set(db, "transcription", "enabled")
    await _set(db, "llm", "enabled")
    await db.execute(
        "INSERT INTO component_overrides (node_id, id, want, hold_by, hold_at, updated_by)"
        " VALUES ($1, 'transcription', 'installed', 'admin', $2, 'test')",
        NODE,
        to_ts(db, datetime(2026, 11, 3, 9, tzinfo=UTC)),
    )
    await db.execute(
        "INSERT INTO component_overrides (node_id, id, want, updated_by)"
        " VALUES ($1, 'chapters', 'enabled', 'test')",
        NODE,
    )
    got, _ = await store.desired(db, NODE)
    assert {k: v.want for k, v in got.items()} == {
        "transcription": "installed",
        "llm": "enabled",
        "chapters": "enabled",
    }
    tr = got["transcription"]
    assert tr.hold is not None and tr.hold.by == "admin" and tr.hold.at.hour == 9
    assert tr.generation == 2  # the home's 1 + the override's 1
    home, _ = await store.desired(db)
    assert home["transcription"].want == "enabled" and "chapters" not in home


@pytest.mark.asyncio
async def test_report_prunes_keeps_since_and_caps_detail(db: Database) -> None:
    """T10: a second report with fewer rows drops the missing one; since stays while the
    phase does; another part's rows are untouched."""
    rows = [
        Status(
            id="transcription",
            phase="running",
            observed_generation=3,
            conditions=(Condition(type="Ready", status="true"),),
        ),
        Status(
            id="chapters",
            phase="waiting",
            conditions=(
                Condition(
                    type="RequirementsMet",
                    status="false",
                    reason="dependency_off",
                    params={"component": "llm"},
                ),
            ),
        ),
    ]
    await store.report_status(db, HOME, "pipeline", rows, version="0.3.66")
    await store.report_status(db, HOME, "api", [Status(id="transcription", phase="failed")])
    first = {(s.id, s.part): s for s in await store.statuses(db)}
    assert len(first) == 3
    assert first[("chapters", "pipeline")].reason == "dependency_off"
    assert first[("chapters", "pipeline")].conditions[0].params == {"component": "llm"}
    await asyncio.sleep(0.01)
    await store.report_status(db, HOME, "pipeline", rows[:1], version="0.3.66")
    after = {(s.id, s.part): s for s in await store.statuses(db)}
    assert set(after) == {("transcription", "pipeline"), ("transcription", "api")}
    tp = after[("transcription", "pipeline")]
    assert (tp.version, tp.observed_generation) == ("0.3.66", 3)
    assert tp.since == first[("transcription", "pipeline")].since
    first_tp = first[("transcription", "pipeline")]
    assert first_tp.since is not None and first_tp.updated_at is not None
    assert tp.updated_at is not None and tp.updated_at > first_tp.updated_at
    rolled = store.rollup(await store.statuses(db, "transcription"))
    assert rolled is not None and rolled.phase == "failed"
    big: dict[str, Any] = {"log": "x" * MAX_DETAIL_BYTES}
    await store.report_status(
        db, HOME, "pipeline", [Status(id="transcription", phase="stopped", detail=big)]
    )
    tp = {(s.id, s.part): s for s in await store.statuses(db)}[("transcription", "pipeline")]
    assert tp.detail == {"truncated": True}
    assert tp.since is not None and tp.since > first_tp.since
    await store.report_status(db, HOME, "pipeline", [])
    assert [(s.id, s.part) for s in await store.statuses(db)] == [("transcription", "api")]


@pytest.mark.asyncio
async def test_an_unknown_reason_writes_nothing(db: Database) -> None:
    """T11: a report with one bad row is refused whole, before any write."""
    rows = [
        Status(id="llm", phase="running"),
        Status(
            id="chapters",
            phase="waiting",
            conditions=(Condition(type="Ready", status="false", reason="nonsense"),),
        ),
    ]
    with pytest.raises(store.StatusError, match="unknown reason 'nonsense'"):
        await store.report_status(db, HOME, "pipeline", rows)
    assert await store.statuses(db) == []


def _swap(url: str, *, db: str, user: str) -> str:
    base, _, _ = url.rpartition("/")
    scheme, _, rest = base.partition("://")
    host = rest.rpartition("@")[2]
    return f"{scheme}://{user}@{host}/{db}"


@pytest.mark.asyncio
async def test_machines_write_only_their_own_status(db: Database) -> None:
    """T04 + T05 on Postgres: a machine's login (in maktaba_worker) has no direct writes
    on the component tables; through component_status_report it writes its own rows and
    is refused another machine's."""
    if not is_pg(db):
        pytest.skip("roles and guarded functions are Postgres's")
    import asyncpg  # type: ignore[import-untyped]

    name = await db.fetchval("SELECT current_database()")
    a, b = str(uuid.uuid4()), str(uuid.uuid4())
    role = "maktaba_node_" + a.replace("-", "")[:12]
    await db.execute(f"CREATE ROLE {role} LOGIN IN ROLE maktaba_worker")
    try:
        await _node(db, a, role)
        await _node(db, b, "maktaba_node_other")
        node = await Database.connect(_swap(PG_URL, db=name, user=role))
        try:
            for q in (
                "INSERT INTO components (id, want, updated_by) VALUES ('llm', 'enabled', 'x')",
                "INSERT INTO component_status (machine, id, part, phase)"
                f" VALUES ('{a}', 'llm', 'pipeline', 'running')",
                "UPDATE component_meta SET revision = revision + 1",
            ):
                with pytest.raises(asyncpg.InsufficientPrivilegeError):
                    await node.execute(q)
            row = [Status(id="llm", phase="running")]
            await store.report_status(node, a, "pipeline", row)
            with pytest.raises(asyncpg.InsufficientPrivilegeError):
                await store.report_status(node, b, "pipeline", row)
            got, _ = await store.desired(node, a)
            assert got == {}
            assert [(s.machine, s.id) for s in await store.statuses(node)] == [(a, "llm")]
        finally:
            await node.close()
    finally:
        await db.execute(f"DROP ROLE IF EXISTS {role}")
