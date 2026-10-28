"""The download loop shared by ``youtube_fetch`` and ``youtube_backfill`` (Epic 47).

A job takes a library's ``wanted`` items in order (the new uploads newest first for
``youtube_fetch``; the back catalogue oldest first, at most :data:`BACKFILL_BATCH`, for
``youtube_backfill``) until **one video lands**; it then requests ``convert_mp4`` for that
video and its own stage for the next item (``StageResult.requests``, the graph's own
enqueue). Per item:

1. yt-dlp downloads into a local work folder (``~/.maktaba/work/yt/<library>/<id>/``,
   kept for 24 h so a ``.part`` resumes), with the sidecars;
2. the file is checked locally (a full decode; an MP4 without faststart is remuxed first);
3. the sidecars are copied beside the target, and the video goes in through the swap order
   (:func:`~maktaba_pipeline.library_mgmt.swap.swap_in`: staged dotfile, checked on the
   share, ledger row ``youtube_fetch``) as ``<root>/<folder>/<date> <title> [<id>].mp4``;
4. the scanner's single-file upsert makes its ``videos`` row (the probe follows, then
   ``link`` reads the ``.info.json``), and the archive gets its line.

Errors follow :mod:`maktaba_pipeline.integrations.youtube_errors`: a bot check or a 429
stops the job (it parks), a skipped or gone item is recorded and the loop goes on, a
network error is tried again later in the job (up to 5 times), a missing format once with
``-f b``. Three failed items in a row with the same class stop the job early.
"""

from __future__ import annotations

import asyncio
import os
import shutil
import time
from collections import deque
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field
from datetime import UTC, date, datetime, timedelta
from pathlib import Path
from typing import Any, Literal
from uuid import UUID, uuid4

from ...db.jobs import Stage, enqueue
from ...db.jobs_state import read_flags
from ...db.pubsub import VIDEOS_NEW, get_bus
from ...discovery.ytdlp_sidecars import VideoInfo, parse_info_json
from ...identity import hash_file
from ...integrations.youtube_download import (
    DownloadOptions,
    download_argv,
    parse_progress,
    run_ytdlp,
    sanitize_name,
    video_filename,
)
from ...integrations.youtube_errors import (
    BOT_CHECK_KIND,
    RATE_LIMITED_KIND,
    Classified,
    classify,
)
from ...library_mgmt import youtube_sources as ys
from ...library_mgmt.repair import plain_name
from ...library_mgmt.swap import SwapError, SwapVerifyFailed, swap_in
from ...library_mgmt.verify import ProbeInfo, VerifyResult, moov_before_mdat, verify_media
from ...scanner.service import SaveCandidateParams
from ...scanner.store import SqlScanStore
from .. import media
from ..context import JobCancelled, StageContext, StageFailure
from ..graph import EPIC47_PRIORITIES, Request, StageResult
from ..hashing import canonical_hash
from ..sql import is_pg, loads, now, to_ts, uid

__all__ = [
    "BACKFILL_BATCH",
    "FAILURE_RUN",
    "TRANSIENT_TRIES",
    "Verifier",
    "drain",
    "set_sleep",
    "set_verifier",
    "work_root",
]

BACKFILL_BATCH = 25
TRANSIENT_TRIES = 5
FAILURE_RUN = 3
TRANSIENT_DELAY_SEC = 30.0
RATE_LIMIT_BACKOFF = timedelta(hours=1)
WORK_KEEP = timedelta(hours=24)
WORK_ENV = "MAKTABA_YT_WORK"
PARTIAL = (".part", ".ytdl", ".temp")

#: ``(local file, the duration yt-dlp reported, full decode?, audio only?)``
Verifier = Callable[[str, float | None, bool, bool], Awaitable[VerifyResult]]
Sleep = Callable[[float], Awaitable[None]]


async def default_verify(
    path: str, duration: float | None, full: bool, audio_only: bool
) -> VerifyResult:
    """The shared verification for a download: readable, a video stream (an audio-only
    source: none needed), faststart, the duration yt-dlp reported, and (``full``) a full
    decode."""
    original = ProbeInfo(path, None, duration, None, ()) if audio_only else None
    res = await verify_media(path, original, mode="remux", full_decode=full, count=False)
    got = (res.measured.get("new") or {}).get("duration")
    if not audio_only and duration and isinstance(got, int | float):
        tol = max(2.0, duration * 0.01)  # YouTube's duration is whole seconds
        res.measured["expected_duration"] = duration
        if abs(float(got) - duration) > tol:
            res.reasons.append(f"duration {got:.1f} s, YouTube says {duration:.0f} s")
            res.ok = False
    return res


_verifier: Verifier = default_verify
_sleep: Sleep = asyncio.sleep


def set_verifier(fn: Verifier | None) -> Verifier:
    """Replace the verification (tests); None restores it. Returns the previous one."""
    global _verifier
    previous = _verifier
    _verifier = fn or default_verify
    return previous


def set_sleep(fn: Sleep | None) -> Sleep:
    global _sleep
    previous = _sleep
    _sleep = fn or asyncio.sleep
    return previous


def work_root() -> Path:
    configured = os.environ.get(WORK_ENV, "").strip()
    if configured:
        return Path(configured)
    return Path.home() / ".maktaba" / "work" / "yt"


def _prune_work(folder: Path, keep: timedelta = WORK_KEEP) -> None:
    """Item folders untouched for ``keep`` (an abandoned ``.part``, a failed check)."""
    if not folder.is_dir():
        return
    cutoff = time.time() - keep.total_seconds()
    for child in folder.iterdir():
        try:
            if child.is_dir() and child.stat().st_mtime < cutoff:
                shutil.rmtree(child, ignore_errors=True)
        except OSError:
            continue


# ------------------------------------------------------------------ items


@dataclass(slots=True)
class Item:
    youtube_id: str
    source_id: UUID
    folder: str
    options: DownloadOptions
    upload_date: date | None
    tries: int = 0
    format_fallback: bool = False


def _as_date(v: Any) -> date | None:
    if v is None:
        return None
    if isinstance(v, date):
        return v
    try:
        return date.fromisoformat(str(v)[:10])
    except ValueError:
        return None


async def _items(db: Any, library_id: UUID, *, backfill: bool, limit: int | None) -> list[Item]:
    modes = "('new_and_backfill')" if backfill else "('new_and_backfill','new_only')"
    order = "ASC" if backfill else "DESC"
    rows = await db.fetch(
        "SELECT d.youtube_id AS yid, d.source_id AS sid, d.upload_date AS d, s.folder AS folder,"
        " s.options AS options, s.external_id AS ext FROM youtube_downloads d"
        " JOIN youtube_sources s ON s.id = d.source_id"
        f" WHERE d.library_id = $1 AND d.state = 'wanted' AND s.mode IN {modes}"
        " AND d.backfill = $2 AND (d.not_before IS NULL OR d.not_before <= $3)"
        f" ORDER BY (d.upload_date IS NULL), d.upload_date {order}, d.requested_at, d.youtube_id"
        + ("" if limit is None else f" LIMIT {int(limit)}"),
        uid(library_id),
        backfill if is_pg(db) else int(backfill),
        to_ts(db, now()),
    )
    return [
        Item(
            youtube_id=str(r["yid"]),
            source_id=UUID(str(r["sid"])),
            folder=str(r["folder"] or sanitize_name(str(r["ext"]))),
            options=DownloadOptions.from_json(loads(r["options"])),
            upload_date=_as_date(r["d"]),
        )
        for r in rows
    ]


async def _set_item(db: Any, library_id: UUID, youtube_id: str, state: str, **cols: Any) -> None:
    sets = ["state = $3"]
    args: list[Any] = [uid(library_id), youtube_id, state]
    for k, v in cols.items():
        if k == "attempts_delta":
            args.append(int(v))
            sets.append(f"attempts = attempts + ${len(args)}")
            continue
        if isinstance(v, datetime):
            v = to_ts(db, v)
        elif isinstance(v, UUID):
            v = uid(v)
        args.append(v)
        sets.append(f"{k} = ${len(args)}")
    await db.execute(
        f"UPDATE youtube_downloads SET {', '.join(sets)} WHERE library_id = $1 AND youtube_id = $2",
        *args,
    )


async def _roots(db: Any, library_id: UUID) -> list[str]:
    row = await db.fetchrow("SELECT roots FROM libraries WHERE id = $1", uid(library_id))
    if row is None:
        raise StageFailure(
            "library_missing", f"library {library_id} does not exist", retryable=False
        )
    roots = [str(r) for r in (loads(row["roots"]) or [])]
    if not roots:
        raise StageFailure("library_no_root", "the library has no folder", retryable=False)
    return roots


# ------------------------------------------------------------------ one item


@dataclass(slots=True)
class Outcome:
    kind: Literal[
        "done", "skipped", "gone", "transient", "format", "unknown", "verify", "bot_check",
        "rate_limited", "stopped",
    ]  # fmt: skip
    reason: str | None = None
    message: str | None = None
    video_id: UUID | None = None
    bytes: int | None = None
    verify: dict[str, Any] | None = None


def _outcome(c: Classified) -> Outcome:
    kind = {
        "bot_check": "bot_check",
        "rate_limited": "rate_limited",
        "skipped": "skipped",
        "gone": "gone",
        "transient": "transient",
        "format": "format",
        "unknown": "unknown",
    }[c.action]
    return Outcome(kind, c.reason, c.message)  # type: ignore[arg-type]


def _db_join(root: str, *parts: str) -> str:
    sep = "\\" if ("\\" in root and "/" not in root) else "/"
    return sep.join([root.rstrip("/\\"), *parts])


def _made(work: Path, youtube_id: str, audio_only: bool) -> Path | None:
    stem = plain_name(youtube_id)
    exts = (".m4a", ".mp4", ".webm", ".opus", ".mp3") if audio_only else (".mp4", ".mkv", ".webm")
    for ext in exts:
        p = work / f"{stem}{ext}"
        if p.is_file() and p.stat().st_size > 0:
            return p
    return None


async def _faststart(path: Path) -> Path:
    """An MP4 whose index is at the end, remuxed with it at the front (the browser can
    start playing it); the same file when it already is (or is not an MP4)."""
    if moov_before_mdat(str(path)) is not False:
        return path
    out = path.with_name(path.stem + ".faststart" + path.suffix)
    res = await media.run_media(
        [media.FFMPEG, "-nostdin", "-v", "error", "-y", "-i", str(path), "-map", "0",
         "-c", "copy", "-movflags", "+faststart", str(out)],
        timeout=1800.0,
    )  # fmt: skip
    if res.returncode != 0 or not out.is_file():
        out.unlink(missing_ok=True)
        return path
    os.replace(out, path)
    return path


def _copy_sidecars(work: Path, youtube_id: str, media_file: Path, dest_dir: str, stem: str) -> int:
    """``v-<id>.info.json`` / ``.jpg`` / ``.ar.srt`` ... beside the target as
    ``<stem>.info.json`` ...; each through a dotfile then a rename."""
    plain = plain_name(youtube_id)
    n = 0
    for p in sorted(work.iterdir()):
        if p == media_file or not p.is_file() or not p.name.startswith(plain + "."):
            continue
        if p.suffix.lower() in PARTIAL or ".faststart." in p.name:
            continue
        suffix = p.name[len(plain) :]
        target = os.path.join(dest_dir, stem + suffix)
        tmp = os.path.join(dest_dir, f".{stem}{suffix}.maktaba-new")
        shutil.copyfile(p, tmp)
        os.replace(tmp, target)
        n += 1
    return n


@dataclass(slots=True)
class Place:
    db: Any
    ctx: StageContext
    library_id: UUID
    root_db: str
    root_local: str
    archive: str
    store: SqlScanStore


async def _upsert(place: Place, final_db: str) -> UUID:
    """The scanner's single-file upsert: the ``videos`` row (and the probe that starts
    the pipeline), or the existing row for the same bytes."""
    local = place.ctx.local_path(final_db)
    h = await asyncio.to_thread(hash_file, local)
    lib = await place.store.get_library(place.library_id)
    st = os.stat(local)
    params = SaveCandidateParams(
        library_id=place.library_id,
        content_hash=h.content_hash,
        path=final_db,
        filename=os.path.basename(local),
        size_bytes=h.size_bytes,
        mtime=datetime.fromtimestamp(st.st_mtime, UTC),
        last_seen_at=datetime.now(UTC),
        enqueue_probe=lib is not None and not lib.disabled,
    )
    if not is_pg(place.db):
        return await _upsert_sqlite(place, params)
    save = await place.store.save_candidate(params)
    return save.video_id


async def _upsert_sqlite(place: Place, params: SaveCandidateParams) -> UUID:
    """The same upsert on SQLite, whose ``videos`` lost the ``(library_id, content_hash)``
    key in 0004 (``content_hash`` is unique on its own there), so the scanner's
    ``ON CONFLICT (library_id, content_hash)`` cannot be prepared."""
    db = place.db
    async with db.transaction():
        row = await db.fetchrow(
            "SELECT id FROM videos WHERE library_id = $1 AND content_hash = $2",
            uid(params.library_id),
            params.content_hash,
        )
        if row is not None:
            return UUID(str(row["id"]))
        video_id = uuid4()
        await db.execute(
            "INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes,"
            " mtime, last_seen_at, state) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'discovered')",
            str(video_id),
            uid(params.library_id),
            params.content_hash,
            params.path,
            params.filename,
            params.size_bytes,
            to_ts(db, params.mtime),
            to_ts(db, params.last_seen_at),
        )
        if params.enqueue_probe:
            await enqueue(db, video_id=video_id, stage=Stage.PROBE, priority=100)
    get_bus().publish(
        VIDEOS_NEW,
        {
            "id": str(video_id),
            "library_id": str(params.library_id),
            "content_hash": params.content_hash,
            "path": params.path,
            "filename": params.filename,
            "state": "discovered",
        },
    )
    return video_id


async def _place(place: Place, item: Item, work: Path, media_file: Path) -> Outcome:
    info_path = work / f"{plain_name(item.youtube_id)}.info.json"
    info: VideoInfo | None = None
    if info_path.is_file():
        parsed = parse_info_json(info_path.read_text(encoding="utf-8", errors="replace"))
        info = parsed if isinstance(parsed, VideoInfo) else None
    audio_only = item.options.audio_only
    if media_file.suffix.lower() == ".mp4":
        media_file = await _faststart(media_file)
    duration = None if info is None else info.duration_s
    checked = await _verifier(str(media_file), duration, True, audio_only)
    if not checked.ok:
        return Outcome(
            "verify",
            "verify",
            "verification failed: " + "; ".join(checked.reasons),
            verify=checked.as_json(),
        )

    title = info.title if info is not None else None
    day = (info.upload_date if info is not None else None) or item.upload_date
    name = video_filename(item.youtube_id, title, day, media_file.suffix.lstrip(".").lower())
    folder = sanitize_name(item.folder)
    dir_db = _db_join(place.root_db, folder)
    final_db = _db_join(place.root_db, folder, name)
    dir_local = place.ctx.local_path(dir_db)
    os.makedirs(dir_local, exist_ok=True)
    final_local = place.ctx.local_path(final_db)
    stem = name.rsplit(".", 1)[0]
    if os.path.exists(final_local):
        # this very video is there already (the name carries its id): take it as it is
        vid = await _upsert(place, final_db)
        return Outcome("done", "exists", None, vid, os.path.getsize(final_local))
    await asyncio.to_thread(_copy_sidecars, work, item.youtube_id, media_file, dir_local, stem)

    async def on_share(staged: str) -> VerifyResult:
        return await _verifier(staged, duration, False, audio_only)

    try:
        await swap_in(
            place.db,
            library_id=place.library_id,
            kind="youtube_fetch",
            source=str(media_file),
            final_path=final_db,
            verify=on_share,
            job_id=place.ctx.job.id,
            to_local=place.ctx.local_path,
        )
    except SwapVerifyFailed as exc:
        return Outcome("verify", "verify", str(exc), verify=exc.result.as_json())
    except SwapError as exc:
        return Outcome("unknown", "placement", str(exc))
    size = os.path.getsize(final_local)
    vid = await _upsert(place, final_db)
    ys.append_archive(place.archive, item.youtube_id)
    return Outcome("done", None, None, vid, size)


async def _download(
    place: Place,
    item: Item,
    *,
    fallback: bool,
    on_bytes: Callable[[int, int | None], Awaitable[None]],
) -> Outcome:
    work = work_root() / str(place.library_id) / plain_name(item.youtube_id)
    work.mkdir(parents=True, exist_ok=True)
    os.utime(work)
    job_id = place.ctx.job.id

    async def stop() -> bool:
        return (await read_flags(place.db, job_id=job_id)).cancel

    async def line(text: str) -> None:
        doc = parse_progress(text)
        if doc is not None:
            got = doc.get("downloaded_bytes")
            total = doc.get("total_bytes") or doc.get("total_bytes_estimate")
            if isinstance(got, int | float):
                await on_bytes(int(got), int(total) if isinstance(total, int | float) else None)

    res = await run_ytdlp(
        download_argv(
            item.youtube_id,
            work,
            archive=place.archive,
            options=item.options,
            fallback_format=fallback,
        ),
        on_line=line,
        should_stop=stop,
    )
    if res.stopped:
        return Outcome("stopped")
    found = _made(work, item.youtube_id, item.options.audio_only)
    out_text = "\n".join(res.stdout)
    if found is None:
        if res.returncode == 0 and "has already been recorded in the archive" in out_text:
            return Outcome("done", "archive")
        if res.returncode == 0 and "does not pass filter" in out_text:
            return Outcome("skipped", "live", "a live stream or a premiere")
        if res.returncode == 0:
            return Outcome("unknown", "no_file", "yt-dlp finished without a file")
        return _outcome(classify(res.stderr or out_text))
    if res.returncode != 0:
        # the video is there and an error came after it: a sidecar (subtitles often meet
        # a 429 of their own); the video is checked like any other, the error noted
        await place.ctx.log(
            "warn",
            f"{item.youtube_id}: downloaded with an error: {classify(res.stderr).message}",
            {"youtube_id": item.youtube_id},
        )
    outcome = await _place(place, item, work, found)
    if outcome.kind == "done":
        shutil.rmtree(work, ignore_errors=True)
    return outcome


# ------------------------------------------------------------------ the loop


@dataclass(slots=True)
class Tally:
    done: int = 0
    skipped: int = 0
    gone: int = 0
    failed: int = 0
    retried: int = 0
    bytes: int = 0
    ids: list[str] = field(default_factory=list)

    def as_json(self) -> dict[str, Any]:
        return {
            "downloaded": self.done,
            "skipped": self.skipped,
            "gone": self.gone,
            "failed": self.failed,
            "retried": self.retried,
            "bytes": self.bytes,
        }


async def _yield_reason(db: Any, job: Any, stage: str, priority: int) -> str | None:
    """Why a back-catalogue batch stops before its next item: its job or its stage was
    paused, or more urgent work waits for the pool (a new upload, a listing)."""
    flags = await read_flags(db, job_id=job.id)
    if flags.pause:
        return "paused"
    row = await db.fetchrow("SELECT paused FROM job_stages WHERE stage = $1", stage)
    if row is not None and bool(row["paused"]):
        return "stage paused"
    other = await db.fetchrow(
        "SELECT j.stage AS stage FROM processing_jobs j JOIN job_stages s ON s.stage = j.stage"
        " WHERE s.pool = (SELECT pool FROM job_stages WHERE stage = $1) AND j.id <> $2"
        " AND j.state = 'pending' AND j.priority < $3 AND s.enabled AND NOT s.paused"
        " AND j.parked_on IS NULL AND NOT j.cancel_requested AND NOT j.pause_requested"
        " AND (j.not_before IS NULL OR j.not_before <= $4) ORDER BY j.priority, j.id LIMIT 1",
        stage,
        job.id,
        priority,
        to_ts(db, now()),
    )
    return None if other is None else f"yielded to {other['stage']}"


async def _backfill_progress(db: Any, library_id: UUID) -> tuple[int, int]:
    row = await db.fetchrow(
        "SELECT count(*) AS total, sum(CASE WHEN d.state = 'wanted' THEN 0 ELSE 1 END) AS done"
        " FROM youtube_downloads d JOIN youtube_sources s ON s.id = d.source_id"
        " WHERE d.library_id = $1 AND d.backfill = $2 AND s.mode = 'new_and_backfill'",
        uid(library_id),
        True if is_pg(db) else 1,
    )
    return int(row["done"] or 0), int(row["total"] or 0)


async def drain(db: Any, job: Any, ctx: StageContext, *, backfill: bool) -> StageResult:
    library_id: UUID = job.subject.id
    stage = "youtube_backfill" if backfill else "youtube_fetch"
    priority = EPIC47_PRIORITIES[stage]
    roots = await _roots(db, library_id)
    root_local = ctx.local_path(roots[0])
    if not os.path.isdir(root_local):
        raise StageFailure(
            "file_missing", f"no such file or directory: library root {roots[0]}", retryable=True
        )
    archive = ys.archive_path(root_local)

    # a crash mid-item left it ``downloading``: one network_yt job runs at a time
    await db.execute(
        "UPDATE youtube_downloads SET state = 'wanted' WHERE library_id = $1"
        " AND state = 'downloading'",
        uid(library_id),
    )
    await asyncio.to_thread(ys.write_archive, archive, await ys.local_ids(db, library_id))
    await asyncio.to_thread(_prune_work, work_root() / str(library_id))

    items = await _items(
        db, library_id, backfill=backfill, limit=BACKFILL_BATCH if backfill else None
    )
    place = Place(db, ctx, library_id, roots[0], root_local, archive, SqlScanStore(db))
    tally = Tally()
    queue: deque[Item] = deque(items)
    total = len(items)
    run_class: str | None = None
    run_items: set[str] = set()
    landed: list[tuple[str, UUID]] = []
    stopped_why: str | None = None
    #: (done, of) for the progress line: this job's items, or the library's back catalogue
    counts = [0, total]

    async def count() -> None:
        n = tally.done + tally.skipped + tally.gone + tally.failed
        counts[:] = list(await _backfill_progress(db, library_id)) if backfill else [n, total]

    async def show(got: int = 0, size: int | None = None, *, force: bool = False) -> None:
        done, of = counts
        msg = f"backfill: {done:,} of {of:,}" if backfill else f"{done} of {of}"
        if got:
            msg += f" · {got / 1_000_000:.0f} MB"
            msg += f" of {size / 1_000_000:.0f} MB" if size else ""
        await ctx.progress("download", done, of, msg, force=force)

    while queue:
        await ctx.check_cancel()
        if backfill and tally.done + tally.skipped + tally.gone + tally.failed > 0:
            why = await _yield_reason(db, job, stage, priority)
            if why is not None:
                stopped_why = why
                break
        item = queue.popleft()
        if item.tries:
            await _sleep(TRANSIENT_DELAY_SEC)  # a network error: give it a moment
        await count()
        await show()
        await _set_item(db, library_id, item.youtube_id, "downloading", attempts_delta=1)
        try:
            out = await _download(place, item, fallback=item.format_fallback, on_bytes=show)
        except BaseException:
            await _set_item(db, library_id, item.youtube_id, "wanted")
            raise
        t = now()

        if out.kind == "stopped":
            await _set_item(db, library_id, item.youtube_id, "wanted", attempts_delta=-1)
            raise JobCancelled(f"job {job.id} cancelled")
        if out.kind == "bot_check":
            await _set_item(
                db, library_id, item.youtube_id, "wanted",
                failure_class="bot_check", error=out.message,
            )  # fmt: skip
            await ys.set_source_status(db, item.source_id, "bot_check", out.message)
            raise StageFailure(
                BOT_CHECK_KIND,
                f"YouTube asked to sign in (downloading {item.youtube_id}); waiting before"
                " the next try, or give the source a signed-in browser's cookies",
            )
        if out.kind == "rate_limited":
            await _set_item(
                db, library_id, item.youtube_id, "wanted",
                failure_class="rate_limited", error=out.message,
                not_before=t + RATE_LIMIT_BACKOFF,
            )  # fmt: skip
            raise StageFailure(
                RATE_LIMITED_KIND,
                f"YouTube said too many requests (downloading {item.youtube_id}); waiting"
                " at least an hour",
            )

        fail_key: str | None = None
        if out.kind == "done":
            tally.done += 1
            tally.bytes += out.bytes or 0
            tally.ids.append(item.youtube_id)
            if out.video_id is not None:
                landed.append((item.youtube_id, out.video_id))
            await _set_item(
                db, library_id, item.youtube_id, "done",
                reason=out.reason, video_id=out.video_id, bytes=out.bytes, done_at=t,
                failure_class=None, error=None, not_before=None,
            )  # fmt: skip
            await db.execute(
                "UPDATE youtube_sources SET last_fetched_at = $2, status = 'ok', error = NULL"
                " WHERE id = $1",
                uid(item.source_id),
                to_ts(db, t),
            )
        elif out.kind in ("skipped", "gone"):
            if out.kind == "skipped":
                tally.skipped += 1
            else:
                tally.gone += 1
            await _set_item(
                db, library_id, item.youtube_id, out.kind,
                reason=out.reason, failure_class=out.kind, error=out.message,
            )  # fmt: skip
        elif out.kind == "format" and not item.format_fallback:
            item.format_fallback = True
            queue.appendleft(item)
            await _set_item(db, library_id, item.youtube_id, "wanted", error=out.message)
            tally.retried += 1
            continue
        elif out.kind == "transient" and item.tries + 1 < TRANSIENT_TRIES:
            item.tries += 1
            tally.retried += 1
            fail_key = "transient"
            queue.append(item)
            await _set_item(
                db, library_id, item.youtube_id, "wanted",
                failure_class="transient", error=out.message,
            )  # fmt: skip
        else:
            tally.failed += 1
            fail_key = {"verify": "convert_verify", "format": "format"}.get(out.kind, out.kind)
            await _set_item(
                db, library_id, item.youtube_id, "failed",
                reason=out.reason, failure_class=fail_key, error=out.message,
                not_before=None,
            )  # fmt: skip
            await ctx.log(
                "warn",
                f"{item.youtube_id}: {out.message or out.kind}",
                {"youtube_id": item.youtube_id, "class": fail_key, "verify": out.verify},
            )

        # a run counts items, not tries: one item's network retries are one failure
        if fail_key is None:
            run_class, run_items = None, set()
        elif fail_key == run_class:
            run_items.add(item.youtube_id)
        else:
            run_class, run_items = fail_key, {item.youtube_id}
        run_len = len(run_items)
        if run_class is not None and run_len >= FAILURE_RUN:
            raise StageFailure(
                f"youtube_{run_class}",
                f"{FAILURE_RUN} downloads in a row failed the same way ({run_class}): stopped;"
                f" the last: {(out.message or '')[:200]}",
                retryable=run_class == "transient",
            )
        if landed:
            break  # one video per job: it goes on to convert while the next downloads

    await count()
    await show(force=True)
    remaining = await ys.wanted_count(db, library_id, backfill=backfill)
    # one item at a time, through the graph: the video that landed goes on to
    # ``convert_mp4`` (and from there to dedup) while this stage's next job downloads the
    # next one, queued behind anything more urgent in the pool
    requests = [
        Request(
            "convert_mp4", "video", vid, f"downloaded from YouTube ({yid_})",
            priority=EPIC47_PRIORITIES["convert_mp4"],
        )
        for yid_, vid in landed
    ]  # fmt: skip
    if remaining:
        why = "next back-catalogue item" if backfill else "next new upload"
        requests.append(Request(stage, "library", library_id, why, priority=priority))
    summary: dict[str, Any] = {**tally.as_json(), "items": total, "remaining": remaining}
    if stopped_why:
        summary["stopped"] = stopped_why
    return StageResult(
        output_hash=canonical_hash({"done": sorted(tally.ids)}),
        requests=tuple(requests),
        summary=summary,
    )
