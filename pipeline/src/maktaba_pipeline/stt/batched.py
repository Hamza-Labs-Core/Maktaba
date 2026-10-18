"""Batched Whisper: many files through one model, one GPU pass at a time (plan 03-10).

``mlx_whisper.transcribe`` decodes one 30 s window of one file per GPU pass and leaves
most of an M-series GPU idle (35-42x realtime per stream, 61x with four processes). This
module runs the same algorithm across many files at once: every running transcription
keeps its own seek pointer, and each pass decodes one window of every active file as a
single batch (148x realtime for a batch of 32 on an M3 Ultra).

The per-file algorithm is stock Whisper's, unchanged: seek to the last complete
timestamp (or past the window when it ends in silence), fall back to a higher
temperature when a window's text is too repetitive or improbable, skip a window
Whisper calls silence, no conditioning on the previous text. Word timings (DTW over the
cross-attention, Whisper's own) are optional; with them a sentence starts at its first
word and ends at its last, and the seek follows the last word.

The model and the GPU belong to one thread. Callers (the ``transcribe`` stage, many at a
time in the worker's ``gpu`` pool) ``await`` :meth:`BatchedTranscriber.transcribe`.
"""

from __future__ import annotations

import asyncio
import queue
import threading
import time
from collections.abc import Callable, Sequence
from dataclasses import dataclass, field
from typing import Any, Protocol

import numpy as np

from ..log import get_logger
from .align import AlignItem, align_batch

__all__ = [
    "BatchedTranscriber",
    "Sentence",
    "Transcript",
    "WhisperSettings",
    "get_transcriber",
]

_log = get_logger()

SAMPLE_RATE = 16_000
HOP_LENGTH = 160
N_FRAMES = 3000  # one 30 s window, in mel frames
FRAMES_PER_SECOND = SAMPLE_RATE // HOP_LENGTH  # 100
INPUT_STRIDE = 2  # mel frames per audio token
TIME_PRECISION = INPUT_STRIDE * HOP_LENGTH / SAMPLE_RATE  # 0.02 s per timestamp token


@dataclass(frozen=True, slots=True)
class WhisperSettings:
    """Whisper's knobs, as ``mlx_whisper.transcribe`` names them."""

    model: str = "mlx-community/whisper-large-v3-turbo"
    language: str = "ar"
    max_batch: int = 32
    word_timestamps: bool = True
    temperatures: tuple[float, ...] = (0.0, 0.2, 0.4, 0.6, 0.8, 1.0)
    compression_ratio_threshold: float = 2.4
    logprob_threshold: float = -1.0
    no_speech_threshold: float = 0.6


@dataclass(slots=True)
class Sentence:
    start: float
    end: float
    text: str
    avg_logprob: float
    no_speech_prob: float
    words: list[tuple[float, float, str]] | None = None


@dataclass(slots=True)
class Transcript:
    sentences: list[Sentence]
    audio_sec: float
    language: str
    model: str
    gpu_sec: float  # wall time from admission to the last window


class _Decoded(Protocol):
    tokens: list[int]
    avg_logprob: float
    compression_ratio: float
    no_speech_prob: float
    temperature: float


#: (mel windows, one temperature per window) -> one result per window
DecodeFn = Callable[[Sequence[Any], Sequence[float]], Sequence[_Decoded]]
#: (segments of one window, the window's mel, its frames, last speech end) -> adds "words"
AlignFn = Callable[[list[dict[str, Any]], Any, int, float], None]


#: what a caller hands the GPU thread: the audio, its future, and the loop to answer on
_Submission = tuple[np.ndarray, "asyncio.Future[Transcript]", asyncio.AbstractEventLoop]


class _Tokenizer(Protocol):
    timestamp_begin: int
    eot: int

    def decode(self, tokens: list[int]) -> str: ...


@dataclass(eq=False)  # a job is itself (its fields hold MLX arrays: no == on them)
class _Job:
    mel: Any  # (frames, n_mels): the whole file, padded with one window of silence
    content_frames: int
    audio_sec: float
    future: asyncio.Future[Transcript]
    loop: asyncio.AbstractEventLoop
    admitted: float
    seek: int = 0
    temp_idx: int = 0
    last_speech: float = 0.0
    segments: list[dict[str, Any]] = field(default_factory=list)

    @property
    def done(self) -> bool:
        return self.seek >= self.content_frames


def _needs_fallback(r: _Decoded, s: WhisperSettings) -> bool:
    if r.no_speech_prob > s.no_speech_threshold:
        return False  # silence: no point decoding it again
    return r.compression_ratio > s.compression_ratio_threshold or (
        r.avg_logprob < s.logprob_threshold
    )


def window_segments(
    job: _Job,
    result: _Decoded,
    segment_size: int,
    tokenizer: _Tokenizer,
    settings: WhisperSettings,
) -> tuple[list[dict[str, Any]], bool, float] | None:
    """One decoded window of one file: its sentences, and where the next window starts
    (stock ``mlx_whisper.transcribe``, minus prompts and the progress bar). ``None``
    when the window is silence. Returns (sentences, ends in silence, window offset)."""
    window_seek = job.seek  # word alignment places a sentence by its window's start
    time_offset = window_seek * HOP_LENGTH / SAMPLE_RATE
    tokens = np.array(result.tokens, dtype=np.int64)

    # no speech: fast-forward, unless the text is probable enough to keep anyway
    if result.no_speech_prob > settings.no_speech_threshold and not (
        result.avg_logprob > settings.logprob_threshold
    ):
        job.seek += segment_size
        return None

    def new_segment(start: float, end: float, toks: np.ndarray) -> dict[str, Any]:
        text_tokens = [int(t) for t in toks.tolist() if t < tokenizer.eot]
        return {
            "seek": window_seek,
            "start": start,
            "end": end,
            "text": tokenizer.decode(text_tokens),
            "tokens": [int(t) for t in toks.tolist()],
            "avg_logprob": result.avg_logprob,
            "no_speech_prob": result.no_speech_prob,
        }

    current: list[dict[str, Any]] = []
    is_ts = tokens >= tokenizer.timestamp_begin
    single_timestamp_ending = is_ts[-2:].tolist() == [False, True]
    consecutive = np.where(np.logical_and(is_ts[:-1], is_ts[1:]))[0] + 1
    if len(consecutive) > 0:
        slices = consecutive.tolist()
        if single_timestamp_ending:
            slices.append(len(tokens))
        last = 0
        for cut in slices:
            part = tokens[last:cut]
            start_pos = int(part[0]) - tokenizer.timestamp_begin
            end_pos = int(part[-1]) - tokenizer.timestamp_begin
            current.append(
                new_segment(
                    time_offset + start_pos * TIME_PRECISION,
                    time_offset + end_pos * TIME_PRECISION,
                    part,
                )
            )
            last = cut
        if single_timestamp_ending:
            job.seek += segment_size  # no speech after the last timestamp
        else:
            # the last sentence is unfinished: decode it again from its start
            last_ts = int(tokens[last - 1]) - tokenizer.timestamp_begin
            job.seek += last_ts * INPUT_STRIDE
    else:
        duration = segment_size * HOP_LENGTH / SAMPLE_RATE
        stamps = tokens[is_ts.nonzero()[0]]
        if len(stamps) > 0 and int(stamps[-1]) != tokenizer.timestamp_begin:
            duration = (int(stamps[-1]) - tokenizer.timestamp_begin) * TIME_PRECISION
        current.append(new_segment(time_offset, time_offset + duration, tokens))
        job.seek += segment_size
    return current, single_timestamp_ending, time_offset


def finish_window(
    job: _Job,
    current: list[dict[str, Any]],
    single_timestamp_ending: bool,
    time_offset: float,
) -> None:
    """After word alignment (if any): the seek follows the last word, and the window's
    sentences join the file's."""
    last_word_end = _last_word_end(current)
    if last_word_end is not None:
        if not single_timestamp_ending and last_word_end > time_offset:
            job.seek = round(last_word_end * FRAMES_PER_SECOND)
        job.last_speech = last_word_end
    end_of_audio = job.audio_sec  # a window at the very end can name times past the audio
    for seg in current:
        seg["end"] = min(seg["end"], end_of_audio)
        if seg["start"] < seg["end"] and seg["text"].strip():
            job.segments.append(seg)


def apply_window(
    job: _Job,
    result: _Decoded,
    segment_size: int,
    tokenizer: _Tokenizer,
    settings: WhisperSettings,
    *,
    mel_segment: Any = None,
    align: AlignFn | None = None,
) -> None:
    """One window, aligned on its own (``align``) when given."""
    got = window_segments(job, result, segment_size, tokenizer, settings)
    if got is None:
        return
    current, single, offset = got
    if align is not None and mel_segment is not None:
        align(current, mel_segment, segment_size, job.last_speech)
    finish_window(job, current, single, offset)


def _last_word_end(segments: list[dict[str, Any]]) -> float | None:
    words = [w for s in segments for w in (s.get("words") or [])]
    return float(words[-1]["end"]) if words else None


def to_sentences(segments: list[dict[str, Any]]) -> list[Sentence]:
    """Whisper segments as sentences, bounded by their words when there are any."""
    out: list[Sentence] = []
    for s in segments:
        words = s.get("words") or []
        start, end = float(s["start"]), float(s["end"])
        if words:
            start, end = float(words[0]["start"]), float(words[-1]["end"])
        if end <= start:
            continue
        out.append(
            Sentence(
                start=round(start, 3),
                end=round(end, 3),
                text=str(s["text"]).strip(),
                avg_logprob=float(s["avg_logprob"]),
                no_speech_prob=float(s["no_speech_prob"]),
                words=[
                    (round(float(w["start"]), 3), round(float(w["end"]), 3), str(w["word"]))
                    for w in words
                ]
                or None,
            )
        )
    return out


class BatchedTranscriber:
    """One Whisper model, one GPU thread, many files in flight.

    ``decode`` / ``align`` / ``mel_of`` / ``tokenizer`` default to MLX-Whisper's; tests
    pass fakes to exercise the seek logic without a GPU."""

    def __init__(
        self,
        settings: WhisperSettings | None = None,
        *,
        decode: DecodeFn | None = None,
        align: AlignFn | None = None,
        mel_of: Callable[[np.ndarray], Any] | None = None,
        window_of: Callable[[Any, int, int], Any] | None = None,
        tokenizer: _Tokenizer | None = None,
    ) -> None:
        self.settings = settings or WhisperSettings()
        self._decode = decode
        self._align = align
        self._mel_of = mel_of
        self._window_of = window_of
        self._tokenizer = tokenizer
        #: every window of a pass aligned in one model call (stt/align.py)
        self._align_batch: Callable[[list[AlignItem]], None] | None = None
        self._inbox: queue.Queue[_Submission] = queue.Queue()
        self._thread: threading.Thread | None = None
        self._lock = threading.Lock()
        self.windows = 0  # windows decoded, for the report
        self.passes = 0

    # --- public ---------------------------------------------------------------

    async def transcribe(self, audio: np.ndarray) -> Transcript:
        """``audio``: mono float32 at 16 kHz."""
        loop = asyncio.get_running_loop()
        fut: asyncio.Future[Transcript] = loop.create_future()
        self._ensure_thread()
        self._inbox.put((audio, fut, loop))
        return await fut

    # --- the GPU thread ---------------------------------------------------------

    def _ensure_thread(self) -> None:
        with self._lock:
            if self._thread is None or not self._thread.is_alive():
                self._thread = threading.Thread(target=self._run, name="whisper-batch", daemon=True)
                self._thread.start()

    def _load(self) -> None:
        """MLX-Whisper's model, mel, window, decode and word alignment."""
        if self._decode is not None and self._mel_of is not None:
            return
        import mlx.core as mx  # type: ignore[import-untyped,import-not-found,unused-ignore]
        from mlx_whisper.audio import (  # type: ignore[import-untyped,import-not-found,unused-ignore]
            N_SAMPLES,
            log_mel_spectrogram,
            pad_or_trim,
        )
        from mlx_whisper.decoding import (  # type: ignore[import-untyped,import-not-found,unused-ignore]
            DecodingOptions,
            DecodingTask,
            GreedyDecoder,
        )
        from mlx_whisper.load_models import (  # type: ignore[import-untyped,import-not-found,unused-ignore]
            load_model,
        )
        from mlx_whisper.tokenizer import (  # type: ignore[import-untyped,import-not-found,unused-ignore]
            get_tokenizer,
        )

        s = self.settings
        t0 = time.monotonic()
        model = load_model(s.model, dtype=mx.float16)
        tok = get_tokenizer(
            model.is_multilingual,
            num_languages=model.num_languages,
            language=s.language,
            task="transcribe",
        )
        self._tokenizer = tok
        n_mels = model.dims.n_mels

        def mel_of(audio: np.ndarray) -> Any:
            return log_mel_spectrogram(mx.array(audio), n_mels=n_mels, padding=N_SAMPLES)

        def window_of(mel: Any, seek: int, size: int) -> Any:
            return pad_or_trim(mel[seek : seek + size], N_FRAMES, axis=-2).astype(mx.float16)

        class RowGreedy(GreedyDecoder):  # type: ignore[misc]
            """Greedy decoding with a temperature per row: every window of a pass in
            one batch, whatever fallback temperature it has reached."""

            def __init__(self, temps: Sequence[float], eot: int) -> None:
                super().__init__(1.0, eot)
                t = mx.array(list(temps), dtype=mx.float32)
                self.hot = t > 0
                self.inv = mx.where(self.hot, 1.0 / mx.maximum(t, 1e-6), 1.0)[:, None]

            def update(self, tokens: Any, logits: Any, sum_logprobs: Any) -> Any:
                greedy = logits.argmax(axis=-1)
                sampled = mx.random.categorical(logits * self.inv)
                next_tokens = mx.where(self.hot, sampled, greedy)
                logprobs = logits - mx.logsumexp(logits, axis=-1, keepdims=True)
                current = logprobs[mx.arange(logprobs.shape[0]), next_tokens]
                sum_logprobs += current * (tokens[:, -1] != self.eot)
                eot_mask = tokens[:, -1] == self.eot
                next_tokens = next_tokens * (1 - eot_mask) + self.eot * eot_mask
                tokens = mx.concatenate([tokens, next_tokens[:, None]], axis=-1)
                return tokens, mx.all(tokens[:, -1] == self.eot), sum_logprobs

        def run_decode(mels: Sequence[Any], temps: Sequence[float]) -> Sequence[_Decoded]:
            opts = DecodingOptions(
                language=s.language,
                task="transcribe",
                temperature=max(temps) or 0.0,
                without_timestamps=False,
                fp16=True,
            )
            task = DecodingTask(model, opts)
            if any(t > 0 for t in temps):
                task.decoder = RowGreedy(temps, tok.eot)
            return list(task.run(mx.stack(list(mels))))

        self._mel_of = mel_of
        self._window_of = window_of
        self._decode = run_decode
        self._align = None
        if s.word_timestamps:
            self._align_batch = lambda items: align_batch(model, tok, items)
        _log.info("whisper.loaded", model=s.model, seconds=round(time.monotonic() - t0, 1))

    def _admit(self, jobs: list[_Job], block: bool) -> None:
        while len(jobs) < self.settings.max_batch:
            try:
                audio, fut, loop = self._inbox.get(block=block and not jobs, timeout=1.0)
            except queue.Empty:
                return
            block = False
            try:
                assert self._mel_of is not None
                mel = self._mel_of(audio)
                frames = int(mel.shape[0]) - N_FRAMES
                jobs.append(
                    _Job(
                        mel=mel,
                        content_frames=max(0, frames),
                        audio_sec=len(audio) / SAMPLE_RATE,
                        future=fut,
                        loop=loop,
                        admitted=time.monotonic(),
                    )
                )
            except Exception as exc:  # noqa: BLE001 - one file's failure is its own
                loop.call_soon_threadsafe(_set_exception, fut, exc)

    def _run(self) -> None:
        try:
            self._load()
        except Exception as exc:  # noqa: BLE001 - every waiting job learns why
            _log.exception("whisper.load_failed")
            while True:
                try:
                    _, fut, loop = self._inbox.get_nowait()
                except queue.Empty:
                    break
                loop.call_soon_threadsafe(_set_exception, fut, exc)
            self._decode = None
            self._thread = None
            return
        jobs: list[_Job] = []
        while True:
            self._admit(jobs, block=True)
            if not jobs:
                continue
            try:
                self._pass(jobs)
            except Exception as exc:  # noqa: BLE001 - a broken batch fails its files only
                _log.exception("whisper.batch_failed", files=len(jobs))
                for j in jobs:
                    j.loop.call_soon_threadsafe(_set_exception, j.future, exc)
                jobs.clear()
                continue
            for j in [j for j in jobs if j.done]:
                jobs.remove(j)
                try:
                    result = Transcript(
                        sentences=to_sentences(j.segments),
                        audio_sec=j.audio_sec,
                        language=self.settings.language,
                        model=self.settings.model,
                        gpu_sec=time.monotonic() - j.admitted,
                    )
                except Exception as exc:  # noqa: BLE001 - the thread outlives one file
                    j.loop.call_soon_threadsafe(_set_exception, j.future, exc)
                    continue
                j.loop.call_soon_threadsafe(_set_result, j.future, result)

    def _pass(self, jobs: list[_Job]) -> None:
        """One GPU pass: one window of every active file, each at the temperature its
        window has reached (a fallback re-decodes the same window, warmer, next pass)."""
        s = self.settings
        assert self._decode is not None and self._window_of is not None
        assert self._tokenizer is not None
        for j in jobs:
            if j.future.cancelled():
                j.seek = j.content_frames
        live = [j for j in jobs if not j.done]
        if not live:
            return
        sizes = [min(N_FRAMES, j.content_frames - j.seek) for j in live]
        windows = [self._window_of(j.mel, j.seek, n) for j, n in zip(live, sizes, strict=True)]
        results = self._decode(windows, [s.temperatures[j.temp_idx] for j in live])
        self.passes += 1
        self.windows += len(windows)
        pending: list[tuple[_Job, list[dict[str, Any]], bool, float, Any, int]] = []
        for j, n, win, r in zip(live, sizes, windows, results, strict=True):
            if _needs_fallback(r, s) and j.temp_idx + 1 < len(s.temperatures):
                j.temp_idx += 1
                continue
            j.temp_idx = 0
            got = window_segments(j, r, n, self._tokenizer, s)
            if got is not None:
                pending.append((j, got[0], got[1], got[2], win, n))
        if self._align_batch is not None:
            items = [AlignItem(cur, win, n, j.last_speech) for j, cur, _, _, win, n in pending]
            if items:
                self._align_batch(items)
        elif self._align is not None:
            for j, cur, _, _, win, n in pending:
                self._align(cur, win, n, j.last_speech)
        for j, cur, single, offset, _, _ in pending:
            finish_window(j, cur, single, offset)


def _set_result(fut: asyncio.Future[Transcript], value: Transcript) -> None:
    if not fut.done():
        fut.set_result(value)


def _set_exception(fut: asyncio.Future[Transcript], exc: BaseException) -> None:
    if not fut.done():
        fut.set_exception(exc)


_SHARED: dict[WhisperSettings, BatchedTranscriber] = {}


def get_transcriber(settings: WhisperSettings) -> BatchedTranscriber:
    """The process's transcriber for these settings (one model per settings)."""
    t = _SHARED.get(settings)
    if t is None:
        t = _SHARED[settings] = BatchedTranscriber(settings)
    return t
