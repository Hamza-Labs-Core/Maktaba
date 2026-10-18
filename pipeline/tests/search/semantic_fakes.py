"""In-memory stand-ins for the embedding model and the Chroma collection.

``FakeEmbedder`` maps a text to a normalised bag-of-words vector (so texts sharing words
are close) and records every call, prefixes included. ``FakeCollection`` keeps vectors in
a dict and answers ``query`` by cosine distance, honouring the ``where`` shapes the
semantic index uses (``{"k": v}``, ``{"k": {"$in": [...]}}``).
"""

from __future__ import annotations

import math
from collections.abc import Sequence
from dataclasses import dataclass, field
from typing import Any

from maktaba_pipeline.search.semantic import PASSAGE_PREFIX, QUERY_PREFIX

DIM = 32


def bag_vector(text: str) -> list[float]:
    v = [0.0] * DIM
    for word in text.split():
        v[sum(word.encode("utf-8")) % DIM] += 1.0
    norm = math.sqrt(sum(x * x for x in v)) or 1.0
    return [x / norm for x in v]


class FakeModel:
    """A sentence-transformers look-alike: ``encode(texts, ...)``; records its calls."""

    def __init__(self) -> None:
        self.calls: list[tuple[list[str], int]] = []

    def encode(self, texts: list[str], **kw: Any) -> list[list[float]]:
        self.calls.append((list(texts), int(kw.get("batch_size", 0))))
        return [
            bag_vector(t.removeprefix(PASSAGE_PREFIX).removeprefix(QUERY_PREFIX)) for t in texts
        ]


@dataclass
class FakeEmbedder:
    model_name: str = "fake/e5"
    passages: list[list[str]] = field(default_factory=list)
    queries: list[str] = field(default_factory=list)

    def embed_passages(self, texts: Sequence[str]) -> list[list[float]]:
        self.passages.append(list(texts))
        return [bag_vector(t) for t in texts]

    def embed_query(self, text: str) -> list[float]:
        self.queries.append(text)
        return bag_vector(text)


def _matches(meta: dict[str, Any], where: dict[str, Any] | None) -> bool:
    for key, cond in (where or {}).items():
        if key == "$and":
            if not all(_matches(meta, w) for w in cond):
                return False
            continue
        if isinstance(cond, dict):
            if "$in" in cond and meta.get(key) not in cond["$in"]:
                return False
        elif meta.get(key) != cond:
            return False
    return True


@dataclass
class FakeCollection:
    rows: dict[str, tuple[list[float], dict[str, Any], str]] = field(default_factory=dict)
    upserts: int = 0

    def upsert(
        self,
        ids: Sequence[str],
        embeddings: Sequence[Sequence[float]],
        metadatas: Sequence[dict[str, Any]],
        documents: Sequence[str],
    ) -> None:
        self.upserts += 1
        for i, e, m, d in zip(ids, embeddings, metadatas, documents, strict=True):
            self.rows[i] = (list(e), dict(m), d)

    def delete(self, where: dict[str, Any]) -> None:
        for key in [k for k, (_, m, _) in self.rows.items() if _matches(m, where)]:
            del self.rows[key]

    def query(
        self,
        query_embeddings: Sequence[Sequence[float]],
        n_results: int,
        where: dict[str, Any] | None = None,
        include: Sequence[str] = (),
    ) -> dict[str, Any]:
        q = query_embeddings[0]
        scored = sorted(
            (1.0 - sum(a * b for a, b in zip(q, e, strict=True)), key, m, d)
            for key, (e, m, d) in self.rows.items()
            if _matches(m, where)
        )[:n_results]
        return {
            "ids": [[s[1] for s in scored]],
            "distances": [[s[0] for s in scored]],
            "metadatas": [[s[2] for s in scored]],
            "documents": [[s[3] for s in scored]],
        }

    def metas(self) -> list[dict[str, Any]]:
        return [m for _, m, _ in self.rows.values()]
