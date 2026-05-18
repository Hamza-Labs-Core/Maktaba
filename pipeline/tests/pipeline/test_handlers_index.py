"""Track R4 — real INDEX stage adapter.

The adapter is a thin glue layer mirroring :func:`transcribe_handler`:

1. resolve the TRANSCRIBE-produced transcript for the job's
   ``payload.transcript_id`` (header for ``video_id`` / ``language`` +
   every committed ``transcript_segments`` row ordered by ``seq``),
2. resolve the target Chroma collection + embed fn via the DI seam
   (default = the configured process-wide collection; tests inject a
   fake collection + deterministic embed so no model loads and no
   network is touched),
3. embed + upsert every segment via the existing
   :func:`search.embedder.index_segments` (heavy logic stays there),
4. advance the FSM ``TRANSCRIBED -> INDEXED`` (replay-guarded exactly
   like ``commit_transcribe``); INDEX has no follow-on enqueue,
5. flip the job ``done``.

These tests assert the adapter wires those pieces together against the
*same* DB contract the libraries already expect (the shared
:class:`StageDB` fake seeded with TRANSCRIBE-produced rows + a fake
in-memory collection). Async work is driven via :func:`asyncio.run` —
the same pattern ``tests/pipeline/test_handlers_transcribe.py`` uses,
and for the same netguard reason; these tests are intentionally NOT
``unit``-marked.
"""

from __future__ import annotations

import asyncio
import json
from collections.abc import Sequence
from dataclasses import dataclass, field
from typing import Any
from uuid import UUID, uuid4

from maktaba_pipeline.db.jobs import Stage
from maktaba_pipeline.handlers import index_handler
from maktaba_pipeline.search.index_stage import IndexTarget
from tests.audio._fake_audio_db import _ProcessingJobRow

from .conftest import StageDB, make_job


@dataclass
class _FakeCollection:
    """In-memory stand-in for ``chromadb.Collection``.

    Records every upsert so tests can assert the exact ids / documents /
    metadata / embeddings and verify idempotent re-runs do not duplicate
    vectors (Chroma upsert overwrites by id — modelled here as a dict).
    """

    name: str = "test-segments"
    store: dict[str, dict[str, Any]] = field(default_factory=dict)
    upsert_calls: list[dict[str, Any]] = field(default_factory=list)

    def upsert(
        self,
        ids: Sequence[str],
        documents: Sequence[str],
        metadatas: Sequence[dict[str, Any]],
        embeddings: Sequence[Sequence[float]] | None = None,
    ) -> None:
        self.upsert_calls.append(
            {
                "ids": list(ids),
                "documents": list(documents),
                "metadatas": [dict(m) for m in metadatas],
                "embeddings": [list(e) for e in embeddings] if embeddings else None,
            }
        )
        for i, vid_id in enumerate(ids):
            self.store[vid_id] = {
                "document": documents[i],
                "metadata": dict(metadatas[i]),
                "embedding": list(embeddings[i]) if embeddings else None,
            }

    def delete(self, ids: Sequence[str]) -> None:  # pragma: no cover - unused
        for i in ids:
            self.store.pop(i, None)

    def query(self, *a: Any, **k: Any) -> dict[str, Any]:  # pragma: no cover
        return {}


def _deterministic_embed(texts: Sequence[str]) -> list[list[float]]:
    """A pure, deterministic embed — no model load, no network."""
    return [[float(len(t)), float(sum(ord(c) for c in t) % 97)] for t in texts]


def _seed_claimed_index_job(
    stage_db: StageDB,
    *,
    job_id: int,
    video_id: UUID,
    transcript_id: UUID | None,
    audio_track_id: int = 1,
) -> None:
    """Mirror an INDEX row the ClaimLoop flipped to ``running``.

    TRANSCRIBE enqueued it with ``payload={transcript_id, audio_track_id}``.
    """
    payload = (
        None
        if transcript_id is None
        else json.dumps(
            {
                "transcript_id": str(transcript_id),
                "audio_track_id": audio_track_id,
            }
        )
    )
    stage_db.processing_jobs[job_id] = _ProcessingJobRow(
        id=job_id,
        video_id=video_id,
        stage=Stage.INDEX.value,
        state="running",
        payload=payload,
    )
    stage_db._job_next_id = max(stage_db._job_next_id, job_id + 1)  # noqa: SLF001


def test_index_handler_loads_segments_indexes_and_advances() -> None:
    stage_db = StageDB(dialect="postgres")
    video_id = stage_db.add_video(state="transcribed")
    tid = stage_db.seed_transcript(video_id=video_id, language="ara")
    job = make_job(
        job_id=40,
        video_id=video_id,
        stage=Stage.INDEX,
        payload={"transcript_id": str(tid), "audio_track_id": 1},
    )
    _seed_claimed_index_job(stage_db, job_id=40, video_id=video_id, transcript_id=tid)

    coll = _FakeCollection()
    picks: list[UUID] = []

    async def fake_resolve(*, video_id: UUID) -> IndexTarget:
        picks.append(video_id)
        return IndexTarget(collection=coll, embed=_deterministic_embed)

    asyncio.run(index_handler(stage_db, job, resolve_target=fake_resolve))

    # the collection was resolved via the injected DI seam.
    assert picks == [video_id]
    # exactly one upsert with every segment, ordered by seq, with
    # deterministic ids {transcript_id}:{seq}.
    assert len(coll.upsert_calls) == 1
    call = coll.upsert_calls[0]
    assert call["ids"] == [f"{tid}:0", f"{tid}:1", f"{tid}:2"]
    assert call["documents"] == ["bismillah", "al-hamdu", "lillah"]
    # metadata carries the transcript's video_id + language.
    md = call["metadatas"][0]
    assert md["video_id"] == str(video_id)
    assert md["transcript_id"] == str(tid)
    assert md["language"] == "ara"
    assert md["seq"] == 0
    # the deterministic embed was used (no model load).
    assert call["embeddings"] == _deterministic_embed(
        ["bismillah", "al-hamdu", "lillah"]
    )
    # FSM advanced TRANSCRIBED -> INDEXED.
    assert stage_db.videos[video_id].state == "indexed"
    # INDEX has no follow-on enqueue (THUMBNAIL has no module yet).
    assert not [
        pj
        for pj in stage_db.processing_jobs.values()
        if pj.stage in (Stage.THUMBNAIL.value, Stage.SUBTITLE_GEN.value)
    ]
    # the INDEX job itself is done.
    assert stage_db.processing_jobs[40].state == "done"


def test_index_handler_idempotent_on_rerun_no_dup_vectors() -> None:
    """Re-running INDEX (retried job) is replay-safe: the same
    deterministic ids upsert in place — no duplicate vectors — and the
    FSM advance is a no-op past TRANSCRIBED (it is already INDEXED)."""
    stage_db = StageDB(dialect="postgres")
    video_id = stage_db.add_video(state="transcribed")
    tid = stage_db.seed_transcript(video_id=video_id)
    coll = _FakeCollection()

    async def fake_resolve(*, video_id: UUID) -> IndexTarget:  # noqa: ARG001
        return IndexTarget(collection=coll, embed=_deterministic_embed)

    job1 = make_job(
        job_id=41,
        video_id=video_id,
        stage=Stage.INDEX,
        payload={"transcript_id": str(tid), "audio_track_id": 1},
    )
    _seed_claimed_index_job(stage_db, job_id=41, video_id=video_id, transcript_id=tid)
    asyncio.run(index_handler(stage_db, job1, resolve_target=fake_resolve))

    job2 = make_job(
        job_id=42,
        video_id=video_id,
        stage=Stage.INDEX,
        payload={"transcript_id": str(tid), "audio_track_id": 1},
    )
    _seed_claimed_index_job(stage_db, job_id=42, video_id=video_id, transcript_id=tid)
    asyncio.run(index_handler(stage_db, job2, resolve_target=fake_resolve))

    assert stage_db.processing_jobs[41].state == "done"
    assert stage_db.processing_jobs[42].state == "done"
    assert stage_db.videos[video_id].state == "indexed"
    # Two upsert *calls* (one per run) but the store has exactly the 3
    # vectors — same ids overwrote in place, no duplicates.
    assert len(coll.upsert_calls) == 2
    assert sorted(coll.store) == [f"{tid}:0", f"{tid}:1", f"{tid}:2"]


def test_index_handler_converges_with_subtitle_gen() -> None:
    """INDEX + SUBTITLE_GEN both branch off TRANSCRIBED and converge on
    INDEXED. If the state is already past TRANSCRIBED (SUBTITLE_GEN ran
    first), INDEX must NOT double-advance / raise — it leaves the state
    and still indexes + marks done."""
    stage_db = StageDB(dialect="postgres")
    video_id = stage_db.add_video(state="indexed")  # SUBTITLE_GEN won the race
    tid = stage_db.seed_transcript(video_id=video_id)
    job = make_job(
        job_id=43,
        video_id=video_id,
        stage=Stage.INDEX,
        payload={"transcript_id": str(tid), "audio_track_id": 1},
    )
    _seed_claimed_index_job(stage_db, job_id=43, video_id=video_id, transcript_id=tid)
    coll = _FakeCollection()

    async def fake_resolve(*, video_id: UUID) -> IndexTarget:  # noqa: ARG001
        return IndexTarget(collection=coll, embed=_deterministic_embed)

    asyncio.run(index_handler(stage_db, job, resolve_target=fake_resolve))

    # State stayed INDEXED (no illegal transition), segments still indexed,
    # job done.
    assert stage_db.videos[video_id].state == "indexed"
    assert len(coll.store) == 3
    assert stage_db.processing_jobs[43].state == "done"


def test_index_handler_missing_payload_is_non_retryable() -> None:
    stage_db = StageDB(dialect="postgres")
    video_id = stage_db.add_video(state="transcribed")
    job = make_job(job_id=44, video_id=video_id, stage=Stage.INDEX)
    _seed_claimed_index_job(
        stage_db, job_id=44, video_id=video_id, transcript_id=None
    )

    async def fake_resolve(*, video_id: UUID) -> IndexTarget:  # noqa: ARG001  # pragma: no cover
        raise AssertionError("collection must not be resolved with no payload")

    asyncio.run(index_handler(stage_db, job, resolve_target=fake_resolve))

    assert stage_db.processing_jobs[44].state == "failed"
    err = json.loads(stage_db.processing_jobs[44].error or "{}")
    assert err["retryable"] is False
    assert err["kind"] == "index_missing_payload"


def test_index_handler_missing_transcript_is_non_retryable() -> None:
    """No ``transcripts`` row for the payload id → a data inconsistency
    (TRANSCRIBE must have activated it); non-retryable."""
    stage_db = StageDB(dialect="postgres")
    video_id = stage_db.add_video(state="transcribed")
    ghost = uuid4()
    job = make_job(
        job_id=45,
        video_id=video_id,
        stage=Stage.INDEX,
        payload={"transcript_id": str(ghost), "audio_track_id": 1},
    )
    _seed_claimed_index_job(
        stage_db, job_id=45, video_id=video_id, transcript_id=ghost
    )

    async def fake_resolve(*, video_id: UUID) -> IndexTarget:  # noqa: ARG001  # pragma: no cover
        raise AssertionError("collection must not be resolved with no transcript")

    asyncio.run(index_handler(stage_db, job, resolve_target=fake_resolve))

    assert stage_db.processing_jobs[45].state == "failed"
    err = json.loads(stage_db.processing_jobs[45].error or "{}")
    assert err["retryable"] is False
    assert err["kind"] == "index_missing_transcript"


def test_index_handler_no_segments_is_non_retryable() -> None:
    """A transcript header with zero segments is a data inconsistency
    (a committed transcript always has >= 1 segment); non-retryable."""
    stage_db = StageDB(dialect="postgres")
    video_id = stage_db.add_video(state="transcribed")
    tid = stage_db.seed_transcript(video_id=video_id, segments=[])
    job = make_job(
        job_id=46,
        video_id=video_id,
        stage=Stage.INDEX,
        payload={"transcript_id": str(tid), "audio_track_id": 1},
    )
    _seed_claimed_index_job(stage_db, job_id=46, video_id=video_id, transcript_id=tid)

    async def fake_resolve(*, video_id: UUID) -> IndexTarget:  # noqa: ARG001  # pragma: no cover
        raise AssertionError("collection must not be resolved with no segments")

    asyncio.run(index_handler(stage_db, job, resolve_target=fake_resolve))

    assert stage_db.processing_jobs[46].state == "failed"
    err = json.loads(stage_db.processing_jobs[46].error or "{}")
    assert err["retryable"] is False
    assert err["kind"] == "index_no_segments"


def test_index_handler_vector_store_error_is_retryable() -> None:
    """A transient embed / vector-store failure is retryable; the FSM
    must not advance prematurely."""
    stage_db = StageDB(dialect="postgres")
    video_id = stage_db.add_video(state="transcribed")
    tid = stage_db.seed_transcript(video_id=video_id)
    job = make_job(
        job_id=47,
        video_id=video_id,
        stage=Stage.INDEX,
        payload={"transcript_id": str(tid), "audio_track_id": 1},
    )
    _seed_claimed_index_job(stage_db, job_id=47, video_id=video_id, transcript_id=tid)

    class _BoomCollection(_FakeCollection):
        def upsert(self, *a: Any, **k: Any) -> None:
            raise OSError("chroma write pipe broke")

    async def fake_resolve(*, video_id: UUID) -> IndexTarget:  # noqa: ARG001
        return IndexTarget(collection=_BoomCollection(), embed=_deterministic_embed)

    asyncio.run(index_handler(stage_db, job, resolve_target=fake_resolve))

    # attempts(1) < max_attempts(3) and retryable → pending.
    assert stage_db.processing_jobs[47].state == "pending"
    err = json.loads(stage_db.processing_jobs[47].error or "{}")
    assert err["retryable"] is True
    assert err["kind"] == "index_failed"
    # FSM stayed put — no premature INDEXED.
    assert stage_db.videos[video_id].state == "transcribed"


def test_index_handler_video_vanished_is_non_retryable() -> None:
    """TOCTOU: the video row vanished before commit_index's state read.
    Terminal — a re-run cannot resurrect it (LookupError guard)."""
    stage_db = StageDB(dialect="postgres")
    video_id = stage_db.add_video(state="transcribed")
    tid = stage_db.seed_transcript(video_id=video_id)
    job = make_job(
        job_id=48,
        video_id=video_id,
        stage=Stage.INDEX,
        payload={"transcript_id": str(tid), "audio_track_id": 1},
    )
    _seed_claimed_index_job(stage_db, job_id=48, video_id=video_id, transcript_id=tid)
    # Drop the video row AFTER the transcript is seeded (header read
    # joins on transcript_id, not videos, so load still succeeds; the
    # state read inside commit_index then misses).
    del stage_db.videos[video_id]

    coll = _FakeCollection()

    async def fake_resolve(*, video_id: UUID) -> IndexTarget:  # noqa: ARG001
        return IndexTarget(collection=coll, embed=_deterministic_embed)

    asyncio.run(index_handler(stage_db, job, resolve_target=fake_resolve))

    assert stage_db.processing_jobs[48].state == "failed"
    err = json.loads(stage_db.processing_jobs[48].error or "{}")
    assert err["retryable"] is False
    assert err["kind"] == "index_video_vanished"
