"""Semantic search over transcript segments: the embedder and what the store holds.

Both sides of semantic search use this module so they can never disagree on the model or
the prefixes:

* the engine ``index`` stage (``tv.maktaba.semantic-search``) embeds a video's active
  transcript with :meth:`E5Embedder.embed_passages` (``"passage: "``) and writes the
  vectors to ``segment_embeddings`` (:mod:`maktaba_pipeline.search.pgstore`);
* the gRPC server (:mod:`maktaba_pipeline.grpc_server`) embeds a query with
  :meth:`E5Embedder.embed_query` (``"query: "``) and
  :meth:`~maktaba_pipeline.search.pgstore.PgVectorStore.search` returns the top-k
  segments, filtered by library.

The vectors live in Postgres (pgvector, Epic 43); Chroma was retired in story 43.5.
``sentence_transformers`` and ``torch`` come from the optional ``search`` extra and are
imported lazily (through :func:`importlib.import_module`, so type checking does not depend
on whether the extra is installed).
"""

from __future__ import annotations

import importlib
import importlib.util
import os
import threading
from collections.abc import Callable, Sequence
from dataclasses import dataclass
from typing import Any, Protocol
from uuid import UUID

__all__ = [
    "DEFAULT_BATCH_SIZE",
    "DEFAULT_EMBED_MODEL",
    "INDEX_VERSION",
    "E5Embedder",
    "Embedder",
    "IndexedSegment",
    "SegmentHit",
    "SemanticUnavailable",
    "StoredVector",
    "batch_size",
    "embed_model",
    "embed_segments",
    "get_embedder",
    "get_query_embedder",
    "pick_device",
    "search_available",
]

#: bump when what a vector holds changes (text normalisation, metadata, prefixes): every
#: video is indexed again
#: 2: each vector carries its video's detected language (the search's language filter)
INDEX_VERSION = 2

DEFAULT_EMBED_MODEL = "intfloat/multilingual-e5-small"
DEFAULT_BATCH_SIZE = 64

MODEL_ENV = "MAKTABA_EMBED_MODEL"
DEVICE_ENV = "MAKTABA_EMBED_DEVICE"
BATCH_ENV = "MAKTABA_EMBED_BATCH"

PASSAGE_PREFIX = "passage: "
QUERY_PREFIX = "query: "


class SemanticUnavailable(RuntimeError):
    """The ``search`` extra (sentence-transformers) is not installed."""


def embed_model() -> str:
    """The embedding model: ``MAKTABA_EMBED_MODEL`` or the multilingual e5-small default."""
    return os.getenv(MODEL_ENV, "").strip() or DEFAULT_EMBED_MODEL


def batch_size() -> int:
    raw = os.getenv(BATCH_ENV, "").strip()
    try:
        n = int(raw) if raw else DEFAULT_BATCH_SIZE
    except ValueError:
        n = DEFAULT_BATCH_SIZE
    return max(1, n)


def search_available() -> bool:
    """True when sentence-transformers is installed.

    Found, not imported: importing it loads torch (seconds, and its memory), and every
    worker asks this at start to decide whether it serves ``index``. A broken install
    still fails, at the first index job (``search_unavailable``).
    """
    return importlib.util.find_spec("sentence_transformers") is not None


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


def embed_segments(
    embedder: Embedder, segments: Sequence[IndexedSegment]
) -> tuple[list[IndexedSegment], list[list[float]]]:
    """One batch's non-blank segments and their vectors."""
    chunk = [s for s in segments if s.text.strip()]
    if not chunk:
        return [], []
    return chunk, embedder.embed_passages([s.text for s in chunk])


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
