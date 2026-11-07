"""The library files one job of a file stage may change (Epic 54 wave 5, story 54.32 §2, T02).

In a first-party plugin's runner the sandbox lets a ``files`` group write anywhere in the
library's folders and their trash: Landlock and Seatbelt match folders, not subjects. A
job is narrower. When a runner job of one of :data:`SCOPED` starts, the runner reads the
paths of its subject through these statements, once, before the job runs, and its audit
hook (:func:`maktaba_plugin_runner.sandbox.guard_scope`) refuses, with ``plugin_scope``,
any change to a library file outside them:

* ``convert_mp4`` for a video: the video's file, its MP4 (:func:`convert.final_path_for`)
  and the paths of the video's own ledger rows;
* ``dedup_apply`` for a duplicate group: its members' files (copies included) and the
  members' videos' files;
* ``file_restore`` for a library: the old and new paths of the ledger rows its payload
  names, when they are this library's.

Beside each of those files :class:`JobScope` also allows what the swap and the convert
write next to it: the staged copy (``.<stem>.maktaba-new<ext>``), the subtitle sidecars
(``<stem>.<lang>[.<n>].srt|vtt``) and their staged copies. The trash (``.maktaba/`` under
a library root) is not checked: a file gets there only by a move whose source is checked,
and leaves it only by a move whose destination is.

A job of any other stage, a ``convert_mp4`` for a whole library, and every in-process job
keep no scope, so nothing changes for them.
"""

from __future__ import annotations

import os
import re
from collections.abc import Awaitable, Callable, Iterable
from dataclasses import dataclass, field
from typing import Any

from .convert import final_path_for
from .swap import STAGED_MARK

__all__ = ["SCOPED", "JobScope", "group_paths", "job_paths", "restore_paths", "video_paths"]


def _texts(rows: Iterable[Any], *cols: str) -> list[str]:
    return [str(r[c]) for r in rows for c in cols if r[c]]


async def video_paths(db: Any, video_id: str) -> list[str]:
    """A video's file, its MP4, and the paths of its own ledger rows (database form)."""
    own = _texts(await db.fetch("SELECT path FROM videos WHERE id = $1", video_id), "path")
    journal = _texts(
        await db.fetch("SELECT old_path, new_path FROM file_actions WHERE video_id = $1", video_id),
        "old_path",
        "new_path",
    )
    return [*own, *(final_path_for(p) for p in own), *journal]


async def group_paths(db: Any, group_id: str) -> list[str]:
    """A duplicate group's members' files (copies included) and their videos' files
    (database form). Not the ledger: a retried move's source is a member's file, and a
    ledger row the plugin itself wrote must not widen what the job may move."""
    members = _texts(
        await db.fetch("SELECT path FROM duplicate_members WHERE group_id = $1", group_id), "path"
    )
    videos = _texts(
        await db.fetch(
            "SELECT v.path FROM videos v JOIN duplicate_members m ON m.video_id = v.id"
            " WHERE m.group_id = $1",
            group_id,
        ),
        "path",
    )
    return [*members, *videos]


async def restore_paths(db: Any, library_id: str, action_ids: Iterable[str]) -> list[str]:
    """The old and new paths of the named ledger rows of this library (database form)."""
    out: list[str] = []
    for action_id in action_ids:
        rows = await db.fetch(
            "SELECT old_path, new_path FROM file_actions WHERE id = $1 AND library_id = $2",
            action_id,
            library_id,
        )
        out += _texts(rows, "old_path", "new_path")
    return out


def _action_ids(payload: Any) -> list[str]:
    raw = payload.get("action_ids") if isinstance(payload, dict) else None
    return sorted({str(v) for v in raw}) if isinstance(raw, list) else []


#: stage -> (the subject kind it is scoped for, how its paths are read)
SCOPED: dict[str, tuple[str, Callable[[Any, Any], Awaitable[list[str]]]]] = {
    "convert_mp4": ("video", lambda db, job: video_paths(db, str(job.subject.id))),
    "dedup_apply": ("dup_group", lambda db, job: group_paths(db, str(job.subject.id))),
    "file_restore": (
        "library",
        lambda db, job: restore_paths(db, str(job.subject.id), _action_ids(job.payload)),
    ),
}


async def job_paths(db: Any, stage: str, job: Any) -> list[str] | None:
    """The database paths a job of ``stage`` may change, or None when it keeps no scope."""
    entry = SCOPED.get(stage)
    if entry is None or job.subject.kind != entry[0]:
        return None
    return await entry[1](db, job)


def key(path: str) -> str:
    """A local path as compared: absolute, normalised, its folder's symlinks resolved (the
    file itself may not exist yet)."""
    a = os.path.normpath(os.path.abspath(path))
    return os.path.join(os.path.realpath(os.path.dirname(a)), os.path.basename(a))


@dataclass
class JobScope:
    """The local files one job may change; :meth:`allows` answers for one path."""

    stage: str
    files: frozenset[str]
    #: the library roots as compared (:func:`key`, and as given): a path outside them, or
    #: in their ``.maktaba`` folder (the trash), is not this scope's to refuse
    roots: tuple[str, ...] = ()
    #: folder -> the name patterns allowed there (the files' siblings)
    _siblings: dict[str, list[re.Pattern[str]]] = field(default_factory=dict, repr=False)
    #: what the audit hook refused, so the job is refused even when its code caught it
    refused: list[str] = field(default_factory=list)

    @classmethod
    def of(cls, stage: str, local_paths: Iterable[str], roots: Iterable[str] = ()) -> JobScope:
        rs: list[str] = []
        for r in roots:
            for x in (os.path.normpath(os.path.abspath(r)), os.path.realpath(r)):
                if x not in rs:
                    rs.append(x)
        files = frozenset(key(p) for p in local_paths if p)
        sib: dict[str, list[re.Pattern[str]]] = {}
        for f in files:
            d, name = os.path.split(f)
            stem, ext = os.path.splitext(name)
            s, e = re.escape(stem), re.escape(ext)
            sib.setdefault(d, []).append(
                re.compile(
                    rf"^(\.{s}{re.escape(STAGED_MARK)}{e}"  # the swap's staged copy
                    rf"|\.?{s}\.[-A-Za-z0-9_]{{1,35}}(\.\d+)?\.(srt|vtt)"  # a sidecar
                    rf"(\.maktaba-new|\.maktaba-tmp)?)$"
                )
            )
        return cls(stage=stage, files=files, roots=tuple(rs), _siblings=sib)

    def governs(self, k: str) -> bool:
        """Whether a compared path (:func:`key`) is a library file this scope decides on:
        under a root, outside its ``.maktaba`` folder."""
        for r in self.roots:
            if k.startswith(r + os.sep):
                rel = k[len(r) + 1 :]
                return not (rel == ".maktaba" or rel.startswith(".maktaba" + os.sep))
        return False

    def allows(self, local_path: str) -> bool:
        k = key(local_path)
        if k in self.files:
            return True
        d, name = os.path.split(k)
        return any(p.match(name) for p in self._siblings.get(d, ()))
