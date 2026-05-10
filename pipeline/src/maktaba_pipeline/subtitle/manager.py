"""Store / delete subtitle files + maintain the ``subtitle_files`` registry.

The layout under ``~/.maktaba/cache/subtitles/`` is

    {root}/{video_id}/{source}.{language}.{format}

Atomic writes: :func:`write_atomic` writes to a sibling ``.tmp-{pid}-{n}``
file and ``os.replace``s onto the target. Same atomicity guarantee as
the audio cache; concurrent generators of the same subtitle either see
"file present" or "file with full new contents", never a torn write.

Soft-deletion: :func:`soft_delete_subtitle` tombstones the row by
setting ``deleted_at`` and unlinks the file. The DB row stays so a
re-render with the same (video, language, format, source) tuple
doesn't violate the partial unique index — the next register call
upserts onto the tombstone.
"""

from __future__ import annotations

import hashlib
import json
import os
from contextlib import suppress
from dataclasses import dataclass, field
from enum import StrEnum
from pathlib import Path
from typing import Any, Protocol
from uuid import UUID

from .formats import SubtitleFormat

__all__ = [
    "SubtitleRecord",
    "SubtitleSource",
    "cache_path_for",
    "register_subtitle",
    "soft_delete_subtitle",
    "write_atomic",
]


class SubtitleSource(StrEnum):
    """The three places a subtitle file can come from.

    Mirrors the ``subtitle_files.source`` CHECK constraint in
    migration 0015.
    """

    EMBEDDED = "embedded"   # extracted from container (ffmpeg)
    GENERATED = "generated"  # rendered from transcript_segments
    EXTERNAL = "external"    # sidecar .srt/.vtt discovered on disk


@dataclass(slots=True, frozen=True)
class SubtitleRecord:
    """In-memory shape of one ``subtitle_files`` row.

    ``transcript_id`` is set only for ``GENERATED`` rows — the row
    points back at the transcript that produced the file, so a transcript
    deletion cascades the right way (FK is ON DELETE SET NULL on disk
    but the application also soft-deletes the file row).
    """

    video_id: UUID
    language: str
    format: SubtitleFormat
    source: SubtitleSource
    path: Path
    byte_size: int
    sha256: str
    transcript_id: UUID | None = None
    metadata: dict[str, Any] = field(default_factory=dict)


class _DBConn(Protocol):
    dialect: str

    def transaction(self) -> Any: ...

    async def fetchrow(self, sql: str, *args: Any) -> Any: ...

    async def execute(self, sql: str, *args: Any) -> Any: ...


def cache_path_for(
    video_id: UUID,
    language: str,
    fmt: SubtitleFormat,
    source: SubtitleSource,
    *,
    root: str | os.PathLike[str] | None = None,
) -> Path:
    """Compute the on-disk path for a subtitle file.

    The path is deterministic so a re-render lands on the same file and
    :func:`write_atomic` can swap it in place.
    """
    base = Path(root) if root else Path.home() / ".maktaba" / "cache" / "subtitles"
    return base / str(video_id) / f"{source.value}.{language}.{fmt.value}"


def write_atomic(path: str | os.PathLike[str], content: str | bytes) -> tuple[int, str]:
    """Write ``content`` to ``path`` atomically.

    Returns ``(byte_size, sha256_hex)`` so the caller can stamp the row.
    The temp file lives in the same directory so ``os.replace`` is a
    rename within one filesystem.

    Concurrent writers each pick a unique temp name; the loser's
    ``replace`` simply overwrites the winner's. Both observe the
    file-replaced postcondition.
    """
    dest = Path(path)
    dest.parent.mkdir(parents=True, exist_ok=True)
    payload = content.encode("utf-8") if isinstance(content, str) else content
    digest = hashlib.sha256(payload).hexdigest()
    # ``mktemp``-style: pid + counter ensures uniqueness without an
    # extra syscall to ``tempfile.NamedTemporaryFile``.
    tmp = dest.with_name(f".{dest.name}.tmp-{os.getpid()}-{_next_counter()}")
    with open(tmp, "wb") as fh:
        fh.write(payload)
        fh.flush()
        os.fsync(fh.fileno())
    os.replace(tmp, dest)
    return len(payload), digest


_counter = 0


def _next_counter() -> int:
    global _counter
    _counter += 1
    return _counter


_PG_UPSERT_REGISTER = """
INSERT INTO subtitle_files
       (video_id, transcript_id, language, format, source, path, byte_size, sha256,
        is_embedded, is_external, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (video_id, language, format, source) WHERE deleted_at IS NULL DO UPDATE SET
    path        = EXCLUDED.path,
    byte_size   = EXCLUDED.byte_size,
    sha256      = EXCLUDED.sha256,
    metadata    = EXCLUDED.metadata,
    transcript_id = EXCLUDED.transcript_id
RETURNING id
"""

_SQLITE_UPSERT_REGISTER = """
INSERT INTO subtitle_files
       (video_id, transcript_id, language, format, source, path, byte_size, sha256,
        is_embedded, is_external, metadata)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (video_id, language, format, source) WHERE deleted_at IS NULL DO UPDATE SET
    path        = excluded.path,
    byte_size   = excluded.byte_size,
    sha256      = excluded.sha256,
    metadata    = excluded.metadata,
    transcript_id = excluded.transcript_id
RETURNING id
"""

_PG_SOFT_DELETE = """
UPDATE subtitle_files
   SET deleted_at = now()
 WHERE video_id = $1 AND language = $2 AND format = $3 AND source = $4
       AND deleted_at IS NULL
RETURNING path
"""

_SQLITE_SOFT_DELETE = """
UPDATE subtitle_files
   SET deleted_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
 WHERE video_id = ? AND language = ? AND format = ? AND source = ?
       AND deleted_at IS NULL
RETURNING path
"""


async def register_subtitle(db: _DBConn, record: SubtitleRecord) -> int:
    """Upsert a row into ``subtitle_files`` and return the row id.

    Idempotent: a second call with the same (video, language, format,
    source) updates ``path / byte_size / sha256 / metadata`` in place.
    """
    is_embedded = record.source is SubtitleSource.EMBEDDED
    is_external = record.source is SubtitleSource.EXTERNAL
    metadata_json = json.dumps(record.metadata)
    if db.dialect == "postgres":
        row = await db.fetchrow(
            _PG_UPSERT_REGISTER,
            record.video_id,
            record.transcript_id,
            record.language,
            record.format.value,
            record.source.value,
            str(record.path),
            record.byte_size,
            record.sha256,
            is_embedded,
            is_external,
            metadata_json,
        )
    else:
        row = await db.fetchrow(
            _SQLITE_UPSERT_REGISTER,
            str(record.video_id),
            str(record.transcript_id) if record.transcript_id else None,
            record.language,
            record.format.value,
            record.source.value,
            str(record.path),
            record.byte_size,
            record.sha256,
            1 if is_embedded else 0,
            1 if is_external else 0,
            metadata_json,
        )
    return int(row["id"])


async def soft_delete_subtitle(
    db: _DBConn,
    *,
    video_id: UUID,
    language: str,
    fmt: SubtitleFormat,
    source: SubtitleSource,
    delete_file: bool = True,
) -> bool:
    """Tombstone the row and (optionally) unlink the on-disk file.

    Returns ``True`` when a live row was found; ``False`` if the entry
    was already deleted or never existed. Missing files are tolerated
    silently — the row deletion is the source of truth.
    """
    sql = _PG_SOFT_DELETE if db.dialect == "postgres" else _SQLITE_SOFT_DELETE
    if db.dialect == "postgres":
        row = await db.fetchrow(sql, video_id, language, fmt.value, source.value)
    else:
        row = await db.fetchrow(sql, str(video_id), language, fmt.value, source.value)
    if row is None:
        return False
    if delete_file:
        with suppress(FileNotFoundError, OSError):
            Path(row["path"]).unlink()
    return True
