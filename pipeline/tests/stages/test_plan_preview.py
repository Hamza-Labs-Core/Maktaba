"""The plan page, the plan preview (dry run) and the gRPC calls the control centre uses."""

from __future__ import annotations

import asyncio
import json
from collections.abc import Iterator

import pytest

from maktaba_pipeline.engine.media import reset_runner
from maktaba_pipeline.engine.plan_page import PLAN_PAGE, PREVIEW_PAGE, render_plan
from maktaba_pipeline.engine.plan_preview import PlanPreview
from maktaba_pipeline.engine.sql import dumps
from maktaba_pipeline.engine.stages import review_pages
from maktaba_pipeline.grpc_server import PipelineService
from maktaba_pipeline.runtime import Database
from tests.stages.test_catalog_stages import _pipeline  # noqa: PLC2701 — the shared world
from tests.stages.world import make_fake_media


@pytest.fixture(autouse=True)
def _media() -> Iterator[None]:
    make_fake_media({})
    yield
    reset_runner()


async def _wait(pp: PlanPreview) -> dict[str, object]:
    for _ in range(600):
        if not pp.running:
            return pp.status()
        await asyncio.sleep(0.05)
    raise AssertionError("the preview never finished")


@pytest.mark.asyncio
async def test_storing_a_plan_writes_its_page_and_it_has_no_script(db: Database) -> None:
    await _pipeline(db, episodes=40)
    page = (review_pages.reviews_dir() / PLAN_PAGE).read_text(encoding="utf-8")
    assert page.startswith("<!doctype html>") and "<script" not in page.lower()
    assert "Channel plan" in page and "Weekly grid" in page
    assert not list(review_pages.reviews_dir().glob("*.part.html"))


@pytest.mark.asyncio
async def test_a_preview_plans_with_the_current_settings_and_writes_no_plan(db: Database) -> None:
    await _pipeline(db, episodes=40)
    plans = await db.fetchval("SELECT count(*) FROM channel_plans")
    channels = await db.fetchval("SELECT count(*) FROM channels")
    pp = PlanPreview(db)
    assert pp.start("preview") == {"started": True, "kind": "preview"}
    with pytest.raises(RuntimeError):  # one at a time
        pp.start("preview")
    st = await _wait(pp)
    assert st["error"] is None and st["ready"]["preview"] is True  # type: ignore[index]
    assert st["summary"]["channels"] >= 1  # type: ignore[index]
    text = (review_pages.reviews_dir() / PREVIEW_PAGE).read_text(encoding="utf-8")
    assert "Nothing was written" in text and "(preview)" in text
    assert await db.fetchval("SELECT count(*) FROM channel_plans") == plans
    assert await db.fetchval("SELECT count(*) FROM channels") == channels


@pytest.mark.asyncio
async def test_a_changed_planner_setting_shows_in_the_preview_changes(db: Database) -> None:
    await _pipeline(db, episodes=40)
    row = await db.fetchrow("SELECT value FROM app_settings WHERE key = 'rules.planner'")
    changes = json.loads(row["value"]) if isinstance(row["value"], str) else dict(row["value"])
    changes["channel_min_days"] = 100000.0  # no channel has a long enough month any more
    await db.execute(
        "UPDATE app_settings SET value = $1 WHERE key = 'rules.planner'", dumps(changes)
    )
    pp = PlanPreview(db)
    pp.start("preview")
    st = await _wait(pp)
    assert st["error"] is None
    assert st["summary"]["changes"]["removed"] >= 1  # type: ignore[index]


@pytest.mark.asyncio
async def test_stored_kind_renders_the_latest_plan_and_says_when_there_is_none(
    db: Database,
) -> None:
    pp = PlanPreview(db)
    pp.start("stored")
    assert "no stored plan" in str((await _wait(pp))["error"]).lower()
    await _pipeline(db, episodes=40)
    (review_pages.reviews_dir() / PLAN_PAGE).unlink()
    pp.start("stored")
    st = await _wait(pp)
    assert st["error"] is None and (review_pages.reviews_dir() / PLAN_PAGE).is_file()


@pytest.mark.asyncio
async def test_grpc_service_methods(db: Database) -> None:
    await _pipeline(db, episodes=40)
    svc = PipelineService(plan_preview=PlanPreview(db))
    assert (await svc.plan_preview_start({"kind": "preview"}))["started"] is True
    busy = await svc.plan_preview_start({"kind": "preview"})
    assert busy["started"] is False and busy["busy"] is True
    for _ in range(600):
        st = await svc.plan_preview_status({})
        if not st["running"]:
            break
        await asyncio.sleep(0.05)
    assert st["error"] is None and st["ready"]["preview"]
    with pytest.raises(ValueError):
        await svc.plan_preview_start({"kind": "nope"})
    with pytest.raises(RuntimeError):
        await PipelineService().plan_preview_status({})


@pytest.mark.unit
def test_render_escapes_names_and_survives_a_plan_without_channels() -> None:
    plan = {"channels": [], "lanes": {}, "filler_pools": {}, "unplaced": [
        {"what": "<img src=x onerror=alert(1)>", "why": "no room"}], "notes": ["<b>n</b>"],
        "planner_version": 2, "generated_at": "2026-01-01T00:00:00+00:00"}  # fmt: skip
    page = render_plan(plan)
    assert "<img src=x" not in page and "&lt;img src=x" in page and "&lt;b&gt;n" in page
