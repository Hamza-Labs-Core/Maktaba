"""Story 26.7 §2/D3 — the classify stage body."""

from __future__ import annotations

from uuid import uuid4

import pytest

from maktaba_pipeline.classify import classify_stage
from maktaba_pipeline.classify.classify_stage import run_classify
from maktaba_pipeline.enrich import EnrichSettings, ProviderKey

from ..enrich._fake_enrich_db import FakeEnrichDB


@pytest.mark.asyncio
async def test_classify_persists_parsed_title_and_enqueues_enrich() -> None:
    db = FakeEnrichDB()
    vid, lib = uuid4(), uuid4()
    res = await run_classify(
        db,
        video_id=vid,
        library_id=lib,
        filename="Breaking.Bad.S01E02.1080p.x265-GROUP.mkv",
        settings=EnrichSettings(enabled=True),
        providers=[ProviderKey("tmdb", configured=True)],
    )
    assert res.classified is True
    assert res.error is None
    assert str(vid) in db.parsed_titles
    # Enrich enqueued because enabled + a key is configured.
    assert res.enrich_enqueued is True
    assert any(j["video_id"] == str(vid) for j in db.jobs.values())


@pytest.mark.asyncio
async def test_classify_skips_enrich_without_key() -> None:
    db = FakeEnrichDB()
    vid, lib = uuid4(), uuid4()
    res = await run_classify(
        db,
        video_id=vid,
        library_id=lib,
        filename="movie.2009.1080p.mkv",
        settings=EnrichSettings(enabled=True),
        providers=[ProviderKey("tmdb", configured=False)],
    )
    assert res.classified is True
    assert res.enrich_enqueued is False
    assert db.jobs == {}


@pytest.mark.asyncio
async def test_classify_failure_is_isolated(monkeypatch: pytest.MonkeyPatch) -> None:
    # test_classify_failure_does_not_block_ready: a parse/persist fault is
    # captured in `error`, never raised — the stage still returns so the
    # orchestrator advances the video.
    db = FakeEnrichDB()
    vid, lib = uuid4(), uuid4()

    def boom(*_args: object, **_kwargs: object) -> object:
        raise RuntimeError("parser exploded")

    monkeypatch.setattr(classify_stage.title_parser, "parse", boom)
    res = await run_classify(
        db,
        video_id=vid,
        library_id=lib,
        filename="whatever.mkv",
        settings=EnrichSettings(enabled=False),
        providers=[],
    )
    assert res.classified is False
    assert res.error is not None and "parser exploded" in res.error
