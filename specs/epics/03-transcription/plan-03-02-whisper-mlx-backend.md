---
name: Plan 03-02 — Whisper MLX backend
description: Implementation plan for Epic 3 Story 2 (Whisper MLX backend). Wraps `mlx-whisper` as the default Apple-Silicon STT backend; covers async streaming over a sync mlx generator, OOM auto-degradation, hallucination-loop detection, NFC normalization, and the resume path via initial-prompt + start_offset.
type: plan
---

# Plan 03-02 — Whisper MLX Backend (default on Apple Silicon)

> **Canonical story:** [story-03-02-whisper-mlx-backend.md](story-03-02-whisper-mlx-backend.md).
>
> **Depends on:** [Plan 03-01](plan-03-01-backend-protocol.md) — the
> `STTBackend` Protocol, `Segment`, `Word`, `BackendHealth`,
> `TranscriptionHints`, `AudioSource`, error taxonomy, and the
> `stt_conformance_suite` fixture. This plan implements one concrete
> backend and consumes the suite; it does **not** modify the protocol.
>
> **Architecture references.** [§3.4 Transcriber (pluggable STT)](../../architecture.md)
> lists `whisper-mlx` as the default Apple-Silicon backend at ~0.3× RT;
> [§11.4 Example pipeline.toml](../../architecture.md) defines
> `[stt.default] backend = "whisper-mlx"` and the `initial_prompt_ar`
> default; [§7.6 Real-time progress persistence](../../architecture.md)
> defines per-segment commit, which is what makes streaming valuable
> here.
>
> **Out of scope.** Backend registry / fallback chain (3.5). Per-segment
> DB commit (3.6). Pause / resume orchestration (3.7) — this backend
> *honors* `start_offset_sec` on the input side, but the surrounding
> job-level pause is 3.7. CUDA / CPU fallback (3.3). OpenAI API (3.4).
> Diarization (3.9).

---

## 1. Architecture diagram — backend internals

```
       Orchestrator (transcribe stage, 3.5/3.6)
              │  audio: AudioSource (PCM iter or path)
              │  language: "ar" | "en" | None
              │  hints: TranscriptionHints
              ▼
┌───────────────────────────────────────────────────────────────┐
│  WhisperMLXBackend  ── pipeline/stt/whisper_mlx/backend.py   │
│                                                               │
│  __init__(model="large-v3", degrade_on_oom=True, ...)        │
│   - resolves model id via _MODEL_TABLE                        │
│   - lazily loads on first warmup() / transcribe()             │
│                                                               │
│  async transcribe(audio, language, hints) ────────────┐      │
│   1. Materialize audio                                │      │
│      - file path  → use directly                      │      │
│      - PCM iter   → drain to scratch float32 array    │      │
│        (mlx_whisper takes np.ndarray or path)         │      │
│   2. Apply seek: drop first start_offset_sec samples  │      │
│   3. Build mlx_whisper kwargs:                        │      │
│        word_timestamps = hints.word_timestamps        │      │
│        initial_prompt  = hints.initial_prompt or      │      │
│                          settings.stt.initial_prompt_ar│      │
│        language        = language (None ⇒ auto)       │      │
│        condition_on_previous_text = True              │      │
│        no_speech_threshold = 0.6                      │      │
│   4. Run mlx_whisper.transcribe in a worker thread    │      │
│      via asyncio.to_thread, pushing each emitted seg  │      │
│      onto an asyncio.Queue. Bridge sync→async iter.   │      │
│   5. For each raw segment from the queue:             │      │
│        - normalize text (NFC, strip BOM, trim)        │      │
│        - rebase timestamps by +start_offset_sec       │      │
│        - check hallucination loop (Levenshtein)       │      │
│        - drop empty-text silence segments             │      │
│        - yield Segment(...)                           │      │
│                                                               │
│  detect_language(audio):                                      │
│    Drains first 30 s, calls mlx_whisper.load_model + the      │
│    decoder's `detect_language` head, returns ISO 639-1.       │
│                                                               │
│  health():                                                    │
│    ready = arch == "arm64" and platform.system() == "Darwin"  │
│            and mlx_whisper importable and model file present  │
│    model_loaded = self._model is not None                     │
│    device = "mlx"                                             │
│                                                               │
│  warmup(): loads the model; idempotent                        │
│  close():  releases model, calls mx.metal.clear_cache()       │
└───────────────────────────────────────────────────────────────┘
                 │
                 │ on RuntimeError("out of memory") / mx errors
                 ▼
       BackendOOM raised; orchestrator (3.5) consults
       library settings.degrade_on_oom and reschedules
       with a smaller model id from _MODEL_TABLE.
```

Five things to notice:

1. **mlx-whisper is fundamentally synchronous.** Its top-level
   `transcribe(audio, ...)` is a regular function that returns a dict;
   *however*, when called with `verbose=False` and the
   `_segment_callback` hook (see §3.4), it emits segments at boundary
   points before the whole decode finishes. We bridge that callback to
   an `asyncio.Queue` and yield from the queue.
2. **PCM streams are drained to memory.** mlx-whisper accepts numpy
   arrays, so we accumulate PCM chunks into one `np.float32` buffer
   before invoking the decoder. This is acceptable: 30 s of mono 16 kHz
   audio is 1.92 MB; a 90-minute file is ~345 MB, well within the
   memory budget of an Apple-Silicon worker. (The *transcribe* stage
   does still benefit from streaming because segments are committed as
   they arrive — that part is preserved.)
3. **`requires_file = False`** because we accept PCM iters too. But the
   conformance fixtures hand the backend a path, so the path code path
   is the well-trodden one.
4. **Resume is implemented in the input layer**, not in the model. We
   trim the audio before invoking mlx-whisper and rebase timestamps
   on the way out. mlx-whisper has no "resume from t" knob.
5. **The backend never writes to the DB.** Segments come out of
   `transcribe()`; the orchestrator (3.6) commits them. This keeps the
   conformance suite (3.1) green without standing up Postgres.

---

## 2. New artifacts

| Layer | Path | Status | Purpose |
|---|---|---|---|
| Python | `pipeline/src/maktaba_pipeline/stt/whisper_mlx/__init__.py` | **new** | Package marker; exports `WhisperMLXBackend`. |
| Python | `pipeline/src/maktaba_pipeline/stt/whisper_mlx/backend.py` | **new** | The backend class implementing the Protocol. |
| Python | `pipeline/src/maktaba_pipeline/stt/whisper_mlx/_models.py` | **new** | `_MODEL_TABLE` mapping model ids ↔ HF repo paths and a `degrade_chain()` helper (`large-v3` → `medium` → `small` → `tiny`). |
| Python | `pipeline/src/maktaba_pipeline/stt/whisper_mlx/_audio.py` | **new** | `drain_pcm_to_float32(source)` and `seek_samples(arr, start_offset_sec)` helpers. |
| Python | `pipeline/src/maktaba_pipeline/stt/whisper_mlx/_normalize.py` | **new** | `normalize_text(s)` — NFC + BOM strip + whitespace trim; bidi marks left to renderer (story 3.2 acceptance). |
| Python | `pipeline/src/maktaba_pipeline/stt/whisper_mlx/_hallucination.py` | **new** | Sliding-window hallucination-loop detector (story 3.2 edge case). |
| Python | `pipeline/src/maktaba_pipeline/stt/whisper_mlx/_runtime.py` | **new** | Platform probe — `is_apple_silicon()`, mlx availability check, model-file presence check. |
| Python | `pipeline/src/maktaba_pipeline/stt/whisper_mlx/tests/test_conformance.py` | **new** | Plugs the backend into `stt_conformance_suite` (Plan 03-01); skipped on non-arm64-darwin. |
| Python | `pipeline/src/maktaba_pipeline/stt/whisper_mlx/tests/test_initial_prompt.py` | **new** | Story 3.2 test case — `hints.initial_prompt` raises confidence on Arabic religious vocabulary. |
| Python | `pipeline/src/maktaba_pipeline/stt/whisper_mlx/tests/test_language_autodetect.py` | **new** | Story 3.2 test case — Arabic file with `language=None` → `ar`. |
| Python | `pipeline/src/maktaba_pipeline/stt/whisper_mlx/tests/test_apple_silicon_only.py` | **new** | On non-arm64-darwin, `health().ready == False`. |
| Python | `pipeline/src/maktaba_pipeline/stt/whisper_mlx/tests/test_oom_degradation.py` | **new** | Edge case — patched `mlx_whisper.transcribe` raises `RuntimeError("out of memory")`; backend raises `BackendOOM`. |
| Python | `pipeline/src/maktaba_pipeline/stt/whisper_mlx/tests/test_hallucination_loop.py` | **new** | Edge case — feed canned segments to the detector; force-decode-window decision is made and recorded. |
| Python | `pipeline/src/maktaba_pipeline/stt/whisper_mlx/tests/test_normalize.py` | **new** | Unit tests for `normalize_text`. |
| Python | `pipeline/src/maktaba_pipeline/stt/whisper_mlx/tests/test_resume_offset.py` | **new** | `start_offset_sec` trims input and rebases output. |
| Python | `pipeline/src/maktaba_pipeline/stt/whisper_mlx/tests/test_warmup_idempotent.py` | **new** | `await warmup()` twice loads model once. |
| Config | `pipeline/pyproject.toml` | **edit** | Add `mlx-whisper` and `mlx` as optional `[mlx]` extras (so `pip install '.[mlx]'` is needed only on Apple Silicon). |
| CI | `.github/workflows/pipeline.yml` | **edit** | Add a macOS-arm64 matrix entry that installs the `[mlx]` extra and runs `test_conformance.py`; existing Linux entries run `pytest -k 'not whisper_mlx'`. |
| Docs | `pipeline/src/maktaba_pipeline/stt/whisper_mlx/README.md` | **new** | Half-page "what this backend does, when it skips itself, how to swap models". |

---

## 3. Implementation

### 3.1 `_models.py`

```python
"""Whisper model id ↔ HF repo + the degrade chain."""
from __future__ import annotations

from typing import Final

# mlx-community publishes ggml/mlx Whisper checkpoints. We hard-pin
# revisions in pipeline/settings.py, not here, so this table is just
# the canonical id mapping.
_MODEL_TABLE: Final[dict[str, str]] = {
    "large-v3":      "mlx-community/whisper-large-v3-mlx",
    "large-v3-turbo": "mlx-community/whisper-large-v3-turbo",
    "medium":        "mlx-community/whisper-medium-mlx",
    "small":         "mlx-community/whisper-small-mlx",
    "tiny":          "mlx-community/whisper-tiny-mlx",
}

_DEGRADE_CHAIN: Final[list[str]] = ["large-v3", "medium", "small", "tiny"]


def repo_for(model_id: str) -> str:
    if model_id not in _MODEL_TABLE:
        raise ValueError(f"unknown whisper-mlx model {model_id!r}")
    return _MODEL_TABLE[model_id]


def next_smaller(model_id: str) -> str | None:
    """Return the next smaller model in the degrade chain, or None if
    we're already at the smallest."""
    try:
        i = _DEGRADE_CHAIN.index(model_id)
    except ValueError:
        return "medium"  # unknown id (e.g. turbo) → safe fallback
    return _DEGRADE_CHAIN[i + 1] if i + 1 < len(_DEGRADE_CHAIN) else None
```

### 3.2 `_runtime.py`

```python
"""Platform probes; cheap and import-safe (no model load)."""
from __future__ import annotations

import platform
from importlib.util import find_spec


def is_apple_silicon() -> bool:
    return platform.system() == "Darwin" and platform.machine() == "arm64"


def mlx_whisper_importable() -> bool:
    return find_spec("mlx_whisper") is not None and find_spec("mlx") is not None


def model_present(repo: str) -> bool:
    """Hugging Face cache-only check; we never download from health()."""
    from huggingface_hub import try_to_load_from_cache  # noqa: PLC0415
    return try_to_load_from_cache(repo, "config.json") is not None
```

### 3.3 `_normalize.py` and `_hallucination.py`

```python
# _normalize.py
import unicodedata


def normalize_text(text: str) -> str:
    s = unicodedata.normalize("NFC", text)
    s = s.lstrip("﻿").strip()  # strip BOM + whitespace
    # Collapse runs of whitespace (mlx-whisper occasionally double-spaces).
    return " ".join(s.split())
```

```python
# _hallucination.py
"""Detect Whisper's repetition-loop hallucination — ≥3 consecutive
segments whose text is within Levenshtein distance ≤2 of each other
(and longer than 10 chars). On detection, we surface a flag the
backend uses to force a new decode window via the `condition_on_previous_text`
toggle, and we accumulate a count for transcripts.metadata.hallucination_breaks.
"""
from __future__ import annotations

from collections import deque

from rapidfuzz.distance import Levenshtein


class HallucinationGuard:
    def __init__(self, window: int = 3, max_distance: int = 2, min_len: int = 10):
        self._window: deque[str] = deque(maxlen=window)
        self._max_distance = max_distance
        self._min_len = min_len
        self.breaks = 0

    def observe(self, text: str) -> bool:
        """Return True when a loop is detected. The caller flips the
        decode window and clears the guard via .reset().
        """
        if len(text) < self._min_len:
            self._window.clear()
            return False
        self._window.append(text)
        if len(self._window) < self._window.maxlen:
            return False
        a, b, c = self._window
        if (
            Levenshtein.distance(a, b) <= self._max_distance
            and Levenshtein.distance(b, c) <= self._max_distance
        ):
            self.breaks += 1
            return True
        return False

    def reset(self) -> None:
        self._window.clear()
```

### 3.4 `backend.py` (the heart)

```python
"""WhisperMLXBackend — Apple-Silicon-default STT backend.

Notes on threading:

mlx_whisper.transcribe is sync. We run it in asyncio.to_thread() and
inject a `_segment_callback` (a small monkeypatch on the verbose=False
loop) that pushes each emitted Whisper segment onto an asyncio.Queue.
The backend's async iterator pulls from the queue. The thread signals
EOF by pushing a sentinel.

This avoids spawning a child process per transcription (mlx benefits
from re-using the loaded model in-process) while still letting the
caller stream segments and cancel cooperatively.
"""
from __future__ import annotations

import asyncio
import time
from collections.abc import AsyncIterator
from pathlib import Path

import numpy as np

from ..errors import BackendFatal, BackendNotReady, BackendOOM
from ..types import (
    AudioSource,
    BackendHealth,
    Segment,
    TranscriptionHints,
    Word,
    _FilePath,
    _Pcm16Mono16k,
)
from ._audio import drain_pcm_to_float32, seek_samples
from ._hallucination import HallucinationGuard
from ._models import repo_for
from ._normalize import normalize_text
from ._runtime import is_apple_silicon, mlx_whisper_importable, model_present


_SAMPLE_RATE = 16_000
_SENTINEL = object()


class WhisperMLXBackend:
    name = "whisper-mlx"
    supports_streaming = True
    supports_word_timestamps = True
    requires_file = False
    cost_per_minute: float | None = 0.0

    def __init__(
        self,
        model: str = "large-v3",
        *,
        initial_prompt_default: str = "بسم الله الرحمن الرحيم",
        no_speech_threshold: float = 0.6,
    ) -> None:
        self.model_id = model
        self._repo = repo_for(model)
        self._initial_prompt_default = initial_prompt_default
        self._no_speech_threshold = no_speech_threshold
        self._loaded = False  # set by warmup(); we re-use across calls
        self._mlx = None  # lazily imported

    # ------------------------------------------------------------------ Lifecycle

    async def warmup(self) -> None:
        if self._loaded:
            return
        if not (is_apple_silicon() and mlx_whisper_importable()):
            raise BackendNotReady(f"{self.name} requires arm64-darwin + mlx_whisper")
        import mlx_whisper  # noqa: PLC0415
        # mlx_whisper loads on first transcribe; we kick a no-op decode of
        # 0.5 s of silence to force model + tokenizer into RAM.
        silence = np.zeros(_SAMPLE_RATE // 2, dtype=np.float32)
        await asyncio.to_thread(
            mlx_whisper.transcribe,
            silence, path_or_hf_repo=self._repo, language="en",
            verbose=False, word_timestamps=False,
        )
        self._mlx = mlx_whisper
        self._loaded = True

    async def close(self) -> None:
        if not self._loaded:
            return
        try:
            import mlx.core as mx  # noqa: PLC0415
            mx.metal.clear_cache()
        except ImportError:
            pass
        self._loaded = False
        self._mlx = None

    async def health(self) -> BackendHealth:
        ready = (
            is_apple_silicon()
            and mlx_whisper_importable()
            and model_present(self._repo)
        )
        return BackendHealth(
            ready=ready,
            model_loaded=self._loaded,
            version=_get_mlx_version() or "unknown",
            device="mlx" if ready else "unknown",
            last_check_at=time.time(),
            reason=None if ready else _explain_not_ready(self._repo),
        )

    # ------------------------------------------------------------------ Inference

    async def detect_language(self, audio: AudioSource) -> str:
        await self.warmup()
        arr = await self._materialize(audio, max_seconds=30.0)
        # mlx_whisper exposes `detect_language` on its model; if not
        # accessible at the package level, fall through to a no-op
        # transcribe with `language=None` and read the result key.
        result = await asyncio.to_thread(
            self._mlx.transcribe,
            arr, path_or_hf_repo=self._repo,
            language=None, verbose=False, word_timestamps=False,
        )
        lang = result.get("language", "en")
        return lang[:2]  # whisper sometimes returns full names

    async def transcribe(
        self,
        audio: AudioSource,
        language: str | None,
        hints: TranscriptionHints,
    ) -> AsyncIterator[Segment]:
        await self.warmup()
        arr = await self._materialize(audio)
        arr = seek_samples(arr, hints.start_offset_sec, _SAMPLE_RATE)

        prompt = hints.initial_prompt or (
            self._initial_prompt_default if language == "ar" else None
        )
        guard = HallucinationGuard()

        queue: asyncio.Queue = asyncio.Queue()
        loop = asyncio.get_running_loop()

        def on_segment(raw):
            loop.call_soon_threadsafe(queue.put_nowait, raw)

        def run() -> None:
            try:
                self._mlx.transcribe(
                    arr,
                    path_or_hf_repo=self._repo,
                    language=language,
                    initial_prompt=prompt,
                    word_timestamps=hints.word_timestamps,
                    no_speech_threshold=self._no_speech_threshold,
                    condition_on_previous_text=True,
                    verbose=False,
                    _segment_callback=on_segment,  # see §3.5 for shim
                )
            except Exception as exc:
                loop.call_soon_threadsafe(queue.put_nowait, exc)
            finally:
                loop.call_soon_threadsafe(queue.put_nowait, _SENTINEL)

        worker = asyncio.create_task(asyncio.to_thread(run))
        seq = 0
        try:
            while True:
                item = await queue.get()
                if item is _SENTINEL:
                    return
                if isinstance(item, BaseException):
                    if _is_oom(item):
                        raise BackendOOM(str(item)) from item
                    raise BackendFatal(repr(item)) from item

                seg = self._convert(item, seq, hints, offset=hints.start_offset_sec)
                if not seg.text:
                    continue  # silence segment; drop (story 3.1 edge case)
                if guard.observe(seg.text):
                    seg.metadata = (seg.metadata or {}) | {
                        "hallucination_break": guard.breaks
                    }
                    guard.reset()
                yield seg
                seq += 1
        finally:
            if not worker.done():
                worker.cancel()
                try:
                    await worker
                except (asyncio.CancelledError, BaseException):
                    pass

    # ------------------------------------------------------------------ Helpers

    async def _materialize(self, audio: AudioSource, *, max_seconds: float | None = None) -> np.ndarray:
        match audio:
            case _FilePath(path=p):
                return _read_wav_to_float32(p, max_seconds=max_seconds)
            case _Pcm16Mono16k(chunks=ch):
                return await drain_pcm_to_float32(ch, max_seconds=max_seconds)
        raise BackendFatal(f"unsupported AudioSource: {audio}")

    def _convert(self, raw: dict, seq: int, hints: TranscriptionHints, *, offset: float) -> Segment:
        words: list[Word] | None = None
        if hints.word_timestamps and raw.get("words"):
            words = [
                Word(
                    seq=i,
                    start=float(w["start"]) + offset,
                    end=float(w["end"]) + offset,
                    text=normalize_text(w["word"]),
                    confidence=w.get("probability"),
                )
                for i, w in enumerate(raw["words"])
            ]
        return Segment(
            seq=seq,
            start=float(raw["start"]) + offset,
            end=float(raw["end"]) + offset,
            text=normalize_text(raw["text"]),
            confidence=raw.get("avg_logprob"),
            words=words,
            metadata={
                "no_speech_prob": float(raw.get("no_speech_prob", 0.0)),
                "compression_ratio": float(raw.get("compression_ratio", 0.0)),
            },
        )


def _is_oom(exc: BaseException) -> bool:
    msg = str(exc).lower()
    return isinstance(exc, RuntimeError) and ("out of memory" in msg or "oom" in msg)


def _read_wav_to_float32(path: Path, *, max_seconds: float | None) -> np.ndarray:
    """Read a 16 kHz mono WAV produced by Plan 02-03's file extractor."""
    import wave  # noqa: PLC0415
    with wave.open(str(path), "rb") as wf:
        assert wf.getnchannels() == 1
        assert wf.getframerate() == _SAMPLE_RATE
        n_frames = wf.getnframes()
        if max_seconds is not None:
            n_frames = min(n_frames, int(max_seconds * _SAMPLE_RATE))
        raw = wf.readframes(n_frames)
    return np.frombuffer(raw, dtype=np.int16).astype(np.float32) / 32768.0


def _get_mlx_version() -> str | None:
    try:
        import mlx_whisper  # noqa: PLC0415
        return getattr(mlx_whisper, "__version__", None)
    except ImportError:
        return None


def _explain_not_ready(repo: str) -> str:
    if not is_apple_silicon():
        return f"requires arm64-darwin (got {platform_str()})"
    if not mlx_whisper_importable():
        return "mlx_whisper not installed (try `pip install '.[mlx]'`)"
    if not model_present(repo):
        return f"model not in cache: {repo!r}; run `huggingface-cli download {repo}`"
    return "unknown"


def platform_str() -> str:
    import platform  # noqa: PLC0415
    return f"{platform.system().lower()}-{platform.machine()}"
```

### 3.5 The `_segment_callback` shim

`mlx_whisper.transcribe` does not officially expose a per-segment
callback. Two options:

1. **Pin a known revision** that exposes a hook; mlx_whisper >= 0.4 has
   one in its `decode_loop` internals (we patch a tiny wrapper into the
   loaded module on first import).
2. **Subclass the decoder** and pass it as `model=` (cleaner but more
   coupled to mlx_whisper internals).

We ship option 1 with a thin `_install_callback_shim()` called once at
module import that monkey-patches `mlx_whisper.transcribe` to invoke
`kwargs.pop("_segment_callback", None)` after every segment is decoded.
The patch is idempotent and gated on the mlx_whisper version pin
declared in `pyproject.toml`. If the upstream API changes, the shim
fails loudly at import — better than a silent fallback.

### 3.6 `_audio.py`

```python
"""PCM helpers for the MLX backend."""
from collections.abc import AsyncIterator

import numpy as np


async def drain_pcm_to_float32(
    chunks: AsyncIterator[bytes],
    *,
    max_seconds: float | None = None,
    sample_rate: int = 16_000,
) -> np.ndarray:
    parts: list[np.ndarray] = []
    samples = 0
    cap = int(max_seconds * sample_rate) if max_seconds else None
    async for chunk in chunks:
        if not chunk:
            continue
        arr = np.frombuffer(chunk, dtype=np.int16)
        if cap is not None and samples + arr.size >= cap:
            parts.append(arr[: cap - samples])
            break
        parts.append(arr)
        samples += arr.size
    if not parts:
        return np.zeros(0, dtype=np.float32)
    return np.concatenate(parts).astype(np.float32) / 32768.0


def seek_samples(arr: np.ndarray, offset_sec: float, sample_rate: int) -> np.ndarray:
    if offset_sec <= 0.0:
        return arr
    n = int(offset_sec * sample_rate)
    return arr[n:] if n < arr.size else np.zeros(0, dtype=arr.dtype)
```

---

## 4. Test plan

### 4.1 `test_conformance.py`

```python
import platform

import pytest

from maktaba_pipeline.stt.conformance.suite import stt_conformance_suite
from maktaba_pipeline.stt.whisper_mlx import WhisperMLXBackend


pytestmark = pytest.mark.skipif(
    platform.system() != "Darwin" or platform.machine() != "arm64",
    reason="whisper-mlx is Apple-Silicon only",
)


@pytest.fixture(scope="module")
def backend():
    # Use tiny for CI speed; conformance suite tolerances are lenient.
    b = WhisperMLXBackend(model="tiny")
    yield b


def test_conformance(backend):
    stt_conformance_suite(backend)
```

### 4.2 `test_initial_prompt.py`

```python
"""Story 3.2 acceptance: hints.initial_prompt biases Arabic decode."""
import pytest

from maktaba_pipeline.stt.audio_source import from_file
from maktaba_pipeline.stt.types import TranscriptionHints
from maktaba_pipeline.stt.whisper_mlx import WhisperMLXBackend
from .helpers import open_arabic_fixture


@pytest.mark.skipif_no_mlx
async def test_initial_prompt_lifts_confidence():
    backend = WhisperMLXBackend(model="tiny")
    src = from_file(open_arabic_fixture())
    confs_with: list[float] = []
    confs_without: list[float] = []

    hints_with = TranscriptionHints(initial_prompt="بسم الله الرحمن الرحيم")
    async for s in backend.transcribe(src, "ar", hints_with):
        if s.confidence is not None:
            confs_with.append(s.confidence)

    src2 = from_file(open_arabic_fixture())
    hints_without = TranscriptionHints(initial_prompt="")
    async for s in backend.transcribe(src2, "ar", hints_without):
        if s.confidence is not None:
            confs_without.append(s.confidence)

    # Whisper avg_logprob is negative; less negative is better.
    assert mean(confs_with) >= mean(confs_without) - 0.05
```

### 4.3 `test_oom_degradation.py`

```python
"""Edge case: mlx_whisper raises RuntimeError('out of memory') →
backend raises BackendOOM. Orchestrator (story 3.5) decides whether
to degrade. We do NOT degrade inside the backend."""
import pytest

from maktaba_pipeline.stt.audio_source import from_file
from maktaba_pipeline.stt.errors import BackendOOM
from maktaba_pipeline.stt.types import TranscriptionHints
from maktaba_pipeline.stt.whisper_mlx import WhisperMLXBackend


async def test_oom_raised(monkeypatch):
    backend = WhisperMLXBackend(model="tiny")
    await backend.warmup()

    def boom(*a, **kw):
        raise RuntimeError("Metal: out of memory while compiling kernel")

    monkeypatch.setattr(backend._mlx, "transcribe", boom)

    with pytest.raises(BackendOOM):
        async for _ in backend.transcribe(
            from_file(_silence_fixture()), "en", TranscriptionHints()
        ):
            pass


def test_next_smaller_chain():
    from maktaba_pipeline.stt.whisper_mlx._models import next_smaller
    assert next_smaller("large-v3") == "medium"
    assert next_smaller("medium") == "small"
    assert next_smaller("small") == "tiny"
    assert next_smaller("tiny") is None
```

### 4.4 `test_hallucination_loop.py`

```python
from maktaba_pipeline.stt.whisper_mlx._hallucination import HallucinationGuard


def test_three_identical_long_lines_trigger():
    g = HallucinationGuard()
    text = "اللهم صلي على محمد وعلى آل محمد كما"
    assert not g.observe(text)
    assert not g.observe(text)
    assert g.observe(text)
    assert g.breaks == 1


def test_short_lines_do_not_trigger():
    g = HallucinationGuard()
    for _ in range(5):
        assert not g.observe("نعم")
    assert g.breaks == 0


def test_distance_above_threshold_does_not_trigger():
    g = HallucinationGuard()
    assert not g.observe("the quick brown fox jumps over the lazy dog")
    assert not g.observe("then quack brown fish jumps over the lazy frog")
    assert not g.observe("a totally different sentence about something else")
    assert g.breaks == 0
```

### 4.5 `test_normalize.py`

```python
from maktaba_pipeline.stt.whisper_mlx._normalize import normalize_text


def test_nfc():
    decomposed = "á"  # 'a' + combining acute
    assert normalize_text(decomposed) == "á"


def test_strips_bom_and_trims():
    assert normalize_text("﻿   hello   ") == "hello"


def test_collapses_whitespace():
    assert normalize_text("foo    bar\tbaz") == "foo bar baz"


def test_preserves_arabic():
    s = "السلام عليكم"
    assert normalize_text(s) == s
```

### 4.6 `test_resume_offset.py`

```python
from maktaba_pipeline.stt.whisper_mlx._audio import seek_samples
import numpy as np


def test_seek_zero_is_identity():
    a = np.arange(16000, dtype=np.float32)
    assert seek_samples(a, 0.0, 16000) is a


def test_seek_one_second_drops_16k():
    a = np.arange(32000, dtype=np.float32)
    out = seek_samples(a, 1.0, 16000)
    assert out.size == 16000
    assert out[0] == 16000.0


def test_seek_past_end_returns_empty():
    a = np.zeros(1000, dtype=np.float32)
    out = seek_samples(a, 10.0, 16000)
    assert out.size == 0


# Integration test: feed a 30 s audio + start_offset_sec=10 →
# segments returned have start >= 10.0 (rebased).
```

### 4.7 `test_apple_silicon_only.py`

```python
import platform

import pytest

from maktaba_pipeline.stt.whisper_mlx import WhisperMLXBackend


async def test_health_false_off_apple_silicon(monkeypatch):
    monkeypatch.setattr(platform, "machine", lambda: "x86_64")
    h = await WhisperMLXBackend(model="tiny").health()
    assert h.ready is False
    assert h.device == "unknown"
    assert "arm64" in (h.reason or "")
```

### 4.8 `test_warmup_idempotent.py`

Calls `await backend.warmup()` twice. Asserts `mlx_whisper.transcribe`
is invoked exactly once (the silence-prime) by patching it with a
counter.

---

## 5. Edge cases (story 3.2) — explicit handling

| Story §Edge case | Handling here |
|---|---|
| **Out-of-VRAM.** | `_is_oom()` matches `RuntimeError("out of memory")`/`oom`. Backend raises `BackendOOM`. Orchestrator (3.5) consults `library.settings.degrade_on_oom`; if true, picks `_models.next_smaller(current)` and reschedules with `metadata.degraded_from = current`. The backend itself never makes the policy decision. |
| **Repeated identical segments (hallucination loop).** | `HallucinationGuard.observe()` runs on every emitted segment text. On a hit, the backend annotates `Segment.metadata.hallucination_break` and resets the guard's window. The `condition_on_previous_text=True` flag is intentionally left on so the next decode window naturally diverges; we do *not* retry the segment, because the segment IS valid output — we only flag it for downstream display. The aggregate count is the orchestrator's responsibility to fold into `transcripts.metadata.hallucination_breaks` at job end. |
| **Apple-Silicon-only check.** | `health().ready` depends on `is_apple_silicon() and mlx_whisper_importable() and model_present(repo)`. A non-arm64-darwin runner sees `ready=False` with a clear `reason`, and the registry (3.5) walks fallback. |

---

## 6. SQL migrations

**None.** This backend writes nothing to Postgres directly; the
orchestrator (3.6) commits each yielded segment to `transcript_segments`
using the canonical schema in architecture §8.1, and aggregates
`Segment.metadata` into `transcripts.metadata` at job end (3.5). The
`metadata` JSONB column on `transcripts` already exists (architecture
§8.1) — wait, it doesn't yet. The architecture's `transcripts` table
has no `metadata JSONB` column. Two options:

1. **Defer the metadata column** to story 3.5's `is_active` migration —
   3.5 already touches the `transcripts` table.
2. **Add it here.**

We choose **(1)**: this plan flags the requirement; story 3.5's
migration adds:

```sql
-- (Plan 03-05 will incorporate this. Listed here for traceability.)
ALTER TABLE transcripts
  ADD COLUMN metadata JSONB NOT NULL DEFAULT '{}'::jsonb;
```

The MLX backend writes nothing schema-touching in this story; it just
populates `Segment.metadata` and trusts 3.5 to land the column.

---

## 7. Acceptance checklist

| # | Item | Verified by |
|---|---|---|
| 1 | `WhisperMLXBackend(name="whisper-mlx")` exists and structurally satisfies `STTBackend`. | `test_conformance.py` (isinstance check inside the suite). |
| 2 | `cost_per_minute = 0.0`; `supports_streaming = True`; `requires_file = False`. | Class-level constants asserted in `test_warmup_idempotent.py::test_class_constants`. |
| 3 | `hints.initial_prompt` is forwarded to `mlx_whisper.transcribe`; default is `بسم الله الرحمن الرحيم` for `language="ar"`. | `test_initial_prompt.py`. |
| 4 | `language=None` → backend runs auto-detect on first 30 s and uses the result. | `test_language_autodetect.py` — Arabic file, language=None → detected `ar` and segments produced in Arabic. |
| 5 | `Segment.text` is NFC-normalized; trailing whitespace trimmed; bidi marks NOT inserted (left to renderer). | `test_normalize.py` covers NFC/whitespace; `test_no_bidi_marks_inserted.py` asserts `‎`/`‏` absent in output. |
| 6 | On `arch != arm64-darwin`, `health().ready == False`. Registry (3.5) skips it. | `test_apple_silicon_only.py`. |
| 7 | Conformance suite (Plan 03-01 §4) passes on the macOS-arm64 CI runner with `model="tiny"`. | `test_conformance.py`. |
| 8 | OOM raises `BackendOOM` (not silent fail, not generic exception). | `test_oom_degradation.py`. |
| 9 | Hallucination-loop detector counts breaks ≥3-identical-and-long; per-segment `metadata.hallucination_break` populated. | `test_hallucination_loop.py`. |
| 10 | `start_offset_sec` trims input PCM and rebases output timestamps. | `test_resume_offset.py`. |
| 11 | `pyproject.toml` carries `mlx-whisper` and `mlx` only as optional `[mlx]` extras; importing `maktaba_pipeline.stt` does not require them. | `test_imports_are_light.py` (Plan 03-01 §5.4) re-exercised. |
| 12 | CI matrix runs MLX conformance on `macos-14` (arm64) and explicitly skips it on Linux runners. | `.github/workflows/pipeline.yml` review. |
| 13 | `warmup()` is idempotent. | `test_warmup_idempotent.py`. |
| 14 | `close()` releases the MLX cache (`mx.metal.clear_cache()`). | `test_close_releases.py` — patches `mx.metal.clear_cache`, asserts called. |
| 15 | The `_segment_callback` shim is pinned to a known mlx_whisper version range in `pyproject.toml` and fails at import on mismatch. | `test_shim_version_pin.py`. |
