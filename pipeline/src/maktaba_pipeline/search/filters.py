"""Search filter dataclass and SQL / Chroma serialization helpers.

The same :class:`Filters` value applies to both the FTS path (where
it becomes a WHERE clause) and the vector path (where it becomes
a Chroma ``where`` dict). Keeping both translations in one place
makes it impossible for the two engines to apply different filters
to the same request.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any
from uuid import UUID

__all__ = ["Filters", "to_chroma_where", "to_sql_where"]


@dataclass(frozen=True, slots=True)
class Filters:
    """Optional facets the caller can attach to a search request."""

    language: str | None = None
    speaker: str | None = None
    video_id: UUID | None = None
    min_duration_sec: float | None = None
    max_duration_sec: float | None = None


def to_sql_where(f: Filters) -> tuple[str, list[Any]]:
    """Build a parameterised ``AND ...`` clause and its arg list.

    Returns ``("", [])`` when no filters are set. Caller is
    responsible for renumbering placeholders to ``$N`` / ``?`` based
    on dialect — the helper emits ``%s``-style positional markers
    and the caller substitutes.

    Speaker pivots to ``transcript_segments`` (no speaker column on
    units), so the assembled clause references a sub-query rather
    than a column. The other filters touch ``u.language``,
    ``t.video_id``, and the ``end_sec - start_sec`` duration.
    """
    clauses: list[str] = []
    args: list[Any] = []
    if f.language is not None:
        clauses.append("u.language = ?")
        args.append(f.language)
    if f.video_id is not None:
        clauses.append("t.video_id = ?")
        args.append(f.video_id)
    if f.min_duration_sec is not None:
        clauses.append("(u.end_sec - u.start_sec) >= ?")
        args.append(f.min_duration_sec)
    if f.max_duration_sec is not None:
        clauses.append("(u.end_sec - u.start_sec) <= ?")
        args.append(f.max_duration_sec)
    if f.speaker is not None:
        clauses.append(
            "EXISTS (SELECT 1 FROM transcript_segments s "
            "WHERE s.transcript_id = u.transcript_id "
            "AND s.speaker = ? "
            "AND s.start_sec <= u.end_sec AND s.end_sec >= u.start_sec)"
        )
        args.append(f.speaker)
    if not clauses:
        return "", []
    return " AND " + " AND ".join(clauses), args


def to_chroma_where(f: Filters) -> dict[str, Any]:
    """Build a Chroma ``where`` dict for the same filters.

    Returns ``{}`` when no filters apply. Chroma only sees metadata
    we wrote at upsert time — language, video_id, duration. The
    speaker filter has no metadata equivalent and is silently
    dropped (the caller should layer the speaker filter at the
    hydration step instead).
    """
    clauses: list[dict[str, Any]] = []
    if f.language is not None:
        clauses.append({"language": {"$eq": f.language}})
    if f.video_id is not None:
        clauses.append({"video_id": {"$eq": str(f.video_id)}})
    if f.min_duration_sec is not None:
        clauses.append({"duration_sec": {"$gte": f.min_duration_sec}})
    if f.max_duration_sec is not None:
        clauses.append({"duration_sec": {"$lte": f.max_duration_sec}})
    if not clauses:
        return {}
    if len(clauses) == 1:
        return clauses[0]
    return {"$and": clauses}
