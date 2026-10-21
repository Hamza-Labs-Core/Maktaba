"""``repair_broken``: download again the files ffprobe cannot read (mediaintel ``repair-broken``).

Ported from ``scripts/mediaintel/repair_broken.py``; the file work is the shared
:mod:`maktaba_pipeline.library_mgmt.repair` (the same code the stand-alone tool runs).

Subject: a library. Pool ``repairs``. **Writes to the share, so it is off until the
``repair`` rules say ``enabled``.** For each video whose latest ``probe`` job failed:

* the file is read again first: a passing failure (a busy share, a timeout) is no reason to
  replace anything. It reads fine: recorded ``readable``, probe asked again;
* a file that cannot be told (a timeout) is left for a later run;
* a file that really is broken (an unfinished download: "moov atom not found") with a YouTube
  id is fetched again (H.264 first, at most ``max_height``) into a local temporary folder,
  checked with ffprobe, copied beside the original, checked there and only then swapped in; the
  broken file is kept as ``<name>.broken`` (rule ``keep_broken_copy``). ``probe`` and
  ``content_hash`` are asked to look at the new file;
* a video no longer on YouTube, or a file without a YouTube id, is reported and left alone.

* Epic 40: a file an admin asked to download again (``item_requests``, action ``redownload``,
  from the failures page) is fetched whatever the ``enabled`` switch says (the admin asked
  for this file) and without the read-again check (a missing file cannot be read); the
  request is marked done with what it came to.

What each file came to is in ``video_repairs``. The change set (the files to be fetched) goes
through ``apply_or_review``: in review mode nothing is downloaded until an operator applies it.
"""

from __future__ import annotations

import asyncio
import shutil
import subprocess
import tempfile
from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path
from typing import Any
from uuid import UUID

from ...library_mgmt import repair
from ...schedule.tv_settings import RepairConfig
from ..changes import ChangeSet, apply_or_review
from ..context import StageContext
from ..graph import Request, StageResult, Subject
from ..hashing import canonical_hash
from ..media import FFMPEG, FFPROBE
from ..paths import to_db
from ..sql import is_pg, now, to_ts, uid
from .registry import StageImpl

__all__ = ["IMPL", "Broken", "broken_files", "requested_files", "set_runner"]

_run: Callable[..., Any] = subprocess.run


def set_runner(fn: Callable[..., Any] | None) -> Callable[..., Any]:
    """Replace ``subprocess.run`` for yt-dlp / ffmpeg / ffprobe (tests); None restores it."""
    global _run
    previous = _run
    _run = fn or subprocess.run
    return previous


@dataclass(frozen=True, slots=True)
class Broken:
    video_id: UUID
    path: str
    youtube_id: str | None
    #: an admin's ``item_requests`` row (Epic 40): fetched without the read-again check
    request_id: int | None = None


_BROKEN = """
SELECT v.id AS id, v.path AS path, m.external_id AS youtube_id
  FROM videos v
  LEFT JOIN media_metadata_enrichment m
    ON m.video_id = v.id AND m.provider = 'youtube' AND m.is_dismissed = {false}
 WHERE v.library_id = $1 AND v.deleted_at IS NULL
   AND (SELECT j.state FROM processing_jobs j
         WHERE j.video_id = v.id AND j.stage = 'probe' ORDER BY j.id DESC LIMIT 1) = 'failed'
 ORDER BY v.path
"""


async def broken_files(db: Any, library_id: UUID) -> list[Broken]:
    """The library's videos whose latest ``probe`` job failed, with their YouTube id."""
    sql = _BROKEN.replace("{false}", "false" if is_pg(db) else "0")
    return [
        Broken(
            UUID(str(r["id"])),
            str(r["path"]),
            None if r["youtube_id"] is None else str(r["youtube_id"]),
        )
        for r in await db.fetch(sql, uid(library_id))
    ]


_REQUESTED = """
SELECT r.id AS request_id, v.id AS id, v.path AS path, m.external_id AS youtube_id
  FROM item_requests r
  JOIN videos v ON v.id = r.video_id
  LEFT JOIN media_metadata_enrichment m
    ON m.video_id = v.id AND m.provider = 'youtube' AND m.is_dismissed = {false}
 WHERE v.library_id = $1 AND r.action = 'redownload' AND r.done_at IS NULL
 ORDER BY r.id
"""


async def requested_files(db: Any, library_id: UUID) -> list[Broken]:
    """The library's videos an admin asked to download again (open requests, oldest first;
    one per video)."""
    sql = _REQUESTED.replace("{false}", "false" if is_pg(db) else "0")
    out: dict[str, Broken] = {}
    for r in await db.fetch(sql, uid(library_id)):
        key = str(r["id"])
        if key not in out:
            out[key] = Broken(
                UUID(key),
                str(r["path"]),
                None if r["youtube_id"] is None else str(r["youtube_id"]),
                int(r["request_id"]),
            )
    return list(out.values())


async def _close_requests(db: Any, video_id: UUID, result: str) -> None:
    await db.execute(
        "UPDATE item_requests SET done_at = $2, result = $3"
        " WHERE video_id = $1 AND action = 'redownload' AND done_at IS NULL",
        uid(video_id),
        to_ts(db, now()),
        result[:500],
    )


async def _cfg(db: Any) -> tuple[RepairConfig, dict[str, Any]]:
    from ... import rules

    cfg, changes, _ = await rules.read(db, "repair")
    return cfg, changes


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    """The switch, the settings that change what is fetched and the files waiting."""
    cfg, changes = await _cfg(db)
    rows = await broken_files(db, subject.id) if cfg.enabled else []
    asked = await requested_files(db, subject.id)
    return canonical_hash(
        {
            "enabled": cfg.enabled,
            "rules": changes,
            "broken": [(str(b.video_id), b.youtube_id) for b in rows],
            "requested": [b.request_id for b in asked],
        }
    )


async def _record(
    db: Any,
    b: Broken,
    library_id: UUID,
    state: str,
    message: str,
    backup: Path | None = None,
) -> None:
    await db.execute(
        "INSERT INTO video_repairs (video_id, library_id, youtube_id, state, message,"
        " backup_path, attempts, updated_at) VALUES ($1,$2,$3,$4,$5,$6,1,$7)"
        " ON CONFLICT (video_id) DO UPDATE SET library_id = EXCLUDED.library_id,"
        " youtube_id = EXCLUDED.youtube_id, state = EXCLUDED.state, message = EXCLUDED.message,"
        " backup_path = EXCLUDED.backup_path, attempts = video_repairs.attempts + 1,"
        " updated_at = EXCLUDED.updated_at",
        uid(b.video_id),
        uid(library_id),
        b.youtube_id,
        state,
        message[:500],
        None if backup is None else to_db(str(backup)),  # canonical on a share
        to_ts(db, now()),
    )


def _fetch_one(local: str, youtube_id: str, cfg: RepairConfig) -> repair.RepairOutcome:
    work = Path(tempfile.mkdtemp(prefix="maktaba-repair-"))
    try:
        return repair.fetch_and_swap(
            local,
            youtube_id,
            work,
            cookies_from_browser=cfg.cookies_from_browser or None,
            max_height=cfg.max_height,
            h264_first=cfg.h264_first,
            keep_broken=cfg.keep_broken_copy,
            download_timeout_s=cfg.download_timeout_minutes * 60.0,
            run=_run,
            ffmpeg=FFMPEG,
            ffprobe=FFPROBE,
        )
    finally:
        shutil.rmtree(work, ignore_errors=True)


async def run(db: Any, job: Any, ctx: StageContext) -> StageResult:
    cfg: RepairConfig = await ctx.rules("repair")
    library_id: UUID = job.subject.id
    asked = await requested_files(db, library_id)
    if not cfg.enabled and not asked:
        return StageResult(
            output_hash=canonical_hash("repair_broken:off"),
            outcome="unchanged",
            summary={"enabled": False},
        )
    rows = await broken_files(db, library_id) if cfg.enabled else []
    if cfg.max_per_run > 0:
        rows = rows[: cfg.max_per_run]
    seen = {b.video_id for b in asked}
    rows = [*asked, *(b for b in rows if b.video_id not in seen)]
    counts = {"repaired": 0, "readable": 0, "failed": 0, "no_id": 0, "undecided": 0}
    results: list[tuple[str, str]] = []
    requests: list[Request] = []

    changes = ChangeSet(
        "repair_broken",
        changed=tuple(
            {
                "kind": "file",
                "video_id": str(b.video_id),
                "path": b.path,
                "youtube_id": b.youtube_id,
            }
            for b in rows
        ),
    )

    async def apply(_cs: ChangeSet) -> None:
        for n, b in enumerate(rows, 1):
            await ctx.check_cancel()
            await ctx.progress("repair", n - 1, len(rows), Path(b.path).name)
            local = ctx.local_path(b.path)
            if b.request_id is not None:
                await _requested(b, local)
                continue
            probed = await asyncio.to_thread(repair.check, local, ffprobe=FFPROBE, run=_run)
            if probed.readable:  # a passing failure: the file is fine
                counts["readable"] += 1
                await _record(db, b, library_id, "readable", "reads fine now, kept")
                requests.append(_ask("probe", b, "read fine after all"))
                results.append((str(b.video_id), "readable"))
                continue
            if not probed.why.startswith("broken file"):
                counts["undecided"] += 1  # cannot tell now: never replace on a guess
                results.append((str(b.video_id), "undecided"))
                continue
            if not b.youtube_id:
                counts["no_id"] += 1
                msg = f"{probed.why}; no YouTube id, cannot fetch it again"
                await _record(db, b, library_id, "no_id", msg)
                results.append((str(b.video_id), "no_id"))
                continue
            got = await asyncio.to_thread(_fetch_one, local, b.youtube_id, cfg)
            if got.ok:
                counts["repaired"] += 1
                await _record(db, b, library_id, "repaired", got.message, got.backup)
                requests.append(_ask("probe", b, "replaced from YouTube"))
                requests.append(_ask("content_hash", b, "replaced from YouTube"))
                results.append((str(b.video_id), "repaired"))
            else:
                counts["failed"] += 1
                await _record(db, b, library_id, "failed", f"{probed.why}; {got.message}")
                results.append((str(b.video_id), "failed"))

    async def _requested(b: Broken, local: str) -> None:
        """An admin's re-download: no read-again check, the request is closed."""
        if not b.youtube_id:
            counts["no_id"] += 1
            msg = "asked to download again, but it has no YouTube id"
            await _record(db, b, library_id, "no_id", msg)
            await _close_requests(db, b.video_id, msg)
            results.append((str(b.video_id), "no_id"))
            return
        got = await asyncio.to_thread(_fetch_one, local, b.youtube_id, cfg)
        if got.ok:
            counts["repaired"] += 1
            await _record(db, b, library_id, "repaired", got.message, got.backup)
            await db.execute(
                "UPDATE videos SET missing_confirmed_at = NULL WHERE id = $1", uid(b.video_id)
            )
            requests.append(_ask("probe", b, "downloaded again"))
            requests.append(_ask("content_hash", b, "downloaded again"))
            results.append((str(b.video_id), "repaired"))
        else:
            counts["failed"] += 1
            await _record(db, b, library_id, "failed", got.message)
            results.append((str(b.video_id), "failed"))
        await _close_requests(db, b.video_id, got.message)

    status = await apply_or_review(ctx, changes, apply)
    if status == "review":
        return StageResult(
            output_hash=canonical_hash("repair_broken:review"), outcome="awaiting_review"
        )
    return StageResult(
        output_hash=canonical_hash(sorted(results)),
        outcome="changed" if counts["repaired"] else None,
        requests=tuple(requests),
        summary={"enabled": cfg.enabled, "broken": len(rows), "requested": len(asked), **counts},
    )


def _ask(stage: str, b: Broken, reason: str) -> Request:
    return Request(stage, "video", b.video_id, reason)


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
