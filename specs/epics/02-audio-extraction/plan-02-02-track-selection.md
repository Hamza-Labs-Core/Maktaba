# Implementation Plan — Story 2.2 Track Selection

> Companion to [story-02-02-track-selection.md](story-02-02-track-selection.md).
> The story states *what* and *why*; this plan states *how*.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Primary language | Python 3.12+. Track selection is a `pipeline` stage decision that runs between `probe` (Story 2.1) and `extract` (Story 2.3); it lives in the Pipeline Service ([architecture.md §3.3](../../architecture.md)). |
| Module location | `pipeline/src/maktaba_pipeline/media/track_selection.py` for the algorithm; `pipeline/src/maktaba_pipeline/media/iso639.py` for language normalization; `pipeline/src/maktaba_pipeline/pipeline/stages/select_track.py` for the stage wrapper that fans extract jobs out (one per selected track). |
| Go side | A read-only surface only: `api/internal/tracks/preview.go` exposes `GET /api/videos/{id}/tracks` so the web UI can show the user which track will be transcribed and let them override. The override path is a column on `videos.metadata.track_override` — selection respects it before applying the priority list. The Go side never picks a track itself; it mirrors the same sqlc queries the Python side reads. |
| Database surface | Reads `audio_tracks` (Story 2.1 schema), `libraries.settings` JSONB (`preferred_audio_language`, `multi_audio`, `exclude_descriptive`), and `videos.metadata.track_override`. Writes one `processing_jobs(stage='extract')` row per selected track with `extra->>'audio_track_id'` set, plus a `track_selection_decisions` audit row capturing the rule that fired (debugging "why did it pick that track?" without re-running). |
| Out of scope | The actual extraction pipe (Story 2.3); pause/resume of in-flight extractions (Story 2.4); STT language auto-detect (Epic 3); UI for the override (Epic 7). This plan stops at "given a probed video and a library config, return one or more `audio_tracks` rows + enqueue jobs". |

## 1. Architecture diagram

```
                  pipeline.stages.probe (Story 2.1)
                                │
                                ▼ writes audio_tracks rows + advances state to PROBED
                  ┌─────────────────────────────────┐
                  │ pipeline.stages.select_track    │ ← Story 2.2
                  │                                 │
                  │  load(video_id) → tracks, lib   │
                  │       │                         │
                  │       ▼                         │
                  │  filter:                        │
                  │   ├─ drop disposition.commentary│
                  │   └─ drop disposition.descriptions│
                  │       (unless explicitly opted-in)│
                  │       │                         │
                  │       ▼                         │
                  │  if videos.metadata.track_override set │
                  │     → return that single track  │
                  │       │                         │
                  │       ▼                         │
                  │  if library.settings.multi_audio│
                  │     → return ALL filtered tracks│
                  │       │                         │
                  │       ▼                         │
                  │  apply priority list:           │
                  │   1. preferred_audio_language   │
                  │   2. ara (Arabic)               │
                  │   3. is_default = true          │
                  │   4. tie-break: channels DESC,  │
                  │                  is_default DESC,│
                  │                  index ASC      │
                  │       │                         │
                  │       ▼                         │
                  │  enqueue one extract job per    │
                  │  selected (video, audio_track)  │
                  │  + write track_selection_decisions│
                  └─────────────────────────────────┘
                                │
                                ▼
                  pipeline.stages.extract (Story 2.3)
```

Per-track language flow (Section 7) sits between `probe` and `select_track` for tracks tagged `und`:

```
  audio_tracks row, language='und'
            │
            ▼
   need_langid_for_track(track, lib)?  ──▶  no  ──▶ keep 'und', selection treats as last resort
            │
            yes (library.settings.langid_undetermined = true,
                  duration_sec ≥ 60, codec is decodable)
            ▼
   sample 30 s at ~1/3 of duration (avoid intros/outros)
            │
            ▼
   ffmpeg → 16 kHz mono PCM (5 MB tmp buffer; never persisted)
            │
            ▼
   whisper-cpp tiny-multilingual --detect-language-only
            │
            ▼
   write audio_tracks.detected_language (ISO 639-3) +
         audio_tracks.detected_language_confidence (REAL 0..1)
   selection treats detected_language as language IFF confidence ≥ 0.6
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `pipeline/src/maktaba_pipeline/media/track_selection.py` | Pure-function selection algorithm; no I/O, no DB. |
| `pipeline/src/maktaba_pipeline/media/iso639.py` | ISO 639-1/639-2B/639-2T → 639-3 normalization. |
| `pipeline/src/maktaba_pipeline/media/track_filters.py` | Disposition + title-regex filters (commentary, descriptive). |
| `pipeline/src/maktaba_pipeline/media/langid_probe.py` | Sample-and-detect path for `und` tracks. |
| `pipeline/src/maktaba_pipeline/pipeline/stages/select_track.py` | Stage wrapper: DB I/O, job enqueue, audit row. |
| `pipeline/tests/media/test_track_selection.py` | Unit tests for the pure selector (every case in story §Test cases). |
| `pipeline/tests/media/test_iso639.py` | Normalization unit tests. |
| `pipeline/tests/pipeline/stages/test_select_track_stage.py` | Integration test against an in-memory SQLite (mirrors prod schema via `shared/db/migrations/`). |
| `pipeline/tests/media/test_langid_probe.py` | Mocks ffmpeg + whisper-cpp; asserts confidence threshold + persisted columns. |
| `shared/db/migrations/0009_audio_tracks_extensions.sql` | `track_selection_decisions` audit table; `audio_tracks.detected_language` + confidence; `videos.metadata.track_override` is JSONB so no migration needed. |
| `shared/db/queries/track_selection.sql` | sqlc input — read tracks/lib, write decision, enqueue extract jobs. |
| `api/internal/tracks/preview.go` | `GET /api/videos/{id}/tracks` handler. |
| `api/internal/tracks/preview_test.go` | Handler tests. |
| `api/internal/tracks/override.go` | `PUT /api/videos/{id}/tracks/override` handler. |
| `api/internal/tracks/override_test.go` | Handler tests. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/pipeline/runner.py` | Register `select_track` between `probe` and `extract`. |
| `pipeline/pyproject.toml` | Add `langcodes>=3.4` (ISO 639 conversions) and `whisper-cpp-python>=0.2` (tiny model only — already a dependency surface in Epic 3, gated import). |
| `api/go.mod` | No new deps; reuses existing `chi`, `pgx`, `sqlc` setup. |
| `shared/db/queries/track_selection.sql` | New sqlc input for both Go (`api/internal/db/`) and Python (`pipeline/src/maktaba_pipeline/db/`). |
| `specs/epics/02-audio-extraction/README.md` | Tick story 2.2 once landed. |

### 2.3 Function signatures (canonical)

Python — pure selector:

```python
# pipeline/src/maktaba_pipeline/media/track_selection.py
from dataclasses import dataclass
from typing import Sequence

@dataclass(frozen=True)
class AudioTrackRow:
    id: int
    index: int
    codec: str
    channels: int
    sample_rate: int
    language: str            # ISO 639-3, normalized; never NULL — 'und' if unknown
    title: str | None
    is_default: bool
    disposition: dict        # raw ffprobe disposition dict
    detected_language: str | None         # populated by langid_probe (Section 7)
    detected_language_confidence: float | None

@dataclass(frozen=True)
class LibrarySettings:
    preferred_audio_language: str | None  # ISO 639 in any form; normalized internally
    multi_audio: bool
    exclude_descriptive: bool             # default True
    include_commentary: bool              # default False
    langid_confidence_threshold: float    # default 0.6

@dataclass(frozen=True)
class SelectionDecision:
    selected_track_ids: tuple[int, ...]
    rule: str                # "user_override" | "preferred_language" | "arabic" | "default" | "first" | "multi_audio_all"
    rejected: tuple[tuple[int, str], ...]   # (track_id, reason) for audit

def select_tracks(
    tracks: Sequence[AudioTrackRow],
    library: LibrarySettings,
    *,
    track_override_id: int | None = None,
) -> SelectionDecision: ...
```

Python — language normalization:

```python
# pipeline/src/maktaba_pipeline/media/iso639.py
def to_iso639_3(code: str | None) -> str:
    """
    Map any common form (639-1 'ar', 639-2/B 'ara', 639-2/T 'ara', BCP-47 'ar-SA',
    container quirks 'arb' (MSA), 'mul', 'zxx') to ISO 639-3. Returns 'und' for
    None, empty, '???', or anything that fails to parse.

    Pure, allocation-free for the cache hits; backed by lru_cache(maxsize=2048).
    """

def is_arabic(code: str) -> bool:
    """ISO 639-3 'ara' (macrolanguage) or any of its individual languages
    (arb=MSA, arz=Egyptian, apc=Levantine, ...). Used by the priority rule
    'prefer Arabic'."""
```

Python — disposition / regex filters:

```python
# pipeline/src/maktaba_pipeline/media/track_filters.py
DESCRIPTIVE_TITLE_RE = re.compile(
    r"\b(audio[- ]?descri(ption|bed)|described|sdh|cc|hearing impaired)\b",
    re.IGNORECASE,
)

def is_commentary(t: AudioTrackRow) -> bool:
    return bool(t.disposition.get("commentary"))

def is_descriptive(t: AudioTrackRow) -> bool:
    if t.disposition.get("descriptions") or t.disposition.get("hearing_impaired"):
        return True
    if t.title and DESCRIPTIVE_TITLE_RE.search(t.title):
        return True
    return False
```

Python — stage wrapper:

```python
# pipeline/src/maktaba_pipeline/pipeline/stages/select_track.py
class SelectTrackStage(Stage[VideoId, list[JobId]]):
    name = "select_track"
    input_state = VideoState.PROBED
    output_state = VideoState.PROBED   # state stays PROBED; extract owns the next transition

    async def run(self, ctx: StageContext, video_id: VideoId) -> list[JobId]:
        ...
```

Go — preview handler:

```go
// api/internal/tracks/preview.go
package tracks

type TrackView struct {
    ID                int64   `json:"id"`
    Index             int     `json:"index"`
    Codec             string  `json:"codec"`
    Channels          int     `json:"channels"`
    Language          string  `json:"language"`           // ISO 639-3
    Title             string  `json:"title,omitempty"`
    IsDefault         bool    `json:"is_default"`
    IsCommentary      bool    `json:"is_commentary"`
    IsDescriptive     bool    `json:"is_descriptive"`
    DetectedLang      string  `json:"detected_language,omitempty"`
    DetectedConfidence *float64 `json:"detected_language_confidence,omitempty"`
    SelectedForExtract bool   `json:"selected_for_extract"`
    SelectionRule     string  `json:"selection_rule,omitempty"`
}

// GET /api/videos/{id}/tracks
func (h *Handler) GetTracks(w http.ResponseWriter, r *http.Request) { ... }

// PUT /api/videos/{id}/tracks/override   body: { "audio_track_id": 42 } | { "audio_track_id": null }
func (h *Handler) SetOverride(w http.ResponseWriter, r *http.Request) { ... }
```

### 2.4 Algorithm (prose)

Given a list of `AudioTrackRow`s and a `LibrarySettings`:

1. **Apply hard filters** — drop tracks where `is_commentary(t)` is true (unless `library.include_commentary`); drop tracks where `is_descriptive(t)` is true (unless `library.exclude_descriptive` is false). Record rejections with reason.
2. **Track override short-circuit** — if `track_override_id` is set, look it up in the *post-filter* list. If present → return it as the sole selected track with `rule = "user_override"`. If absent (the user pinned a commentary or descriptive track) → still return it; the user wins. If the override id is not in the *un-filtered* list at all → `ValueError("track_override_id not in this video")` (caller turns this into a 422 over HTTP).
3. **Multi-audio fanout** — if `library.multi_audio` is true → return all post-filter tracks with `rule = "multi_audio_all"`. The stage will enqueue one extract job per id.
4. **Apply priority list** (first match wins, ties broken by §2.5):
   1. Any track whose `effective_language(t)` (defined below) equals `to_iso639_3(library.preferred_audio_language)` → `rule = "preferred_language"`.
   2. Any track whose `effective_language(t)` is Arabic (per `is_arabic`) → `rule = "arabic"`.
   3. Any track with `is_default = true` → `rule = "default"`.
   4. The first track by `index` → `rule = "first"`.
5. **`effective_language(t)`** is `t.language` if it is not `'und'`; else `t.detected_language` if `t.detected_language_confidence >= library.langid_confidence_threshold`; else `'und'`.
6. **Empty input** — `tracks == []` → `ValueError`. The probe stage already routes audioless videos to `READY_NO_AUDIO` (Story 2.1 AC #3); reaching `select_track` with no tracks is a programming error.

### 2.5 Tie-breakers

Within a rule, multiple candidates are common (`eng` stereo vs `eng` 5.1, two default-tagged tracks from a buggy mux). Sort the candidates and pick `[0]`:

```
key = (
    -channels,           # more channels wins (5.1 > stereo)
    not is_default,      # default flag wins
    index,               # earliest index wins
)
```

This is deterministic across re-runs (Story 2.2 edge case "Selection determinism under re-probe") because every key component is recorded in `audio_tracks` and the `index` is provided by ffprobe in stable stream order.

## 3. Python code scaffolding

### 3.1 `track_selection.py`

```python
"""Track selection — picks one (or many, for multi-audio libraries) audio_tracks
rows for the extract stage. Pure function; no DB, no I/O.

Selection priority (first match wins, ties broken by tiebreaker_key):
  user_override → multi_audio_all → preferred_language → arabic → default → first
"""
from __future__ import annotations
from dataclasses import dataclass
from typing import Sequence

from .iso639 import is_arabic, to_iso639_3
from .track_filters import is_commentary, is_descriptive


@dataclass(frozen=True)
class AudioTrackRow:
    id: int
    index: int
    codec: str
    channels: int
    sample_rate: int
    language: str
    title: str | None
    is_default: bool
    disposition: dict
    detected_language: str | None = None
    detected_language_confidence: float | None = None


@dataclass(frozen=True)
class LibrarySettings:
    preferred_audio_language: str | None = None
    multi_audio: bool = False
    exclude_descriptive: bool = True
    include_commentary: bool = False
    langid_confidence_threshold: float = 0.6


@dataclass(frozen=True)
class SelectionDecision:
    selected_track_ids: tuple[int, ...]
    rule: str
    rejected: tuple[tuple[int, str], ...]


def _effective_language(t: AudioTrackRow, threshold: float) -> str:
    if t.language and t.language != "und":
        return t.language
    if (
        t.detected_language
        and t.detected_language_confidence is not None
        and t.detected_language_confidence >= threshold
    ):
        return t.detected_language
    return "und"


def _tiebreaker_key(t: AudioTrackRow) -> tuple:
    return (-t.channels, not t.is_default, t.index)


def select_tracks(
    tracks: Sequence[AudioTrackRow],
    library: LibrarySettings,
    *,
    track_override_id: int | None = None,
) -> SelectionDecision:
    if not tracks:
        raise ValueError("select_tracks: no audio tracks; probe should have routed to READY_NO_AUDIO")

    rejected: list[tuple[int, str]] = []
    keep: list[AudioTrackRow] = []

    for t in tracks:
        if not library.include_commentary and is_commentary(t):
            rejected.append((t.id, "commentary"))
            continue
        if library.exclude_descriptive and is_descriptive(t):
            rejected.append((t.id, "descriptive"))
            continue
        keep.append(t)

    # User override wins absolutely — even over filters, even over multi_audio.
    if track_override_id is not None:
        for t in tracks:  # check the unfiltered list so override can resurrect a filtered track
            if t.id == track_override_id:
                return SelectionDecision(
                    selected_track_ids=(t.id,),
                    rule="user_override",
                    rejected=tuple(r for r in rejected if r[0] != t.id),
                )
        raise ValueError(f"track_override_id={track_override_id} not present in this video's tracks")

    if not keep:
        # Every track was commentary/descriptive. Story §Edge cases: still pick the
        # least-bad option (the first descriptive track) rather than failing.
        # Caller logs WARN; selection never blocks the pipeline.
        fallback = min(tracks, key=_tiebreaker_key)
        return SelectionDecision(
            selected_track_ids=(fallback.id,),
            rule="fallback_after_filter",
            rejected=tuple(rejected),
        )

    if library.multi_audio:
        ordered = sorted(keep, key=_tiebreaker_key)
        return SelectionDecision(
            selected_track_ids=tuple(t.id for t in ordered),
            rule="multi_audio_all",
            rejected=tuple(rejected),
        )

    pref = to_iso639_3(library.preferred_audio_language) if library.preferred_audio_language else None
    threshold = library.langid_confidence_threshold

    rules: list[tuple[str, callable]] = [
        ("preferred_language", lambda t: pref is not None and _effective_language(t, threshold) == pref),
        ("arabic", lambda t: is_arabic(_effective_language(t, threshold))),
        ("default", lambda t: t.is_default),
        ("first", lambda _t: True),
    ]
    for rule_name, predicate in rules:
        candidates = [t for t in keep if predicate(t)]
        if candidates:
            winner = min(candidates, key=_tiebreaker_key)
            return SelectionDecision(
                selected_track_ids=(winner.id,),
                rule=rule_name,
                rejected=tuple(rejected),
            )

    # Unreachable: the "first" rule's predicate is always true and `keep` is non-empty.
    raise AssertionError("select_tracks: priority list exhausted; this is a bug")
```

### 3.2 `iso639.py`

```python
"""ISO 639 normalization. Wraps `langcodes` so the rest of the pipeline can
treat language as a flat 639-3 string."""
from __future__ import annotations
from functools import lru_cache

from langcodes import Language, tag_is_valid


_ARABIC_INDIVIDUALS = frozenset({
    "arb",  # Modern Standard Arabic
    "arz",  # Egyptian Arabic
    "apc",  # Levantine Arabic
    "acm",  # Mesopotamian Arabic
    "ary",  # Moroccan Arabic
    "apd",  # Sudanese Arabic
    "ajp",  # South Levantine
    "afb",  # Gulf Arabic
    "ayl",  # Libyan Arabic
    "ayn",  # Sanaani Arabic
    "ayp",  # North Mesopotamian Arabic
    "shu",  # Chadian Arabic
    "ssh",  # Shihhi Arabic
})


@lru_cache(maxsize=2048)
def to_iso639_3(code: str | None) -> str:
    if not code:
        return "und"
    raw = code.strip().lower()
    if raw in {"und", "???", "mul", "zxx", "und-und"}:
        return "und"
    try:
        if not tag_is_valid(raw):
            return "und"
        lang = Language.get(raw)
        three = lang.to_alpha3()
        return three or "und"
    except (KeyError, LookupError, ValueError):
        return "und"


def is_arabic(code: str) -> bool:
    return code == "ara" or code in _ARABIC_INDIVIDUALS
```

### 3.3 `track_filters.py`

```python
import re
from .track_selection import AudioTrackRow

DESCRIPTIVE_TITLE_RE = re.compile(
    r"\b(audio[- ]?descri(ption|bed)|described|sdh|cc|hearing[- ]?impaired)\b",
    re.IGNORECASE,
)


def is_commentary(t: AudioTrackRow) -> bool:
    return bool(t.disposition.get("commentary"))


def is_descriptive(t: AudioTrackRow) -> bool:
    if t.disposition.get("descriptions") or t.disposition.get("hearing_impaired"):
        return True
    if t.title and DESCRIPTIVE_TITLE_RE.search(t.title):
        return True
    return False
```

### 3.4 `pipeline/stages/select_track.py`

```python
"""select_track stage — between probe and extract.

Loads the audio_tracks rows that probe wrote, asks the pure selector to choose
one (or many for multi_audio), enqueues an extract job per selection, and
records the decision in track_selection_decisions for audit.
"""
from __future__ import annotations
import json
import structlog
from uuid import UUID

from maktaba_pipeline.db import queries
from maktaba_pipeline.media.track_selection import (
    AudioTrackRow, LibrarySettings, select_tracks,
)
from maktaba_pipeline.media.iso639 import to_iso639_3
from maktaba_pipeline.media.langid_probe import maybe_detect_language
from maktaba_pipeline.pipeline.types import Stage, StageContext, VideoState

log = structlog.get_logger(__name__)


class SelectTrackStage(Stage):
    name = "select_track"
    input_state = VideoState.PROBED
    output_state = VideoState.PROBED  # extract owns the AUDIO_EXTRACTED transition

    async def run(self, ctx: StageContext, video_id: UUID) -> list[int]:
        async with ctx.db.tx() as conn:
            video = await queries.get_video_with_library(conn, video_id)
            raw_tracks = await queries.list_audio_tracks(conn, video_id)
            override = (video.metadata or {}).get("track_override")

        tracks = [
            AudioTrackRow(
                id=r.id,
                index=r.index,
                codec=r.codec,
                channels=r.channels,
                sample_rate=r.sample_rate,
                language=to_iso639_3(r.language),
                title=r.title,
                is_default=r.is_default,
                disposition=r.disposition or {},
                detected_language=r.detected_language,
                detected_language_confidence=r.detected_language_confidence,
            )
            for r in raw_tracks
        ]

        # Section 7: opportunistic per-track lang-id for 'und' tracks.
        settings_dict = video.library_settings or {}
        if settings_dict.get("langid_undetermined", False):
            for i, t in enumerate(tracks):
                if t.language == "und" and t.detected_language is None:
                    detected, conf = await maybe_detect_language(ctx, video.path, t)
                    if detected is not None:
                        async with ctx.db.tx() as conn:
                            await queries.update_track_detected_language(
                                conn, t.id, detected, conf,
                            )
                        tracks[i] = AudioTrackRow(
                            **{**t.__dict__, "detected_language": detected, "detected_language_confidence": conf},
                        )

        library = LibrarySettings(
            preferred_audio_language=settings_dict.get("preferred_audio_language"),
            multi_audio=settings_dict.get("multi_audio", False),
            exclude_descriptive=settings_dict.get("exclude_descriptive", True),
            include_commentary=settings_dict.get("include_commentary", False),
            langid_confidence_threshold=settings_dict.get("langid_confidence_threshold", 0.6),
        )

        decision = select_tracks(tracks, library, track_override_id=override)

        log.info(
            "track_selection",
            video_id=str(video_id),
            rule=decision.rule,
            selected=list(decision.selected_track_ids),
            rejected=[{"id": tid, "reason": why} for tid, why in decision.rejected],
        )

        async with ctx.db.tx() as conn:
            await queries.insert_track_selection_decision(
                conn,
                video_id=video_id,
                rule=decision.rule,
                selected_track_ids=list(decision.selected_track_ids),
                rejected_json=json.dumps([{"id": t, "reason": r} for t, r in decision.rejected]),
                library_settings_snapshot=json.dumps(settings_dict),
            )
            job_ids = []
            for track_id in decision.selected_track_ids:
                jid = await queries.enqueue_extract_job(
                    conn,
                    video_id=video_id,
                    audio_track_id=track_id,
                )
                job_ids.append(jid)
        return job_ids
```

### 3.5 `langid_probe.py` (Section 7 implementation)

```python
"""Sample-and-detect language ID for `und`-tagged tracks.

Runs `ffmpeg -ss {mid} -t 30 -map 0:a:{idx} -ac 1 -ar 16000 -f s16le pipe:1`
and feeds the bytes to whisper-cpp's tiny-multilingual `--detect-language-only`
mode. Never persists audio. Result is cached on the audio_tracks row so the
next selection run is a no-op.
"""
from __future__ import annotations
import asyncio
import structlog

log = structlog.get_logger(__name__)

SAMPLE_SECONDS = 30
SAMPLE_OFFSET_FRACTION = 1 / 3   # avoid intros/outros


async def maybe_detect_language(
    ctx, video_path: str, track,
) -> tuple[str | None, float | None]:
    """Returns (iso_639_3_code, confidence) or (None, None) on any failure.
    Failures are logged at WARN; selection treats `und` as last resort regardless.
    """
    try:
        duration = await ctx.media_info.duration_seconds(video_path)
    except Exception as e:
        log.warning("langid_duration_failed", path=video_path, err=str(e))
        return None, None
    if duration is None or duration < 60:
        return None, None  # too short to bother

    offset = max(0.0, duration * SAMPLE_OFFSET_FRACTION - SAMPLE_SECONDS / 2)
    cmd = [
        "ffmpeg", "-hide_banner", "-nostdin", "-threads", "1",
        "-ss", f"{offset:.2f}",
        "-t", str(SAMPLE_SECONDS),
        "-i", video_path,
        "-map", f"0:a:{track.index}",
        "-ac", "1", "-ar", "16000", "-sample_fmt", "s16",
        "-f", "s16le", "pipe:1",
    ]
    proc = await asyncio.create_subprocess_exec(
        *cmd, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE,
    )
    pcm, stderr = await proc.communicate()
    if proc.returncode != 0:
        log.warning("langid_ffmpeg_failed", returncode=proc.returncode,
                    stderr=stderr[-2048:].decode("utf-8", "replace"))
        return None, None

    code, conf = await ctx.langid.detect(pcm)  # whisper-cpp wrapper
    return code, conf
```

## 4. Go code scaffolding

### 4.1 `api/internal/tracks/preview.go`

```go
// Package tracks exposes the preview surface for Story 2.2: list audio tracks
// for a video, mark which one selection chose, and let the user pin an override.
package tracks

import (
    "encoding/json"
    "errors"
    "net/http"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"

    "maktaba/api/internal/db"
    "maktaba/pipeline/internal/iso639" // shared Go mirror of pipeline/media/iso639.py
)

type Handler struct {
    Q *db.Queries
}

type TrackView struct {
    ID                 int64    `json:"id"`
    Index              int      `json:"index"`
    Codec              string   `json:"codec"`
    Channels           int      `json:"channels"`
    Language           string   `json:"language"`
    Title              string   `json:"title,omitempty"`
    IsDefault          bool     `json:"is_default"`
    IsCommentary       bool     `json:"is_commentary"`
    IsDescriptive      bool     `json:"is_descriptive"`
    DetectedLang       string   `json:"detected_language,omitempty"`
    DetectedConfidence *float64 `json:"detected_language_confidence,omitempty"`
    SelectedForExtract bool     `json:"selected_for_extract"`
    SelectionRule      string   `json:"selection_rule,omitempty"`
}

func (h *Handler) GetTracks(w http.ResponseWriter, r *http.Request) {
    videoID, err := uuid.Parse(r.PathValue("id"))
    if err != nil {
        http.Error(w, "invalid video id", http.StatusBadRequest)
        return
    }

    rows, err := h.Q.ListTracksWithSelection(r.Context(), videoID)
    if errors.Is(err, pgx.ErrNoRows) || len(rows) == 0 {
        http.NotFound(w, r)
        return
    }
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    out := make([]TrackView, 0, len(rows))
    for _, row := range rows {
        out = append(out, TrackView{
            ID:                 row.ID,
            Index:              int(row.Index),
            Codec:              row.Codec,
            Channels:           int(row.Channels),
            Language:           iso639.ToIso6393(row.Language.String),
            Title:              row.Title.String,
            IsDefault:          row.IsDefault,
            IsCommentary:       row.IsCommentary,
            IsDescriptive:      row.IsDescriptive,
            DetectedLang:       row.DetectedLanguage.String,
            DetectedConfidence: row.DetectedLanguageConfidence,
            SelectedForExtract: row.SelectedForExtract,
            SelectionRule:      row.SelectionRule.String,
        })
    }
    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(out)
}
```

### 4.2 `api/internal/tracks/override.go`

```go
type OverrideRequest struct {
    AudioTrackID *int64 `json:"audio_track_id"`  // nil clears the override
}

func (h *Handler) SetOverride(w http.ResponseWriter, r *http.Request) {
    videoID, err := uuid.Parse(r.PathValue("id"))
    if err != nil {
        http.Error(w, "invalid video id", http.StatusBadRequest)
        return
    }
    var req OverrideRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "invalid body", http.StatusBadRequest)
        return
    }

    if req.AudioTrackID != nil {
        ok, err := h.Q.AudioTrackBelongsToVideo(r.Context(), db.AudioTrackBelongsToVideoParams{
            VideoID: videoID,
            ID:      *req.AudioTrackID,
        })
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }
        if !ok {
            http.Error(w, "track does not belong to this video", http.StatusUnprocessableEntity)
            return
        }
    }

    if err := h.Q.SetTrackOverride(r.Context(), db.SetTrackOverrideParams{
        VideoID:      videoID,
        AudioTrackID: req.AudioTrackID,
    }); err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    // Re-enqueue the select_track stage so the override takes effect on the next sweep.
    if err := h.Q.RequeueSelectTrack(r.Context(), videoID); err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    w.WriteHeader(http.StatusNoContent)
}
```

### 4.3 `pipeline/internal/iso639` — Go mirror

A small Go package keeps a parallel mapping for the API's preview rendering. It uses `golang.org/x/text/language` plus an explicit Arabic-individual table identical to Section 3.2's set. Cross-language parity test: `pipeline/tests/cross_lang_iso639_test.py` invokes the Go binary on a 200-row CSV and compares results to the Python output column-for-column.

## 5. Database changes

### 5.1 Migration `0009_audio_tracks_extensions.sql`

```sql
-- +goose Up
-- +goose StatementBegin

-- Per-track language ID (Section 7).
ALTER TABLE audio_tracks
    ADD COLUMN IF NOT EXISTS detected_language TEXT,
    ADD COLUMN IF NOT EXISTS detected_language_confidence REAL;

ALTER TABLE audio_tracks
    ADD CONSTRAINT audio_tracks_detected_lang_format_chk
    CHECK (
        detected_language IS NULL
        OR detected_language ~ '^[a-z]{3}$'
    );

ALTER TABLE audio_tracks
    ADD CONSTRAINT audio_tracks_detected_lang_conf_range_chk
    CHECK (
        detected_language_confidence IS NULL
        OR (detected_language_confidence >= 0 AND detected_language_confidence <= 1)
    );

-- Disposition stored verbatim from ffprobe so filters can read commentary,
-- descriptions, hearing_impaired, etc. without growing the schema per flag.
ALTER TABLE audio_tracks
    ADD COLUMN IF NOT EXISTS disposition JSONB NOT NULL DEFAULT '{}'::jsonb;

-- Audit row per selection run. One row per (video, run); replaced on re-run.
CREATE TABLE IF NOT EXISTS track_selection_decisions (
    video_id                  UUID PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    rule                      TEXT NOT NULL,
    selected_track_ids        BIGINT[] NOT NULL,
    rejected                  JSONB NOT NULL DEFAULT '[]'::jsonb,
    library_settings_snapshot JSONB NOT NULL,
    decided_at                TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS track_selection_decisions_rule_idx
    ON track_selection_decisions (rule);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS track_selection_decisions;
ALTER TABLE audio_tracks
    DROP CONSTRAINT IF EXISTS audio_tracks_detected_lang_conf_range_chk,
    DROP CONSTRAINT IF EXISTS audio_tracks_detected_lang_format_chk;
ALTER TABLE audio_tracks
    DROP COLUMN IF EXISTS detected_language_confidence,
    DROP COLUMN IF EXISTS detected_language,
    DROP COLUMN IF EXISTS disposition;
-- +goose StatementEnd
```

### 5.2 sqlc input — `shared/db/queries/track_selection.sql`

```sql
-- name: ListAudioTracks :many
SELECT id, index, codec, channels, sample_rate, language, title, is_default,
       disposition, detected_language, detected_language_confidence
  FROM audio_tracks
 WHERE video_id = $1
 ORDER BY index;

-- name: GetVideoWithLibrary :one
SELECT v.id            AS video_id,
       v.path,
       v.metadata,
       l.settings      AS library_settings
  FROM videos v
  JOIN libraries l ON l.id = v.library_id
 WHERE v.id = $1;

-- name: UpdateTrackDetectedLanguage :exec
UPDATE audio_tracks
   SET detected_language = $2,
       detected_language_confidence = $3
 WHERE id = $1;

-- name: InsertTrackSelectionDecision :exec
INSERT INTO track_selection_decisions
    (video_id, rule, selected_track_ids, rejected, library_settings_snapshot)
VALUES ($1, $2, $3, $4::jsonb, $5::jsonb)
ON CONFLICT (video_id) DO UPDATE
   SET rule = EXCLUDED.rule,
       selected_track_ids = EXCLUDED.selected_track_ids,
       rejected = EXCLUDED.rejected,
       library_settings_snapshot = EXCLUDED.library_settings_snapshot,
       decided_at = now();

-- name: EnqueueExtractJob :one
INSERT INTO processing_jobs (video_id, stage, state, extra)
VALUES ($1, 'extract', 'pending', jsonb_build_object('audio_track_id', $2::bigint))
RETURNING id;

-- name: ListTracksWithSelection :many
-- Used by GET /api/videos/{id}/tracks. Joins the per-track rows with the
-- decision row so the UI knows which track is selected and why.
SELECT a.id,
       a.index,
       a.codec,
       a.channels,
       a.language,
       a.title,
       a.is_default,
       COALESCE((a.disposition->>'commentary')::boolean, false)        AS is_commentary,
       COALESCE((a.disposition->>'descriptions')::boolean, false)
       OR COALESCE((a.disposition->>'hearing_impaired')::boolean, false) AS is_descriptive,
       a.detected_language,
       a.detected_language_confidence,
       (a.id = ANY(d.selected_track_ids)) AS selected_for_extract,
       d.rule                              AS selection_rule
  FROM audio_tracks a
  LEFT JOIN track_selection_decisions d ON d.video_id = a.video_id
 WHERE a.video_id = $1
 ORDER BY a.index;

-- name: AudioTrackBelongsToVideo :one
SELECT EXISTS (
    SELECT 1 FROM audio_tracks WHERE id = $1 AND video_id = $2
) AS ok;

-- name: SetTrackOverride :exec
-- Sets metadata.track_override on the videos row. NULL clears it.
UPDATE videos
   SET metadata = CASE
                    WHEN $2::bigint IS NULL THEN metadata - 'track_override'
                    ELSE jsonb_set(metadata, '{track_override}', to_jsonb($2::bigint), true)
                  END,
       updated_at = now()
 WHERE id = $1;

-- name: RequeueSelectTrack :exec
INSERT INTO processing_jobs (video_id, stage, state)
VALUES ($1, 'select_track', 'pending')
ON CONFLICT (video_id, stage) WHERE state IN ('pending','running')
DO NOTHING;
```

## 6. Multi-track extraction

When `library.multi_audio = true`, `select_tracks` returns N ids and the stage enqueues N extract jobs. Each downstream extract → transcribe chain runs independently:

1. **Job extras carry the track id.** `processing_jobs.extra->>'audio_track_id'` is the only link; the extract worker reads it via `AS bigint` and passes it as `-map 0:a:{idx}` after looking up `audio_tracks.index` (NOT the id). Workers must never trust `audio_tracks.id == audio_tracks.index`.
2. **Per-track transcripts.** `transcripts.audio_track_id` is already part of the unique key (architecture §8.1), so two tracks of the same video produce two transcript rows with no conflict.
3. **Subtitle-file naming.** When N transcripts exist for one video, the subtitle generator (Epic 4) writes `{basename}.{lang3}.srt` with the language from `transcripts.language` — collision is impossible because we already filtered duplicates within a language by tiebreaker.
4. **Indexer fan-in.** All transcripts for a video are indexed under the same `videos.id`; FTS hits for "where does the speaker say X?" return the video, and the segment carries `audio_track_id` so the player can switch tracks to the matched one.
5. **Cancellation.** Cancelling a video cancels the *select_track* job; in-flight extract/transcribe jobs for that video are stopped via the existing pause-and-kill path (Story 2.3 AC #4). One paused track does not pause its sibling — they're independent rows.

## 7. Per-track language detection

Three layers, in order of cost:

| Layer | Source | Cost | When it runs |
|---|---|---|---|
| 1 | `tags.language` from ffprobe → `audio_tracks.language`, normalized via `to_iso639_3` | free | Story 2.1 (probe) |
| 2 | Sample-and-detect with whisper-cpp tiny-multilingual on a 30-s slice (`langid_probe.maybe_detect_language`) | ~150 ms CPU, no GPU; ffmpeg seek + decode dominates | When `library.settings.langid_undetermined = true` AND `audio_tracks.language = 'und'` AND `detected_language IS NULL`. Runs once per track; result is cached. |
| 3 | Full STT auto-detect — Epic 3 owns this | full transcription cost | Always, regardless of layer 2's outcome — STT may correct a layer-2 misclassification but doesn't gate selection. |

**Why three layers and not two?** Layer 2 lets the *selector* (this story) make a smart decision before paying for STT. A `und`-tagged track that layer 2 detects as `eng` with confidence 0.9 wins the `preferred_language="eng"` rule and is queued for transcription; without layer 2 it would lose that rule and we might transcribe the wrong track or fall back to "first by index". The per-track confidence is part of the priority calculation (`langid_confidence_threshold`, default 0.6) so that low-confidence detections don't fool the selector.

**Why whisper-cpp tiny instead of `mlx-whisper` tiny?** Track selection runs on every probe. Loading MLX (Metal GPU) on macOS for a 150 ms task adds ~2 s of warmup that overshadows the work. `whisper-cpp` is CPU, ~50 MB model, no warmup, and produces the same `language_probs` output we need. The full transcription step (Epic 3) keeps using MLX/CUDA where appropriate.

**Caching contract.** Once `(detected_language, detected_language_confidence)` is set on an `audio_tracks` row, layer 2 never runs again for that row. Re-probing the file (Story 2.1's idempotent UPSERT) preserves these columns by virtue of `ON CONFLICT DO NOTHING` on `(video_id, index)`.

**Library opt-in.** `langid_undetermined` defaults to **false** because most files have correct language tags and the cost (an extra 30-s ffmpeg + a small model load per `und` track) isn't worth it for libraries with clean metadata. Users with messy archives flip it on per library.

## 8. Test plan

### 8.1 Unit tests (`tests/media/test_track_selection.py`)

Each test below is exactly one of the cases listed in the story's `## Test cases` plus the edge cases.

| Test | What it pins |
|---|---|
| `test_select_prefers_user_language` | Library prefers `en`; tracks `[ara, eng]` → `selected_track_ids == (eng_id,)`, `rule == "preferred_language"`. |
| `test_select_falls_back_to_arabic` | No preference; tracks `[eng, ara]` → ara, `rule == "arabic"`. |
| `test_select_uses_default_disposition` | No preference, no Arabic; tracks `[eng-non-default, fre-default]` → fre, `rule == "default"`. |
| `test_select_falls_back_to_first` | No preference, no Arabic, no default → index 0, `rule == "first"`. |
| `test_select_multi_audio_returns_all` | `multi_audio=True`, three tracks → three ids in tiebreaker order, `rule == "multi_audio_all"`. |
| `test_select_excludes_commentary` | Track with `disposition.commentary=1` is skipped by all rules. |
| `test_select_excludes_descriptive_by_disposition` | `disposition.descriptions=1` track is skipped. |
| `test_select_excludes_descriptive_by_title_regex` | Track titled "Audio Description" is skipped. |
| `test_select_descriptive_kept_when_lib_disables_filter` | `exclude_descriptive=False` → descriptive track is eligible. |
| `test_select_commentary_kept_when_lib_includes` | `include_commentary=True` → commentary track is eligible. |
| `test_select_user_override_wins_over_priority` | Override id pinned to a non-Arabic non-preferred track → that exact id, `rule == "user_override"`. |
| `test_select_user_override_resurrects_filtered_track` | Override pinned to a *commentary* track → that id is returned (user wins). |
| `test_select_user_override_unknown_id_raises` | Override id not in tracks list → `ValueError`. |
| `test_select_tiebreak_more_channels_wins` | Two `eng` tracks (stereo + 5.1) → 5.1 selected. |
| `test_select_tiebreak_default_beats_index` | Two `eng` tracks same channel count, one `is_default` → default wins. |
| `test_select_tiebreak_index_breaks_remaining_ties` | Two `eng` 5.1 tracks, neither default → lower index wins. |
| `test_select_und_with_high_confidence_detection_counts_as_language` | Track tagged `und` with `detected_language='ara', detected_confidence=0.9` and Arabic is last-resort priority → wins `arabic` rule. |
| `test_select_und_with_low_confidence_stays_und` | Same but `detected_confidence=0.3` (< threshold) → does not win the `arabic` rule; falls through. |
| `test_select_und_pcm_track_when_arabic_preferred` | One track, `lang=und, codec=pcm`, library prefers `ara` → returns the `und` track via `first` rule (story §AC #3). |
| `test_select_empty_input_raises` | `tracks=[]` → `ValueError`. |
| `test_select_all_filtered_falls_back` | Every track is commentary, `include_commentary=False` → returns lowest-tiebreaker-key track with `rule == "fallback_after_filter"`. |
| `test_select_determinism_across_reorder` | Same tracks shuffled into different list orders → identical decision (the algorithm sorts internally). |

### 8.2 Unit tests (`tests/media/test_iso639.py`)

| Test | What it pins |
|---|---|
| `test_normalize_639_1_to_639_3` | `"ar"` → `"ara"`, `"en"` → `"eng"`, `"fr"` → `"fra"`. |
| `test_normalize_639_2b_aliases` | `"fre"` (639-2/B) → `"fra"` (639-3 = 639-2/T). |
| `test_normalize_arabic_individuals_passthrough` | `"arb"` → `"arb"`, `"arz"` → `"arz"`. |
| `test_normalize_bcp47_strips_region` | `"ar-SA"` → `"ara"`, `"en-US"` → `"eng"`. |
| `test_normalize_unknown_returns_und` | `"???"`, `""`, `None`, `"xx"`, `"mul"`, `"zxx"` → `"und"`. |
| `test_is_arabic_macrolanguage` | `is_arabic("ara")` is True. |
| `test_is_arabic_individuals` | `is_arabic("arb")`, `is_arabic("arz")`, `is_arabic("apc")` all True. |
| `test_is_arabic_negative` | `is_arabic("eng")`, `is_arabic("und")` False. |

### 8.3 Integration tests (`tests/pipeline/stages/test_select_track_stage.py`)

| Test | What it pins |
|---|---|
| `test_stage_enqueues_one_extract_job_for_single_track_video` | Probe wrote 3 tracks; default lib settings → 1 extract job, `extra->>'audio_track_id'` matches the selected id. |
| `test_stage_enqueues_n_jobs_for_multi_audio_lib` | Same probe; `multi_audio=True` → N extract jobs, ids are unique, all correspond to non-commentary tracks. |
| `test_stage_records_decision_row` | After run, `track_selection_decisions` has one row keyed on `video_id` with the rule that fired and the rejected track ids. |
| `test_stage_idempotent_on_replay` | Run the stage twice on the same `(video_id)` → same set of extract jobs (no duplicate `pending` rows; the unique partial index on `processing_jobs(video_id, stage) WHERE state IN ('pending','running')` blocks the second insert). |
| `test_stage_runs_langid_when_lib_opts_in` | Track has `language='und'`; `langid_undetermined=True`; mock `ctx.langid.detect` returns `("eng", 0.85)` → row's `detected_language` is updated and the rule fired is `arabic` (no), `preferred_language` (no), `first` (yes — there's one track). |
| `test_stage_skips_langid_when_lib_opted_out` | Same track but `langid_undetermined=False` → `detected_language` stays NULL, no ffmpeg subprocess spawned. |
| `test_stage_audit_includes_settings_snapshot` | `library_settings_snapshot` matches the lib row at decision time, even if the lib row changes afterward (the decision is frozen). |

### 8.4 Unit tests (`tests/media/test_langid_probe.py`)

| Test | What it pins |
|---|---|
| `test_langid_returns_none_for_short_files` | Duration 30 s → returns `(None, None)` without spawning ffmpeg. |
| `test_langid_offset_is_one_third_in` | Mock ffmpeg; assert the `-ss` argument is within `[duration/3 - 16, duration/3]`. |
| `test_langid_handles_ffmpeg_failure` | Mock ffmpeg returncode 1 → returns `(None, None)`, logs WARN. |
| `test_langid_writes_back_to_audio_tracks_via_ctx` | Mock ctx + queries; assert `update_track_detected_language` is called with the detected code and confidence. |

### 8.5 Go unit tests (`api/internal/tracks/preview_test.go`)

| Test | What it pins |
|---|---|
| `TestGetTracks_ReturnsAllTracksWithSelection` | DB has 3 tracks, decision selected ids `[2]` → response has 3 entries; only id 2 has `selected_for_extract=true`; `selection_rule` is the recorded rule. |
| `TestGetTracks_NotFoundForUnknownVideo` | Random UUID → 404. |
| `TestGetTracks_NormalizesLanguageToIso6393` | DB row has `language='ar-SA'` → response has `"language": "ara"`. |
| `TestSetOverride_Validates_TrackBelongsToVideo` | PUT with id of a track in *another* video → 422. |
| `TestSetOverride_Clear_RemovesMetadataKey` | PUT `{"audio_track_id": null}` → row's `metadata` no longer has the `track_override` key. |
| `TestSetOverride_Requeues_SelectTrack` | After successful PUT, a `processing_jobs(stage='select_track', state='pending')` row exists. |

### 8.6 Cross-language parity (`tests/cross_lang_iso639_test.py`)

A 200-row CSV (`shared/fixtures/iso639_parity.csv`) of `(input, expected)` pairs is run through both the Python `to_iso639_3` and the Go `iso639.ToIso6393`; results are compared cell-for-cell. Any divergence is a CI failure.

## 9. Test code scaffolding

### 9.1 `pipeline/tests/media/test_track_selection.py`

```python
from maktaba_pipeline.media.track_selection import (
    AudioTrackRow, LibrarySettings, select_tracks,
)


def _track(
    *, id, index=0, language="und", channels=2, is_default=False,
    title=None, disposition=None, codec="aac", sample_rate=48000,
    detected_language=None, detected_language_confidence=None,
):
    return AudioTrackRow(
        id=id, index=index, codec=codec, channels=channels,
        sample_rate=sample_rate, language=language, title=title,
        is_default=is_default, disposition=disposition or {},
        detected_language=detected_language,
        detected_language_confidence=detected_language_confidence,
    )


def test_select_prefers_user_language():
    tracks = [
        _track(id=1, index=0, language="ara"),
        _track(id=2, index=1, language="eng"),
    ]
    lib = LibrarySettings(preferred_audio_language="en")
    decision = select_tracks(tracks, lib)
    assert decision.selected_track_ids == (2,)
    assert decision.rule == "preferred_language"


def test_select_falls_back_to_arabic():
    tracks = [
        _track(id=1, index=0, language="eng"),
        _track(id=2, index=1, language="ara"),
    ]
    decision = select_tracks(tracks, LibrarySettings())
    assert decision.selected_track_ids == (2,)
    assert decision.rule == "arabic"


def test_select_uses_default_disposition():
    tracks = [
        _track(id=1, index=0, language="eng"),
        _track(id=2, index=1, language="fra", is_default=True),
    ]
    decision = select_tracks(tracks, LibrarySettings())
    assert decision.selected_track_ids == (2,)
    assert decision.rule == "default"


def test_select_falls_back_to_first():
    tracks = [
        _track(id=1, index=0, language="eng"),
        _track(id=2, index=1, language="fra"),
    ]
    decision = select_tracks(tracks, LibrarySettings())
    assert decision.selected_track_ids == (1,)
    assert decision.rule == "first"


def test_select_multi_audio_returns_all():
    tracks = [
        _track(id=1, index=0, language="ara"),
        _track(id=2, index=1, language="eng"),
        _track(id=3, index=2, language="fra"),
    ]
    decision = select_tracks(tracks, LibrarySettings(multi_audio=True))
    assert sorted(decision.selected_track_ids) == [1, 2, 3]
    assert decision.rule == "multi_audio_all"


def test_select_excludes_commentary():
    tracks = [
        _track(id=1, index=0, language="ara", disposition={"commentary": 1}),
        _track(id=2, index=1, language="eng"),
    ]
    decision = select_tracks(tracks, LibrarySettings())
    assert decision.selected_track_ids == (2,)
    assert (1, "commentary") in decision.rejected


def test_select_excludes_descriptive_by_title_regex():
    tracks = [
        _track(id=1, index=0, language="eng", title="English Audio Description"),
        _track(id=2, index=1, language="eng"),
    ]
    decision = select_tracks(tracks, LibrarySettings())
    assert decision.selected_track_ids == (2,)


def test_select_und_pcm_track_when_arabic_preferred():
    tracks = [
        _track(id=1, index=0, language="und", codec="pcm_s16le"),
    ]
    decision = select_tracks(tracks, LibrarySettings(preferred_audio_language="ar"))
    assert decision.selected_track_ids == (1,)
    assert decision.rule == "first"


def test_select_und_with_high_confidence_detection_counts_as_language():
    tracks = [
        _track(id=1, index=0, language="eng"),
        _track(
            id=2, index=1, language="und",
            detected_language="ara", detected_language_confidence=0.9,
        ),
    ]
    decision = select_tracks(tracks, LibrarySettings())
    assert decision.selected_track_ids == (2,)
    assert decision.rule == "arabic"


def test_select_und_with_low_confidence_stays_und():
    tracks = [
        _track(id=1, index=0, language="eng"),
        _track(
            id=2, index=1, language="und",
            detected_language="ara", detected_language_confidence=0.3,
        ),
    ]
    decision = select_tracks(tracks, LibrarySettings())
    # Falls through to "first" since neither track is Arabic at ≥0.6 confidence.
    assert decision.selected_track_ids == (1,)
    assert decision.rule == "first"


def test_select_tiebreak_more_channels_wins():
    tracks = [
        _track(id=1, index=0, language="eng", channels=2),
        _track(id=2, index=1, language="eng", channels=6),
    ]
    decision = select_tracks(tracks, LibrarySettings(preferred_audio_language="en"))
    assert decision.selected_track_ids == (2,)


def test_select_user_override_wins_over_priority():
    tracks = [
        _track(id=1, index=0, language="ara"),
        _track(id=2, index=1, language="eng"),
    ]
    decision = select_tracks(tracks, LibrarySettings(), track_override_id=2)
    assert decision.selected_track_ids == (2,)
    assert decision.rule == "user_override"


def test_select_user_override_resurrects_filtered_track():
    tracks = [
        _track(id=1, index=0, language="eng", disposition={"commentary": 1}),
        _track(id=2, index=1, language="ara"),
    ]
    decision = select_tracks(tracks, LibrarySettings(), track_override_id=1)
    assert decision.selected_track_ids == (1,)


def test_select_user_override_unknown_id_raises():
    tracks = [_track(id=1, index=0, language="eng")]
    try:
        select_tracks(tracks, LibrarySettings(), track_override_id=999)
    except ValueError:
        return
    raise AssertionError("expected ValueError for unknown override id")


def test_select_empty_input_raises():
    try:
        select_tracks([], LibrarySettings())
    except ValueError:
        return
    raise AssertionError("expected ValueError for empty tracks")


def test_select_determinism_across_reorder():
    a = _track(id=1, index=0, language="ara", channels=2)
    b = _track(id=2, index=1, language="ara", channels=6)
    c = _track(id=3, index=2, language="eng")
    d1 = select_tracks([a, b, c], LibrarySettings())
    d2 = select_tracks([c, a, b], LibrarySettings())
    d3 = select_tracks([b, c, a], LibrarySettings())
    assert d1.selected_track_ids == d2.selected_track_ids == d3.selected_track_ids
```

### 9.2 `api/internal/tracks/preview_test.go`

```go
package tracks_test

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/google/uuid"

    "maktaba/api/internal/tracks"
    "maktaba/api/internal/testdb"
)

func TestGetTracks_ReturnsAllTracksWithSelection(t *testing.T) {
    db := testdb.Fresh(t)
    videoID := testdb.SeedVideoWithTracks(t, db, []testdb.Track{
        {Index: 0, Language: "ara", Channels: 2},
        {Index: 1, Language: "eng", Channels: 6},
        {Index: 2, Language: "fre", IsDefault: true},
    })
    selected := testdb.SeedSelectionDecision(t, db, videoID, "preferred_language", []int64{2})

    h := &tracks.Handler{Q: db.Q}
    r := httptest.NewRequest(http.MethodGet, "/api/videos/"+videoID.String()+"/tracks", nil)
    r.SetPathValue("id", videoID.String())
    w := httptest.NewRecorder()

    h.GetTracks(w, r)

    if w.Code != http.StatusOK {
        t.Fatalf("status = %d, want 200", w.Code)
    }
    var got []tracks.TrackView
    if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
        t.Fatal(err)
    }
    if len(got) != 3 {
        t.Fatalf("len(got) = %d, want 3", len(got))
    }
    var selectedCount int
    for _, v := range got {
        if v.SelectedForExtract {
            selectedCount++
            if v.ID != selected {
                t.Fatalf("selected id = %d, want %d", v.ID, selected)
            }
            if v.SelectionRule != "preferred_language" {
                t.Fatalf("rule = %q, want preferred_language", v.SelectionRule)
            }
        }
    }
    if selectedCount != 1 {
        t.Fatalf("selected count = %d, want 1", selectedCount)
    }
}

func TestSetOverride_Validates_TrackBelongsToVideo(t *testing.T) {
    db := testdb.Fresh(t)
    videoA := testdb.SeedVideoWithTracks(t, db, []testdb.Track{{Index: 0, Language: "eng"}})
    videoB := testdb.SeedVideoWithTracks(t, db, []testdb.Track{{Index: 0, Language: "ara"}})
    foreignTrack := testdb.FirstTrackID(t, db, videoB)

    h := &tracks.Handler{Q: db.Q}
    body := bytes.NewBufferString(`{"audio_track_id":` + strconv.FormatInt(foreignTrack, 10) + `}`)
    r := httptest.NewRequest(http.MethodPut, "/api/videos/"+videoA.String()+"/tracks/override", body)
    r.SetPathValue("id", videoA.String())
    w := httptest.NewRecorder()

    h.SetOverride(w, r)
    if w.Code != http.StatusUnprocessableEntity {
        t.Fatalf("status = %d, want 422", w.Code)
    }
}

func TestSetOverride_Requeues_SelectTrack(t *testing.T) {
    db := testdb.Fresh(t)
    videoID := testdb.SeedVideoWithTracks(t, db, []testdb.Track{{Index: 0, Language: "eng"}})
    trackID := testdb.FirstTrackID(t, db, videoID)

    h := &tracks.Handler{Q: db.Q}
    body := bytes.NewBufferString(`{"audio_track_id":` + strconv.FormatInt(trackID, 10) + `}`)
    r := httptest.NewRequest(http.MethodPut, "/api/videos/"+videoID.String()+"/tracks/override", body)
    r.SetPathValue("id", videoID.String())
    w := httptest.NewRecorder()

    h.SetOverride(w, r)
    if w.Code != http.StatusNoContent {
        t.Fatalf("status = %d, want 204", w.Code)
    }
    if !testdb.HasPendingJob(t, db, videoID, "select_track") {
        t.Fatal("expected a pending select_track job after override")
    }
}
```

## 10. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| File has identical-language stereo + 5.1 tracks | 5.1 wins via tiebreaker `-channels`. | `test_select_tiebreak_more_channels_wins` |
| Audio-described track tagged via `disposition.descriptions` | Excluded by default (`exclude_descriptive=True`). | `test_select_excludes_descriptive_by_disposition` |
| Audio-described track tagged via title only | Title regex `\b(audio[- ]?description|sdh|cc|hearing impaired)\b` excludes it. | `test_select_excludes_descriptive_by_title_regex` |
| Library wants the descriptive track (accessibility-focused user) | `exclude_descriptive=False` makes it eligible. | `test_select_descriptive_kept_when_lib_disables_filter` |
| Library wants commentary | `include_commentary=True` keeps commentary tracks. | `test_select_commentary_kept_when_lib_includes` |
| User override pins a commentary track | Filter is bypassed for the override; user wins. | `test_select_user_override_resurrects_filtered_track` |
| All tracks are commentary AND library excludes commentary | `fallback_after_filter` rule returns the lowest-tiebreaker track and logs WARN; pipeline does not stall. | `test_select_all_filtered_falls_back` |
| Track with `und` language and `pcm` codec, library prefers `ara` | Still selected over no track at all (story §AC #3). | `test_select_und_pcm_track_when_arabic_preferred` |
| Track with `und` language but layer-2 detection finds `eng` at 0.9 | `_effective_language` returns `eng`; the `eng` rule (or the right one) fires. | `test_select_und_with_high_confidence_detection_counts_as_language` |
| Track with `und` and detection returns `ara` at 0.3 | `_effective_language` returns `und`; track loses the `arabic` rule. | `test_select_und_with_low_confidence_stays_und` |
| Library setting changes (`preferred_audio_language` flips from `en` to `ar`) | The next select_track run produces a different decision; old `track_selection_decisions` row is replaced via `ON CONFLICT (video_id) DO UPDATE`. Existing transcripts are not invalidated; the next extract is queued for the new track. | `test_stage_idempotent_on_replay` (run with changed settings) |
| `preferred_audio_language` is a 639-2/B alias (`fre` instead of `fra`) | Normalized to `fra` before comparison. | `test_normalize_639_2b_aliases` + `test_select_prefers_user_language` |
| `preferred_audio_language = "ar-SA"` (BCP-47 with region) | Normalized to `ara`; matches all Arabic individuals via `is_arabic`. | `test_normalize_bcp47_strips_region` |
| Probe wrote `disposition` as NULL (older row) | `disposition or {}` defends; filters return False. | constructor default in `_track` test factory |
| Re-probe rewrites tracks with new ids | Selection is keyed on (language, channels, default, index) — re-probe yields the same key, so the same rule fires; ids change but the chosen track is the same physical stream. The `track_selection_decisions` row is rewritten by `ON CONFLICT`. | story §Edge cases (determinism) + `test_select_determinism_across_reorder` |
| Track index changes after a remux (someone rewrote the file) | `videos.content_hash` changed → it's a different `videos` row from the scanner's perspective; old row is orphaned and selection runs fresh against the new row. No special handling here. | Story 1.2 owns this transition |
| Layer-2 langid model is missing on this host | `ctx.langid` raises `ModelNotInstalled`; `maybe_detect_language` catches and returns `(None, None)`; selection proceeds with `und`. | `test_langid_handles_ffmpeg_failure` (parametrized for the model-missing branch) |

## 11. Dependencies

| Dep | Version | Why this one |
|---|---|---|
| `langcodes` | 3.4.x | Python-native ISO 639 + BCP-47 normalization. Pure-Python fallback works without the optional `language_data` package; we install the latter so 639-2/B aliases (`fre`/`fra`, `ger`/`deu`) round-trip. License: MIT. |
| `whisper-cpp-python` | 0.2.x | CPU-only inference for layer-2 lang-id. No model warmup, ~50 MB tiny multilingual model, language detection without full transcription. License: MIT (binding) + MIT (whisper.cpp). |
| `golang.org/x/text/language` | latest | Go side BCP-47 parsing. Standard library extension, already vendored across the API. |
| `github.com/google/uuid` | already in `api/go.mod` | Path param parsing. |

**Considered and rejected:**
- `iso639-lang` (Python) — no BCP-47 support, no individual-language tables for Arabic.
- `pycld3` — Google's CLD3 doesn't read PCM directly; would need a wrapper, and its language set is over-broad (script-level rather than spoken-language-level) for our purposes.
- A from-scratch ISO 639 table — unnecessary maintenance burden when `langcodes` already maintains it.

## 12. Acceptance checklist

Before this story is marked done:

**Code — Python**
- [ ] `pipeline/src/maktaba_pipeline/media/track_selection.py` exposes `AudioTrackRow`, `LibrarySettings`, `SelectionDecision`, `select_tracks`.
- [ ] `pipeline/src/maktaba_pipeline/media/iso639.py` exposes `to_iso639_3`, `is_arabic`.
- [ ] `pipeline/src/maktaba_pipeline/media/track_filters.py` exposes `is_commentary`, `is_descriptive`, `DESCRIPTIVE_TITLE_RE`.
- [ ] `pipeline/src/maktaba_pipeline/media/langid_probe.py` exposes `maybe_detect_language`.
- [ ] `pipeline/src/maktaba_pipeline/pipeline/stages/select_track.py` registers `SelectTrackStage` between `probe` and `extract` in `runner.py`.
- [ ] `langcodes>=3.4` and `whisper-cpp-python>=0.2` added to `pyproject.toml`; lockfile updated.

**Code — Go**
- [ ] `api/internal/tracks/preview.go` exposes `Handler.GetTracks`.
- [ ] `api/internal/tracks/override.go` exposes `Handler.SetOverride`.
- [ ] `pipeline/internal/iso639` mirrors the Python normalizer; cross-language parity test passes.

**Database**
- [ ] `shared/db/migrations/0009_audio_tracks_extensions.sql` applies cleanly on a fresh prior schema; `goose down` reverts cleanly (tested in CI).
- [ ] `audio_tracks.detected_language` column accepts only 3-letter lowercase ISO 639 codes via `audio_tracks_detected_lang_format_chk`.
- [ ] `audio_tracks.detected_language_confidence` is in [0,1] via `audio_tracks_detected_lang_conf_range_chk`.
- [ ] `track_selection_decisions` has a row per video after `select_track` runs; re-running replaces the row.
- [ ] `shared/db/queries/track_selection.sql` generates Go and Python clients via sqlc.

**Behaviour (story acceptance criteria)**
- [ ] AC #1: All four priority rules in the story map to test cases that pass.
- [ ] AC #2: `multi_audio=True` enqueues one `extract` job per non-commentary track; verified by `test_stage_enqueues_n_jobs_for_multi_audio_lib`.
- [ ] AC #3: `und` + `pcm` track in an Arabic-preferring library is still selected; verified by `test_select_und_pcm_track_when_arabic_preferred`.
- [ ] All test cases listed in the story (`test_select_*`) have a corresponding pytest that passes.

**Multi-track extraction**
- [ ] Each enqueued extract job has `extra->>'audio_track_id'` set; the extract worker reads `audio_tracks.index` (not `id`) from that row before invoking ffmpeg.
- [ ] `transcripts.audio_track_id` is set per track; multiple transcripts for one video are supported by the existing unique key `(video_id, audio_track_id, backend, model)`.
- [ ] Cancelling one of the per-track extract jobs does not affect siblings.

**Per-track language detection**
- [ ] Layer-2 langid is gated on `library.settings.langid_undetermined`; default `false`.
- [ ] When enabled, only `und`-tagged tracks with `detected_language IS NULL` are sampled; results are persisted; the next run is a no-op.
- [ ] `langid_confidence_threshold` (default 0.6) gates whether the detected language counts toward selection priority.

**Performance**
- [ ] `select_tracks` benchmark on 32 tracks runs in < 100 µs (pure function; no I/O).
- [ ] `maybe_detect_language` runtime per `und` track is under 800 ms p95 on the standard CI runner (one ffmpeg seek + 30 s decode + tiny model inference).
- [ ] No allocations in the hot path of `to_iso639_3` for cache hits (`functools.lru_cache`).

**Docs**
- [ ] `specs/epics/02-audio-extraction/README.md` ticks story 2.2.
- [ ] Module docstring in `track_selection.py` documents the priority list and tie-breaker rule.
- [ ] `pipeline/internal/iso639/doc.go` documents the Python parity contract and the parity-test fixture path.

**Operational**
- [ ] INFO log line `track_selection video_id=… rule=… selected=[…] rejected=[{id,reason},…]` is structured (matches the JSON shape used by the rest of `pipeline/`).
- [ ] WARN log line `track_selection_fallback_after_filter` is emitted when every track was filtered.
- [ ] Metric `track_selection_decisions_total{rule="…"}` is incremented per decision; surfaces "how often is the user overriding selection?" without a query.
