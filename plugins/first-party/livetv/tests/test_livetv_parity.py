"""tv.maktaba.livetv does exactly what ``plan_channels`` and ``write_channels`` did in-process
(Epic 54, stories 54.26, 54.34): a library with a show and loose talks, detected and planned
into channels on two copies of one database, in-process on one and through the plugin (the
light runner, the real plugin API, its statements and its role) on the other. With staging
off the plan's channels go on the air; with staging on they become a staged TV change. The
plan, the channels, their links, filler pools and the TV change are compared, then N16."""

from __future__ import annotations

import os
import types
import uuid
from collections.abc import Iterator
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

import pytest
from maktaba_plugin_sdk.parity import ParityConfig

from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.media import reset_runner
from maktaba_pipeline.engine.sql import dumps, now, to_ts
from tests.plugins.firstparty.harness import SameIds, parity
from tests.stages.world import make_fake_media, make_show, models_off

pytestmark = [pytest.mark.integration]

PG_URL = os.environ.get("MAKTABA_TEST_PG_URL", "")
HERE = Path(__file__).parent
STAGES = ["plan_channels", "write_channels"]
KEYS = {
    "channel_plans": ("planner_version", "input_hash"),
    "channels": ("id",),
    "external_links": ("target", "subject_kind", "subject_id"),
    "filler_pools": ("id",),
    "filler_items": ("id",),
    "channel_schedule_state": ("channel_id", "sched_gen"),
    "tv_changes": ("id",),
    "tv_releases": ("id",),
    "tv_release_channels": ("release_id", "channel_id"),
}


@pytest.fixture
def template(pg_template: str) -> str:
    if not PG_URL:
        pytest.skip("MAKTABA_TEST_PG_URL not set")
    return pg_template


#: the TV change's clock on both sides: a generation's anchor, boundaries and horizon follow
#: it, and the two sides run moments apart
FROZEN = datetime(2026, 11, 7, 12, 0, tzinfo=UTC)


def freeze_tv_clock(monkeypatch: pytest.MonkeyPatch) -> None:
    """``now`` of the TV release code and of both stages (each module took it by name); the
    engine's own clock (claims, leases) stays real."""
    from maktaba_fp_livetv.stages import plan_channels, write_channels

    from maktaba_pipeline.tvrelease import build, commit, gc, generation, rollback, stage, store

    for mod in (
        plan_channels,
        write_channels,
        build,
        commit,
        gc,
        generation,
        rollback,
        stage,
        store,
    ):
        monkeypatch.setattr(mod, "now", lambda: FROZEN)


@pytest.fixture(autouse=True)
def _media() -> Iterator[None]:
    make_fake_media({})
    yield
    reset_runner()


async def _rule(db: Any, section: str, value: dict[str, Any]) -> None:
    await db.execute(
        "INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2, $3)"
        " ON CONFLICT (key) DO UPDATE SET value = excluded.value",
        f"rules.{section}",
        dumps(value),
        to_ts(db, now()),
    )


def seeder(*, staging: bool) -> Any:
    async def seed(db: Any) -> None:
        with pytest.MonkeyPatch.context() as mp:
            mp.setattr(uuid, "uuid4", SameIds(5442))
            await models_off(db)
            # a test library has hours, not the month a channel needs by default
            await _rule(db, "planner", {"channel_min_days": 1.0, "channel_day_hours": 10.0})
            if not staging:
                await _rule(db, "tv_staging", {"enabled": False})
            w = await make_show(db, name="قصص الأنبياء", episodes=40, loose=4)
            await enqueue_job(db, subject_kind="library", subject_id=w.library, stage="detect")

    return seed


async def _prove(
    template: str, monkeypatch: pytest.MonkeyPatch, *, staging: bool
) -> tuple[list[str], str]:
    from maktaba_fp_livetv.stages import write_channels

    from maktaba_pipeline.plugins import firstparty
    from maktaba_pipeline.tvrelease import build, store

    ids = SameIds(5443)
    if staging:
        freeze_tv_clock(monkeypatch)
    for mod in (write_channels, build, store):
        fake = types.SimpleNamespace(
            uuid4=ids, uuid5=uuid.uuid5, UUID=uuid.UUID, NAMESPACE_URL=uuid.NAMESPACE_URL
        )
        monkeypatch.setattr(mod, "uuid", fake)
    cfg = ParityConfig.load(HERE.parent / "parity.toml")
    tables = [(t, KEYS[t]) for t in cfg.tables]
    ignore = {k: list(v) for k, v in cfg.ignore.items()}
    async with parity(
        PG_URL,
        template,
        bundle=firstparty._raw(),
        stages=STAGES,
        group="light",
        seed=seeder(staging=staging),
        monkeypatch=monkeypatch,
    ) as p:
        await p.run(before_side=ids.reset)
        assert p.a.ran == p.b.ran, "the two sides ran different jobs"
        for s in STAGES:
            assert s in p.b.ran, f"{s} never ran"
        n = await p.b.db.fetchval(
            "SELECT count(*) FROM plugin_invocations WHERE plugin_id = 'tv.maktaba.livetv'"
        )
        assert n >= len(STAGES), f"only {n} jobs went through the plugin"
        if staging:
            assert await p.b.db.fetchval("SELECT count(*) FROM tv_changes") == 1
        else:
            assert await p.b.db.fetchval("SELECT count(*) FROM channels") > 0
        rows = await p.compare(tables, ignore=ignore, perf=False)
        assert rows == [], "\n".join(rows[:30])
        problems = await p.compare_n16(
            [], ignore=ignore, stages=STAGES, perf=cfg.perf, timing_only=True
        )
        slowest = p.report("tv.maktaba.livetv" + (".staged" if staging else ""))
    return problems, slowest


@pytest.mark.asyncio
async def test_livetv_wave_7_writes_the_air_identically(
    template: str, monkeypatch: pytest.MonkeyPatch
) -> None:
    problems, slowest = await _prove(template, monkeypatch, staging=False)
    assert problems == [], "\n".join(problems[:20]) + "\nslowest plugin jobs:\n" + slowest


@pytest.mark.asyncio
async def test_livetv_wave_7_stages_a_tv_change_identically(
    template: str, monkeypatch: pytest.MonkeyPatch
) -> None:
    problems, slowest = await _prove(template, monkeypatch, staging=True)
    assert problems == [], "\n".join(problems[:20]) + "\nslowest plugin jobs:\n" + slowest
