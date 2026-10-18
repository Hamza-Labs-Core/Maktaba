"""Speech only: Silero VAD finds the pauses, and Whisper decodes the speech packed together
(plan 03-10).

A lecture is about a quarter pauses, but mostly short ones; with faster-whisper's
``vad_filter`` settings (threshold 0.5, pauses of 2 s or more, 0.4 s kept around each
stretch of speech) 4.7 % of the library's audio goes (measured on 13 h of lectures). The
speech is concatenated, transcribed, and every sentence and word time is mapped back to
the file's own timeline (:class:`TimeMap`). Off by default (``MAKTABA_STT_VAD=1``): on
the GPU it saved 3 % and changed the text (see transcribe.use_vad).

The model is Silero VAD's ONNX file (from the ``silero-vad`` package, MIT), run with
onnxruntime on the CPU: about 400x realtime, alongside the GPU. Its package is not
imported (it imports torch), only its data file is read.
"""

from __future__ import annotations

import bisect
import importlib.util
import threading
from dataclasses import dataclass
from pathlib import Path
from typing import Any

__all__ = ["VadSettings", "TimeMap", "pack", "speech_chunks", "vad_available"]

SAMPLE_RATE = 16_000
WINDOW = 512  # samples per VAD step (32 ms)
CONTEXT = 64  # samples of the previous step the v5 model reads


@dataclass(frozen=True, slots=True)
class VadSettings:
    """faster-whisper's ``VadOptions`` defaults."""

    threshold: float = 0.5
    min_speech_ms: int = 250
    min_silence_ms: int = 2000
    speech_pad_ms: int = 400


def _model_path() -> Path | None:
    spec = importlib.util.find_spec("silero_vad")
    if spec is None or not spec.submodule_search_locations:
        return None
    path = Path(next(iter(spec.submodule_search_locations))) / "data" / "silero_vad.onnx"
    return path if path.exists() else None


def vad_available() -> bool:
    return _model_path() is not None and importlib.util.find_spec("onnxruntime") is not None


_SESSION: Any = None
_LOCK = threading.Lock()


def _session() -> Any:
    global _SESSION
    with _LOCK:
        if _SESSION is None:
            import onnxruntime as ort  # type: ignore[import-untyped,import-not-found,unused-ignore]

            path = _model_path()
            if path is None:
                raise RuntimeError("silero-vad is not installed (the stt extra)")
            opts = ort.SessionOptions()
            opts.inter_op_num_threads = 1
            opts.intra_op_num_threads = 1
            _SESSION = ort.InferenceSession(
                str(path), sess_options=opts, providers=["CPUExecutionProvider"]
            )
        return _SESSION


def speech_probs(audio: Any) -> list[float]:
    """Silero's speech probability for every 32 ms of ``audio`` (float32, 16 kHz)."""
    import numpy as np

    sess = _session()
    state = np.zeros((2, 1, 128), np.float32)
    ctx = np.zeros((1, CONTEXT), np.float32)
    sr = np.array(SAMPLE_RATE, np.int64)
    out: list[float] = []
    for i in range(len(audio) // WINDOW):
        x = np.asarray(audio[i * WINDOW : (i + 1) * WINDOW], np.float32)[None]
        p, state = sess.run(None, {"input": np.concatenate([ctx, x], 1), "state": state, "sr": sr})
        ctx = x[:, -CONTEXT:]
        out.append(float(p[0][0]))
    return out


def chunks_from_probs(probs: list[float], n_samples: int, s: VadSettings) -> list[tuple[int, int]]:
    """Silero's ``get_speech_timestamps`` (as faster-whisper calls it): stretches of
    speech in samples, each padded, merged across pauses shorter than twice the pad."""
    min_speech = SAMPLE_RATE * s.min_speech_ms // 1000
    min_silence = SAMPLE_RATE * s.min_silence_ms // 1000
    pad = SAMPLE_RATE * s.speech_pad_ms // 1000
    neg = s.threshold - 0.15
    speeches: list[list[int]] = []
    start, temp_end, triggered = 0, 0, False
    for i, p in enumerate(probs):
        cur = i * WINDOW
        if p >= s.threshold and temp_end:
            temp_end = 0
        if p >= s.threshold and not triggered:
            triggered, start = True, cur
            continue
        if p < neg and triggered:
            if not temp_end:
                temp_end = cur
            if cur - temp_end < min_silence:
                continue
            if temp_end - start > min_speech:
                speeches.append([start, temp_end])
            triggered, temp_end = False, 0
    if triggered and n_samples - start > min_speech:
        speeches.append([start, n_samples])
    for i, sp in enumerate(speeches):
        if i == 0:
            sp[0] = max(0, sp[0] - pad)
        if i < len(speeches) - 1:
            gap = speeches[i + 1][0] - sp[1]
            if gap < 2 * pad:
                sp[1] += gap // 2
                speeches[i + 1][0] = max(0, speeches[i + 1][0] - gap // 2)
            else:
                sp[1] = min(n_samples, sp[1] + pad)
                speeches[i + 1][0] = max(0, speeches[i + 1][0] - pad)
        else:
            sp[1] = min(n_samples, sp[1] + pad)
    return [(a, b) for a, b in speeches]


def speech_chunks(audio: Any, s: VadSettings | None = None) -> list[tuple[int, int]]:
    return chunks_from_probs(speech_probs(audio), len(audio), s or VadSettings())


class TimeMap:
    """Packed time (seconds into the speech-only audio) -> the file's own time."""

    def __init__(self, chunks: list[tuple[int, int]]) -> None:
        self.packed_starts: list[float] = []
        self.orig_starts: list[float] = []
        at = 0
        for a, b in chunks:
            self.packed_starts.append(at / SAMPLE_RATE)
            self.orig_starts.append(a / SAMPLE_RATE)
            at += b - a
        self.packed_sec = at / SAMPLE_RATE

    def __call__(self, t: float, *, end: bool = False) -> float:
        """``end``: the time ends something (a sentence, a word), so one exactly at a cut
        belongs to the stretch before it, not after the removed pause."""
        if not self.packed_starts:
            return t
        find = bisect.bisect_left if end else bisect.bisect_right
        i = max(0, find(self.packed_starts, t) - 1)
        return round(self.orig_starts[i] + (t - self.packed_starts[i]), 3)


def pack(audio: Any, chunks: list[tuple[int, int]]) -> tuple[Any, TimeMap]:
    """The speech of ``audio`` end to end, and the map back to its own timeline."""
    import numpy as np

    if not chunks:
        return np.zeros(0, np.float32), TimeMap([])
    return np.concatenate([audio[a:b] for a, b in chunks]), TimeMap(chunks)
