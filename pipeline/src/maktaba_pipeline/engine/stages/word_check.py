"""``word_check`` (Epic 52, stories 52.6, 52.14, 52.15): a transcript's suspect words,
saved for a home admin to review.

The component ``review.words`` owns it (Epic 51: off, held and caught up like any stage).
On a video it checks the active transcript (:mod:`maktaba_pipeline.word_review.signals`)
and writes its flags (:mod:`maktaba_pipeline.word_review.store`); CPU only, no model, no
LLM, so it runs again whenever a pack, the vocabulary or the library's settings change.
Its input fingerprint is the transcript and its revision, the packs it reads (code
version and data digest), the library's vocabulary and settings, and
:data:`WORD_CHECK_VERSION`: the same input is ``unchanged``.

On a library it plans (story 52.6 §7): the **backfill** enqueues every active transcript
of the library that was never checked, at :data:`BACKFILL_PRIORITY`, the lowest of any
stage; with ``word_review.backfill`` off it cancels the library's pending backfill jobs
instead. A **recheck** (the library's settings changed) enqueues them all; each one's
fingerprint decides whether there is work. A backfill job never runs while ``transcribe``
has pending or running jobs: it is given back (:class:`JobDeferred`, no attempt used).
"""

from __future__ import annotations

import time
from typing import Any

from ...db.jobs import Job
from ...language_packs import registry as packs
from ...language_packs.registry import DEFAULT_FOR_SCRIPT, script_of_language
from ...log import get_logger
from ...word_review import gazetteer, signals, vocabulary
from ...word_review import store as flag_store
from ...word_review.settings import Effective, WordReviewConfig, effective
from ..context import JobDeferred, StageContext
from ..graph import WORD_CHECK_VERSION, StageResult, Subject
from ..hashing import canonical_hash
from ..sql import is_pg, loads, now, to_ts, uid
from ._analysis import library_setting
from .registry import StageImpl
from .second_listen import stored_words

__all__ = [
    "BACKFILL",
    "BACKFILL_PRIORITY",
    "IMPL",
    "WORD_CHECK_VERSION",
    "input_fingerprint",
    "run",
]

_log = get_logger()

#: the backfill's priority: behind everything else (lower runs first; normal is 100)
BACKFILL_PRIORITY = 900
#: a recheck after a settings change: behind new work, before the backfill
RECHECK_PRIORITY = 600
#: the payload key of a backfill job
BACKFILL = "backfill"
#: how long a backfill job waits for transcription to be idle, each time (s)
BUSY_DEFER_S = 300.0
#: how long a job waits for runtime.lang's pack to land on this machine (s)
RUNTIME_DEFER_S = 120.0
#: how often a worker that serves the stage plans the backfill (s)
PLAN_EVERY_S = 1800.0
#: how many transcripts one planning job enqueues at most
PLAN_BATCH = 5000


async def home_config(db: Any) -> WordReviewConfig:
    from ... import rules

    try:
        cfg, _changes, _bad = await rules.read(db, "word_review")
    except Exception:  # noqa: BLE001 - the defaults
        return WordReviewConfig()
    return cfg if isinstance(cfg, WordReviewConfig) else WordReviewConfig()


async def library_settings(db: Any, library_id: Any) -> Effective:
    home = await home_config(db)
    try:
        own = await library_setting(db, library_id, "word_review")
    except Exception:  # noqa: BLE001 - a library row gone: the home's
        own = None
    return effective(home, own)


async def _transcript(db: Any, video_id: Any) -> Any:
    return await db.fetchrow(
        "SELECT t.id, t.video_id, t.audio_track_id, t.language, t.model, t.metadata,"
        " COALESCE(t.revision, 0) AS revision, v.library_id, v.duration_sec AS duration_s"
        " FROM transcripts t JOIN videos v ON v.id = t.video_id"
        " WHERE t.video_id = $1 AND t.is_active",
        uid(video_id),
    )


def _packs_for(reg: packs.Registry, language: str) -> dict[str, str]:
    """The packs a transcript in ``language`` reads: its own (or the fallback), and the
    default pack of every other script (code-switching)."""
    own_script = script_of_language(language)
    out = {"own": reg.version_of(reg.for_script(own_script, language))}
    for script in sorted(DEFAULT_FOR_SCRIPT):
        if script != own_script:
            out[script] = reg.version_of(reg.for_script(script, language))
    return out


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    if subject.kind == "library":
        s = await library_settings(db, subject.id)
        n = await db.fetchval(
            "SELECT COUNT(*) FROM transcripts t JOIN videos v ON v.id = t.video_id"
            " WHERE v.library_id = $1 AND t.is_active",
            uid(subject.id),
        )
        return canonical_hash({"plan": int(n or 0), "backfill": s.backfill,
                               "s": s.fingerprint(), "v": WORD_CHECK_VERSION})  # fmt: skip
    row = await _transcript(db, subject.id)
    if row is None:
        return None
    reg = await packs.load(db)
    s = await library_settings(db, row["library_id"])
    return canonical_hash(
        {
            "transcript": str(row["id"]),
            "revision": int(row["revision"] or 0),
            "packs": _packs_for(reg, str(row["language"] or "")),
            "vocabulary": await vocabulary.version(db, row["library_id"]),
            "settings": s.fingerprint(),
            "v": WORD_CHECK_VERSION,
        }
    )


async def transcription_busy(db: Any) -> bool:
    """``transcribe`` has pending or running jobs (the backfill waits)."""
    got = await db.fetchval(
        "SELECT 1 FROM processing_jobs WHERE stage = 'transcribe'"
        " AND state IN ('pending', 'claimed', 'running', 'resuming') LIMIT 1"
    )
    return bool(got)


def _sentences(rows: list[Any]) -> list[signals.SentenceIn]:
    out = []
    for r in rows:
        meta = loads(r["metadata"]) or {}
        meta = meta if isinstance(meta, dict) else {}
        alt = meta.get("alt") if isinstance(meta.get("alt"), list) else None
        out.append(
            signals.SentenceIn(
                seq=int(r["seq"]),
                start=float(r["start_sec"]),
                end=float(r["end_sec"]),
                text=str(r["text"] or ""),
                words=stored_words(r, meta),
                alt=alt,
                no_speech=float(meta.get("no_speech", 0.0) or 0.0),
            )
        )
    return out


async def check_video(db: Any, row: Any, settings: Effective) -> tuple[list[signals.Flag], Any]:
    """The flags of the video's active transcript ``row`` (nothing written)."""
    reg = await packs.load(db)
    segs = list(
        await db.fetch(
            "SELECT seq, start_sec, end_sec, text, metadata, word_data FROM transcript_segments"
            " WHERE transcript_id = $1 ORDER BY seq",
            row["id"],
        )
    )
    sentences = _sentences(segs)
    vocab = await vocabulary.load(db, row["library_id"])
    language = str(row["language"] or "")
    names = await gazetteer.load_names(db, row["library_id"], row["video_id"])
    names.extend(vocab.all_names())

    def split(name: str) -> list[tuple[str, str, str]]:
        out = []
        from ...language_packs.scripts import runs as script_runs

        for run in script_runs(name):
            pack = reg.for_script(run.script, language)
            for t in pack.tokens(name[run.start : run.end]):
                if t.kind == "word":
                    out.append((pack.code, t.text, pack.key(t.text)))
        return out

    meta = loads(row["metadata"]) or {}
    audio = float((meta.get("audio_sec") if isinstance(meta, dict) else None) or 0.0)
    if audio <= 0 and sentences:
        audio = max(s.end for s in sentences)
    if audio <= 0:
        audio = float(row["duration_s"] or 0.0)
    inp = signals.Inputs(
        sentences=sentences,
        language=language,
        settings=settings,
        pack_for=reg.for_script,
        model=str(row["model"] or ""),
        audio_sec=audio,
        names=gazetteer.build(names, split),
        vocabulary=vocab,
    )
    return signals.check(inp), vocab


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    if job.subject.kind == "library":
        return await _plan(db, job)
    payload = job.payload or {}
    row = await _transcript(db, job.subject.id)
    if row is None:
        return StageResult(output_hash=canonical_hash("no transcript"), outcome="unchanged",
                           summary={"word_check": "no transcript"})  # fmt: skip
    settings = await library_settings(db, row["library_id"])
    if not settings.enabled or (payload.get(BACKFILL) and not settings.backfill):
        return StageResult(output_hash=canonical_hash("off"), outcome="unchanged",
                           summary={"word_check": "off for this library"})  # fmt: skip
    if payload.get(BACKFILL) and await transcription_busy(db):
        raise JobDeferred("waiting: transcription busy", BUSY_DEFER_S)
    reg = await packs.load(db)
    if not reg.runtime:
        raise JobDeferred("waiting: the runtime.lang pack is not on this machine yet",
                          RUNTIME_DEFER_S)  # fmt: skip
    t0 = time.process_time()
    flags, vocab = await check_video(db, row, settings)
    cpu = time.process_time() - t0
    done = await flag_store.write(db, row, flags, WORD_CHECK_VERSION, vocab)
    out = flag_store.flags_hash(flags)
    if done.get("inactive"):
        return StageResult(output_hash=out, outcome="unchanged",
                           summary={"word_check": "transcript replaced meanwhile"})  # fmt: skip
    opened = sum(1 for f in flags if f.status == "open" and f.kind == "word")
    return StageResult(
        output_hash=out,
        summary={
            "transcript_id": str(row["id"]),
            "flags": len(flags),
            "open": opened,
            "over_budget": sum(1 for f in flags if f.status == "skipped"),
            "poor": any(f.kind == "transcript" for f in flags),
            "cpu_s": round(cpu, 3),
            **{k: v for k, v in done.items() if v},
        },
    )


async def _plan(db: Any, job: Job) -> StageResult:
    """The library's backfill (or recheck) planning job."""
    from ..enqueue import enqueue_job

    lib = job.subject.id
    s = await library_settings(db, lib)
    payload = job.payload or {}
    recheck = bool(payload.get("recheck"))
    if not s.backfill and not recheck:
        n = await cancel_backfill(db, lib)
        return StageResult(output_hash=canonical_hash(["cancelled", n]),
                           summary={"backfill": "off", "cancelled": n})  # fmt: skip
    if not s.enabled:
        return StageResult(output_hash=canonical_hash("off"), summary={"word_check": "off"})
    if recheck:
        sql = (
            "SELECT t.video_id FROM transcripts t JOIN videos v ON v.id = t.video_id"
            " WHERE v.library_id = $1 AND t.is_active ORDER BY v.created_at DESC LIMIT $2"
        )
    else:
        sql = (
            "SELECT t.video_id FROM transcripts t JOIN videos v ON v.id = t.video_id"
            " WHERE v.library_id = $1 AND t.is_active"
            "   AND NOT EXISTS (SELECT 1 FROM processing_done_subjects d"
            "                   WHERE d.library_id = v.library_id AND d.stage = 'word_check'"
            "                     AND d.subject_id = t.video_id)"
            "   AND NOT EXISTS (SELECT 1 FROM processing_jobs j"
            "                   WHERE j.stage = 'word_check' AND j.subject_id = t.video_id"
            "                     AND j.state IN ('pending', 'claimed', 'running', 'paused',"
            "                                     'resuming'))"
            " ORDER BY v.created_at DESC LIMIT $2"
        )
    rows = await db.fetch(sql, uid(lib), PLAN_BATCH)
    n = 0
    for r in rows:
        await enqueue_job(
            db,
            subject_kind="video",
            subject_id=r["video_id"],
            stage="word_check",
            priority=RECHECK_PRIORITY if recheck else BACKFILL_PRIORITY,
            reason={
                "why": "word_review_recheck" if recheck else "word_review_backfill",
                "by": "system",
            },  # fmt: skip
            payload=None if recheck else {BACKFILL: True},
        )
        n += 1
    return StageResult(
        output_hash=canonical_hash([("recheck" if recheck else "backfill"), n, len(rows)]),
        summary={"recheck" if recheck else "backfill": n},
    )


async def cancel_backfill(db: Any, library_id: Any) -> int:
    """The library's backfill jobs not started yet, cancelled."""
    is_backfill = (
        "(payload ->> 'backfill') = 'true'"
        if is_pg(db)
        else "json_extract(payload, '$.backfill') = 1"
    )
    rows = await db.fetch(
        "SELECT id FROM processing_jobs WHERE stage = 'word_check' AND library_id = $1"
        f" AND state IN ('pending', 'paused') AND {is_backfill}",
        uid(library_id),
    )
    for r in rows:
        await db.execute(
            "UPDATE processing_jobs SET state = 'cancelled', finished_at = $2"
            " WHERE id = $1 AND state IN ('pending', 'paused')",
            r["id"],
            to_ts(db, now()),
        )
    return len(rows)


_LAST_PLAN: list[float] = []


async def refresh(db: Any, serving: bool) -> None:
    """A worker that serves the stage plans every library's backfill now and then (the
    planning job coalesces: an unchanged library plans nothing)."""
    if not serving:
        return
    t = time.monotonic()
    if _LAST_PLAN and t - _LAST_PLAN[0] < PLAN_EVERY_S:
        return
    _LAST_PLAN[:] = [t]
    from ..enqueue import enqueue_job

    try:
        libs = await db.fetch("SELECT id FROM libraries")
        for r in libs:
            await enqueue_job(
                db, subject_kind="library", subject_id=r["id"], stage="word_check",
                priority=BACKFILL_PRIORITY,
                reason={"why": "word_review_backfill", "by": "system"},
            )  # fmt: skip
    except Exception as exc:  # noqa: BLE001 - the next refresh plans again
        _log.warning("word_check_plan_failed", error=f"{type(exc).__name__}: {exc}"[:200])


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint, refresh=refresh)
