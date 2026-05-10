"""Epic 5 — search indexing and query.

Module map:

- :mod:`.fts`       PostgreSQL FTS — ``tsquery`` builder + result struct.
- :mod:`.embedder`  ChromaDB vector indexing for transcript segments.
- :mod:`.hybrid`    RRF merge of FTS + semantic results.
- :mod:`.suggest`   Autocomplete from ``search_history`` prefix matching.

Heavy dependencies (chromadb, sentence-transformers) are lazy — importing
this package doesn't pull them in. The Chroma backend lives behind a
``Protocol`` so unit tests substitute an in-memory fake.
"""

from __future__ import annotations

from .embedder import (
    ChromaCollection,
    EmbeddingFunction,
    SegmentDoc,
    SemanticHit,
    index_segments,
    semantic_search,
)
from .fts import FTSHit, build_tsquery, fts_search, normalize_query
from .hybrid import HybridHit, reciprocal_rank_fusion
from .suggest import Suggestion, record_search, suggest

__all__ = [
    "ChromaCollection",
    "EmbeddingFunction",
    "FTSHit",
    "HybridHit",
    "SegmentDoc",
    "SemanticHit",
    "Suggestion",
    "build_tsquery",
    "fts_search",
    "index_segments",
    "normalize_query",
    "reciprocal_rank_fusion",
    "record_search",
    "semantic_search",
    "suggest",
]
