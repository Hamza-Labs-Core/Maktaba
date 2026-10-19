"""``maktaba-pipeline doctor``: can this worker run here?

The compose healthcheck and an operator both call it. Each check prints one line
(``ok``/``FAIL``, the check, what it found) and the command exits 1 when any check
fails, so a broken worker is reported unhealthy instead of always green:

- **database**: the configured database answers ``SELECT 1``.
- **schema**: the applied migration (``goose_db_version``) is at least the
  :data:`~maktaba_pipeline.cluster.schema.SCHEMA_REV` this code ships, and the home
  does not demand a newer machine (the same rule that pauses a node). SQLite has no
  goose table, so it is reported and not judged.
- **queue**: ``processing_jobs`` is readable (the claim loop's table); the queued and
  running counts are shown.
- **ffmpeg** / **ffprobe**: the binaries the stages run (``MAKTABA_FFMPEG`` /
  ``MAKTABA_FFPROBE`` or PATH) exist and are executable.
- **storage**: the Chroma directory, and the thumbnail and model roots when set, are
  writable (a directory that does not exist yet must be creatable). The probe file
  is removed again; nothing else is created.
"""

from __future__ import annotations

import asyncio
import os
import shutil
import tempfile
from collections.abc import Awaitable, Callable, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any, TextIO

from .binaries import ffmpeg_binary, ffprobe_binary

__all__ = ["Check", "check_binary", "check_storage", "run_doctor", "storage_dirs"]

#: per database step; the compose healthcheck allows 10 s for the whole command
DB_TIMEOUT_SEC = 4.0


@dataclass(frozen=True, slots=True)
class Check:
    name: str
    ok: bool
    detail: str

    def line(self) -> str:
        return f"{'ok  ' if self.ok else 'FAIL'}  {self.name:<9} {self.detail}"


def check_binary(name: str, binary: str) -> Check:
    """``binary`` is a path to an executable, or a name found on PATH."""
    found = shutil.which(binary)
    if found is None and os.path.isfile(binary) and os.access(binary, os.X_OK):
        found = binary
    if found is None:
        return Check(name, False, f"{binary} not found (set MAKTABA_{name.upper()} or PATH)")
    return Check(name, True, found)


def storage_dirs(env: dict[str, str] | None = None) -> list[tuple[str, str]]:
    """The directories this worker writes: (label, path)."""
    from .search.semantic import chroma_dir

    env = dict(os.environ) if env is None else env
    out = [("chroma", chroma_dir())]
    for label, var in (("thumbnails", "MAKTABA_THUMBNAIL_ROOT"), ("models", "MAKTABA_MODELS_DIR")):
        raw = (env.get(var) or "").strip()
        if raw:
            out.append((label, str(Path(raw).expanduser())))
    return out


def check_storage(label: str, path: str) -> Check:
    """``path`` is a writable directory, or could be created by this user."""
    p = Path(path)
    if p.exists() and not p.is_dir():
        return Check("storage", False, f"{label} {path} is not a directory")
    if not p.exists():
        parent = p.parent
        while not parent.exists() and parent != parent.parent:
            parent = parent.parent
        if parent.is_dir() and os.access(parent, os.W_OK | os.X_OK):
            return Check("storage", True, f"{label} {path} (not created yet; {parent} writable)")
        return Check("storage", False, f"{label} {path} missing and {parent} is not writable")
    try:
        fd, probe = tempfile.mkstemp(prefix=".maktaba-doctor-", dir=path)
        os.close(fd)
        os.remove(probe)
    except OSError as exc:
        return Check("storage", False, f"{label} {path} not writable: {exc.strerror or exc}")
    return Check("storage", True, f"{label} {path} writable")


async def _db_checks(
    database_url: str | None, connect: Callable[[str], Awaitable[Any]]
) -> list[Check]:
    from .cluster.schema import SCHEMA_REV, pause_reason, read_home_schema
    from .engine.sql import is_pg

    if not database_url:
        msg = "no database configured (MAKTABA_DATABASE_URL, or join this machine)"
        return [
            Check("database", False, msg),
            Check("schema", False, "not checked: no database"),
            Check("queue", False, "not checked: no database"),
        ]
    try:
        db = await asyncio.wait_for(connect(database_url), DB_TIMEOUT_SEC)
        await asyncio.wait_for(db.fetchval("SELECT 1"), DB_TIMEOUT_SEC)
    except Exception as exc:  # noqa: BLE001 - any failure to reach it is the finding
        why = "timed out" if isinstance(exc, TimeoutError) else f"{type(exc).__name__}: {exc}"
        return [
            Check("database", False, f"unreachable ({why})"),
            Check("schema", False, "not checked: database unreachable"),
            Check("queue", False, "not checked: database unreachable"),
        ]
    out = [Check("database", True, "postgres reachable" if is_pg(db) else "sqlite open")]
    try:
        if is_pg(db):
            home = await asyncio.wait_for(read_home_schema(db), DB_TIMEOUT_SEC)
            if home.applied is None:
                out.append(Check("schema", False, "goose_db_version unreadable (not migrated?)"))
            elif (paused := pause_reason(home, SCHEMA_REV)) is not None:
                out.append(Check("schema", False, paused))
            else:
                out.append(
                    Check("schema", True, f"database at {home.applied:04d}, needs {SCHEMA_REV:04d}")
                )
        else:
            out.append(Check("schema", True, f"sqlite: not judged (code at {SCHEMA_REV:04d})"))
        try:
            rows = await asyncio.wait_for(
                db.fetch("SELECT state, COUNT(*) AS n FROM processing_jobs GROUP BY state"),
                DB_TIMEOUT_SEC,
            )
        except Exception as exc:  # noqa: BLE001 - an unreadable queue is the finding
            out.append(
                Check("queue", False, f"processing_jobs unreadable ({type(exc).__name__}: {exc})")
            )
        else:
            counts = {str(r[0]): int(r[1]) for r in rows}
            out.append(
                Check(
                    "queue",
                    True,
                    f"processing_jobs readable ({counts.get('queued', 0)} queued, "
                    f"{counts.get('running', 0)} running)",
                )
            )
    finally:
        await db.close()
    return out


async def run_doctor(
    database_url: str | None,
    *,
    connect: Callable[[str], Awaitable[Any]] | None = None,
    dirs: Sequence[tuple[str, str]] | None = None,
    out: TextIO | None = None,
    quiet: bool = False,
) -> int:
    """Run every check, print them (failures only when ``quiet``), 0 when all pass."""
    if connect is None:
        from .runtime import Database

        connect = Database.connect
    checks = await _db_checks(database_url, connect)
    checks.append(check_binary("ffmpeg", ffmpeg_binary()))
    checks.append(check_binary("ffprobe", ffprobe_binary()))
    for label, path in storage_dirs() if dirs is None else dirs:
        checks.append(check_storage(label, path))
    if out is not None:
        for c in checks:
            if not quiet or not c.ok:
                out.write(c.line() + "\n")
        out.flush()
    return 0 if all(c.ok for c in checks) else 1
