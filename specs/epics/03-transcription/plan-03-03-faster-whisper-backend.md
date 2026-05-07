---
name: Plan 03-03 — Faster-Whisper backend (CUDA / CPU)
description: Implementation plan for Epic 3 Story 3 (Faster-Whisper backend). Wraps `faster_whisper.WhisperModel` for Linux + NVIDIA and CPU fallback, with one shared base class for CUDA and CPU variants, native generator-based streaming, compute-type fallback (float16 → float32 / int8 → float32), and conformance against Plan 03-01.
type: plan
---

# Plan 03-03 — Faster-Whisper Backend (CUDA / CPU)

> **Canonical story:** [story-03-03-faster-whisper-backend.md](story-03-03-faster-whisper-backend.md).
>
> **Depends on:** [Plan 03-01](plan-03-01-backend-protocol.md) for the
> Protocol, types, error taxonomy, and conformance suite. Same shape
> contract as [Plan 03-02](plan-03-02-whisper-mlx-backend.md); shares
> nothing in code.
>
> **Architecture references.** [§3.4 Transcriber](../../architecture.md)
> lists `whisper-cuda` (Linux + NVIDIA, ~0.1× RT) and `whisper-cpu`
> (Linux without GPU, ~3× RT). [§11.4 pipeline.toml](../../architecture.md)
> permits per-library backend selection.
>
> **Out of scope.** Backend registry / fallback selection (story 3.5).
> Per-segment commit (story 3.6). Pause / resume orchestration (3.7).
> Diarization (3.9). The OpenAI API backend (3.4).

---

## 1. Architecture diagram

```
        Orchestrator (transcribe stage)
                │
                ▼
   ┌────────────────────────────────────────────────────────┐
   │  _BaseFasterWhisperBackend (abstract)                  │
   │  pipeline/stt/faster_whisper/_base.py                  │
   │                                                        │
   │   __init__(model, *, device, compute_type, threads)   │
   │   - subclasses bind device + compute_type defaults     │
   │   - construction is cheap; model loads on warmup()     │
   │                                                        │
   │   async transcribe(audio, language, hints):            │
   │     1. Materialize AudioSource → ndarray (PCM int16)   │
   │        - file:  faster_whisper accepts a path          │
   │        - pipe:  drain to np.float32 (helper from 03-02 │
   │                  is duplicated here, lighter footprint)│
   │     2. Apply seek (start_offset_sec)                   │
   │     3. Call self._model.transcribe(audio, ...) which   │
   │        returns (segments_iter, info)                   │
   │     4. Drive the generator in a worker thread, push    │
   │        each segment onto an asyncio.Queue              │
   │     5. Convert + yield Segment per Plan 03-01 schema   │
   │                                                        │
   │   async detect_language(audio):                        │
   │     uses faster_whisper.detect_language_function(...) │
   │     on the first 30 s.                                 │
   │                                                        │
   │   async health(): driver-aware probe; cheap            │
   │   async warmup(): instantiates WhisperModel; retries   │
   │                   on compute-type mismatch (see §3.4)  │
   │   async close(): drops the model reference             │
   └────────────────────────────────────────────────────────┘
                │ extends                       │ extends
                ▼                               ▼
   ┌────────────────────────┐         ┌────────────────────────┐
   │ FasterWhisperCUDA      │         │ FasterWhisperCPU       │
   │ name = "whisper-cuda"  │         │ name = "whisper-cpu"   │
   │ device = "cuda"        │         │ device = "cpu"         │
   │ compute_type=float16   │         │ compute_type=int8      │
   │ supports_streaming=Y   │         │ supports_streaming=Y   │
   │ requires_file = False  │         │ requires_file = False  │
   │ cost_per_minute = 0.0  │         │ cost_per_minute = 0.0  │
   └────────────────────────┘         └────────────────────────┘
```

Five things to notice:

1. **One base class, two subclasses.** Story 3.3 explicitly asks for
   shared code "to avoid duplication". The base is abstract enough that
   subclasses set only `device`, `compute_type`, and `name`.
2. **`faster_whisper.WhisperModel.transcribe()` already returns a
   generator.** Each segment is decoded as the iterator advances. We
   simply consume that generator on a worker thread and push results
   onto an asyncio.Queue — same pattern as Plan 03-02 but cleaner here
   because the upstream API is already iterator-friendly.
3. **`requires_file = False`** for both variants; faster_whisper accepts
   numpy arrays for audio. PCM streams are drained to memory once
   (same memory budget reasoning as Plan 03-02 §1).
4. **Compute-type fallback is a property of the base class.** On
   construction failure (e.g. CUDA reports "float16 unsupported" on
   compute capability < 7.0, or `int8` unavailable on a CPU without
   AVX2), we retry once with `float32`, record the choice in a
   per-instance `actual_compute_type` field, and surface it in
   `health().reason`.
5. **CUDA is opt-in and CI-skippable.** Health probe checks
   `torch.cuda.is_available()` *via the lighter `ctranslate2.get_cuda_device_count()`*
   so we don't depend on PyTorch. CI runs CPU on every push; CUDA runs
   on a dedicated self-hosted runner with `runs-on: [self-hosted, gpu]`
   and is allowed to skip if the runner is offline.

---

## 2. New artifacts

| Layer | Path | Status | Purpose |
|---|---|---|---|
| Python | `pipeline/src/maktaba_pipeline/stt/faster_whisper/__init__.py` | **new** | Package marker; exports `FasterWhisperCUDA`, `FasterWhisperCPU`. |
| Python | `pipeline/src/maktaba_pipeline/stt/faster_whisper/_base.py` | **new** | Abstract `_BaseFasterWhisperBackend`. |
| Python | `pipeline/src/maktaba_pipeline/stt/faster_whisper/cuda.py` | **new** | `FasterWhisperCUDA` subclass. |
| Python | `pipeline/src/maktaba_pipeline/stt/faster_whisper/cpu.py` | **new** | `FasterWhisperCPU` subclass. |
| Python | `pipeline/src/maktaba_pipeline/stt/faster_whisper/_audio.py` | **new** | `drain_pcm_to_float32` (matches Plan 03-02 helper byte-for-byte; copy is intentional to keep packages independently importable). |
| Python | `pipeline/src/maktaba_pipeline/stt/faster_whisper/_runtime.py` | **new** | `cuda_available()`, `cuda_device_count()`, `compute_type_supported(device, ct)`. |
| Python | `pipeline/src/maktaba_pipeline/stt/faster_whisper/tests/test_conformance_cpu.py` | **new** | Plug `FasterWhisperCPU(model="tiny")` into `stt_conformance_suite`; runs on every CI matrix entry. |
| Python | `pipeline/src/maktaba_pipeline/stt/faster_whisper/tests/test_conformance_cuda.py` | **new** | Plug `FasterWhisperCUDA(model="tiny")` into `stt_conformance_suite`; gated `pytest.mark.skipif` on `cuda_available()`. |
| Python | `pipeline/src/maktaba_pipeline/stt/faster_whisper/tests/test_word_timestamps_match_segment.py` | **new** | Story 3.3 test case — sum of word durations within ε of segment duration. |
| Python | `pipeline/src/maktaba_pipeline/stt/faster_whisper/tests/test_compute_type_fallback.py` | **new** | Story 3.3 edge case — patched WhisperModel raises on float16 → backend retries with float32 and records the choice. |
| Python | `pipeline/src/maktaba_pipeline/stt/faster_whisper/tests/test_health.py` | **new** | CPU backend always ready; CUDA backend's `ready` follows `cuda_available()`. |
| Python | `pipeline/src/maktaba_pipeline/stt/faster_whisper/tests/test_streaming_iterator.py` | **new** | The async iterator yields the first segment before the upstream generator is exhausted (proves streaming). |
| Config | `pipeline/pyproject.toml` | **edit** | Add `faster-whisper>=1.0` as a non-optional dep (CPU mode runs everywhere); add `[cuda]` extra documenting the system requirement (CUDA toolkit + cuDNN), no extra Python pkg. |
| CI | `.github/workflows/pipeline.yml` | **edit** | Linux-cpu matrix runs CPU conformance + word-ts test. Add `gpu` self-hosted runner job that runs the CUDA test set when available. |
| Docs | `pipeline/src/maktaba_pipeline/stt/faster_whisper/README.md` | **new** | Half-page "what this backend does, how to install on Linux+NVIDIA, why it's the default fallback for non-Apple hosts". |

---

## 3. Implementation

### 3.1 `_runtime.py`

```python
"""Lightweight probes; no model load, no torch import."""
from __future__ import annotations

from importlib.util import find_spec


def faster_whisper_importable() -> bool:
    return find_spec("faster_whisper") is not None and find_spec("ctranslate2") is not None


def cuda_available() -> bool:
    """True iff at least one CUDA device is visible to ctranslate2."""
    try:
        import ctranslate2  # noqa: PLC0415
        return ctranslate2.get_cuda_device_count() > 0
    except Exception:
        return False


def cuda_device_count() -> int:
    try:
        import ctranslate2  # noqa: PLC0415
        return ctranslate2.get_cuda_device_count()
    except Exception:
        return 0


def cpu_supports_int8() -> bool:
    """ctranslate2 int8 needs AVX2; check via the published `get_supported_compute_types`."""
    try:
        import ctranslate2  # noqa: PLC0415
        return "int8" in ctranslate2.get_supported_compute_types("cpu")
    except Exception:
        return False
```

### 3.2 `_base.py`

```python
"""Abstract base for FasterWhisperCUDA and FasterWhisperCPU.

Subclasses set `name`, `device`, and `_default_compute_type`; the
base owns lifecycle, audio handling, generator-bridging, and segment
conversion.
"""
from __future__ import annotations

import asyncio
import platform
import time
from abc import ABC
from collections.abc import AsyncIterator
from pathlib import Path
from typing import ClassVar

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
from ._runtime import cuda_available, faster_whisper_importable

_SAMPLE_RATE = 16_000
_SENTINEL = object()


class _BaseFasterWhisperBackend(ABC):
    name: ClassVar[str]
    _device: ClassVar[str]              # "cuda" | "cpu"
    _default_compute_type: ClassVar[str]  # "float16" | "int8"

    supports_streaming: bool = True
    supports_word_timestamps: bool = True
    requires_file: bool = False
    cost_per_minute: float | None = 0.0

    def __init__(
        self,
        model: str = "large-v3",
        *,
        compute_type: str | None = None,
        threads: int | None = None,
    ) -> None:
        self.model_id = model
        self._desired_compute_type = compute_type or self._default_compute_type
        self._actual_compute_type: str | None = None  # populated after warmup
        self._threads = threads
        self._model = None  # faster_whisper.WhisperModel | None

    # ------------------------------------------------------------------ Lifecycle

    async def warmup(self) -> None:
        if self._model is not None:
            return
        if not faster_whisper_importable():
            raise BackendNotReady(f"{self.name} requires faster-whisper")
        if self._device == "cuda" and not cuda_available():
            raise BackendNotReady(f"{self.name} requires a visible CUDA device")

        from faster_whisper import WhisperModel  # noqa: PLC0415

        attempts: list[str] = [self._desired_compute_type]
        if "float32" not in attempts:
            attempts.append("float32")  # exactly-one fallback per story 3.3 edge case

        last_exc: BaseException | None = None
        for ct in attempts:
            try:
                self._model = await asyncio.to_thread(
                    WhisperModel,
                    self.model_id,
                    device=self._device,
                    compute_type=ct,
                    cpu_threads=self._threads or 0,
                    download_root=None,  # ~/.cache/huggingface by default
                )
                self._actual_compute_type = ct
                return
            except (RuntimeError, ValueError) as exc:
                last_exc = exc
                continue
        raise BackendFatal(
            f"{self.name} could not initialize WhisperModel with any of {attempts}: {last_exc}"
        )

    async def close(self) -> None:
        self._model = None
        # ctranslate2 releases on GC; explicit `del` is enough.

    async def health(self) -> BackendHealth:
        ready = faster_whisper_importable() and (
            self._device == "cpu" or cuda_available()
        )
        reason = None
        if not ready:
            reason = (
                "CUDA device not visible; check NVIDIA driver / nvidia-smi"
                if self._device == "cuda"
                else "faster-whisper not importable"
            )
        return BackendHealth(
            ready=ready,
            model_loaded=self._model is not None,
            version=_get_fw_version() or "unknown",
            device=self._device if ready else "unknown",  # type: ignore[arg-type]
            last_check_at=time.time(),
            reason=reason,
        )

    # ------------------------------------------------------------------ Inference

    async def detect_language(self, audio: AudioSource) -> str:
        await self.warmup()
        arr = await self._materialize(audio, max_seconds=30.0)
        # WhisperModel.detect_language accepts an ndarray and returns
        # (lang, prob, all_lang_probs). The first 30 s is enough.
        lang, _prob, _all = await asyncio.to_thread(
            self._model.detect_language, arr
        )
        return lang

    async def transcribe(
        self,
        audio: AudioSource,
        language: str | None,
        hints: TranscriptionHints,
    ) -> AsyncIterator[Segment]:
        await self.warmup()
        arr = await self._materialize(audio)
        arr = seek_samples(arr, hints.start_offset_sec, _SAMPLE_RATE)

        prompt = hints.initial_prompt
        word_ts = hints.word_timestamps

        queue: asyncio.Queue = asyncio.Queue(maxsize=64)
        loop = asyncio.get_running_loop()

        def run() -> None:
            try:
                segments_iter, _info = self._model.transcribe(
                    arr,
                    language=language,
                    initial_prompt=prompt,
                    word_timestamps=word_ts,
                    condition_on_previous_text=True,
                    vad_filter=True,
                    vad_parameters={"min_silence_duration_ms": 500},
                )
                for raw in segments_iter:
                    loop.call_soon_threadsafe(queue.put_nowait, raw)
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
                    continue
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

    def _convert(self, raw, seq: int, hints: TranscriptionHints, *, offset: float) -> Segment:
        # raw is a faster_whisper.transcribe.Segment NamedTuple.
        words: list[Word] | None = None
        if hints.word_timestamps and getattr(raw, "words", None):
            words = [
                Word(
                    seq=i,
                    start=float(w.start) + offset,
                    end=float(w.end) + offset,
                    text=w.word.strip(),
                    confidence=getattr(w, "probability", None),
                )
                for i, w in enumerate(raw.words)
            ]
        return Segment(
            seq=seq,
            start=float(raw.start) + offset,
            end=float(raw.end) + offset,
            text=raw.text.strip(),
            confidence=getattr(raw, "avg_logprob", None),
            words=words,
            metadata={
                "no_speech_prob": float(getattr(raw, "no_speech_prob", 0.0)),
                "compression_ratio": float(getattr(raw, "compression_ratio", 0.0)),
                "compute_type": self._actual_compute_type or "?",
            },
        )


def _is_oom(exc: BaseException) -> bool:
    msg = str(exc).lower()
    return "out of memory" in msg or "cuda error: out of memory" in msg


def _read_wav_to_float32(path: Path, *, max_seconds: float | None) -> np.ndarray:
    import wave  # noqa: PLC0415
    with wave.open(str(path), "rb") as wf:
        n = wf.getnframes()
        if max_seconds is not None:
            n = min(n, int(max_seconds * _SAMPLE_RATE))
        raw = wf.readframes(n)
    return np.frombuffer(raw, dtype=np.int16).astype(np.float32) / 32768.0


def _get_fw_version() -> str | None:
    try:
        import faster_whisper  # noqa: PLC0415
        return getattr(faster_whisper, "__version__", None)
    except ImportError:
        return None
```

### 3.3 `cuda.py` and `cpu.py`

```python
# cuda.py
from ._base import _BaseFasterWhisperBackend


class FasterWhisperCUDA(_BaseFasterWhisperBackend):
    name = "whisper-cuda"
    _device = "cuda"
    _default_compute_type = "float16"
```

```python
# cpu.py
from ._base import _BaseFasterWhisperBackend


class FasterWhisperCPU(_BaseFasterWhisperBackend):
    name = "whisper-cpu"
    _device = "cpu"
    _default_compute_type = "int8"
```

### 3.4 Compute-type fallback semantics

| Desired | Failure raises | Retry with | If retry fails |
|---|---|---|---|
| `float16` (CUDA default on Compute Cap. ≥ 7.0) | `RuntimeError` from ctranslate2 ("compute_type not supported" or older driver) | `float32` | `BackendFatal("could not initialize ...")` |
| `int8` (CPU default; AVX2 required) | `ValueError` from ctranslate2 ("compute_type 'int8' not supported on this device") | `float32` | `BackendFatal` |
| Explicit `float32` (set via constructor) | n/a | n/a | n/a |

The actual compute type used is recorded in `Segment.metadata.compute_type`
(useful for benchmark reporting) and surfaced in `health().reason` once
loaded. Story 3.3 acceptance: "the backend falls back to float32 once
and records the choice."

---

## 4. Test plan

### 4.1 `test_conformance_cpu.py`

```python
"""CPU conformance: every CI runner runs this."""
import pytest

from maktaba_pipeline.stt.conformance.suite import stt_conformance_suite
from maktaba_pipeline.stt.faster_whisper import FasterWhisperCPU


@pytest.fixture(scope="module")
def backend():
    yield FasterWhisperCPU(model="tiny")


def test_conformance(backend):
    stt_conformance_suite(backend)
```

### 4.2 `test_conformance_cuda.py`

```python
"""CUDA conformance: gated on a visible NVIDIA device."""
import pytest

from maktaba_pipeline.stt.conformance.suite import stt_conformance_suite
from maktaba_pipeline.stt.faster_whisper import FasterWhisperCUDA
from maktaba_pipeline.stt.faster_whisper._runtime import cuda_available


pytestmark = pytest.mark.skipif(not cuda_available(), reason="no CUDA device visible")


@pytest.fixture(scope="module")
def backend():
    yield FasterWhisperCUDA(model="tiny")


def test_conformance(backend):
    stt_conformance_suite(backend)
```

### 4.3 `test_word_timestamps_match_segment.py`

```python
"""Story 3.3: sum of word durations within ε of segment duration."""
import pytest

from maktaba_pipeline.stt.audio_source import from_file
from maktaba_pipeline.stt.faster_whisper import FasterWhisperCPU
from maktaba_pipeline.stt.types import TranscriptionHints
from .helpers import open_english_fixture


EPSILON = 0.10  # word boundaries snap to frame edges


async def test_word_durations_sum_close_to_segment():
    backend = FasterWhisperCPU(model="tiny")
    src = from_file(open_english_fixture())
    hints = TranscriptionHints(word_timestamps=True)
    async for seg in backend.transcribe(src, "en", hints):
        assert seg.words, "word_timestamps=True but words are empty"
        sum_word_dur = sum(w.end - w.start for w in seg.words)
        seg_dur = seg.end - seg.start
        # Words may have small inter-word gaps; sum should not exceed
        # segment duration. Allow ε for snap-to-frame.
        assert sum_word_dur <= seg_dur + EPSILON
        # And shouldn't be wildly less either (would indicate dropped words).
        assert sum_word_dur >= 0.5 * seg_dur
```

### 4.4 `test_compute_type_fallback.py`

```python
"""Story 3.3 edge case: compute_type mismatch falls back to float32 once."""
from unittest.mock import MagicMock, patch

import pytest

from maktaba_pipeline.stt.faster_whisper import FasterWhisperCPU
from maktaba_pipeline.stt.errors import BackendFatal


async def test_int8_fallback_to_float32(monkeypatch):
    backend = FasterWhisperCPU(model="tiny")

    constructed: list[str] = []

    def fake_ctor(model_id, *, device, compute_type, **kw):
        constructed.append(compute_type)
        if compute_type == "int8":
            raise ValueError("compute_type 'int8' not supported on this device")
        # Else succeed with a stub
        m = MagicMock()
        return m

    with patch("faster_whisper.WhisperModel", side_effect=fake_ctor):
        await backend.warmup()

    assert constructed == ["int8", "float32"]
    assert backend._actual_compute_type == "float32"


async def test_both_fail_raises_fatal(monkeypatch):
    backend = FasterWhisperCPU(model="tiny")

    def boom(*a, **kw):
        raise RuntimeError("nope")

    with patch("faster_whisper.WhisperModel", side_effect=boom), \
         pytest.raises(BackendFatal):
        await backend.warmup()
```

### 4.5 `test_health.py`

```python
import pytest

from maktaba_pipeline.stt.faster_whisper import FasterWhisperCPU, FasterWhisperCUDA


async def test_cpu_health_ready_when_importable(monkeypatch):
    monkeypatch.setattr(
        "maktaba_pipeline.stt.faster_whisper._runtime.faster_whisper_importable",
        lambda: True,
    )
    h = await FasterWhisperCPU(model="tiny").health()
    assert h.ready is True
    assert h.device == "cpu"


async def test_cuda_health_false_when_no_gpu(monkeypatch):
    monkeypatch.setattr(
        "maktaba_pipeline.stt.faster_whisper._runtime.cuda_available",
        lambda: False,
    )
    h = await FasterWhisperCUDA(model="tiny").health()
    assert h.ready is False
    assert "CUDA" in (h.reason or "")
```

### 4.6 `test_streaming_iterator.py`

```python
"""Prove the iterator yields segments before the upstream generator
finishes, i.e. that we are *streaming* and not buffering all segments.
"""
import asyncio
import time

from maktaba_pipeline.stt.audio_source import from_file
from maktaba_pipeline.stt.faster_whisper import FasterWhisperCPU
from maktaba_pipeline.stt.types import TranscriptionHints
from .helpers import open_english_fixture


async def test_first_segment_arrives_before_completion():
    backend = FasterWhisperCPU(model="tiny")
    src = from_file(open_english_fixture())
    started = time.monotonic()
    first = None
    last_segments = []
    async for s in backend.transcribe(src, "en", TranscriptionHints()):
        if first is None:
            first = time.monotonic() - started
        last_segments.append(s)
    total = time.monotonic() - started
    # The first segment of a 30 s clip should arrive in less time than
    # the entire decode. (Loose bound; we just want to prove streaming.)
    assert first < total * 0.95
    assert last_segments
```

---

## 5. Edge cases (story 3.3) — explicit handling

| Story §Edge case | Handling here |
|---|---|
| **Compute-type mismatch.** | `_BaseFasterWhisperBackend.warmup()` retries `float32` exactly once if the desired type fails (§3.4). `_actual_compute_type` is stored on the instance and reflected in `Segment.metadata.compute_type`. |

(Story 3.3's edge case list is short — only the compute-type case. The
remaining edge concerns — OOM, retries, etc. — are inherited
implicitly from Plan 03-02's design and verified by the conformance
suite, which is shared.)

---

## 6. SQL migrations

**None.** The `transcripts` row's `backend` and `model` columns
(architecture §8.1) already accommodate `whisper-cuda` / `whisper-cpu`
as values; the orchestrator (3.5) is responsible for writing them.

The `metadata` JSONB column on `transcripts` referenced in this plan
(via `Segment.metadata.compute_type`) is added by Plan 03-05's
migration (`000X_transcripts_is_active.sql` will be expanded to
include the column — see Plan 03-05 §SQL migrations).

---

## 7. Acceptance checklist

| # | Item | Verified by |
|---|---|---|
| 1 | `FasterWhisperCUDA(name="whisper-cuda")` and `FasterWhisperCPU(name="whisper-cpu")` exist; share `_BaseFasterWhisperBackend`. | Source diff. |
| 2 | Both subclasses structurally satisfy `STTBackend`. | `test_conformance_cpu.py` and `test_conformance_cuda.py` both call `stt_conformance_suite` whose first assertion is `isinstance(backend, STTBackend)`. |
| 3 | `transcribe()` is streaming — yields each segment as faster-whisper emits it (no buffering). | `test_streaming_iterator.py`. |
| 4 | Conformance suite passes for `FasterWhisperCPU` on every CI matrix entry (mandatory). | `.github/workflows/pipeline.yml` job log. |
| 5 | Conformance suite passes for `FasterWhisperCUDA` on the GPU runner; gracefully skipped when no GPU. | `pytestmark.skipif(not cuda_available())`. |
| 6 | Word-timestamp parity test passes (sum of word durations ≤ segment duration + ε; ≥ 0.5 × segment duration). | `test_word_timestamps_match_segment.py`. |
| 7 | Compute-type fallback: `int8` fail → `float32` retry; `float16` fail → `float32` retry; recorded in `_actual_compute_type` and `Segment.metadata.compute_type`. | `test_compute_type_fallback.py`. |
| 8 | Both fallback failure → `BackendFatal`. | Same test, second case. |
| 9 | `health().ready` for CPU is True when `faster_whisper` is importable; for CUDA, depends on `ctranslate2.get_cuda_device_count() > 0`. | `test_health.py`. |
| 10 | OOM (CUDA or CPU) raises `BackendOOM`. | Inherited from `_BaseFasterWhisperBackend._is_oom`; covered by `tests/test_oom_cuda.py` (skipped without GPU). |
| 11 | `Segment.text` is stripped of trailing whitespace. | `test_strip_text.py` (one-liner). |
| 12 | `pyproject.toml` adds `faster-whisper>=1.0` as an unconditional dep; the `[cuda]` extra documents but does not gate it. | File diff. |
| 13 | Importing `maktaba_pipeline.stt` does NOT import `faster_whisper` (it imports lazily in `warmup()`). | `test_imports_are_light.py` (Plan 03-01) extended to assert no `faster_whisper` in `sys.modules`. |
| 14 | `Segment.metadata.compute_type` is populated. | `test_metadata_includes_compute_type.py`. |
| 15 | `close()` drops the model reference; subsequent `warmup()` re-loads. | `test_close_warmup_cycle.py`. |
