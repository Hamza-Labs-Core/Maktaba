# Plan 3.9 — Diarization (opt-in, off by default) — implementation

> Implementation plan for [story-03-09-diarization.md](story-03-09-diarization.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: hooks into the transcribe stage skeleton
> from [Plan 3.6](plan-03-06-segment-commit.md), reads the
> backend-registry library setting from
> [Story 3.5](story-03-05-backend-registry.md), and reuses the FFmpeg
> audio decoder from
> [Plan 2.3](../02-audio-extraction/plan-02-03-stream-extraction.md).
> Speaker matching to a known speaker library is **deferred to v1.1**
> (a future Epic 9 story).

---

## 0. Decisions and departures from `architecture.md` and the story

| # | Decision | Source | Rationale |
|---|----------|--------|-----------|
| D1 | The diarizer runs **before** STT in a single sequential pipeline by default; the "in parallel if memory permits" path is gated by a per-host config flag `transcribe.diarize_parallel = false` (default). | Story acceptance: "runs **before** STT … (or in parallel if memory permits)". | Parallel mode requires loading both pyannote (~1.5 GB GPU) and Whisper-large-v3 (~3 GB GPU) at once. On a 16 GB Mac with other apps open this OOMs. Sequential is safe everywhere; parallel is an opt-in optimization for boxes with ≥24 GB GPU. We ship the safe default and let operators flip the flag on workstations that can take it. |
| D2 | Pyannote is imported **lazily** inside the diarization module — `import pyannote.audio` happens the first time `Diarizer.diarize(...)` is called, never at module import. | Story acceptance: "verify import is lazy" + the test `test_diarize_disabled_skips_pipeline`. | Pyannote pulls in torch, transformers, and a ~500 MB model checkpoint download on first import. Cold-import cost is ~3 s on a fast SSD; we don't pay that for every worker process if no library has diarization on. |
| D3 | The `diarization_lock` semaphore is a **process-global asyncio.Semaphore(1)** module-level singleton in `pipeline/src/maktaba_pipeline/diarize/lock.py`. Workers that run diarization in subprocesses use `multiprocessing.BoundedSemaphore` instead, with the same name and default count. | Story acceptance: "process-global `diarization_lock` semaphore (default 1)". | Asyncio semaphores are cheap and accurate within a single Python process — which matches our worker model where every stage is an async task in the worker process. The MP variant covers the (less common) case where the operator has split workers across child processes for memory isolation. The default count is configurable via `transcribe.diarization_lock_count` (default 1). |
| D4 | Speaker assignment uses the **midpoint** of each STT segment (`(start + end) / 2`) against the diarization intervals, as specified. The split-on-disagreement path (story edge case) only fires when the STT segment **fully contains** at least one diarization boundary AND `library.settings.transcribe.split_on_speaker_change` is true (default `false`). | Story edge case + acceptance "assigns each segment's `speaker` to whichever interval covers its midpoint". | Splitting every disagreeing segment doubles the row count for noisy multi-speaker conversations and produces visible "stutter" in subtitles (the same speaker name in two consecutive rows when the boundary lies between two same-speaker turns by accident). Off by default keeps the v1 transcript clean; the flag exists so heavy diarization users can turn it on. |
| D5 | Diarization runs against the same **audio_decoder stream** the transcribe stage will consume (one FFmpeg invocation, two consumers via `asyncio.tee` over the byte chunks). When sequential mode is used (D1 default), the diarization is fed the stream first, the stream is replayed via the FFmpeg cache, and STT consumes the cached file. | Refines the story (which doesn't specify whether diarization re-runs FFmpeg). | Two FFmpeg passes on a 4-hour file is ~10 minutes wasted per diarization run. One pass + cache is ~5 minutes of disk I/O at the worst-case (8 kHz mono 16-bit at 16 kHz × 4 hours × 2 = ~460 MB). We spend bytes for time. |
| D6 | When diarization fails entirely (story edge case), the failure is recorded as `transcripts.metrics.diarization_failed = {error, at, traceback_short}`; the transcribe stage continues and commits segments with `speaker = NULL`. The job state stays `running`; we do **not** mark it `failed`. | Story edge case: "Diarization fails entirely. Segments are committed without speaker labels; the failure is recorded on the transcript row but does not fail the job." | Diarization is opt-in cosmetic metadata. Failing the whole job because pyannote crashed on one bad audio file would lose hours of transcribe work. The metric makes the failure visible to the user; they can re-run with diarization off if they want, or wait for the upstream pyannote bug to be fixed. |
| D7 | Speaker IDs are assigned at the **library level**, not the video level: `Speaker 1`, `Speaker 2`, ... are local to each video (resetting per video). The story says "local to the video" explicitly; we mirror that and explicitly do **not** attempt cross-video matching at this stage. | Story acceptance: "Speaker IDs are local to the video (`Speaker 1`, `Speaker 2`, …) at this stage." | Cross-video matching needs a voiceprint embedding store (the `speakers` table), and the matching logic is its own bag of cans-of-worms (decision threshold tuning, library re-clustering). The architecture already plans for this in Epic 9; deferring keeps Story 3.9 small and shippable. |

If D5 is rejected (re-run FFmpeg twice rather than tee-cache), §2.6
changes (diarization gets its own decoder context manager) and the
performance budget in §7 worsens by ~50% on diarized jobs;
correctness is unaffected.

---

## 1. Architecture diagram — diarization in the transcribe stage

```
         ┌─────────────────────────────────────────────────────────────┐
         │  Transcribe stage entry                                     │
         │   ctx.run_transcribe_stage(ctx, claimed_job)                │
         │                                                             │
         │   diarize_enabled = library.settings                        │
         │     .transcribe.diarize == true   (default false)           │
         └───────────────────────────┬─────────────────────────────────┘
                                     │
                       ┌─────────────┴─────────────┐
                       │                           │
                  diarize_enabled = false   diarize_enabled = true
                       │                           │
                       ▼                           ▼
         ┌─────────────────────┐    ┌────────────────────────────────────┐
         │  STT stream → seg   │    │  acquire diarization_lock (D3)     │
         │  speaker = NULL     │    │     ↓                              │
         │  (existing path)    │    │  open audio_decoder once (D5)      │
         └─────────────────────┘    │     ↓                              │
                                    │  Diarizer.diarize(stream)          │
                                    │   (lazy pyannote import - D2)      │
                                    │     returns DiarizationResult:     │
                                    │       intervals: [(s, e, spkr_id)] │
                                    │       speaker_count: int           │
                                    │     wall_sec: ~5-15% of audio dur. │
                                    │     ↓                              │
                                    │  release diarization_lock          │
                                    │     ↓                              │
                                    │  open STT session (existing path), │
                                    │  feed cached PCM file              │
                                    │     ↓                              │
                                    │  for each STT segment:             │
                                    │    speaker_id = SpeakerAssigner    │
                                    │      .assign(seg.midpoint,         │
                                    │              intervals)            │
                                    │    if split_on_speaker_change      │
                                    │       and contains_boundary(seg):  │
                                    │       split into seg.a / seg.b     │
                                    │       commit each with its speaker │
                                    │    else:                           │
                                    │       commit segment with speaker  │
                                    │     ↓                              │
                                    │  on Diarizer error (D6):           │
                                    │    log + record metrics            │
                                    │    fall through to no-speaker path │
                                    └────────────────────────────────────┘
                                                     │
                                                     ▼
                            ┌─────────────────────────────────────────┐
                            │  SegmentCommitter (Plan 3.6) — speaker  │
                            │  goes into transcript_segments.speaker  │
                            │  in the same atomic xact as everything  │
                            │  else.                                  │
                            └─────────────────────────────────────────┘
```

Diarization is **a layer on top of the existing pipeline**, not a
parallel pipeline. The STT loop is unchanged; only the segment object
gains a `speaker` field that is `None` (default) or a speaker label
(when diarization is enabled and successful).

---

## 2. Detailed implementation

### 2.1 Package layout — Python (Pipeline Service)

```
pipeline/src/maktaba_pipeline/
├── diarize/
│   ├── __init__.py             # public surface: Diarizer, DiarizationResult
│   ├── lock.py                 # diarization_lock semaphore singleton (D3)
│   ├── pyannote_backend.py     # lazy pyannote import (D2), .diarize()
│   ├── assigner.py             # SpeakerAssigner: midpoint → speaker_id
│   ├── splitter.py             # split_segment_at_boundary helper
│   ├── errors.py               # DiarizationError, DiarizationDisabled
│   └── tests/
│       ├── conftest.py         # fixtures: 2-speaker fake intervals
│       ├── test_lock.py
│       ├── test_assigner.py
│       ├── test_splitter.py
│       ├── test_lazy_import.py
│       ├── test_diarize_off_by_default.py
│       ├── test_diarize_assigns_speakers.py
│       ├── test_diarize_disabled_skips_pipeline.py
│       └── test_diarize_failure_does_not_fail_job.py
└── pipeline/
    └── stages/
        └── transcribe.py       # extended: optional pre-pass diarization
```

### 2.2 `lock.py` — the global semaphore (D3)

```python
"""Process-global semaphore that gates pyannote runs.

Pyannote is GPU-greedy; one pass at a time per process by default.
"""
from __future__ import annotations
import asyncio
import os

DEFAULT_LOCK_COUNT = int(os.getenv("MAKTABA_DIARIZATION_LOCK_COUNT", "1"))

_lock: asyncio.Semaphore | None = None


def get_lock() -> asyncio.Semaphore:
    """Return the singleton; created lazily on first use to bind to the running loop."""
    global _lock
    if _lock is None:
        _lock = asyncio.Semaphore(DEFAULT_LOCK_COUNT)
    return _lock


def reset_for_tests() -> None:  # pragma: no cover (test-only)
    global _lock
    _lock = None
```

### 2.3 `pyannote_backend.py` — lazy import (D2)

```python
"""Pyannote diarization wrapper. Imports pyannote ONLY when .diarize() is called."""
from __future__ import annotations
import logging
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Iterator

from maktaba_pipeline.diarize.errors import DiarizationError
from maktaba_pipeline.diarize.lock import get_lock

log = logging.getLogger(__name__)


@dataclass(frozen=True)
class SpeakerInterval:
    start: float
    end: float
    speaker_id: str           # "Speaker 1", "Speaker 2", … local to the video


@dataclass(frozen=True)
class DiarizationResult:
    intervals: tuple[SpeakerInterval, ...]
    speaker_count: int
    wall_sec: float
    pyannote_model: str       # for transcript metrics


class Diarizer:
    """Pyannote-backed diarization. Lazy on first .diarize() call.

    Construct with the pretrained pipeline name (default
    'pyannote/speaker-diarization-3.1'); credentials come from
    HUGGINGFACE_TOKEN env var (the model is gated).
    """

    def __init__(
        self,
        *,
        model_name: str = "pyannote/speaker-diarization-3.1",
        device: str = "auto",
    ):
        self._model_name = model_name
        self._device = device
        self._pipeline = None  # lazy

    async def diarize(self, audio_wav_path: Path) -> DiarizationResult:
        async with get_lock():
            # IMPORTANT: import inside the function (D2). Test
            # `test_diarize_disabled_skips_pipeline` asserts pyannote is not
            # imported when diarize is off.
            from pyannote.audio import Pipeline as _PyannotePipeline  # noqa
            import torch  # noqa

            if self._pipeline is None:
                try:
                    self._pipeline = _PyannotePipeline.from_pretrained(
                        self._model_name)
                except Exception as e:
                    raise DiarizationError(
                        f"failed to load pyannote pipeline {self._model_name}: {e}"
                    ) from e
                if self._device != "auto":
                    self._pipeline.to(torch.device(self._device))

            t0 = time.monotonic()
            try:
                annotation = self._pipeline(str(audio_wav_path))
            except Exception as e:
                raise DiarizationError(
                    f"pyannote crashed on {audio_wav_path}: {e}"
                ) from e
            wall = time.monotonic() - t0

            return _annotation_to_result(
                annotation, wall_sec=wall, model=self._model_name)


def _annotation_to_result(annotation, *, wall_sec: float, model: str) -> DiarizationResult:
    """Pyannote returns an Annotation with .itertracks(yield_label=True).

    Speakers come back as opaque labels ('SPEAKER_00', 'SPEAKER_01', …);
    we relabel them to 'Speaker 1', 'Speaker 2', … in first-appearance order.
    """
    raw_intervals: list[tuple[float, float, str]] = []
    for turn, _track, label in annotation.itertracks(yield_label=True):
        raw_intervals.append((turn.start, turn.end, label))
    raw_intervals.sort(key=lambda x: x[0])

    relabel: dict[str, str] = {}
    intervals: list[SpeakerInterval] = []
    for start, end, raw_label in raw_intervals:
        if raw_label not in relabel:
            relabel[raw_label] = f"Speaker {len(relabel) + 1}"
        intervals.append(SpeakerInterval(
            start=start, end=end, speaker_id=relabel[raw_label]))

    return DiarizationResult(
        intervals=tuple(intervals),
        speaker_count=len(relabel),
        wall_sec=wall_sec,
        pyannote_model=model,
    )
```

### 2.4 `assigner.py` — midpoint → speaker_id (D4)

```python
"""SpeakerAssigner — bisect-search a sorted interval list."""
from __future__ import annotations
import bisect
from dataclasses import dataclass
from typing import Sequence

from maktaba_pipeline.diarize.pyannote_backend import SpeakerInterval


class SpeakerAssigner:
    """Assigns speaker_id to a (start, end) STT segment via its midpoint."""

    def __init__(self, intervals: Sequence[SpeakerInterval]):
        # Sort by start; build a parallel list of starts for bisect.
        self._intervals = sorted(intervals, key=lambda i: i.start)
        self._starts = [i.start for i in self._intervals]

    def assign(self, segment_start: float, segment_end: float) -> str | None:
        midpoint = (segment_start + segment_end) / 2.0
        idx = bisect.bisect_right(self._starts, midpoint) - 1
        if idx < 0:
            # midpoint precedes all intervals → no speaker (silence?)
            return None
        candidate = self._intervals[idx]
        if candidate.start <= midpoint <= candidate.end:
            return candidate.speaker_id
        # Midpoint falls in a gap between intervals.
        return None

    def boundaries_within(
        self, segment_start: float, segment_end: float,
    ) -> list[float]:
        """Return diarization boundaries strictly inside the segment.

        Used for split-on-speaker-change (D4). A 'boundary' is the end
        of one interval where the next interval has a different speaker.
        """
        out: list[float] = []
        for i in range(len(self._intervals) - 1):
            cur = self._intervals[i]
            nxt = self._intervals[i + 1]
            if cur.speaker_id == nxt.speaker_id:
                continue
            boundary = cur.end
            if segment_start < boundary < segment_end:
                out.append(boundary)
        return out
```

### 2.5 `splitter.py` — split_segment_at_boundary

```python
"""Split a STT segment into .a / .b sub-segments at a diarization boundary.

Used only when library.settings.transcribe.split_on_speaker_change is true.
"""
from __future__ import annotations
from dataclasses import replace
from typing import Iterator


def split_segment_at_boundary(segment, boundary_sec: float, assigner):
    """Yield two segments (start..boundary), (boundary..end) with metadata.split_from."""
    if not (segment.start < boundary_sec < segment.end):
        yield segment
        return
    a_text, b_text = _split_text_proportionally(
        segment.text, segment.start, segment.end, boundary_sec, segment.words)
    base_meta = dict(getattr(segment, "metadata", {}) or {})
    base_meta["split_from"] = {
        "original_start": segment.start, "original_end": segment.end,
        "boundary": boundary_sec,
    }
    a_meta = {**base_meta, "split_suffix": "a"}
    b_meta = {**base_meta, "split_suffix": "b"}
    a = replace(segment,
                end=boundary_sec, text=a_text,
                speaker=assigner.assign(segment.start, boundary_sec),
                metadata=a_meta)
    b = replace(segment,
                start=boundary_sec, text=b_text,
                speaker=assigner.assign(boundary_sec, segment.end),
                metadata=b_meta)
    yield a
    yield b


def _split_text_proportionally(text, start, end, boundary, words):
    """If word-level timings exist, split by word boundary; else by char ratio."""
    if words:
        a_words = [w for w in words if w.end <= boundary]
        b_words = [w for w in words if w.start >= boundary]
        return (
            " ".join(w.text for w in a_words),
            " ".join(w.text for w in b_words),
        )
    ratio = (boundary - start) / max(end - start, 1e-6)
    cut = int(round(len(text) * ratio))
    return text[:cut].strip(), text[cut:].strip()
```

### 2.6 Stage integration — diarize-then-transcribe

```python
# pipeline/src/maktaba_pipeline/pipeline/stages/transcribe.py  (excerpt)

from maktaba_pipeline.diarize.pyannote_backend import Diarizer, DiarizationResult
from maktaba_pipeline.diarize.assigner import SpeakerAssigner
from maktaba_pipeline.diarize.splitter import split_segment_at_boundary
from maktaba_pipeline.diarize.errors import DiarizationError


async def run_transcribe_stage(ctx, claimed_job):
    settings = claimed_job.library_settings.get("transcribe", {})
    diarize_enabled = bool(settings.get("diarize", False))
    split_on_speaker_change = bool(settings.get("split_on_speaker_change", False))

    # Resume context, prompt, decoder seek (Plan 3.7) — unchanged.
    ctx_resume = await resume_context.build(...)

    diarization: DiarizationResult | None = None
    diarization_metric: dict | None = None
    if diarize_enabled and not is_resume:  # diarization runs once per video
        diarization, diarization_metric = await _run_diarization(
            ctx, claimed_job, audio_path=ctx_resume.audio_wav_path,
        )

    # Persist whatever we know about diarization on the transcript row.
    await _record_diarization_metrics(
        ctx, ctx_resume.transcript_id_for_new_segments, diarization, diarization_metric)

    assigner = SpeakerAssigner(diarization.intervals) if diarization else None

    backend = await stt_registry.open_session(...)
    com = SegmentCommitter(...)
    rb = ReorderBuffer(...)

    async with audio_decoder(claimed_job.video, start_sec=ctx_resume.seek_from_sec) as audio:
        async for raw_seg in backend.transcribe_stream(audio):
            wall_sec = ...
            try:
                ready = rb.push(raw_seg)
            except OutOfOrderSegmentDropped:
                continue
            for seg in ready:
                # Apply speaker label (and optionally split).
                for tagged in _apply_speaker_label(
                    seg, assigner, split_on_speaker_change=split_on_speaker_change,
                ):
                    await com.commit(tagged, wall_sec=wall_sec)
        for seg in rb.flush():
            for tagged in _apply_speaker_label(
                seg, assigner, split_on_speaker_change=split_on_speaker_change,
            ):
                await com.commit(tagged, wall_sec=0.0)

    await mark_done(ctx.db_pool, job_id=claimed_job.id)


async def _run_diarization(ctx, claimed_job, *, audio_path):
    """Returns (DiarizationResult|None, metric_dict).

    On failure: logs, records the failure in metric_dict, returns (None, metric).
    The transcribe stage continues without speaker labels (D6).
    """
    diarizer = Diarizer(
        model_name=ctx.cfg.diarize.model_name,
        device=ctx.cfg.diarize.device,
    )
    try:
        result = await diarizer.diarize(audio_path)
        return result, {
            "diarization_succeeded": True,
            "speaker_count": result.speaker_count,
            "diarization_wall_sec": result.wall_sec,
            "pyannote_model": result.pyannote_model,
        }
    except DiarizationError as e:
        log.warning("diarization_failed",
                    extra={"job_id": claimed_job.id, "err": str(e)})
        return None, {
            "diarization_failed": True,
            "diarization_error": str(e)[:512],
            "diarization_failed_at": "now",
        }


def _apply_speaker_label(segment, assigner, *, split_on_speaker_change):
    if assigner is None:
        yield segment
        return
    if split_on_speaker_change:
        boundaries = assigner.boundaries_within(segment.start, segment.end)
        if boundaries:
            # Use the first internal boundary; multi-boundary recursion is
            # rare in practice and a future enhancement (logged as TODO).
            yield from split_segment_at_boundary(segment, boundaries[0], assigner)
            return
    speaker = assigner.assign(segment.start, segment.end)
    yield replace(segment, speaker=speaker)


async def _record_diarization_metrics(ctx, transcript_id, result, metric):
    if result is None and metric is None:
        return
    payload = metric or {}
    async with ctx.db_pool.acquire() as conn:
        await conn.execute("""
            UPDATE transcripts
               SET diarized = $2,
                   metrics = COALESCE(metrics, '{}'::jsonb) ||
                             $3::jsonb
             WHERE id = $1
        """, transcript_id, result is not None, json.dumps(payload))
```

### 2.7 Library settings — config surface

The diarization toggle is a per-library setting written through the
existing library settings API (Epic 9 Story 9.x). Concretely the JSON
payload:

```json
{
  "transcribe": {
    "backend": "whisper-mlx",
    "model": "large-v3",
    "diarize": false,
    "split_on_speaker_change": false,
    "diarize_parallel": false,
    "resume_prompt_segments": 3
  }
}
```

Defaults are baked into `pipeline/src/maktaba_pipeline/config/defaults.py`
so a missing key resolves to `false`/`3` without DB lookups.

### 2.8 Schema — additive

No new tables. The existing schema already accommodates everything:

- `transcript_segments.speaker TEXT NULL` — the assigned label, or NULL.
- `transcripts.diarized BOOLEAN NOT NULL` — set to `true` on success.
- `transcripts.metrics JSONB` — gets the diarization metric payload.

A small migration `0014_transcript_segments_speaker_index.sql` adds:

```sql
BEGIN;
CREATE INDEX IF NOT EXISTS transcript_segments_speaker_idx
    ON transcript_segments (transcript_id, speaker)
    WHERE speaker IS NOT NULL;
COMMIT;
```

…to support the future "show me only Speaker 2's lines" view from
Epic 11. Index is filtered partial so non-diarized transcripts pay zero
storage.

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `pipeline/src/maktaba_pipeline/diarize/__init__.py` | re-exports | (n/a) |
| 2 | `pipeline/src/maktaba_pipeline/diarize/errors.py` | `DiarizationError`, `DiarizationDisabled` | (n/a) |
| 3 | `pipeline/src/maktaba_pipeline/diarize/lock.py` | `get_lock`, `reset_for_tests`, `DEFAULT_LOCK_COUNT` | `test_lock` |
| 4 | `pipeline/src/maktaba_pipeline/diarize/pyannote_backend.py` | `Diarizer`, `DiarizationResult`, `SpeakerInterval`, `_annotation_to_result` | `test_lazy_import` (no tests calling .diarize need pyannote installed if lazily imported), `test_pyannote_backend_relabels` |
| 5 | `pipeline/src/maktaba_pipeline/diarize/assigner.py` | `SpeakerAssigner.assign`, `.boundaries_within` | `test_assigner` |
| 6 | `pipeline/src/maktaba_pipeline/diarize/splitter.py` | `split_segment_at_boundary` | `test_splitter` |
| 7 | `shared/db/migrations/0014_transcript_segments_speaker_index.sql` | partial index | migration applies cleanly |
| 8 | `pipeline/src/maktaba_pipeline/pipeline/stages/transcribe.py` | wire `_run_diarization`, `_apply_speaker_label`, `_record_diarization_metrics` | `test_diarize_off_by_default`, `test_diarize_assigns_speakers`, `test_diarize_disabled_skips_pipeline`, `test_diarize_failure_does_not_fail_job` |

---

## 4. Test cases

### 4.1 `test_diarize_off_by_default` (story-named)

```python
async def test_diarize_off_by_default(db, library, job_factory):
    """Library without 'diarize' setting → segments have speaker=None."""
    await library.set_setting("transcribe", {
        "backend": "whisper-mlx", "model": "large-v3",
        # No 'diarize' key → defaults to False.
    })
    job = await job_factory.fresh(video_duration_sec=60)
    await run_transcribe_stage(job.id)
    rows = await db.fetch(
        "SELECT speaker FROM transcript_segments "
        "WHERE transcript_id IN (SELECT id FROM transcripts WHERE video_id=$1)",
        job.video_id)
    assert all(r["speaker"] is None for r in rows)
```

### 4.2 `test_diarize_assigns_speakers` (story-named)

```python
async def test_diarize_assigns_speakers_alternating(
    db, library, job_factory, two_speaker_fixture, fake_diarizer,
):
    """Two-speaker fixture → segments alternate Speaker 1 / Speaker 2."""
    # Fake intervals: 0-30s = Speaker 1, 30-60s = Speaker 2, alternating each 30s.
    fake_diarizer.set_intervals([
        SpeakerInterval(0, 30, "Speaker 1"),
        SpeakerInterval(30, 60, "Speaker 2"),
        SpeakerInterval(60, 90, "Speaker 1"),
        SpeakerInterval(90, 120, "Speaker 2"),
    ])
    await library.set_setting("transcribe", {"backend": "synth", "diarize": True})

    job = await job_factory.fresh(video=two_speaker_fixture)
    await run_transcribe_stage(job.id)

    rows = await db.fetch("""
        SELECT seq, start_sec, end_sec, speaker
          FROM transcript_segments
         WHERE transcript_id IN (SELECT id FROM transcripts WHERE video_id=$1)
         ORDER BY seq""", job.video_id)
    # The synth backend emits 10s segments. The expected pattern is
    # 1,1,1,2,2,2,1,1,1,2,2,2.
    speakers = [r["speaker"] for r in rows]
    assert speakers == [
        "Speaker 1", "Speaker 1", "Speaker 1",
        "Speaker 2", "Speaker 2", "Speaker 2",
        "Speaker 1", "Speaker 1", "Speaker 1",
        "Speaker 2", "Speaker 2", "Speaker 2",
    ]
```

### 4.3 `test_diarize_disabled_skips_pipeline` (story-named)

```python
async def test_diarize_disabled_does_not_import_pyannote(
    db, library, job_factory, monkeypatch,
):
    """diarize=false → pyannote.audio is never imported."""
    import sys
    # Wipe any prior import so we can detect a fresh one.
    for k in [k for k in sys.modules if k.startswith("pyannote")]:
        del sys.modules[k]

    await library.set_setting("transcribe", {
        "backend": "synth", "model": "fake", "diarize": False})
    job = await job_factory.fresh(video_duration_sec=10)
    await run_transcribe_stage(job.id)

    assert "pyannote" not in sys.modules
    assert "pyannote.audio" not in sys.modules
```

### 4.4 `test_diarize_failure_does_not_fail_job` (edge case)

```python
async def test_diarize_failure_falls_through_with_metrics(
    db, library, job_factory, fake_diarizer,
):
    """Pyannote crashes → segments commit speaker=None; transcript metrics record failure."""
    fake_diarizer.set_failure(DiarizationError("simulated GPU OOM"))
    await library.set_setting("transcribe", {
        "backend": "synth", "model": "fake", "diarize": True})

    job = await job_factory.fresh(video_duration_sec=30)
    await run_transcribe_stage(job.id)

    # Job is done; segments have NULL speaker.
    j = await db.fetchrow("SELECT state FROM processing_jobs WHERE id=$1", job.id)
    assert j["state"] == "done"
    rows = await db.fetch(
        "SELECT speaker FROM transcript_segments "
        "WHERE transcript_id IN (SELECT id FROM transcripts WHERE video_id=$1)",
        job.video_id)
    assert all(r["speaker"] is None for r in rows)

    t = await db.fetchrow(
        "SELECT diarized, metrics FROM transcripts WHERE video_id=$1", job.video_id)
    assert t["diarized"] is False
    md = t["metrics"]
    assert md["diarization_failed"] is True
    assert "simulated GPU OOM" in md["diarization_error"]
```

### 4.5 `test_assigner` (unit)

```python
def test_assigner_midpoint_falls_in_interval():
    a = SpeakerAssigner([
        SpeakerInterval(0, 10, "Speaker 1"),
        SpeakerInterval(10, 20, "Speaker 2"),
    ])
    assert a.assign(0, 6) == "Speaker 1"          # midpoint 3
    assert a.assign(7, 11) == "Speaker 1"         # midpoint 9
    assert a.assign(8, 12) == "Speaker 2"         # midpoint 10 (boundary inclusive)
    assert a.assign(15, 19) == "Speaker 2"        # midpoint 17


def test_assigner_returns_none_in_gap():
    a = SpeakerAssigner([
        SpeakerInterval(0, 5, "Speaker 1"),
        SpeakerInterval(10, 15, "Speaker 2"),
    ])
    # Midpoint 7.5 falls in the gap [5..10] → None.
    assert a.assign(5, 10) is None


def test_boundaries_within_returns_only_speaker_changes():
    a = SpeakerAssigner([
        SpeakerInterval(0, 10, "Speaker 1"),
        SpeakerInterval(10, 20, "Speaker 1"),  # same speaker — no boundary
        SpeakerInterval(20, 30, "Speaker 2"),  # boundary at 20
        SpeakerInterval(30, 40, "Speaker 1"),  # boundary at 30
    ])
    bs = a.boundaries_within(5, 35)
    assert bs == [20, 30]
```

### 4.6 `test_splitter` (unit)

```python
def test_split_at_boundary_word_aware():
    seg = Segment(
        start=0.0, end=10.0,
        text="hello world how are you",
        words=[
            Word(seq=1, start=0.0, end=2.0, text="hello"),
            Word(seq=2, start=2.0, end=4.0, text="world"),
            Word(seq=3, start=4.5, end=6.0, text="how"),
            Word(seq=4, start=6.0, end=8.0, text="are"),
            Word(seq=5, start=8.0, end=10.0, text="you"),
        ],
        speaker=None, confidence=0.9, metadata={})
    assigner = SpeakerAssigner([
        SpeakerInterval(0, 4.5, "Speaker 1"),
        SpeakerInterval(4.5, 10, "Speaker 2"),
    ])
    parts = list(split_segment_at_boundary(seg, 4.5, assigner))
    assert len(parts) == 2
    assert parts[0].text == "hello world" and parts[0].speaker == "Speaker 1"
    assert parts[1].text == "how are you" and parts[1].speaker == "Speaker 2"
    assert parts[0].metadata["split_from"]["boundary"] == 4.5
    assert parts[0].metadata["split_suffix"] == "a"
    assert parts[1].metadata["split_suffix"] == "b"


def test_split_falls_back_to_char_ratio_without_words():
    seg = Segment(start=0.0, end=10.0, text="abcdefghij",
                  words=[], speaker=None, confidence=0.9, metadata={})
    assigner = SpeakerAssigner([SpeakerInterval(0, 10, "Speaker 1")])
    parts = list(split_segment_at_boundary(seg, 5.0, assigner))
    assert parts[0].text == "abcde"
    assert parts[1].text == "fghij"
```

### 4.7 `test_lock` (unit)

```python
async def test_diarization_lock_serializes(monkeypatch):
    monkeypatch.setattr(
        "maktaba_pipeline.diarize.lock.DEFAULT_LOCK_COUNT", 1)
    lock_mod.reset_for_tests()

    sem = lock_mod.get_lock()
    in_critical = []

    async def worker(name):
        async with sem:
            in_critical.append(name)
            await asyncio.sleep(0.02)
            in_critical.remove(name)
            assert in_critical == []  # never two in critical section

    await asyncio.gather(*(worker(str(i)) for i in range(5)))


def test_get_lock_is_singleton():
    lock_mod.reset_for_tests()
    a = lock_mod.get_lock()
    b = lock_mod.get_lock()
    assert a is b
```

### 4.8 `test_lazy_import` (unit)

```python
def test_module_import_does_not_import_pyannote():
    import sys
    for k in [k for k in sys.modules if k.startswith("pyannote")]:
        del sys.modules[k]
    # Importing the wrapper must NOT pull in pyannote.
    import maktaba_pipeline.diarize.pyannote_backend  # noqa
    assert "pyannote" not in sys.modules
    assert "pyannote.audio" not in sys.modules

    # Constructing a Diarizer must also NOT import pyannote.
    d = maktaba_pipeline.diarize.pyannote_backend.Diarizer()
    assert "pyannote" not in sys.modules
```

### 4.9 `test_pyannote_backend_relabels` (unit, requires pyannote)

```python
@pytest.mark.requires_pyannote
def test_annotation_to_result_relabels_in_first_appearance_order():
    # Build a fake annotation-like object.
    fake = FakeAnnotation([
        FakeTurn(0.0, 5.0, "SPEAKER_03"),      # arrives first → Speaker 1
        FakeTurn(5.0, 10.0, "SPEAKER_07"),     # second  → Speaker 2
        FakeTurn(10.0, 15.0, "SPEAKER_03"),    # back to Speaker 1
    ])
    res = _annotation_to_result(fake, wall_sec=1.0, model="x")
    assert res.speaker_count == 2
    assert [(i.start, i.speaker_id) for i in res.intervals] == [
        (0.0, "Speaker 1"), (5.0, "Speaker 2"), (10.0, "Speaker 1")]
```

---

## 5. Edge cases and how the plan handles each

| # | Edge case (story §"Edge cases") | Handled by |
|---|---------------------------------|------------|
| E1 | Diarization disagrees mid-segment — a single STT segment spans two speakers. | Default behavior (`split_on_speaker_change = false`): the segment's midpoint wins; the segment gets one speaker, and the other half of the segment is "wrong" by ≤ half the segment length (typically ≤ 5 s). With the flag enabled, `splitter.split_segment_at_boundary` produces two rows with `metadata.split_from` describing the original segment and `.a` / `.b` suffixes. Word-aware splitting is used when word timestamps are available; otherwise char-ratio split. (`test_splitter`) (D4) |
| E2 | Diarization fails entirely — pyannote crashes, OOM, or the model can't load. | `_run_diarization` catches `DiarizationError`, logs `diarization_failed`, records `transcripts.metrics.diarization_failed = {error, …}`, and returns `(None, metric)`. The transcribe stage continues with `assigner = None`; segments commit with `speaker = NULL`. Job state stays `running` and ends `done`. (`test_diarize_failure_does_not_fail_job`) (D6) |
| E3 | Pyannote model checkpoint download fails on first run (HF Hub down or token missing). | `Diarizer._pipeline = Pipeline.from_pretrained(...)` raises; we wrap in `DiarizationError("failed to load pyannote pipeline …")`. Same fall-through as E2 — job completes without speakers. The user sees the error in the transcript metrics and can retry the pipeline once their HF token is set. |
| E4 | The diarization lock is contended by N>1 simultaneous transcribes. | `get_lock()` returns the singleton `asyncio.Semaphore(DEFAULT_LOCK_COUNT)`. N-1 transcribes wait at `async with get_lock()` while one runs pyannote. The transcribe stage's heartbeat continues firing during the wait (the wait is in the diarize step *before* the transcribe loop opens), and the job state stays `running`. The wait shows up in metrics as `diarization_lock_wait_sec`. (`test_lock`) |
| E5 | Diarization is enabled but the fixture has no detectable speech (silence). | Pyannote returns an empty annotation → `intervals = ()`, `speaker_count = 0`. `SpeakerAssigner.assign` always returns `None` for empty interval lists, so segments commit with `speaker = NULL`. The metric records `speaker_count = 0`. |
| E6 | Resume of a diarized job after pause/crash. | Diarization runs **once per video, not per resume** — gated by `if diarize_enabled and not is_resume`. The intervals from the original diarization pass live in `transcripts.metrics.diarization_intervals` (added to the metric payload — see the schema TODO in §7) and are reloaded on resume. The first version of this code re-runs diarization on every resume; that's a known correctness regression and the resume-aware load path lands as part of A8 below before the story is closed. |
| E7 | Library setting flips diarize from off → on after some segments have already been committed (rare; user-driven). | The new setting takes effect on the **next** transcribe stage entry — usually the next resume, since live editing of library settings during a transcribe is locked by Epic 9. The previously-committed segments keep `speaker = NULL`; new segments get speaker labels. The seam is visible in the UI (rows with NULL speaker followed by rows with a speaker), which we document as expected. |
| E8 | Two segments share the same speaker but a microphone glitch makes pyannote think they're different. | This is a pyannote accuracy issue, not a code issue. We surface `speaker_count` in metrics so users can see "21 speakers detected on a 2-person podcast" and decide whether to disable diarization or accept the noise. No code path in this story attempts to merge spurious speakers — that's the v1.1 cross-video matching work. |
| E9 | Speaker labels are not stable across re-runs (pyannote is stochastic). | First-appearance-order relabeling (`_annotation_to_result`) makes labels stable as long as the *order* of speech is the same. A re-run that produces the same intervals in the same order produces the same `Speaker 1` / `Speaker 2` mapping. (`test_pyannote_backend_relabels`) |
| E10 | `diarize_parallel = true` on a host that can't actually fit both models. | Sequential mode is the default (D1); parallel mode is opt-in. When opt-in fires an OOM, the diarize task raises `DiarizationError` (E2 path). The user sees the failure in metrics and flips the flag back. We do not auto-fall-back from parallel → sequential because that would mask the misconfiguration. |

---

## 6. Acceptance checklist

- [ ] **A1** Library setting `diarize = true` enables the pyannote pipeline; the default (no setting or `false`) leaves diarization off. (`test_diarize_off_by_default`)
- [ ] **A2** When enabled, the diarizer runs **before** STT on the same audio stream (sequential, D1 default); produces a list of `(start, end, speaker_id)` intervals; the transcribe stage assigns each segment's `speaker` to the interval covering its midpoint. (`test_diarize_assigns_speakers`)
- [ ] **A3** Speaker IDs are local to the video (`Speaker 1`, `Speaker 2`, …) at this stage; no attempt is made to match against the `speakers` table. (`test_pyannote_backend_relabels`)
- [ ] **A4** Diarization is gated by a process-global semaphore `diarization_lock` (default count 1). Multiple concurrent transcribes serialize through it. (`test_lock`)
- [ ] **A5** `pyannote.audio` is imported lazily — it is **not** in `sys.modules` after a transcribe with `diarize = false`. (`test_diarize_disabled_skips_pipeline`, `test_lazy_import`)
- [ ] **A6** When a single STT segment spans two speakers AND `split_on_speaker_change = true`, the segment is split into `.a` / `.b` rows with `metadata.split_from = {…}`. Word-level text re-assignment uses word timestamps when present. (`test_splitter`)
- [ ] **A7** Diarization failure (any exception from pyannote) is caught: segments commit with `speaker = NULL`, `transcripts.diarized` stays `false`, and `transcripts.metrics.diarization_failed = {error, …}` is recorded. The job does **not** fail. (`test_diarize_failure_does_not_fail_job`)
- [ ] **A8** Resume of a diarized job does NOT re-run diarization — intervals are persisted on the transcript and reloaded on resume. (Test landing alongside E6 fix: `test_diarize_intervals_persisted_for_resume`.)
- [ ] **A9** Pyannote's model checkpoint download failure is handled as a `DiarizationError` and follows the A7 fall-through path. (Add `test_pyannote_load_failure_recorded_on_transcript`.)
- [ ] **A10** Migration `0014_transcript_segments_speaker_index.sql` adds the partial speaker index and is idempotent on re-run.
- [ ] **A11** Library setting JSON shape (`transcribe.diarize`, `.split_on_speaker_change`, `.diarize_parallel`) defaults are baked into `config/defaults.py`; missing keys resolve without a DB lookup.
- [ ] **A12** No code path in this story writes to the `speakers` or `segment_speakers` tables — those are reserved for the v1.1 cross-video matching work. (Static check; lint rule on `INSERT INTO speakers` outside Epic 9.)
