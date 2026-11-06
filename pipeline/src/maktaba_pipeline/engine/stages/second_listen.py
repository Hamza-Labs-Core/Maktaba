"""``second_listen``: Cohere Transcribe Arabic hears a video's transcript again and corrects
Whisper's words (:mod:`maktaba_pipeline.stt.refine`), as a stage of its own after
``transcribe``.

Whisper's transcript is stored with its words (each sentence's word timings, which the
alignment needs). This stage reads it and the file's audio (decoded off the share again),
runs the second listen and stores the corrected transcript as the video's active one; the
stages that read a transcript (subtitles, the search index, tags) follow this stage, not
``transcribe``, so they run once, on the corrected words. With the second listen off
(``MAKTABA_STT_REFINE=0``) it passes Whisper's transcript on as it is.

Where: any machine that runs the second listen (:func:`.transcribe.second_listen_problem`):
Apple Silicon, or an NVIDIA GPU that holds Cohere beside Whisper or, where it holds one at
a time (an 8 GB card on Windows), taking turns with it (:mod:`maktaba_pipeline.stt.gpu_turns`).
A machine that cannot leaves the stage to those that can, unless the home's rules let it
pass transcripts on with Whisper's words (``rules.transcription.without_second_listen``):
then it gives a job back for :data:`PASS_ON_AFTER_S` first, for a machine that runs it,
and then records the transcript as made without it. A machine that runs the second
listen queues those again (:func:`.transcribe.relisten`, with its tries and backoff).

The input fingerprint is Whisper's transcript (the one this stage corrects: kept in the
corrected transcript's ``metadata.refine.whisper``), the model and the version, so a
transcribe that changes nothing queues nothing here, and a corrected transcript is not
heard again.
"""

from __future__ import annotations

import asyncio
import math
from typing import Any
from uuid import uuid4

from ...db.jobs import Job
from ...log import get_logger
from ...stt.settings import SAMPLE_RATE, Sentence, Transcript, Word
from ...stt.word_data import unpack
from ..context import JobDeferred, StageContext, StageFailure
from ..graph import SECOND_LISTEN_VERSION, StageResult, Subject
from ..hashing import canonical_hash
from ..sql import dumps, is_pg, loads, now, to_ts, uid
from . import transcribe as tr
from ._common import require_video
from .registry import StageImpl

__all__ = ["IMPL", "PASS_ON_AFTER_S", "input_fingerprint", "run", "unavailable"]

_log = get_logger()

#: a machine without the second listen, which the rules let pass transcripts on, gives a
#: job back until it is this old (s), for a machine that runs it
PASS_ON_AFTER_S = 900.0
#: ...for this long each time
PASS_ON_DEFER_S = 300.0


def unavailable() -> str | None:
    """Why this worker does not claim the stage, or None: with the second listen off every
    worker passes transcripts on; on, a worker that cannot run it leaves the stage to the
    machines that do (:func:`.transcribe.waits_for_second_listen`), unless the rules let it
    pass them on with Whisper's words."""
    if tr.refine_model() is None:
        return None
    return tr.waits_for_second_listen()


async def _active(db: Any, video_id: Any) -> Any:
    return await db.fetchrow(
        "SELECT id, audio_track_id, language, detected_language, backend, model,"
        " backend_version, word_level, diarized, metadata"
        " FROM transcripts WHERE video_id = $1 AND is_active",
        uid(video_id),
    )


def _whisper_id(row: Any) -> str:
    """The Whisper transcript ``row`` is, or was corrected from."""
    meta = loads(row["metadata"]) or {}
    refine = meta.get("refine") if isinstance(meta, dict) else None
    source = refine.get("whisper") if isinstance(refine, dict) else None
    return str(source or row["id"])


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    """DB only: Whisper's transcript of the video, the second listen's model (None: off)
    and this stage's version. None without a transcript."""
    row = await _active(db, subject.id)
    if row is None:
        return None
    return canonical_hash(
        {"whisper": _whisper_id(row), "refine": tr.refine_model(), "v": SECOND_LISTEN_VERSION}
    )


async def _segments(db: Any, transcript_id: Any) -> list[Any]:
    return list(
        await db.fetch(
            "SELECT seq, start_sec, end_sec, text, confidence, metadata, word_data"
            " FROM transcript_segments WHERE transcript_id = $1 ORDER BY seq",
            uid(transcript_id),
        )
    )


def _sentences(rows: list[Any]) -> tuple[list[Sentence], bool]:
    """The stored sentences, and whether every one with text has its words."""
    out: list[Sentence] = []
    words_all = True
    for r in rows:
        meta = loads(r["metadata"]) or {}
        timed = stored_words(r, meta)
        conf = float(r["confidence"] or 0.0)
        if timed is None and str(r["text"] or "").strip():
            words_all = False
        out.append(
            Sentence(
                start=float(r["start_sec"]),
                end=float(r["end_sec"]),
                text=str(r["text"] or ""),
                avg_logprob=math.log(conf) if conf > 0 else -10.0,
                no_speech_prob=float(meta.get("no_speech", 0.0)) if isinstance(meta, dict) else 0.0,
                words=timed,
            )
        )
    return out, words_all


def stored_words(row: Any, meta: Any) -> list[Word] | None:
    """A stored sentence's words: its ``word_data`` (Epic 52, story 52.1), else the
    ``metadata.words`` of a transcript stored before it, else None."""
    data = row["word_data"]
    if data:
        try:
            return unpack(str(row["text"] or ""), bytes(data), float(row["start_sec"])) or None
        except ValueError:
            return None
    words = meta.get("words") if isinstance(meta, dict) else None
    if not words:
        return None
    return [Word(float(a), float(b), str(w)) for a, b, w in words]


def _texts_hash(sentences: list[Sentence]) -> str:
    """As ``transcribe`` hashes its output: the sentences' times and texts."""
    return canonical_hash([(s.start, s.end, s.text) for s in sentences])


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    v = await require_video(db, job.subject.id)
    row = await _active(db, v.id)
    if row is None:
        return StageResult(
            output_hash=canonical_hash("no transcript"),
            outcome="unchanged",
            summary={"second_listen": "no transcript"},
        )
    meta = loads(row["metadata"]) or {}
    sentences, has_words = _sentences(await _segments(db, row["id"]))
    out = _texts_hash(sentences)
    model = tr.refine_model()
    if model is None:
        return StageResult(output_hash=out, summary={"second_listen": "off"})
    refine = meta.get("refine") if isinstance(meta, dict) else None
    if isinstance(refine, dict) and refine.get("source") in ("hf", "share"):
        # corrected already (by this stage, or inside transcribe before the split)
        return StageResult(output_hash=out, summary={"second_listen": "done already"})
    fingerprint = await input_fingerprint(db, job.subject)
    relisten = bool((job.payload or {}).get(tr.RELISTEN))
    if not tr.runs_second_listen():
        # claimed only where the rules let a machine pass transcripts on (or its model
        # just failed to load): a while for one that runs it, first
        if _age_s(job) < (tr.RELISTEN_WAIT_S if relisten else PASS_ON_AFTER_S):
            raise JobDeferred(
                "the second listen: left to a machine that runs it",
                tr.RELISTEN_DEFER_S if relisten else PASS_ON_DEFER_S,
            )
        why = tr.second_listen_problem() or "it does not run here now"
        return await _skipped(db, job, row, meta, out, fingerprint, model, why, relisten)
    if not sentences:
        return StageResult(output_hash=out, summary={"second_listen": "no speech"})
    if not has_words:
        # transcribed before the second listen had its own stage: no words to align on.
        # The sweep transcribes it again (which stores them)
        why = "the transcript has no word timings stored (made before the second listen's stage)"
        return await _skipped(db, job, row, meta, out, fingerprint, model, why, relisten)

    stream = 0
    chosen = await tr.chosen_track(db, v.id)
    if chosen is not None:
        _track, stream = chosen
    found = await tr._on_share(ctx.local_path(v.path), v.path)
    budget = tr.audio_budget()
    held = await budget.acquire(float(v.duration_s or 3600))
    try:
        await tr.wait_for_memory(ctx)
        try:
            audio = await tr.decode_audio(found, stream=stream)
        except TimeoutError as exc:
            raise StageFailure("nas_timeout", f"{found}: decoding took too long") from exc
        total = round(audio.size / SAMPLE_RATE)
        await ctx.progress(
            "second_listen", 0, total, "second listen", steps=["decode", "listen", "store"],
            force=True,
        )  # fmt: skip
        result = Transcript(
            sentences=sentences,
            audio_sec=audio.size / SAMPLE_RATE,
            language=str(row["language"] or "ar"),
            model=str(row["model"] or ""),
            gpu_sec=0.0,
            detected_language=row["detected_language"],
            backend=str(row["backend"] or ""),
            backend_version=row["backend_version"],
        )
        refined = await listen(db, result, audio, model)
        del audio
    finally:
        await budget.release(held)
    if refined.get("no_room"):
        # no room on this GPU now: no try. The job goes back for a worker with room (this
        # one waits out its backoff, stt/cohere.py)
        raise JobDeferred(f"the second listen: {refined.get('skipped')}", tr.RELISTEN_DEFER_S)
    if refined.get("source") == "skipped":
        return await _skipped(
            db, job, row, meta, out, fingerprint, model, str(refined.get("skipped")), relisten,
            refined=refined,
        )  # fmt: skip
    tid = await _store(db, v.id, row, meta, result, refined)
    texts = _texts_hash(result.sentences)
    return StageResult(
        output_hash=texts,
        summary={
            "transcript_id": tid,
            "whisper_transcript_id": str(row["id"]),
            "segments": len(result.sentences),
            "audio_sec": round(result.audio_sec, 1),
            "refine": refined,
        },
    )


async def listen(db: Any, result: Transcript, audio: Any, model: str) -> dict[str, Any]:
    """The second listen on ``result`` (in place), in its turn on the GPU where Whisper and
    it take turns (:mod:`maktaba_pipeline.stt.gpu_turns`). Where they were to share it but
    the GPU has no room beside Whisper after all, they take turns from then on, and this
    one is heard again in its turn."""
    from ...stt import gpu_turns
    from ...stt.cohere import loaded
    from ...stt.cohere import unavailable as no_second_listen
    from ...stt.model_share import resolve_models_dir

    settings = tr.settings_from_env()
    # the share is looked for until the model is loaded (a stale mount can be slow)
    share = None if loaded(model) or no_second_listen() else await resolve_models_dir(db)
    for _ in range(2):
        mode = gpu_turns.sharing(model, settings)
        beside = mode != gpu_turns.SEQUENTIAL
        async with gpu_turns.use(gpu_turns.COHERE, model, settings):
            t0 = asyncio.get_running_loop().time()
            refined = await asyncio.to_thread(
                tr._refine, result, audio, model, share, settings, beside
            )
            seconds = asyncio.get_running_loop().time() - t0
        if refined.get("no_room") and mode == gpu_turns.TOGETHER:
            from ...stt import cohere

            gpu_turns.take_turns(model)
            cohere.forget_failure(model)
            continue
        if seconds > 0 and refined.get("source") in ("hf", "share"):
            refined["rtf"] = round(result.audio_sec / seconds, 1)
        return refined
    return refined


async def _skipped(
    db: Any,
    job: Job,
    row: Any,
    meta: dict[str, Any],
    out: str,
    fingerprint: str | None,
    model: str,
    why: str,
    relisten: bool,
    refined: dict[str, Any] | None = None,
) -> StageResult:
    """The transcript stays Whisper's, recorded as made without its second listen (its
    ``metadata.refine``; the job's input hash, :func:`.transcribe.skipped_fingerprint`):
    a machine that runs it queues it again. The stages after this one run on it."""
    info = refined or {"model": model, "source": "skipped", "skipped": why[:300]}
    meta = {**meta, "refine": info}
    async with db.transaction():
        await db.execute(
            "UPDATE transcripts SET metadata = $2 WHERE id = $1", row["id"], dumps(meta)
        )
        if not relisten:
            # an ordinary run starts the tries over; a relisten job's go on counting
            await db.execute(
                "DELETE FROM relisten_attempts WHERE video_id = $1", uid(job.subject.id)
            )
    _log.info("second_listen.skipped", video_id=str(job.subject.id), why=why[:200])
    return StageResult(
        output_hash=out,
        input_hash=tr.skipped_fingerprint(fingerprint) if fingerprint else None,
        summary={"refine": info},
    )


async def _store(
    db: Any,
    video_id: Any,
    row: Any,
    meta: dict[str, Any],
    result: Transcript,
    refined: dict[str, Any],
) -> str:
    """The corrected transcript replaces Whisper's as the video's active one, atomically
    (a new ``transcripts`` row, Whisper's kept as superseded, without its words: the
    corrected one keeps them, with the second listen's marks, in ``word_data``). The
    video's relisten tries go; a reviewer's fixes are carried over (story 52.9)."""
    seg_rows, any_words = tr.segment_rows(result.sentences)
    new_meta = {k: v for k, v in meta.items() if k != "words"}
    new_meta["refine"] = {**refined, "whisper": str(row["id"])}
    if any_words:
        new_meta["words"] = True
    tid = uid(uuid4())
    async with db.transaction():
        await db.execute(
            """
            INSERT INTO transcripts (id, video_id, audio_track_id, language, detected_language,
                                     backend, model, backend_version, word_level, diarized,
                                     is_active, metadata)
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, false, $11)
            """,
            tid,
            uid(video_id),
            row["audio_track_id"],
            row["language"],
            row["detected_language"],
            row["backend"],
            row["model"],
            row["backend_version"],
            row["word_level"],
            row["diarized"],
            dumps(new_meta),
        )
        await tr.insert_segments(db, tid, seg_rows)
        await tr.carry_over_edits(db, video_id, tid)
        await db.execute(
            "UPDATE transcripts SET is_active = false, superseded_at = $3"
            " WHERE video_id = $1 AND is_active AND id <> $2",
            uid(video_id),
            tid,
            to_ts(db, now()),
        )
        await db.execute("UPDATE transcripts SET is_active = true WHERE id = $1", tid)
        await db.execute(_DROP_WORDS_PG if is_pg(db) else _DROP_WORDS_SQLITE, row["id"])
        await db.execute(
            "UPDATE transcript_segments SET word_data = NULL"
            " WHERE transcript_id = $1 AND word_data IS NOT NULL",
            row["id"],
        )
        await db.execute("DELETE FROM relisten_attempts WHERE video_id = $1", uid(video_id))
    return str(tid)


_DROP_WORDS_PG = (
    "UPDATE transcript_segments SET metadata = metadata - 'words'"
    " WHERE transcript_id = $1 AND metadata ? 'words'"
)
_DROP_WORDS_SQLITE = (
    "UPDATE transcript_segments SET metadata = json_remove(metadata, '$.words')"
    " WHERE transcript_id = $1 AND json_extract(metadata, '$.words') IS NOT NULL"
)


def _age_s(job: Job) -> float:
    return tr._age_s(job)


IMPL = StageImpl(
    run=run,
    input_fingerprint=input_fingerprint,
    unavailable=unavailable,
    still_current=tr.listen_still_current,
    refresh=tr.listen_refresh,
)
