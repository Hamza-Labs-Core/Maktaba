"""ChromaDB vector indexing for transcript segments.

The collection schema:

  - ``ids``       — ``"{transcript_id}:{seq}"`` (deterministic, idempotent upsert).
  - ``documents`` — the raw segment text.
  - ``metadatas`` — ``{video_id, transcript_id, segment_id, start_sec, end_sec,
                       language, speaker?}``.
  - ``embeddings``— produced by the caller-supplied
                    :class:`EmbeddingFunction`.

Why deterministic ids: re-indexing the same transcript is a no-op when
nothing changed. Chroma's upsert semantics match if and only if the id
collides — so we hash by ``(transcript_id, seq)`` and rely on the
caller to bump ``seq`` when a segment is revised (the segment-commit
flow guarantees seq stability per transcript).

The :class:`ChromaCollection` Protocol is a strict subset of the
``chromadb.Collection`` interface — enough for tests to substitute a
dict-backed fake. The real chromadb client is imported lazily inside
:func:`make_collection`, which keeps the package's import cost low.
"""

from __future__ import annotations

from collections.abc import Iterable, Sequence
from dataclasses import dataclass, field
from typing import Any, Protocol
from uuid import UUID

__all__ = [
    "ChromaCollection",
    "EmbeddingFunction",
    "SegmentDoc",
    "SemanticHit",
    "embed_id_for",
    "index_segments",
    "make_collection",
    "semantic_search",
]


@dataclass(slots=True, frozen=True)
class SegmentDoc:
    """One document to index — derived from a ``transcript_segments`` row."""

    segment_id: int
    transcript_id: UUID
    video_id: UUID
    seq: int
    start_sec: float
    end_sec: float
    text: str
    language: str
    speaker: str | None = None
    extra: dict[str, Any] = field(default_factory=dict)


@dataclass(slots=True, frozen=True)
class SemanticHit:
    """One result row from a vector query.

    ``score`` is ``1 - distance`` so larger is better, matching the
    convention :class:`maktaba_pipeline.search.fts.FTSHit` uses.
    """

    segment_id: int
    transcript_id: UUID
    video_id: UUID
    start_sec: float
    end_sec: float
    text: str
    score: float


def embed_id_for(transcript_id: UUID, seq: int) -> str:
    """The collection-key for ``(transcript_id, seq)``."""
    return f"{transcript_id}:{seq}"


class EmbeddingFunction(Protocol):
    """Signature of the embed callback Chroma collections accept.

    Returning lists of lists keeps the shape compatible with both the
    sentence-transformers ``encode`` API and Chroma's native embedding-
    function abstraction without an adapter.
    """

    def __call__(self, texts: Sequence[str]) -> list[list[float]]: ...


class ChromaCollection(Protocol):
    """Subset of ``chromadb.Collection`` we depend on.

    The real Chroma type is dynamically created at runtime, so we can't
    structurally type against it directly; the Protocol pins the surface
    we exercise and lets tests substitute a fake.
    """

    name: str

    def upsert(
        self,
        ids: Sequence[str],
        documents: Sequence[str],
        metadatas: Sequence[dict[str, Any]],
        embeddings: Sequence[Sequence[float]] | None = None,
    ) -> None: ...

    def delete(self, ids: Sequence[str]) -> None: ...

    def query(
        self,
        query_texts: Sequence[str] | None = None,
        query_embeddings: Sequence[Sequence[float]] | None = None,
        n_results: int = 10,
        where: dict[str, Any] | None = None,
    ) -> dict[str, Any]: ...


def make_collection(
    name: str,
    *,
    persist_dir: str | None = None,
    embedding_function: EmbeddingFunction | None = None,
) -> ChromaCollection:
    """Open or create a Chroma collection.

    ``chromadb`` is imported lazily so the pipeline package can be
    inspected (and unit-tested) without the dependency installed.
    """
    try:
        import chromadb  # type: ignore[import-not-found]
    except ImportError as exc:  # pragma: no cover — env-only
        raise RuntimeError(
            "chromadb is not installed; install the optional `search` extra"
        ) from exc

    client = chromadb.PersistentClient(path=persist_dir) if persist_dir else chromadb.Client()
    coll = client.get_or_create_collection(
        name=name,
        embedding_function=embedding_function,
    )
    return coll  # type: ignore[no-any-return]


def _metadata_for(doc: SegmentDoc) -> dict[str, Any]:
    """Project a :class:`SegmentDoc` into the metadata dict Chroma stores."""
    md: dict[str, Any] = {
        "video_id": str(doc.video_id),
        "transcript_id": str(doc.transcript_id),
        "segment_id": doc.segment_id,
        "seq": doc.seq,
        "start_sec": doc.start_sec,
        "end_sec": doc.end_sec,
        "language": doc.language,
    }
    if doc.speaker:
        md["speaker"] = doc.speaker
    for key, value in doc.extra.items():
        # Don't let extra keys shadow the canonical ones above.
        md.setdefault(key, value)
    return md


def index_segments(
    collection: ChromaCollection,
    docs: Iterable[SegmentDoc],
    *,
    embed: EmbeddingFunction | None = None,
) -> int:
    """Upsert ``docs`` into the vector store. Returns the count written.

    When ``embed`` is ``None`` the collection's bound embedding function
    (the one passed at :func:`make_collection` time) is used; otherwise
    embeddings are computed eagerly and shipped alongside the documents.
    The latter path is exercised by tests that want to verify the exact
    vectors stored.
    """
    materialised = list(docs)
    if not materialised:
        return 0
    ids = [embed_id_for(d.transcript_id, d.seq) for d in materialised]
    texts = [d.text for d in materialised]
    metas = [_metadata_for(d) for d in materialised]
    embeddings = embed(texts) if embed is not None else None
    collection.upsert(
        ids=ids,
        documents=texts,
        metadatas=metas,
        embeddings=embeddings,
    )
    return len(materialised)


def semantic_search(
    collection: ChromaCollection,
    query: str,
    *,
    limit: int = 50,
    video_id: UUID | None = None,
    embed: EmbeddingFunction | None = None,
) -> list[SemanticHit]:
    """Run a semantic query against the collection.

    ``video_id`` translates to a Chroma ``where`` clause so the search
    is filtered server-side. When ``embed`` is supplied the query text
    is embedded by the caller; otherwise Chroma's bound embedding
    function handles it.
    """
    if not query.strip():
        return []
    where = {"video_id": str(video_id)} if video_id is not None else None
    if embed is not None:
        vec = embed([query])
        result = collection.query(
            query_embeddings=vec,
            n_results=limit,
            where=where,
        )
    else:
        result = collection.query(
            query_texts=[query],
            n_results=limit,
            where=where,
        )
    return _parse_chroma_result(result)


def _parse_chroma_result(result: dict[str, Any]) -> list[SemanticHit]:
    """Flatten Chroma's nested ``query`` response into :class:`SemanticHit`."""
    if not result:
        return []
    ids_batches = result.get("ids") or []
    documents_batches = result.get("documents") or []
    metadatas_batches = result.get("metadatas") or []
    distances_batches = result.get("distances") or []
    # Chroma always returns one outer list per query string. We pass one
    # query, so we read the first slot.
    if not ids_batches:
        return []
    ids = ids_batches[0]
    documents = documents_batches[0] if documents_batches else [None] * len(ids)
    metadatas = metadatas_batches[0] if metadatas_batches else [{}] * len(ids)
    distances = distances_batches[0] if distances_batches else [0.0] * len(ids)
    hits: list[SemanticHit] = []
    for _idx, meta, doc, dist in zip(ids, metadatas, documents, distances, strict=False):
        md = meta or {}
        score = 1.0 - float(dist) if dist is not None else 0.0
        try:
            hits.append(
                SemanticHit(
                    segment_id=int(md["segment_id"]),
                    transcript_id=UUID(str(md["transcript_id"])),
                    video_id=UUID(str(md["video_id"])),
                    start_sec=float(md["start_sec"]),
                    end_sec=float(md["end_sec"]),
                    text=str(doc) if doc is not None else "",
                    score=score,
                )
            )
        except (KeyError, ValueError):
            # Skip malformed rows rather than fail the whole query.
            continue
    return hits
