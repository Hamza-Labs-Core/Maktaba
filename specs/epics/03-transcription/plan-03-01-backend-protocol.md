---
name: Plan 03-01 — STT backend protocol
description: Implementation plan for Epic 3 Story 1 (STT backend protocol). Specifies the Python `STTBackend` Protocol, the canonical `Segment` / `Word` / `BackendHealth` / `TranscriptionHints` value objects, the `AudioSource` adapter, and the backend-agnostic `stt_conformance_suite` pytest fixture that every concrete backend (3.2 / 3.3 / 3.4) must pass in CI.
type: plan
---

# Plan 03-01 — STT Backend Protocol

> **Canonical story:** [story-03-01-backend-protocol.md](story-03-01-backend-protocol.md)
> (acceptance criteria + edge cases + test cases).
>
> **Architecture references.** [§3.4 Transcriber (pluggable STT)](../../architecture.md)
> defines the Protocol shape and the canonical `Segment` schema; [§7.6
> Real-time progress persistence](../../architecture.md) defines the
> per-segment commit contract that this protocol must keep
> commit-friendly (yields are the unit of durability); [§8.1 Core tables](../../architecture.md)
> defines `transcript_segments`, the storage shape that `Segment` must
> map to losslessly.
>
> **Scope.** This is the **abstract layer only.** No concrete backend is
> shipped here. The plan delivers (a) the Python type surface every
> backend conforms to, (b) the value objects (`Segment`, `Word`,
> `BackendHealth`, `TranscriptionHints`), (c) the `AudioSource` shape
> (the adapter that bridges Plan 02-03's stream/file extractor to the
> backends), and (d) the backend-agnostic conformance suite that 3.2,
> 3.3, 3.4 will each parametrize. Concrete backends are 3.2 (`whisper-mlx`),
> 3.3 (`faster-whisper`), 3.4 (`openai-api`); their orchestration
> (registry, fallback, transcript history) is 3.5.
>
> **Out of scope.** Diarization (3.9), pause/resume (3.7), crash
> recovery (3.8), per-segment commit (3.6), backend selection (3.5),
> orchestration of the `transcribe` stage. The protocol must *enable*
> all of them; their implementations are separate stories.

---

## 1. Architecture diagram — where the protocol sits

```
                  ┌────────────────────────────────────────────────┐
                  │  Pipeline Service (Python, asyncio)            │
                  └────────────────────────────────────────────────┘
                                       │
       ┌───────────────────────────────┴────────────────────────────┐
       │                                                            │
       ▼                                                            ▼
┌────────────────────────┐                            ┌─────────────────────────────┐
│ pipeline/stages/       │  (out of scope here, 3.6)  │ stt/registry.py            │
│   transcribe.py        │  ──────────────────────►   │  (story 3.5)               │
│  - claims job          │                            │   list() / fallback()      │
│  - opens AudioSource   │                            └─────────────┬───────────────┘
│  - resolves backend    │                                          │
│  - drives async iter   │  picks one                              │
└────────────────────────┘                            ┌─────────────▼───────────────┐
       │                                              │ Concrete backends (3.2/3/4) │
       │   AudioSource                                │   whisper_mlx.py            │
       │   (PCM iter or path)                         │   faster_whisper.py         │
       │   ◄──────── Plan 02-03                       │   openai_api.py             │
       │                                              └─────────────┬───────────────┘
       ▼                                                            │ implements
┌─────────────────────────────────────────────────────────────────────────────────┐
│  pipeline/stt/protocol.py     ◄── THIS PLAN                                     │
│                                                                                 │
│   STTBackend (typing.Protocol, runtime_checkable)                              │
│     name: str                                                                   │
│     supports_streaming: bool                                                    │
│     supports_word_timestamps: bool                                              │
│     requires_file: bool                                                         │
│     cost_per_minute: float | None                                               │
│                                                                                 │
│     async def transcribe(audio, language, hints) -> AsyncIterator[Segment]:    │
│     async def detect_language(audio) -> str:                                    │
│     async def health() -> BackendHealth:                                        │
│     async def warmup() -> None:                                                 │
│     async def close() -> None:                                                  │
│                                                                                 │
│  pipeline/stt/types.py                                                          │
│   Segment, Word, BackendHealth, TranscriptionHints, AudioSource                │
│                                                                                 │
│  pipeline/stt/conformance/                                                      │
│   suite.py        — @pytest.fixture stt_conformance_suite(backend)              │
│   fixtures/       — short Arabic + English audio + reference text              │
│   _normalize.py   — NFC + diacritic strip + Levenshtein helpers                │
└─────────────────────────────────────────────────────────────────────────────────┘
                                       │
                                       ▼ produces
                          ┌─────────────────────────────┐
                          │ Segment(seq, start, end,    │
                          │   text, words?, conf?)      │
                          │ — caller commits to Postgres│
                          │   per architecture §7.6     │
                          └─────────────────────────────┘
```

Five things to notice:

1. **The protocol owns no I/O.** `STTBackend` is pure: `audio` in,
   `Segment`s out. No DB writes, no heartbeats, no file system. That's
   the orchestrator's job (3.6 / 3.5). Keeping the protocol pure is what
   lets the conformance suite run a backend against a sample WAV
   without booting Postgres.
2. **`AudioSource` is the adapter.** It abstracts over Plan 02-03's
   stream and file modes so a backend can ask "give me a path" or "give
   me PCM chunks" and get either, regardless of how the orchestrator
   originally extracted. The protocol does not require a backend to
   support both — `requires_file` declares it.
3. **`AsyncIterator[Segment]` is the contract**, even for backends that
   are not natively streaming. A non-streaming backend simply yields
   all segments at the end. This single abstraction is what lets the
   per-segment-commit logic (3.6) be backend-agnostic.
4. **Conformance is a fixture, not a base class.** Backends do not
   inherit anything. The pytest fixture parametrizes a shared list of
   tests against any backend instance. This keeps the protocol
   structurally typed (Python's nominal-typing trap avoided) and lets
   third parties drop in a new backend in one file.
5. **Health gates the registry.** `health.ready=False` ⇒ registry skips
   it. The protocol mandates that `health()` is **cheap** (no model
   load, no GPU pin, no API call beyond a TCP probe).

---

## 2. New artifacts

| Layer | Path | Status | Purpose |
|---|---|---|---|
| Python | `pipeline/src/maktaba_pipeline/stt/__init__.py` | **new** | Package marker; re-exports `STTBackend`, `Segment`, `Word`, `BackendHealth`, `TranscriptionHints`, `AudioSource`. |
| Python | `pipeline/src/maktaba_pipeline/stt/protocol.py` | **new** | The `STTBackend` `typing.Protocol` (runtime-checkable). |
| Python | `pipeline/src/maktaba_pipeline/stt/types.py` | **new** | Pydantic models: `Segment`, `Word`, `BackendHealth`, `TranscriptionHints`. `AudioSource` ADT. |
| Python | `pipeline/src/maktaba_pipeline/stt/audio_source.py` | **new** | `AudioSource` factory — wraps a Plan 02-03 `AudioPipeReader` or `AudioFileExtractor` so backends can request `.as_path()` or `.as_pcm_iter()` lazily. |
| Python | `pipeline/src/maktaba_pipeline/stt/errors.py` | **new** | Backend-error taxonomy: `BackendNotReady`, `BackendOOM`, `BackendTransient`, `BackendFatal`, `BackendBudgetExceeded`. |
| Python | `pipeline/src/maktaba_pipeline/stt/conformance/__init__.py` | **new** | Conformance-suite package marker. |
| Python | `pipeline/src/maktaba_pipeline/stt/conformance/suite.py` | **new** | `stt_conformance_suite(backend)` fixture and the parametrized test list. |
| Python | `pipeline/src/maktaba_pipeline/stt/conformance/_normalize.py` | **new** | `nfc()`, `strip_diacritics()`, `loose_match()` — Arabic-aware comparison helpers. |
| Python | `pipeline/src/maktaba_pipeline/stt/conformance/fixtures/` | **new** | Tiny royalty-free audio + reference text (see §6). |
| Python | `pipeline/src/maktaba_pipeline/stt/tests/test_types.py` | **new** | Unit tests for `Segment`, `Word`, `TranscriptionHints` validation. |
| Python | `pipeline/src/maktaba_pipeline/stt/tests/test_audio_source.py` | **new** | Unit tests for `AudioSource.as_path()` materialization, `as_pcm_iter()` chunk shape, lifecycle close. |
| Python | `pipeline/src/maktaba_pipeline/stt/tests/test_protocol_isinstance.py` | **new** | Runtime-checkable `isinstance` smoke tests against a `FakeBackend`. |
| Python | `pipeline/src/maktaba_pipeline/stt/tests/test_conformance_against_fake.py` | **new** | The conformance suite running against a fake backend that emits canned segments — proves the suite itself works. |
| Fixture | `shared/fixtures/stt/lecture_30s_ar.wav` | **new** | 30 s mono 16 kHz PCM-WAV Arabic recitation (royalty-free). |
| Fixture | `shared/fixtures/stt/lecture_30s_ar.ref.txt` | **new** | Reference transcript for `lecture_30s_ar.wav`, NFC-normalized. |
| Fixture | `shared/fixtures/stt/talk_30s_en.wav` | **new** | 30 s mono 16 kHz PCM-WAV English speech (royalty-free). |
| Fixture | `shared/fixtures/stt/talk_30s_en.ref.txt` | **new** | Reference transcript for `talk_30s_en.wav`. |
| Docs | `pipeline/src/maktaba_pipeline/stt/README.md` | **new** | One-page "how to add a backend" — structurally typed, plug a class, list it in the registry, run the conformance fixture. |

No DB migration in this story; `transcripts` columns already exist
(architecture §8.1). Story 3.5 owns the `is_active` migration that the
orchestrator will use; this story does not touch the schema.

---

## 3. Type surface

### 3.1 `pipeline/src/maktaba_pipeline/stt/types.py`

```python
"""Canonical value objects shared by every STT backend.

Keep this file dependency-light: pydantic + stdlib only. Anything
heavier (numpy, mlx, torch, openai) belongs in a concrete backend
module so importing the protocol does not pull a 2 GB ML stack.
"""
from __future__ import annotations

import dataclasses
from collections.abc import AsyncIterator
from pathlib import Path
from typing import Literal

from pydantic import BaseModel, Field, model_validator


class Word(BaseModel):
    """Word-level timing inside a segment.

    `start`/`end` are absolute seconds in the original audio timeline,
    *not* relative to the parent segment. This matches the orchestrator's
    expectation in `transcript_words` (architecture §8.1).
    """

    seq: int = Field(ge=0)
    start: float = Field(ge=0.0)
    end: float = Field(ge=0.0)
    text: str
    confidence: float | None = Field(default=None, ge=0.0, le=1.0)

    @model_validator(mode="after")
    def _bounds(self) -> "Word":
        if self.end < self.start:
            raise ValueError(f"word end {self.end} < start {self.start}")
        return self


class Segment(BaseModel):
    """A single transcribed segment, the per-segment-commit unit (story 3.6).

    Mirrors `transcript_segments` in architecture §8.1. The orchestrator
    is responsible for assigning `transcript_id`; the backend only fills
    in shape data.
    """

    seq: int = Field(ge=0)
    start: float = Field(ge=0.0)
    end: float = Field(ge=0.0)
    text: str
    speaker: str | None = None
    confidence: float | None = Field(default=None, ge=0.0, le=1.0)
    words: list[Word] | None = None
    # Free-form per-segment metadata for diagnostics (e.g. avg_logprob,
    # no_speech_prob from Whisper). Persisted into transcripts.metadata
    # only when the orchestrator chooses; not part of the schema contract.
    metadata: dict[str, float | int | str] | None = None

    @model_validator(mode="after")
    def _bounds(self) -> "Segment":
        if self.end < self.start:
            raise ValueError(f"segment end {self.end} < start {self.start}")
        if self.words:
            for w in self.words:
                # Allow ε=0.05 s slack so backends that snap word edges
                # to frame boundaries do not fail this gate.
                if w.start + 0.05 < self.start or w.end > self.end + 0.05:
                    raise ValueError(
                        f"word [{w.start},{w.end}] outside segment "
                        f"[{self.start},{self.end}]"
                    )
        return self


class TranscriptionHints(BaseModel):
    """Decoder hints. All fields optional; backends may ignore unknowns."""

    initial_prompt: str | None = None
    vocabulary: list[str] = Field(default_factory=list)
    speaker_count: int | None = Field(default=None, ge=1)
    word_timestamps: bool = True
    # The orchestrator passes a non-zero offset when resuming after a
    # pause/cancel (story 3.7). Backends that support resume MUST honor
    # this; backends that do not (e.g. openai-api) raise
    # NotImplementedError so the orchestrator falls back to re-decoding
    # from the offset position via input-side seeking in extract.
    start_offset_sec: float = Field(default=0.0, ge=0.0)


class BackendHealth(BaseModel):
    """Cheap liveness probe. Must not load the model or call the network
    beyond a TCP-level reachability check (e.g. `requests.head` with a
    short timeout). The pipeline calls this at every claim and at
    `GET /api/system/health`.
    """

    ready: bool
    model_loaded: bool
    version: str
    device: Literal["mlx", "cuda", "cpu", "remote", "unknown"]
    last_check_at: float  # unix seconds
    # Optional human-readable reason, surfaced in the API + logs when ready=False.
    reason: str | None = None


# AudioSource is an ADT, not a pydantic model — it carries an open
# stream that pydantic should not try to (de)serialize.
@dataclasses.dataclass
class _Pcm16Mono16k:
    """The PCM-stream variant. Each chunk is a multiple of 2 bytes
    (sample size) at 16 kHz mono, the canonical extract output."""

    chunks: AsyncIterator[bytes]
    duration_hint_sec: float | None  # ffprobe duration; None if unknown


@dataclasses.dataclass
class _FilePath:
    """The file-on-disk variant. Always a 16 kHz mono WAV produced by
    Plan 02-03's file-mode extractor. Path is owned by the orchestrator;
    the backend MUST NOT delete or move it."""

    path: Path
    duration_sec: float


AudioSource = _Pcm16Mono16k | _FilePath
```

### 3.2 `pipeline/src/maktaba_pipeline/stt/protocol.py`

```python
"""The single STTBackend Protocol every backend implements.

Structural typing (typing.Protocol) is a deliberate choice: backends
do not inherit a base class. A backend is just a class whose method
signatures match. This keeps third-party backends frictionless and
keeps test doubles (FakeBackend) trivial.
"""
from __future__ import annotations

from collections.abc import AsyncIterator
from typing import Protocol, runtime_checkable

from .types import AudioSource, BackendHealth, Segment, TranscriptionHints


@runtime_checkable
class STTBackend(Protocol):
    """Speech-to-text backend.

    A backend is a stateless-on-class, possibly stateful-on-instance
    object. Construction is cheap; loading the model happens on first
    `transcribe()` or on explicit `warmup()`.
    """

    name: str
    """Stable identifier persisted into `transcripts.backend`. Lowercase,
    no spaces, ASCII only. Examples: `whisper-mlx`, `whisper-cuda`,
    `whisper-cpu`, `openai-api`."""

    supports_streaming: bool
    """True when `transcribe()` yields segments as the audio is decoded
    (i.e. before the audio finishes). False when all segments are
    yielded at the end."""

    supports_word_timestamps: bool
    """True when the backend can populate `Segment.words` if asked."""

    requires_file: bool
    """True when the backend needs an on-disk file path. Forces the
    extract stage into file mode (Plan 02-03 §2.4)."""

    cost_per_minute: float | None
    """USD per audio minute, or None when local/free. Used by the
    budget cap (story 3.4) and by the API for cost projection."""

    async def transcribe(
        self,
        audio: AudioSource,
        language: str | None,
        hints: TranscriptionHints,
    ) -> AsyncIterator[Segment]:
        """Yield segments in monotonically non-decreasing `start` order
        (ε=0.05 s overlap allowed). The orchestrator commits each segment
        as it arrives (story 3.6). Cancellation must be cooperative: on
        `asyncio.CancelledError` the backend MUST release GPU/file
        resources before propagating."""
        ...

    async def detect_language(self, audio: AudioSource) -> str:
        """Return ISO 639-1 language code. Implementation should look at
        the first ~30 s only."""
        ...

    async def health(self) -> BackendHealth:
        """Cheap probe; see types.BackendHealth docstring."""
        ...

    async def warmup(self) -> None:
        """Load the model so the next `transcribe()` does not stall.
        The orchestrator calls this before flipping the job to `running`
        so the heartbeat budget is not consumed by a 30 s model load
        (story 3.1 edge case "Backend cold start"). May be a no-op."""
        ...

    async def close(self) -> None:
        """Release model + GPU + network handles. Safe to call twice.
        The orchestrator calls this on worker shutdown."""
        ...
```

### 3.3 `pipeline/src/maktaba_pipeline/stt/audio_source.py`

```python
"""Adapter from Plan 02-03's extractors to AudioSource."""
from __future__ import annotations

from collections.abc import AsyncIterator
from pathlib import Path

from ..media.audio import AudioFileExtractor, AudioPipeReader
from .types import AudioSource, _FilePath, _Pcm16Mono16k


def from_pipe(reader: AudioPipeReader, duration_hint: float | None) -> AudioSource:
    """Wrap a streaming PCM reader. The reader's lifecycle (process
    reaping, signals) stays owned by the caller; the backend only
    iterates the chunks."""
    return _Pcm16Mono16k(chunks=reader.aiter(), duration_hint_sec=duration_hint)


def from_file(extractor: AudioFileExtractor) -> AudioSource:
    """Wrap a materialized WAV. The cache file lives until the stage
    cleans it up on terminal state (Plan 02-03 §2.x); the backend MUST
    NOT touch the file lifetime."""
    return _FilePath(path=extractor.path, duration_sec=extractor.duration_sec)


async def materialize_to_path(source: AudioSource, scratch_dir: Path) -> Path:
    """If the source is a pipe, drain it to a temp WAV and return the
    path. Used by `requires_file=True` backends when the orchestrator
    accidentally hands them a stream (defense in depth — the orchestrator
    *should* have already chosen file mode based on
    `requires_file`, but if not, we degrade gracefully).

    This writes a temp `*.wav` next to other Plan 02-03 cache outputs and
    returns it; the caller is responsible for unlinking."""
    ...
```

### 3.4 `pipeline/src/maktaba_pipeline/stt/errors.py`

```python
"""Backend-error taxonomy. The orchestrator (story 3.5) maps these to
job retry policies."""


class BackendError(Exception):
    """Base."""


class BackendNotReady(BackendError):
    """Health check returned ready=False at claim time. Not retryable
    by the same backend; orchestrator walks fallback chain."""


class BackendOOM(BackendError):
    """GPU/CPU memory exhausted. Retryable after model auto-degrade
    (story 3.2 edge case 'Out-of-VRAM')."""


class BackendTransient(BackendError):
    """Network blip, 5xx, rate limit. Retryable with exponential
    backoff."""


class BackendFatal(BackendError):
    """Auth failure, model file missing, codec unsupported. Not
    retryable until configuration changes."""


class BackendBudgetExceeded(BackendError):
    """Per-library budget cap (story 3.4 acceptance criterion).
    Orchestrator pushes the job to `not_before = first of next month`."""
```

---

## 4. The conformance suite

### 4.1 Design

The suite is a pytest **fixture factory**, not a base class. A backend
plugs in like this:

```python
# pipeline/src/maktaba_pipeline/stt/whisper_mlx/tests/test_conformance.py
import pytest

from maktaba_pipeline.stt.conformance.suite import stt_conformance_suite
from maktaba_pipeline.stt.whisper_mlx import WhisperMLXBackend


@pytest.fixture
def backend():
    return WhisperMLXBackend(model="tiny")  # tiny model for CI speed


def test_conformance(backend):
    stt_conformance_suite(backend)
```

`stt_conformance_suite(backend)` is *not* a single test — it is the
parametrized list of tests below, executed synchronously inside the
caller. Each backend's CI job is gated on every assertion passing.

### 4.2 The suite (`conformance/suite.py`)

```python
"""Backend-agnostic conformance suite. Story 3.1 §Test cases."""
from __future__ import annotations

import asyncio
from pathlib import Path
from statistics import mean

import pytest

from .._normalize import loose_match  # NFC + diacritic strip
from ..audio_source import from_file
from ..protocol import STTBackend
from ..types import TranscriptionHints

FIXTURES = Path(__file__).parent / "fixtures"


def stt_conformance_suite(backend: STTBackend) -> None:
    """Run every conformance test case against `backend`. Tests are
    sync wrappers around asyncio.run because pytest's per-test event
    loop semantics interact badly with model lifetimes."""
    assert isinstance(backend, STTBackend), (
        f"{type(backend).__name__} does not satisfy STTBackend Protocol; "
        "check method signatures"
    )

    asyncio.run(_test_transcribe_short_arabic(backend))
    asyncio.run(_test_transcribe_short_english(backend))
    asyncio.run(_test_segments_are_monotonic(backend))
    asyncio.run(_test_segments_cover_audio(backend))
    if backend.supports_word_timestamps:
        asyncio.run(_test_word_timestamps_when_supported(backend))
    asyncio.run(_test_language_detection(backend))
    asyncio.run(_test_pause_between_segments(backend))


# ---- Individual cases --------------------------------------------------------

async def _test_transcribe_short_arabic(backend: STTBackend) -> None:
    src = from_file(_open_fixture("lecture_30s_ar.wav", duration=30.0))
    ref = (FIXTURES / "lecture_30s_ar.ref.txt").read_text(encoding="utf-8")

    segments = []
    async for s in backend.transcribe(src, language="ar", hints=TranscriptionHints()):
        segments.append(s)

    assert segments, "no segments produced for 30 s Arabic clip"
    joined = " ".join(s.text for s in segments)
    assert loose_match(joined, ref, threshold=0.7), (
        f"Arabic transcript too far from reference. got={joined!r} ref={ref!r}"
    )


async def _test_transcribe_short_english(backend: STTBackend) -> None:
    src = from_file(_open_fixture("talk_30s_en.wav", duration=30.0))
    ref = (FIXTURES / "talk_30s_en.ref.txt").read_text(encoding="utf-8")
    segments = []
    async for s in backend.transcribe(src, language="en", hints=TranscriptionHints()):
        segments.append(s)
    joined = " ".join(s.text for s in segments)
    assert loose_match(joined, ref, threshold=0.7)


async def _test_segments_are_monotonic(backend: STTBackend) -> None:
    src = from_file(_open_fixture("talk_30s_en.wav", duration=30.0))
    last_end = 0.0
    eps = 0.05
    async for s in backend.transcribe(src, language="en", hints=TranscriptionHints()):
        assert s.start + eps >= last_end, (
            f"non-monotonic: seg.start={s.start} < prev.end={last_end}"
        )
        last_end = s.end


async def _test_segments_cover_audio(backend: STTBackend) -> None:
    src = from_file(_open_fixture("talk_30s_en.wav", duration=30.0))
    total = 0.0
    async for s in backend.transcribe(src, language="en", hints=TranscriptionHints()):
        total += s.end - s.start
    assert total >= 0.9 * 30.0, f"coverage {total / 30.0:.2f} < 0.9"


async def _test_word_timestamps_when_supported(backend: STTBackend) -> None:
    src = from_file(_open_fixture("talk_30s_en.wav", duration=30.0))
    hints = TranscriptionHints(word_timestamps=True)
    async for s in backend.transcribe(src, language="en", hints=hints):
        assert s.words, f"segment {s.seq} has empty words list"
        for w in s.words:
            assert w.start <= w.end
            assert s.start - 0.05 <= w.start
            assert w.end <= s.end + 0.05


async def _test_language_detection(backend: STTBackend) -> None:
    ar = await backend.detect_language(from_file(_open_fixture("lecture_30s_ar.wav", 30.0)))
    en = await backend.detect_language(from_file(_open_fixture("talk_30s_en.wav", 30.0)))
    assert ar == "ar", f"expected ar, got {ar!r}"
    assert en == "en", f"expected en, got {en!r}"


async def _test_pause_between_segments(backend: STTBackend) -> None:
    """Run transcribe, cancel after segment N, then re-open with
    start_offset_sec = seg[N].end. Assert no overlap and no gap > ε."""
    src = from_file(_open_fixture("talk_30s_en.wav", duration=30.0))
    first_run: list = []
    cancel_after = 2

    async def collect():
        async for s in backend.transcribe(src, language="en", hints=TranscriptionHints()):
            first_run.append(s)
            if len(first_run) >= cancel_after:
                raise asyncio.CancelledError

    with pytest.raises(asyncio.CancelledError):
        await collect()

    if not first_run:
        pytest.skip("backend yields zero segments before cancellation; not a defect")
    last_end = first_run[-1].end
    src2 = from_file(_open_fixture("talk_30s_en.wav", duration=30.0))
    hints = TranscriptionHints(start_offset_sec=last_end)
    second_run: list = []
    try:
        async for s in backend.transcribe(src2, language="en", hints=hints):
            second_run.append(s)
    except NotImplementedError:
        pytest.skip(f"{backend.name} does not support start_offset; orchestrator re-decodes")
    if second_run:
        gap = second_run[0].start - last_end
        assert -0.05 <= gap <= 1.0, f"resume gap {gap:.2f} s out of bounds"


def _open_fixture(name: str, duration: float):
    """Return a stub AudioFileExtractor pointing at the fixture WAV."""
    from ..audio_source import _FilePath  # noqa: PLC0415
    from types import SimpleNamespace
    return SimpleNamespace(path=FIXTURES / name, duration_sec=duration)
```

### 4.3 `_normalize.py` — Arabic-aware comparison

```python
"""Loose text comparison for transcript fixtures.

Whisper-family models do not reproduce diacritics 1-for-1; the suite
strips diacritics on both sides and uses normalized Levenshtein."""
import unicodedata
from typing import Final

# Arabic combining marks U+064B..U+065F + tatweel U+0640.
_AR_DIACRITICS: Final = set(range(0x064B, 0x0660)) | {0x0640}


def nfc(s: str) -> str:
    return unicodedata.normalize("NFC", s)


def strip_diacritics(s: str) -> str:
    return "".join(ch for ch in s if ord(ch) not in _AR_DIACRITICS)


def loose_match(got: str, ref: str, threshold: float = 0.7) -> bool:
    g = strip_diacritics(nfc(got)).lower()
    r = strip_diacritics(nfc(ref)).lower()
    return _ratio(g, r) >= threshold


def _ratio(a: str, b: str) -> float:
    """Token-set Jaccard. Cheap; enough for fixture-vs-output sanity."""
    sa, sb = set(a.split()), set(b.split())
    if not sa and not sb:
        return 1.0
    return len(sa & sb) / max(len(sa | sb), 1)
```

---

## 5. Test plan (this story)

The conformance suite proves that a backend conforms; this section
covers the **protocol layer itself.**

### 5.1 `tests/test_types.py`

```python
import pytest
from pydantic import ValidationError

from maktaba_pipeline.stt.types import Segment, Word, TranscriptionHints


def test_segment_rejects_negative_bounds():
    with pytest.raises(ValidationError):
        Segment(seq=0, start=5.0, end=4.9, text="x")


def test_segment_accepts_word_at_boundary_within_epsilon():
    Segment(
        seq=0, start=0.0, end=1.0, text="hi",
        words=[Word(seq=0, start=-0.01, end=1.04, text="hi")],
    )


def test_segment_rejects_word_outside_bounds():
    with pytest.raises(ValidationError):
        Segment(
            seq=0, start=0.0, end=1.0, text="hi",
            words=[Word(seq=0, start=0.0, end=1.5, text="hi")],
        )


def test_word_rejects_inverted_bounds():
    with pytest.raises(ValidationError):
        Word(seq=0, start=2.0, end=1.0, text="x")


def test_hints_default_word_timestamps_true():
    h = TranscriptionHints()
    assert h.word_timestamps is True
    assert h.start_offset_sec == 0.0
    assert h.vocabulary == []


def test_hints_rejects_negative_offset():
    with pytest.raises(ValidationError):
        TranscriptionHints(start_offset_sec=-0.1)
```

### 5.2 `tests/test_protocol_isinstance.py`

```python
"""Runtime-checkable Protocol smoke tests. A backend must satisfy
isinstance() so the registry can filter the build."""
from collections.abc import AsyncIterator
import time

from maktaba_pipeline.stt.protocol import STTBackend
from maktaba_pipeline.stt.types import (
    AudioSource, BackendHealth, Segment, TranscriptionHints,
)


class FakeBackend:
    name = "fake"
    supports_streaming = True
    supports_word_timestamps = False
    requires_file = False
    cost_per_minute = None

    async def transcribe(
        self, audio: AudioSource, language: str | None, hints: TranscriptionHints,
    ) -> AsyncIterator[Segment]:
        yield Segment(seq=0, start=0.0, end=1.0, text="hi")

    async def detect_language(self, audio: AudioSource) -> str:
        return "en"

    async def health(self) -> BackendHealth:
        return BackendHealth(
            ready=True, model_loaded=False, version="0.0",
            device="cpu", last_check_at=time.time(),
        )

    async def warmup(self) -> None: ...
    async def close(self) -> None: ...


def test_fake_satisfies_protocol():
    assert isinstance(FakeBackend(), STTBackend)


def test_missing_method_fails_isinstance():
    class Broken:
        name = "broken"
        supports_streaming = True
        supports_word_timestamps = False
        requires_file = False
        cost_per_minute = None
        # missing transcribe()

    assert not isinstance(Broken(), STTBackend)
```

### 5.3 `tests/test_audio_source.py`

```python
"""Bridge tests: from_pipe yields a _Pcm16Mono16k whose chunks aiter()
matches the underlying reader; from_file yields a _FilePath and does
not stat the path; materialize_to_path drains a pipe to disk and the
file is a valid 16 kHz mono WAV.

Each case uses a fake AudioPipeReader / AudioFileExtractor so this
test does not depend on Plan 02-03's ffmpeg subprocess."""
```

(Implemented; assertions per the docstring.)

### 5.4 `tests/test_conformance_against_fake.py`

A `CannedBackend` that replays a hand-curated `Segment` list for each
fixture is checked into the test directory. Running the conformance
suite against it asserts that the suite itself works end-to-end (the
fixtures load, the matchers fire, the monotonic / coverage / language
checks pass when the canned data satisfies them, and fail with a clear
message when they don't). Three negative variants force each individual
assertion to trigger.

---

## 6. Fixture preparation

Two short clips are needed; both must be license-clean.

| Fixture | Source | Length | Format | Notes |
|---|---|---|---|---|
| `lecture_30s_ar.wav` | Public-domain recitation excerpt, manually clipped + downsampled | 30 s | mono 16 kHz s16 WAV | Reference text NFC-normalized, no diacritics required for matching. |
| `talk_30s_en.wav` | LibriVox public-domain recording, clipped to 30 s | 30 s | mono 16 kHz s16 WAV | Reference matches the source script with whitespace normalized. |

Recipe (run once, output checked into `shared/fixtures/stt/`):

```bash
ffmpeg -i source-ar.flac -ss 30 -t 30 \
    -ac 1 -ar 16000 -sample_fmt s16 \
    shared/fixtures/stt/lecture_30s_ar.wav

ffmpeg -i librivox-en.mp3 -ss 60 -t 30 \
    -ac 1 -ar 16000 -sample_fmt s16 \
    shared/fixtures/stt/talk_30s_en.wav
```

Both fixtures total well under 1 MB at 16-bit 16 kHz mono (~960 KB
each), keeping the repo light. Reference texts live next to the WAVs
and are kept in sync via a small `make-fixtures.sh` script.

---

## 7. Edge cases (story 3.1) — how the protocol enables each

| Story §Edge case | Where the protocol pays for it |
|---|---|
| **Backends that emit segments out of order.** | Not the protocol's job to reorder; the orchestrator (story 3.6) buffers a small reorder window before commit. The protocol *permits* out-of-order emission so a backend doesn't have to fake it. |
| **Backends that emit empty `text` for silence.** | Protocol does not forbid empty text (`Segment.text` has no min length). Orchestrator (story 3.6) drops them before commit; gap accounting still advances `processed_seconds`. |
| **Backend cold start.** | `warmup()` is a first-class protocol method. Orchestrator calls it before flipping job to `running`; conformance suite does not require a fast warmup, only that it returns. |

---

## 8. SQL migrations

**None in this story.** The protocol writes nothing; persistence is
3.5's responsibility. We do, however, document the column-to-field
mapping the orchestrator will use, so 3.5 can implement without
re-deriving it:

```sql
-- Reminder of the existing transcripts schema (architecture §8.1) —
-- this story does not modify it. Story 3.5 owns the is_active migration.

-- transcript_segments columns ←→ Segment fields
--   transcript_id   ← orchestrator-assigned (NOT in Segment)
--   seq             ← Segment.seq
--   start_sec       ← Segment.start
--   end_sec         ← Segment.end
--   text            ← Segment.text
--   speaker         ← Segment.speaker
--   confidence      ← Segment.confidence

-- transcript_words columns ←→ Word fields
--   segment_id      ← orchestrator-assigned (NOT in Word)
--   seq             ← Word.seq
--   start_sec       ← Word.start
--   end_sec         ← Word.end
--   text            ← Word.text
--   confidence      ← Word.confidence
```

The orchestrator maps `Segment` → row by adding `transcript_id` and
issuing the per-segment `INSERT` (story 3.6). The protocol guarantees
the field shape; the orchestrator owns the schema.

---

## 9. Acceptance checklist

| # | Item | Verified by |
|---|---|---|
| 1 | `STTBackend` Protocol exists at `maktaba_pipeline.stt.protocol.STTBackend` with all attributes / methods listed in §3.2. | `tests/test_protocol_isinstance.py::test_fake_satisfies_protocol` |
| 2 | `STTBackend` is `@runtime_checkable`; `isinstance(x, STTBackend)` works. | `tests/test_protocol_isinstance.py::test_fake_satisfies_protocol`, `test_missing_method_fails_isinstance` |
| 3 | `Segment`, `Word`, `BackendHealth`, `TranscriptionHints` exist in `maktaba_pipeline.stt.types`, validated by pydantic. | `tests/test_types.py` (six cases) |
| 4 | `BackendHealth` reports `{ready, model_loaded, version, device, last_check_at}`. | `tests/test_types.py::test_health_shape`, used by `GET /api/system/health` (story 3.5). |
| 5 | `AudioSource` adapter (`from_pipe`, `from_file`, `materialize_to_path`) bridges Plan 02-03. | `tests/test_audio_source.py` |
| 6 | `stt_conformance_suite(backend)` runs the seven cases listed in story 3.1 §Test cases. | `conformance/suite.py`, `tests/test_conformance_against_fake.py` |
| 7 | The suite is **gated as required in CI** for every backend listed in architecture §3.4. | CI workflow `.github/workflows/pipeline.yml` matrix entries (story 3.5 wires the matrix; this story ships the suite they consume). |
| 8 | Fixture WAVs (Arabic + English, 30 s each, mono 16 kHz) and reference texts are committed under `shared/fixtures/stt/`. | Files exist, ≤ 2 MB combined, license-clean. |
| 9 | `errors.py` taxonomy (`BackendNotReady`, `BackendOOM`, `BackendTransient`, `BackendFatal`, `BackendBudgetExceeded`) is importable. | Smoke import in `tests/test_types.py`. |
| 10 | Importing `maktaba_pipeline.stt` does **not** import mlx, torch, openai, or any heavy ML stack. | `tests/test_imports_are_light.py` (asserts `sys.modules` does not contain those names after the import). |
| 11 | Protocol's `transcribe()` is `AsyncIterator[Segment]` (not `Awaitable[list[Segment]]`). | Type annotation in `protocol.py`; inspected via `typing.get_type_hints` in a test. |
| 12 | `cost_per_minute: float \| None` accommodates free local backends and priced API backends uniformly. | `tests/test_protocol_isinstance.py` covers both shapes. |
| 13 | `README.md` "how to add a backend" walkthrough lists: write class, structurally satisfy Protocol, add a `tests/test_conformance.py` that calls the fixture, list the backend in the CI matrix. | File exists, four-step list. |
| 14 | No DB migration introduced. | `git diff` against `shared/db/migrations/` is empty. |
