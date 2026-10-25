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
import importlib.util
import os
import re
import threading
from collections.abc import Callable, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Protocol
from uuid import UUID

from ..log import get_logger
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
    "StoredVector",
    "VectorCollection",
    "batch_size",
    "chroma_dir",
    "collection_name",
    "embed_model",
    "get_embedder",
    "close_clients",
    "collection_open",
    "open_collection",
    "sync_threshold",
    "parse_hits",
    "pick_device",
    "search_available",
    "video_vectors",
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


#: how many additions Chroma keeps in its log before it writes its HNSW index to disk
SYNC_THRESHOLD_ENV = "MAKTABA_CHROMA_SYNC_THRESHOLD"
#: Chroma's default is 1000: it then rewrites the whole index (on the home Mac 12 million
#: vectors, a 21 GB data file and a 1.2 GB id map) every 1000 vectors, seconds each: the
#: index worker wrote about 120 vectors a second. Each write also costs more the more
#: writes wait in the log since the last rewrite, and so does the rewrite: measured on a
#: copy of that index, 10 000 writes about 600 a second (8 000: 560, 15 000: 570 at
#: first and less as the log grows, 50 000: 550 falling to 330). The writes not yet in
#: the index are in Chroma's log, applied again when it opens. A kill during the rewrite
#: is what breaks the index, so fewer rewrites are also fewer chances of that.
DEFAULT_SYNC_THRESHOLD = 10_000


def sync_threshold() -> int:
    """``MAKTABA_CHROMA_SYNC_THRESHOLD`` (0: leave the collection's own setting)."""
    raw = os.getenv(SYNC_THRESHOLD_ENV, "").strip()
    try:
        n = int(raw) if raw else DEFAULT_SYNC_THRESHOLD
    except ValueError:
        n = DEFAULT_SYNC_THRESHOLD
    return max(0, n)


def collection_name(model: str, prefix: str | None = None) -> str:
    """The collection of ``model``'s vectors: ``maktaba_segments-intfloat-multilingual-e5-small``.

    Chroma names allow ``[A-Za-z0-9._-]`` and must start and end with a letter or digit.
    """
    base = prefix or os.getenv(COLLECTION_ENV, "").strip() or DEFAULT_COLLECTION_PREFIX
    slug = re.sub(r"[^A-Za-z0-9._-]+", "-", model).strip("-._")
    name = f"{base}-{slug}".strip("-._")
    return name[:512]


def search_available() -> bool:
    """True when chromadb and sentence-transformers are installed.

    Found, not imported: importing them loads torch (seconds, and its memory), and every
    worker asks this at start to decide whether it serves ``index``. A broken install
    still fails, at the first index job (``search_unavailable``).
    """
    return all(
        importlib.util.find_spec(m) is not None for m in ("chromadb", "sentence_transformers")
    )


# --------------------------------------------------------------------------- embedder


class Embedder(Protocol):
    """What the stage and the query side need from an embedding model."""

    model_name: str

    def embed_passages(self, texts: Sequence[str]) -> list[list[float]]: ...

    def embed_query(self, text: str) -> list[float]: ...


def pick_device(torch: Any | None = None) -> str:
    """``MAKTABA_EMBED_DEVICE`` when set, else Apple's GPU (MPS), else the CPU.

    Not an NVIDIA GPU of its own accord (story 38.9): it is Whisper's and the second
    listen's, and their sizes (the supervisor's GPU tiers, the second listen's room check)
    leave nothing for the e5 model and its CUDA context (0.5-1 GB). e5-small embeds a
    transcript's chunks in moments on the CPU; ``MAKTABA_EMBED_DEVICE=cuda`` puts it on
    the GPU all the same."""
    forced = os.getenv(DEVICE_ENV, "").strip()
    if forced:
        return forced
    if torch is None:
        try:
            torch = importlib.import_module("torch")
        except ImportError:
            return "cpu"
    try:
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

    def delete(
        self, ids: Sequence[str] | None = None, where: dict[str, Any] | None = None
    ) -> None: ...

    def get(
        self,
        ids: Sequence[str] | None = None,
        where: dict[str, Any] | None = None,
        include: Sequence[str] = ...,
    ) -> dict[str, Any]: ...

    def query(
        self,
        query_embeddings: Sequence[Sequence[float]],
        n_results: int,
        where: dict[str, Any] | None = None,
        include: Sequence[str] = ...,
    ) -> dict[str, Any]: ...


_log = get_logger()
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
        coll = _tuned(client, coll, name)
        _COLLECTIONS[key] = coll
        return coll


def collection_open(model: str | None = None, persist_dir: str | None = None) -> bool:
    """Whether this process has the collection open already (:func:`open_collection` is
    then a lookup: it needs no lock of the search index's writes)."""
    key = (persist_dir or chroma_dir(), collection_name(model or embed_model()))
    with _STORE_LOCK:
        return key in _COLLECTIONS


def _tuned(client: Any, coll: Any, name: str) -> Any:
    """The collection with this machine's :func:`sync_threshold` (stored in Chroma's own
    configuration of the collection: set once, kept by every later open)."""
    want = sync_threshold()
    if not want:
        return coll
    try:
        hnsw = (getattr(coll, "configuration", None) or {}).get("hnsw") or {}
        have = hnsw.get("sync_threshold")
        if have is None or int(have) == want:
            return coll
        coll.modify(configuration={"hnsw": {"sync_threshold": want}})
        _log.info("chroma_sync_threshold_set", collection=name, before=int(have), after=want)
        return client.get_collection(name=name, embedding_function=None)
    except Exception as exc:  # noqa: BLE001 - an older chromadb: its own setting stays
        _log.warning("chroma_sync_threshold_unchanged", error=f"{type(exc).__name__}: {exc}")
        return coll


def close_clients() -> int:
    """Close this process's Chroma clients (the worker stops): what each holds open is
    released, and a later :func:`open_collection` opens a new one. The number closed.
    Call it with no write running (search/gate.py)."""
    with _STORE_LOCK:
        clients = list(_CLIENTS.values())
        _CLIENTS.clear()
        _COLLECTIONS.clear()
    for client in clients:
        try:
            close = getattr(client, "close", None)
            if callable(close):
                close()
            else:
                system = getattr(client, "_system", None)
                stop = getattr(system, "stop", None)
                if callable(stop):
                    stop()
        except Exception as exc:  # noqa: BLE001 - the process exits anyway
            _log.warning("chroma_close_failed", error=f"{type(exc).__name__}: {exc}")
    if clients:
        try:
            shared = importlib.import_module("chromadb.api.client").SharedSystemClient
            shared.clear_system_cache()
        except Exception:  # noqa: BLE001 - an older or newer chromadb: nothing cached there
            pass
    return len(clients)


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
class StoredVector:
    """One segment's vector as the store holds it (chapters and topics read these)."""

    segment_id: int
    transcript_id: str
    seq: int
    start_sec: float
    end_sec: float
    text: str
    embedding: list[float]


def video_vectors(
    collection: VectorCollection, video_id: UUID | str, ids: Sequence[str] | None = None
) -> list[StoredVector]:
    """Every vector of ``video_id`` in segment order (no embedding model is loaded); with
    ``ids`` (from the database) only those, read by id: a read by video scans the whole
    collection's metadata (0.8 s on 12 million vectors, by id 0.01 s)."""
    include = ["embeddings", "metadatas", "documents"]
    if ids is not None:
        if not ids:
            return []
        res = collection.get(ids=list(ids), include=include)
    else:
        res = collection.get(where={"video_id": str(video_id)}, include=include)
    embeddings = res.get("embeddings")
    if embeddings is None:  # a numpy array has no truth value: test for None, not falsy
        embeddings = []
    metas = res.get("metadatas") or []
    docs = res.get("documents") or [""] * len(metas)
    out = [
        StoredVector(
            segment_id=int(m.get("segment_id", 0)),
            transcript_id=str(m.get("transcript_id", "")),
            seq=int(m.get("seq", 0)),
            start_sec=float(m.get("start_sec", 0.0)),
            end_sec=float(m.get("end_sec", 0.0)),
            text=str(d or ""),
            embedding=[float(x) for x in e],
        )
        for e, m, d in zip(embeddings, metas, docs, strict=True)
    ]
    out.sort(key=lambda v: v.seq)
    return out


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
        """Drop every vector of the video (a scan of the collection's metadata: about a
        second on 12 million vectors; :meth:`video_ids` and :meth:`delete_ids` are the
        index stage's way)."""
        self.collection.delete(where={"video_id": str(video_id)})

    def video_ids(self, video_id: UUID | str) -> set[str]:
        """The ids of the video's vectors now (a read: no lock of the writes)."""
        res = self.collection.get(where={"video_id": str(video_id)}, include=[])
        return {str(i) for i in res.get("ids") or []}

    def delete_ids(self, ids: Sequence[str]) -> None:
        if ids:
            self.collection.delete(ids=list(ids))

    def embed_segments(
        self, segments: Sequence[IndexedSegment]
    ) -> tuple[list[IndexedSegment], list[list[float]]]:
        """One batch's non-blank segments and their vectors (the model only: no Chroma)."""
        chunk = [s for s in segments if s.text.strip()]
        if not chunk:
            return [], []
        return chunk, self.embedder.embed_passages([s.text for s in chunk])

    def write_vectors(
        self,
        *,
        video_id: UUID | str,
        library_id: UUID | str,
        chunk: Sequence[IndexedSegment],
        vectors: Sequence[Sequence[float]],
        language: str | None = None,
    ) -> int:
        """Upsert one embedded batch (:meth:`embed_segments`); the number written."""
        if not chunk:
            return 0
        self.collection.upsert(
            ids=[embed_id_for(s.transcript_id, s.seq) for s in chunk],
            embeddings=[list(v) for v in vectors],
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

    def upsert_segments(
        self,
        *,
        video_id: UUID | str,
        library_id: UUID | str,
        segments: Sequence[IndexedSegment],
        language: str | None = None,
    ) -> int:
        """Embed ``segments`` (one batch) and upsert them; blank ones are skipped."""
        chunk, vectors = self.embed_segments(segments)
        return self.write_vectors(
            video_id=video_id,
            library_id=library_id,
            chunk=chunk,
            vectors=vectors,
            language=language,
        )

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
