"""Canonical hashing for stage fingerprints (plan 06-11 section 3.1).

Python owns hashing. Go never computes these values; it only compares
them as opaque strings.
"""

from __future__ import annotations

import hashlib
import json
from typing import Any

__all__ = ["canonical_hash", "md5_hex"]


def canonical_hash(obj: Any) -> str:
    """Return ``sha256(canonical json)[:32]``.

    Canonical JSON: sorted keys, no whitespace, non-ASCII kept. Tuples and
    lists hash alike, which is what makes a fingerprint stable across a
    round trip through JSON storage.
    """
    text = json.dumps(
        obj,
        sort_keys=True,
        separators=(",", ":"),
        ensure_ascii=False,
        default=_default,
    )
    return hashlib.sha256(text.encode("utf-8")).hexdigest()[:32]


def md5_hex(parts: list[str]) -> str:
    """MD5 over newline-joined ``parts`` (cheap aggregate over many rows)."""
    return hashlib.md5("\n".join(parts).encode("utf-8"), usedforsecurity=False).hexdigest()


def _default(value: Any) -> Any:
    if isinstance(value, (set, frozenset)):
        return sorted(value)
    isoformat = getattr(value, "isoformat", None)
    if callable(isoformat):
        return isoformat()
    return str(value)
