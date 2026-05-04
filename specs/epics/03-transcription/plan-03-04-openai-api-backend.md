---
name: Plan 03-04 — OpenAI API backend
description: Implementation plan for Epic 3 Story 4 (OpenAI Whisper API backend). Wraps the official Whisper endpoint with size-based chunking (24 MB), silence pre-stripping with a silence-map for original-timeline timestamps, exponential-backoff retry on 429/5xx, and a per-library monthly USD budget cap enforced before claim via a new `stt_usage` ledger and a budget-projection Go API endpoint.
type: plan
---

# Plan 03-04 — OpenAI API Backend

> **Canonical story:** [story-03-04-openai-api-backend.md](story-03-04-openai-api-backend.md).
>
> **Depends on:** [Plan 03-01](plan-03-01-backend-protocol.md) (Protocol,
> types, errors, conformance suite). Cross-cuts the orchestrator: this
> story introduces a budget-cap check that the *claim* path
> ([Story 6.3 claim loop](../06-job-queue/) and [Plan 03-05](plan-03-05-backend-registry.md))
> consults before flipping a job to `running`.
>
> **Architecture references.** [§3.4 Transcriber](../../architecture.md)
> lists `openai-api` as the network-bound backend; [§11.4 pipeline.toml](../../architecture.md)
> defines `[stt.backends.openai] api_key_env = "OPENAI_API_KEY"`,
> `model = "whisper-1"`, `max_usd_per_month = 50`; [§11.5 Secrets](../../architecture.md)
> requires that `OPENAI_API_KEY` is read by the Pipeline service only —
> the Streaming and (default) API services never see it.
>
> **Out of scope.** Backend registry / fallback chain (3.5). Per-segment
> commit (3.6). Pause / resume orchestration (3.7). Diarization (3.9) —
> the OpenAI Whisper endpoint does not provide speaker labels. The
> *future* `gpt-4o-transcribe` endpoint variant is reserved as a
> separate model id; the wrapper's chunking and budget logic apply
> unchanged.

---

## 1. Architecture diagram

```
   Orchestrator (transcribe stage)
            │  audio: AudioSource (always _FilePath here — requires_file=True)
            │  language, hints
            ▼
┌─────────────────────────────────────────────────────────────────────┐
│  OpenAIWhisperBackend  pipeline/stt/openai_api/backend.py          │
│                                                                     │
│  __init__(api_key, model="whisper-1", *, base_url=None,             │
│           max_chunk_bytes=24 * 1024 * 1024,                          │
│           silence_strip_threshold_sec=5.0,                           │
│           retry_max=5, retry_base=0.5, retry_jitter=0.25)           │
│                                                                     │
│  async transcribe(audio, language, hints):                          │
│    1. Materialize input WAV path                                    │
│       (orchestrator handed us _FilePath because requires_file=True) │
│    2. Strip silences > silence_strip_threshold_sec via              │
│       ffmpeg -af silenceremove → working WAV + silence_map          │
│    3. Chunk working WAV by file size into ≤ max_chunk_bytes WAVs    │
│    4. For each chunk:                                               │
│       a. POST /v1/audio/transcriptions with verbose_json            │
│          + timestamp_granularities=["segment", "word" if hints.wt]  │
│       b. On 429/5xx → retry with exponential backoff + jitter       │
│       c. Convert response.segments to Segment using                 │
│          rebase_to_original_timeline(seg, chunk_offset_in_working,  │
│                                      silence_map)                   │
│       d. yield Segment                                              │
│    5. Always: unlink temp chunks/working WAV in `finally`           │
│                                                                     │
│  detect_language(audio):                                            │
│    POST first 30 s; read response.language; map to ISO 639-1        │
│                                                                     │
│  health(): TCP-only — verifies api_key present + base_url reachable │
│    via HEAD /; never charges.                                       │
│                                                                     │
│  estimate_cost_usd(duration_sec): duration_sec/60 * cost_per_minute │
└─────────────────────────────────────────────────────────────────────┘
            │ persists usage on success only
            ▼
┌─────────────────────────────────────────────────────────────────────┐
│  stt_usage ledger (NEW table — see §5)                              │
│   one row per successful chunk: video_id, audio_track_id, backend,  │
│   model, billed_seconds, usd_cost, billed_at                        │
└─────────────────────────────────────────────────────────────────────┘
            │ read by
            ▼
┌─────────────────────────────────────────────────────────────────────┐
│  Budget-cap pre-claim check (orchestrator-side, story 3.5)         │
│   For each pending transcribe job:                                  │
│     projected = videos.duration_sec/60 * cost_per_minute            │
│     month_used = SUM(usd_cost) WHERE backend=$1 AND                 │
│                  date_trunc('month', billed_at) = current_month     │
│     IF month_used + projected > max_usd_per_month: refuse claim,    │
│        set processing_jobs.not_before = first-of-next-month,        │
│        error.kind = "budget_cap"                                    │
└─────────────────────────────────────────────────────────────────────┘
```

Six things to notice:

1. **`requires_file = True`.** The OpenAI endpoint takes a multipart
   file upload, not a stream. Plan 02-03's file-mode extractor produces
   a 16 kHz mono WAV that we hand directly to the backend.
2. **Silence pre-stripping with a silence map.** The OpenAI endpoint
   silently truncates internal decode windows on long silences and
   produces messy timestamps. We strip silences > 5 s with
   `ffmpeg -af silenceremove`, but record exactly what we stripped so
   we can rebase the API's chunk-local timestamps back to the
   *original* audio timeline before yielding. Story 3.4 acceptance
   ("audio that includes silence longer than the API's 30 s
   internal-window limit").
3. **Size-based chunking, not time-based.** The 24 MB API limit is on
   bytes, not seconds. With 16-bit 16 kHz mono PCM (32 KB/s = 1.92
   MB/min), 24 MB ≈ 12.5 min of WAV with the WAV header. We pick a
   safety margin and split at ~12 min, snapping to silence boundaries
   when possible to avoid mid-word cuts.
4. **Re-stitching is verified by integration test.** Story 3.4 requires
   a 90-min fixture. The integration test runs the chunker offline (no
   real API call) plus a unit test that runs against a recorded
   `vcr.py` cassette captures of the actual API response shape.
5. **Budget cap is enforced before claim, not after.** The cost would
   already be incurred if we waited until after the API call. The
   orchestrator runs a projection query against `stt_usage` and the
   library's `max_usd_per_month` and refuses to flip the job to
   `running` if it'd push the calendar month over budget.
6. **`OPENAI_API_KEY` is read once, in pipeline only.** Per
   architecture §11.5. The key is loaded into pydantic settings at
   startup; never logged; never returned by `/api/settings`. The Go
   API endpoint that returns budget status (`GET /api/system/stt/budget`)
   reads the **ledger**, not the key.

---

## 2. New artifacts

| Layer | Path | Status | Purpose |
|---|---|---|---|
| Python | `pipeline/src/maktaba_pipeline/stt/openai_api/__init__.py` | **new** | Package marker; exports `OpenAIWhisperBackend`. |
| Python | `pipeline/src/maktaba_pipeline/stt/openai_api/backend.py` | **new** | The backend class. |
| Python | `pipeline/src/maktaba_pipeline/stt/openai_api/_chunker.py` | **new** | `chunk_wav_by_size(path, max_bytes) -> list[ChunkSpec]`; snaps to silence when possible. |
| Python | `pipeline/src/maktaba_pipeline/stt/openai_api/_silence.py` | **new** | `strip_silences(path, threshold_sec) -> (working_path, silence_map)` and `rebase_to_original_timeline(seg, working_offset, silence_map)`. |
| Python | `pipeline/src/maktaba_pipeline/stt/openai_api/_retry.py` | **new** | `async_retry(fn, *, max_attempts, base, jitter)` — exponential backoff with ±25% jitter. |
| Python | `pipeline/src/maktaba_pipeline/stt/openai_api/_pricing.py` | **new** | `COST_PER_MINUTE_USD` dict keyed by model; build-time-pinned (story 3.4 acceptance: "populated from the live price list at package build time"). |
| Python | `pipeline/src/maktaba_pipeline/stt/openai_api/_budget.py` | **new** | `BudgetLedger.record(...)`, `BudgetLedger.month_used(...)`. Postgres-backed. |
| Python | `pipeline/src/maktaba_pipeline/stt/openai_api/tests/test_conformance.py` | **new** | Conformance against a recorded cassette + a fake `httpx` transport. Real-API run gated on `OPENAI_API_KEY` env var. |
| Python | `pipeline/src/maktaba_pipeline/stt/openai_api/tests/test_chunking_preserves_timestamps.py` | **new** | 90-min fixture; assert tile contiguity within ε of single-call equivalent. |
| Python | `pipeline/src/maktaba_pipeline/stt/openai_api/tests/test_budget_cap.py` | **new** | Set cap = $0.10; 30 min job → claim refused with `not_before = first of next month`. |
| Python | `pipeline/src/maktaba_pipeline/stt/openai_api/tests/test_retry_on_429.py` | **new** | Cassette returns 429 then 200 → retry once; 5× 429 → fail. |
| Python | `pipeline/src/maktaba_pipeline/stt/openai_api/tests/test_silence_map_60s_gap.py` | **new** | Fixture with a known 60 s silence in the middle → segment timestamps are in original timeline. |
| Python | `pipeline/src/maktaba_pipeline/stt/openai_api/tests/test_no_confidence_field.py` | **new** | API response without `avg_logprob` → `Segment.confidence is None`. |
| Python | `pipeline/src/maktaba_pipeline/stt/openai_api/tests/test_health.py` | **new** | Missing `OPENAI_API_KEY` → `health().ready=False, reason="OPENAI_API_KEY missing"`. |
| Python | `pipeline/src/maktaba_pipeline/stt/openai_api/tests/cassettes/` | **new** | `vcrpy` recordings of canonical responses (200, 429, 5xx, no-confidence). Recorded once, replayed in CI. |
| Migration | `shared/db/migrations/0011_stt_usage.sql` | **new** | Creates `stt_usage` ledger table + supporting index. |
| Go | `apps/api/internal/http/system/stt_budget.go` | **new** | `GET /api/system/stt/budget` returns current month's spend per backend. |
| Go | `apps/api/internal/http/system/stt_budget_test.go` | **new** | Handler tests using sqlc-generated queries. |
| Go (sqlc) | `shared/db/queries/stt_usage.sql` | **new** | `GetMonthlyStttUsage(backend, month_start, month_end)`, `InsertStttUsage(...)`. |
| Config | `pipeline/pyproject.toml` | **edit** | Add `httpx>=0.27`, `vcrpy>=6` (test only). The `openai` SDK is **not** used; we hit the REST endpoint directly with `httpx` to keep retry control and to avoid the SDK's auto-retry. |
| Config | `pipeline/src/maktaba_pipeline/settings.py` | **edit** | Add `[stt.backends.openai]` section: `api_key_env`, `model`, `max_usd_per_month`, `base_url` (override for testing/proxy), `silence_strip_threshold_sec`. |
| Docs | `pipeline/src/maktaba_pipeline/stt/openai_api/README.md` | **new** | "When to use, cost projection table, how to record cassettes for tests." |

---

## 3. Implementation

### 3.1 `_pricing.py`

```python
"""Pinned at package build time.

Updated by a release script that scrapes platform.openai.com/docs/pricing.
We never call a live pricing endpoint at runtime — that would couple
backend health to a marketing page.
"""
from typing import Final

COST_PER_MINUTE_USD: Final[dict[str, float]] = {
    "whisper-1":       0.006,   # $0.006 per minute, billed per second
    "gpt-4o-transcribe": 0.006,  # placeholder, refresh on release
}
```

### 3.2 `_silence.py`

```python
"""Silence pre-stripping with an inverse map for timestamp rebasing.

The OpenAI Whisper endpoint internally chunks at 30 s windows; long
silences confuse the segmenter. We strip silences > threshold_sec via
ffmpeg's `silenceremove`, recording the *removed* spans so we can
rebase the API's working-timeline timestamps back to the original
audio timeline before yielding Segments.

The silence map is a list[(orig_silence_start, orig_silence_end)].
Working timeline t_w corresponds to original t_o = t_w + Σ silence_lengths
that occur *before* the equivalent original time. Because silences
were removed, working time is contiguous; original time has gaps.
"""
from __future__ import annotations

import asyncio
import json
import re
from dataclasses import dataclass
from pathlib import Path


@dataclass(frozen=True)
class SilenceSpan:
    orig_start: float
    orig_end: float

    @property
    def duration(self) -> float:
        return self.orig_end - self.orig_start


SilenceMap = list[SilenceSpan]


async def strip_silences(
    path: Path,
    threshold_sec: float,
    *,
    scratch_dir: Path,
    noise_db: int = -30,
) -> tuple[Path, SilenceMap]:
    """Run ffmpeg twice:

    1. `-af silencedetect=...` to discover silences in the original
       audio (does not modify it). Parse stderr for
       `silence_start: X` / `silence_end: Y | silence_duration: Z`.
    2. `-af silenceremove=stop_periods=-1:stop_silence=<threshold>:stop_threshold=<noise_db>dB`
       to produce the working WAV.

    Two passes (vs one with `silencedetect,silenceremove` in series) is
    intentional: silenceremove changes timestamps, silencedetect must
    see the original.
    """
    # 1. Detection
    detect_cmd = [
        "ffmpeg", "-hide_banner", "-nostats", "-nostdin",
        "-i", str(path),
        "-af", f"silencedetect=noise={noise_db}dB:d={threshold_sec}",
        "-f", "null", "-",
    ]
    proc = await asyncio.create_subprocess_exec(
        *detect_cmd,
        stdout=asyncio.subprocess.DEVNULL,
        stderr=asyncio.subprocess.PIPE,
    )
    _, stderr = await proc.communicate()
    silences = _parse_silence_log(stderr.decode("utf-8", errors="replace"))

    # 2. Removal
    out_path = scratch_dir / f"{path.stem}.dehushed.wav"
    remove_cmd = [
        "ffmpeg", "-hide_banner", "-nostats", "-nostdin", "-y",
        "-i", str(path),
        "-af",
        (
            f"silenceremove="
            f"start_periods=0:"
            f"stop_periods=-1:"
            f"stop_silence={threshold_sec}:"
            f"stop_threshold={noise_db}dB"
        ),
        "-ac", "1", "-ar", "16000", "-sample_fmt", "s16",
        str(out_path),
    ]
    proc = await asyncio.create_subprocess_exec(
        *remove_cmd, stdout=asyncio.subprocess.DEVNULL, stderr=asyncio.subprocess.PIPE,
    )
    _, stderr2 = await proc.communicate()
    if proc.returncode != 0:
        raise RuntimeError(f"silenceremove failed: {stderr2.decode()}")
    return out_path, silences


def _parse_silence_log(stderr: str) -> SilenceMap:
    starts = [float(m.group(1)) for m in re.finditer(r"silence_start: ([\d.]+)", stderr)]
    ends = [float(m.group(1)) for m in re.finditer(r"silence_end: ([\d.]+)", stderr)]
    return [SilenceSpan(s, e) for s, e in zip(starts, ends)]


def working_to_original(t_working: float, silence_map: SilenceMap) -> float:
    """Map a working-timeline timestamp back to the original timeline.

    Walk through silence_map in order; each span that ends at-or-before
    the equivalent original time has been removed and so adds its
    duration to the offset. The equivalent original time is the
    accumulator we're computing — this needs care:

      t_o = t_w + Σ_{s ∈ silence_map, s.orig_start <= t_o} s.duration

    We compute this iteratively (silences are typically <100 per file).
    """
    t_o = t_working
    for span in silence_map:
        if span.orig_start <= t_o:
            t_o += span.duration
        else:
            break
    return t_o
```

### 3.3 `_chunker.py`

```python
"""Split a working WAV into ≤ max_bytes WAV chunks, snapping to
silence midpoints when available."""
from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path

import numpy as np


@dataclass
class ChunkSpec:
    path: Path
    working_offset_sec: float  # offset of this chunk in the working WAV
    duration_sec: float


def chunk_wav_by_size(
    src: Path,
    max_bytes: int,
    *,
    scratch_dir: Path,
    sample_rate: int = 16_000,
    sample_width: int = 2,
) -> list[ChunkSpec]:
    """Cut into chunks of at most `max_bytes`. We compute each chunk's
    sample count from `(max_bytes - WAV_HEADER_BYTES) // sample_width`
    and write a WAV with the correct header in one go.

    A simpler implementation would call ffmpeg with `-segment_time`,
    but ffmpeg's `-segment` muxer does not align to sample frames
    cleanly with WAV; we'd risk one-sample drift per chunk. Instead we
    read the source via `wave` and write fixed-size WAVs manually.
    """
    import wave  # noqa: PLC0415
    samples_per_chunk = (max_bytes - 44) // sample_width  # WAV hdr ≈ 44 bytes

    chunks: list[ChunkSpec] = []
    with wave.open(str(src), "rb") as wf:
        assert wf.getframerate() == sample_rate and wf.getnchannels() == 1
        total = wf.getnframes()
        i = 0
        idx = 0
        while i < total:
            n = min(samples_per_chunk, total - i)
            wf.setpos(i)
            data = wf.readframes(n)
            out = scratch_dir / f"{src.stem}.chunk{idx:03d}.wav"
            with wave.open(str(out), "wb") as wo:
                wo.setnchannels(1)
                wo.setsampwidth(sample_width)
                wo.setframerate(sample_rate)
                wo.writeframes(data)
            chunks.append(ChunkSpec(
                path=out,
                working_offset_sec=i / sample_rate,
                duration_sec=n / sample_rate,
            ))
            i += n
            idx += 1
    return chunks
```

### 3.4 `_retry.py`

```python
"""Exponential backoff with jitter."""
from __future__ import annotations

import asyncio
import random
from collections.abc import Awaitable, Callable
from typing import TypeVar

import httpx

from ..errors import BackendTransient

T = TypeVar("T")

_RETRYABLE_STATUSES = frozenset({429, 500, 502, 503, 504})


async def with_retry(
    fn: Callable[[], Awaitable[T]],
    *,
    max_attempts: int = 5,
    base: float = 0.5,
    factor: float = 2.0,
    jitter: float = 0.25,
) -> T:
    last_exc: BaseException | None = None
    for attempt in range(max_attempts):
        try:
            return await fn()
        except httpx.HTTPStatusError as exc:
            last_exc = exc
            if exc.response.status_code not in _RETRYABLE_STATUSES:
                raise
        except (httpx.TimeoutException, httpx.NetworkError) as exc:
            last_exc = exc

        if attempt == max_attempts - 1:
            break
        delay = base * (factor ** attempt)
        delay *= 1 + random.uniform(-jitter, jitter)  # noqa: S311
        await asyncio.sleep(delay)

    raise BackendTransient(f"OpenAI Whisper retries exhausted: {last_exc!r}")
```

### 3.5 `_budget.py`

```python
"""Postgres-backed monthly usage ledger.

The orchestrator (story 3.5) calls `month_used(backend, now)` *before*
flipping a job to running, and the backend calls `record(...)` only
after a chunk's API call succeeds and the segments have been emitted.
"""
from __future__ import annotations

import datetime as dt
from dataclasses import dataclass
from uuid import UUID

import asyncpg


@dataclass
class UsageRow:
    video_id: UUID
    audio_track_id: int
    backend: str
    model: str
    billed_seconds: float
    usd_cost: float
    chunk_index: int


class BudgetLedger:
    def __init__(self, pool: asyncpg.Pool) -> None:
        self._pool = pool

    async def month_used(self, backend: str, now: dt.datetime) -> float:
        """Return USD already billed for `backend` this calendar month."""
        month_start = now.replace(day=1, hour=0, minute=0, second=0, microsecond=0)
        async with self._pool.acquire() as conn:
            v = await conn.fetchval(
                """
                SELECT COALESCE(SUM(usd_cost), 0)::FLOAT8
                FROM stt_usage
                WHERE backend = $1
                  AND billed_at >= $2
                """,
                backend, month_start,
            )
        return v or 0.0

    async def record(self, row: UsageRow) -> None:
        async with self._pool.acquire() as conn:
            await conn.execute(
                """
                INSERT INTO stt_usage
                  (video_id, audio_track_id, backend, model,
                   billed_seconds, usd_cost, chunk_index, billed_at)
                VALUES ($1, $2, $3, $4, $5, $6, $7, now())
                """,
                row.video_id, row.audio_track_id, row.backend, row.model,
                row.billed_seconds, row.usd_cost, row.chunk_index,
            )
```

### 3.6 `backend.py`

```python
"""OpenAIWhisperBackend.

httpx-based, no openai SDK; we want full control over retry, timeouts,
streaming response-body iteration, and api-key handling.
"""
from __future__ import annotations

import asyncio
import os
import time
from collections.abc import AsyncIterator
from pathlib import Path
from uuid import UUID

import httpx

from ..errors import BackendBudgetExceeded, BackendFatal, BackendNotReady
from ..types import (
    AudioSource, BackendHealth, Segment, TranscriptionHints, Word, _FilePath,
)
from ._budget import BudgetLedger, UsageRow
from ._chunker import ChunkSpec, chunk_wav_by_size
from ._pricing import COST_PER_MINUTE_USD
from ._retry import with_retry
from ._silence import strip_silences, working_to_original


class OpenAIWhisperBackend:
    name = "openai-api"
    supports_streaming = False
    supports_word_timestamps = True
    requires_file = True

    def __init__(
        self,
        *,
        api_key: str | None = None,
        model: str = "whisper-1",
        base_url: str = "https://api.openai.com/v1",
        max_chunk_bytes: int = 24 * 1024 * 1024,
        silence_strip_threshold_sec: float = 5.0,
        retry_max: int = 5,
        retry_base: float = 0.5,
        retry_jitter: float = 0.25,
        scratch_dir: Path | None = None,
        ledger: BudgetLedger | None = None,
        # Caller context for ledger writes:
        video_id: UUID | None = None,
        audio_track_id: int | None = None,
    ) -> None:
        self._api_key = api_key or os.environ.get("OPENAI_API_KEY")
        self.model_id = model
        self._base_url = base_url
        self._max_chunk_bytes = max_chunk_bytes
        self._silence_threshold = silence_strip_threshold_sec
        self._retry_max = retry_max
        self._retry_base = retry_base
        self._retry_jitter = retry_jitter
        self._scratch_dir = scratch_dir or Path("/tmp")
        self._ledger = ledger
        self._video_id = video_id
        self._audio_track_id = audio_track_id

    @property
    def cost_per_minute(self) -> float:
        return COST_PER_MINUTE_USD[self.model_id]

    # ------------------------------------------------------------------ Lifecycle

    async def warmup(self) -> None:
        if not self._api_key:
            raise BackendNotReady("OPENAI_API_KEY missing")

    async def close(self) -> None:
        return None

    async def health(self) -> BackendHealth:
        if not self._api_key:
            return BackendHealth(
                ready=False, model_loaded=False, version=self.model_id,
                device="remote", last_check_at=time.time(),
                reason="OPENAI_API_KEY missing",
            )
        # Cheap reachability check; never charges.
        try:
            async with httpx.AsyncClient(timeout=5.0) as c:
                r = await c.head(self._base_url)
            ready = r.status_code < 500
            reason = None if ready else f"base_url returned {r.status_code}"
        except httpx.HTTPError as exc:
            ready = False
            reason = f"base_url unreachable: {exc!r}"
        return BackendHealth(
            ready=ready, model_loaded=True, version=self.model_id,
            device="remote", last_check_at=time.time(), reason=reason,
        )

    # ------------------------------------------------------------------ Inference

    async def detect_language(self, audio: AudioSource) -> str:
        await self.warmup()
        if not isinstance(audio, _FilePath):
            raise BackendFatal("openai-api requires _FilePath AudioSource")
        # Transcribe first 30 s with no language pin and read response.language.
        # We trim with ffmpeg to keep the upload small.
        clip = await _trim_first_n_seconds(audio.path, n=30.0, scratch=self._scratch_dir)
        try:
            data = await self._post_transcribe(
                clip, language=None, word_timestamps=False,
            )
            return (data.get("language") or "en")[:2]
        finally:
            clip.unlink(missing_ok=True)

    async def transcribe(
        self,
        audio: AudioSource,
        language: str | None,
        hints: TranscriptionHints,
    ) -> AsyncIterator[Segment]:
        await self.warmup()
        if not isinstance(audio, _FilePath):
            raise BackendFatal("openai-api requires _FilePath AudioSource")
        if hints.start_offset_sec > 0.0:
            raise NotImplementedError(
                "openai-api does not support start_offset_sec; "
                "the orchestrator must extract a trimmed file"
            )

        scratch = self._scratch_dir
        working_path, silence_map = await strip_silences(
            audio.path, self._silence_threshold, scratch_dir=scratch,
        )
        chunks: list[ChunkSpec] = []
        try:
            chunks = chunk_wav_by_size(
                working_path, self._max_chunk_bytes, scratch_dir=scratch,
            )
            seq = 0
            for idx, chunk in enumerate(chunks):
                data = await self._post_transcribe(
                    chunk.path,
                    language=language,
                    word_timestamps=hints.word_timestamps,
                )
                for raw in data.get("segments", []):
                    seg = self._convert(
                        raw, seq, hints,
                        chunk_offset=chunk.working_offset_sec,
                        silence_map=silence_map,
                    )
                    if not seg.text:
                        continue
                    yield seg
                    seq += 1
                # Ledger: bill per chunk, only after segments emitted.
                await self._record_usage(
                    chunk_index=idx, billed_seconds=chunk.duration_sec,
                )
        finally:
            for c in chunks:
                c.path.unlink(missing_ok=True)
            working_path.unlink(missing_ok=True)

    # ------------------------------------------------------------------ HTTP

    async def _post_transcribe(
        self,
        wav: Path,
        *,
        language: str | None,
        word_timestamps: bool,
    ) -> dict:
        granularities = ["segment"]
        if word_timestamps:
            granularities.append("word")

        async def call() -> dict:
            async with httpx.AsyncClient(timeout=600.0) as client:
                with wav.open("rb") as f:
                    files = {"file": (wav.name, f, "audio/wav")}
                    data = {
                        "model": self.model_id,
                        "response_format": "verbose_json",
                        "timestamp_granularities[]": granularities,
                    }
                    if language:
                        data["language"] = language
                    headers = {"Authorization": f"Bearer {self._api_key}"}
                    r = await client.post(
                        f"{self._base_url}/audio/transcriptions",
                        headers=headers, data=data, files=files,
                    )
                    r.raise_for_status()
                    return r.json()

        return await with_retry(
            call,
            max_attempts=self._retry_max,
            base=self._retry_base,
            jitter=self._retry_jitter,
        )

    def _convert(
        self,
        raw: dict,
        seq: int,
        hints: TranscriptionHints,
        *,
        chunk_offset: float,
        silence_map,
    ) -> Segment:
        # raw["start"]/raw["end"] are chunk-local on the WORKING timeline.
        # First add the chunk offset (still working timeline), then rebase
        # into the original timeline via working_to_original.
        s_w = float(raw["start"]) + chunk_offset
        e_w = float(raw["end"]) + chunk_offset
        s_o = working_to_original(s_w, silence_map)
        e_o = working_to_original(e_w, silence_map)

        words: list[Word] | None = None
        if hints.word_timestamps and raw.get("words"):
            words = []
            for i, w in enumerate(raw["words"]):
                ws_w = float(w["start"]) + chunk_offset
                we_w = float(w["end"]) + chunk_offset
                words.append(Word(
                    seq=i,
                    start=working_to_original(ws_w, silence_map),
                    end=working_to_original(we_w, silence_map),
                    text=w["word"].strip(),
                    confidence=None,  # API does not provide per-word confidence
                ))
        return Segment(
            seq=seq,
            start=s_o,
            end=e_o,
            text=(raw.get("text") or "").strip(),
            confidence=None,  # API verbose_json does not expose per-segment confidence
            words=words,
            metadata={"working_start": s_w, "working_end": e_w},
        )

    async def _record_usage(self, *, chunk_index: int, billed_seconds: float) -> None:
        if self._ledger is None:
            return
        if self._video_id is None or self._audio_track_id is None:
            return
        await self._ledger.record(UsageRow(
            video_id=self._video_id,
            audio_track_id=self._audio_track_id,
            backend=self.name,
            model=self.model_id,
            billed_seconds=billed_seconds,
            usd_cost=billed_seconds / 60.0 * self.cost_per_minute,
            chunk_index=chunk_index,
        ))


async def _trim_first_n_seconds(path: Path, *, n: float, scratch: Path) -> Path:
    out = scratch / f"{path.stem}.lang.wav"
    proc = await asyncio.create_subprocess_exec(
        "ffmpeg", "-hide_banner", "-nostats", "-nostdin", "-y",
        "-i", str(path), "-t", str(n),
        "-ac", "1", "-ar", "16000", "-sample_fmt", "s16",
        str(out),
        stdout=asyncio.subprocess.DEVNULL, stderr=asyncio.subprocess.DEVNULL,
    )
    await proc.wait()
    return out
```

### 3.7 The pre-claim budget check (orchestrator-side, story 3.5 owns the wiring)

```python
# pipeline/src/maktaba_pipeline/pipeline/stages/transcribe.py (excerpt)
async def claim_or_defer(
    job: Job, video: Video, settings: LibrarySettings, ledger: BudgetLedger,
) -> ClaimDecision:
    backend = registry.get(settings.stt.backend)
    if backend.cost_per_minute is None or backend.cost_per_minute == 0.0:
        return ClaimDecision.proceed()

    cap = settings.stt.backends.get(backend.name, {}).get("max_usd_per_month")
    if cap is None:
        return ClaimDecision.proceed()

    projected = (video.duration_sec or 0.0) / 60.0 * backend.cost_per_minute
    used = await ledger.month_used(backend.name, datetime.now(timezone.utc))
    if used + projected <= cap:
        return ClaimDecision.proceed()

    return ClaimDecision.defer(
        not_before=_first_of_next_month(),
        reason="budget_cap",
        details={"projected": projected, "used": used, "cap": cap},
    )
```

The `ClaimDecision.defer(...)` path writes
`processing_jobs.not_before = first-of-next-month` and
`error.kind = "budget_cap"`; the claim loop respects `not_before`.

---

## 4. Test plan

### 4.1 `test_conformance.py`

Runs the conformance suite against an `httpx.MockTransport` that
serves cassetted responses for the two fixture WAVs. The same suite
also runs against the real API in a once-a-day scheduled CI job that
requires `OPENAI_API_KEY`.

```python
import os
import pytest

from maktaba_pipeline.stt.conformance.suite import stt_conformance_suite
from maktaba_pipeline.stt.openai_api import OpenAIWhisperBackend


@pytest.mark.skipif(
    not os.environ.get("OPENAI_API_KEY") and not os.environ.get("USE_CASSETTES"),
    reason="needs API key or cassette mode",
)
def test_conformance(tmp_path):
    backend = OpenAIWhisperBackend(scratch_dir=tmp_path)
    stt_conformance_suite(backend)
```

### 4.2 `test_chunking_preserves_timestamps.py`

```python
"""Story 3.4 test case: 90-min fixture; assert tile contiguity within ε
of single-call equivalent.

The fixture is a 90-min synthetic clip — 18 × 5-min tiles concatenated,
each tile a different speaker reading a known sentence. The reference
single-call response is recorded once via cassette.
"""
import math

import pytest

from maktaba_pipeline.stt.audio_source import from_file
from maktaba_pipeline.stt.openai_api import OpenAIWhisperBackend
from maktaba_pipeline.stt.types import TranscriptionHints
from .helpers import open_90min_fixture, load_reference_segments


@pytest.mark.usefixtures("cassette_chunked", "cassette_single_call")
async def test_chunked_segments_match_single_call(tmp_path):
    backend = OpenAIWhisperBackend(scratch_dir=tmp_path, max_chunk_bytes=8 * 1024 * 1024)
    src = from_file(open_90min_fixture())
    chunked = []
    async for s in backend.transcribe(src, "en", TranscriptionHints()):
        chunked.append(s)
    ref = load_reference_segments()  # produced from a single-call cassette

    assert len(chunked) == len(ref)
    for a, b in zip(chunked, ref):
        assert math.isclose(a.start, b.start, abs_tol=0.5)
        assert math.isclose(a.end, b.end, abs_tol=0.5)
```

### 4.3 `test_budget_cap.py`

```python
"""Story 3.4 test case: cap = $0.10 → 30 min job claim refused, pushed
to next month.
"""
import datetime as dt

from maktaba_pipeline.pipeline.stages.transcribe import claim_or_defer
from .helpers import fake_settings_with_cap, fake_video_30min, fake_ledger_with_zero_used


async def test_budget_cap_defers_claim():
    settings = fake_settings_with_cap(0.10)
    video = fake_video_30min()  # 30 * 0.006 = $0.18 projected → over $0.10 cap
    ledger = fake_ledger_with_zero_used()

    decision = await claim_or_defer(job=..., video=video, settings=settings, ledger=ledger)
    assert decision.action == "defer"
    assert decision.reason == "budget_cap"
    today = dt.datetime.now(dt.timezone.utc)
    expected = (today.replace(day=1) + dt.timedelta(days=32)).replace(day=1)
    assert decision.not_before.date() == expected.date()
```

### 4.4 `test_retry_on_429.py`

```python
import httpx
import pytest

from maktaba_pipeline.stt.errors import BackendTransient
from maktaba_pipeline.stt.openai_api._retry import with_retry


async def test_429_then_200_returns_success():
    calls = {"n": 0}

    async def call() -> dict:
        calls["n"] += 1
        if calls["n"] == 1:
            req = httpx.Request("POST", "/x")
            raise httpx.HTTPStatusError(
                "rate limited", request=req,
                response=httpx.Response(429, request=req),
            )
        return {"ok": True}

    out = await with_retry(call, max_attempts=5, base=0.0, jitter=0.0)
    assert out == {"ok": True}
    assert calls["n"] == 2


async def test_five_429_raises_transient():
    async def call():
        req = httpx.Request("POST", "/x")
        raise httpx.HTTPStatusError(
            "rate limited", request=req,
            response=httpx.Response(429, request=req),
        )

    with pytest.raises(BackendTransient):
        await with_retry(call, max_attempts=5, base=0.0, jitter=0.0)
```

### 4.5 `test_silence_map_60s_gap.py`

```python
"""Fixture: 30 s speech + 60 s silence + 30 s speech = 120 s total.
After silence-strip, working WAV is 60 s; API segments are in working
time. After rebase, the second-half segments must start at >= 90 s in
the original timeline.
"""
from maktaba_pipeline.stt.openai_api._silence import (
    SilenceSpan, working_to_original,
)


def test_rebase_after_60s_silence():
    sm = [SilenceSpan(orig_start=30.0, orig_end=90.0)]
    # In working time, second-half speech starts at 30.0; in original it's 90.0.
    assert working_to_original(30.0, sm) == 90.0
    assert working_to_original(31.5, sm) == 91.5


def test_rebase_no_silence_is_identity():
    assert working_to_original(42.0, []) == 42.0
```

### 4.6 `test_no_confidence_field.py`

```python
"""Story 3.4 edge case: API verbose_json returns segments without
avg_logprob; Segment.confidence must be None and downstream must not
crash."""
from maktaba_pipeline.stt.openai_api.backend import OpenAIWhisperBackend
from maktaba_pipeline.stt.types import TranscriptionHints


def test_convert_passes_none_confidence():
    backend = OpenAIWhisperBackend(api_key="sk-test")
    seg = backend._convert(
        {"start": 0.0, "end": 1.0, "text": "hi"},
        seq=0, hints=TranscriptionHints(word_timestamps=False),
        chunk_offset=0.0, silence_map=[],
    )
    assert seg.confidence is None
    assert seg.text == "hi"
```

### 4.7 `test_health.py`

```python
async def test_no_api_key_health_false(monkeypatch):
    monkeypatch.delenv("OPENAI_API_KEY", raising=False)
    h = await OpenAIWhisperBackend().health()
    assert h.ready is False
    assert "OPENAI_API_KEY" in (h.reason or "")
```

---

## 5. SQL migrations

### 5.1 `shared/db/migrations/0011_stt_usage.sql`

```sql
-- +goose Up
-- 0011_stt_usage.sql — per-chunk billing ledger for paid STT backends.
--
-- Ownership: written exclusively by the OpenAI-API backend after a
-- chunk's API call returns 200 and segments have been yielded. Read by
-- the orchestrator's pre-claim budget check (story 3.5) and by the
-- Go API's GET /api/system/stt/budget endpoint.

CREATE TABLE stt_usage (
    id              BIGSERIAL PRIMARY KEY,
    video_id        UUID    NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    audio_track_id  BIGINT  NOT NULL REFERENCES audio_tracks(id),
    backend         TEXT    NOT NULL,
    model           TEXT    NOT NULL,
    chunk_index     INT     NOT NULL,
    billed_seconds  REAL    NOT NULL,
    usd_cost        REAL    NOT NULL,
    billed_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The pre-claim check sums usd_cost for a (backend, calendar-month)
-- pair; this index makes that O(month_rows) without touching the heap.
CREATE INDEX stt_usage_backend_billed_at
    ON stt_usage (backend, billed_at);

-- Convenience lookup for "what did this video cost".
CREATE INDEX stt_usage_video
    ON stt_usage (video_id, audio_track_id);

-- +goose Down
DROP INDEX IF EXISTS stt_usage_video;
DROP INDEX IF EXISTS stt_usage_backend_billed_at;
DROP TABLE IF EXISTS stt_usage;
```

### 5.2 `shared/db/queries/stt_usage.sql` (sqlc)

```sql
-- name: GetMonthlyStttUsage :one
SELECT COALESCE(SUM(usd_cost), 0)::FLOAT8 AS total_usd
FROM stt_usage
WHERE backend = $1
  AND billed_at >= $2
  AND billed_at <  $3;

-- name: InsertStttUsage :exec
INSERT INTO stt_usage
  (video_id, audio_track_id, backend, model, chunk_index,
   billed_seconds, usd_cost)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListBackendsWithMonthlyUsage :many
SELECT backend,
       COALESCE(SUM(usd_cost), 0)::FLOAT8 AS total_usd,
       COUNT(*)                          AS chunks
FROM stt_usage
WHERE billed_at >= $1 AND billed_at < $2
GROUP BY backend
ORDER BY backend;
```

### 5.3 Go endpoint — `GET /api/system/stt/budget`

```go
// apps/api/internal/http/system/stt_budget.go
func (h *Handlers) GetSTTBudget(w http.ResponseWriter, r *http.Request) {
    monthStart, monthEnd := currentMonth(time.Now().UTC())
    rows, err := h.q.ListBackendsWithMonthlyUsage(r.Context(),
        sqlc.ListBackendsWithMonthlyUsageParams{Column1: monthStart, Column2: monthEnd})
    if err != nil {
        problem.Internal(w, r, err)
        return
    }
    body := map[string]any{
        "month_start": monthStart,
        "month_end":   monthEnd,
        "backends":    rows,  // [{ backend, total_usd, chunks }]
    }
    json.NewEncoder(w).Encode(body)
}

func currentMonth(now time.Time) (time.Time, time.Time) {
    start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
    return start, start.AddDate(0, 1, 0)
}
```

The endpoint requires an authenticated user (admin scope only — the
budget table is operationally sensitive). The response contains no
secrets; the API key is never exposed.

---

## 6. Edge cases (story 3.4) — explicit handling

| Story §Edge case | Handling here |
|---|---|
| **API timeout mid-upload.** | `httpx.AsyncClient(timeout=600)` + `with_retry` catches `TimeoutException`. The chunk's segments are not yielded until the API returns 200, so `processed_seconds` (story 3.6) only advances on a successful commit; a timed-out chunk simply re-uploads. |
| **API returns segments without confidence.** | `_convert()` always sets `Segment.confidence = None` (the verbose_json schema does not expose per-segment confidence). Downstream code (story 3.6 commit, story 4 subtitle gen) already treats confidence as optional. |
| **Audio with > 30 s silence.** | `_silence.strip_silences()` removes silences > `silence_strip_threshold_sec` (default 5 s) and records a `SilenceMap`; `_convert()` calls `working_to_original()` to rebase every timestamp. Verified by `test_silence_map_60s_gap.py` and the integration test against the 60 s-silence fixture. |

---

## 7. Acceptance checklist

| # | Item | Verified by |
|---|---|---|
| 1 | `OpenAIWhisperBackend(name="openai-api")` exists; structurally satisfies `STTBackend`. | Conformance suite isinstance check. |
| 2 | `cost_per_minute` populated from `_pricing.COST_PER_MINUTE_USD["whisper-1"] = 0.006`. | `test_pricing_pinned.py`. |
| 3 | `supports_streaming = False`; `requires_file = True`. | Class constants asserted in conformance. |
| 4 | The orchestrator routes `_FilePath` AudioSource (Plan 02-03 file mode) into the backend; passing a `_Pcm16Mono16k` raises `BackendFatal`. | `test_pcm_input_rejected.py`. |
| 5 | Audio is chunked into ≤ 24 MB pieces; chunks tile the working timeline contiguously. | `test_chunking_preserves_timestamps.py`. |
| 6 | Chunk timestamps are re-stitched against the original timeline within ε of the single-call equivalent. | Same test. |
| 7 | Per-library budget cap is enforced **before** claim. Projection = `videos.duration_sec/60 * cost_per_minute`. Sum is over `stt_usage` for the calendar month. Over-cap → `ClaimDecision.defer` with `not_before = first of next month`, `error.kind = "budget_cap"`. | `test_budget_cap.py` + integration `test_claim_refused_when_over_cap.py`. |
| 8 | API returns 429 → exponential backoff (0.5/1/2/4/8 s, ±25% jitter) up to 5 attempts. | `test_retry_on_429.py`. |
| 9 | After 5 retries → `BackendTransient`. | Same test (second case). |
| 10 | Silences > 5 s pre-stripped via `ffmpeg -af silenceremove`; `SilenceMap` records the removed spans; `_convert()` calls `working_to_original()` so segment timestamps remain in original timeline. | `test_silence_map_60s_gap.py` + integration with 60 s silence fixture. |
| 11 | Segments without `avg_logprob` → `Segment.confidence is None`. | `test_no_confidence_field.py`. |
| 12 | `OPENAI_API_KEY` missing → `health().ready=False`. | `test_health.py`. |
| 13 | The API key is never logged, never returned by `/api/settings`, never sent to the Streaming or default-API services. | `test_no_secret_leak.py` (regex-greps logs in CI), code review of `settings.py`. |
| 14 | New migration `0011_stt_usage.sql` adds the ledger table + two indexes. | `pgtap` migration test asserts schema. |
| 15 | Go endpoint `GET /api/system/stt/budget` returns current-month totals per backend; admin-scoped. | `stt_budget_test.go`. |
| 16 | Ledger writes happen **after** segments are emitted for a chunk (i.e. a failed chunk does not bill). | `test_no_billing_on_failure.py` — patched cassette returns 500; ledger remains empty. |
| 17 | `_pricing.COST_PER_MINUTE_USD` is a build-time constant; no live pricing endpoint is called. | `grep` for `pricing` URL in source returns nothing. |
| 18 | Cassettes exist for: 200 normal, 200 no-confidence, 429, 500, 90-min chunked aggregate, single-call aggregate. | `cassettes/` directory listing. |
