"""Embedding service — e5-style passage/query prefixes + stub for tests.

The real :class:`EmbeddingService` lazy-loads
``sentence-transformers`` (an optional extra) and prepends the
canonical ``passage:`` / ``query:`` prefixes that the
``intfloat/multilingual-e5-*`` family expects. The default
``model_name`` is ``e5-small`` to keep test bootstrap cheap;
production wires ``e5-large`` via the orchestrator config.

:class:`StubEmbeddingService` is a deterministic fake that derives
its 64-d vectors from SHA-256 hashes of the input text. It exposes
the exact same interface so callers (the indexer, the engine) work
with either.
"""

from __future__ import annotations

import hashlib
import math
import struct
from typing import Any

__all__ = ["EmbeddingService", "StubEmbeddingService"]


_E5_PASSAGE_PREFIX = "passage: "
_E5_QUERY_PREFIX = "query: "


class EmbeddingService:
    """Encode passages and queries with an e5-style sentence model.

    The underlying ``sentence-transformers`` model is loaded lazily
    on first use so importing this module does not pay the model-
    download tax. Subclasses override :meth:`embed_passages` /
    :meth:`embed_query` to plug in a stub.
    """

    def __init__(
        self,
        *,
        model_name: str = "intfloat/multilingual-e5-small",
        device: str = "cpu",
        embed_passage_prefix: str = _E5_PASSAGE_PREFIX,
        embed_query_prefix: str = _E5_QUERY_PREFIX,
    ) -> None:
        self._model_name = model_name
        self._device = device
        self._passage_prefix = embed_passage_prefix
        self._query_prefix = embed_query_prefix
        self._model: Any | None = None
        self._dim: int | None = None

    def _load(self) -> Any:
        """Resolve the model object, lazy-loading on first call."""
        if self._model is None:
            try:
                # Optional extras: chromadb stack pulls this in. Module
                # may not be installed; we surface a clean RuntimeError.
                from sentence_transformers import SentenceTransformer  # type: ignore[import-not-found,unused-ignore] # noqa: I001
            except ImportError as exc:
                raise RuntimeError(
                    "sentence-transformers is not installed; install pipeline[search]",
                ) from exc
            self._model = SentenceTransformer(self._model_name, device=self._device)
            self._dim = int(self._model.get_sentence_embedding_dimension())
        return self._model

    def embed_passages(self, texts: list[str]) -> list[list[float]]:
        """Embed a batch of passages (each prefixed with ``passage:``)."""
        if not texts:
            return []
        model = self._load()
        prefixed = [f"{self._passage_prefix}{t}" for t in texts]
        out = model.encode(prefixed, normalize_embeddings=True)
        return [list(row) for row in out]

    def embed_query(self, text: str) -> list[float]:
        """Embed one query string (prefixed with ``query:``)."""
        model = self._load()
        out = model.encode([f"{self._query_prefix}{text}"], normalize_embeddings=True)
        return list(out[0])

    def dim(self) -> int:
        """Return the model's embedding dimension."""
        if self._dim is None:
            self._load()
        assert self._dim is not None
        return self._dim


class StubEmbeddingService(EmbeddingService):
    """Deterministic fake — SHA-256-derived 64-d unit vectors.

    No model is loaded. Same text → same vector. Different text →
    different vector, with cosine similarity behaving qualitatively
    like a real embedder (similar strings share a hash prefix and
    therefore have higher dot products than unrelated strings).

    The prefix arguments still apply: ``embed_passages`` adds the
    passage prefix before hashing, ``embed_query`` adds the query
    prefix. That way the stub mirrors the production calling
    contract.
    """

    _DIM = 64

    def __init__(
        self,
        *,
        embed_passage_prefix: str = _E5_PASSAGE_PREFIX,
        embed_query_prefix: str = _E5_QUERY_PREFIX,
    ) -> None:
        super().__init__(
            model_name="stub",
            device="cpu",
            embed_passage_prefix=embed_passage_prefix,
            embed_query_prefix=embed_query_prefix,
        )
        self._dim = self._DIM

    def _load(self) -> Any:  # pragma: no cover - stub never loads
        raise RuntimeError("StubEmbeddingService never loads a real model")

    def embed_passages(self, texts: list[str]) -> list[list[float]]:
        return [self._hash_vec(f"{self._passage_prefix}{t}") for t in texts]

    def embed_query(self, text: str) -> list[float]:
        return self._hash_vec(f"{self._query_prefix}{text}")

    def dim(self) -> int:
        return self._DIM

    @classmethod
    def _hash_vec(cls, text: str) -> list[float]:
        """Stretch SHA-256 to 64 floats and unit-normalize.

        Two SHA-256 blocks give us 64 bytes — one byte per output
        dim. We re-center to [-1, 1] then L2-normalize so cosine
        similarity is well-behaved.
        """
        block_a = hashlib.sha256(text.encode("utf-8")).digest()
        block_b = hashlib.sha256(block_a).digest()
        raw = block_a + block_b  # 64 bytes
        # Each byte → float in [-1, 1].
        vec = [((b / 255.0) * 2.0) - 1.0 for b in struct.unpack("64B", raw)]
        norm = math.sqrt(sum(v * v for v in vec)) or 1.0
        return [v / norm for v in vec]
