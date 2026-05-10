"""Thin wrapper around ChromaDB with an in-memory fallback for tests.

ChromaDB is an optional dependency (``pipeline[search]``); when it
is not installed we transparently fall back to a brute-force
in-memory cosine store with the same public surface. That lets
unit tests for the indexer and the search engine run without
pulling tens of MB of native deps.

The wrapper exposes one collection per library
(``library-<library_id>`` with cosine space). All payload types
match what the real client expects so swapping the stub for the
real client is purely a constructor change.
"""

from __future__ import annotations

import math
from collections.abc import Iterable
from pathlib import Path
from typing import Any, Protocol

__all__ = [
    "ChromaClient",
    "ChromaCollection",
    "make_in_memory_client",
]


class ChromaCollection(Protocol):
    """Common surface for both real and in-memory collections."""

    def upsert(
        self,
        unit_ids: list[str],
        embeddings: list[list[float]],
        metadatas: list[dict[str, Any]],
        documents: list[str],
    ) -> None: ...

    def query(
        self,
        *,
        embedding: list[float],
        top_k: int,
        where: dict[str, Any] | None = None,
    ) -> list[tuple[str, float]]: ...

    def delete(self, unit_ids: list[str]) -> None: ...


class ChromaClient:
    """Entry point that hands out per-library :class:`ChromaCollection`\\ s.

    Set ``in_memory=True`` (or omit ``persist_dir``) to use the stub
    backend. The real client is imported lazily inside
    :meth:`collection` so the optional dependency only matters when
    the user actually needs persistent storage.
    """

    def __init__(
        self,
        *,
        persist_dir: Path | None = None,
        in_memory: bool = False,
    ) -> None:
        self._persist_dir = persist_dir
        self._in_memory = in_memory or persist_dir is None
        self._cache: dict[str, ChromaCollection] = {}
        # Held lazily so importing this module does not require
        # chromadb to be installed.
        self._real_client: Any | None = None

    def collection(self, library_id: str) -> ChromaCollection:
        """Return (or lazily create) the per-library collection."""
        name = f"library-{library_id}"
        cached = self._cache.get(name)
        if cached is not None:
            return cached

        if self._in_memory:
            col: ChromaCollection = _InMemoryChromaCollection(name=name)
        else:
            col = _build_real_collection(self, name)
        self._cache[name] = col
        return col


def make_in_memory_client() -> ChromaClient:
    """Convenience helper for tests."""
    return ChromaClient(in_memory=True)


def _build_real_collection(client: ChromaClient, name: str) -> ChromaCollection:
    """Build a real chromadb-backed collection.

    Imports ``chromadb`` lazily; raises a clear error mentioning the
    extras name when it isn't installed.
    """
    try:
        import chromadb  # type: ignore[import-not-found]
    except ImportError as exc:  # pragma: no cover - import-time path
        raise RuntimeError(
            "chromadb is not installed; install pipeline[search] or pass in_memory=True",
        ) from exc

    if client._real_client is None:
        if client._persist_dir is None:
            client._real_client = chromadb.EphemeralClient()
        else:
            client._real_client = chromadb.PersistentClient(path=str(client._persist_dir))
    raw = client._real_client.get_or_create_collection(
        name=name,
        metadata={"hnsw:space": "cosine"},
    )
    return _RealChromaCollection(raw=raw)


class _RealChromaCollection:
    """Adapter from chromadb's API to the local protocol."""

    def __init__(self, *, raw: Any) -> None:
        self._raw = raw

    def upsert(
        self,
        unit_ids: list[str],
        embeddings: list[list[float]],
        metadatas: list[dict[str, Any]],
        documents: list[str],
    ) -> None:
        self._raw.upsert(
            ids=unit_ids,
            embeddings=embeddings,
            metadatas=metadatas,
            documents=documents,
        )

    def query(
        self,
        *,
        embedding: list[float],
        top_k: int,
        where: dict[str, Any] | None = None,
    ) -> list[tuple[str, float]]:
        kwargs: dict[str, Any] = {
            "query_embeddings": [embedding],
            "n_results": top_k,
        }
        if where:
            kwargs["where"] = where
        res = self._raw.query(**kwargs)
        ids = res.get("ids", [[]])[0]
        dists = res.get("distances", [[0.0] * len(ids)])[0]
        return [(str(i), float(d)) for i, d in zip(ids, dists, strict=True)]

    def delete(self, unit_ids: list[str]) -> None:
        if not unit_ids:
            return
        self._raw.delete(ids=unit_ids)


class _InMemoryChromaCollection:
    """Brute-force in-memory cosine store.

    Suitable for tests and small benches — O(N) per query. Stores
    vectors in plain Python lists; no numpy dependency.
    """

    def __init__(self, *, name: str) -> None:
        self._name = name
        self._vectors: dict[str, list[float]] = {}
        self._norms: dict[str, float] = {}
        self._metadata: dict[str, dict[str, Any]] = {}
        self._documents: dict[str, str] = {}

    def upsert(
        self,
        unit_ids: list[str],
        embeddings: list[list[float]],
        metadatas: list[dict[str, Any]],
        documents: list[str],
    ) -> None:
        if not (len(unit_ids) == len(embeddings) == len(metadatas) == len(documents)):
            raise ValueError("upsert: all input lists must have equal length")
        for uid, vec, md, doc in zip(unit_ids, embeddings, metadatas, documents, strict=True):
            self._vectors[uid] = list(vec)
            self._norms[uid] = math.sqrt(sum(v * v for v in vec)) or 1.0
            self._metadata[uid] = dict(md)
            self._documents[uid] = doc

    def query(
        self,
        *,
        embedding: list[float],
        top_k: int,
        where: dict[str, Any] | None = None,
    ) -> list[tuple[str, float]]:
        if not self._vectors or top_k <= 0:
            return []
        q_norm = math.sqrt(sum(v * v for v in embedding)) or 1.0
        scored: list[tuple[str, float]] = []
        for uid, vec in self._vectors.items():
            if where and not _matches_where(self._metadata.get(uid, {}), where):
                continue
            dot = sum(a * b for a, b in zip(embedding, vec, strict=True))
            cos = dot / (q_norm * self._norms[uid])
            # Match real chromadb: cosine *distance*, not similarity.
            distance = 1.0 - cos
            scored.append((uid, distance))
        scored.sort(key=lambda kv: kv[1])
        return scored[:top_k]

    def delete(self, unit_ids: list[str]) -> None:
        for uid in unit_ids:
            self._vectors.pop(uid, None)
            self._norms.pop(uid, None)
            self._metadata.pop(uid, None)
            self._documents.pop(uid, None)


def _matches_where(metadata: dict[str, Any], where: dict[str, Any]) -> bool:
    """Minimal where-filter — supports ``{key: {"$eq": v}}`` + ``$and``.

    The real chromadb client supports a richer set, but this stub is
    only meant for tests. Top-level ``$and`` is unrolled; everything
    else is treated as a plain key with optional ``$eq`` wrapping.
    """
    if "$and" in where:
        clauses: Iterable[dict[str, Any]] = where["$and"]
        return all(_matches_where(metadata, c) for c in clauses)
    for key, expected in where.items():
        if isinstance(expected, dict) and "$eq" in expected:
            if metadata.get(key) != expected["$eq"]:
                return False
        else:
            if metadata.get(key) != expected:
                return False
    return True
