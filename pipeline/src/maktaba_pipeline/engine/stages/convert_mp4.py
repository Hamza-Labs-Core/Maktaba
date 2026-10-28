"""``convert_mp4`` (Epic 47, story 47.4): convert a video's file to MP4 without losing it.

Pool ``convert`` (one at a time), on the home. Two subjects:

* **a library**: plans every video of the library (``media_info.raw_ffprobe`` when the
  probe stage stored it, else ffprobe) and enqueues one ``convert_mp4`` job per video
  that needs work, at priority 150 (``EPIC47_PRIORITIES``). With the payload
  ``{"dry_run": true}`` it only writes the plan to its ``output_summary`` and enqueues
  nothing. ``{"video_ids": [...]}`` limits it to those videos;
* **a video**: the conversion itself (a per-video *Convert now* is enqueued at 50).

A video's job: probe the file (fresh), plan it (:mod:`...library_mgmt.convert`), run
ffmpeg into the local work folder ``~/.maktaba/work/convert/<job>/`` with its progress
on the job, verify the result there (full decode, frame count, streams, faststart), then
swap it in (:func:`...library_mgmt.swap.swap_in`: staged beside the original on the share
and checked there with a size check and ffprobe, the ``videos`` row keeps its id, the
original goes to the library's trash). Text subtitles are also written beside the MP4 as
``<name>.<lang>.srt``. The thumbnail cache folder moves to the new content hash (the
poster, sprite and small copy are asked for again only when that fails), and ``probe``
is asked to read the new container. Nothing that depends on the words or the picture
(transcribe, index, embeddings) runs again: the stage has no edges.

Failures (Epic 40 classes, :mod:`..failure_policy`):

* the input cannot be read (``ffmpeg_decode``): fails at once, the admin decides;
* the MP4 fails verification (``convert_verify``): the original is untouched, the check's
  measurements are on the ``file_actions`` row; tried once more (re-encoding, where the
  library allows it), then failed;
* the file is not there (``file_missing``): parked on ``dependency:share:<root>`` when the
  library's root does not answer, else failed;
* the work folder's disk is full (``disk_full``): parked on ``resource:disk`` until the
  disk watcher sees room.
"""

from __future__ import annotations

import asyncio
import contextlib
import errno
import os
import shutil
from pathlib import Path
from typing import Any
from uuid import UUID

from ...db.jobs import Job
from ...library_mgmt import convert as cv
from ...library_mgmt.ledger import advance, create_action
from ...library_mgmt.swap import SwapError, SwapVerifyFailed, swap_in
from ...library_mgmt.unicode_open import open_fallback
from ...library_mgmt.verify import ProbeInfo, VerifyResult, count_frames, verify_media
from ..context import StageContext, StageFailure
from ..enqueue import enqueue_job
from ..graph import EPIC47_PRIORITIES, Request, StageResult
from ..hashing import canonical_hash
from ..sql import uid
from . import thumb_small
from ._common import require_video
from .registry import StageImpl

__all__ = ["IMPL", "PLAN_CAP", "run"]

STEPS = ["plan", "convert", "verify", "swap"]
#: the dry run's plan lists at most this many files (the counts cover all)
PLAN_CAP = 2000
#: room the work folder needs beyond the input's size
WORK_SLACK = 256 * 1024 * 1024


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    if job.subject.kind == "library":
        return await _run_library(db, job, ctx)
    return await _run_video(db, job, ctx)


def _dry_run(job: Job) -> bool:
    return bool((job.payload or {}).get("dry_run"))


# ---------------------------------------------------------------- library


async def _plan_video(
    db: Any, ctx: StageContext, vid: UUID, path: str, allow_reencode: bool
) -> dict[str, Any]:
    local = ctx.local_path(path)
    entry: dict[str, Any] = {"video_id": str(vid), "path": path}
    if not await asyncio.to_thread(os.path.isfile, local):
        return {**entry, "action": "skip", "reason": "missing"}
    probe = await cv.stored_probe(db, vid)
    if probe is None:
        probe, why = await cv.probe_json(local)
        if probe is None:
            return {**entry, "action": "skip", "reason": "unreadable", "error": why[:200]}
    fs = await asyncio.to_thread(cv.faststart_of, local)
    plan = cv.plan_for(probe, path, allow_reencode=allow_reencode, faststart=fs)
    entry.update(plan.as_json())
    if plan.action != "skip":
        final = cv.final_path_for(path)
        entry["final_path"] = final
        if final != path and await asyncio.to_thread(os.path.lexists, ctx.local_path(final)):
            entry.update(action="skip", reason="target_exists")
    return entry


async def _run_library(db: Any, job: Job, ctx: StageContext) -> StageResult:
    library_id = job.subject.id
    dry = _dry_run(job)
    settings = await cv.convert_settings(db, library_id)
    rows = await db.fetch(
        "SELECT id, path FROM videos WHERE library_id = $1 AND deleted_at IS NULL"
        " AND superseded_by IS NULL AND path IS NOT NULL ORDER BY path",
        uid(library_id),
    )
    only = {str(v) for v in (job.payload or {}).get("video_ids") or []}
    if only:
        rows = [r for r in rows if str(r["id"]) in only]
    plan: list[dict[str, Any]] = []
    actions: dict[str, int] = {}
    reasons: dict[str, int] = {}
    enqueued = 0
    for n, r in enumerate(rows):
        await ctx.check_cancel()
        await ctx.progress("plan", n, len(rows), steps=["plan"])
        entry = await _plan_video(
            db, ctx, UUID(str(r["id"])), str(r["path"]), settings.allow_reencode
        )
        actions[entry["action"]] = actions.get(entry["action"], 0) + 1
        if entry.get("reason"):
            reasons[entry["reason"]] = reasons.get(entry["reason"], 0) + 1
        if len(plan) < PLAN_CAP:
            plan.append(entry)
        if entry["action"] != "skip" and not dry:
            res = await enqueue_job(
                db,
                subject_kind="video",
                subject_id=entry["video_id"],
                stage="convert_mp4",
                priority=EPIC47_PRIORITIES["convert_mp4"],
                reason={"why": "library conversion to MP4", "by": f"job:{job.id}"},
                triggered_by=job.id,
            )
            enqueued += int(res.enqueued)
    await ctx.progress("plan", len(rows), len(rows), steps=["plan"], force=True)
    summary: dict[str, Any] = {
        "dry_run": dry,
        "allow_reencode": settings.allow_reencode,
        "videos": len(rows),
        "actions": actions,
        "reasons": reasons,
        "enqueued": enqueued,
    }
    if dry:
        summary["plan"] = plan
        summary["plan_truncated"] = len(rows) > PLAN_CAP
    return StageResult(
        output_hash=canonical_hash(
            {"dry_run": dry, "plan": [(e["video_id"], e["action"], e.get("reason")) for e in plan]}
        ),
        outcome="unchanged" if dry or not enqueued else "changed",
        summary=summary,
    )


# ---------------------------------------------------------------- video


async def _verify_failed_before(db: Any, job: Job) -> bool:
    """This job's earlier attempt left a convert row that failed its verification."""
    n = await db.fetchval(
        "SELECT count(*) FROM file_actions WHERE job_id = $1 AND video_id = $2"
        " AND kind = 'convert' AND state = 'failed' AND verify IS NOT NULL",
        job.id,
        uid(job.subject.id),
    )
    return bool(n)


def _disk_full(where: str, why: str) -> StageFailure:
    # "disk is full" is the disk_full cause: parked on resource:disk (failure_classes)
    return StageFailure("disk_full", f"the work disk is full at {where}: {why}")


def _skip(video_id: UUID, content_hash: str, plan: cv.Plan, **extra: Any) -> StageResult:
    return StageResult(
        output_hash=canonical_hash(
            {"skip": plan.reason, "hash": content_hash, "action": plan.action}
        ),
        outcome="skipped_unchanged" if plan.reason == "already_mp4" else "unchanged",  # type: ignore[arg-type]
        summary={"video_id": str(video_id), **plan.as_json(), "result": "skipped", **extra},
    )


async def _run_video(db: Any, job: Job, ctx: StageContext) -> StageResult:
    v = await require_video(db, job.subject.id)
    row = await db.fetchrow(
        "SELECT content_hash, poster_path, sprite_path FROM videos WHERE id = $1", uid(v.id)
    )
    content_hash = str(row["content_hash"])
    settings = await cv.convert_settings(db, v.library_id)
    local = ctx.local_path(v.path)
    work = cv.work_dir_for(job.id)
    await ctx.progress("plan", 0, 1, steps=STEPS, force=True)
    try:
        try:
            opened = await asyncio.to_thread(open_fallback, local, str(work))
        except FileNotFoundError as exc:
            # file_missing: parked on the share when its root does not answer
            raise StageFailure("file_missing", f"file_missing: {exc}") from exc
        src = opened.path
        probe, why = await cv.probe_json(src)
        if probe is None:
            raise StageFailure(
                "ffmpeg_decode", f"the original cannot be read: {why[:300]}", retryable=False
            )
        retry_reencode = await _verify_failed_before(db, job)
        fs = await asyncio.to_thread(cv.faststart_of, src)
        plan = cv.plan_for(
            probe,
            v.path,
            allow_reencode=settings.allow_reencode,
            faststart=fs,
            force_reencode=retry_reencode,
        )
        final = cv.final_path_for(v.path)
        if plan.action != "skip" and final != v.path and os.path.lexists(ctx.local_path(final)):
            plan = cv.Plan("skip", "target_exists", subs=plan.subs)
        if plan.action == "skip" or _dry_run(job):
            return _skip(v.id, content_hash, plan, final_path=final, dry_run=_dry_run(job))

        # the work folder
        need = os.path.getsize(src) + WORK_SLACK
        try:
            free = await asyncio.to_thread(cv.free_bytes, str(work))
        except OSError as exc:
            raise _disk_full(str(work), str(exc)) from exc
        if free < need:
            raise _disk_full(str(work), f"{free} bytes free, {need} needed")
        out = work / (Path(local).stem + ".mp4")
        encoder = await _convert(ctx, src, str(out), plan, probe.duration or 0.0)

        # verify locally: the full decode runs once, here
        await ctx.progress("verify", 0, 1, steps=STEPS, force=True)
        orig_info = probe.info(src)
        orig_frames = await count_frames(src) if probe.video else None
        result = await verify_media(
            str(out), orig_info, mode=plan.mode, original_frames=orig_frames, nice=True
        )
        if not result.ok:
            action_id = await _record_failed_verify(db, job, v, content_hash, final, result)
            raise await _verify_failure(ctx, "locally", action_id, result)

        subs = await _extract_subs(src, plan, work)
        await ctx.check_cancel()
        await ctx.progress("swap", 0, 1, steps=STEPS, force=True)
        swapped = await _swap(db, job, ctx, v, str(out), final, orig_info, orig_frames, plan)
        new_hash = str(
            await db.fetchval("SELECT content_hash FROM videos WHERE id = $1", uid(v.id))
        )
        # the stored identity of the old file means nothing now (dedup reads it again)
        await db.execute("UPDATE videos SET full_hash = NULL WHERE id = $1", uid(v.id))
        if not plan.copy_audio:
            await db.execute("UPDATE audio_tracks SET codec = 'aac' WHERE video_id = $1", uid(v.id))
        written = await asyncio.to_thread(_place_subs, subs, ctx.local_path(final))
        requests = [Request("probe", "video", v.id, "converted to MP4")]
        rekey = cv.rekey_thumbnails(
            content_hash,
            new_hash,
            root=thumb_small.ROOT,
            poster_path=row["poster_path"],
            sprite_path=row["sprite_path"],
        )
        if rekey.moved:
            await db.execute(
                "UPDATE videos SET poster_path = $2, sprite_path = $3 WHERE id = $1",
                uid(v.id),
                rekey.poster_path,
                rekey.sprite_path,
            )
        elif not rekey.ok:
            await ctx.log("warn", f"thumbnail cache not moved, made again: {rekey.error}")
            requests += [
                Request("thumbnail", "video", v.id, "converted to MP4, thumbnails not moved"),
                Request("thumb_small", "video", v.id, "converted to MP4, thumbnails not moved"),
            ]
        await ctx.progress("swap", 1, 1, steps=STEPS, force=True)
        return StageResult(
            output_hash=canonical_hash({"action": plan.action, "hash": new_hash}),
            outcome="changed",
            changed_subjects=(("video", v.id),),
            requests=tuple(requests),
            summary={
                "video_id": str(v.id),
                **plan.as_json(),
                "result": "converted",
                "encoder": encoder,
                "retry_reencode": retry_reencode,
                "final_path": swapped.final_path,
                "trash_path": swapped.trash_path,
                "file_action": str(swapped.action.id),
                "srt": written,
                "thumbnails": "moved" if rekey.moved else ("kept" if rekey.ok else "remade"),
                "verify": result.measured,
            },
        )
    finally:
        await asyncio.to_thread(shutil.rmtree, work, True)


async def _convert(
    ctx: StageContext, src: str, out: str, plan: cv.Plan, duration: float
) -> str | None:
    """ffmpeg into the work folder; returns the video encoder used (None: copied)."""
    total = max(1, round(duration))

    async def on_progress(done_s: float) -> None:
        await ctx.progress("convert", min(total, round(done_s)), total, steps=STEPS)

    encoders: list[list[str]] = await cv.h264_encoders() if plan.action == "reencode" else [[]]
    last: cv.FfmpegFailed | None = None
    for enc in encoders:
        args = cv.ffmpeg_args(src, out, plan, video_encoder=enc)
        try:
            await ctx.progress("convert", 0, total, steps=STEPS, force=True)
            await cv.run_ffmpeg(args, on_progress=on_progress, check_cancel=ctx.check_cancel)
        except cv.FfmpegFailed as exc:
            _unlink(out)
            err = exc.stderr
            if "no space left on device" in err.lower():
                raise _disk_full(out, err.strip().splitlines()[-1][:300]) from exc
            if cv.input_damaged(err):
                raise StageFailure(
                    "ffmpeg_decode",
                    f"the original is damaged: {err.strip()[-300:]}",
                    retryable=False,
                ) from exc
            last = exc
            if enc and enc is not encoders[-1]:
                await ctx.log("warn", f"{enc[1]} failed, trying {encoders[-1][1]}")
            continue
        except OSError as exc:
            _unlink(out)
            if exc.errno == errno.ENOSPC:
                raise _disk_full(out, str(exc)) from exc
            raise
        except BaseException:
            _unlink(out)  # cancelled: no partial file stays
            raise
        return enc[1] if enc else None
    assert last is not None
    raise StageFailure("convert_ffmpeg", str(last)[:500])


def _unlink(path: str) -> None:
    with contextlib.suppress(FileNotFoundError):
        os.remove(path)


async def _verify_failure(
    ctx: StageContext, where: str, action_id: Any, result: VerifyResult
) -> StageFailure:
    """``convert_verify`` (retried once). The reasons go to the job log and the ledger
    row, not the message: a reason such as "decode errors: invalid data found" would make
    the cause ``ffmpeg_decode`` (failed at once, causes.py), and this is the new file's
    fault, not the original's."""
    await ctx.log("warn", f"the MP4 failed its check {where}", {"reasons": result.reasons})
    return StageFailure(
        "convert_verify",
        f"the MP4 failed its check {where}: {len(result.reasons)} problem(s),"
        f" see file action {action_id}",
    )


async def _record_failed_verify(
    db: Any, job: Job, v: Any, content_hash: str, final: str, result: VerifyResult
) -> Any:
    """A ``failed`` ledger row with what the check measured (the failures page shows it;
    the retry reads it to re-encode)."""
    a = await create_action(
        db,
        library_id=v.library_id,
        kind="convert",
        video_id=v.id,
        job_id=job.id,
        old_path=v.path,
        new_path=final,
        old_hash=content_hash,
        old_size=v.size_bytes,
    )
    await advance(
        db,
        a.id,
        "failed",
        verify=result.as_json(),
        error="verification failed (local): " + "; ".join(result.reasons),
    )
    return a.id


async def _extract_subs(src: str, plan: cv.Plan, work: Path) -> list[tuple[cv.SubStream, str]]:
    out: list[tuple[cv.SubStream, str]] = []
    for s in plan.subs:
        dest = work / f"sub{s.ordinal}.{s.language}.srt"
        if await cv.extract_srt(src, s, str(dest)):
            out.append((s, str(dest)))
    return out


def _place_subs(subs: list[tuple[cv.SubStream, str]], final_local: str) -> list[str]:
    """Copy the .srt files beside the MP4 (never over an existing file)."""
    written: list[str] = []
    for s, path in subs:
        dest = cv.sidecar_name(final_local, s, os.path.lexists)
        if dest is None:
            continue
        tmp = os.path.join(os.path.dirname(dest), "." + os.path.basename(dest) + ".maktaba-new")
        try:
            shutil.copyfile(path, tmp)
            os.rename(tmp, dest)
            written.append(os.path.basename(dest))
        except OSError:
            _unlink(tmp)
    return written


async def _swap(
    db: Any,
    job: Job,
    ctx: StageContext,
    v: Any,
    out: str,
    final: str,
    orig_info: ProbeInfo,
    orig_frames: int | None,
    plan: cv.Plan,
) -> Any:
    async def on_share(staged: str) -> VerifyResult:
        # cheap: the size was checked by the swap; ffprobe's streams, duration, faststart
        return await verify_media(
            staged,
            orig_info,
            mode=plan.mode,
            original_frames=orig_frames,
            full_decode=False,
            count=False,
        )

    try:
        return await swap_in(
            db,
            library_id=v.library_id,
            kind="convert",
            source=out,
            final_path=final,
            verify=on_share,
            video_id=v.id,
            job_id=job.id,
            to_local=ctx.local_path,
        )
    except SwapVerifyFailed as exc:
        raise await _verify_failure(
            ctx, "on the share", exc.action.id if exc.action else None, exc.result
        ) from exc
    except SwapError as exc:
        if not os.path.lexists(ctx.local_path(v.path)):
            raise StageFailure("file_missing", f"file_missing: {exc}") from exc
        if "no space left on device" in str(exc).lower():
            raise StageFailure("disk_full", f"the share is full: {exc}") from exc
        raise StageFailure("convert_swap", str(exc)[:500]) from exc


IMPL = StageImpl(run=run)
