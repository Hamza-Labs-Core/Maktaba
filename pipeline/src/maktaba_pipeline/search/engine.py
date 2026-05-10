"""Top-level search engine — fans out FTS + vector, fuses via RRF.

The public entry point is :func:`search`. Three modes are
supported:

- ``"fts"`` — Postgres tsvector or SQLite FTS5 only.
- ``"semantic"`` — Chroma vector search only.
- ``"hybrid"`` — both, fused with reciprocal-rank fusion (Story 5.4).

Each path is wrapped in a ``try/except``: a failure in one engine
logs a WARN and degrades to the other. A failure in both surfaces
as an empty response rather than an exception so the API stays
"soft" — search is best-effort, not a system invariant.
"""

from __future__ import annotations

import asyncio
import time
from collections.abc import Awaitable
from dataclasses import dataclass, field
from typing import Any, Literal, Protocol

import structlog

from .chroma_client import ChromaClient
from .embedder import EmbeddingService
from .filters import Filters, to_chroma_where
from .fts.postgres import postgres_fts_search
from .fts.query import build_fts_query
from .fts.snippet import build_snippet
from .fts.sqlite import sqlite_fts_search
from .rrf import rrf_fuse

__all__ = [
    "SearchHit",
    "SearchRequest",
    "SearchResponse",
    "search",
]

_log = structlog.get_logger(__name__)


class _Row(Protocol):
    def __getitem__(self, key: str) -> Any: ...


class _DBConn(Protocol):
    dialect: str

    def fetch(self, sql: str, *args: Any) -> Awaitable[list[_Row]]: ...


SearchMode = Literal["fts", "semantic", "hybrid"]


@dataclass(slots=True)
class SearchRequest:
    """One search call.

    ``library_id`` scopes the query to a single library. The two
    fan-out paths share the same effective filter set so a doc that
    one path returns but the other excludes won't sneak in via
    fusion.
    """

    query: str
    library_id: str
    mode: SearchMode = "hybrid"
    limit: int = 25
    filters: Filters | None = None


@dataclass(slots=True)
class SearchHit:
    """One result row, hydrated with the originating unit metadata."""

    unit_id: int
    segment_id: int
    video_id: str
    start_sec: float
    end_sec: float
    text: str
    speaker: str | None
    score: float
    snippet: str


@dataclass(slots=True)
class SearchResponse:
    """Aggregated response — :attr:`hits` is already truncated to
    :attr:`SearchRequest.limit`."""

    total: int
    took_ms: int
    hits: list[SearchHit]
    metadata: dict[str, Any] = field(default_factory=dict)


_HYDRATE_PG = """
SELECT u.id            AS unit_id,
       u.segment_ids   AS segment_ids,
       u.start_sec     AS start_sec,
       u.end_sec       AS end_sec,
       u.text          AS text,
       t.video_id      AS video_id,
       t.id            AS transcript_id
  FROM transcript_units u
  JOIN transcripts t ON t.id = u.transcript_id
 WHERE u.id = ANY($1::bigint[])
"""

_HYDRATE_SQLITE_TMPL = """
SELECT u.id            AS unit_id,
       u.segment_ids   AS segment_ids,
       u.start_sec     AS start_sec,
       u.end_sec       AS end_sec,
       u.text          AS text,
       t.video_id      AS video_id,
       t.id            AS transcript_id
  FROM transcript_units u
  JOIN transcripts t ON t.id = u.transcript_id
 WHERE u.id IN ({placeholders})
"""


async def search(
    req: SearchRequest,
    *,
    db: _DBConn,
    embedder: EmbeddingService,
    chroma: ChromaClient,
) -> SearchResponse:
    """Run a search and hydrate the top hits.

    Implements the FSM described in plan-05-04: the two engines run
    concurrently via :func:`asyncio.gather` with ``return_exceptions``;
    each error is logged and the path is dropped. The hydration query
    then resolves unit ids back to text + video metadata.
    """
    started = time.perf_counter()
    filters = req.filters or Filters()
    language = filters.language

    fts_task: Awaitable[list[tuple[int, float]]] | None = None
    vec_task: Awaitable[list[tuple[int, float]]] | None = None

    if req.mode in ("fts", "hybrid"):
        fts_task = _run_fts(db, req, language)
    if req.mode in ("semantic", "hybrid"):
        vec_task = _run_vector(req, embedder, chroma, filters)

    fts_hits: list[tuple[int, float]] = []
    vec_hits: list[tuple[int, float]] = []
    fts_error: str | None = None
    vec_error: str | None = None

    if fts_task is not None and vec_task is not None:
        results = await asyncio.gather(fts_task, vec_task, return_exceptions=True)
        fts_result, vec_result = results
        if isinstance(fts_result, BaseException):
            fts_error = repr(fts_result)
            _log.warning("search.fts_failed", error=fts_error)
        else:
            fts_hits = fts_result
        if isinstance(vec_result, BaseException):
            vec_error = repr(vec_result)
            _log.warning("search.vector_failed", error=vec_error)
        else:
            vec_hits = vec_result
    elif fts_task is not None:
        try:
            fts_hits = await fts_task
        except Exception as exc:
            fts_error = repr(exc)
            _log.warning("search.fts_failed", error=fts_error)
    elif vec_task is not None:
        try:
            vec_hits = await vec_task
        except Exception as exc:
            vec_error = repr(exc)
            _log.warning("search.vector_failed", error=vec_error)

    # Fuse. In single-mode cases the empty list contributes nothing.
    fused = rrf_fuse(fts_hits, vec_hits, limit=req.limit)
    unit_ids = [h.doc_id for h in fused]
    hits = await _hydrate(db, req, fused, unit_ids)

    took_ms = int((time.perf_counter() - started) * 1000)
    metadata: dict[str, Any] = {
        "mode": req.mode,
        "fts_count": len(fts_hits),
        "vector_count": len(vec_hits),
    }
    if fts_error is not None:
        metadata["fts_error"] = fts_error
    if vec_error is not None:
        metadata["vector_error"] = vec_error
    return SearchResponse(
        total=len(fused),
        took_ms=took_ms,
        hits=hits,
        metadata=metadata,
    )


async def _run_fts(
    db: _DBConn,
    req: SearchRequest,
    language: str | None,
) -> list[tuple[int, float]]:
    """Dispatch to the dialect-specific FTS function."""
    if db.dialect == "postgres":
        return await postgres_fts_search(
            db,
            req.query,
            library_id=req.library_id,
            language=language,
            limit=100,
        )
    return await sqlite_fts_search(
        db,
        req.query,
        library_id=req.library_id,
        language=language,
        limit=100,
    )


async def _run_vector(
    req: SearchRequest,
    embedder: EmbeddingService,
    chroma: ChromaClient,
    filters: Filters,
) -> list[tuple[int, float]]:
    """Embed the query, then run a top-100 vector search.

    The chroma client is sync; we call it directly. Distances are
    converted to "higher = better" so RRF sees the same orientation
    on both paths.
    """
    embedding = embedder.embed_query(req.query)
    where = to_chroma_where(filters)
    collection = chroma.collection(req.library_id)
    raw = collection.query(embedding=embedding, top_k=100, where=where or None)
    # Chroma returns cosine *distance* (0 = identical, 2 = opposite).
    # Invert so higher is better.
    return [(int(unit_id_str), -float(dist)) for unit_id_str, dist in raw]


async def _hydrate(
    db: _DBConn,
    req: SearchRequest,
    fused: list[Any],
    unit_ids: list[int],
) -> list[SearchHit]:
    """Resolve unit ids → :class:`SearchHit`\\ s, in fused order."""
    if not unit_ids:
        return []

    if db.dialect == "postgres":
        rows = await db.fetch(_HYDRATE_PG, unit_ids)
    else:
        placeholders = ",".join("?" for _ in unit_ids)
        sql = _HYDRATE_SQLITE_TMPL.format(placeholders=placeholders)
        rows = await db.fetch(sql, *unit_ids)

    by_unit: dict[int, _Row] = {int(r["unit_id"]): r for r in rows}

    clause = build_fts_query(req.query)
    out: list[SearchHit] = []
    for hit in fused:
        row = by_unit.get(hit.doc_id)
        if row is None:
            continue
        segment_ids = _decode_segment_ids(row["segment_ids"])
        segment_id = segment_ids[0] if segment_ids else 0
        text = str(row["text"])
        snippet = build_snippet(text, clause.tokens)
        out.append(
            SearchHit(
                unit_id=int(row["unit_id"]),
                segment_id=segment_id,
                video_id=str(row["video_id"]),
                start_sec=float(row["start_sec"]),
                end_sec=float(row["end_sec"]),
                text=text,
                speaker=None,
                score=hit.score,
                snippet=snippet,
            )
        )
    return out


def _decode_segment_ids(raw: Any) -> list[int]:
    """Decode the segment_ids column (JSONB on PG, TEXT JSON on SQLite)."""
    if raw is None:
        return []
    if isinstance(raw, list):
        return [int(v) for v in raw]
    if isinstance(raw, str):
        import json

        try:
            decoded = json.loads(raw)
        except json.JSONDecodeError:
            return []
        if isinstance(decoded, list):
            return [int(v) for v in decoded]
    return []
