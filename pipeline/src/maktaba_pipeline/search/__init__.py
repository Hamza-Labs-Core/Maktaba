"""Epic 5 — search indexing and query.

Module map:

- :mod:`.fts`       PostgreSQL FTS — ``tsquery`` builder + result struct.
- :mod:`.embedder`  transcript segments as vector-search records.
- :mod:`.semantic`  the e5 embedder; :mod:`.pgstore` the vectors in Postgres (pgvector).
- :mod:`.hybrid`    RRF merge of FTS + semantic results.
- :mod:`.suggest`   Autocomplete from ``search_history`` prefix matching.

Heavy dependencies (sentence-transformers) are lazy — importing this package
doesn't pull them in.
"""

from __future__ import annotations

from .embedder import (
    EmbeddingFunction,
    SegmentCollection,
    SegmentDoc,
    SemanticHit,
    index_segments,
    semantic_search,
)
from .fts import FTSHit, build_tsquery, fts_search, normalize_query
from .hybrid import HybridHit, reciprocal_rank_fusion
from .suggest import Suggestion, record_search, suggest

__all__ = [
    "SegmentCollection",
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
