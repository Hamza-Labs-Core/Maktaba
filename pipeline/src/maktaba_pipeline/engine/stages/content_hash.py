"""``content_hash``: a video's real content identity, one queue job per video.

A video can exist before its file was read: the mediaintel import creates every
``videos`` row from the cache (path, size, mtime, length) without touching the share,
with a stand-in hash and ``metadata.hash_pending``. This stage reads the file once, the
scanner's own way (:func:`maktaba_pipeline.identity.hasher.hash_file`: BLAKE3 of head,
tail and size), in the ``nas_read`` pool next to every other share read, and stores the
real hash. ``thumb_small`` waits for it (its cache folder is named after the hash).

A file whose content another video of the library already has (the same file twice) is
not given that hash (``UNIQUE (library_id, content_hash)``): it is recorded as
``metadata.duplicate_of`` and keeps its stand-in.
"""

from __future__ import annotations

import asyncio
from typing import Any

from ...db.jobs import Job
from ...identity.hasher import hash_file
from ..context import StageContext, StageFailure
from ..graph import CONTENT_HASH_VERSION, StageResult, Subject
from ..hashing import canonical_hash
from ..sql import dumps, uid
from ._common import load_video, require_video
from .registry import StageImpl

__all__ = ["IMPL", "PENDING", "is_pending"]

#: ``videos.metadata`` key: the row's ``content_hash`` is a stand-in until this stage runs
PENDING = "hash_pending"


def is_pending(metadata: dict[str, Any] | None) -> bool:
    return bool((metadata or {}).get(PENDING))


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    """DB only: the file's identity (a moved or rewritten file is hashed again)."""
    v = await load_video(db, subject.id)
    if v is None:
        return None
    return canonical_hash(
        {"path": v.path, "size": v.size_bytes, "mtime": v.mtime, "v": CONTENT_HASH_VERSION}
    )


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    v = await require_video(db, job.subject.id)
    local = ctx.local_path(v.path)
    try:
        async with ctx.nas.timed(local):
            res = await asyncio.to_thread(hash_file, local)
    except FileNotFoundError as exc:
        raise StageFailure("file_missing", f"{local}: not found", retryable=False) from exc
    except (OSError, ValueError) as exc:
        raise StageFailure("file_unreadable", f"{local}: {exc}") from exc

    meta = dict(v.metadata)
    meta.pop(PENDING, None)
    twin = await db.fetchrow(
        "SELECT id FROM videos WHERE library_id = $1 AND content_hash = $2 AND id <> $3",
        uid(v.library_id),
        res.content_hash,
        uid(v.id),
    )
    if twin is not None:
        meta["duplicate_of"] = str(twin["id"])
        await db.execute(
            "UPDATE videos SET metadata = $2, size_bytes = $3 WHERE id = $1",
            uid(v.id),
            dumps(meta),
            res.size_bytes,
        )
        return StageResult(
            output_hash=canonical_hash({"duplicate_of": str(twin["id"])}),
            summary={"duplicate_of": str(twin["id"])},
        )
    meta.pop("duplicate_of", None)
    await db.execute(
        "UPDATE videos SET content_hash = $2, size_bytes = $3, metadata = $4 WHERE id = $1",
        uid(v.id),
        res.content_hash,
        res.size_bytes,
        dumps(meta),
    )
    return StageResult(
        output_hash=canonical_hash(res.content_hash),
        summary={"content_hash": res.content_hash, "bytes": res.size_bytes},
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
