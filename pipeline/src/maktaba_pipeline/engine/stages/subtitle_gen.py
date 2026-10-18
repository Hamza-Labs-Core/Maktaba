"""``subtitle_gen``: the transcript as subtitle files next to the video (plan 03-10).

``Name.mp4`` gets ``Name.ar.srt`` and ``Name.ar.vtt`` in the same folder: Plex, VLC and
the TVs pick them up, and the transcript lives with the file it belongs to. Each file
is written atomically (a temporary file, then a rename) and recorded in
``subtitle_files`` (source ``generated``, its path and sha256).

A file already there that Maktaba did not write (no ``subtitle_files`` row with its
hash) is never overwritten: the job notes it and leaves it. A rewrite replaces only a
file whose content still matches what Maktaba last wrote.
"""

from __future__ import annotations

import asyncio
import hashlib
import os
import posixpath
from typing import Any

from ...db.jobs import Job
from ..context import StageContext, StageFailure
from ..graph import SUBTITLE_VERSION, StageResult, Subject
from ..hashing import canonical_hash
from ..sql import dumps, now, to_ts, uid
from ._common import require_video
from .content_hash import READ_TIMEOUT_S, on_disk
from .registry import StageImpl

__all__ = ["IMPL", "SUBTITLE_VERSION", "srt", "vtt"]


def _clock(sec: float, sep: str) -> str:
    ms = max(0, round(sec * 1000))
    h, rem = divmod(ms, 3_600_000)
    m, rem = divmod(rem, 60_000)
    s, ms = divmod(rem, 1000)
    return f"{h:02d}:{m:02d}:{s:02d}{sep}{ms:03d}"


def srt(segments: list[tuple[float, float, str]]) -> str:
    cues = [
        f"{i}\n{_clock(a, ',')} --> {_clock(b, ',')}\n{t.strip()}\n"
        for i, (a, b, t) in enumerate(segments, 1)
    ]
    return "\n".join(cues)


def vtt(segments: list[tuple[float, float, str]]) -> str:
    cues = [f"{_clock(a, '.')} --> {_clock(b, '.')}\n{t.strip()}\n" for a, b, t in segments]
    return "WEBVTT\n\nNOTE maktaba (generated from the transcript)\n\n" + "\n".join(cues)


async def _active(db: Any, video_id: Any) -> tuple[str, str, list[tuple[float, float, str]]] | None:
    t = await db.fetchrow(
        "SELECT id, language FROM transcripts WHERE video_id = $1 AND is_active"
        " ORDER BY created_at DESC LIMIT 1",
        uid(video_id),
    )
    if t is None:
        return None
    rows = await db.fetch(
        "SELECT start_sec, end_sec, text FROM transcript_segments WHERE transcript_id = $1"
        " ORDER BY seq",
        t["id"],
    )
    segs = [(float(r["start_sec"]), float(r["end_sec"]), str(r["text"])) for r in rows]
    return str(t["id"]), str(t["language"]), segs


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    t = await db.fetchrow(
        "SELECT id FROM transcripts WHERE video_id = $1 AND is_active"
        " ORDER BY created_at DESC LIMIT 1",
        uid(subject.id),
    )
    return canonical_hash({"t": None if t is None else str(t["id"]), "v": SUBTITLE_VERSION})


def _sha(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def _write_atomic(path: str, data: bytes) -> None:
    tmp = f"{path}.maktaba-tmp"
    with open(tmp, "wb") as f:
        f.write(data)
    os.replace(tmp, path)


def _current_sha(path: str) -> str | None:
    try:
        with open(path, "rb") as f:
            return _sha(f.read())
    except FileNotFoundError:
        return None


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    v = await require_video(db, job.subject.id)
    active = await _active(db, v.id)
    if active is None or not active[2]:
        return StageResult(output_hash=canonical_hash("none"), summary={"written": 0})
    transcript_id, lang, segs = active

    local = ctx.local_path(v.path)
    try:
        found = await asyncio.wait_for(asyncio.to_thread(on_disk, local), READ_TIMEOUT_S)
    except TimeoutError as exc:
        raise StageFailure("nas_timeout", f"{local}: no answer", retryable=False) from exc
    except OSError as exc:
        raise StageFailure("nas_unavailable", f"{local}: {exc}") from exc
    if found is None:
        raise StageFailure("file_missing", f"{local}: not found", retryable=False)

    stem = os.path.splitext(found)[0]
    written, kept = [], []
    for fmt, body in (("srt", srt(segs)), ("vtt", vtt(segs))):
        target = f"{stem}.{lang}.{fmt}"
        data = body.encode("utf-8")
        mine = await db.fetchrow(
            "SELECT sha256 FROM subtitle_files WHERE video_id = $1 AND language = $2"
            " AND format = $3 AND source = 'generated' AND deleted_at IS NULL",
            uid(v.id),
            lang,
            fmt,
        )
        try:
            on_share = await asyncio.wait_for(
                asyncio.to_thread(_current_sha, target), READ_TIMEOUT_S
            )
            if on_share is not None and (mine is None or on_share != mine["sha256"]):
                kept.append(posixpath.basename(target))  # not ours, or edited since
                continue
            if on_share != _sha(data):
                await asyncio.wait_for(
                    asyncio.to_thread(_write_atomic, target, data), READ_TIMEOUT_S
                )
        except TimeoutError as exc:
            raise StageFailure("nas_timeout", f"{target}: write did not finish") from exc
        except PermissionError as exc:
            raise StageFailure("nas_readonly", f"{target}: {exc}", retryable=False) from exc
        except OSError as exc:
            raise StageFailure("nas_unavailable", f"{target}: {exc}") from exc
        db_path = ctx.path_map.to_db(target)
        await db.execute(
            "UPDATE subtitle_files SET deleted_at = $4 WHERE video_id = $1 AND language = $2"
            " AND format = $3 AND source = 'generated' AND deleted_at IS NULL",
            uid(v.id),
            lang,
            fmt,
            to_ts(db, now()),
        )
        await db.execute(
            "INSERT INTO subtitle_files (video_id, transcript_id, language, format, source,"
            " path, byte_size, sha256, is_external, metadata)"
            " VALUES ($1, $2, $3, $4, 'generated', $5, $6, $7, true, $8)",
            uid(v.id),
            uid(transcript_id),
            lang,
            fmt,
            db_path,
            len(data),
            _sha(data),
            dumps({"plan": "03-10"}),
        )
        written.append(posixpath.basename(target))
    return StageResult(
        output_hash=canonical_hash({"t": transcript_id, "written": written, "kept": kept}),
        summary={"written": written, "kept_foreign": kept, "segments": len(segs)},
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
