"""``transcribe``: a video's speech as sentences, each at its exact time (plan 03-10).

One job per video in the ``gpu`` pool. The audio is decoded straight off the share
(ffmpeg: mono, 16 kHz, into memory: no extract stage, no audio cache) and handed to the
process's :class:`~maktaba_pipeline.stt.batched.BatchedTranscriber`, which decodes one
window of every running job per GPU pass. The transcript replaces the video's active one
in one transaction: a new ``transcripts`` row (on the stream it decoded, recorded in
``audio_tracks`` as track 0) with one ``transcript_segments`` row per sentence.

Settings (environment, read per run): ``MAKTABA_STT_MODEL`` (default
``mlx-community/whisper-large-v3-turbo``), ``MAKTABA_STT_LANGUAGE`` (``ar``),
``MAKTABA_STT_WORDS`` (``1``: sentence bounds from word timings),
``MAKTABA_STT_STORE_WORDS`` (``0``: keep each sentence's words in its metadata).
"""

from __future__ import annotations

import asyncio
import importlib.util
import math
import os
import platform
from typing import TYPE_CHECKING, Any
from uuid import uuid4

from ...db.jobs import Job
from ...log import get_logger
from ...stt.settings import SAMPLE_RATE, Checkpoint, FileProgress, Transcript, WhisperSettings
from ..context import JobDeferred, StageContext, StageFailure
from ..graph import TRANSCRIBE_VERSION, StageResult, Subject
from ..hashing import canonical_hash
from ..health import system_memory
from ..sql import dumps, is_pg, loads, now, to_ts, uid
from ._common import load_video, require_video
from .content_hash import READ_TIMEOUT_S, on_disk
from .registry import StageImpl

if TYPE_CHECKING:
    import numpy as np

    from ...stt.batched import BatchedTranscriber

__all__ = ["IMPL", "TRANSCRIBE_VERSION", "settings_from_env", "unavailable"]

_log = get_logger()


def unavailable() -> str | None:
    """Why this worker cannot run transcribe, or None: MLX-Whisper runs on Apple
    Silicon only, with the ``stt`` extra. A worker without it never claims the stage
    (it would fail every job), but still imports this module to enqueue it."""
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
    from ...stt.batched import get_transcriber as shared

    return shared(settings)


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
    env = os.environ
    return WhisperSettings(
        model=env.get("MAKTABA_STT_MODEL", "mlx-community/whisper-large-v3-turbo"),
        language=env.get("MAKTABA_STT_LANGUAGE", "ar"),
        word_timestamps=env.get("MAKTABA_STT_WORDS", "1") != "0",
        max_batch=max_batch or int(env.get("MAKTABA_STT_BATCH", "32")),
        gpu_limit_gb=float(env.get("MAKTABA_STT_GPU_GB", "24")),
        gpu_cache_gb=float(env.get("MAKTABA_STT_GPU_CACHE_GB", "2")),
    )


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
    return canonical_hash(
        {
            "hash": None if row is None else str(row["content_hash"]),
            "size": v.size_bytes,
            "model": s.model,
            "language": s.language,
            "words": s.word_timestamps,
            # the setting, not whether this process could run it: every worker
            # computes the same fingerprint for the same input
            "vad": os.environ.get("MAKTABA_STT_VAD", "0") == "1",
            "v": TRANSCRIBE_VERSION,
        }
    )


async def decode_audio(path: str, timeout: float = DECODE_TIMEOUT_S) -> np.ndarray:
    """The file's first audio track as mono float32 at 16 kHz."""
    import numpy as np

    proc = await asyncio.create_subprocess_exec(
        "ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error",
        "-i", path, "-map", "0:a:0", "-vn", "-ac", "1", "-ar", str(SAMPLE_RATE),
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


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    v = await require_video(db, job.subject.id)
    local = ctx.local_path(v.path)
    try:
        found = await asyncio.wait_for(asyncio.to_thread(on_disk, local), READ_TIMEOUT_S)
    except TimeoutError as exc:
        raise StageFailure("nas_timeout", f"{local}: no answer", retryable=False) from exc
    except OSError as exc:
        raise StageFailure("nas_unavailable", f"{local}: {exc}") from exc
    if found is None:
        raise StageFailure("file_missing", f"{local}: not found", retryable=False)

    budget = audio_budget()
    held = await budget.acquire(float(v.duration_s or 3600))
    try:
        await wait_for_memory(ctx)
    except BaseException:
        await budget.release(held)
        raise
    try:
        return await _transcribe_file(db, v, found, ctx, await input_fingerprint(db, job.subject))
    finally:
        await budget.release(held)


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
) -> Transcript:
    """Transcribe, saving the file's progress every ``MAKTABA_STT_CHECKPOINT_S``."""
    resume = await load_checkpoint(db, v.id, fingerprint)
    progress = FileProgress(saved_seek=resume.seek if resume else 0)
    progress.saved_count = len(resume.segments) if resume else 0
    every = float(os.environ.get("MAKTABA_STT_CHECKPOINT_S", str(CHECKPOINT_S)))
    task = asyncio.ensure_future(
        get_transcriber(settings).transcribe(audio, resume=resume, progress=progress, scale=scale)
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
                "transcribe", round(cp.seek / 100 * scale), audio_sec, "on the GPU", force=True
            )
    finally:
        if not task.done():
            task.cancel()


async def _transcribe_file(
    db: Any, v: Any, found: str, ctx: StageContext, fingerprint: str | None
) -> StageResult:
    try:
        audio = await decode_audio(found)
    except TimeoutError as exc:
        raise StageFailure("nas_timeout", f"{found}: decoding took too long") from exc
    if audio.size < SAMPLE_RATE // 2:
        raise StageFailure("no_audio", f"{found}: no audible audio", retryable=False)

    settings = settings_from_env()
    await ctx.progress(
        "transcribe", 0, round(audio.size / SAMPLE_RATE), "on the GPU",
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
            scale = total_sec / (audio.size / SAMPLE_RATE)
            result = await _run_saving(
                db, v, ctx, fingerprint, audio, settings, round(total_sec), scale
            )
    except Exception as exc:  # the model or the GPU: retryable (a restart may fix it)
        raise StageFailure("stt_failed", f"{type(exc).__name__}: {exc}") from exc
    del audio
    if tmap is not None:
        result = _remap(result, tmap, total_sec)

    transcript_id = await _store(db, v.id, result, settings)
    texts = [(s.start, s.end, s.text) for s in result.sentences]
    rtf = result.audio_sec / result.gpu_sec if result.gpu_sec > 0 else None
    return StageResult(
        output_hash=canonical_hash(texts),
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


async def _store(db: Any, video_id: Any, result: Transcript, settings: WhisperSettings) -> str:
    """The transcript replaces the video's active one, atomically."""
    store_words = os.environ.get("MAKTABA_STT_STORE_WORDS", "0") == "1"
    async with db.transaction():
        # the stream ffmpeg decoded (-map 0:a:0), recorded as the probe would have: the
        # default only when the video has none (the probe's choice of default stands)
        track = await db.fetchrow(
            "INSERT INTO audio_tracks (video_id, track_index, is_default)"
            " VALUES ($1, 0, NOT EXISTS (SELECT 1 FROM audio_tracks"
            "  WHERE video_id = $1 AND is_default))"
            " ON CONFLICT (video_id, track_index) DO UPDATE SET track_index = 0"
            " RETURNING id",
            uid(video_id),
        )
        row = await db.fetchrow(
            """
            INSERT INTO transcripts (id, video_id, audio_track_id, language, detected_language,
                                     backend, model, backend_version, word_level, diarized,
                                     is_active, metadata)
            VALUES ($8, $1, $7, $2, $9, 'mlx-batched', $3, $4, $5, false, false, $6)
            RETURNING id
            """,
            uid(video_id),
            result.language,
            result.model,
            _backend_version(),
            settings.word_timestamps,
            dumps({"audio_sec": round(result.audio_sec, 1), "plan": "03-10"}),
            track["id"],
            uid(uuid4()),  # SQLite gives the column no default
            result.detected_language,  # what Whisper heard, not the language it was told
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
        if result.detected_language:
            await db.execute(
                "UPDATE videos SET detected_language = $2"
                " WHERE id = $1 AND detected_language IS NULL",
                uid(video_id),
                result.detected_language,
            )
    return str(tid)


def _backend_version() -> str | None:
    try:
        from importlib.metadata import version

        return f"mlx-whisper {version('mlx-whisper')}"
    except Exception:  # noqa: BLE001 - informational only
        return None


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint, unavailable=unavailable)
