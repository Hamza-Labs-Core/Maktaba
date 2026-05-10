"""Epic 5 search subsystem — chunking, FTS, vector, hybrid RRF.

This package contains four user-visible flows:

- :func:`chunk_for_transcript` — Story 5.1, segment → unit chunking.
- :func:`engine.search` — Stories 5.2/5.3/5.4, the end-to-end search
  entry point with hybrid mode.
- :class:`IndexerWorker` — incremental Chroma indexing.
- :class:`EmbeddingService` / :class:`StubEmbeddingService` — passage
  and query embeddings (production model + test stub).

The submodules are designed so each stage can be exercised in
isolation: the chunker takes plain :class:`SegmentRow`\\ s, the FTS
search functions only need a :class:`DBConn`, and the engine
accepts a stub embedder + in-memory chroma client for unit tests.
"""

from __future__ import annotations

from .chunker import chunk_for_transcript, chunk_segments_into_units
from .engine import SearchHit, SearchRequest, SearchResponse, search
from .filters import Filters
from .models import SegmentRow, Sentence, UnitDraft
from .normalize import collapse_whitespace, nfc
from .rrf import RrfHit, rrf_fuse

__all__ = [
    "Filters",
    "RrfHit",
    "SearchHit",
    "SearchRequest",
    "SearchResponse",
    "SegmentRow",
    "Sentence",
    "UnitDraft",
    "chunk_for_transcript",
    "chunk_segments_into_units",
    "collapse_whitespace",
    "nfc",
    "rrf_fuse",
    "search",
]
