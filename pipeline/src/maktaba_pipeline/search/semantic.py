"""Semantic search over transcript segments: the embedder, the vector store, the query.

Both sides of semantic search live here so they can never disagree on the model, the
prefixes or the collection:

* the engine ``index`` stage (:mod:`maktaba_pipeline.engine.stages.index`) embeds a
  video's active transcript with :meth:`E5Embedder.embed_passages` (``"passage: "``) and
  :meth:`SemanticIndex.replace_video` swaps the video's vectors;
* the gRPC server (:mod:`maktaba_pipeline.grpc_server`) embeds a query with
  :meth:`E5Embedder.embed_query` (``"query: "``) and :meth:`SemanticIndex.search` returns
  the top-k segments, filtered by library.

The store is one persistent Chroma collection (cosine space) per embedding model, at
``MAKTABA_CHROMA_DIR`` (default ``~/.maktaba/chroma``); vectors of two models never share
a collection, so changing ``MAKTABA_EMBED_MODEL`` cannot mix dimensions. Vector ids are
:func:`~maktaba_pipeline.search.embedder.embed_id_for` (``{transcript_id}:{seq}``), and
every vector carries ``video_id``, ``library_id``, ``transcript_id``, ``segment_id``,
``seq``, ``start_sec`` and ``end_sec``.

``chromadb``, ``sentence_transformers`` and ``torch`` come from the optional ``search``
extra and are imported lazily (through :func:`importlib.import_module`, so type checking
does not depend on whether the extra is installed).
"""

from __future__ import annotations

import importlib
import os
import re
import threading
from collections.abc import Callable, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Protocol
from uuid import UUID

from .embedder import embed_id_for

__all__ = [
    "DEFAULT_BATCH_SIZE",
    "DEFAULT_CHROMA_DIR",
    "DEFAULT_EMBED_MODEL",
    "INDEX_VERSION",
    "E5Embedder",
    "Embedder",
    "IndexedSegment",
    "SegmentHit",
    "SemanticIndex",
    "SemanticUnavailable",
    "VectorCollection",
    "batch_size",
    "chroma_dir",
    "collection_name",
    "embed_model",
    "get_embedder",
    "open_collection",
    "parse_hits",
    "pick_device",
    "search_available",
]

#: bump when what a vector holds changes (text normalisation, metadata, prefixes): every
#: video is indexed again
#: 2: each vector carries its video's detected language (the search's language filter)
INDEX_VERSION = 2

DEFAULT_EMBED_MODEL = "intfloat/multilingual-e5-small"
DEFAULT_CHROMA_DIR = "~/.maktaba/chroma"
DEFAULT_BATCH_SIZE = 64
DEFAULT_COLLECTION_PREFIX = "maktaba_segments"

MODEL_ENV = "MAKTABA_EMBED_MODEL"
DEVICE_ENV = "MAKTABA_EMBED_DEVICE"
BATCH_ENV = "MAKTABA_EMBED_BATCH"
CHROMA_DIR_ENV = "MAKTABA_CHROMA_DIR"
COLLECTION_ENV = "MAKTABA_CHROMA_COLLECTION"

PASSAGE_PREFIX = "passage: "
QUERY_PREFIX = "query: "


class SemanticUnavailable(RuntimeError):
    """The ``search`` extra (chromadb, sentence-transformers) is not installed."""


def embed_model() -> str:
    """The embedding model: ``MAKTABA_EMBED_MODEL`` or the multilingual e5-small default."""
    return os.getenv(MODEL_ENV, "").strip() or DEFAULT_EMBED_MODEL


def chroma_dir() -> str:
    """Where the persistent Chroma lives (``MAKTABA_CHROMA_DIR``, ``~`` expanded)."""
    raw = os.getenv(CHROMA_DIR_ENV, "").strip() or DEFAULT_CHROMA_DIR
    return str(Path(raw).expanduser())


def batch_size() -> int:
    raw = os.getenv(BATCH_ENV, "").strip()
    try:
        n = int(raw) if raw else DEFAULT_BATCH_SIZE
    except ValueError:
        n = DEFAULT_BATCH_SIZE
    return max(1, n)


def collection_name(model: str, prefix: str | None = None) -> str:
    """The collection of ``model``'s vectors: ``maktaba_segments-intfloat-multilingual-e5-small``.

    Chroma names allow ``[A-Za-z0-9._-]`` and must start and end with a letter or digit.
    """
    base = prefix or os.getenv(COLLECTION_ENV, "").strip() or DEFAULT_COLLECTION_PREFIX
    slug = re.sub(r"[^A-Za-z0-9._-]+", "-", model).strip("-._")
    name = f"{base}-{slug}".strip("-._")
    return name[:512]


def search_available() -> bool:
    """True when chromadb and sentence-transformers can be imported."""
    try:
        importlib.import_module("chromadb")
        importlib.import_module("sentence_transformers")
    except ImportError:
        return False
    return True


# --------------------------------------------------------------------------- embedder


class Embedder(Protocol):
    """What the stage and the query side need from an embedding model."""

    model_name: str

    def embed_passages(self, texts: Sequence[str]) -> list[list[float]]: ...

    def embed_query(self, text: str) -> list[float]: ...


def pick_device(torch: Any | None = None) -> str:
    """``MAKTABA_EMBED_DEVICE`` when set, else CUDA, else Apple's GPU (MPS), else CPU."""
    forced = os.getenv(DEVICE_ENV, "").strip()
    if forced:
        return forced
    if torch is None:
        try:
            torch = importlib.import_module("torch")
        except ImportError:
            return "cpu"
    try:
        if torch.cuda.is_available():
            return "cuda"
        mps = getattr(torch.backends, "mps", None)
        if mps is not None and mps.is_available():
            return "mps"
    except Exception:  # noqa: BLE001 — a broken accelerator probe means CPU
        return "cpu"
    return "cpu"


class E5Embedder:
    """An e5-family sentence-transformers model: ``passage: `` / ``query: `` prefixes,
    normalised vectors (so cosine distance is ``1 - dot``), batched encoding.

    The model is loaded on first use (thread-safe), on :func:`pick_device`.
    """

    def __init__(
        self,
        model_name: str | None = None,
        *,
        device: str | None = None,
        batch: int | None = None,
        loader: Callable[[str, str], Any] | None = None,
    ) -> None:
        self.model_name = model_name or embed_model()
        self._device = device
        self._batch = batch or batch_size()
        self._loader = loader
        self._model: Any = None
        self._lock = threading.Lock()
        # Metal (MPS) must not be driven from two threads at once: index jobs run in
        # worker threads and queries in the gRPC server's, so encoding is one at a time
        # (a concurrent encode aborted the worker: MTLCommandBuffer status assertion)
        self._run_lock = threading.Lock()

    @property
    def device(self) -> str:
        if self._device is None:
            self._device = pick_device()
        return self._device

    def _load(self) -> Any:
        if self._model is not None:
            return self._model
        with self._lock:
            if self._model is None:
                if self._loader is not None:
                    self._model = self._loader(self.model_name, self.device)
                else:
                    try:
                        st = importlib.import_module("sentence_transformers")
                    except ImportError as exc:
                        raise SemanticUnavailable(
                            "sentence-transformers is not installed; install the optional "
                            "`search` extra (uv sync --extra search)"
                        ) from exc
                    self._model = st.SentenceTransformer(self.model_name, device=self.device)
        return self._model

    def warm(self) -> None:
        """Load the model now (and run it once) so the first query is not slow."""
        self.embed_query("warm up")

    def _encode(self, texts: list[str]) -> list[list[float]]:
        model = self._load()
        with self._run_lock:
            vecs = model.encode(
                texts,
                batch_size=self._batch,
                normalize_embeddings=True,
                convert_to_numpy=True,
                show_progress_bar=False,
            )
        return [[float(x) for x in v] for v in vecs]

    def embed_passages(self, texts: Sequence[str]) -> list[list[float]]:
        if not texts:
            return []
        return self._encode([PASSAGE_PREFIX + t for t in texts])

    def embed_query(self, text: str) -> list[float]:
        return self._encode([QUERY_PREFIX + text])[0]


_EMBEDDERS: dict[str, E5Embedder] = {}
_EMBEDDERS_LOCK = threading.Lock()


def get_embedder(model: str | None = None) -> E5Embedder:
    """The process-wide embedder of ``model`` (loaded once, shared by stage and RPC)."""
    name = model or embed_model()
    with _EMBEDDERS_LOCK:
        emb = _EMBEDDERS.get(name)
        if emb is None:
            emb = _EMBEDDERS[name] = E5Embedder(name)
        return emb


_QUERY_EMBEDDERS: dict[tuple[str, str], E5Embedder] = {}


def get_query_embedder(model: str | None = None) -> E5Embedder:
    """The process-wide embedder for search queries: the same model as the index, on
    ``MAKTABA_QUERY_EMBED_DEVICE`` (default ``cpu``). One short sentence embeds in
    milliseconds on the CPU and never waits behind the index stage's GPU batches (one
    Metal user per process at a time) while a library is being indexed."""
    name = model or embed_model()
    device = os.environ.get("MAKTABA_QUERY_EMBED_DEVICE", "cpu")
    with _EMBEDDERS_LOCK:
        emb = _QUERY_EMBEDDERS.get((name, device))
        if emb is None:
            emb = _QUERY_EMBEDDERS[(name, device)] = E5Embedder(name, device=device)
        return emb


# --------------------------------------------------------------------------- store


class VectorCollection(Protocol):
    """The part of ``chromadb.Collection`` used here (tests pass an in-memory fake)."""

    def upsert(
        self,
        ids: Sequence[str],
        embeddings: Sequence[Sequence[float]],
        metadatas: Sequence[dict[str, Any]],
        documents: Sequence[str],
    ) -> None: ...

    def delete(self, where: dict[str, Any]) -> None: ...

    def query(
        self,
        query_embeddings: Sequence[Sequence[float]],
        n_results: int,
        where: dict[str, Any] | None = None,
        include: Sequence[str] = ...,
    ) -> dict[str, Any]: ...


_CLIENTS: dict[str, Any] = {}
_COLLECTIONS: dict[tuple[str, str], Any] = {}
_STORE_LOCK = threading.Lock()


def open_collection(model: str | None = None, persist_dir: str | None = None) -> Any:
    """The persistent Chroma collection of ``model``'s vectors (one client per directory
    per process: Chroma wants a single writer)."""
    name = collection_name(model or embed_model())
    path = persist_dir or chroma_dir()
    with _STORE_LOCK:
        key = (path, name)
        if key in _COLLECTIONS:
            return _COLLECTIONS[key]
        try:
            chromadb = importlib.import_module("chromadb")
        except ImportError as exc:
            raise SemanticUnavailable(
                "chromadb is not installed; install the optional `search` extra "
                "(uv sync --extra search)"
            ) from exc
        client = _CLIENTS.get(path)
        if client is None:
            Path(path).mkdir(parents=True, exist_ok=True)
            client = chromadb.PersistentClient(
                path=path, settings=chromadb.Settings(anonymized_telemetry=False)
            )
            _CLIENTS[path] = client
        coll = client.get_or_create_collection(
            name=name, metadata={"hnsw:space": "cosine"}, embedding_function=None
        )
        _COLLECTIONS[key] = coll
        return coll


@dataclass(frozen=True, slots=True)
class IndexedSegment:
    """One segment to embed: a ``transcript_segments`` row and where it belongs."""

    segment_id: int
    transcript_id: UUID
    seq: int
    start_sec: float
    end_sec: float
    text: str


@dataclass(frozen=True, slots=True)
class SegmentHit:
    segment_id: int
    video_id: str
    start_sec: float
    end_sec: float
    score: float
    text: str = ""

    def to_json(self) -> dict[str, Any]:
        return {
            "segment_id": self.segment_id,
            "video_id": self.video_id,
            "start_sec": self.start_sec,
            "end_sec": self.end_sec,
            "score": self.score,
            "text": self.text,
        }


class SemanticIndex:
    """The vector store for one embedding model."""

    def __init__(self, collection: VectorCollection, embedder: Embedder) -> None:
        self.collection = collection
        self.embedder = embedder

    @classmethod
    def for_queries(cls) -> SemanticIndex:
        """The shared collection, read with the query embedder."""
        model = embed_model()
        return cls(open_collection(model), get_query_embedder(model))

    @classmethod
    def default(cls) -> SemanticIndex:
        model = embed_model()
        return cls(open_collection(model), get_embedder(model))

    def delete_video(self, video_id: UUID | str) -> None:
        self.collection.delete(where={"video_id": str(video_id)})

    def upsert_segments(
        self,
        *,
        video_id: UUID | str,
        library_id: UUID | str,
        segments: Sequence[IndexedSegment],
        language: str | None = None,
    ) -> int:
        """Embed ``segments`` (one batch) and upsert them; blank ones are skipped."""
        chunk = [s for s in segments if s.text.strip()]
        if not chunk:
            return 0
        vecs = self.embedder.embed_passages([s.text for s in chunk])
        self.collection.upsert(
            ids=[embed_id_for(s.transcript_id, s.seq) for s in chunk],
            embeddings=vecs,
            documents=[s.text for s in chunk],
            metadatas=[
                {
                    "video_id": str(video_id),
                    "library_id": str(library_id),
                    "language": language or "",
                    "transcript_id": str(s.transcript_id),
                    "segment_id": int(s.segment_id),
                    "seq": int(s.seq),
                    "start_sec": float(s.start_sec),
                    "end_sec": float(s.end_sec),
                }
                for s in chunk
            ],
        )
        return len(chunk)

    def replace_video(
        self,
        *,
        video_id: UUID | str,
        library_id: UUID | str,
        segments: Sequence[IndexedSegment],
        batch: int | None = None,
        language: str | None = None,
    ) -> int:
        """Drop the video's vectors, then embed and upsert ``segments`` in batches.

        Returns how many vectors were written (blank segments are skipped).
        """
        self.delete_video(video_id)
        size = batch or batch_size()
        return sum(
            self.upsert_segments(
                video_id=video_id,
                library_id=library_id,
                segments=segments[i : i + size],
                language=language,
            )
            for i in range(0, len(segments), size)
        )

    def search(
        self,
        query: str,
        *,
        k: int = 50,
        library_ids: Sequence[str] | None = None,
        languages: Sequence[str] | None = None,
    ) -> list[SegmentHit]:
        """The ``k`` segments nearest to ``query`` (score = cosine similarity), only from
        ``library_ids`` and of ``languages`` (the video's detected language) when given:
        both filter before the top k is taken."""
        if not query.strip() or k <= 0:
            return []
        terms: list[dict[str, Any]] = []
        for key, values in (("library_id", library_ids), ("language", languages)):
            vals = [str(x) for x in (values or []) if str(x)]
            if len(vals) == 1:
                terms.append({key: vals[0]})
            elif vals:
                terms.append({key: {"$in": vals}})
        where: dict[str, Any] | None = None
        if len(terms) == 1:
            where = terms[0]
        elif terms:
            where = {"$and": terms}
        res = self.collection.query(
            query_embeddings=[self.embedder.embed_query(query)],
            n_results=k,
            where=where,
            include=["metadatas", "distances", "documents"],
        )
        return parse_hits(res)


def parse_hits(res: dict[str, Any]) -> list[SegmentHit]:
    """Flatten a one-query Chroma result; rows without the needed metadata are skipped."""
    ids = (res.get("ids") or [[]])[0] or []
    metas = (res.get("metadatas") or [[]])[0] or [{}] * len(ids)
    dists = (res.get("distances") or [[]])[0] or [None] * len(ids)
    docs = (res.get("documents") or [[]])[0] or [None] * len(ids)
    out: list[SegmentHit] = []
    for meta, dist, doc in zip(metas, dists, docs, strict=False):
        md = meta or {}
        try:
            out.append(
                SegmentHit(
                    segment_id=int(md["segment_id"]),
                    video_id=str(md["video_id"]),
                    start_sec=float(md["start_sec"]),
                    end_sec=float(md["end_sec"]),
                    score=1.0 - float(dist) if dist is not None else 0.0,
                    text="" if doc is None else str(doc),
                )
            )
        except (KeyError, TypeError, ValueError):
            continue
    return out
