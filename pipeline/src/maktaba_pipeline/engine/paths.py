"""Per-host path mapping (multi-host workers, plan 06-11 notes N2).

The database stores POSIX paths exactly as the scanner saw them (for example
``Y:/Channel/a.mp4``). A worker on another machine mounts the same NAS
elsewhere (``/Volumes/Y/Channel/a.mp4`` on a Mac), so each worker declares
its own prefix map and converts at the file-system boundary only.
"""

from __future__ import annotations

import json
from dataclasses import dataclass

__all__ = ["PathMap", "active_path_map", "set_active_path_map", "to_local"]


@dataclass(frozen=True, slots=True)
class PathMap:
    """Ordered ``(db_prefix, local_prefix)`` pairs; the longest db prefix wins."""

    entries: tuple[tuple[str, str], ...] = ()

    @classmethod
    def parse(cls, text: str | None) -> PathMap:
        """Parse ``Y:/=/Volumes/Y/;O:/=/Volumes/O/`` or a JSON object ``{"Y:/": "/Volumes/Y/"}``."""
        if not text or not text.strip():
            return cls()
        text = text.strip()
        pairs: list[tuple[str, str]] = []
        if text.startswith("{"):
            pairs = [(str(k), str(v)) for k, v in json.loads(text).items()]
        else:
            for part in text.split(";"):
                if "=" in part:
                    a, _, b = part.partition("=")
                    pairs.append((a.strip(), b.strip()))
        return cls(tuple(sorted((_norm(a), _norm(b)) for a, b in pairs if a and b)))

    def to_local(self, db_path: str) -> str:
        p = db_path.replace("\\", "/")
        best: tuple[str, str] | None = None
        for src, dst in self.entries:
            if p.lower().startswith(src.lower()) and (best is None or len(src) > len(best[0])):
                best = (src, dst)
        if best is None:
            return db_path
        return best[1] + p[len(best[0]) :]

    def to_db(self, local_path: str) -> str:
        p = local_path.replace("\\", "/")
        best: tuple[str, str] | None = None
        for src, dst in self.entries:
            if p.startswith(dst) and (best is None or len(dst) > len(best[1])):
                best = (src, dst)
        if best is None:
            return p
        return best[0] + p[len(best[1]) :]

    def as_json(self) -> dict[str, str]:
        return dict(self.entries)


def _norm(prefix: str) -> str:
    p = prefix.replace("\\", "/")
    return p if p.endswith("/") else p + "/"


_ACTIVE = PathMap()


def set_active_path_map(path_map: PathMap) -> None:
    global _ACTIVE
    _ACTIVE = path_map


def active_path_map() -> PathMap:
    return _ACTIVE


def to_local(db_path: str) -> str:
    """Map a database path to this host's file-system path."""
    return _ACTIVE.to_local(db_path)
