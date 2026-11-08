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

The file is found by its normalised name
(:func:`maktaba_pipeline.identity.on_disk.on_disk`) and the video's path becomes the name on
disk, as a scan would have stored it, so every later stage opens it.

Epic 54 story 54.36: a stage of ``tv.maktaba.core-media``; the name lookup and the stand-in
marker are the pipeline's (``identity/on_disk.py``), which other stages share.
"""

from __future__ import annotations

import asyncio
import posixpath
from typing import Any

from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.context import StageContext, StageFailure
from maktaba_pipeline.engine.graph import CONTENT_HASH_VERSION, StageResult, Subject
from maktaba_pipeline.engine.hashing import canonical_hash
from maktaba_pipeline.engine.sql import dumps, uid
from maktaba_pipeline.engine.stages._common import load_video, require_video
from maktaba_pipeline.engine.stages.registry import StageImpl
from maktaba_pipeline.identity.hasher import hash_file
from maktaba_pipeline.identity.on_disk import PENDING, READ_TIMEOUT_S, on_disk

__all__ = ["IMPL"]


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
        found = await asyncio.wait_for(asyncio.to_thread(on_disk, local), READ_TIMEOUT_S)
    except TimeoutError as exc:
        raise StageFailure(
            "nas_timeout", f"{local}: no answer in {READ_TIMEOUT_S:.0f} s", retryable=False
        ) from exc
    except OSError as exc:  # a stalled share, not a missing file: retried
        raise StageFailure("nas_unavailable", f"{local}: {exc}") from exc
    if found is None:
        raise StageFailure("file_missing", f"{local}: not found", retryable=False)
    if found != local:  # the name on disk differs in normalisation only: store that one
        new_path = ctx.path_map.to_db(found)
        await db.execute(
            "UPDATE videos SET path = $2, filename = $3 WHERE id = $1",
            uid(v.id),
            new_path,
            posixpath.basename(new_path),
        )
        local = found
    try:
        async with ctx.nas.timed(local):
            res = await asyncio.wait_for(asyncio.to_thread(hash_file, local), READ_TIMEOUT_S)
    except TimeoutError as exc:
        raise StageFailure(
            "nas_timeout",
            f"{local}: read did not finish in {READ_TIMEOUT_S:.0f} s",
            retryable=False,
        ) from exc
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
