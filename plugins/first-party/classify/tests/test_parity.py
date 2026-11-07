"""tv.maktaba.classify does exactly what its stages did in-process (Epic 54, stories 54.26,
54.28): a seeded channel library run through the whole graph on two copies of one database,
the wave's stages in-process on one and through the plugin on the other (the real plugin
API, its statements and its role), then every row the plugin writes, every job the queue
recorded and every log line compared."""

from __future__ import annotations

import os
import uuid
from collections.abc import Iterator
from typing import Any

import pytest

from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.media import reset_runner
from maktaba_plugin_sdk.parity import ParityConfig
from tests.plugins.firstparty.harness import parity
from tests.stages.world import make_fake_media, models_off

pytestmark = [pytest.mark.integration]

PG_URL = os.environ.get("MAKTABA_TEST_PG_URL", "")
HERE = os.path.dirname(__file__)
#: the stages wave 1 moved (story 54.28)
STAGES = ["promo_links", "clip_groups", "topic_collections", "rate_style"]
KEYS = {
    "promo_links": ("video_id",),
    "clip_groups": ("id",),
    "clip_group_items": ("group_id", "video_id"),
    "topic_collections": ("id",),
    "topic_items": ("topic_id", "video_id"),
    "season_style": ("season_id",),
}


def selected() -> list[str]:
    want = [s for s in os.environ.get("MAKTABA_PARITY_STAGES", "").split(",") if s]
    return [s for s in STAGES if not want or s in want]


@pytest.fixture(autouse=True)
def _media() -> Iterator[None]:
    make_fake_media({})
    yield
    reset_runner()


@pytest.fixture
def template(pg_template: str) -> str:
    if not PG_URL:
        pytest.skip("MAKTABA_TEST_PG_URL not set")
    if not selected():
        pytest.skip("no stage of tv.maktaba.classify selected")
    return pg_template


class _Ids:
    """The same ids on both sides: uuid4 answers a fixed sequence while a side is seeded."""

    def __init__(self) -> None:
        import random

        self.rng = random.Random(54)

    def __call__(self) -> uuid.UUID:
        return uuid.UUID(int=self.rng.getrandbits(128), version=4)


def seeder(monkeypatch: pytest.MonkeyPatch) -> Any:
    from tests.stages.test_leftover_stages import make_catalog

    async def seed(db: Any) -> None:
        monkeypatch.setattr(uuid, "uuid4", _Ids())
        try:
            await models_off(db)
            cat = await make_catalog(db)
            await enqueue_job(db, subject_kind="library", subject_id=cat["lib"], stage="detect")
        finally:
            monkeypatch.undo()

    return seed


@pytest.mark.asyncio
async def test_classify_wave_1_is_identical(template: str, monkeypatch: pytest.MonkeyPatch) -> None:
    from maktaba_pipeline.plugins import firstparty

    cfg = ParityConfig.load(__import__("pathlib").Path(HERE).parent / "parity.toml")
    raw = firstparty._raw()
    async with parity(
        PG_URL,
        template,
        bundle=raw,
        stages=selected(),
        seed=seeder(monkeypatch),
        monkeypatch=monkeypatch,
    ) as p:
        await p.run()
        assert p.a.ran == p.b.ran, "the two sides ran different jobs"
        for s in selected():
            assert s in p.b.ran, f"{s} never ran"
        n = await p.b.db.fetchval(
            "SELECT count(*) FROM plugin_invocations WHERE plugin_id = 'tv.maktaba.classify'"
        )
        assert n >= len(selected()), f"only {n} jobs went through the plugin"
        rows = await p.compare(
            [(t, KEYS[t]) for t in cfg.tables],
            ignore={k: list(v) for k, v in cfg.ignore.items()},
            perf=False,
        )
        assert rows == [], "\n".join(rows[:20])
        await p.bench(selected())
        problems = await p.compare(
            [(t, KEYS[t]) for t in cfg.tables],
            ignore={k: list(v) for k, v in cfg.ignore.items()},
            stages=selected(),
            perf=cfg.perf,
        )
    assert problems == [], "\n".join(problems[:20])
