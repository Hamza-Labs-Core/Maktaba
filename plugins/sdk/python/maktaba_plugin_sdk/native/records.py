"""A row the plugin API answers, read the way an asyncpg ``Record`` is read.

``row["name"]``, ``row[0]``, ``row.get("name")``, ``dict(row)``, ``row.keys()``,
``row.values()``, ``row.items()``, ``len(row)`` and iteration over the values, so moved
stage code reads rows exactly as it did in-process.
"""

from __future__ import annotations

from collections.abc import Iterator, Sequence
from typing import Any

__all__ = ["Record"]


class Record:
    __slots__ = ("_cols", "_index", "_vals")

    def __init__(self, columns: Sequence[str], values: Sequence[Any], index: dict[str, int]):
        self._cols = columns
        self._vals = values
        self._index = index

    @staticmethod
    def index_of(columns: Sequence[str]) -> dict[str, int]:
        out: dict[str, int] = {}
        for i, c in enumerate(columns):
            out.setdefault(c, i)  # asyncpg answers the first of two equal names
        return out

    def __getitem__(self, k: int | str | slice) -> Any:
        if isinstance(k, str):
            return self._vals[self._index[k]]
        return self._vals[k]

    def get(self, k: str, default: Any = None) -> Any:
        i = self._index.get(k)
        return default if i is None else self._vals[i]

    def keys(self) -> Iterator[str]:
        return iter(self._cols)

    def values(self) -> Iterator[Any]:
        return iter(self._vals)

    def items(self) -> Iterator[tuple[str, Any]]:
        return iter(zip(self._cols, self._vals, strict=True))

    def __contains__(self, k: object) -> bool:
        return k in self._index

    def __iter__(self) -> Iterator[Any]:
        return iter(self._vals)

    def __len__(self) -> int:
        return len(self._vals)

    def __eq__(self, other: object) -> bool:
        if isinstance(other, Record):
            return list(self._vals) == list(other._vals)
        if isinstance(other, tuple | list):
            return list(self._vals) == list(other)
        return NotImplemented

    def __hash__(self) -> int:
        return hash(tuple(self._vals))

    def __repr__(self) -> str:
        inner = " ".join(f"{c}={v!r}" for c, v in zip(self._cols, self._vals, strict=True))
        return f"<Record {inner}>"
