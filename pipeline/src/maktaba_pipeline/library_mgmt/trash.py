"""The file trash (Epic 47, story 47.2).

A file Maktaba removes (a converted original, a merged duplicate) is moved, never
deleted, to::

    <library root>/.maktaba/trash/<kind>/<yyyy-mm-dd>/<path relative to the root>

The trash is on the same volume and share as the file, so the move is one ``rename``:
atomic, no copy, no space doubled. A move that would cross a device is refused; there is
never a copy-and-delete. The scanner, the walker and the watcher skip ``.maktaba`` (and
every dotfile), so a trashed file is never seen as a library file.

``trash_purge`` (engine/stages/trash_purge.py) deletes the entries whose ``purge_after``
(the library's ``settings.trash.days``, 30 by default) has passed, through
:func:`purge_due`; it stops at a file whose size no longer matches the ledger.
"""

from __future__ import annotations

import errno
import os
import posixpath
from collections.abc import Callable, Sequence
from dataclasses import dataclass, field
from datetime import UTC, date, datetime, timedelta
from typing import Any
from uuid import UUID

from ..engine import paths
from ..engine.sql import loads, now, to_ts, uid
from .ledger import FileAction, action_from_row, update_fields

__all__ = [
    "DEFAULT_TRASH_DAYS",
    "TRASH_DIR",
    "CrossDeviceError",
    "PurgeReport",
    "TrashError",
    "TrashSizeMismatch",
    "device_of",
    "due_actions",
    "free_trash_path",
    "library_roots",
    "move_to_trash",
    "prune_empty_dirs",
    "purge_after",
    "purge_due",
    "restore_from_trash",
    "root_for",
    "today",
    "trash_days",
    "trash_path_for",
]

#: the trash folder under a library root (inside ``.maktaba``, which every reader skips)
TRASH_DIR = ".maktaba/trash"
DEFAULT_TRASH_DAYS = 30


class TrashError(Exception):
    """A file could not be moved into or out of the trash; nothing was changed."""


class CrossDeviceError(TrashError):
    """The move would cross a volume: refused (a trash move is a rename, never a copy)."""


class TrashSizeMismatch(TrashError):
    """A trashed file's size is not what the ledger recorded: it is not deleted."""

    def __init__(self, action: FileAction, found: int) -> None:
        super().__init__(
            f"trash file {action.trash_path} is {found} bytes, the ledger says "
            f"{action.old_size}: not deleted"
        )
        self.action = action
        self.found = found


# ---------------------------------------------------------------- paths


def _norm(p: str) -> str:
    return p.replace("\\", "/").rstrip("/") or "/"


def root_for(roots: Sequence[str], path: str) -> str | None:
    """The longest of ``roots`` that ``path`` is inside (component-wise), or None."""
    p = _norm(path)
    best: str | None = None
    for r in roots:
        rn = _norm(r)
        prefix = rn if rn.endswith("/") else rn + "/"
        if p.startswith(prefix) and (best is None or len(rn) > len(_norm(best))):
            best = r
    return best


def trash_path_for(root: str, path: str, kind: str, day: date) -> str:
    """``<root>/.maktaba/trash/<kind>/<day>/<path relative to root>`` (no disk access)."""
    rn = _norm(root)
    p = _norm(path)
    prefix = rn if rn.endswith("/") else rn + "/"
    if not p.startswith(prefix):
        raise TrashError(f"{path} is not under the library root {root}")
    rel = p[len(prefix) :]
    if not rel or rel.startswith(".maktaba/"):
        raise TrashError(f"{path} cannot go to the trash (it is the root or in .maktaba)")
    return posixpath.join(rn, TRASH_DIR, kind, day.isoformat(), rel)


def free_trash_path(local_trash: str) -> str:
    """``local_trash``, or ``name~2.ext``, ``name~3.ext``... when it is taken (the same
    file trashed twice the same day): a trash move never overwrites."""
    if not os.path.lexists(local_trash):
        return local_trash
    stem, ext = os.path.splitext(local_trash)
    n = 2
    while os.path.lexists(f"{stem}~{n}{ext}"):
        n += 1
    return f"{stem}~{n}{ext}"


# ---------------------------------------------------------------- moves


def device_of(path: str) -> int:
    """The device of ``path``, or of its nearest existing parent."""
    p = os.path.abspath(path)
    while True:
        try:
            return os.stat(p).st_dev
        except FileNotFoundError:
            parent = os.path.dirname(p)
            if parent == p:
                raise
            p = parent


def _rename_same_volume(src: str, dest: str) -> None:
    if not os.path.lexists(src):
        raise TrashError(f"{src} does not exist")
    if os.path.lexists(dest):
        raise TrashError(f"{dest} exists already: not overwritten")
    if device_of(src) != device_of(os.path.dirname(dest) or "."):
        raise CrossDeviceError(f"{src} and {dest} are on different volumes: not moved")
    os.makedirs(os.path.dirname(dest), exist_ok=True)
    try:
        os.rename(src, dest)
    except OSError as exc:
        if exc.errno == errno.EXDEV:
            raise CrossDeviceError(f"{src} and {dest} are on different volumes") from exc
        raise TrashError(f"{src} -> {dest}: {exc}") from exc


def move_to_trash(src: str, dest: str) -> None:
    """Move ``src`` to the trash path ``dest`` (local paths): one same-volume rename.

    Refuses (:class:`TrashError`, nothing changed) when ``src`` is gone, ``dest`` exists,
    or the two are on different volumes (:class:`CrossDeviceError`)."""
    _rename_same_volume(src, dest)


def restore_from_trash(trash: str, dest: str) -> None:
    """Move a trashed file back to ``dest``: the same rules as :func:`move_to_trash`."""
    _rename_same_volume(trash, dest)


def prune_empty_dirs(path: str, stop: str) -> None:
    """Remove the empty directories from ``path`` up to (not including) ``stop``."""
    cur = os.path.abspath(path)
    stop_abs = os.path.abspath(stop)
    while cur.startswith(stop_abs + os.sep) and cur != stop_abs:
        try:
            os.rmdir(cur)
        except OSError:
            return
        cur = os.path.dirname(cur)


# ---------------------------------------------------------------- settings


async def library_roots(db: Any, library_id: UUID | str) -> list[str]:
    """``libraries.roots`` (the database's form)."""
    raw = await db.fetchval("SELECT roots FROM libraries WHERE id = $1", uid(library_id))
    if raw is None:
        return []
    if isinstance(raw, str):
        raw = loads(raw)
    return [str(r) for r in raw]


async def trash_days(db: Any, library_id: UUID | str) -> int:
    """The library's ``settings.trash.days`` (a whole number >= 0), else 30."""
    raw = await db.fetchval("SELECT settings FROM libraries WHERE id = $1", uid(library_id))
    settings = loads(raw) if raw is not None else None
    trash = settings.get("trash") if isinstance(settings, dict) else None
    days = trash.get("days") if isinstance(trash, dict) else None
    if isinstance(days, bool) or not isinstance(days, int | float) or days < 0:
        return DEFAULT_TRASH_DAYS
    return int(days)


async def purge_after(db: Any, library_id: UUID | str, *, at: datetime | None = None) -> datetime:
    """When a file trashed now (or ``at``) may be deleted."""
    return (at or now()) + timedelta(days=await trash_days(db, library_id))


# ---------------------------------------------------------------- purge


@dataclass
class PurgeReport:
    purged: list[UUID] = field(default_factory=list)
    #: already gone from the trash (an earlier purge cut short, or removed by hand)
    missing: list[UUID] = field(default_factory=list)
    #: superseded duplicate rows deleted with their file (cascading their derived data)
    rows_deleted: list[UUID] = field(default_factory=list)
    bytes_freed: int = 0


async def due_actions(
    db: Any, library_id: UUID | str, *, at: datetime | None = None
) -> list[FileAction]:
    """The library's trash entries whose grace period has passed, oldest first."""
    rows = await db.fetch(
        "SELECT * FROM file_actions WHERE library_id = $1 AND trash_path IS NOT NULL"
        " AND purged_at IS NULL AND state = 'done' AND purge_after IS NOT NULL"
        " AND purge_after <= $2 ORDER BY purge_after, id",
        uid(library_id),
        to_ts(db, at or now()),
    )
    return [action_from_row(r) for r in rows]


async def purge_due(
    db: Any,
    library_id: UUID | str,
    *,
    at: datetime | None = None,
    to_local: Callable[[str], str] | None = None,
    before_each: Callable[[int, int], Any] | None = None,
) -> PurgeReport:
    """Delete the library's trash entries past ``purge_after``; record ``purged_at``.

    A file whose size is not the ledger's ``old_size`` stops the purge
    (:class:`TrashSizeMismatch`; that file and every later one stay). A file already gone
    is recorded as purged. For a ``dedup`` entry the superseded video row is deleted
    after its file (its derived data cascades). ``before_each(done, total)`` runs before
    each entry (progress, a cancel check; it may raise to stop).
    """
    local = to_local or paths.to_local
    report = PurgeReport()
    due = await due_actions(db, library_id, at=at)
    for i, action in enumerate(due):
        if before_each is not None:
            res = before_each(i, len(due))
            if hasattr(res, "__await__"):
                await res
        assert action.trash_path is not None
        file = local(action.trash_path)
        gone = not os.path.lexists(file)
        if not gone:
            size = os.path.getsize(file)
            if action.old_size is not None and size != action.old_size:
                await update_fields(
                    db,
                    action.id,
                    error=f"purge refused: {size} bytes in the trash, {action.old_size} recorded",
                )
                raise TrashSizeMismatch(action, size)
            os.remove(file)
            report.bytes_freed += size
            root = _trash_root(file)
            if root is not None:
                prune_empty_dirs(os.path.dirname(file), root)
        async with db.transaction():
            await update_fields(
                db,
                action.id,
                purged_at=now(),
                error="already gone from the trash at purge" if gone else None,
            )
            if action.kind == "dedup" and action.video_id is not None:
                hit = await db.fetchval(
                    "DELETE FROM videos WHERE id = $1 AND state = 'superseded' RETURNING id",
                    uid(action.video_id),
                )
                if hit is not None:
                    report.rows_deleted.append(action.video_id)
        (report.missing if gone else report.purged).append(action.id)
    return report


def _trash_root(file: str) -> str | None:
    """``<root>/.maktaba/trash`` of a local trash file, for pruning empty folders."""
    marker = os.sep + os.path.join(".maktaba", "trash") + os.sep
    i = file.find(marker)
    return None if i < 0 else file[: i + len(marker) - 1]


def today() -> date:
    return datetime.now(UTC).date()
