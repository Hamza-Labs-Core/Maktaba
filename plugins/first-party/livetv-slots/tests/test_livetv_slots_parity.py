"""tv.maktaba.livetv-slots does exactly what Epic 33's TV change stages did in-process (Epic
54, stories 54.26, 54.34): a TV change staged (``tv_stage``), put on the air (``tv_swap``),
rolled back (``tv_rollback``) and the staging cleaned up (``tv_gc``), on two copies of one
database, in-process on one and through the plugin (the light runner, the real plugin API,
its statements and its role) on the other. The swap commit and the rollback write the stored
TV settings, the engine's: the worker runs them for the plugin's job (relayed). The lineup,
the releases, the schedule generations, the routing, the change and its events and the TV
settings are compared after each step, then N16."""

from __future__ import annotations

import os
import types
import uuid
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

import pytest
from maktaba_plugin_sdk.parity import ParityConfig

from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import CATALOG_ID
from maktaba_pipeline.engine.sql import dumps
from tests.plugins.firstparty.harness import SameIds, parity

pytestmark = [pytest.mark.integration]

PG_URL = os.environ.get("MAKTABA_TEST_PG_URL", "")
HERE = Path(__file__).parent
STAGES = ["tv_stage", "tv_swap", "tv_rollback", "tv_gc"]
KEYS = {
    "tv_changes": ("id",),
    "tv_change_events": ("change_id", "action", "from_state", "to_state"),
    "tv_releases": ("id",),
    "tv_release_channels": ("release_id", "channel_id"),
    "channels": ("id",),
    "channel_schedule_state": ("channel_id", "sched_gen"),
    "channel_programs": ("channel_id", "sched_gen", "seq"),
    "tv_routing": ("scope",),
    "app_settings": ("key",),
}
#: the TV change's clock on both sides (a Monday at noon, as the Epic 33 tests)
FROZEN = datetime(2026, 6, 15, 12, 0, tzinfo=UTC)


@pytest.fixture
def template(pg_template: str) -> str:
    if not PG_URL:
        pytest.skip("MAKTABA_TEST_PG_URL not set")
    return pg_template


def seeder(ids: SameIds) -> Any:
    """Two libraries, two channels on the air with a generation-0 schedule, and a draft
    change that moves one channel to the other library and adds a third."""

    async def seed(db: Any) -> None:
        from maktaba_pipeline.channels.pass_ import topup_once
        from maktaba_pipeline.tvrelease.store import create_change
        from tests.tvrelease.helpers import add_channel, channel_def, library_with

        ids.reset()  # the change's id (tvrelease.store's, patched to ids)
        with pytest.MonkeyPatch.context() as mp:
            mp.setattr(uuid, "uuid4", SameIds(5444))
            lib = await library_with(db, 8, tag="first")
            other = await library_with(db, 8, tag="second")
            a = await add_channel(db, lib, 101)
            await add_channel(db, lib, 102)
            await topup_once(db, now=FROZEN)
            # the TV settings a release snapshots, so the swap and the rollback move them
            await db.execute(
                "INSERT INTO app_settings (key, value) VALUES ('rules.tv', $1)",
                dumps({"guide_days": 3}),
            )
            d = await channel_def(db, a)
            added = {**d, "id": "00000000-0000-0000-0000-00000000b0d1", "number": 150}
            added["slug"] = "added"
            pending: dict[str, Any] = {
                "channels": {a: {**d, "library_id": str(other), "number": 111}, added["id"]: added},
                "settings": {"tv": {"guide_days": 5}},
            }
            cid = await create_change(
                db, state="draft", title="t", source={"kind": "manual"}, pending=pending
            )
            await enqueue_job(
                db,
                subject_kind="catalog",
                subject_id=CATALOG_ID,
                stage="tv_stage",
                payload={"change_id": cid},
            )

    return seed


def freeze(monkeypatch: pytest.MonkeyPatch, ids: SameIds) -> None:
    from maktaba_pipeline.tvrelease import build, commit, gc, generation, rollback, stage, store

    for mod in (build, commit, gc, generation, rollback, stage, store):
        monkeypatch.setattr(mod, "now", lambda: FROZEN)
    for mod in (build, store):
        monkeypatch.setattr(
            mod,
            "uuid",
            types.SimpleNamespace(
                uuid4=ids, uuid5=uuid.uuid5, UUID=uuid.UUID, NAMESPACE_URL=uuid.NAMESPACE_URL
            ),
        )


async def _next(p: Any, ids: SameIds, stage: str, *, swapping: bool = False) -> None:
    """Each side queues ``stage`` for its change (as the API's controller would), then both
    drain."""
    for side in (p.a, p.b):
        cid = str(await side.db.fetchval("SELECT id FROM tv_changes ORDER BY number LIMIT 1"))
        if swapping:
            # what the controller does on approval: the change is swapping
            await side.db.execute("UPDATE tv_changes SET state = 'swapping' WHERE id = $1", cid)
        payload = {"change_id": cid} if stage != "tv_gc" else {}
        await enqueue_job(
            side.db,
            subject_kind="catalog",
            subject_id=CATALOG_ID,
            stage=stage,
            payload=payload,
            force=True,
        )
    await p.run(before_side=ids.reset)


@pytest.mark.asyncio
async def test_livetv_slots_wave_7_is_identical(
    template: str, monkeypatch: pytest.MonkeyPatch
) -> None:
    from maktaba_pipeline.plugins import firstparty

    ids = SameIds(5445)
    freeze(monkeypatch, ids)
    cfg = ParityConfig.load(HERE.parent / "parity.toml")
    tables = [(t, KEYS[t]) for t in cfg.tables]
    ignore = {k: list(v) for k, v in cfg.ignore.items()}
    async with parity(
        PG_URL,
        template,
        bundle=firstparty._raw(),
        stages=STAGES,
        group="light",
        seed=seeder(ids),
        monkeypatch=monkeypatch,
    ) as p:
        # 1. staged: a release and a schedule generation built
        await p.run(before_side=ids.reset)
        assert await p.b.db.fetchval("SELECT state FROM tv_changes") == "staged"
        assert (rows := await p.compare(tables, ignore=ignore, perf=False)) == [], "\n".join(
            rows[:30]
        )
        # 2. swapped: the lineup, the generation and the TV settings on the air
        await _next(p, ids, "tv_swap", swapping=True)
        assert await p.b.db.fetchval("SELECT state FROM tv_changes") == "live"
        assert (rows := await p.compare(tables, ignore=ignore, perf=False)) == [], "\n".join(
            rows[:30]
        )
        # 3. rolled back: the previous release back, the change's edits kept in a draft
        await _next(p, ids, "tv_rollback")
        assert (rows := await p.compare(tables, ignore=ignore, perf=False)) == [], "\n".join(
            rows[:30]
        )
        # 4. the staging cleaned up
        await _next(p, ids, "tv_gc")
        assert (rows := await p.compare(tables, ignore=ignore, perf=False)) == [], "\n".join(
            rows[:30]
        )
        for s in STAGES:
            assert s in p.b.ran, f"{s} never ran"
        n = await p.b.db.fetchval(
            "SELECT count(*) FROM plugin_invocations WHERE plugin_id = 'tv.maktaba.livetv-slots'"
        )
        assert n >= len(STAGES), f"only {n} jobs went through the plugin"
        problems = await p.compare_n16(
            [], ignore=ignore, stages=["tv_gc"], perf=cfg.perf, timing_only=True
        )
        slowest = p.report("tv.maktaba.livetv-slots")
    assert problems == [], "\n".join(problems[:20]) + "\nslowest plugin jobs:\n" + slowest
