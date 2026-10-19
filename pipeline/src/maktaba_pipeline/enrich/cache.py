"""The shared response cache (``web_metadata_cache``, slot 0077): searches, pages and the
model's answers, by key, for ``rules.enrich.cache_days``."""

from __future__ import annotations

import hashlib
from datetime import timedelta
from typing import Any

from ..engine.sql import dumps, is_pg, loads, now, parse_ts, to_ts

__all__ = ["cache_get", "cache_key", "cache_put"]


def cache_key(kind: str, *parts: str) -> str:
    h = hashlib.sha256("\x1f".join(parts).encode("utf-8")).hexdigest()
    return f"{kind}:{h}"


async def cache_get(db: Any, key: str) -> Any | None:
    row = await db.fetchrow(
        "SELECT response, expires_at FROM web_metadata_cache WHERE cache_key = $1", key
    )
    if row is None:
        return None
    exp = parse_ts(row["expires_at"])
    if exp is not None and exp <= now():
        return None
    return loads(row["response"])


async def cache_put(db: Any, key: str, provider: str, value: Any, *, days: int) -> None:
    if days <= 0:
        return
    at = now()
    cast = "::jsonb" if is_pg(db) else ""
    await db.execute(
        "INSERT INTO web_metadata_cache (cache_key, provider, response, fetched_at, expires_at)"
        f" VALUES ($1,$2,$3{cast},$4,$5) ON CONFLICT (cache_key) DO UPDATE SET"
        " provider = EXCLUDED.provider, response = EXCLUDED.response,"
        " fetched_at = EXCLUDED.fetched_at, expires_at = EXCLUDED.expires_at",
        key,
        provider,
        dumps(value),
        to_ts(db, at),
        to_ts(db, at + timedelta(days=days)),
    )
