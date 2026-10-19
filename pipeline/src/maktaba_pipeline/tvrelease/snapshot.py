"""The live TV as a snapshot, and the digests of a release (story 33.2, design 3).

A snapshot is ``{"defs": {channel id: def}, "art": {channel id: {kind: override}},
"settings": {"tv": {...}, "tv_output": {...}}}``:

* ``def`` is the full ``channels`` row a release wants (:data:`DEF_COLUMNS`), the same
  shape the API's channel editor writes into a draft (``api/internal/tvchange``);
* ``art`` is the art set by hand (``channel_art_overrides``, slot 0114) by kind, each
  ``{path, settings, base}`` plus the file's ``sha256`` when the file can be read (the
  art files are content-addressed already: ``<kind>-<sha12>.<ext>``, so an old release
  keeps its picture);
* ``settings`` are the stored overrides of ``rules.tv`` and ``rules.tv_output`` (only
  what differs from the defaults, as ``app_settings`` holds them).

Digests are :func:`..engine.hashing.canonical_hash` of canonical JSON, so the same
lineup in another order has the same digest (TC1).
"""

from __future__ import annotations

import hashlib
from pathlib import Path
from typing import Any

from ..engine.hashing import canonical_hash
from ..engine.sql import loads

__all__ = [
    "DEF_COLUMNS",
    "SETTINGS_SECTIONS",
    "content_digest",
    "live_snapshot",
    "lineup_digest",
    "release_digest",
    "streaming_version",
]

#: a channel definition: the ``channels`` columns a release carries
DEF_COLUMNS = (
    "id",
    "library_id",
    "number",
    "name",
    "slug",
    "logo_path",
    "category",
    "mode",
    "mode_config",
    "source_filter",
    "transition",
    "enabled",
    "sort_order",
)
#: the rules sections a release snapshots (README R2.1, plan 33.2 D4)
SETTINGS_SECTIONS = ("tv", "tv_output")
_ART_TABLE = "channel_art_overrides"


def _def(row: Any) -> dict[str, Any]:
    out: dict[str, Any] = {}
    for col in DEF_COLUMNS:
        v = row[col]
        if col in ("mode_config", "source_filter"):
            v = loads(v) if v not in (None, "") else ({} if col == "mode_config" else None)
        elif col in ("id", "library_id") and v is not None:
            v = str(v)
        elif col in ("number", "sort_order"):
            v = int(v or 0)
        elif col == "enabled":
            v = bool(v)
        out[col] = v
    return out


async def _has_table(db: Any, name: str) -> bool:
    if db.dialect == "postgres":
        return bool(await db.fetchval("SELECT to_regclass($1) IS NOT NULL", name))
    return bool(
        await db.fetchval(
            "SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = $1", name
        )
    )


def _sha256(path: Any) -> str | None:
    try:
        p = Path(str(path))
        if p.is_file():
            return hashlib.sha256(p.read_bytes()).hexdigest()
    except OSError:
        return None
    return None


async def live_snapshot(db: Any) -> dict[str, Any]:
    """The live lineup (``lifecycle = 'live'``), its hand-set art and the stored TV
    settings."""
    cols = ", ".join(DEF_COLUMNS)
    rows = await db.fetch(f"SELECT {cols} FROM channels WHERE lifecycle = 'live'")
    defs = {str(r["id"]): _def(r) for r in rows}
    art: dict[str, dict[str, Any]] = {}
    if await _has_table(db, _ART_TABLE):
        for r in await db.fetch(f"SELECT channel_id, kind, path, settings, base FROM {_ART_TABLE}"):
            cid = str(r["channel_id"])
            if cid not in defs:
                continue
            art.setdefault(cid, {})[str(r["kind"])] = {
                "path": r["path"],
                "settings": loads(r["settings"]) if r["settings"] else None,
                "base": loads(r["base"]) if r["base"] else None,
                "sha256": _sha256(r["path"]) if r["path"] else None,
            }
    settings: dict[str, Any] = {}
    for section in SETTINGS_SECTIONS:
        row = await db.fetchrow("SELECT value FROM app_settings WHERE key = $1", f"rules.{section}")
        val = loads(row["value"]) if row is not None else None
        settings[section] = val if isinstance(val, dict) else {}
    return {"defs": defs, "art": art, "settings": settings}


def lineup_digest(defs: dict[str, Any], art: dict[str, Any]) -> str:
    """The lineup's digest: every definition and its art, in channel id order."""
    return canonical_hash({"defs": defs, "art": _art_identity(art)})


def _art_identity(art: dict[str, Any]) -> dict[str, Any]:
    """The art as it identifies a release: the file content (sha256) when known, else
    its path, and the settings (``base`` is bookkeeping for a revert)."""
    return {
        cid: {
            kind: {"file": o.get("sha256") or o.get("path"), "settings": o.get("settings")}
            for kind, o in sorted(kinds.items())
        }
        for cid, kinds in sorted(art.items())
    }


def streaming_version() -> tuple[str, str]:
    """The streaming release this machine would stage: version and sha256. Until
    side-by-side binaries land (story 33.14) the version is Maktaba's and the checksum
    is empty (the supervisor then checks nothing; plan 33.2 step 4)."""
    import os

    from .. import __version__

    return os.environ.get("MAKTABA_STREAMING_VERSION", __version__), os.environ.get(
        "MAKTABA_STREAMING_SHA256", ""
    )


def content_digest(snap: dict[str, Any], streaming: tuple[str, str]) -> str:
    """What a release puts on the air: settings, lineup, art and the streaming binary.
    Two snapshots with the same content digest air the same (a noop change)."""
    return canonical_hash(
        {
            "settings": {k: snap["settings"].get(k) or {} for k in SETTINGS_SECTIONS},
            "lineup": lineup_digest(snap["defs"], snap["art"]),
            "streaming": list(streaming),
        }
    )


def release_digest(content: str, base_release_id: str | None, sched: list[str]) -> str:
    """A release's identity: its content, the release it was built on and the channels
    whose schedule it regenerates. Building the same change on the same base again
    gives the same release (a retried stage job reuses it, story 33.2 AC1)."""
    return "sha256:" + canonical_hash(
        {"content": content, "base": base_release_id, "sched": sorted(sched)}
    )
