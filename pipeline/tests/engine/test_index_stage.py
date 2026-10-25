"""The engine ``index`` stage: fingerprint, embed the active transcript, replace vectors."""

from __future__ import annotations

import uuid
from collections.abc import Iterator
from datetime import UTC, datetime
from types import SimpleNamespace
from typing import Any, cast
from uuid import UUID

import pytest

from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.context import StageContext, StageFailure
from maktaba_pipeline.engine.graph import Subject
from maktaba_pipeline.engine.sql import is_pg, to_ts
from maktaba_pipeline.engine.stages import index as stage
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.search.semantic import SemanticIndex, SemanticUnavailable
from tests.engine.factory import make_library, make_video
from tests.search.semantic_fakes import FakeCollection, FakeEmbedder


class _Ctx:
    def __init__(self) -> None:
        self.progress_calls: list[tuple[str, int, int]] = []
        self.cancel_checks = 0

    async def progress(self, step: str, done: int, total: int, *a: Any, **kw: Any) -> None:
        self.progress_calls.append((step, done, total))

    async def check_cancel(self) -> None:
        self.cancel_checks += 1


@pytest.fixture
def store() -> Iterator[tuple[FakeCollection, FakeEmbedder]]:
    coll, emb = FakeCollection(), FakeEmbedder()
    stage.set_index_factory(lambda: SemanticIndex(coll, emb))
    yield coll, emb
    stage.reset_index_factory()


def _job(video: UUID, library: UUID) -> Job:
    subject = Subject(kind="video", id=video, library_id=library)
    return cast(Job, SimpleNamespace(subject=subject, subject_kind="video", id=1))


async def _transcript(
    db: Database,
    video: UUID,
    texts: list[str],
    *,
    active: bool = True,
    created: datetime = datetime(2026, 1, 1, tzinfo=UTC),
) -> UUID:
    track = await db.fetchrow(
        "SELECT id FROM audio_tracks WHERE video_id = $1 AND track_index = 0", str(video)
    )
    if track is None:
        await db.execute(
            "INSERT INTO audio_tracks (video_id, track_index) VALUES ($1, 0)", str(video)
        )
        track = await db.fetchrow("SELECT id FROM audio_tracks WHERE video_id = $1", str(video))
    if active:
        await db.execute(
            "UPDATE transcripts SET is_active = $2 WHERE video_id = $1",
            str(video),
            False if is_pg(db) else 0,
        )
    tid = uuid.uuid4()
    await db.execute(
        "INSERT INTO transcripts (id, video_id, audio_track_id, language, backend, model,"
        " is_active, created_at) VALUES ($1,$2,$3,'ar','whisper','large-v3',$4,$5)",
        str(tid),
        str(video),
        track["id"],
        active if is_pg(db) else int(active),
        to_ts(db, created),
    )
    for i, text in enumerate(texts):
        await db.execute(
            "INSERT INTO transcript_segments (transcript_id, seq, start_sec, end_sec, text)"
            " VALUES ($1,$2,$3,$4,$5)",
            str(tid),
            i,
            float(i * 4),
            float(i * 4 + 4),
            text,
        )
    return tid


@pytest.mark.asyncio
async def test_fingerprint_follows_transcript_size_and_model(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    lib = await make_library(db)
    v = await make_video(db, lib)
    subject = Subject(kind="video", id=v, library_id=lib)
    monkeypatch.delenv("MAKTABA_EMBED_MODEL", raising=False)
    none = await stage.input_fingerprint(db, subject)
    assert none is not None
    await _transcript(db, v, ["a", "b"])
    first = await stage.input_fingerprint(db, subject)
    assert first not in (None, none)
    assert await stage.input_fingerprint(db, subject) == first  # stable
    monkeypatch.setenv("MAKTABA_EMBED_MODEL", "intfloat/multilingual-e5-base")
    assert await stage.input_fingerprint(db, subject) != first
    monkeypatch.delenv("MAKTABA_EMBED_MODEL")
    await _transcript(db, v, ["a", "b"], created=datetime(2026, 2, 1, tzinfo=UTC))
    assert await stage.input_fingerprint(db, subject) != first  # a new transcript
    gone = Subject(kind="video", id=uuid.uuid4())
    assert await stage.input_fingerprint(db, gone) is None


@pytest.mark.asyncio
async def test_run_embeds_the_active_transcript_and_replaces_old_vectors(
    db: Database,
    store: tuple[FakeCollection, FakeEmbedder],
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    coll, emb = store
    monkeypatch.setenv("MAKTABA_EMBED_BATCH", "2")
    lib = await make_library(db)
    v = await make_video(db, lib)
    old = await _transcript(db, v, ["قديم"])
    ctx = _Ctx()
    await stage.run(db, _job(v, lib), cast(StageContext, ctx))
    assert {m["transcript_id"] for m in coll.metas()} == {str(old)}

    new = await _transcript(
        db, v, ["صلاة الفجر", "زكاة", "صوم", ""], created=datetime(2026, 3, 1, tzinfo=UTC)
    )
    ctx = _Ctx()
    res = await stage.run(db, _job(v, lib), cast(StageContext, ctx))
    metas = coll.metas()
    assert {m["transcript_id"] for m in metas} == {str(new)}  # the old transcript is gone
    assert len(metas) == 3  # the blank segment is not embedded
    assert all(m["library_id"] == str(lib) and m["video_id"] == str(v) for m in metas)
    seg_ids = {
        int(r["id"])
        for r in await db.fetch(
            "SELECT id FROM transcript_segments WHERE transcript_id = $1 AND text <> ''", str(new)
        )
    }
    assert {m["segment_id"] for m in metas} == seg_ids
    assert [len(b) for b in emb.passages[-2:]] == [2, 1]  # batches of MAKTABA_EMBED_BATCH
    timings = {k: res.summary.pop(k) for k in list(res.summary) if k.endswith("_sec")}
    assert set(timings) == {"embed_sec", "write_sec", "write_wait_sec", "read_sec"}
    assert res.summary == {
        "transcript_id": str(new),
        "segments": 3,
        "model": "fake/e5",
        "stale_ids": 2,  # the old transcript's one segment, the new one's blank one
    }
    # the old vectors go by id once the new ones are written: no delete scans the
    # collection by video (about a second each on the home Mac's 12 million vectors)
    stale = sorted([f"{old}:0", f"{new}:3"])
    assert coll.deletes[-1] == {"ids": stale, "where": None}
    assert all(d["where"] is None for d in coll.deletes)
    assert ctx.progress_calls[0] == ("embed", 0, 4) and ctx.progress_calls[-1] == ("embed", 4, 4)
    assert ctx.cancel_checks == 2

    deletes = len(coll.deletes)
    again = await stage.run(db, _job(v, lib), cast(StageContext, _Ctx()))
    assert again.output_hash == res.output_hash  # same transcript, same output
    assert again.summary["stale_ids"] == 2  # asked again: Chroma no longer has them
    assert coll.deletes[deletes:] == [{"ids": stale, "where": None}]

    hits = SemanticIndex(coll, emb).search("صلاة الفجر", k=1, library_ids=[str(lib)])
    assert hits[0].start_sec == 0.0 and hits[0].segment_id in seg_ids


@pytest.mark.asyncio
async def test_run_without_a_transcript_clears_the_video(
    db: Database, store: tuple[FakeCollection, FakeEmbedder]
) -> None:
    coll, _ = store
    lib = await make_library(db)
    v = await make_video(db, lib)
    tid = await _transcript(db, v, ["x"])
    await stage.run(db, _job(v, lib), cast(StageContext, _Ctx()))
    assert coll.rows
    await db.execute(
        "UPDATE transcripts SET is_active = $2 WHERE id = $1", str(tid), False if is_pg(db) else 0
    )
    res = await stage.run(db, _job(v, lib), cast(StageContext, _Ctx()))
    assert coll.rows == {} and res.summary["segments"] == 0


@pytest.mark.asyncio
async def test_missing_search_extra_fails_without_retry(db: Database) -> None:
    def unavailable() -> SemanticIndex:
        raise SemanticUnavailable("chromadb is not installed")

    stage.set_index_factory(unavailable)
    try:
        lib = await make_library(db)
        v = await make_video(db, lib)
        with pytest.raises(StageFailure) as err:
            await stage.run(db, _job(v, lib), cast(StageContext, _Ctx()))
        assert err.value.kind == "search_unavailable" and not err.value.retryable
    finally:
        stage.reset_index_factory()


@pytest.mark.asyncio
async def test_a_blanked_segment_loses_its_vector(
    db: Database, store: tuple[FakeCollection, FakeEmbedder]
) -> None:
    """The same transcript, edited: a segment made blank has its vector deleted (by id),
    the others are replaced in place."""
    coll, _ = store
    lib = await make_library(db)
    v = await make_video(db, lib)
    tid = await _transcript(db, v, ["أ", "ب", "ج"])
    await stage.run(db, _job(v, lib), cast(StageContext, _Ctx()))
    assert len(coll.rows) == 3
    await db.execute(
        "UPDATE transcript_segments SET text = '' WHERE transcript_id = $1 AND seq = 1", str(tid)
    )
    res = await stage.run(db, _job(v, lib), cast(StageContext, _Ctx()))
    assert sorted(m["seq"] for m in coll.metas()) == [0, 2]
    assert res.summary["stale_ids"] == 1


@pytest.mark.asyncio
async def test_the_embedding_does_not_hold_the_write_lock(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    """One job embeds while another writes: only the upsert holds the search index's
    write lock (search/gate.py), the model has its own."""
    from maktaba_pipeline.search import gate

    coll = FakeCollection()
    held: list[bool] = []

    class Probe(FakeEmbedder):
        def embed_passages(self, texts: Any) -> list[list[float]]:
            held.append(gate._write_lock.locked())
            return super().embed_passages(texts)

    stage.set_index_factory(lambda: SemanticIndex(coll, Probe()))
    try:
        lib = await make_library(db)
        v = await make_video(db, lib)
        await _transcript(db, v, ["أ", "ب"])
        await stage.run(db, _job(v, lib), cast(StageContext, _Ctx()))
    finally:
        stage.reset_index_factory()
    assert held and not any(held)
