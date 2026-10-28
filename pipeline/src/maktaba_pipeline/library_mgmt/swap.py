"""Swap a verified file in for a video's file, crash-safely (Epic 47, story 47.2).

The swap order, shared by convert (A), the YouTube download (C) and a merged duplicate's
loser (B, whose "swap" is the move to the trash alone)::

    1. stage   copy the new file beside the target as ``.<name>.maktaba-new.<ext>`` (a
               dotfile: the watcher and the scanner ignore it), check its size  -> staged
               verify it in place, on the share (the caller's check)             -> verified
    2. swap    one DB transaction: the ``videos`` row gets the new path, filename,
               content_hash, size_bytes and mtime, keeping its id               -> swapped
               (before the renames: when the watcher sees the new file, the scanner's
               upsert on (library_id, content_hash) finds this row and makes none)
    3. rename  the original to the trash, then the staged file to its final name -> done

A crash leaves the old file or the new one in place and tracked. :func:`recover` (on
worker start, and before a swap of the same video) finishes a ``swapped`` row when its
staged file is there or its final file is, and otherwise puts the row back to the old
values; a ``planned``/``staged``/``verified`` row loses its staged file and is
``rolled_back`` (the stage starts again).

:func:`restore` undoes a ``done`` row until its trash entry is purged: the trashed file
comes back, and the change to the database is reversed (for a conversion the row gets its
old values and the MP4 goes to the trash).
"""

from __future__ import annotations

import asyncio
import os
import posixpath
import re
import shutil
from collections.abc import Awaitable, Callable, Mapping
from dataclasses import dataclass, field
from datetime import UTC, date, datetime
from typing import TYPE_CHECKING, Any
from uuid import UUID

from ..engine import paths
from ..engine.sql import to_ts, uid
from ..identity.hasher import hash_file
from ..log import get_logger
from .ledger import (
    ActionKind,
    FileAction,
    LedgerError,
    advance,
    create_action,
    get_action,
    open_actions,
)
from .trash import (
    CrossDeviceError,
    TrashError,
    device_of,
    library_roots,
    move_to_trash,
    purge_after,
    restore_from_trash,
    root_for,
    today,
    trash_path_for,
)

if TYPE_CHECKING:
    from .verify import VerifyResult

__all__ = [
    "STAGED_MARK",
    "CrashHook",
    "RecoveryReport",
    "RestoreError",
    "RestoreHook",
    "SwapError",
    "SwapResult",
    "SwapVerifyFailed",
    "VerifyFn",
    "is_staged_name",
    "recover",
    "register_restore_hook",
    "restore",
    "staged_path",
    "swap_in",
]

_log = get_logger()

#: ``.<stem>.maktaba-new<ext>``: the staged file beside its target
STAGED_MARK = ".maktaba-new"
_STAGED_RE = re.compile(r"^\..+\.maktaba-new(\.[^./]+)?$")

#: ``(staged local path) -> result``: the caller's check of the staged file, on the share
VerifyFn = Callable[[str], Awaitable["VerifyResult"]]
#: a test seam: called with the step just finished ("staged", "verified", "swapped",
#: "trashed"); raising there is a crash at that point
CrashHook = Callable[[str], None]
ToLocal = Callable[[str], str]


class SwapError(Exception):
    """The swap did not happen; the original is in place and the row unchanged."""

    def __init__(self, message: str, action: FileAction | None = None) -> None:
        super().__init__(message)
        self.action = action


class SwapVerifyFailed(SwapError):
    """The staged file failed verification (``result``); it was deleted."""

    def __init__(self, message: str, action: FileAction, result: VerifyResult) -> None:
        super().__init__(message, action)
        self.result = result


class RestoreError(Exception):
    """A ledger row cannot be restored (not done, purged, its trash file gone ...)."""


@dataclass(frozen=True, slots=True)
class SwapResult:
    action: FileAction
    #: the final path, the database's form
    final_path: str
    #: where the original went, the database's form (None: there was none on disk)
    trash_path: str | None


# ---------------------------------------------------------------- names


def staged_path(final: str) -> str:
    """``<dir>/.<stem>.maktaba-new<ext>`` beside ``final``."""
    d, name = os.path.split(final)
    stem, ext = os.path.splitext(name)
    return os.path.join(d, f".{stem}{STAGED_MARK}{ext}")


def is_staged_name(name: str) -> bool:
    """A staged file's base name (``.x.maktaba-new.mp4``): never a library file."""
    return bool(_STAGED_RE.match(name))


def _same(a: str, b: str) -> bool:
    return os.path.normcase(os.path.abspath(a)) == os.path.normcase(os.path.abspath(b))


def _hook(crash: CrashHook | None, step: str) -> None:
    if crash is not None:
        crash(step)


def _remove(path: str | None) -> None:
    if path and os.path.lexists(path):
        os.remove(path)


def _db_basename(db_path: str) -> str:
    return db_path.replace("\\", "/").rsplit("/", 1)[-1]


def _mtime(db: Any, local: str) -> Any:
    return to_ts(db, datetime.fromtimestamp(os.stat(local).st_mtime, UTC))


def _free_trash(trash_db: str, local: ToLocal) -> str:
    """``trash_db``, or ``name~2.ext`` ... when that trash entry is taken."""
    if not os.path.lexists(local(trash_db)):
        return trash_db
    stem, ext = posixpath.splitext(trash_db)
    n = 2
    while os.path.lexists(local(f"{stem}~{n}{ext}")):
        n += 1
    return f"{stem}~{n}{ext}"


async def _trash_target(
    db: Any, library_id: UUID | str, db_path: str, kind: str, local: ToLocal, day: date
) -> str:
    """The free trash path (database form) for ``db_path``; refuses a file outside the
    library's roots or on another volume than its root."""
    roots = await library_roots(db, library_id)
    root = root_for(roots, db_path)
    if root is None:
        raise TrashError(f"{db_path} is not under any root of library {library_id}")
    if device_of(local(db_path)) != device_of(local(root)):
        raise CrossDeviceError(f"{db_path} is on another volume than its root {root}")
    return _free_trash(trash_path_for(root, db_path, kind, day), local)


# ---------------------------------------------------------------- swap


async def swap_in(
    db: Any,
    *,
    library_id: UUID | str,
    kind: ActionKind,
    source: str,
    final_path: str,
    verify: VerifyFn | None = None,
    video_id: UUID | str | None = None,
    job_id: int | None = None,
    to_local: ToLocal | None = None,
    day: date | None = None,
    crash: CrashHook | None = None,
) -> SwapResult:
    """Put the local file ``source`` at ``final_path`` (database form) in the swap order.

    With ``video_id`` the video's row takes the new file (its id kept) and its current
    file goes to the trash; without one the file is new (a download): nothing is
    replaced, and the caller's scan makes its row. ``verify`` checks the staged file in
    place; a failure deletes it and raises :class:`SwapVerifyFailed` (the ledger row is
    ``failed`` with the check's measurements). Any other refusal before the swap raises
    :class:`SwapError` with everything as it was.
    """
    local = to_local or paths.to_local
    old: Any = None
    if video_id is not None:
        # the stage's own retry: a row this video's last attempt left open is settled first
        await recover(db, video_id=video_id, include_live=True, to_local=local)
        old = await db.fetchrow(
            "SELECT id, library_id, path, content_hash, size_bytes FROM videos WHERE id = $1",
            uid(video_id),
        )
        if old is None:
            raise SwapError(f"video {video_id} not found")
    local_final = local(final_path)
    local_old = None if old is None else local(str(old["path"]))
    if os.path.lexists(local_final) and (local_old is None or not _same(local_final, local_old)):
        raise SwapError(f"{final_path} exists already: not overwritten")
    if not os.path.isfile(source):
        raise SwapError(f"the new file {source} does not exist")
    staged = staged_path(local_final)
    src_size = os.path.getsize(source)
    action = await create_action(
        db,
        library_id=library_id,
        kind=kind,
        video_id=video_id,
        job_id=job_id,
        old_path=None if old is None else str(old["path"]),
        new_path=final_path,
        old_hash=None if old is None else str(old["content_hash"]),
        old_size=None if old is None else int(old["size_bytes"]),
    )

    # 1. stage beside the target, check the size, verify in place
    try:
        if not _same(source, staged):
            _remove(staged)
            await asyncio.to_thread(shutil.copyfile, source, staged)
        got = os.path.getsize(staged)
        if got != src_size:
            raise SwapError(f"the staged copy is {got} bytes, the new file {src_size}")
        new_hash = (await asyncio.to_thread(hash_file, staged)).content_hash
    except (OSError, SwapError) as exc:
        _remove(staged)
        action = await advance(db, action.id, "failed", error=f"staging failed: {exc}")
        raise SwapError(f"staging {final_path} failed: {exc}", action) from exc
    action = await advance(
        db, action.id, "staged", expect="planned", new_size=src_size, new_hash=new_hash
    )
    _hook(crash, "staged")
    if verify is not None:
        result = await verify(staged)
        if not result.ok:
            _remove(staged)
            action = await advance(
                db,
                action.id,
                "failed",
                verify=result.as_json(),
                error="verification failed: " + "; ".join(result.reasons),
            )
            raise SwapVerifyFailed(
                f"{final_path} failed verification: {'; '.join(result.reasons)}", action, result
            )
        action = await advance(db, action.id, "verified", expect="staged", verify=result.as_json())
    else:
        action = await advance(db, action.id, "verified", expect="staged")
    _hook(crash, "verified")

    # 2. one transaction: the row takes the new file (same id); the ledger says swapped
    trash_db: str | None = None
    try:
        if old is not None and local_old is not None and os.path.lexists(local_old):
            trash_db = await _trash_target(
                db, library_id, str(old["path"]), kind, local, day or today()
            )
        pa = None if trash_db is None else await purge_after(db, library_id)
        async with db.transaction():
            if old is not None:
                await db.execute(
                    "UPDATE videos SET path = $2, filename = $3, content_hash = $4,"
                    " size_bytes = $5, mtime = $6, updated_at = $7 WHERE id = $1",
                    uid(video_id),
                    final_path,
                    _db_basename(final_path),
                    new_hash,
                    src_size,
                    _mtime(db, staged),
                    to_ts(db, datetime.now(UTC)),
                )
            action = await advance(
                db,
                action.id,
                "swapped",
                expect="verified",
                trash_path=trash_db,
                purge_after=pa,
            )
    except Exception as exc:  # noqa: BLE001 - also a DB refusal (a hash already in the library)
        await _fail_before_swap(db, action.id, staged, exc)
        raise SwapError(f"swapping {final_path} failed: {exc}", action) from exc
    _hook(crash, "swapped")

    # 3. the renames
    action = await _finish_swapped(db, action, local, crash)
    return SwapResult(action=action, final_path=final_path, trash_path=trash_db)


async def _fail_before_swap(db: Any, action_id: UUID, staged: str, exc: BaseException) -> None:
    try:
        _remove(staged)
    finally:
        cur = await get_action(db, action_id)
        if cur is not None and cur.state in ("planned", "staged", "verified"):
            await advance(db, action_id, "failed", error=f"swap refused: {exc}")


async def _finish_swapped(
    db: Any, action: FileAction, local: ToLocal, crash: CrashHook | None = None
) -> FileAction:
    """Steps 3-4 for a ``swapped`` row: the renames, or (the new file lost) the way back."""
    l_new = None if action.new_path is None else local(action.new_path)
    l_old = None if action.old_path is None else local(action.old_path)
    l_trash = None if action.trash_path is None else local(action.trash_path)
    same = l_new is not None and l_old is not None and _same(l_new, l_old)
    staged = None if l_new is None else staged_path(l_new)

    if l_new is None:
        # nothing staged (a merged duplicate): the move to the trash is all there is
        if l_trash is not None and l_old is not None and os.path.lexists(l_old):
            move_to_trash(l_old, l_trash)
        _hook(crash, "trashed")
        return await advance(db, action.id, "done", expect="swapped")

    if staged is not None and os.path.lexists(staged):
        if l_trash is not None and l_old is not None and os.path.lexists(l_old):
            move_to_trash(l_old, l_trash)
        _hook(crash, "trashed")
        if os.path.lexists(l_new):
            raise SwapError(f"{action.new_path} appeared before the rename: left staged", action)
        os.rename(staged, l_new)
        return await advance(db, action.id, "done", expect="swapped")

    new_there = os.path.lexists(l_new) and (
        action.new_size is None or os.path.getsize(l_new) == action.new_size
    )
    if same and l_trash is not None and not os.path.lexists(l_trash):
        # same name, the original never left: what is there is the original
        new_there = False
    if new_there:
        # the staged file already has its final name: only the original may be left
        if not same and l_trash is not None and l_old is not None and os.path.lexists(l_old):
            move_to_trash(l_old, l_trash)
        return await advance(db, action.id, "done", expect="swapped")
    return await _roll_back_swapped(db, action, l_old, l_trash)


async def _roll_back_swapped(
    db: Any, action: FileAction, l_old: str | None, l_trash: str | None
) -> FileAction:
    """The new file is gone: the original comes back (from the trash if it went there)
    and the row gets its old values."""
    if (
        l_old is not None
        and l_trash is not None
        and not os.path.lexists(l_old)
        and os.path.lexists(l_trash)
    ):
        restore_from_trash(l_trash, l_old)
    async with db.transaction():
        if action.video_id is not None and action.old_path is not None:
            mtime_sql, args = "", []
            if l_old is not None and os.path.lexists(l_old):
                mtime_sql = ", mtime = $6"
                args = [_mtime(db, l_old)]
            await db.execute(
                "UPDATE videos SET path = $2, filename = $3, content_hash = $4,"
                f" size_bytes = $5{mtime_sql} WHERE id = $1",
                uid(action.video_id),
                action.old_path,
                _db_basename(action.old_path),
                action.old_hash,
                action.old_size,
                *args,
            )
        return await advance(
            db,
            action.id,
            "rolled_back",
            expect="swapped",
            error="the new file was lost before the rename: the original is back",
        )


# ---------------------------------------------------------------- recovery


@dataclass
class RecoveryReport:
    finished: list[UUID] = field(default_factory=list)
    rolled_back: list[UUID] = field(default_factory=list)
    failed: list[tuple[UUID, str]] = field(default_factory=list)

    @property
    def touched(self) -> int:
        return len(self.finished) + len(self.rolled_back) + len(self.failed)


async def recover(
    db: Any,
    *,
    library_id: UUID | str | None = None,
    video_id: UUID | str | None = None,
    include_live: bool = False,
    to_local: ToLocal | None = None,
) -> RecoveryReport:
    """Settle the ledger rows a crash left open (see the module doc).

    On worker start (``include_live=False``) a row whose job is still claimed or running
    is left to that job. A row that cannot be settled stays open and is reported.
    """
    local = to_local or paths.to_local
    report = RecoveryReport()
    for action in await open_actions(
        db, library_id=library_id, video_id=video_id, include_live=include_live
    ):
        try:
            if action.kind == "restore":
                await _finish_restore(db, action, local)
                report.finished.append(action.id)
            elif action.state == "swapped":
                done = await _finish_swapped(db, action, local)
                (report.finished if done.state == "done" else report.rolled_back).append(action.id)
            else:
                if action.new_path is not None:
                    _remove(staged_path(local(action.new_path)))
                await advance(
                    db,
                    action.id,
                    "rolled_back",
                    expect=action.state,
                    error=f"cut short while {action.state}: the staged file was removed",
                )
                report.rolled_back.append(action.id)
        except (OSError, TrashError, LedgerError, SwapError, RestoreError) as exc:
            _log.warning("file_action_recovery_failed", action=str(action.id), error=str(exc))
            report.failed.append((action.id, str(exc)))
    return report


# ---------------------------------------------------------------- restore

#: ``(db, the action restored, the restore row, the restored file's local path)``: puts
#: the database back as it was before the action. One per kind.
RestoreHook = Callable[[Any, FileAction, FileAction, str], Awaitable[None]]


async def _restore_convert(db: Any, orig: FileAction, row: FileAction, l_orig: str) -> None:
    if orig.video_id is None or orig.old_path is None:
        return
    await db.execute(
        "UPDATE videos SET path = $2, filename = $3, content_hash = $4, size_bytes = $5,"
        " mtime = $6, updated_at = $7 WHERE id = $1",
        uid(orig.video_id),
        orig.old_path,
        _db_basename(orig.old_path),
        orig.old_hash,
        orig.old_size,
        _mtime(db, l_orig),
        to_ts(db, datetime.now(UTC)),
    )


async def _restore_dedup(db: Any, orig: FileAction, row: FileAction, l_orig: str) -> None:
    """The loser is a video again. ``dedup_apply`` records the state it had in the
    ledger's ``verify.prior_state``; the user data it moved stays with the keeper (story
    47.7 may register a fuller hook)."""
    if orig.video_id is None:
        return
    prior = (orig.verify or {}).get("prior_state") or "discovered"
    await db.execute(
        "UPDATE videos SET superseded_by = NULL, state = $2 WHERE id = $1 AND state = 'superseded'",
        uid(orig.video_id),
        str(prior),
    )


_RESTORE_HOOKS: dict[str, RestoreHook] = {
    "convert": _restore_convert,
    "dedup": _restore_dedup,
}


def register_restore_hook(kind: ActionKind, hook: RestoreHook) -> None:
    """Replace how a ``kind`` row's database change is undone."""
    _RESTORE_HOOKS[kind] = hook


def restore_hooks() -> Mapping[str, RestoreHook]:
    return dict(_RESTORE_HOOKS)


async def restore(
    db: Any,
    action_id: UUID | str,
    *,
    job_id: int | None = None,
    to_local: ToLocal | None = None,
    day: date | None = None,
) -> FileAction:
    """Undo a ``done`` ledger row whose trash entry is not purged; returns the ``restore``
    row (``done``). The trashed file goes back to its old path; for a conversion the MP4
    goes to the trash first (it may stand where the original goes) and the video's row
    gets its old path, hash, size and mtime; the restored row becomes ``rolled_back``."""
    local = to_local or paths.to_local
    orig = await get_action(db, action_id)
    if orig is None:
        raise RestoreError(f"file action {action_id} not found")
    if orig.state != "done":
        raise RestoreError(f"file action {action_id} is {orig.state}, not done")
    if orig.trash_path is None or orig.old_path is None:
        raise RestoreError(f"file action {action_id} sent nothing to the trash")
    if orig.purged_at is not None:
        raise RestoreError(f"file action {action_id} was purged on {orig.purged_at:%Y-%m-%d}")
    if orig.kind not in _RESTORE_HOOKS:
        raise RestoreError(f"a {orig.kind} action cannot be restored")
    if not os.path.lexists(local(orig.trash_path)):
        raise RestoreError(f"{orig.trash_path} is no longer in the trash")
    mp4_trash: str | None = None
    if (
        orig.kind == "convert"
        and orig.new_path is not None
        and os.path.lexists(local(orig.new_path))
    ):
        mp4_trash = await _trash_target(
            db, orig.library_id, orig.new_path, "restore", local, day or today()
        )
    row = await create_action(
        db,
        library_id=orig.library_id,
        kind="restore",
        video_id=orig.video_id,
        job_id=job_id,
        state="swapped",
        old_path=orig.new_path if mp4_trash is not None else None,
        new_path=orig.old_path,
        trash_path=mp4_trash,
        old_hash=orig.new_hash if mp4_trash is not None else None,
        old_size=orig.new_size if mp4_trash is not None else None,
        new_hash=orig.old_hash,
        new_size=orig.old_size,
        verify={"restores": str(orig.id)},
        purge_after=None if mp4_trash is None else await purge_after(db, orig.library_id),
    )
    return await _finish_restore(db, row, local)


async def _finish_restore(db: Any, row: FileAction, local: ToLocal) -> FileAction:
    """Carry a ``restore`` row forward (idempotent: recovery calls it after a crash)."""
    restores = (row.verify or {}).get("restores")
    orig = None if restores is None else await get_action(db, str(restores))
    if orig is None or orig.trash_path is None or row.new_path is None:
        raise RestoreError(f"restore {row.id}: the action it restores is gone")
    l_back = local(row.new_path)
    l_orig_trash = local(orig.trash_path)
    if os.path.lexists(l_orig_trash):
        # 1. the MP4 out (it may stand where the original goes), 2. the original back
        if row.old_path is not None and row.trash_path is not None:
            l_mp4 = local(row.old_path)
            if os.path.lexists(l_mp4):
                move_to_trash(l_mp4, local(row.trash_path))
        restore_from_trash(l_orig_trash, l_back)
    elif not os.path.lexists(l_back):
        raise RestoreError(f"restore {row.id}: {orig.trash_path} and {row.new_path} are gone")
    # 3. the database, in one transaction
    async with db.transaction():
        await _RESTORE_HOOKS[orig.kind](db, orig, row, l_back)
        if orig.state == "done":
            await advance(db, orig.id, "rolled_back", expect="done", error=f"restored ({row.id})")
        return await advance(db, row.id, "done")
