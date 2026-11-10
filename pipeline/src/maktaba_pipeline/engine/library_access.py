"""What the engine may do in a job's library (Epic 59, story 59.03).

The API's media check (``shared/go/mediaaccess``) stores each library's access in
``libraries.access``: ``rw``, ``ro`` (Maktaba may list and read, not write) or ``none``
(it cannot list the library's folders); NULL is a library never checked, which behaves as
before (``rw``). A read-only library is a supported state:

- a stage whose plugin writes the library (``[trusted] files.write`` names ``library`` or
  ``trash``: convert, dedup, repair, YouTube downloads) does not run on it; the job fails
  once, not retried, as ``library_read_only``, and nothing is touched. ``library.subtitles``
  is not refused: the subtitles stage keeps its files in the database there.
- a scan does not run on a library it cannot list (``library_no_access``).
- a write that fails with a permission error anyway (a subfolder the root's check could not
  see, an ACL changed since) is recorded against the library, with its path, and the next
  read of the library's access checks it again (the API's ``GET .../access``).
"""

from __future__ import annotations

import contextlib
from datetime import datetime
from typing import Any

from ..log import get_logger
from .sql import dumps, now

__all__ = [
    "READ_ONLY",
    "access_of",
    "library_of",
    "record_write_failure",
    "refusal",
    "writes_library",
]

_log = get_logger()

#: the access values a writing stage is refused on
READ_ONLY = frozenset({"ro", "none"})

#: how many failed writes a library keeps (newest first)
MAX_WRITE_FAILURES = 5


def writes_library(stage: str) -> bool:
    """The stage's plugin writes the library's own files (``files.write`` names ``library``,
    a part of it other than ``library.subtitles``, or ``trash``)."""
    from ..plugins import firstparty

    fp = firstparty.by_stage(stage)
    if fp is None:
        return False
    files = fp.trusted.get("files") or {}
    for raw in files.get("write") or ():
        area = str(raw)
        if area == "library.subtitles":
            continue
        if area in ("library", "trash") or area.startswith("library."):
            return True
    return False


_LIBRARY_ACCESS = "SELECT access FROM libraries WHERE id = $1"
_VIDEO_ACCESS = (
    "SELECT l.access FROM videos v JOIN libraries l ON l.id = v.library_id WHERE v.id = $1"
)


async def access_of(db: Any, *, subject_kind: str, subject_id: Any) -> str | None:
    """The access of the library a job works on (a library job's, or a video's library);
    None when never checked, unknown, or on a database without the column."""
    if subject_kind == "library":
        sql = _LIBRARY_ACCESS
    elif subject_kind == "video":
        sql = _VIDEO_ACCESS
    else:
        return None
    try:
        row = await db.fetchrow(sql, subject_id)
    except Exception as exc:  # noqa: BLE001 - an older schema: behave as before the check
        _log.debug("library_access_unread", error=str(exc)[:200])
        return None
    if row is None or row["access"] is None:
        return None
    return str(row["access"])


async def refusal(
    db: Any, *, stage: str, subject_kind: str, subject_id: Any
) -> tuple[str, str] | None:
    """Why the job must not run on its library ((kind, message)), or None to run it."""
    writes = writes_library(stage)
    if not writes and stage != "scan":
        return None
    access = await access_of(db, subject_kind=subject_kind, subject_id=subject_id)
    if stage == "scan" and access == "none":
        return "library_no_access", "Maktaba cannot list this library's folders"
    if writes and access in READ_ONLY:
        return "library_read_only", "This library is read-only for Maktaba"
    return None


async def library_of(db: Any, *, subject_kind: str, subject_id: Any) -> Any:
    """The library id a job works on, or None."""
    if subject_kind == "library":
        return subject_id
    if subject_kind != "video":
        return None
    with contextlib.suppress(Exception):
        row = await db.fetchrow("SELECT library_id FROM videos WHERE id = $1", subject_id)
        return row["library_id"] if row is not None else None
    return None


def _merge_failure(detail: Any, path: str, at: datetime) -> dict[str, Any]:
    import json

    if isinstance(detail, str):
        try:
            detail = json.loads(detail)
        except ValueError:
            detail = {}
    doc: dict[str, Any] = dict(detail) if isinstance(detail, dict) else {}
    stamp = at.isoformat().replace("+00:00", "Z")
    failures = [
        f for f in doc.get("write_failures") or [] if isinstance(f, dict) and f.get("path") != path
    ]
    doc["write_failures"] = [{"path": path, "at": stamp}, *failures][:MAX_WRITE_FAILURES]
    doc["recheck_after"] = stamp
    return doc


async def record_write_failure(db: Any, *, library_id: Any, path: str) -> None:
    """Record a write that failed with a permission error against ``library_id`` (newest
    first, at most five) and ask the next read of its access to check again. Best effort:
    the job's own failure is what reports it."""
    if library_id is None or not path:
        return
    try:
        row = await db.fetchrow("SELECT access_detail FROM libraries WHERE id = $1", library_id)
        if row is None:
            return
        doc = _merge_failure(row["access_detail"], path, now())
        await db.execute(
            "UPDATE libraries SET access_detail = $2 WHERE id = $1", library_id, dumps(doc)
        )
        _log.info("library_write_refused", library_id=str(library_id), path=path)
    except Exception as exc:  # noqa: BLE001 - recording is best effort
        _log.warning("library_write_failure_unrecorded", error=str(exc)[:200])
