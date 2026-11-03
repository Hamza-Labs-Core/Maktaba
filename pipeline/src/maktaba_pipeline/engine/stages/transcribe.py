"""``transcribe``: a video's speech as sentences, each at its exact time (plan 03-10).

One job per video in the ``gpu`` pool. The audio is decoded straight off the share
(ffmpeg: mono, 16 kHz, into memory: no extract stage, no audio cache) and handed to the
process's :class:`~maktaba_pipeline.stt.batched.BatchedTranscriber`, which decodes one
window of every running job per GPU pass: on MLX on Apple Silicon, on CTranslate2
elsewhere (:mod:`maktaba_pipeline.stt.engine`, :mod:`maktaba_pipeline.stt.ct2`: an
NVIDIA GPU through CUDA, else the CPU). The transcript replaces the video's active one
in one transaction: a new ``transcripts`` row (on the track it decoded: of a file with
several audio streams the one the selection policy chooses, decoded on its own stream,
else the first stream, recorded as track 0; ``backend`` names the engine) with one
``transcript_segments`` row per sentence.

Settings (environment, read per run): ``MAKTABA_STT_MODEL`` (default
whisper-large-v3-turbo in the engine's format: ``mlx-community/whisper-large-v3-turbo``,
``dropbox-dash/faster-whisper-large-v3-turbo``; CTranslate2 loads the CTranslate2
conversion of an MLX model it is given), ``MAKTABA_STT_LANGUAGE`` (``ar``),
``MAKTABA_STT_WORDS`` (``1``: sentence bounds from word timings),
``MAKTABA_STT_STORE_WORDS`` (``0``: keep each sentence's words in its metadata; they are
kept anyway until the second listen has run, which needs them),
``MAKTABA_STT_REFINE`` (``1``: a second listen corrects Whisper's words: the stage
``second_listen`` after this one, :mod:`.second_listen`; ``0`` keeps Whisper's as they are)
with ``MAKTABA_STT_REFINE_MODEL`` (default ``CohereLabs/cohere-transcribe-arabic-07-2026``),
``MAKTABA_STT_BATCH`` (files decoded together: 32 on MLX, 8 on CTranslate2). MLX's GPU
memory: ``MAKTABA_STT_GPU_GB`` / ``_GPU_CACHE_GB``; CTranslate2's device and precision:
``MAKTABA_STT_DEVICE`` (``auto``), ``MAKTABA_STT_COMPUTE_TYPE`` (``auto``),
``MAKTABA_STT_CPU_THREADS``. The library's ``stt.initial_prompt`` (names, terms) biases
every window's decoding.

Where: any machine with a Whisper engine transcribes. The second listen is a stage of its
own (``second_listen``, :mod:`.second_listen`), which runs where it can
(:func:`second_listen_problem`: a Windows or Linux machine whose NVIDIA GPU holds Cohere,
beside Whisper or taking turns with it, :mod:`maktaba_pipeline.stt.gpu_turns`; a Mac with
mlx-audio) on the stored transcript and the file's audio, without Whisper again. Its job
records a transcript left without it in its input hash (:func:`skipped_fingerprint`): a
worker that runs the second listen queues such transcripts again, a few at a time
(:func:`relisten`; one transcribed before the split, without its words stored, is
transcribed again first), while one that cannot lets them stand.
A video whose relisten jobs keep leaving it without the second listen (a file that worker
cannot read, a model that does not load) waits longer after each (from the end of the
try), the untried ones first, and after ``rules.transcription.relisten_tries`` is left as
it is, said once in its job log (``relisten_attempts``, slots 0145 and 0146). It gets its
tries again with a second-listen model or a capability (:func:`second_listen_capability`)
the home has not seen before (``relisten_seen``), or with the operator's next
``relisten_round``. A try that found no room on the GPU is no try: the job goes back.
Deleted and missing videos are not tried, and their records go.
"""

from __future__ import annotations

import asyncio
import functools
import importlib.util
import math
import os
import platform
import re
import subprocess
import sys
import time
from datetime import UTC, timedelta
from pathlib import Path
from typing import TYPE_CHECKING, Any
from uuid import uuid4

from ...audio.probe import AudioTrack, parse_ffprobe_json
from ...binaries import ffmpeg_binary
from ...db.jobs import Job
from ...log import get_logger
from ...stt import engine
from ...stt.cohere import DEFAULT_MODEL as REFINE_MODEL
from ...stt.placement import TranscriptionConfig
from ...stt.settings import SAMPLE_RATE, Checkpoint, FileProgress, Transcript, WhisperSettings
from ..context import JobDeferred, StageContext, StageFailure
from ..events import log_event
from ..graph import TRANSCRIBE_VERSION, Request, StageResult, Subject
from ..hashing import canonical_hash
from ..health import system_memory
from ..sql import dumps, is_pg, loads, now, parse_ts, to_ts, uid
from ._common import load_video, require_video
from .content_hash import READ_TIMEOUT_S, on_disk
from .registry import StageImpl

if TYPE_CHECKING:
    import numpy as np

    from ...stt.batched import BatchedTranscriber

__all__ = [
    "IMPL",
    "TRANSCRIBE_VERSION",
    "relisten",
    "second_listen_problem",
    "settings_from_env",
    "skipped_fingerprint",
    "still_current",
    "unavailable",
]

_log = get_logger()


def unavailable() -> str | None:
    """Why this worker cannot run transcribe, or None: MLX-Whisper runs on Apple
    Silicon only, with the ``stt`` extra; elsewhere CTranslate2, with the ``stt`` extra,
    a model it can load and the GPU it was told to use, if any
    (:func:`maktaba_pipeline.stt.ct2.unavailable`). A worker that cannot never claims
    the stage (it would fail every job), but still imports this module to enqueue it.
    The second listen is a stage of its own: it keeps no machine from Whisper."""
    return _engine_unavailable()


def _engine_unavailable() -> str | None:
    if engine.current() == engine.CT2:
        if importlib.util.find_spec("numpy") is None:
            return "numpy is not installed (the stt extra)"
        from ...stt.ct2 import unavailable as ct2_unavailable

        return ct2_unavailable(settings_from_env())
    if platform.system() != "Darwin" or platform.machine() != "arm64":
        return "MLX-Whisper needs Apple Silicon"
    for mod in ("numpy", "mlx_whisper"):
        if importlib.util.find_spec(mod) is None:
            return f"{mod} is not installed (the stt extra)"
    return None


def use_vad() -> bool:
    """Decode speech only (``MAKTABA_STT_VAD=1``, when Silero is installed). Off by
    default: on 5.45 h of lectures it cut 7 % of the audio but decoding only 3 %, and the
    text moved (word similarity to the full decode 0.89, one file 0.67), so it is not
    worth it for this library (plan 03-10)."""
    from ...stt.vad import vad_available

    return os.environ.get("MAKTABA_STT_VAD", "0") == "1" and vad_available()


def _remap(result: Transcript, tmap: Any, audio_sec: float) -> Transcript:
    """A transcript of the speech-only audio, on the file's own timeline."""
    for s in result.sentences:
        s.start, s.end = tmap(s.start), tmap(s.end, end=True)
        if s.words:
            s.words = [(tmap(a), tmap(b, end=True), w) for a, b, w in s.words]
    result.audio_sec = audio_sec
    return result


def get_transcriber(settings: WhisperSettings) -> BatchedTranscriber:
    """The process's transcriber for these settings, on this machine's engine."""
    if engine.current() == engine.CT2:
        from ...stt.ct2 import get_transcriber as on_ct2

        return on_ct2(settings)
    from ...stt.batched import get_transcriber as shared

    return shared(settings)


def _where(settings: WhisperSettings) -> str:
    """Where the files are decoded, for the job's progress line: a CTranslate2 worker
    without a usable GPU decodes on the CPU (known once its model is loaded)."""
    device = getattr(get_transcriber(settings), "device", None) or settings.device
    return "on the CPU" if str(device).startswith("cpu") else "on the GPU"


#: decoding the audio off the share: generous, a long lecture over a busy NAS
DECODE_TIMEOUT_S = 900.0

#: how often a file's progress is saved (``MAKTABA_STT_CHECKPOINT_S``): a restart loses
#: at most this much of each file's decoding
CHECKPOINT_S = 60.0


class AudioBudget:
    """Hours of audio a worker holds at once (decoded audio + its spectrogram on the
    GPU, ~0.5 GB per hour). A job waits here before decoding its file, so memory stays
    bounded however long the videos are; a file longer than the whole budget runs alone."""

    def __init__(self, seconds: float) -> None:
        self.limit = seconds
        self.used = 0.0
        self._cond: asyncio.Condition | None = None

    def _c(self) -> asyncio.Condition:
        if self._cond is None:
            self._cond = asyncio.Condition()
        return self._cond

    async def acquire(self, seconds: float) -> float:
        need = min(max(seconds, 1.0), self.limit)
        async with self._c():
            await self._c().wait_for(lambda: self.used == 0 or self.used + need <= self.limit)
            self.used += need
        return need

    async def release(self, held: float) -> None:
        async with self._c():
            self.used = max(0.0, self.used - held)
            self._c().notify_all()


_BUDGET: AudioBudget | None = None


def audio_budget() -> AudioBudget:
    """The process's budget: ``MAKTABA_STT_AUDIO_HOURS`` (default 6)."""
    global _BUDGET
    if _BUDGET is None:
        _BUDGET = AudioBudget(float(os.environ.get("MAKTABA_STT_AUDIO_HOURS", "6")) * 3600)
    return _BUDGET


def settings_from_env(max_batch: int | None = None) -> WhisperSettings:
    """This machine's settings, each engine reading its own knobs."""
    env = os.environ
    name = engine.current()
    model = engine.model_for(name, env.get("MAKTABA_STT_MODEL"))
    language = env.get("MAKTABA_STT_LANGUAGE", "ar")
    words = env.get("MAKTABA_STT_WORDS", "1") != "0"
    batch = max_batch or int(env.get("MAKTABA_STT_BATCH") or engine.DEFAULT_BATCH[name])
    if name == engine.CT2:
        return WhisperSettings(
            model=model,
            language=language,
            word_timestamps=words,
            max_batch=batch,
            device=env.get("MAKTABA_STT_DEVICE") or "auto",
            compute_type=env.get("MAKTABA_STT_COMPUTE_TYPE") or "auto",
            cpu_threads=int(env.get("MAKTABA_STT_CPU_THREADS") or 0),
        )
    return WhisperSettings(
        model=model,
        language=language,
        word_timestamps=words,
        max_batch=batch,
        gpu_limit_gb=float(env.get("MAKTABA_STT_GPU_GB", "24")),
        gpu_cache_gb=float(env.get("MAKTABA_STT_GPU_CACHE_GB", "2")),
    )


def refine_model() -> str | None:
    """The model of the second listen, or None when it is off (``MAKTABA_STT_REFINE=0``)."""
    if os.environ.get("MAKTABA_STT_REFINE", "1") == "0":
        return None
    return os.environ.get("MAKTABA_STT_REFINE_MODEL", REFINE_MODEL)


# ------------------------------------------------------------------ where it runs

#: ``rules.transcription`` as this worker last read it (:func:`refresh`, before each
#: stage selection)
_RULES = TranscriptionConfig()


def second_listen_problem() -> str | None:
    """Why this worker cannot run the second listen, or None (whether it has the model
    shows when it loads it)."""
    from ...stt.cohere import unavailable as no_second_listen

    why = no_second_listen()
    if why is None and engine.current() == engine.MLX and not importlib.util.find_spec("mlx_audio"):
        why = "mlx-audio is not installed (the stt extra)"
    return why


def runs_second_listen() -> bool:
    """This worker gives its transcripts their second listen: it is on, runs here, and its
    model did not just fail to load (``cohere.load_failure``: then, until the next try, a
    transcript without it stands, and none is queued again)."""
    model = refine_model()
    if model is None or second_listen_problem() is not None:
        return False
    from ...stt.cohere import load_failure

    return load_failure(model) is None


def waits_for_second_listen() -> str | None:
    """Why this worker leaves the ``second_listen`` stage to the machines that run it, or
    None: it is on (``MAKTABA_STT_REFINE``), it cannot run here (or its model just failed
    to load: until the next try, ``cohere.load_failure``), and the home's rules do not let
    a machine without it pass transcripts on with Whisper's words
    (``without_second_listen``)."""
    model = refine_model()
    if model is None or _RULES.without_second_listen:
        return None
    why = second_listen_problem()
    if why is None:
        from ...stt.cohere import load_failure

        why = load_failure(model)
    if why is None:
        return None
    return (
        f"the second listen does not run here ({why}): the machines that run it give the"
        " transcripts their second listen (rules transcription: without_second_listen lets"
        " this one pass them on with Whisper's words)"
    )


def skipped_fingerprint(fingerprint: str) -> str:
    """The inputs a transcript made without its second listen answers: those of
    ``fingerprint``, the second listen left out. Its job stores this as its input hash."""
    return canonical_hash({"inputs": fingerprint, "second_listen": "skipped"})


#: a relisten job (:func:`relisten`) that a worker without the second listen claims goes
#: back for this long, for one that runs it...
RELISTEN_DEFER_S = 900.0
#: ...for a day at most: then the transcript stands (while a machine that runs the
#: second listen is up, its next sweep queues it again)
RELISTEN_WAIT_S = 86_400.0
#: the payload key of a relisten job
RELISTEN = "relisten"


def still_current(job: Job, prev: str, fingerprint: str) -> bool:
    """Whether the video's last transcript, made for ``prev`` inputs, stands for this job
    (the engine then skips the run): made for the same inputs, with or without its second
    listen (that is the ``second_listen`` stage's: Whisper does not run again for it). A
    relisten job (:func:`relisten`) runs: it is queued only for a transcript made before
    the split, without the words the second listen needs."""
    if (job.payload or {}).get(RELISTEN):
        return False
    return prev in (fingerprint, skipped_fingerprint(fingerprint))


def listen_still_current(job: Job, prev: str, fingerprint: str) -> bool:
    """:func:`still_current` of the ``second_listen`` stage: its last result, made for
    ``prev`` inputs, stands for this job on this worker: made for the same inputs, or for
    the same without the second listen on a worker that cannot run it either. A relisten
    job runs where the second listen does: a worker without it gives the job back for a
    while (``JobDeferred``), for one that runs it."""
    relisten = bool((job.payload or {}).get(RELISTEN))
    here = runs_second_listen()
    unchanged = prev in (fingerprint, skipped_fingerprint(fingerprint))
    if relisten and unchanged and not here and _age_s(job) < RELISTEN_WAIT_S:
        raise JobDeferred("the second listen: left to a machine that runs it", RELISTEN_DEFER_S)
    if prev == fingerprint:
        # a relisten job for a transcript recorded as made with its second listen
        # (before this record): where it runs, it runs
        return not (relisten and here)
    return prev == skipped_fingerprint(fingerprint) and not here


def _age_s(job: Job) -> float:
    created = job.created_at
    if created is None or created.year < 2000:
        return 0.0
    if created.tzinfo is None:
        created = created.replace(tzinfo=UTC)
    return (now() - created).total_seconds()


#: a worker that runs the second listen looks for transcripts made without it this often,
#: queues at most this many each time (at the bulk priority: after the new files), and
#: only those made this long ago (one whose own second listen fails to load must not
#: queue its new transcripts again at once)
RELISTEN_EVERY_S = 600.0
RELISTEN_BATCH = 20
RELISTEN_AFTER_S = 3600.0
RELISTEN_PRIORITY = 500
_LAST_RELISTEN: list[float] = []
# How many tries a transcript gets and how far apart: rules.transcription
# (relisten_tries, relisten_wait_hours, relisten_wait_max_hours, relisten_round;
# stt/placement.py), read before each stage selection.


def _open_job(video: str) -> str:
    """A transcribe or second_listen job of the video ``video`` (a column) still to run or
    running."""
    return (
        "EXISTS (SELECT 1 FROM processing_jobs j WHERE j.subject_kind = 'video'"
        f" AND j.subject_id = {video} AND j.stage IN ('transcribe', 'second_listen')"
        " AND j.state IN ('pending','claimed','running','resuming','paused'))"
    )


_OPEN_JOB = _open_job("t.video_id")

#: a video the sweep leaves alone: deleted, or its file missing (a scan found it gone, or
#: confirmed it is), or replaced by a newer copy
_GONE = (
    "(v.deleted_at IS NOT NULL OR v.missing_confirmed_at IS NOT NULL"
    " OR v.state IN ('missing', 'superseded'))"
)


async def refresh(db: Any, serving: bool) -> None:
    """Before each stage selection: the home's ``rules.transcription`` (the sweep is the
    ``second_listen`` stage's: :func:`listen_refresh`)."""
    await read_rules(db)


async def listen_refresh(db: Any, serving: bool) -> None:
    """The ``second_listen`` stage's refresh: the rules; on a worker that serves it and
    runs the second listen, :func:`relisten`."""
    await read_rules(db)
    if serving and runs_second_listen():
        await relisten(db)


async def read_rules(db: Any) -> None:
    """The home's ``rules.transcription``, as this worker reads it now."""
    global _RULES
    from ...rules import read

    _RULES = (await read(db, "transcription"))[0]


async def relisten(db: Any) -> int:
    """Queue the second listen again for the videos whose transcript was made without it
    (``metadata.refine.source = skipped``) as relisten jobs (a worker without the second
    listen gives them back): ``second_listen`` for a transcript with its words stored
    (``metadata.words``), else ``transcribe``, which stores them (its second listen
    follows). Once per ``RELISTEN_EVERY_S``, and only once this
    worker has the model (loaded, or on its disk: a machine without it would make the same
    transcript again). The videos tried least come first, the oldest among them; one
    tried already waits out its backoff, and one tried ``relisten_tries`` times gets no
    more (:func:`_stop_relistens`). Returns the jobs queued."""
    from ...pipeline.backoff import compute_backoff
    from ..enqueue import enqueue_job

    t = time.monotonic()
    if _LAST_RELISTEN and t - _LAST_RELISTEN[0] < RELISTEN_EVERY_S:
        return 0
    model = refine_model()
    if model is None or not await asyncio.to_thread(_has_second_listen_model, model):
        return 0
    _LAST_RELISTEN[:] = [t]
    rules = _RULES
    skipped = _skipped_sql(db)
    await _forget_gone(db)
    await _settle_tries(db, rules)
    await _reset_relistens(db, model, rules.relisten_round)
    await _stop_relistens(db, skipped, rules)
    at = now()
    rows = await db.fetch(
        f"SELECT t.video_id, COALESCE(r.attempts, 0) AS attempts, {_words_sql(db)} AS words"
        " FROM transcripts t"
        " JOIN videos v ON v.id = t.video_id"
        " LEFT JOIN relisten_attempts r ON r.video_id = t.video_id"
        f" WHERE t.is_active AND {skipped} AND t.created_at < $1 AND NOT {_GONE}"
        " AND (r.video_id IS NULL OR (r.stopped_at IS NULL AND r.attempts < $3"
        "  AND r.ended_at IS NOT NULL AND r.next_at <= $4))"
        f" AND NOT {_OPEN_JOB}"
        " ORDER BY COALESCE(r.attempts, 0), t.created_at LIMIT $2",
        to_ts(db, at - timedelta(seconds=RELISTEN_AFTER_S)),
        RELISTEN_BATCH,
        rules.relisten_tries,
        to_ts(db, at),
    )
    queued = 0
    for r in rows:
        res = await enqueue_job(
            db,
            subject_kind="video",
            subject_id=str(r["video_id"]),
            stage="second_listen" if r["words"] else "transcribe",
            priority=RELISTEN_PRIORITY,
            reason={"why": "transcribed without the second listen", "by": "system"},
            payload={RELISTEN: True},
        )
        if res.job_id is None:
            continue
        tries = int(r["attempts"]) + 1
        # the wait is counted from the end of the try (_settle_tries), not from now: a job
        # can wait in the queue for hours. next_at = now + the wait meanwhile, which a
        # worker from before slot 0146 (no ended_at) respects too
        wait = compute_backoff(
            tries,
            base=rules.relisten_wait_hours * 3600,
            cap=max(rules.relisten_wait_hours, rules.relisten_wait_max_hours) * 3600,
        )
        await db.execute(
            "INSERT INTO relisten_attempts (video_id, attempts, last_job_id, last_at, next_at,"
            " ended_at, wait_s, model, relisten_round)"
            " VALUES ($1, $2, $3, $4, $8, NULL, $5, $6, $7)"
            " ON CONFLICT (video_id) DO UPDATE SET"
            " attempts = EXCLUDED.attempts, last_job_id = EXCLUDED.last_job_id,"
            " last_at = EXCLUDED.last_at, next_at = EXCLUDED.next_at, ended_at = NULL,"
            " wait_s = EXCLUDED.wait_s, model = EXCLUDED.model,"
            " relisten_round = EXCLUDED.relisten_round,"
            " stopped_at = NULL",
            uid(r["video_id"]),
            tries,
            res.job_id,
            to_ts(db, at),
            wait,
            model,
            rules.relisten_round,
            to_ts(db, at + timedelta(seconds=wait)),
        )
        queued += 1
    if queued:
        _log.info("transcribe.relisten_queued", jobs=queued, model=model)
    return queued


def _skipped_sql(db: Any) -> str:
    """The transcript ``t`` was made without its second listen."""
    if is_pg(db):
        return "t.metadata -> 'refine' ->> 'source' = 'skipped'"
    return "json_extract(t.metadata, '$.refine.source') = 'skipped'"


def _words_sql(db: Any) -> str:
    """The transcript ``t`` has its words stored (``metadata.words``), as a boolean."""
    if is_pg(db):
        return "COALESCE((t.metadata ->> 'words') = 'true', false)"
    return "COALESCE(json_extract(t.metadata, '$.words') = 1, 0)"


async def _forget_gone(db: Any) -> int:
    """The tries of deleted and missing videos go: the sweep does not try them (a file
    that comes back starts afresh)."""
    rows = await db.fetch(
        "DELETE FROM relisten_attempts WHERE video_id IN"
        f" (SELECT v.id FROM videos v WHERE {_GONE}) RETURNING video_id"
    )
    return len(rows)


async def _settle_tries(db: Any, rules: TranscriptionConfig) -> int:
    """The tries whose job is over: their wait starts when the job ended (its
    ``finished_at``; now when the job is gone), so a job that waited in the queue does not
    use up the wait. A job that ended without running (``skipped_unchanged``: workers
    without the second listen, or without room for it, gave it back until it was a day
    old) was no try, and does not count. Returns how many."""
    rows = await db.fetch(
        "SELECT r.video_id, r.attempts, r.wait_s, j.finished_at, j.outcome"
        " FROM relisten_attempts r LEFT JOIN processing_jobs j ON j.id = r.last_job_id"
        f" WHERE r.ended_at IS NULL AND NOT {_open_job('r.video_id')}"
    )
    from ...pipeline.backoff import compute_backoff

    for r in rows:
        ended = parse_ts(r["finished_at"]) or now()
        wait = r["wait_s"]
        if wait is None:  # a try queued before slot 0146
            wait = compute_backoff(
                max(1, int(r["attempts"])),
                base=rules.relisten_wait_hours * 3600,
                cap=max(rules.relisten_wait_hours, rules.relisten_wait_max_hours) * 3600,
            )
        # the count as it is now, not as read above: a reset since then (another worker's,
        # the operator's round) leaves it at 0, which this must not take below (CHECK >= 0)
        await db.execute(
            "UPDATE relisten_attempts SET ended_at = $2, next_at = $3,"
            " attempts = attempts - CASE WHEN attempts > 0 THEN $4 ELSE 0 END"
            " WHERE video_id = $1 AND ended_at IS NULL",
            uid(r["video_id"]),
            to_ts(db, ended),
            to_ts(db, ended + timedelta(seconds=float(wait))),
            1 if r["outcome"] == "skipped_unchanged" else 0,
        )
    return len(rows)


async def _reset_relistens(db: Any, model: str, round_: int) -> int:
    """Tries start over (none made, nothing stopped). For the videos that stopped: when
    this worker's second-listen model, or its capability (:func:`second_listen_capability`:
    a new machine, GPU, driver or packages), is one the home has not seen before
    (``relisten_seen``), once, whoever sees it first; a model or capability seen before
    changes nothing, so machines that differ do not reset each other. For every video tried
    in a round before the operator's ``relisten_round``: once a round. Returns how many."""
    capability, detail = second_listen_capability()
    at = to_ts(db, now())
    n = 0
    for kind, key, what in (("model", model, {"model": model}), ("capability", capability, detail)):
        seen = await db.fetchrow(
            "INSERT INTO relisten_seen (kind, key, detail, first_seen) VALUES ($1, $2, $3, $4)"
            " ON CONFLICT (kind, key) DO NOTHING RETURNING key",
            kind,
            key,
            dumps(what),
            at,
        )
        if seen is None:
            continue
        rows = await db.fetch(
            "UPDATE relisten_attempts SET attempts = 0, stopped_at = NULL, next_at = $1"
            " WHERE stopped_at IS NOT NULL RETURNING video_id",
            at,
        )
        n += len(rows)
        if rows:
            _log.info("transcribe.relisten_reset", why=f"new {kind}", videos=len(rows), **what)
    rows = await db.fetch(
        "UPDATE relisten_attempts SET attempts = 0, stopped_at = NULL, next_at = $1,"
        " relisten_round = $2 WHERE relisten_round < $2 RETURNING video_id",
        at,
        round_,
    )
    if rows:
        _log.info("transcribe.relisten_reset", why="round", videos=len(rows), round=round_)
    return n + len(rows)


def second_listen_capability() -> tuple[str, dict[str, Any]]:
    """What this worker's second listen runs on, as a key and its parts: the machine
    (:func:`_machine_key`), its engine and processor, on Apple Silicon its memory, on an
    NVIDIA GPU that GPU (name, memory, compute capability, driver), and the versions of
    the packages it runs with. A key the home has not seen gives the stopped transcripts
    their tries again (:func:`_reset_relistens`)."""
    from importlib import metadata

    packages: dict[str, str] = {}
    for name in ("mlx", "mlx-audio", "torch", "transformers"):
        try:
            packages[name] = metadata.version(name)
        except metadata.PackageNotFoundError:
            continue
    detail: dict[str, Any] = {
        "machine": _machine_key(),
        "engine": engine.current(),
        "arch": platform.machine(),
        "packages": packages,
    }
    if engine.current() == engine.MLX:
        mem = system_memory()
        detail["memory_gb"] = None if mem is None else round(mem[0] / (1 << 30))
    else:
        detail["gpu"] = _gpu_key()
    return canonical_hash(detail), detail


def _gpu_key() -> dict[str, Any] | None:
    """The NVIDIA GPU the second listen runs on (Whisper's, ``MAKTABA_STT_DEVICE``), as its
    driver names it, or None without one."""
    from ...stt import cohere_cuda, ct2

    index = cohere_cuda.device_index(os.environ.get("MAKTABA_STT_DEVICE") or "auto")
    gpu = None if index is None else ct2.gpu_info(index)
    if gpu is None:
        return None
    return {
        "name": gpu.name,
        "memory_gb": round(gpu.total_gb),
        "compute": None if gpu.compute is None else f"{gpu.compute[0]}.{gpu.compute[1]}",
        "driver": gpu.driver_major,
    }


def _machine_key() -> str:
    """This machine, steady across renames: a hash of its own id (Windows' MachineGuid,
    macOS' IOPlatformUUID, Linux's machine-id; :func:`stable_machine_id`), else of its host
    name."""
    import socket

    mid = stable_machine_id()
    return canonical_hash({"id": mid} if mid else {"host": socket.gethostname()})[:16]


@functools.cache
def stable_machine_id() -> str:
    """This machine's own id, "" when it cannot be read: ``MAKTABA_MACHINE_ID`` or Windows'
    MachineGuid (:func:`~maktaba_pipeline.cluster.config.machine_id`), macOS' IOPlatformUUID
    (``ioreg``, run without a shell, its answer matched by a pattern), Linux's machine-id."""
    from ...cluster.config import machine_id

    mid = machine_id()
    if mid or sys.platform == "win32":
        return mid
    if sys.platform == "darwin":
        return _io_platform_uuid()
    for path in ("/etc/machine-id", "/var/lib/dbus/machine-id"):
        try:
            text = Path(path).read_text(encoding="ascii").strip()
        except (OSError, UnicodeDecodeError):
            continue
        if re.fullmatch(r"[0-9a-fA-F]{32}", text):
            return text
    return ""


def _io_platform_uuid() -> str:
    """The Mac's IOPlatformUUID (its hardware UUID), "" when ``ioreg`` cannot say."""
    try:
        out = subprocess.run(
            ["/usr/sbin/ioreg", "-rd1", "-c", "IOPlatformExpertDevice"],
            capture_output=True,
            text=True,
            timeout=10,
            check=False,
        ).stdout
    except (OSError, subprocess.SubprocessError):
        return ""
    m = re.search(r'"IOPlatformUUID"\s*=\s*"([0-9A-Fa-f-]{36})"', out)
    return m.group(1) if m else ""


async def _stop_relistens(db: Any, skipped: str, rules: TranscriptionConfig) -> int:
    """The videos tried ``rules.relisten_tries`` times, their last try over and their
    transcript still without the second listen, get no more tries. Each is said once, as
    a warning in its last relisten job's log (Processing, Activity), and in this worker's
    log. Returns how many stopped."""
    rows = await db.fetch(
        "SELECT r.video_id, r.attempts, r.last_job_id, t.metadata, v.library_id"
        " FROM relisten_attempts r"
        " JOIN transcripts t ON t.video_id = r.video_id AND t.is_active"
        " JOIN videos v ON v.id = r.video_id"
        f" WHERE r.stopped_at IS NULL AND r.attempts >= $1 AND r.ended_at IS NOT NULL"
        f" AND {skipped} AND NOT {_GONE} AND NOT {_OPEN_JOB}"
        " LIMIT $2",
        rules.relisten_tries,
        RELISTEN_BATCH,
    )
    stopped = 0
    for r in rows:
        video_id, tries = str(r["video_id"]), int(r["attempts"])
        why = await _last_try(db, r["last_job_id"], loads(r["metadata"]))
        message = (
            f"the second listen: {tries} tries did not give this transcript its second"
            f" listen ({why}); no more tries. Running transcribe on this video again starts"
            " them over."
        )
        async with db.transaction():
            # set once: another worker's sweep at the same time finds it set
            # ...and still out of tries, its last one over: a reset between the choice
            # and here (another worker's, or the operator's round) stands
            won = await db.fetchrow(
                "UPDATE relisten_attempts SET stopped_at = $2"
                " WHERE video_id = $1 AND stopped_at IS NULL AND attempts >= $3"
                " AND ended_at IS NOT NULL RETURNING video_id",
                video_id,
                to_ts(db, now()),
                rules.relisten_tries,
            )
            if won is None:
                continue
            if r["last_job_id"] is not None:
                await log_event(
                    db,
                    job_id=int(r["last_job_id"]),
                    subject_kind="video",
                    subject_id=video_id,
                    library_id=r["library_id"],
                    stage="second_listen",
                    level="warn",
                    kind="step",
                    message=message,
                    data={"relisten": {"tries": tries, "stopped": True, "why": why}},
                )
        _log.warning("transcribe.relisten_stopped", video_id=video_id, tries=tries, why=why)
        stopped += 1
    return stopped


async def _last_try(db: Any, job_id: Any, metadata: Any) -> str:
    """Why the last relisten job left the transcript without its second listen: the job's
    failure, else what the transcript says. The reason comes first and a path the message
    starts with goes last, so the cut to 200 characters keeps the reason (a share's path on
    Windows is long)."""
    if job_id is not None:
        job = await db.fetchrow(
            "SELECT state, error_cause, error FROM processing_jobs WHERE id = $1", int(job_id)
        )
        if job is not None and job["state"] not in ("done", "skipped"):
            try:
                error = loads(job["error"]) or {}
            except ValueError:
                error = {"message": str(job["error"])}
            if not isinstance(error, dict):
                error = {"message": str(error)}
            kind = str(error.get("kind") or job["error_cause"] or "").strip()
            detail = _reason_first(str(error.get("message") or "no reason given"))
            return f"its last job {job['state']}{f' ({kind})' if kind else ''}: {detail}"[:200]
    refine = (metadata or {}).get("refine") or {}
    reason = refine.get("skipped")
    return f"it did not run: {_reason_first(str(reason))}"[:200] if reason else "it did not run"


def _reason_first(message: str) -> str:
    """``<path>: <reason>`` (how the stages name a file's trouble) as ``<reason> (<path>)``;
    any other message as it is."""
    head, sep, tail = message.partition(": ")
    if sep and tail and ("/" in head or "\\" in head):
        return f"{tail} ({head})"
    return message


def _has_second_listen_model(model: str) -> bool:
    """The second listen's model is loaded here, or on this machine's disk (a folder, the
    Hugging Face cache, a copy of the home's)."""
    from ...stt import cohere, model_share

    if cohere.loaded(model) or Path(model).expanduser().is_dir():
        return True
    return model_share.hf_snapshot(model) is not None or bool(model_share.local_snapshots(model))


def _refine(
    result: Transcript,
    audio: Any,
    model: str,
    share: Path | None = None,
    settings: WhisperSettings | None = None,
    beside_whisper: bool = True,
) -> dict[str, Any]:
    """The second listen (in a thread: it decodes on the GPU). The model comes from
    Hugging Face or, on a machine without the licence, from the home's copy on the share
    (``share``: the share's models folder); ``source`` says which, ``runtime`` what heard
    it on CUDA, and ``dictionary`` when the respelling had no dictionary. A worker that
    cannot run it (no mlx-audio; no NVIDIA GPU, or one with
    no room for it beside Whisper, whose ``settings`` say its device and batch; no copy of
    the model) keeps Whisper's words and says why. ``beside_whisper``: Whisper stays on the
    GPU meanwhile (else the two take turns, :mod:`maktaba_pipeline.stt.gpu_turns`)."""
    from ...stt.cohere import NoRoom, dictionary, recogniser
    from ...stt.refine import refine

    try:
        rec = recogniser(model, share, settings, beside_whisper=beside_whisper)
    except Exception as exc:  # noqa: BLE001 - Whisper's transcript is still a transcript
        _log.warning("transcribe.refine_unavailable", model=model, error=str(exc))
        return {
            "model": model,
            "source": "skipped",
            "skipped": f"{type(exc).__name__}: {exc}"[:300],
            **({"no_room": True} if isinstance(exc, NoRoom) else {}),
        }
    source = str(getattr(rec, "source", "hf"))
    runtime = getattr(rec, "runtime", None)
    extra = {"source": source, **({"runtime": str(runtime)} if runtime else {})}
    try:
        spelling = dictionary()
        stats = refine(result, audio, rec, spelling, model).as_dict()
    except Exception as exc:  # noqa: BLE001 - each stretch is merged whole: what is done stands
        _log.warning("transcribe.refine_failed", model=model, exc_info=True)
        return {"model": model, **extra, "failed": f"{type(exc).__name__}: {exc}"[:300]}
    # without CAMeL Tools or its data, Cohere's words not seen in Whisper's are spelt as heard
    return {**stats, **extra, **({"dictionary": "unavailable"} if spelling is None else {})}


async def wait_for_memory(ctx: StageContext) -> None:
    """Back-pressure: while the host is short of memory (``MAKTABA_STT_MIN_FREE_PCT``,
    default 20% available), a job waits before it decodes anything, and says so. After
    ``MAKTABA_STT_MEMORY_WAIT_S`` (default 900) it gives the job back for 10 minutes
    (:class:`JobDeferred`: no attempt used, its claim and audio budget freed)."""
    floor = float(os.environ.get("MAKTABA_STT_MIN_FREE_PCT", "20"))
    limit = float(os.environ.get("MAKTABA_STT_MEMORY_WAIT_S", "900"))
    waited = 0
    while True:
        mem = system_memory()
        if mem is None or not mem[0] or 100.0 * mem[1] / mem[0] >= floor:
            return
        if waited >= limit:
            free = 100.0 * mem[1] / mem[0]
            raise JobDeferred(f"memory: {free:.0f}% available, needs {floor:.0f}%", 600.0)
        if waited % 60 == 0:
            await ctx.progress(
                "waiting", 0, 1,
                f"waiting for memory: {100.0 * mem[1] / mem[0]:.0f}% available, needs {floor:.0f}%",
                force=True,
            )  # fmt: skip
        await asyncio.sleep(5)
        waited += 5


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    """DB only: the file's identity and how it is transcribed."""
    v = await load_video(db, subject.id)
    if v is None:
        return None
    s = settings_from_env()
    row = await db.fetchrow("SELECT content_hash FROM videos WHERE id = $1", uid(v.id))
    prompt = await library_prompt(db, v.library_id)
    return canonical_hash(
        {
            # only when set: a library without a prompt keeps the fingerprint it had
            **({"prompt": prompt} if prompt else {}),
            "hash": None if row is None else str(row["content_hash"]),
            "size": v.size_bytes,
            # one Whisper is one model on either engine: a PC does not transcribe again
            # what a Mac did (the transcript records the engine and model that made it)
            "model": engine.fingerprint_model(s.model),
            "language": s.language,
            "words": s.word_timestamps,
            # the setting, not whether this process could run it: every worker
            # computes the same fingerprint for the same input
            "vad": os.environ.get("MAKTABA_STT_VAD", "0") == "1",
            "refine": refine_model(),
            "v": TRANSCRIBE_VERSION,
        }
    )


async def library_prompt(db: Any, library_id: Any) -> str | None:
    """The library's ``stt.initial_prompt`` (names and terms that bias Whisper), or None."""
    from ._analysis import library_setting

    stt = await library_setting(db, library_id, "stt")
    got = stt.get("initial_prompt") if isinstance(stt, dict) else None
    return (got.strip() or None) if isinstance(got, str) else None


async def decode_audio(
    path: str, timeout: float = DECODE_TIMEOUT_S, *, stream: int = 0
) -> np.ndarray:
    """The file's audio stream ``stream`` (``-map 0:a:N``; the first by default) as
    mono float32 at 16 kHz, piped out of ffmpeg into memory (no file written)."""
    import numpy as np

    proc = await asyncio.create_subprocess_exec(
        ffmpeg_binary(), "-nostdin", "-hide_banner", "-loglevel", "error",
        "-i", path, "-map", f"0:a:{stream}", "-vn", "-ac", "1", "-ar", str(SAMPLE_RATE),
        "-f", "s16le", "-",
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
    )  # fmt: skip
    try:
        out, err = await asyncio.wait_for(proc.communicate(), timeout)
    except TimeoutError:
        proc.kill()
        await proc.wait()
        raise
    if proc.returncode != 0:
        msg = err.decode("utf-8", "replace").strip().splitlines()
        tail = msg[-1] if msg else f"ffmpeg exit {proc.returncode}"
        if "matches no streams" in tail or "does not contain any stream" in tail:
            raise StageFailure("no_audio", f"{path}: no audio track", retryable=False)
        raise StageFailure("decode_failed", f"{path}: {tail}", retryable=False)
    return np.frombuffer(out, np.int16).astype(np.float32) / 32768.0


def needs_file() -> bool:
    """Whether the configured STT backend reads a file (openai-api) rather than the PCM
    this stage decodes itself: then the extract stage's WAV is asked for first
    (graph ``may_request``). False for whisper-mlx, the default."""
    from ...stt.transcribe import configured_backend_requires_file

    return configured_backend_requires_file()


#: the payload key extract's commit puts on the transcribe it queues
_FROM_EXTRACT = "content_hash"


async def cached_wav(db: Any, video_id: Any) -> tuple[str | None, int | None] | None:
    """The video's ``audio_cache`` WAV: ``(path on this machine or None, track id)``,
    or None when extract wrote none."""
    row = await db.fetchrow(
        "SELECT a.path, a.audio_track_id FROM audio_cache a"
        " JOIN videos v ON v.content_hash = a.content_hash WHERE v.id = $1",
        uid(video_id),
    )
    if row is None:
        return None
    path = str(row["path"]) if row["path"] else ""
    present: bool = bool(path) and await asyncio.to_thread(os.path.isfile, path)
    return (path if present else None), int(row["audio_track_id"])


async def chosen_track(db: Any, video_id: Any) -> tuple[int, int] | None:
    """For a file with several audio streams, the one the selection policy chooses (the
    track extract would write): ``(its audio_tracks.id, its -map 0:a:N stream)``.

    The choice is made over the streams the probe saw in the file as it is now
    (``media_info.raw_ffprobe``), not over ``audio_tracks`` rows, which can outlive a
    replaced file's streams. None for a file with one audio stream, one not probed, or a
    chosen stream with no ``audio_tracks`` row: its first stream is decoded and recorded
    as track 0, as before."""
    from ...audio.track_selection import select_tracks

    audio = await _probed_audio(db, video_id)
    if len(audio) < 2:
        return None
    picked = select_tracks(audio)
    if not picked:
        return None
    row = await db.fetchval(
        "SELECT id FROM audio_tracks WHERE video_id = $1 AND track_index = $2",
        uid(video_id),
        picked[0].index,
    )
    return None if row is None else (int(row), picked[0].index)


#: what a failure adds when the path map left the home's path as it was: on a machine
#: that reads the share under another path (a Windows drive letter, or a UNC path) the
#: file is not missing, the map is
UNMAPPED_HINT = (
    "this machine's path map has no entry for this path, so it looked for the home's own"
    " path: map the home's folder to where this machine reads it (Admin > Machines > path"
    " map)"
)


_DRIVE_OR_UNC = re.compile(r"^(?:[A-Za-z]:[\\/]|[\\/]{2}[^\\/])")


def unmapped_here(local: str, db_path: str, windows: bool | None = None) -> bool:
    """The path map left the home's path as it was, and it is another system's: a POSIX
    path (``/Volumes/...``) on Windows, a drive letter or UNC path elsewhere. Only then is
    a file that is not there the map's doing; on the home its own paths stay as they are."""
    if local.replace("\\", "/") != db_path.replace("\\", "/"):
        return False
    if windows is None:
        windows = sys.platform == "win32"
    if windows:
        return db_path.startswith("/") and not db_path.startswith("//")
    return _DRIVE_OR_UNC.match(db_path) is not None


async def _on_share(local: str, db_path: str | None = None) -> str:
    """The video's file as this machine reads it, or why not. ``db_path`` is the path the
    home stored: when the path map left another system's path as it was and it is not
    here, the failure says that the map has no entry for it (a Windows node whose map
    misses a library's folder, :func:`unmapped_here`)."""
    unmapped = db_path is not None and unmapped_here(local, db_path)
    hint = f" ({UNMAPPED_HINT})" if unmapped else ""
    try:
        found = await asyncio.wait_for(asyncio.to_thread(on_disk, local), READ_TIMEOUT_S)
    except TimeoutError as exc:
        raise StageFailure("nas_timeout", f"{local}: no answer", retryable=False) from exc
    except OSError as exc:
        raise StageFailure("nas_unavailable", f"{local}: {exc}{hint}") from exc
    if found is None:
        raise StageFailure("file_missing", f"{local}: not found{hint}", retryable=False)
    return found


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    v = await require_video(db, job.subject.id)
    wav: str | None = None
    track_id: int | None = None
    if needs_file():
        cached = await cached_wav(db, v.id)
        if cached is None and _FROM_EXTRACT not in (job.payload or {}):
            # the backend reads a file and extract has not written it: ask for it. The
            # extract handler queues transcribe again once the WAV is there
            return StageResult(
                output_hash=canonical_hash("awaiting the extracted audio"),
                outcome="waiting_inputs",
                requests=(
                    Request(
                        stage="extract",
                        subject_kind="video",
                        subject_id=v.id,
                        reason="the STT backend reads an audio file",
                    ),
                ),
                summary={"waiting_for": "extract"},
            )
        if cached is not None:
            wav, track_id = cached
            if wav is None:  # written on another machine: decoded off the share instead
                track_id = None
                _log.info("transcribe.wav_not_here", video_id=str(v.id))
    stream = 0
    if wav is None:
        # off the share: of a file with several audio streams, the one the selection
        # policy chooses (extract's choice), decoded on its own stream, not the first
        chosen = await chosen_track(db, v.id)
        if chosen is not None:
            track_id, stream = chosen
    found = wav if wav is not None else await _on_share(ctx.local_path(v.path), v.path)

    budget = audio_budget()
    held = await budget.acquire(float(v.duration_s or 3600))
    try:
        await wait_for_memory(ctx)
    except BaseException:
        await budget.release(held)
        raise
    try:
        result = await _transcribe_file(
            db,
            v,
            found,
            ctx,
            await input_fingerprint(db, job.subject),
            audio_track_id=track_id,
            stream=stream,
        )
    finally:
        await budget.release(held)
    if wav is not None:
        # this transcription was the WAV's only reader: the file and its row go
        from ...audio.extract import release_audio_cache

        row = await db.fetchrow("SELECT content_hash FROM videos WHERE id = $1", uid(v.id))
        if row is not None and row["content_hash"]:
            await release_audio_cache(db, content_hash=str(row["content_hash"]))
    return result


# ------------------------------------------------------------------ checkpoints


async def load_checkpoint(db: Any, video_id: Any, fingerprint: str | None) -> Checkpoint | None:
    """The file's saved progress, when it was made for the same input."""
    row = await db.fetchrow(
        "SELECT fingerprint, seek_frames, last_speech, language, segments"
        " FROM transcribe_checkpoints WHERE video_id = $1",
        uid(video_id),
    )
    if row is None or fingerprint is None or str(row["fingerprint"]) != fingerprint:
        return None
    segs = row["segments"]
    return Checkpoint(
        seek=int(row["seek_frames"]),
        last_speech=float(row["last_speech"]),
        language=row["language"],
        segments=list(loads(segs) if isinstance(segs, str) else segs),
    )


async def save_checkpoint(
    db: Any, video_id: Any, fingerprint: str, cp: Checkpoint, *, append: bool = False
) -> None:
    """Save the file's progress. With ``append``, ``cp.segments`` are only those decoded
    since the last save, added to the saved ones (a long file's sentences are not all
    rewritten every minute); otherwise they replace them."""
    segments = cp.segments
    if append and not is_pg(db):  # SQLite has no JSON array concatenation to lean on
        prev = await load_checkpoint(db, video_id, fingerprint)
        segments = (prev.segments if prev else []) + segments
        append = False
    merged = (
        "CASE WHEN transcribe_checkpoints.fingerprint = EXCLUDED.fingerprint"
        " THEN transcribe_checkpoints.segments || EXCLUDED.segments ELSE EXCLUDED.segments END"
        if append
        else "EXCLUDED.segments"
    )
    await db.execute(
        "INSERT INTO transcribe_checkpoints"
        " (video_id, fingerprint, seek_frames, last_speech, language, segments, updated_at)"
        " VALUES ($1, $2, $3, $4, $5, $6, $7)"
        " ON CONFLICT (video_id) DO UPDATE SET fingerprint = EXCLUDED.fingerprint,"
        " seek_frames = EXCLUDED.seek_frames, last_speech = EXCLUDED.last_speech,"
        f" language = EXCLUDED.language, segments = {merged},"
        " updated_at = EXCLUDED.updated_at",
        uid(video_id),
        fingerprint,
        cp.seek,
        cp.last_speech,
        cp.language,
        dumps(segments),
        to_ts(db, now()),
    )


async def _run_saving(
    db: Any,
    v: Any,
    ctx: StageContext,
    fingerprint: str | None,
    audio: Any,
    settings: WhisperSettings,
    audio_sec: int,
    scale: float = 1.0,
    prompt: str | None = None,
) -> Transcript:
    """Transcribe, saving the file's progress every ``MAKTABA_STT_CHECKPOINT_S``
    (``prompt``: the library's ``stt.initial_prompt``)."""
    resume = await load_checkpoint(db, v.id, fingerprint)
    progress = FileProgress(saved_seek=resume.seek if resume else 0)
    progress.saved_count = len(resume.segments) if resume else 0
    every = float(os.environ.get("MAKTABA_STT_CHECKPOINT_S", str(CHECKPOINT_S)))
    task = asyncio.ensure_future(
        get_transcriber(settings).transcribe(
            audio, resume=resume, progress=progress, scale=scale, prompt=prompt
        )
    )
    try:
        while True:
            done, _ = await asyncio.wait({task}, timeout=every)
            if done:
                return task.result()
            cp, count = progress.snapshot(progress.saved_count)
            if fingerprint is None or cp.seek <= progress.saved_seek:
                continue
            try:  # best effort: a failed save is retried next time, the decoding goes on
                await save_checkpoint(db, v.id, fingerprint, cp, append=progress.saved_count > 0)
            except Exception:  # noqa: BLE001 - the progress note must not stop the work
                _log.warning("transcribe.checkpoint_failed", video_id=str(v.id), exc_info=True)
                continue
            progress.saved_seek, progress.saved_count = cp.seek, count
            await ctx.progress(
                "transcribe", round(cp.seek / 100 * scale), audio_sec, _where(settings), force=True
            )
    finally:
        if not task.done():
            task.cancel()


async def _transcribe_file(
    db: Any,
    v: Any,
    found: str,
    ctx: StageContext,
    fingerprint: str | None,
    *,
    audio_track_id: int | None = None,
    stream: int = 0,
) -> StageResult:
    """Decode ``found`` (the video's audio stream ``stream``, or extract's WAV) and
    store its transcript against ``audio_track_id``."""
    try:
        audio = await decode_audio(found, stream=stream)
    except TimeoutError as exc:
        raise StageFailure("nas_timeout", f"{found}: decoding took too long") from exc
    if audio.size < SAMPLE_RATE // 2:
        raise StageFailure("no_audio", f"{found}: no audible audio", retryable=False)

    settings = settings_from_env()
    await ctx.progress(
        "transcribe", 0, round(audio.size / SAMPLE_RATE), _where(settings),
        steps=["decode", "transcribe", "store"], force=True,
    )  # fmt: skip
    total_sec = audio.size / SAMPLE_RATE
    tmap: Any = None
    if use_vad():
        from ...stt.vad import pack, speech_chunks

        # the pauses out (CPU, beside the GPU): Whisper decodes the speech only
        chunks = await asyncio.to_thread(speech_chunks, audio)
        audio, tmap = pack(audio, chunks)
    try:
        if audio.size < SAMPLE_RATE // 2:  # no speech at all: nothing to decode
            result = Transcript([], total_sec, settings.language, settings.model, 0.0)
        else:
            from ...stt import gpu_turns

            scale = total_sec / (audio.size / SAMPLE_RATE)
            # where the GPU holds one model at a time, Whisper waits for its turn
            async with gpu_turns.use(gpu_turns.WHISPER, refine_model(), settings):
                result = await _run_saving(
                    db, v, ctx, fingerprint, audio, settings, round(total_sec), scale,
                    prompt=await library_prompt(db, v.library_id),
                )  # fmt: skip
    except Exception as exc:  # the model or the GPU: retryable (a restart may fix it)
        raise StageFailure("stt_failed", f"{type(exc).__name__}: {exc}") from exc
    del audio
    if tmap is not None:
        result = _remap(result, tmap, total_sec)

    # a relisten job (a transcript from before the split, without its words): its tries
    # go on counting until its second listen has run
    relisten = bool((ctx.job.payload or {}).get(RELISTEN))
    transcript_id = await _store(
        db, v.id, result, settings, keep_tries=relisten, audio_track_id=audio_track_id
    )
    texts = [(s.start, s.end, s.text) for s in result.sentences]
    rtf = result.audio_sec / result.gpu_sec if result.gpu_sec > 0 else None
    # with the second listen on, the new transcript is Whisper's again (it replaced the
    # corrected one): its second listen runs even when Whisper's words are the same
    out: Any = {"texts": texts, "transcript": transcript_id} if refine_model() else texts
    return StageResult(
        output_hash=canonical_hash(out),
        summary={
            "transcript_id": transcript_id,
            "segments": len(texts),
            "audio_sec": round(result.audio_sec, 1),
            "rtf": None if rtf is None else round(rtf, 1),
            "model": result.model,
            "language": result.language,
            "detected_language": result.detected_language,
        },
    )


async def _store(
    db: Any,
    video_id: Any,
    result: Transcript,
    settings: WhisperSettings,
    refined: dict[str, Any] | None = None,
    keep_tries: bool = False,
    audio_track_id: int | None = None,
) -> str:
    """The transcript replaces the video's active one, atomically. The video's relisten
    tries (``relisten_attempts``) go with the old one, unless ``keep_tries``: a relisten
    job that again went without the second listen. ``audio_track_id``: the track the
    audio came from (extract's WAV, or the chosen stream off the share), else the first
    audio stream, recorded as track 0. Each sentence keeps its words while the second
    listen is on (it needs them: ``metadata.words`` says so) or ``MAKTABA_STT_STORE_WORDS``
    asks for them."""
    store_words = keeps_words() or bool(refine_model() and settings.word_timestamps)
    async with db.transaction():
        if audio_track_id is None:
            audio_track_id = await _first_audio_track(db, video_id)
        row = await db.fetchrow(
            """
            INSERT INTO transcripts (id, video_id, audio_track_id, language, detected_language,
                                     backend, model, backend_version, word_level, diarized,
                                     is_active, metadata)
            VALUES ($8, $1, $7, $2, $9, $10, $3, $4, $5, false, false, $6)
            RETURNING id
            """,
            uid(video_id),
            result.language,
            result.model,
            _backend_version(result),
            settings.word_timestamps,
            dumps(
                {
                    "audio_sec": round(result.audio_sec, 1),
                    "plan": "03-10",
                    **({"refine": refined} if refined else {}),
                    **({"words": True} if store_words else {}),
                }
            ),
            audio_track_id,
            uid(uuid4()),  # SQLite gives the column no default
            result.detected_language,  # what Whisper heard, not the language it was told
            result.backend,  # the engine that made it: mlx-batched, ct2-batched
        )
        tid = row["id"]
        rows = []
        for seq, s in enumerate(result.sentences):
            meta: dict[str, Any] = {"no_speech": round(s.no_speech_prob, 3)}
            if store_words and s.words:
                meta["words"] = [[a, b, w] for a, b, w in s.words]
            rows.append(
                (
                    tid,
                    seq,
                    s.start,
                    s.end,
                    s.text,
                    round(math.exp(s.avg_logprob), 4),
                    dumps(meta),
                )
            )
        if rows:
            await db.executemany(
                "INSERT INTO transcript_segments (transcript_id, seq, start_sec, end_sec, text,"
                " confidence, metadata) VALUES ($1, $2, $3, $4, $5, $6, $7)",
                rows,
            )
        await db.execute(
            "UPDATE transcripts SET is_active = false, superseded_at = $3"
            " WHERE video_id = $1 AND is_active AND id <> $2",
            uid(video_id),
            tid,
            to_ts(db, now()),
        )
        await db.execute("UPDATE transcripts SET is_active = true WHERE id = $1", tid)
        await db.execute("DELETE FROM transcribe_checkpoints WHERE video_id = $1", uid(video_id))
        if not keep_tries:
            await db.execute("DELETE FROM relisten_attempts WHERE video_id = $1", uid(video_id))
        if result.detected_language:
            await db.execute(
                "UPDATE videos SET detected_language = $2"
                " WHERE id = $1 AND detected_language IS NULL",
                uid(video_id),
                result.detected_language,
            )
    return str(tid)


def keeps_words() -> bool:
    """``MAKTABA_STT_STORE_WORDS=1``: every transcript keeps its sentences' words."""
    return os.environ.get("MAKTABA_STT_STORE_WORDS", "0") == "1"


async def _first_audio_track(db: Any, video_id: Any) -> int:
    """The audio_tracks row of the stream decoded off the share (track 0)."""
    # the stream ffmpeg decoded (-map 0:a:0), recorded as the probe would have: the
    # default only when the video has none (the probe's choice of default stands).
    # Its facts come from the probe's ffprobe output when there is one: a row with
    # no codec read as "no audio" to streaming, and the video played silent. A row
    # already there keeps its facts; only the ones it lacks are filled.
    facts = await _probed_first_audio(db, video_id)
    track = await db.fetchrow(
        "INSERT INTO audio_tracks (video_id, track_index, codec, channels, sample_rate,"
        "                          language, title, disposition, is_default)"
        " VALUES ($1, 0, $2, $3, $4, $5, $6, $7, NOT EXISTS (SELECT 1 FROM audio_tracks"
        "  WHERE video_id = $1 AND is_default))"
        " ON CONFLICT (video_id, track_index) DO UPDATE SET"
        "  codec = COALESCE(NULLIF(audio_tracks.codec, ''), EXCLUDED.codec),"
        "  channels = COALESCE(audio_tracks.channels, EXCLUDED.channels),"
        "  sample_rate = COALESCE(audio_tracks.sample_rate, EXCLUDED.sample_rate)"
        " RETURNING id",
        uid(video_id),
        facts.codec if facts else None,
        facts.channels if facts else None,
        facts.sample_rate if facts else None,
        facts.language if facts else "und",
        facts.title if facts else None,
        dumps(facts.disposition if facts else {}),
    )
    return int(track["id"])


async def _probed_audio(db: Any, video_id: Any) -> list[AudioTrack]:
    """The video's audio streams (``-map 0:a:N``) as the probe saw them, from
    ``media_info.raw_ffprobe``; empty when the video was not probed or the output lists
    no audio."""
    raw = await db.fetchval("SELECT raw_ffprobe FROM media_info WHERE video_id = $1", uid(video_id))
    try:
        payload = loads(raw)
    except ValueError:
        return []
    if not isinstance(payload, dict):
        return []
    return parse_ffprobe_json(payload).audio


async def _probed_first_audio(db: Any, video_id: Any) -> AudioTrack | None:
    """The video's first audio stream (``-map 0:a:0``) as the probe saw it; ``None``
    when the video was not probed or the output lists no audio."""
    audio = await _probed_audio(db, video_id)
    return audio[0] if audio else None


def _backend_version(result: Transcript | None = None) -> str | None:
    """The engine's runtime: what the transcriber said (CTranslate2: faster-whisper's and
    CTranslate2's versions, the device and the precision), else MLX-Whisper's version."""
    if result is not None and result.backend_version:
        return result.backend_version
    try:
        from importlib.metadata import version

        return f"mlx-whisper {version('mlx-whisper')}"
    except Exception:  # noqa: BLE001 - informational only
        return None


IMPL = StageImpl(
    run=run,
    input_fingerprint=input_fingerprint,
    unavailable=unavailable,
    still_current=still_current,
    refresh=refresh,
)
