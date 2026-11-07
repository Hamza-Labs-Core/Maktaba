"""tv.maktaba.semantic-search does exactly what its stages did in-process (Epic 54, stories
54.26, 54.30): three transcribed lessons indexed, chaptered and grouped into topics on two
copies of one database, the stages in-process on one and through the plugin (the index
runner, the real plugin API, its statements and its role) on the other, with the Postgres
vector store on. The embedder is a fake, the same on both sides (the runner runs in this
test's process); the rows each side stores are compared."""

from __future__ import annotations

import os
import uuid
from collections.abc import Iterator
from pathlib import Path
from typing import Any

import pytest
from maktaba_plugin_sdk.parity import ParityConfig

from maktaba_pipeline.engine.enqueue import enqueue_job
from tests.plugins.firstparty.harness import SameIds, parity
from tests.stages.world import models_off

pytestmark = [pytest.mark.integration]

PG_URL = os.environ.get("MAKTABA_TEST_PG_URL", "")
HERE = Path(__file__).parent
#: the stages wave 3 moved into tv.maktaba.semantic-search (story 54.30)
STAGES = ["index", "chapter_infer", "topic_assign", "topic_recluster"]
KEYS = {
    "video_embeddings": ("video_id",),
    "segment_embeddings": ("segment_id",),
    "chapters": ("video_id", "seq"),
    "video_topics": ("video_id", "topic_id"),
    "library_topics": ("library_id", "topic_id"),
}
LESSONS = [
    ["الصلاة عماد الدين", "شروط الصلاة الطهارة واستقبال القبلة", "أركان الصلاة القيام والركوع"],
    ["الزكاة ركن من أركان الإسلام", "نصاب الزكاة في الذهب والفضة", "مصارف الزكاة ثمانية"],
    ["الصلاة في وقتها", "سجود السهو في الصلاة", "صلاة الجماعة وفضلها"],
]


def selected() -> list[str]:
    want = [s for s in os.environ.get("MAKTABA_PARITY_STAGES", "").split(",") if s]
    return [s for s in STAGES if not want or s in want]


@pytest.fixture
def template(pg_template: str) -> str:
    if not PG_URL:
        pytest.skip("MAKTABA_TEST_PG_URL not set")
    if not selected():
        pytest.skip("no stage of tv.maktaba.semantic-search selected")
    return pg_template


@pytest.fixture
def fakes() -> Iterator[None]:
    from maktaba_fp_semantic_search.stages import chapter_infer
    from maktaba_fp_semantic_search.stages import index as stage

    from maktaba_pipeline.search import pgstore
    from maktaba_pipeline.search.pgstore import DIMENSIONS
    from maktaba_pipeline.search.semantic import SemanticIndex
    from tests.search.semantic_fakes import FakeCollection, FakeEmbedder

    coll, emb = FakeCollection(), FakeEmbedder(dim=DIMENSIONS)
    stage.set_index_factory(lambda: SemanticIndex(coll, emb))
    stage.set_embedder_factory(lambda: emb)
    chapter_infer.set_collection_factory(lambda: coll)
    pgstore.forget_state()
    yield
    stage.reset_index_factory()
    stage.reset_embedder_factory()
    chapter_infer.reset_collection_factory()
    pgstore.forget_state()


async def seed(db: Any) -> None:
    from maktaba_pipeline.search import pgstore
    from tests.engine.factory import make_library, make_video

    if not await db.fetchval("SELECT ensure_segment_embeddings()"):
        pytest.skip("pgvector is not installed on MAKTABA_TEST_PG_URL's server")
    await db.execute("UPDATE semantic_store_state SET store = 'postgres'")
    pgstore.forget_state()
    with pytest.MonkeyPatch.context() as mp:
        mp.setattr(uuid, "uuid4", SameIds(5432))
        await models_off(db)
        lib = await make_library(db, "مكتبة البحث")
        for n, lines in enumerate(LESSONS, 1):
            v = await make_video(db, lib, title=f"الدرس {n}", duration=600.0)
            await db.execute(
                "INSERT INTO audio_tracks (video_id, track_index) VALUES ($1, 0)", str(v)
            )
            track = await db.fetchval("SELECT id FROM audio_tracks WHERE video_id = $1", str(v))
            tid = str(uuid.uuid4())
            await db.execute(
                "INSERT INTO transcripts (id, video_id, audio_track_id, language, backend, model,"
                " is_active) VALUES ($1, $2, $3, 'ar', 'test', 'test', true)",
                tid, str(v), track,
            )  # fmt: skip
            for seq, text in enumerate(lines):
                await db.execute(
                    "INSERT INTO transcript_segments (transcript_id, seq, start_sec, end_sec,"
                    " text) VALUES ($1, $2, $3, $4, $5)",
                    tid, seq, seq * 180.0, seq * 180.0 + 170.0, text,
                )  # fmt: skip
            await enqueue_job(db, subject_kind="video", subject_id=v, stage="index")


@pytest.mark.asyncio
@pytest.mark.usefixtures("fakes")
async def test_semantic_search_wave_3_is_identical(
    template: str, monkeypatch: pytest.MonkeyPatch
) -> None:
    from maktaba_pipeline.plugins import firstparty

    cfg = ParityConfig.load(HERE.parent / "parity.toml")
    async with parity(
        PG_URL,
        template,
        bundle=firstparty._raw(),
        stages=selected(),
        group="index",
        seed=seed,
        monkeypatch=monkeypatch,
    ) as p:
        await p.run()
        assert p.a.ran == p.b.ran, "the two sides ran different jobs"
        for s in selected():
            assert s in p.b.ran, f"{s} never ran"
        n = await p.b.db.fetchval(
            "SELECT count(*) FROM plugin_invocations WHERE plugin_id = 'tv.maktaba.semantic-search'"
        )
        assert n >= len(selected()), f"only {n} jobs went through the plugin"
        tables = [(t, KEYS[t]) for t in cfg.tables]
        ignore = {k: list(v) for k, v in cfg.ignore.items()}
        rows = await p.compare(tables, ignore=ignore, perf=False)
        assert rows == [], "\n".join(rows[:30])
        problems = await p.compare_n16(tables, ignore=ignore, stages=selected(), perf=cfg.perf)
        slowest = p.report("tv.maktaba.semantic-search")
    assert problems == [], "\n".join(problems[:20]) + "\nslowest plugin jobs:\n" + slowest
