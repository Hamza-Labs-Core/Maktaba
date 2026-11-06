"""Names a library knows (Epic 52, story 52.6): a dictionary lacks them, so a name the
transcript spells as the library does is likely right (its priority is halved) and the
library's spelling is a candidate.

Built from the library's own metadata: its name, its series, the speakers of its videos,
its YouTube channels, the video's title, and the vocabulary's names (story 52.10). Each
name is split with the pack and kept by its key; a word matches a name exactly by key, or
one edit away for names of :data:`FUZZY_MIN` letters or more.
"""

from __future__ import annotations

from collections.abc import Callable, Iterable
from dataclasses import dataclass, field
from typing import Any

from ..log import get_logger
from .vocabulary import damerau1

__all__ = ["FUZZY_MIN", "Gazetteer", "build", "load_names"]

_log = get_logger()
FUZZY_MIN = 6


@dataclass(slots=True)
class Gazetteer:
    #: pack code -> key -> the library's spelling
    by_key: dict[str, dict[str, str]] = field(default_factory=dict)

    def match(self, code: str, key: str) -> str | None:
        names = self.by_key.get(code)
        if not names or not key:
            return None
        got = names.get(key)
        if got is not None:
            return got
        if len(key) < FUZZY_MIN:
            return None
        for k in sorted(names):
            if abs(len(k) - len(key)) <= 1 and damerau1(k, key):
                return names[k]
        return None


#: a pack's tokens and key, by code: what build() splits names with
Splitter = Callable[[str], Iterable[tuple[str, str, str]]]


def build(names: Iterable[str], split: Splitter) -> Gazetteer:
    """``split(name)`` yields (pack code, token, key) for each word of a name."""
    g = Gazetteer()
    for name in sorted(set(n for n in names if n and n.strip())):
        for code, token, key in split(name):
            if len(key) >= 2:
                g.by_key.setdefault(code, {}).setdefault(key, token)
    return g


async def load_names(db: Any, library_id: Any, video_id: Any) -> list[str]:
    """The library's names, read best effort (a table a test database lacks is skipped)."""
    from ..engine.sql import uid

    queries = (
        ("SELECT name FROM libraries WHERE id = $1", library_id),
        ("SELECT COALESCE(name_override, name) AS name FROM series WHERE library_id = $1",
         library_id),
        ("SELECT DISTINCT s.name FROM speakers s JOIN videos v ON v.id = s.video_id"
         " WHERE v.library_id = $1", library_id),
        ("SELECT title AS name FROM youtube_channels WHERE library_id = $1", library_id),
        ("SELECT title AS name FROM videos WHERE id = $1", video_id),
    )  # fmt: skip
    out: list[str] = []
    for sql, arg in queries:
        try:
            for r in await db.fetch(sql, uid(arg)):
                if r["name"]:
                    out.append(str(r["name"]))
        except Exception as exc:  # noqa: BLE001 - one source missing is no failure
            _log.debug("gazetteer_source_skipped", error=f"{type(exc).__name__}"[:80])
    return out
