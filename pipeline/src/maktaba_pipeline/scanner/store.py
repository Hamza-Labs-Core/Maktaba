"""SQL-backed :class:`~maktaba_pipeline.scanner.service.ScanStore`.

The orchestrator in :mod:`maktaba_pipeline.scanner.service` walks a
library's roots and drives a thin :class:`ScanStore` boundary. Until
gap-closure (HLB-257/255) the only implementations were the in-memory
test fakes and the CLI's :class:`~maktaba_pipeline.scanner.DryRunStore`;
there was no production store, so the SCAN stage had no real handler.

:class:`SqlScanStore` is that production store. It backs onto the
runtime ``Database`` facade (a strict superset of the scanner's
``ScanStore`` protocol) and mirrors the SQL conventions the audio
modules already established:

- ``get_library`` projects the ``libraries`` row (roots array +
  ``settings`` JSONB → ``disabled`` / ``follow_symlinks``), exactly the
  fields :class:`LibraryRecord` carries.
- ``find_video_by_path`` is the slot 0001 ``videos_library_path_idx``
  lookup the orchestrator's skip-rehash optimisation needs.
- ``save_candidate`` UPSERTs the ``videos`` row on the slot 0003
  ``UNIQUE (library_id, content_hash)`` index (a byte-identical file at a
  second path, while the first is still there, is recorded in
  ``video_copies`` instead: story 47.5, :mod:`.copies`) and, when the library is
  enabled, enqueues a **per-video** ``PROBE`` job via the *existing*
  :func:`maktaba_pipeline.db.jobs.enqueue` — that part is unchanged by
  gap-closure (PROBE jobs are per-video; only SCAN itself is
  library-scoped). The whole thing runs inside one transaction so the
  slot 0005 ``videos.new`` NOTIFY trigger fires atomically with the
  insert (Postgres); SQLite's manual pubsub fan-out is the
  orchestrator's job, keyed off ``SaveCandidateResult.inserted``.

The SQL is ``$N``-parameterised for asyncpg; the runtime facade
rewrites to ``?`` for SQLite, so this module stays dialect-agnostic at
the call site — same contract ``audio.probe`` / ``audio.extract`` use.
"""

from __future__ import annotations

import json
from typing import Any, Protocol
from uuid import UUID, uuid4

from ..db.jobs import DBConn as JobDBConn
from ..db.jobs import Stage, enqueue
from .copies import Exists, path_exists, record_copy
from .service import (
    ExistingVideo,
    LibraryRecord,
    SaveCandidateParams,
    SaveCandidateResult,
)

__all__ = ["SqlScanStore"]


class _ScanDB(Protocol):
    """The connection shape :class:`SqlScanStore` needs.

    The runtime ``Database`` facade satisfies this; it is a strict
    superset of the scanner's ``ScanStore`` protocol and of the
    job-queue helpers' ``DBConn``. Tests pass a fake with the same
    surface (``tests/audio._fake_audio_db.FakeAudioDB`` extended with a
    ``libraries`` table)."""

    dialect: str

    def transaction(self) -> Any: ...

    async def fetchrow(self, sql: str, *args: Any) -> Any: ...

    async def fetch(self, sql: str, *args: Any) -> list[Any]: ...

    async def execute(self, sql: str, *args: Any) -> Any: ...


# Library projection — roots array + the two settings flags the
# orchestrator branches on. ``settings`` is JSONB on Postgres / TEXT on
# SQLite; we decode it in Python so the SQL stays portable (no
# dialect-specific JSON operators).
_SELECT_LIBRARY = """
SELECT id, name, roots, settings
  FROM libraries
 WHERE id = $1
"""

# Skip-rehash lookup — slot 0001 videos_library_path_idx.
_SELECT_VIDEO_BY_PATH = """
SELECT id, size_bytes, mtime, content_hash
  FROM videos
 WHERE library_id = $1
   AND path       = $2
 LIMIT 1
"""

# Content-addressed UPSERT on the slot 0003
# UNIQUE (library_id, content_hash) index. On conflict we refresh the
# path/filename/signature (the file moved within the library: story 47.5
# records a second identical file whose first is still there as a
# ``video_copies`` row before reaching this) and bump last_seen_at so the
# slot 0007 straggler-sweep sees the row as live. ``xmax = 0`` is the
# Postgres idiom for "this row was INSERTed, not UPDATEd" — it drives the
# per-video PROBE enqueue + the orchestrator's inserted/skipped bookkeeping.
_UPSERT_VIDEO = """
INSERT INTO videos
       (library_id, content_hash, path, filename, size_bytes, mtime,
        last_seen_at, state)
VALUES ($1, $2, $3, $4, $5, $6, $7, 'discovered')
ON CONFLICT (library_id, content_hash) DO UPDATE SET
    path         = EXCLUDED.path,
    filename     = EXCLUDED.filename,
    size_bytes   = EXCLUDED.size_bytes,
    mtime        = EXCLUDED.mtime,
    last_seen_at = EXCLUDED.last_seen_at,
    updated_at   = now()
RETURNING id, (xmax = 0) AS inserted
"""

# SQLite's twin of videos has no id default and is UNIQUE (content_hash) alone (no composite
# index for ON CONFLICT to name): the update is limited to the same library,
# and a file whose bytes another library holds returns no row (refused).
#
# SQLite has no xmax. ``ON CONFLICT DO UPDATE`` still fires, but we
# detect insert-vs-update by comparing the returned row's created_at to
# its updated_at after the upsert is impractical; instead SQLite uses a
# pre-check SELECT inside the same transaction (see save_candidate).
_SELECT_VIDEO_ID_BY_HASH = """
SELECT id, path FROM videos
 WHERE library_id = $1
   AND content_hash = $2
 LIMIT 1
"""

# Story 47.5: a path that is a recorded copy of a video (the skip-rehash lookup's
# fallback, so an unchanged copy is not hashed again on every scan).
_SELECT_COPY_BY_PATH = """
SELECT c.video_id AS id, c.size_bytes AS size_bytes, c.mtime AS mtime,
       v.content_hash AS content_hash
  FROM video_copies c
  JOIN videos v ON v.id = c.video_id
 WHERE v.library_id = $1
   AND c.path       = $2
 LIMIT 1
"""

_TOUCH_VIDEO = "UPDATE videos SET last_seen_at = $2 WHERE id = $1"

# the row moved to a path that was one of its copies: that path is no copy any more
_FORGET_COPY = "DELETE FROM video_copies WHERE video_id = $1 AND path = $2"

_UPSERT_VIDEO_SQLITE = """
INSERT INTO videos
       (library_id, content_hash, path, filename, size_bytes, mtime,
        last_seen_at, state, id)
VALUES ($1, $2, $3, $4, $5, $6, $7, 'discovered', $8)
ON CONFLICT (content_hash) DO UPDATE SET
    path         = excluded.path,
    filename     = excluded.filename,
    size_bytes   = excluded.size_bytes,
    mtime        = excluded.mtime,
    last_seen_at = excluded.last_seen_at,
    updated_at   = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
 WHERE videos.library_id = excluded.library_id
RETURNING id
"""


class SqlScanStore:
    """Production :class:`ScanStore` over the runtime ``Database``.

    One instance is reused across scans (it owns no per-scan state);
    the orchestrator already enforces one in-flight scan per library
    via the slot 0058 ``processing_jobs_one_live_scan_per_library``
    index and the ``library_scan_state`` cancel columns.
    """

    def __init__(self, db: _ScanDB, *, exists: Exists = path_exists) -> None:
        self._db = db
        self.dialect = db.dialect
        #: whether a database path is a file here (story 47.5: a second identical file
        #: is a copy only while the row's own file is still there)
        self._exists = exists

    async def get_library(self, library_id: UUID) -> LibraryRecord | None:
        row = await self._db.fetchrow(_SELECT_LIBRARY, library_id)
        if row is None:
            return None
        roots = _decode_roots(row["roots"])
        settings = _decode_settings(row["settings"])
        return LibraryRecord(
            id=_to_uuid(row["id"]),
            name=str(row["name"]),
            roots=roots,
            disabled=bool(settings.get("disabled", False)),
            follow_symlinks=bool(settings.get("follow_symlinks", False)),
            ignore_globs=_ignore_globs(settings.get("ignore_globs")),
        )

    async def find_video_by_path(
        self,
        library_id: UUID,
        path: str,
    ) -> ExistingVideo | None:
        row = await self._db.fetchrow(_SELECT_VIDEO_BY_PATH, library_id, path)
        if row is None:
            row = await self._db.fetchrow(_SELECT_COPY_BY_PATH, library_id, path)
        if row is None:
            return None
        return ExistingVideo(
            id=_to_uuid(row["id"]),
            size_bytes=int(row["size_bytes"]),
            mtime=row["mtime"],
            content_hash=str(row["content_hash"]),
        )

    async def save_candidate(
        self,
        params: SaveCandidateParams,
    ) -> SaveCandidateResult:
        """UPSERT the video row and (if enabled) enqueue a per-video PROBE.

        One transaction so the slot 0005 ``videos.new`` trigger fires
        atomically with the insert on Postgres. The PROBE enqueue uses
        the *existing* per-video :func:`enqueue` unchanged — gap-closure
        only made SCAN itself library-scoped; downstream stages stay
        per-video.
        """
        async with self._db.transaction():
            pre = await self._db.fetchrow(
                _SELECT_VIDEO_ID_BY_HASH,
                params.library_id,
                params.content_hash,
            )
            if (
                pre is not None
                and str(pre["path"]) != params.path
                and self._exists(str(pre["path"]))
            ):
                # Story 47.5: the same bytes at a second path while the row's own file
                # is still there: a copy, recorded; the row keeps its path.
                video_id = _to_uuid(pre["id"])
                await record_copy(
                    self._db,
                    video_id,
                    params.path,
                    params.size_bytes,
                    params.mtime,
                    seen_at=params.last_seen_at,
                )
                await self._db.execute(_TOUCH_VIDEO, str(video_id), params.last_seen_at)
                return SaveCandidateResult(
                    video_id=video_id, inserted=False, job_id=None, copy=True
                )
            # new content, or the row's file moved here (its old path is gone)
            if self._db.dialect == "postgres":
                row = await self._db.fetchrow(
                    _UPSERT_VIDEO,
                    params.library_id,
                    params.content_hash,
                    params.path,
                    params.filename,
                    params.size_bytes,
                    params.mtime,
                    params.last_seen_at,
                )
                video_id = _to_uuid(row["id"])
                inserted = bool(row["inserted"])
            else:
                inserted = pre is None
                row = await self._db.fetchrow(
                    _UPSERT_VIDEO_SQLITE,
                    params.library_id,
                    params.content_hash,
                    params.path,
                    params.filename,
                    params.size_bytes,
                    params.mtime,
                    params.last_seen_at,
                    # SQLite's videos.id has no default: a new row's id is made here
                    str(uuid4()),
                )
                if row is None:
                    raise ValueError(
                        "another library holds a file with the same content hash"
                        " (SQLite keeps one video per content hash)"
                    )
                video_id = _to_uuid(row["id"])
            if pre is not None and str(pre["path"]) != params.path:
                await self._db.execute(_FORGET_COPY, str(video_id), params.path)

            job_id: int | None = None
            if inserted and params.enqueue_probe:
                # EXISTING per-video enqueue — unchanged by gap-closure.
                # Its own unique-live partial index makes a re-scan of an
                # unchanged file idempotent (no duplicate PROBE).
                result = await enqueue(
                    _as_job_db(self._db),
                    video_id=video_id,
                    stage=Stage.PROBE,
                    priority=100,
                )
                job_id = result.id

        return SaveCandidateResult(
            video_id=video_id,
            inserted=inserted,
            job_id=job_id,
        )


def _as_job_db(db: _ScanDB) -> JobDBConn:
    # The job-queue helpers expect their own Protocol; the scan DB shape
    # is a structural superset (same indirection ``audio.probe._as_job_db``
    # uses to satisfy the type checker without a runtime cast).
    return db


def _to_uuid(value: Any) -> UUID:
    if isinstance(value, UUID):
        return value
    return UUID(str(value))


def _decode_roots(value: Any) -> tuple[str, ...]:
    """Decode ``libraries.roots``.

    Postgres ``TEXT[]`` arrives as a Python ``list`` from asyncpg;
    SQLite has no array type so the parity build stores a JSON-encoded
    string. Tolerate both plus an already-tuple fake."""
    if value is None:
        return ()
    if isinstance(value, (list, tuple)):
        return tuple(str(v) for v in value)
    if isinstance(value, str):
        parsed = json.loads(value)
        return tuple(str(v) for v in parsed)
    raise TypeError(f"unexpected roots column type: {type(value).__name__}")


def _ignore_globs(value: Any) -> tuple[str, ...]:
    """``settings.ignore_globs``: the non-empty strings of a list (anything else: none)."""
    if not isinstance(value, list):
        return ()
    return tuple(g for g in value if isinstance(g, str) and g)


def _decode_settings(value: Any) -> dict[str, Any]:
    """Decode ``libraries.settings`` (JSONB on PG, TEXT on SQLite)."""
    if value is None:
        return {}
    if isinstance(value, dict):
        return value
    if isinstance(value, str):
        parsed = json.loads(value)
        if not isinstance(parsed, dict):
            raise TypeError("libraries.settings must decode to an object")
        return parsed
    raise TypeError(f"unexpected settings column type: {type(value).__name__}")
