"""The two dialects' column shapes for word review's tables (Epic 52).

``TEXT[]`` columns (``word_flags.reasons``, ``vocabulary.variants``) are arrays on
Postgres and JSON text on SQLite; ``JSONB`` columns are JSON text on both as the engine
passes them (:func:`~maktaba_pipeline.engine.sql.dumps`).
"""

from __future__ import annotations

import json
from typing import Any

from ..engine.sql import is_pg

__all__ = ["array_param", "read_array"]


def array_param(db: Any, values: list[str]) -> Any:
    """A ``TEXT[]`` parameter."""
    return list(values) if is_pg(db) else json.dumps(list(values), ensure_ascii=False)


def read_array(value: Any) -> list[str]:
    """A ``TEXT[]`` column as read on either dialect."""
    if value is None:
        return []
    if isinstance(value, list | tuple):
        return [str(v) for v in value]
    if isinstance(value, str):
        try:
            got = json.loads(value)
        except ValueError:
            # a Postgres array literal read as text: {a,b}
            inner = value.strip("{}")
            return [v.strip('"') for v in inner.split(",") if v]
        return [str(v) for v in got] if isinstance(got, list) else []
    return []
