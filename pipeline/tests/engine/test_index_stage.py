"""The engine ``index`` stage: fingerprint, embed the active transcript, replace vectors.

The store is ``segment_embeddings`` (Postgres, pgvector): here an in-memory stand-in of
:class:`~maktaba_pipeline.search.pgstore.PgVectorStore`, so the tests run on either test
database; tests/search/test_pgstore.py runs the real one.
"""

from __future__ import annotations

import uuid
from collections.abc import Iterator, Sequence
from dataclasses import dataclass, field
from datetime import UTC, datetime
from types import SimpleNamespace
from typing import Any, cast
from uuid import UUID

import pytest
from maktaba_fp_semantic_search.stages import index as stage

from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.context import StageContext, StageFailure
from maktaba_pipeline.engine.graph import Subject
from maktaba_pipeline.engine.sql import is_pg, to_ts
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.search.pgstore import StoreState
from maktaba_pipeline.search.semantic import IndexedSegment, SemanticUnavailable
from tests.engine.factory import make_library, make_video
from tests.search.semantic_fakes import FakeEmbedder


class _Ctx:
    def __init__(self) -> None:
        self.progress_calls: list[tuple[str, int, int]] = []
        self.cancel_checks = 0

    async def progress(self, step: str, done: int, total: int, *a: Any, **kw: Any) -> None:
        self.progress_calls.append((step, done, total))

    async def check_cancel(self) -> None:
        self.cancel_checks += 1


@dataclass
class _Row:
    video_id: str
    library_id: str
    language: str | None
    segment: IndexedSegment
    embedding: list[float]


@dataclass
class FakeStore:
    """``PgVectorStore``'s writes, in memory (one per test: every instance shares it)."""

    rows: dict[int, _Row] = field(default_factory=dict)
    writes: list[int] = field(default_factory=list)

    def __call__(self, db: Any, model: str) -> FakeStore:
        return self

    async def write_vectors(
        self,
        *,
        video_id: UUID | str,
        library_id: UUID | str,
        chunk: Sequence[IndexedSegment],
        vectors: Sequence[Sequence[float]],
        language: str | None = None,
    ) -> int:
        for seg, vec in zip(chunk, vectors, strict=True):
            self.rows[seg.segment_id] = _Row(
                str(video_id), str(library_id), language, seg, list(vec)
            )
        self.writes.append(len(chunk))
        return len(chunk)

    async def delete_video(self, video_id: UUID | str) -> None:
        for k in [k for k, r in self.rows.items() if r.video_id == str(video_id)]:
            del self.rows[k]

    async def keep_only(self, video_id: UUID | str, segment_ids: Sequence[int]) -> int:
        keep = set(segment_ids)
        gone = [k for k, r in self.rows.items() if r.video_id == str(video_id) and k not in keep]
        for k in gone:
            del self.rows[k]
        return len(gone)

    async def has_vectors(self, video_id: UUID | str) -> bool:
        return any(r.video_id == str(video_id) for r in self.rows.values())


@pytest.fixture
def store(monkeypatch: pytest.MonkeyPatch) -> Iterator[tuple[FakeStore, FakeEmbedder]]:
    """pgvector ready, the store in memory, the fake embedder."""
    fake, emb = FakeStore(), FakeEmbedder()

    async def ready(db: Any, *, fresh: bool = False) -> StoreState:
        return StoreState(ready=True)

    monkeypatch.setattr(stage, "store_state", ready)
    monkeypatch.setattr(stage, "PgVectorStore", fake)
    stage.set_embedder_factory(lambda: emb)
    yield fake, emb
    stage.reset_embedder_factory()


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
    store: tuple[FakeStore, FakeEmbedder],
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    fake, emb = store
    monkeypatch.setenv("MAKTABA_EMBED_BATCH", "2")
    lib = await make_library(db)
    v = await make_video(db, lib)
    old = await _transcript(db, v, ["قديم"])
    ctx = _Ctx()
    await stage.run(db, _job(v, lib), cast(StageContext, ctx))
    assert {r.segment.transcript_id for r in fake.rows.values()} == {old}

    new = await _transcript(
        db, v, ["صلاة الفجر", "زكاة", "صوم", ""], created=datetime(2026, 3, 1, tzinfo=UTC)
    )
    ctx = _Ctx()
    res = await stage.run(db, _job(v, lib), cast(StageContext, ctx))
    rows = list(fake.rows.values())
    assert {r.segment.transcript_id for r in rows} == {new}  # the old transcript is gone
    assert len(rows) == 3  # the blank segment is not embedded
    assert all(r.library_id == str(lib) and r.video_id == str(v) for r in rows)
    seg_ids = {
        int(r["id"])
        for r in await db.fetch(
            "SELECT id FROM transcript_segments WHERE transcript_id = $1 AND text <> ''", str(new)
        )
    }
    assert set(fake.rows) == seg_ids
    assert [len(b) for b in emb.passages[-2:]] == [2, 1]  # batches of MAKTABA_EMBED_BATCH
    timings = {k: res.summary.pop(k) for k in list(res.summary) if k.endswith("_sec")}
    assert set(timings) == {"embed_sec", "write_sec"}
    assert res.summary == {
        "transcript_id": str(new),
        "segments": 3,
        "model": "fake/e5",
        "stale_ids": 1,  # the old transcript's one segment
    }
    assert ctx.progress_calls[0] == ("embed", 0, 4) and ctx.progress_calls[-1] == ("embed", 4, 4)
    assert ctx.cancel_checks == 2

    again = await stage.run(db, _job(v, lib), cast(StageContext, _Ctx()))
    assert again.output_hash == res.output_hash  # same transcript, same output
    assert again.summary["stale_ids"] == 0
    assert set(fake.rows) == seg_ids


@pytest.mark.asyncio
async def test_run_without_a_transcript_clears_the_video(
    db: Database, store: tuple[FakeStore, FakeEmbedder]
) -> None:
    fake, _ = store
    lib = await make_library(db)
    v = await make_video(db, lib)
    tid = await _transcript(db, v, ["x"])
    await stage.run(db, _job(v, lib), cast(StageContext, _Ctx()))
    assert fake.rows
    await db.execute(
        "UPDATE transcripts SET is_active = $2 WHERE id = $1", str(tid), False if is_pg(db) else 0
    )
    res = await stage.run(db, _job(v, lib), cast(StageContext, _Ctx()))
    assert fake.rows == {} and res.summary["segments"] == 0


@pytest.mark.asyncio
async def test_missing_search_extra_fails_without_retry(
    db: Database, store: tuple[FakeStore, FakeEmbedder]
) -> None:
    class Broken(FakeEmbedder):
        def embed_passages(self, texts: Any) -> list[list[float]]:
            raise SemanticUnavailable("sentence-transformers is not installed")

    stage.set_embedder_factory(Broken)
    lib = await make_library(db)
    v = await make_video(db, lib)
    await _transcript(db, v, ["x"])
    with pytest.raises(StageFailure) as err:
        await stage.run(db, _job(v, lib), cast(StageContext, _Ctx()))
    assert err.value.kind == "search_unavailable" and not err.value.retryable


@pytest.mark.asyncio
async def test_without_pgvector_the_stage_is_unavailable(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    """SQLite, or a Postgres without pgvector: no semantic search (Chroma, which SQLite
    homes used, was retired in story 43.5); the job fails for good, search is full-text."""

    async def not_ready(db: Any, *, fresh: bool = False) -> StoreState:
        return StoreState(ready=False)

    monkeypatch.setattr(stage, "store_state", not_ready)
    lib = await make_library(db)
    v = await make_video(db, lib)
    await _transcript(db, v, ["x"])
    with pytest.raises(StageFailure) as err:
        await stage.run(db, _job(v, lib), cast(StageContext, _Ctx()))
    assert err.value.kind == "search_unavailable" and not err.value.retryable


@pytest.mark.asyncio
async def test_a_blanked_segment_loses_its_vector(
    db: Database, store: tuple[FakeStore, FakeEmbedder]
) -> None:
    """The same transcript, edited: a segment made blank has its vector deleted, the
    others are replaced in place."""
    fake, _ = store
    lib = await make_library(db)
    v = await make_video(db, lib)
    tid = await _transcript(db, v, ["أ", "ب", "ج"])
    await stage.run(db, _job(v, lib), cast(StageContext, _Ctx()))
    assert len(fake.rows) == 3
    await db.execute(
        "UPDATE transcript_segments SET text = '' WHERE transcript_id = $1 AND seq = 1", str(tid)
    )
    res = await stage.run(db, _job(v, lib), cast(StageContext, _Ctx()))
    assert sorted(r.segment.seq for r in fake.rows.values()) == [0, 2]
    assert res.summary["stale_ids"] == 1
