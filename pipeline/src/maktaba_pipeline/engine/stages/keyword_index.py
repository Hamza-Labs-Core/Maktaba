"""``keyword_index`` (Epic 49, story 49.6): a video's keyword search document.

On a video it builds the document from the video's active transcript (the first by id,
as ``content_screen`` reads it): the lines in order, the named speakers of those lines,
and the title it shows (its manual or found display title, else its metadata title, with
its file name). It upserts the row of ``video_search_docs`` (slot 0232) with the
transcript, its revision and :data:`KEYWORD_INDEX_VERSION` (slot 0233). On SQLite it also
writes the document's FTS5 row, normalised (:mod:`maktaba_pipeline.search.textnorm`);
Postgres's BM25 indexes normalise by themselves. A video with no active transcript (or
deleted) loses its document. The row is its own state: the stage has no input
fingerprint, and a run with nothing to change says ``unchanged``.

It runs after ``second_listen`` (the transcript's edge), when the API changes a title or
a speaker's name, and through the library's plan. On a library it plans: the videos
whose document is missing, older than the stage's version, or built from another
transcript, revision or title, and the documents of videos with no transcript left.
Each video is its own job, at :data:`BACKFILL_PRIORITY`, behind all other work, so a
version bump rebuilds every document one job at a time while search goes on. Until a
video has a document, keyword search finds it by its lines (story 49.7).
"""

from __future__ import annotations

import time
from typing import Any

from ...db.jobs import Job
from ...log import get_logger
from ...search.textnorm import normalize
from ..context import StageContext
from ..graph import KEYWORD_INDEX_VERSION, StageResult
from ..hashing import canonical_hash
from ..sql import is_pg, now, to_ts, uid
from .registry import StageImpl

__all__ = [
    "BACKFILL_PRIORITY",
    "IMPL",
    "KEYWORD_INDEX_VERSION",
    "PLAN_BATCH",
    "build",
    "plan_library",
    "refresh",
    "run",
    "shown_title",
]

_log = get_logger()

#: the planner's priority: behind everything else (lower runs first; normal is 100)
BACKFILL_PRIORITY = 900
#: how often a worker that serves the stage plans every library (s)
PLAN_EVERY_S = 1800.0
#: how many videos one planning job enqueues at most
PLAN_BATCH = 5000
#: how many of a library's videos the planner reads at a time
PLAN_PAGE = 1000

_LIVE = ("pending", "claimed", "running", "paused", "resuming")
_LAST_PLAN: list[float] = []

#: the characters an extension is made of (api/internal/videotitle's rule)
_EXT = set("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz")


def strip_ext(filename: str) -> str:
    """``filename`` without its final extension (``videotitle.StripExt``)."""
    r = filename.rstrip("".join(_EXT))
    if 1 < len(r) < len(filename) and r.endswith("."):
        return r[:-1]
    return filename


def shown_title(row: Any) -> str:
    """The title a document carries: the one the video shows (its manual display title,
    else the one found for it, else its metadata title) and its file name without the
    extension, once each."""
    parts: list[str] = []
    for key in ("display_title_manual", "display_title", "title"):
        v = str(row[key] or "").strip()
        if v:
            parts.append(v)
            break
    name = strip_ext(str(row["filename"] or "")).strip()
    if name and name not in parts:
        parts.append(name)
    return "\n".join(parts)


_VIDEO = (
    "SELECT v.id, v.library_id, v.title, v.display_title, v.display_title_manual, v.filename,"
    " v.deleted_at FROM videos v WHERE v.id = $1"
)
_TRANSCRIPT = (
    "SELECT t.id, t.language, COALESCE(t.revision, 0) AS revision FROM transcripts t"
    " WHERE t.video_id = $1 AND t.is_active ORDER BY t.id LIMIT 1"
)


async def build(db: Any, video_id: Any) -> dict[str, Any] | None:
    """The video's document as a run would write it, or None (no active transcript, or
    the video is gone)."""
    v = await db.fetchrow(_VIDEO, uid(video_id))
    if v is None or v["deleted_at"] is not None:
        return None
    t = await db.fetchrow(_TRANSCRIPT, uid(video_id))
    if t is None:
        return None
    lines = await db.fetch(
        "SELECT g.text FROM transcript_segments g WHERE g.transcript_id = $1 ORDER BY g.seq",
        t["id"],
    )
    body = "\n".join(s for s in (str(r["text"] or "").strip() for r in lines) if s)
    names = await db.fetch(
        "SELECT DISTINCT s.name FROM segment_speakers ss"
        " JOIN speakers s ON s.id = ss.speaker_id"
        " JOIN transcript_segments g ON g.id = ss.segment_id"
        " WHERE g.transcript_id = $1 AND s.unknown_index IS NULL",
        t["id"],
    )
    speakers = "\n".join(
        sorted({str(r["name"]).strip() for r in names if str(r["name"] or "").strip()})
    )
    return {
        "video_id": v["id"],
        "library_id": v["library_id"],
        "language": t["language"],
        "title": shown_title(v),
        "speakers": speakers,
        "body": body,
        "transcript_id": t["id"],
        "transcript_revision": int(t["revision"] or 0),
        "doc_version": KEYWORD_INDEX_VERSION,
    }


_FIELDS = ("library_id", "language", "title", "speakers", "body", "transcript_id",
           "transcript_revision", "doc_version")  # fmt: skip


async def _current(db: Any, video_id: Any) -> Any:
    return await db.fetchrow(
        "SELECT " + ", ".join(_FIELDS) + " FROM video_search_docs WHERE video_id = $1",
        uid(video_id),
    )


def _same(row: Any, doc: dict[str, Any]) -> bool:
    return row is not None and all(str(row[f]) == str(doc[f]) for f in _FIELDS)


async def _write(db: Any, doc: dict[str, Any]) -> None:
    """The document upserted, and on SQLite its FTS5 row, in one transaction: a run cut
    short between them would leave a current row whose FTS5 row a retry never writes (the
    run finds the row current)."""
    cols = ("video_id", *_FIELDS, "built_at")
    vals = [doc["video_id"], *(doc[f] for f in _FIELDS), to_ts(db, now())]
    marks = ", ".join(f"${i}" for i in range(1, len(cols) + 1))
    sets = ", ".join(f"{c} = excluded.{c}" for c in cols[1:])
    async with db.transaction():
        await db.execute(
            f"INSERT INTO video_search_docs ({', '.join(cols)}) VALUES ({marks})"
            f" ON CONFLICT (video_id) DO UPDATE SET {sets}",
            *vals,
        )
        if not is_pg(db):
            await db.execute(
                "DELETE FROM video_search_docs_fts WHERE video_id = $1", doc["video_id"]
            )
            await db.execute(
                "INSERT INTO video_search_docs_fts (video_id, title, speakers, body)"
                " VALUES ($1, $2, $3, $4)",
                doc["video_id"], normalize(doc["title"]), normalize(doc["speakers"]),
                normalize(doc["body"]),
            )  # fmt: skip


async def _drop(db: Any, video_id: Any) -> bool:
    """The video's document deleted (its FTS5 row with it: slot 0232's trigger)."""
    row = await db.fetchrow(
        "SELECT video_id FROM video_search_docs WHERE video_id = $1", uid(video_id)
    )
    if row is None:
        return False
    await db.execute("DELETE FROM video_search_docs WHERE video_id = $1", uid(video_id))
    return True


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    if job.subject.kind == "library":
        return await plan_library(db, job.subject.id)
    doc = await build(db, job.subject.id)
    if doc is None:
        dropped = await _drop(db, job.subject.id)
        return StageResult(
            output_hash=canonical_hash("no document"),
            outcome="changed" if dropped else "unchanged",
            summary={"keyword_index": "removed" if dropped else "no transcript"},
        )
    out = canonical_hash({f: str(doc[f]) for f in _FIELDS})
    if _same(await _current(db, job.subject.id), doc):
        return StageResult(
            output_hash=out, outcome="unchanged", summary={"keyword_index": "current"}
        )
    await _write(db, doc)
    return StageResult(
        output_hash=out,
        summary={
            "keyword_index": "written",
            "transcript_id": str(doc["transcript_id"]),
            "chars": len(doc["body"]),
            "speakers": len(doc["speakers"].splitlines()),
        },
    )


#: one page of a library's videos ($1 the library, $2 the page, $3 its start)
_PAGE = (
    "SELECT v.id FROM videos v WHERE v.library_id = $1"
    " ORDER BY v.created_at DESC, v.id LIMIT $2 OFFSET $3"
)


def _stale(v: Any, t: Any, d: Any) -> bool:
    """Whether the video's document is not what a run would leave now (no line or speaker
    is read: a speaker's new name queues its videos from the API)."""
    if v["deleted_at"] is not None or t is None:
        return d is not None
    if d is None:
        return True
    return (
        int(d["doc_version"] or 0) < KEYWORD_INDEX_VERSION
        or str(d["transcript_id"]) != str(t["id"])
        or int(d["transcript_revision"] or 0) != int(t["revision"] or 0)
        or str(d["title"]) != shown_title(v)
    )


async def plan_library(db: Any, library_id: Any) -> StageResult:
    """Enqueue the library's videos whose document is stale (:func:`_stale`), one job a
    video, behind all other work."""
    from ..enqueue import enqueue_job

    queued = offset = 0
    while queued < PLAN_BATCH:
        page = (uid(library_id), PLAN_PAGE, offset)
        videos = await db.fetch(
            "SELECT v.id, v.title, v.display_title, v.display_title_manual, v.filename,"
            f" v.deleted_at FROM videos v WHERE v.id IN ({_PAGE}) ORDER BY v.created_at DESC, v.id",
            *page,
        )
        if not videos:
            break
        offset += len(videos)
        transcripts: dict[str, Any] = {}
        for r in await db.fetch(
            "SELECT t.video_id, t.id, COALESCE(t.revision, 0) AS revision FROM transcripts t"
            f" WHERE t.is_active AND t.video_id IN ({_PAGE}) ORDER BY t.id",
            *page,
        ):
            transcripts.setdefault(str(r["video_id"]), r)
        docs = {
            str(r["video_id"]): r
            for r in await db.fetch(
                "SELECT d.video_id, d.title, d.transcript_id, d.transcript_revision, d.doc_version"
                f" FROM video_search_docs d WHERE d.video_id IN ({_PAGE})",
                *page,
            )
        }
        live = {
            str(r["subject_id"])
            for r in await db.fetch(
                "SELECT j.subject_id, j.state FROM processing_jobs j"
                " WHERE j.stage = 'keyword_index' AND j.subject_kind = 'video'"
                f"   AND j.subject_id IN ({_PAGE})",
                *page,
            )
            if r["state"] in _LIVE
        }
        for v in videos:
            vid = str(v["id"])
            if vid in live or not _stale(v, transcripts.get(vid), docs.get(vid)):
                continue
            await enqueue_job(
                db,
                subject_kind="video",
                subject_id=v["id"],
                stage="keyword_index",
                priority=BACKFILL_PRIORITY,
                reason={"why": "keyword_index_plan", "by": "system"},
            )
            queued += 1
            if queued >= PLAN_BATCH:
                break
    return StageResult(
        output_hash=canonical_hash(["plan", queued, offset]),
        outcome="changed" if queued else "unchanged",
        summary={"planned": queued, "videos": offset},
    )


async def refresh(db: Any, serving: bool) -> None:
    """A worker that serves the stage plans every library now and then (the planning job
    coalesces, and a library with nothing stale queues nothing)."""
    if not serving:
        return
    t = time.monotonic()
    if _LAST_PLAN and t - _LAST_PLAN[0] < PLAN_EVERY_S:
        return
    from ..enqueue import enqueue_job

    try:
        libs = await db.fetch("SELECT id FROM libraries")
    except Exception as exc:  # noqa: BLE001 - the next refresh plans again
        _log.warning("keyword_index_plan_failed", error=f"{type(exc).__name__}: {exc}"[:200])
        return
    failed = 0
    for r in libs:
        try:
            await enqueue_job(
                db, subject_kind="library", subject_id=r["id"], stage="keyword_index",
                priority=BACKFILL_PRIORITY,
                reason={"why": "keyword_index_plan", "by": "system"},
            )  # fmt: skip
        except Exception as exc:  # noqa: BLE001 - the other libraries are planned still
            failed += 1
            _log.warning("keyword_index_plan_failed", error=f"{type(exc).__name__}: {exc}"[:200])
    if not failed:  # a round that failed is tried again at the next refresh
        _LAST_PLAN[:] = [t]


IMPL = StageImpl(run=run, refresh=refresh)
