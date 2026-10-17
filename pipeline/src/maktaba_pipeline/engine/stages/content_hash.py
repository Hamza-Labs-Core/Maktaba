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

The cache stores names composed (NFC, as Windows wrote them); the share may hold a name
decomposed (NFD: ``أ`` as ``ا`` + U+0654), and SMB matches names byte for byte. The file
is found by its normalised name and the video's path becomes the name on disk, as a scan
would have stored it, so every later stage opens it.
"""

from __future__ import annotations

import asyncio
import os
import posixpath
import unicodedata
from typing import Any

from ...db.jobs import Job
from ...identity.hasher import hash_file
from ..context import StageContext, StageFailure
from ..graph import CONTENT_HASH_VERSION, StageResult, Subject
from ..hashing import canonical_hash
from ..sql import dumps, uid
from ._common import load_video, require_video
from .registry import StageImpl

__all__ = ["IMPL", "PENDING", "is_pending", "on_disk"]

#: ``videos.metadata`` key: the row's ``content_hash`` is a stand-in until this stage runs
PENDING = "hash_pending"

#: longest a name lookup or the head + tail read may take. Some names (``⧸``, ``｜``) make
#: macOS's SMB client hang instead of failing; without a limit the job holds a nas_read
#: slot for good while its heartbeat keeps the reaper away.
READ_TIMEOUT_S = 120.0


def is_pending(metadata: dict[str, Any] | None) -> bool:
    return bool((metadata or {}).get(PENDING))


def on_disk(path: str) -> str | None:
    """``path`` as the file system names it: as given, else its NFD / NFC form, else the
    entry of its folder whose normalised name is the same; None when the folder lists no
    such entry. A folder that cannot be listed (the share stalled) raises ``OSError``:
    that is not a missing file."""
    if os.path.exists(path):
        return path
    parent, name = os.path.split(path)
    for form in ("NFD", "NFC"):
        alt = os.path.join(parent, unicodedata.normalize(form, name))
        if os.path.exists(alt):
            return alt
    entries = os.listdir(parent)
    want = unicodedata.normalize("NFC", name)
    return next(
        (os.path.join(parent, e) for e in entries if unicodedata.normalize("NFC", e) == want),
        None,
    )


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
