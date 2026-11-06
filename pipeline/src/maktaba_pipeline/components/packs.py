"""Where an installed Epic 51 asset pack's files are (Epic 52, story 52.16).

The supervisor gives the pipeline ``MAKTABA_COMPONENTS_DIR`` (``<state>/components``).
Each pack lives in ``<root>/<id>/<version>_<sha12>/`` with a ``current`` file naming the
folder in use, the layout ``cmd/maktaba-server/internal/packs`` writes. :func:`pack_dir`
reads it with that code's rules: one line, no path separator, not starting with a dot, an
existing folder, and not being removed (a ``.removing`` mark). Anything else is None: the
pack is not on this machine (yet).
"""

from __future__ import annotations

import os
from pathlib import Path

__all__ = ["ENV", "pack_dir", "root"]

ENV = "MAKTABA_COMPONENTS_DIR"


def root() -> Path | None:
    raw = (os.environ.get(ENV) or "").strip()
    return Path(raw).expanduser() if raw else None


def pack_dir(component_id: str, base: Path | None = None) -> Path | None:
    """The folder of ``component_id``'s installed pack on this machine, or None."""
    base = base if base is not None else root()
    if base is None or not component_id or "/" in component_id or component_id.startswith("."):
        return None
    home = base / component_id
    if (home / ".removing").exists():
        return None
    try:
        lines = (home / "current").read_text(encoding="utf-8").splitlines()
    except (OSError, UnicodeDecodeError):
        return None
    if len(lines) != 1:
        return None
    name = lines[0].strip()
    if not name or name.startswith(".") or "/" in name or "\\" in name or name in ("..",):
        return None
    folder = home / name
    if not folder.is_dir() or (folder / ".removing").exists():
        return None
    return folder
