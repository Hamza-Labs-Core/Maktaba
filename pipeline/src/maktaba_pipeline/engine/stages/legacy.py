"""Output fingerprints for the stages that predate the engine (plan 06-11 section 3.2).

Legacy handlers keep their own state changes and manual chaining; the engine
only records an output hash after they succeed, so their declared non-legacy
edges (``probe -> thumb_hash, link, detect``) can fire. Legacy stages have no
input fingerprint: they always run when queued.
"""

from __future__ import annotations

from typing import Any

from ..graph import Subject
from ..hashing import canonical_hash
from .registry import StageImpl

__all__ = ["IMPLS"]


async def _scan_output(db: Any, subject: Subject) -> str:
    """Count plus hash of ``(id, size_bytes, mtime)`` of the library's videos."""
    rows = await db.fetch(
        "SELECT id, size_bytes, mtime FROM videos WHERE library_id = $1 AND deleted_at IS NULL"
        " ORDER BY id",
        str(subject.id),
    )
    return canonical_hash(
        {
            "n": len(rows),
            "files": [[str(r["id"]), int(r["size_bytes"]), str(r["mtime"])] for r in rows],
        }
    )


async def _probe_output(db: Any, subject: Subject) -> str:
    """Duration and frame size: what later stages depend on."""
    row = await db.fetchrow(
        "SELECT duration_sec, metadata FROM videos WHERE id = $1", str(subject.id)
    )
    if row is None:
        return canonical_hash("missing")
    from ..sql import loads

    meta = loads(row["metadata"]) or {}
    return canonical_hash(
        {
            "duration": None
            if row["duration_sec"] is None
            else round(float(row["duration_sec"]), 3),
            "width": meta.get("width"),
            "height": meta.get("height"),
        }
    )


async def _always_changed(db: Any, subject: Subject) -> str:
    """Legacy stages with no comparable output: every successful run counts as a change."""
    from ..sql import now

    return canonical_hash(now().isoformat())


IMPLS: dict[str, StageImpl] = {
    "scan": StageImpl(output_fingerprint=_scan_output),
    "probe": StageImpl(output_fingerprint=_probe_output),
    **{
        name: StageImpl(output_fingerprint=_always_changed)
        for name in (
            "extract",
            "thumbnail",
            "categorize",
        )
    },
}
