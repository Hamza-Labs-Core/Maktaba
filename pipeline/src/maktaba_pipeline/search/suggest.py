"""Autocomplete + recent-search surfaces backed by ``search_history``.

Two writes / one read:

- :func:`record_search` — upsert one row (``query_norm`` key) with
  ``hits++`` and ``last_used_at = now()``. Called every time the API
  receives a non-empty search.
- :func:`suggest` — prefix match ``query_norm`` against ``LIKE
  prefix || '%'`` and rank by a recency-weighted hit count.

The prefix path uses ``text_pattern_ops`` on Postgres (per migration
0031) so the LIKE pattern can use the btree index. Recency weighting
is a simple ``hits * exp(-Δ_days / half_life)`` — half_life defaults to
30 days, which keeps last-month searches above last-quarter searches
without burying older perennial queries.
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from typing import Any, Protocol
from uuid import UUID

from .fts import normalize_query

__all__ = [
    "Suggestion",
    "rank_by_recency",
    "record_search",
    "suggest",
]

DEFAULT_HALF_LIFE_DAYS: float = 30.0


@dataclass(slots=True, frozen=True)
class Suggestion:
    """One typeahead candidate. ``score`` is monotonic — sort descending."""

    query: str
    query_norm: str
    hits: int
    last_used_at: datetime
    score: float


class _DBConn(Protocol):
    dialect: str

    async def execute(self, sql: str, *args: Any) -> Any: ...

    async def fetch(self, sql: str, *args: Any) -> list[dict[str, Any]]: ...


_PG_UPSERT_HISTORY = """
INSERT INTO search_history (user_id, query, query_norm, result_count)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id, query_norm) DO UPDATE SET
    hits         = search_history.hits + 1,
    result_count = COALESCE(EXCLUDED.result_count, search_history.result_count),
    last_used_at = now()
"""

_SQLITE_UPSERT_HISTORY = """
INSERT INTO search_history (user_id, query, query_norm, result_count)
VALUES (?, ?, ?, ?)
ON CONFLICT (user_id, query_norm) DO UPDATE SET
    hits         = search_history.hits + 1,
    result_count = COALESCE(excluded.result_count, search_history.result_count),
    last_used_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
"""

_PG_PREFIX_SEARCH = """
SELECT  query, query_norm, hits, last_used_at
FROM    search_history
WHERE   ($1::uuid IS NULL OR user_id = $1)
        AND query_norm LIKE $2 || '%'
ORDER BY last_used_at DESC
LIMIT   $3
"""

_SQLITE_PREFIX_SEARCH = """
SELECT  query, query_norm, hits, last_used_at
FROM    search_history
WHERE   (? IS NULL OR user_id = ?)
        AND query_norm LIKE ? || '%'
ORDER BY last_used_at DESC
LIMIT   ?
"""


async def record_search(
    db: _DBConn,
    query: str,
    *,
    user_id: UUID | None = None,
    result_count: int | None = None,
) -> bool:
    """Persist one search execution. Returns False for empty queries."""
    norm = normalize_query(query)
    if not norm:
        return False
    if db.dialect == "postgres":
        await db.execute(_PG_UPSERT_HISTORY, user_id, query, norm, result_count)
    else:
        await db.execute(
            _SQLITE_UPSERT_HISTORY,
            str(user_id) if user_id is not None else None,
            query,
            norm,
            result_count,
        )
    return True


def _parse_timestamp(value: Any) -> datetime:
    """Coerce a DB timestamp (datetime or ISO-8601 string) into UTC."""
    if isinstance(value, datetime):
        return value.astimezone(UTC) if value.tzinfo else value.replace(tzinfo=UTC)
    if isinstance(value, str):
        # SQLite returns ``YYYY-MM-DDTHH:MM:SS.SSSZ`` from our DEFAULT.
        return datetime.fromisoformat(value.replace("Z", "+00:00"))
    return datetime.now(UTC)


def rank_by_recency(
    rows: list[dict[str, Any]],
    *,
    now: datetime | None = None,
    half_life_days: float = DEFAULT_HALF_LIFE_DAYS,
) -> list[Suggestion]:
    """Apply the recency-weighted score to each row and sort descending.

    Pure Python so unit tests can pin ``now`` and exercise the math
    without a DB round-trip.
    """
    if half_life_days <= 0:
        raise ValueError("half_life_days must be positive")
    reference = now if now is not None else datetime.now(UTC)
    # The ln(2) factor turns half_life into the exponential decay
    # constant: a row exactly half_life_days old gets score * 0.5.
    decay = math.log(2) / half_life_days
    out: list[Suggestion] = []
    for row in rows:
        last_used = _parse_timestamp(row["last_used_at"])
        delta_days = max(0.0, (reference - last_used) / timedelta(days=1))
        weight = math.exp(-decay * delta_days)
        hits = int(row["hits"])
        score = hits * weight
        out.append(
            Suggestion(
                query=str(row["query"]),
                query_norm=str(row["query_norm"]),
                hits=hits,
                last_used_at=last_used,
                score=score,
            )
        )
    out.sort(key=lambda s: (-s.score, -s.last_used_at.timestamp()))
    return out


async def suggest(
    db: _DBConn,
    prefix: str,
    *,
    user_id: UUID | None = None,
    limit: int = 10,
    half_life_days: float = DEFAULT_HALF_LIFE_DAYS,
    now: datetime | None = None,
) -> list[Suggestion]:
    """Return ranked typeahead candidates for ``prefix``.

    Empty/whitespace prefixes return an empty list rather than the
    user's full history; the caller can ask for "recent" separately.
    """
    norm = normalize_query(prefix)
    if not norm:
        return []
    # Pull a larger window than `limit` so the in-Python recency
    # re-rank has something to choose from; 4× is enough margin for
    # typical typeahead loads without blowing up the result set.
    fetch_n = max(limit * 4, limit)
    if db.dialect == "postgres":
        rows = await db.fetch(_PG_PREFIX_SEARCH, user_id, norm, fetch_n)
    else:
        uid_str = str(user_id) if user_id is not None else None
        rows = await db.fetch(_SQLITE_PREFIX_SEARCH, uid_str, uid_str, norm, fetch_n)
    ranked = rank_by_recency(
        [dict(r) for r in rows],
        now=now,
        half_life_days=half_life_days,
    )
    return ranked[:limit]
