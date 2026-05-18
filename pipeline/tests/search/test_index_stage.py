"""Track R4 — :mod:`maktaba_pipeline.search.index_stage` library tests.

Exercises the heavy logic the thin ``index_handler`` adapter delegates
to, against the canonical :class:`FakeAudioDB` (the same in-memory
contract ``commit_transcribe`` writes through) + an in-memory fake
collection. Async + DB-driven, so — like the audio/handler suites —
these are intentionally NOT ``unit``-marked (netguard reason).
"""

from __future__ import annotations

import asyncio
from collections.abc import Sequence
from dataclasses import dataclass, field
from typing import Any
from uuid import uuid4

from maktaba_pipeline.search.embedder import embed_id_for
from maktaba_pipeline.search.index_stage import (
    IndexTarget,
    commit_index,
    load_segment_docs,
)
from tests.audio._fake_audio_db import FakeAudioDB


@dataclass
class _FakeCollection:
    name: str = "t"
    store: dict[str, dict[str, Any]] = field(default_factory=dict)
    calls: int = 0

    def upsert(
        self,
        ids: Sequence[str],
        documents: Sequence[str],
        metadatas: Sequence[dict[str, Any]],
        embeddings: Sequence[Sequence[float]] | None = None,
    ) -> None:
        self.calls += 1
        for i, k in enumerate(ids):
            self.store[k] = {"doc": documents[i], "md": dict(metadatas[i])}

    def delete(self, ids: Sequence[str]) -> None: ...  # pragma: no cover
    def query(self, *a: Any, **k: Any) -> dict[str, Any]:  # pragma: no cover
        return {}


def _embed(texts: Sequence[str]) -> list[list[float]]:
    return [[float(len(t))] for t in texts]


def test_load_segment_docs_maps_rows_to_segmentdocs() -> None:
    db = FakeAudioDB(dialect="postgres")
    video_id = db.add_video(state="transcribed")
    tid = db.seed_transcript(
        video_id=video_id,
        language="eng",
        segments=[(0, 0.0, 1.5, "hello"), (1, 1.5, 3.0, "world")],
    )

    docs = asyncio.run(load_segment_docs(db, transcript_id=tid))

    assert docs is not None
    assert [d.seq for d in docs] == [0, 1]
    assert [d.text for d in docs] == ["hello", "world"]
    assert all(d.video_id == video_id for d in docs)
    assert all(d.transcript_id == tid for d in docs)
    assert all(d.language == "eng" for d in docs)
    assert docs[0].start_sec == 0.0
    assert docs[1].end_sec == 3.0


def test_load_segment_docs_missing_transcript_returns_none() -> None:
    db = FakeAudioDB(dialect="postgres")
    assert asyncio.run(load_segment_docs(db, transcript_id=uuid4())) is None


def test_load_segment_docs_no_segments_returns_empty_list() -> None:
    db = FakeAudioDB(dialect="postgres")
    video_id = db.add_video(state="transcribed")
    tid = db.seed_transcript(video_id=video_id, segments=[])
    docs = asyncio.run(load_segment_docs(db, transcript_id=tid))
    assert docs == []


def test_commit_index_upserts_with_deterministic_ids_and_advances() -> None:
    db = FakeAudioDB(dialect="postgres")
    video_id = db.add_video(state="transcribed")
    tid = db.seed_transcript(video_id=video_id)
    docs = asyncio.run(load_segment_docs(db, transcript_id=tid))
    assert docs is not None
    coll = _FakeCollection()

    count, new_state = asyncio.run(
        commit_index(
            db,
            video_id=video_id,
            transcript_id=tid,
            docs=docs,
            target=IndexTarget(collection=coll, embed=_embed),
        )
    )

    assert count == 3
    assert new_state == "indexed"
    assert sorted(coll.store) == [embed_id_for(tid, s) for s in (0, 1, 2)]
    assert db.videos[video_id].state == "indexed"


def test_commit_index_idempotent_rerun_no_dup_vectors() -> None:
    db = FakeAudioDB(dialect="postgres")
    video_id = db.add_video(state="transcribed")
    tid = db.seed_transcript(video_id=video_id)
    docs = asyncio.run(load_segment_docs(db, transcript_id=tid))
    assert docs is not None
    coll = _FakeCollection()
    target = IndexTarget(collection=coll, embed=_embed)

    asyncio.run(
        commit_index(db, video_id=video_id, transcript_id=tid, docs=docs, target=target)
    )
    # Second run: already INDEXED — FSM advance is a no-op, ids overwrite.
    count2, state2 = asyncio.run(
        commit_index(db, video_id=video_id, transcript_id=tid, docs=docs, target=target)
    )

    assert count2 == 3
    assert state2 == "indexed"
    assert coll.calls == 2
    assert len(coll.store) == 3  # same 3 ids, overwritten — no duplicates


def test_commit_index_missing_video_raises_lookuperror() -> None:
    db = FakeAudioDB(dialect="postgres")
    video_id = db.add_video(state="transcribed")
    tid = db.seed_transcript(video_id=video_id)
    docs = asyncio.run(load_segment_docs(db, transcript_id=tid))
    assert docs is not None
    del db.videos[video_id]

    raised = False
    try:
        asyncio.run(
            commit_index(
                db,
                video_id=video_id,
                transcript_id=tid,
                docs=docs,
                target=IndexTarget(collection=_FakeCollection(), embed=_embed),
            )
        )
    except LookupError:
        raised = True
    assert raised
