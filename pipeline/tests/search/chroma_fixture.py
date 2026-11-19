"""The Chroma folder ``tests/search/data/chroma-1.5.9`` and the vectors it holds (story 43.5).

The copy of the Chroma vectors into Postgres (``search/chroma_copy.py``) reads Chroma's
files, never Chroma itself, and chromadb is no longer a dependency. Its tests read a
folder Chroma 1.5.9 (the release the homes ran) wrote, made by this module once::

    uv venv /tmp/c && uv pip install -p /tmp/c chromadb==1.5.9 numpy
    /tmp/c/bin/python pipeline/tests/search/chroma_fixture.py \
        pipeline/tests/search/data/chroma-1.5.9

``expected()`` gives what it holds, from the same seed, without chromadb.
"""

from __future__ import annotations

import sys
import uuid
from typing import Any

import numpy as np

COLLECTION = "maktaba_segments-intfloat-multilingual-e5-small"
DIM = 384
PER = 50
#: the transcripts in the database's fixture; ORPHAN's is not (its vectors are orphans)
TRANSCRIPTS = [uuid.UUID(int=0x43_05_0000 + i) for i in range(1, 6)]
ORPHAN = uuid.UUID(int=0x43_05_FFFF)
#: Chroma writes its files every SYNC additions: the writes after the last are only in
#: its queue
SYNC = 100


def _unit(rng: Any, n: int) -> Any:
    v = rng.standard_normal((n, DIM)).astype(np.float32)
    return v / np.linalg.norm(v, axis=1, keepdims=True)


def plan() -> tuple[list[tuple[str, list[str], Any]], list[str], tuple[str, Any]]:
    """The writes: (transcript, its ids, their vectors) in order, then the deletes, then
    one id written again with a new vector."""
    rng = np.random.default_rng(7)
    writes = []
    for tid in [ORPHAN, *TRANSCRIPTS]:
        writes.append((str(tid), [f"{tid}:{s}" for s in range(PER)], _unit(rng, PER)))
    # deleted after the last write of the files: the files still hold them, the queue
    # says they are gone (two of the first transcript, two of the last)
    first, last = TRANSCRIPTS[0], TRANSCRIPTS[-1]
    gone = [f"{first}:0", f"{first}:1", f"{last}:5", f"{last}:6"]
    again = (f"{TRANSCRIPTS[-1]}:7", _unit(rng, 1)[0])
    return writes, gone, again


def expected() -> dict[str, Any]:
    """Every live id and its vector, as the folder holds it."""
    writes, gone, again = plan()
    out: dict[str, Any] = {}
    for _tid, ids, vecs in writes:
        out.update(zip(ids, vecs, strict=True))
    for g in gone:
        out.pop(g)
    out[again[0]] = again[1]
    return out


def build(path: str) -> None:  # pragma: no cover - needs chromadb
    import chromadb  # type: ignore[import-not-found]  # the generator's own environment

    settings = chromadb.Settings(anonymized_telemetry=False)
    client = chromadb.PersistentClient(path=path, settings=settings)
    coll = client.get_or_create_collection(
        name=COLLECTION, metadata={"hnsw:space": "cosine"}, embedding_function=None
    )
    coll.modify(configuration={"hnsw": {"sync_threshold": SYNC, "batch_size": SYNC}})
    coll = client.get_collection(name=COLLECTION, embedding_function=None)
    writes, gone, again = plan()
    for tid, ids, vecs in writes:
        coll.upsert(
            ids=ids,
            embeddings=vecs,
            metadatas=[{"transcript_id": tid, "seq": s} for s in range(len(ids))],
            documents=["x"] * len(ids),
        )
    coll.delete(ids=gone)
    eid, vec = again
    coll.upsert(ids=[eid], embeddings=[vec], metadatas=[{"seq": 7}], documents=["x"])


if __name__ == "__main__":  # pragma: no cover
    build(sys.argv[1])
