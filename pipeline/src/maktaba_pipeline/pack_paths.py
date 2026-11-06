"""Paths inside an installed pack that follow its ``current`` pointer (#637).

Epic 51's packs live in ``$MAKTABA_COMPONENTS_DIR/<id>/<version>/`` with a ``current``
file naming the version in use. The supervisor passes the pipeline paths into the pack
it found when it started the process (``MAKTABA_FFMPEG``, ``CAMELTOOLS_DATA``): a pack
installed or updated after that moves ``current``, and these functions answer the same
place in the version ``current`` names now, so a running pipeline uses the new pack at
its next call, with no restart. A path outside the components folder is answered as it
is.
"""

from __future__ import annotations

import os
from pathlib import Path

__all__ = ["current_dir", "follow"]


def _root() -> Path | None:
    raw = (os.environ.get("MAKTABA_COMPONENTS_DIR") or "").strip()
    return Path(raw) if raw else None


def current_dir(pack: str) -> Path | None:
    """``<components>/<pack>/<current>``, or None (no components folder, no pointer, a
    pointer that is not a plain folder name, or no such folder)."""
    root = _root()
    if root is None:
        return None
    try:
        cur = (root / pack / "current").read_text(encoding="utf-8").strip()
    except OSError:
        return None
    if not cur or "/" in cur or "\\" in cur or cur.startswith("."):
        return None
    d = root / pack / cur
    return d if d.is_dir() else None


def follow(path: str) -> str:
    """``path`` moved to the version its pack's ``current`` names now, when ``path`` is
    inside ``<components>/<pack>/<version>/`` and the same place exists in the current
    version; otherwise ``path`` unchanged."""
    root = _root()
    if root is None or not path:
        return path
    try:
        rel = Path(path).resolve(strict=False).relative_to(root.resolve(strict=False))
    except (ValueError, OSError):
        return path
    parts = rel.parts
    if len(parts) < 2:
        return path
    cur = current_dir(parts[0])
    if cur is None or cur.name == parts[1]:
        return path
    moved = cur.joinpath(*parts[2:])
    return str(moved) if moved.exists() else path
