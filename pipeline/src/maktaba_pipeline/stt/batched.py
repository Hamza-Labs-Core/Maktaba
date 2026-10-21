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
import os
import queue
import threading
import time
from collections.abc import Callable, Sequence
from dataclasses import dataclass, field
from typing import Any, Protocol

import numpy as np

from ..log import get_logger
from .align import AlignItem, align_batch
from .rules import TimestampRules
from .settings import (
    SAMPLE_RATE,
    Checkpoint,
    FileProgress,
    Sentence,
    Transcript,
    WhisperSettings,
)

__all__ = [
    "BatchedTranscriber",
    "Sentence",
    "Transcript",
    "WhisperSettings",
    "get_transcriber",
]

_log = get_logger()

HOP_LENGTH = 160
#: below this, Whisper's language guess is not recorded
LANGUAGE_MIN_PROB = 0.5
N_FRAMES = 3000  # one 30 s window, in mel frames
FRAMES_PER_SECOND = SAMPLE_RATE // HOP_LENGTH  # 100
INPUT_STRIDE = 2  # mel frames per audio token
TIME_PRECISION = INPUT_STRIDE * HOP_LENGTH / SAMPLE_RATE  # 0.02 s per timestamp token


class _Decoded(Protocol):
    tokens: list[int]
    avg_logprob: float
    compression_ratio: float
    no_speech_prob: float
    temperature: float


#: (mel windows, one temperature per window) -> one result per window
DecodeFn = Callable[[Sequence[Any], Sequence[float]], Sequence[_Decoded]]
#: (windows, each one's encoder output from an earlier pass or None) -> the pass's output
EncodeFn = Callable[[list[Any], list[Any]], Any]
#: (segments of one window, the window's mel, its frames, last speech end) -> adds "words"
AlignFn = Callable[[list[dict[str, Any]], Any, int, float], None]


#: what a caller hands the GPU thread: the audio, its future, and the loop to answer on
_Submission = tuple[
    np.ndarray,
    "asyncio.Future[Transcript]",
    asyncio.AbstractEventLoop,
    Checkpoint | None,
    FileProgress | None,
    float,
    str | None,
]


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
    #: what Whisper hears in the first window: None until asked, "" when unsure
    language: str | None = None
    #: the encoder output of the window at ``features_seek``, kept while that window
    #: is decoded again at a warmer temperature
    features: Any = None
    features_seek: int = -1
    #: where the caller reads this file's progress (to save it), if it asked
    progress: FileProgress | None = None
    #: seconds of the caller's audio per second decoded here (the speech-only audio
    #: stands for more): progress is counted in the caller's seconds
    scale: float = 1.0
    #: Whisper's ``initial_prompt`` for this file (the library's ``stt.initial_prompt``):
    #: names and terms that bias every window's decoding; None: no prompt
    prompt: str | None = None

    @property
    def done(self) -> bool:
        return self.seek >= self.content_frames

    @property
    def progress_sec(self) -> float:
        return min(self.seek, self.content_frames) / FRAMES_PER_SECOND * self.scale

    @property
    def unsaved_sec(self) -> float:
        """Audio decoded since the caller last saved this file's progress."""
        saved = self.progress.saved_seek if self.progress is not None else 0
        return max(0.0, self.progress_sec - saved / FRAMES_PER_SECOND * self.scale)


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

    ``encode`` / ``decode`` / ``align`` / ``align_batch`` / ``mel_of`` / ``tokenizer``
    default to MLX-Whisper's; tests pass fakes to exercise the seek logic without a GPU
    (with no ``encode`` the windows go to ``decode`` as they are).

    ``encode(windows, cached)`` returns the pass's encoder output, one row per window;
    ``cached[k]``, when not None, is window k's output from an earlier pass (a window
    decoded again after a fallback) and is used as it is.

    The CTranslate2 engine (:class:`~.ct2.CT2Transcriber`, Windows and Linux) is this
    class with CTranslate2's model behind the same hooks."""

    #: what the transcripts it makes record as their engine (``transcripts.backend``)
    backend = "mlx-batched"
    #: and its runtime, once loaded (None: the stage names MLX-Whisper's version)
    backend_version: str | None = None

    def __init__(
        self,
        settings: WhisperSettings | None = None,
        *,
        encode: EncodeFn | None = None,
        decode: DecodeFn | None = None,
        align: AlignFn | None = None,
        align_batch: Callable[[list[AlignItem], Any], None] | None = None,
        mel_of: Callable[[np.ndarray], Any] | None = None,
        window_of: Callable[[Any, int, int], Any] | None = None,
        tokenizer: _Tokenizer | None = None,
        detect: Callable[[Any, list[int]], list[str]] | None = None,
    ) -> None:
        self.settings = settings or WhisperSettings()
        self._encode = encode
        self._decode = decode
        self._align = align
        self._mel_of = mel_of
        self._window_of = window_of
        self._tokenizer = tokenizer
        #: (the pass's encoder output, rows) -> each row's language, "" when unsure
        self._detect = detect
        #: every window of a pass aligned in one model call (stt/align.py)
        self._align_batch = align_batch
        self._inbox: queue.Queue[_Submission] = queue.Queue()
        self._thread: threading.Thread | None = None
        self._lock = threading.Lock()
        self.windows = 0  # windows decoded, for the report
        self.passes = 0
        self.in_flight = 0
        #: seconds of audio transcribed (the seeks' advance), for the live rate
        self.audio_done = 0.0
        #: of it, the part whose file then failed or was given up: not progress
        self.audio_lost = 0.0
        self._jobs: list[_Job] = []  # the files in flight (the GPU thread's list)
        self._mx: Any = None

    # --- public ---------------------------------------------------------------

    def in_flight_audio(self) -> float:
        """Seconds of audio decoded in the files not finished yet and not saved: what a
        stop now would throw away."""
        return sum(j.unsaved_sec for j in list(self._jobs))

    async def transcribe(
        self,
        audio: np.ndarray,
        *,
        resume: Checkpoint | None = None,
        progress: FileProgress | None = None,
        scale: float = 1.0,
        prompt: str | None = None,
    ) -> Transcript:
        """``audio``: mono float32 at 16 kHz. ``resume`` continues an earlier run from its
        checkpoint; ``progress`` receives this file's progress after every pass; ``scale``
        is how many seconds of the caller's audio one second of ``audio`` stands for
        (speech-only audio), for the live counters. ``prompt``: text that biases the
        decoding of every window of this file (Whisper's ``initial_prompt``)."""
        loop = asyncio.get_running_loop()
        fut: asyncio.Future[Transcript] = loop.create_future()
        self._ensure_thread()
        self._inbox.put((audio, fut, loop, resume, progress, scale, prompt or None))
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
            ApplyTimestampRules,
            DecodingOptions,
            DecodingTask,
            GreedyDecoder,
            detect_language,
        )
        from mlx_whisper.load_models import (  # type: ignore[import-untyped,import-not-found,unused-ignore]
            load_model,
        )
        from mlx_whisper.tokenizer import (  # type: ignore[import-untyped,import-not-found,unused-ignore]
            get_tokenizer,
        )

        s = self.settings
        t0 = time.monotonic()
        mx.set_memory_limit(int(s.gpu_limit_gb * 1e9))
        mx.set_cache_limit(int(s.gpu_cache_gb * 1e9))
        self._mx = mx
        from ..engine.health import register_metric

        tag = metric_tag()
        register_metric(f"stt.gpu_active_gb:{tag}", lambda: round(mx.get_active_memory() / 1e9, 2))
        register_metric(f"stt.gpu_cache_gb:{tag}", lambda: round(mx.get_cache_memory() / 1e9, 2))
        register_metric(f"stt.gpu_peak_gb:{tag}", lambda: round(mx.get_peak_memory() / 1e9, 2))
        self._register_metrics()
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

        def encode(windows: list[Any], cached: list[Any]) -> Any:
            # once a pass: the decode, and the word alignment after it, both read it
            todo = [k for k, c in enumerate(cached) if c is None]
            if len(todo) == len(windows):
                return model.encoder(mx.stack(windows))
            fresh: Any = model.encoder(mx.stack([windows[k] for k in todo])) if todo else []
            at = {k: i for i, k in enumerate(todo)}
            rows = [fresh[at[k]] if k in at else cached[k] for k in range(len(windows))]
            return mx.stack(rows)

        def run_decode(
            features: Any, temps: Sequence[float], prompt: str | None = None
        ) -> Sequence[_Decoded]:
            opts = DecodingOptions(
                language=s.language,
                task="transcribe",
                temperature=max(temps) or 0.0,
                without_timestamps=False,
                fp16=True,
                prompt=prompt,
            )
            task = DecodingTask(model, opts)
            if any(t > 0 for t in temps):
                task.decoder = RowGreedy(temps, tok.eot)
            task.logit_filters = [
                _fast_rules(mx, f) if isinstance(f, ApplyTimestampRules) else f
                for f in task.logit_filters
            ]
            # encoder output is recognised by its shape, and not encoded again
            return list(task.run(features))

        self._mel_of = mel_of
        self._window_of = window_of
        self._encode = encode
        self._decode = run_decode
        self._align = None

        def detect(features: Any, rows: list[int]) -> list[str]:
            # one decoder step on encoder output the pass already has
            _, probs = detect_language(model, mx.take(features, mx.array(rows), axis=0), tok)
            out = []
            for p in probs:
                code, prob = max(p.items(), key=lambda kv: kv[1])
                out.append(code if prob >= LANGUAGE_MIN_PROB else "")
            return out

        self._detect = detect
        if s.word_timestamps:
            self._align_batch = lambda items, feats: align_batch(model, tok, items, feats)
        _log.info("whisper.loaded", model=s.model, seconds=round(time.monotonic() - t0, 1))

    def _register_metrics(self) -> None:
        """The stt.* metrics every engine reports. One series per worker (two
        transcription workers on one host must not overwrite each other's), named by
        its slot so a restart continues it."""
        from ..engine.health import register_metric

        tag = metric_tag()
        register_metric(f"stt.files_in_flight:{tag}", lambda: float(self.in_flight))
        register_metric(f"stt.windows:{tag}", lambda: float(self.windows))
        # the report's live speed: audio decoded, less what a failure or a shutdown
        # threw away (the last sample, taken at shutdown, counts the files in flight)
        register_metric(f"stt.audio_sec:{tag}", lambda: round(self.audio_done, 1))
        register_metric(
            f"stt.audio_lost:{tag}",
            lambda: round(self.audio_lost, 1),
            final=lambda: round(self.audio_lost + self.in_flight_audio(), 1),
        )

    def _admit(self, jobs: list[_Job], block: bool) -> None:
        while len(jobs) < self.settings.max_batch:
            try:
                audio, fut, loop, resume, progress, scale, prompt = self._inbox.get(
                    block=block and not jobs, timeout=1.0
                )
            except queue.Empty:
                return
            block = False
            try:
                assert self._mel_of is not None
                mel = self._mel_of(audio)
                frames = int(mel.shape[0]) - N_FRAMES
                job = _Job(
                    mel=mel,
                    content_frames=max(0, frames),
                    audio_sec=len(audio) / SAMPLE_RATE,
                    future=fut,
                    loop=loop,
                    admitted=time.monotonic(),
                    progress=progress,
                    scale=scale,
                    prompt=prompt,
                )
                if resume is not None:
                    job.seek = min(resume.seek, job.content_frames)
                    job.last_speech = resume.last_speech
                    job.language = resume.language
                    job.segments = list(resume.segments)
                jobs.append(job)
            except Exception as exc:  # noqa: BLE001 - one file's failure is its own
                loop.call_soon_threadsafe(_set_exception, fut, exc)

    def _run(self) -> None:
        try:
            self._load()
        except Exception as exc:  # noqa: BLE001 - every waiting job learns why
            _log.exception("whisper.load_failed")
            while True:
                try:
                    _, fut, loop, _, _, _, _ = self._inbox.get_nowait()
                except queue.Empty:
                    break
                loop.call_soon_threadsafe(_set_exception, fut, exc)
            self._decode = None
            self._thread = None
            return
        jobs = self._jobs
        while True:
            self._admit(jobs, block=True)
            if not jobs:
                continue
            self.in_flight = len(jobs)
            try:
                self._pass(jobs)
                cap = self.settings.gpu_cache_gb * 1e9
                if self._mx is not None and self._mx.get_cache_memory() > cap:
                    self._mx.clear_cache()
            except Exception as exc:  # noqa: BLE001 - a broken batch fails its files only
                _log.exception("whisper.batch_failed", files=len(jobs))
                for j in jobs:
                    self.audio_lost += j.unsaved_sec
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
                        detected_language=j.language or None,
                        backend=self.backend,
                        backend_version=self.backend_version,
                    )
                except Exception as exc:  # noqa: BLE001 - the thread outlives one file
                    self.audio_lost += j.unsaved_sec
                    j.loop.call_soon_threadsafe(_set_exception, j.future, exc)
                    continue
                j.loop.call_soon_threadsafe(_set_result, j.future, result)

    def _decode_rows(
        self, features: Any, temps: list[float], prompts: list[str | None]
    ) -> list[_Decoded]:
        """The pass's decode. Files with a prompt are decoded apart, one call per
        prompt (a prompt is the decoder's prefix, the same for every row of a call);
        without prompts this is the one call it always was."""
        assert self._decode is not None
        if not any(prompts):
            return list(self._decode(features, temps))
        groups: dict[str | None, list[int]] = {}
        for k, p in enumerate(prompts):
            groups.setdefault(p, []).append(k)
        out: list[Any] = [None] * len(temps)
        for p, rows in groups.items():
            sub = self._take(features, rows)
            got = (
                self._decode(sub, [temps[k] for k in rows])
                if p is None
                else self._decode(sub, [temps[k] for k in rows], p)  # type: ignore[call-arg]
            )
            for k, r in zip(rows, got, strict=True):
                out[k] = r
        return out

    def _take(self, features: Any, rows: list[int]) -> Any:
        """Rows of the pass's encoder output (an MLX or a NumPy array) or of its windows
        (a list)."""
        if isinstance(features, list):
            return [features[k] for k in rows]
        if self._mx is not None:
            return self._mx.take(features, self._mx.array(rows), axis=0)
        return features[rows]

    def _pass(self, jobs: list[_Job]) -> None:
        """One GPU pass: one window of every active file, each at the temperature its
        window has reached (a fallback re-decodes the same window, warmer, next pass)."""
        s = self.settings
        assert self._decode is not None and self._window_of is not None
        assert self._tokenizer is not None
        for j in jobs:
            if j.future.cancelled() and not j.done:
                self.audio_lost += j.unsaved_sec
                j.seek = j.content_frames
        live = [j for j in jobs if not j.done]
        if not live:
            return
        sizes = [min(N_FRAMES, j.content_frames - j.seek) for j in live]
        windows = [self._window_of(j.mel, j.seek, n) for j, n in zip(live, sizes, strict=True)]
        if self._encode is not None:
            cached = [j.features if j.features_seek == j.seek else None for j in live]
            features = self._encode(windows, cached)
            first = [k for k, j in enumerate(live) if j.language is None]
            if first and self._detect is not None:
                for k, lang in zip(first, self._detect(features, first), strict=True):
                    live[k].language = lang
        else:
            features = windows
        results = self._decode_rows(features, [s.temperatures[j.temp_idx] for j in live],
                                    [j.prompt for j in live])  # fmt: skip
        self.passes += 1
        self.windows += len(windows)
        pending: list[tuple[_Job, list[dict[str, Any]], bool, float, Any, int, int]] = []
        before = {id(j): j.seek for j in live}
        for i, (j, n, win, r) in enumerate(zip(live, sizes, windows, results, strict=True)):
            if _needs_fallback(r, s) and j.temp_idx + 1 < len(s.temperatures):
                j.temp_idx += 1
                if self._encode is not None:  # the same window next pass: keep its encoding
                    j.features, j.features_seek = features[i], j.seek
                continue
            j.temp_idx = 0
            j.features, j.features_seek = None, -1
            got = window_segments(j, r, n, self._tokenizer, s)
            if got is not None:
                pending.append((j, got[0], got[1], got[2], win, n, i))
        if self._align_batch is not None:
            items = [AlignItem(cur, n, j.last_speech, i) for j, cur, _, _, _, n, i in pending]
            if items:
                self._align_batch(items, features)
        elif self._align is not None:
            for j, cur, _, _, win, n, _ in pending:
                self._align(cur, win, n, j.last_speech)
        for j, cur, single, offset, _, _, _ in pending:
            finish_window(j, cur, single, offset)
        for j in live:
            moved = min(j.seek, j.content_frames) - before[id(j)]
            if moved > 0:
                self.audio_done += moved / FRAMES_PER_SECOND * j.scale
            if j.progress is not None:
                j.progress.publish(j.seek, j.last_speech, j.language, j.segments)


def metric_tag() -> str:
    """This worker's suffix on the stt.* metrics: its slot on the host
    (``MAKTABA_STT_SLOT``, set by tools/dev/stack.sh), else its pid."""
    return os.environ.get("MAKTABA_STT_SLOT") or f"pid{os.getpid()}"


def _fast_rules(mx: Any, f: Any) -> Any:
    """MLX-Whisper's timestamp rules, replaced by the batched ones (stt/rules.py).

    They are built from the filter's own fields, which are MLX-Whisper's internals
    (pinned in pyproject.toml); if a release renames them, its filter is kept: slower,
    never wrong."""
    try:
        tok = f.tokenizer
        return TimestampRules(
            mx,
            sample_begin=int(f.sample_begin),
            timestamp_begin=int(tok.timestamp_begin),
            eot=int(tok.eot),
            no_timestamps=tok.no_timestamps,
            max_initial_timestamp_index=f.max_initial_timestamp_index,
        )
    except AttributeError:
        if not _WARNED:
            _WARNED.append(True)
            _log.warning("whisper.fast_rules_unavailable", filter=type(f).__name__)
        return f


_WARNED: list[bool] = []


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
