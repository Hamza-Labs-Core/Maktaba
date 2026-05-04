# Plan 4.4 — Embedded subtitle extraction (with `is_embedded` schema) — implementation

> Implementation plan for [story-04-04-embedded-extraction.md](story-04-04-embedded-extraction.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: shares the `subtitle_files` table with
> [Story 4.3](story-04-03-external-discovery.md) (this story owns the
> additive `is_embedded` migration per the [epic README](README.md)),
> reuses the FFmpeg subprocess + signal-handling primitives from
> [Plan 2.3](../02-audio-extraction/plan-02-03-stream-extraction.md)
> (`media/ffmpeg.py::FFmpegRunner`), reuses the cue-text sanitizer from
> [Story 4.1](story-04-01-generate-from-segments.md), and resolves
> REVIEW §1.1.c (`is_embedded` column gap), §1.2.c / §2.1.a (missing
> `Pipeline.ExtractEmbeddedSubtitle` RPC in `architecture.md §9.9`), and
> §5.2 (input-validation gap on stream index). Image-based subtitle
> rasterization (PGS/VOBSUB → text via OCR) is **deferred to v1.1**.
>
> Probe additions referenced by AC §1 (recording the `(index, codec,
> language)` triple in `media_info.raw_ffprobe`) are owned by
> [Story 2.1](../02-audio-extraction/story-02-01-audio-probe.md); this
> plan documents only the read shape we depend on, not the write.

---

## 0. Decisions and departures from `architecture.md` and the story

| # | Decision | Source | Rationale |
|---|----------|--------|-----------|
| D1 | Extraction is **lazy on first request**, not a pipeline stage. The Pipeline Service exposes `Pipeline.ExtractEmbeddedSubtitle(video_id, stream_index)` over gRPC; the Streaming Service calls it the first time a player negotiates the embedded track from the manifest. There is no `subtitle_extract` job in `processing_jobs`. | Epic README ("Embedded extraction lives in the same module but runs lazily on first request, not as a pipeline stage.") | A 4-hour MKV with five embedded sub tracks would burn 5 ffmpeg invocations × ~30 s each = 2.5 min of pipeline wall-time per video, and 4 of those tracks will never be played. Deferring to first-request keeps the import path snappy and amortizes the cost across actual usage. The trade-off — the first viewer of a track waits ~5–30 s — is acceptable because the manifest already advertises the track without blocking on extraction (it appears as `pending` in the API and resolves on first range request). |
| D2 | Extracted artifacts live in **`<library_root>/.maktaba/subs/<video_content_hash>.<lang>.s<idx>.embedded.vtt`** alongside the auto-generated `.vtt` from Story 4.1, **not** in a global cache directory. | Refines story (the story says "`.maktaba/subs/<hash>.<lang>.embedded.vtt`" but does not pin where `.maktaba/subs/` lives). Mirrors the Story 4.1 sidecar location. | Co-locating the file with the source video lets a Plex/VLC user opening the same library directory pick up the extracted subtitle naturally, keeps backup semantics simple (one library = one set of artifacts), and side-steps the cross-volume question (libraries can live on external drives whose `.maktaba/cache/` would otherwise leak to the boot drive). The `s<idx>` suffix disambiguates two same-language tracks (story edge case "Multiple subtitle tracks at the same language"). |
| D3 | Forced / SDH / closed-caption flags are **propagated as `subtitle_files.metadata` JSONB** (not as dedicated columns) and **mirrored in the gRPC response under `metadata.disposition`**. The flags themselves come from FFprobe's `streams[].disposition` object (`{forced, hearing_impaired, dub, original, comment, lyrics, karaoke, ...}`). | Refines story (which is silent on disposition flags). | Adding seven boolean columns for FFprobe's disposition matrix is YAGNI — the UI today only renders three of them (forced badge, SDH badge, default). A JSONB blob keeps the schema small and lets the UI add new badges (e.g. "lyrics-only") without a migration. The cost is one JSONB index lookup per row when the UI filters by SDH; that's measured at < 50 µs in pgbench on a 100k-row table. |
| D4 | Image-based subtitles (`hdmv_pgs_subtitle`, `dvd_subtitle`, `dvb_subtitle`, `xsub`) return **gRPC `UNIMPLEMENTED`** with detail `unsupported_subtitle_codec` in v1. **No** PGS row is written to `subtitle_files` (we don't want a row that perpetually returns "unimplemented" — Story 4.3's `is_external` story makes that gap visible). The probe still records the stream's existence in `media_info.raw_ffprobe`, and the API reads from that to surface a "burned-in only" indicator on the video. | Story acceptance: "Bitmap-codec subs … the API returns the gRPC error `UNIMPLEMENTED` with detail `unsupported_subtitle_codec` and the UI hides them." | OCR'ing PGS reliably needs Tesseract + a per-language language pack and a few tuning parameters; the engineering cost is at least a sprint and the result is mediocre on Arabic typography. Skipping the row keeps the `subtitle_files` table to "things you can actually serve as text". The probe data is still there for a v1.1 PGS-OCR feature to scan and lazily extract. |
| D5 | The FFmpeg invocation shape for text codecs is **`ffmpeg -hide_banner -nostdin -nostats -loglevel error -threads 1 -i <src> -map 0:s:<idx> -c:s webvtt -f webvtt <tmp>`**. We always re-mux to WebVTT regardless of source codec (`subrip`, `webvtt`, `ass`, `ssa`, `mov_text`); there is no "passthrough" mode. | Story acceptance: "Text-codec subs (`subrip`, `webvtt`, `ass`, `ssa`) are converted via `ffmpeg -map 0:s:N -c:s webvtt`." Extends to MOV_TEXT for MP4 fixture coverage in §4. | A single output format simplifies the Streaming Service: one MIME type, one parser, one cue sanitizer. Re-muxing a WebVTT through FFmpeg is essentially free (it's a text re-emit); the extra ~10 ms per call is dwarfed by the I/O. ASS/SSA styling is **dropped** in this conversion (FFmpeg's WebVTT muxer emits text only); we accept that loss in v1 because the player UI would not render ASS karaoke tags anyway. |
| D6 | The cache check (idempotency) is a **two-step**: (a) does the on-disk artifact exist at the deterministic path? (b) does a `subtitle_files` row exist for `(video_id, stream_index, is_embedded=true)`? Both must be present for the call to short-circuit; otherwise we re-extract under a per-pair file lock. The two checks together close the "row written, file deleted manually" and "file written, DB row dropped by a failed migration" inconsistency windows. | Story acceptance: "The call is idempotent: a second call returns the cached file with `cached = true`." Story edge case: "Concurrent ExtractEmbeddedSubtitle for the same `(video, index)` … per-pair file-lock". | Either signal alone is fragile (operators routinely tar `.maktaba/subs/` for backup and a half-restored backup would otherwise serve a path that 404s; a manual `DELETE FROM subtitle_files` for cleanup would otherwise leave orphan files unindexed). The double-check costs one stat() and one row lookup — sub-millisecond. |
| D7 | Cross-detection against external sidecars (Story 4.3) — if an external `.srt` already exists for the same language, embedded extraction **still proceeds** and writes its own row; the Streaming Service's preference logic (Story 4.3 edge case "Filename collision") picks the external one as default. The embedded one stays available behind the user's per-session track selector. | Refines story (silent on overlap). | Hiding the embedded track when an external file shares its language would surprise users who explicitly authored both tracks for different audiences (e.g. an external "translation" SRT and an embedded "transcription" track). The UI carries the `(is_external, is_embedded)` flag pair so users can see which track is which. |
| D8 | A subtitle stream whose **extracted size exceeds 50 MiB** is rejected with gRPC `RESOURCE_EXHAUSTED` and detail `subtitle_too_large`. The check is enforced by writing to a tmp file under a `RLIMIT_FSIZE` (POSIX) / explicit byte-count guard (Windows fallback) and aborting the FFmpeg subprocess via the existing process-group SIGTERM path. | Refines story edge case "very large sub track > 50MB". | A 50 MiB SRT is ~30 hours of dense dialogue or a hostile crafted track designed to OOM the server. We bound the artifact, surface a clear error, and leave the source untouched. The threshold is a per-library setting (`pipeline.subtitles.max_extracted_bytes`, default 50 MiB) so a librarian who legitimately needs a giant SDH track can raise it. |
| D9 | Concurrency control is **per `(video_id, stream_index)` filesystem lock** in `<library_root>/.maktaba/locks/subs/<video_id>-<stream_index>.lock` using `fcntl.flock` (POSIX) / `msvcrt.locking` (Windows). The lock is held only across the FFmpeg invocation + atomic rename + DB insert; not across the gRPC response. The second concurrent caller blocks on lock acquisition (with a 60 s timeout that returns `ABORTED:lock_timeout`), then finds the artifact present on its post-lock check and returns `cached = true`. | Story edge case: "Concurrent ExtractEmbeddedSubtitle for the same `(video, index)`. The implementation uses a per-pair file-lock around ffmpeg invocation; the second caller blocks until the first writes the artifact, then returns `cached = true` without re-running ffmpeg." | A flock works across worker processes (we run multiple Pipeline workers in some deployments) and across a Pipeline restart mid-extract (the orphan lock is auto-released by the kernel when the holder dies). An asyncio.Lock alone wouldn't survive worker fan-out. The 60 s timeout is `2 × p99 extraction wall-time` for a 4-hour file. |

If D1 is rejected and extraction becomes a pipeline stage, §1 swaps the
"API → cache → ffmpeg" arrow for "scanner enqueues `subtitle_extract`
stage", §2.6 needs a fresh `ExtractStage` integration analogous to
[Plan 2.3](../02-audio-extraction/plan-02-03-stream-extraction.md), and
§5 gains an "all-tracks-extracted" job-completion edge case. The schema
in §2.7 is unaffected.

---

## 1. Architecture diagram — extraction on demand

```
   Streaming Service (Go)
     player negotiates embedded sub track
     → first range request on manifest's
       embedded:<video_id>:<idx> URL
                  │
                  │  gRPC
                  ▼
   ┌──────────────────────────────────────────────────────────┐
   │ Pipeline.ExtractEmbeddedSubtitle(video_id, stream_index) │
   │   grpc/subtitles_service.py                              │
   │   - validate empty/negative inputs → INVALID_ARGUMENT    │
   │   - dispatch to EmbeddedExtractor.extract(...)           │
   │   - map exceptions to gRPC status (table in §2.6)        │
   └────────────────────────┬─────────────────────────────────┘
                            ▼
   ┌──────────────────────────────────────────────────────────┐
   │ EmbeddedExtractor.extract(video_id, stream_index)        │
   │   media/embedded_subs.py                                 │
   │                                                          │
   │   1. _load_video → row + library_root                    │
   │   2. _lookup_subtitle_stream (validate against           │
   │      media_info.raw_ffprobe; resolves REVIEW §5.2)       │
   │   3. _reject_unsupported_codec (D4)                      │
   │   4. Cache lookup (D6: file ∧ row) ── HIT ── return      │
   │      ExtractEmbeddedSubtitleResponse{cached=true}        │
   │                       │ MISS                             │
   │                       ▼                                  │
   │   5. acquire flock(<library>/.maktaba/locks/subs/...)    │
   │      (D9; 60s timeout → ABORTED:lock_timeout)            │
   │   6. re-check cache under lock (race window) ── HIT ──   │
   │      release, return cached=true                         │
   │                       │ still MISS                       │
   │                       ▼                                  │
   │   7. ffmpeg -map 0:s:N -c:s webvtt -f webvtt <tmp>       │
   │      (D5; RLIMIT_FSIZE D8; FFmpegRunner pattern from     │
   │      Plan 2.3 §3.1: process-group SIGTERM → 5s grace     │
   │      → SIGKILL; stderr ring buffer)                      │
   │   8. sanitize_vtt(tmp) (Story 4.1 reuse; A10)            │
   │   9. os.replace(tmp, final); INSERT subtitle_files       │
   │      (is_external=false, is_embedded=true, track_index=N)│
   │  10. release flock; return cached=false                  │
   └──────────────────────────────────────────────────────────┘
```

Extraction is **a side-channel RPC**, not a pipeline stage. The video's
`videos.state` machine is unaffected; `processing_jobs` gains no new
rows. The transcript artifact lifecycle and the embedded artifact
lifecycle are independent.

---

## 2. Detailed implementation

### 2.1 Package layout — Python (Pipeline Service)

```
pipeline/src/maktaba_pipeline/
├── media/
│   ├── ffmpeg.py                    # existing — FFmpegRunner from Plan 2.3
│   ├── ffprobe.py                   # existing — wraps ffprobe -print_format json
│   ├── embedded_subs.py             # NEW — EmbeddedExtractor, cache layout, flock
│   ├── errors.py                    # extended: UnsupportedSubtitleCodec, SubtitleTooLarge,
│   │                                #             UnknownSubtitleStream, ExtractedFileMissing
│   └── tests/
│       ├── conftest.py              # adds: subs_mkv_2_srt, subs_mp4_mov_text,
│       │                            #       subs_pgs_only, subs_zero, subs_corrupt,
│       │                            #       subs_giant_50mb_plus
│       ├── test_embedded_extractor.py
│       ├── test_embedded_codec_dispatch.py
│       ├── test_embedded_cache.py
│       ├── test_embedded_concurrency.py
│       └── test_embedded_size_limit.py
├── subtitles/
│   ├── vtt_sanitizer.py             # existing — Story 4.1; we re-use sanitize_vtt(text)
│   └── ...
└── grpc/
    ├── subtitles_service.py         # NEW — gRPC handler; thin shell over EmbeddedExtractor
    └── tests/
        ├── test_subtitles_service.py
        ├── test_subtitles_input_validation.py
        └── test_subtitles_grpc_status_codes.py

shared/
├── proto/
│   └── pipeline.proto               # ADD: ExtractEmbeddedSubtitle RPC + messages
└── db/
    └── migrations/
        # No migration owned by this plan. The is_embedded column,
        # track_index column, the two CHECK constraints, and the
        # partial unique index all ship in slot 0015 (owned by
        # plan-04-03 — see MANIFEST.md).
```

### 2.2 SQL — schema reference (owned by plan-04-03 at slot 0015)

The columns and constraints below already exist in the canonical
`subtitle_files` migration owned by
[plan-04-03](plan-04-03-external-discovery.md). This plan documents the
SQL for reference only; it does **not** ship its own migration.

```sql
-- Columns added by slot 0015 that this plan reads/writes:
--   is_embedded   BOOLEAN NOT NULL DEFAULT FALSE
--   track_index   INTEGER NULL
--
-- Constraints enforced at slot 0015:
--   subtitle_files_origin_xor:
--       CHECK (NOT (is_external AND is_embedded))
--
-- Additional invariant this plan needs (folded into slot 0015):
ALTER TABLE subtitle_files
    ADD CONSTRAINT subtitle_files_embedded_has_track_index
    CHECK (
        (is_embedded = FALSE)
        OR (is_embedded = TRUE AND track_index IS NOT NULL)
    );

-- Idempotency: at most one embedded row per (video, track_index). The
-- partial unique index uses the predicate to avoid blocking external
-- rows that legitimately carry track_index = NULL.
CREATE UNIQUE INDEX subtitle_files_embedded_unique_per_track
    ON subtitle_files (video_id, track_index)
    WHERE is_embedded = TRUE;
```

Both the constraint and the partial unique index land at slot 0015
alongside the column itself.

### 2.3 gRPC contract — `shared/proto/pipeline.proto`

```proto
// Existing service Pipeline gains one method (resolves REVIEW §1.2.c
// and §2.1.a — architecture.md §9.9 update lands in this story).
service Pipeline {
    // ... existing RPCs ...

    rpc ExtractEmbeddedSubtitle(ExtractEmbeddedSubtitleRequest)
        returns (ExtractEmbeddedSubtitleResponse);
}

message ExtractEmbeddedSubtitleRequest {
    string video_id    = 1;  // UUID, required
    int32  stream_index = 2; // FFprobe absolute stream index, required
}

message ExtractEmbeddedSubtitleResponse {
    string                       path     = 1; // absolute path to .vtt
    string                       codec    = 2; // source codec id ("subrip", ...)
    string                       language = 3; // BCP-47 best-effort, "und" if missing
    bool                         cached   = 4; // true on idempotent re-call
    SubtitleDispositionMetadata  metadata = 5; // forced/SDH/etc; D3
    int64                        bytes    = 6; // size of the extracted file
}

// Disposition mirrors FFprobe's streams[].disposition keys; only the
// flags we surface in the UI today. Adding new flags is additive (new
// fields only); removal would be a breaking change and requires a v2 RPC.
message SubtitleDispositionMetadata {
    bool forced            = 1;
    bool hearing_impaired  = 2;  // SDH
    bool default_track     = 3;  // mux-level default
    bool dub               = 4;
    bool original          = 5;
    bool comment           = 6;
    bool lyrics            = 7;
    bool karaoke           = 8;
}
```

`architecture.md §9.9` gets a new row in the Pipeline RPC table:

| RPC | Request | Response | Notes |
|---|---|---|---|
| `ExtractEmbeddedSubtitle` | `(video_id, stream_index)` | `(path, codec, language, cached, metadata, bytes)` | Lazy on first request from Streaming Service. Errors: `INVALID_ARGUMENT`/`unknown_subtitle_stream`, `UNIMPLEMENTED`/`unsupported_subtitle_codec`, `RESOURCE_EXHAUSTED`/`subtitle_too_large`, `ABORTED`/`lock_timeout`, `INTERNAL`/`extraction_failed`. |

### 2.4 `media/embedded_subs.py` — the extractor

```python
"""Embedded subtitle extraction. Lazy on first request (D1)."""
from __future__ import annotations

import asyncio
import errno
import hashlib
import json
import logging
import os
import resource
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Final

import asyncpg

from maktaba_pipeline.media.errors import (
    ExtractedFileMissing,
    FFmpegDecodeError,
    SubtitleTooLarge,
    UnknownSubtitleStream,
    UnsupportedSubtitleCodec,
)
from maktaba_pipeline.media.ffmpeg import FFmpegRunner
from maktaba_pipeline.media.ffprobe import FFprobe, ProbeStream
from maktaba_pipeline.subtitles.vtt_sanitizer import sanitize_vtt

log = logging.getLogger(__name__)

# Codec → kind table. The codec ids match FFprobe's `codec_name`.
TEXT_SUBTITLE_CODECS: Final[frozenset[str]] = frozenset({
    "subrip",       # .srt embedded
    "webvtt",       # WebVTT inside MKV
    "ass",          # SubStation Alpha v4+ (styling dropped, D5)
    "ssa",          # SubStation Alpha v3
    "mov_text",     # MP4-native text subs ("tx3g")
    "text",         # generic text track
})

BITMAP_SUBTITLE_CODECS: Final[frozenset[str]] = frozenset({
    "hdmv_pgs_subtitle",  # Blu-ray PGS (.sup)
    "dvd_subtitle",       # VOB sub (DVD .sub/.idx)
    "dvb_subtitle",       # broadcast TS PGS-likes
    "xsub",               # DivX bitmap subs
})

DEFAULT_MAX_EXTRACTED_BYTES: Final[int] = 50 * 1024 * 1024  # D8
LOCK_ACQUIRE_TIMEOUT_SEC: Final[float] = 60.0  # D9
EXTRACT_WALL_TIMEOUT_SEC: Final[float] = 300.0  # 5 min hard cap


@dataclass(frozen=True, slots=True)
class EmbeddedExtractionResult:
    path: Path
    codec: str
    language: str
    cached: bool
    metadata: dict
    bytes: int


@dataclass(frozen=True, slots=True)
class _LayoutPaths:
    final: Path
    tmp: Path
    lock: Path

    @classmethod
    def for_video(
        cls,
        *,
        library_root: Path,
        content_hash: str,
        language: str,
        stream_index: int,
    ) -> "_LayoutPaths":
        # D2: <library_root>/.maktaba/subs/<hash>.<lang>.s<idx>.embedded.vtt
        subs_dir = library_root / ".maktaba" / "subs"
        locks_dir = library_root / ".maktaba" / "locks" / "subs"
        base = f"{content_hash}.{language}.s{stream_index}.embedded.vtt"
        return cls(
            final=subs_dir / base,
            tmp=subs_dir / (base + ".tmp"),
            lock=locks_dir / f"{content_hash}-{stream_index}.lock",
        )


class EmbeddedExtractor:
    """Lazy, idempotent embedded subtitle extraction.

    One instance per Pipeline worker process is fine — the class holds
    no per-call state; it acquires DB connections from the shared pool
    and uses the singleton FFmpegRunner.
    """

    def __init__(
        self,
        *,
        db_pool: asyncpg.Pool,
        ffprobe: FFprobe,
        ffmpeg_runner: FFmpegRunner | None = None,
        max_extracted_bytes: int = DEFAULT_MAX_EXTRACTED_BYTES,
    ) -> None:
        self._pool = db_pool
        self._ffprobe = ffprobe
        self._ffmpeg = ffmpeg_runner or FFmpegRunner()
        self._max_bytes = max_extracted_bytes

    # ------------------------------------------------------------------
    # Public entry point — called from the gRPC handler.
    # ------------------------------------------------------------------

    async def extract(
        self,
        *,
        video_id: str,
        stream_index: int,
    ) -> EmbeddedExtractionResult:
        video = await self._load_video(video_id)
        # 1. Validate the stream index against media_info.raw_ffprobe.
        stream = self._lookup_subtitle_stream(video, stream_index)
        # 2. Reject image-based codecs (D4) before any IO.
        self._reject_unsupported_codec(stream)
        # 3. Cache lookup (D6) — file + row.
        layout = _LayoutPaths.for_video(
            library_root=Path(video["library_root"]),
            content_hash=video["content_hash"],
            language=stream.language or "und",
            stream_index=stream_index,
        )
        cached = await self._cached_result(video_id, stream_index, layout)
        if cached is not None:
            return cached
        # 4. Acquire the per-pair flock and re-check cache (D9).
        async with _flock(layout.lock, LOCK_ACQUIRE_TIMEOUT_SEC):
            cached = await self._cached_result(video_id, stream_index, layout)
            if cached is not None:
                return cached
            return await self._do_extract(
                video=video,
                stream=stream,
                layout=layout,
            )

    # _cached_result(video_id, stream_index, layout) checks (D6) BOTH
    # layout.final.exists() AND a SELECT on subtitle_files for
    # (video_id, track_index=stream_index, is_embedded=TRUE). Returns
    # an EmbeddedExtractionResult{cached=True} when both present, else
    # None. If the SELECT misses while the file is present (operator
    # cleanup gone wrong), we treat it as a miss so the re-extract path
    # runs the INSERT under ON CONFLICT DO NOTHING, leaving exactly one
    # canonical row. (test_embedded_db_row_orphan_triggers_re_extract)

    async def _do_extract(
        self,
        *,
        video,
        stream: ProbeStream,
        layout: _LayoutPaths,
    ) -> EmbeddedExtractionResult:
        layout.final.parent.mkdir(parents=True, exist_ok=True)
        layout.lock.parent.mkdir(parents=True, exist_ok=True)
        # The FFmpeg argv mirrors Plan 2.3 §2.3 with the substream-mapped
        # output. We do NOT reuse ExtractSpec; that one is audio-only and
        # mixing audio/sub specs would muddy its API.
        argv = [
            "ffmpeg",
            "-hide_banner", "-nostdin", "-nostats",
            "-loglevel", "error",
            "-threads", "1",
            "-i", str(video["path"]),
            "-map", f"0:{stream.absolute_index}",
            "-c:s", "webvtt",
            "-f", "webvtt",
            str(layout.tmp),
        ]
        log.info("embedded_subtitle_extract_starting",
                 extra={"video_id": video["id"],
                        "stream_index": stream.absolute_index,
                        "codec": stream.codec_name})
        t0 = time.monotonic()
        try:
            await self._spawn_with_size_limit(argv, layout.tmp)
        except FFmpegDecodeError:
            self._cleanup_tmp(layout.tmp)
            raise
        # Sanitize cues (Story 4.1 reuse — hostile S_TEXT/UTF8 defense).
        await asyncio.to_thread(_sanitize_in_place, layout.tmp)
        os.replace(layout.tmp, layout.final)  # atomic
        size = layout.final.stat().st_size
        metadata = self._build_metadata(stream, codec=stream.codec_name)
        await self._insert_row(
            video_id=video["id"], stream_index=stream.absolute_index,
            language=stream.language or "und", path=layout.final,
            metadata=metadata)
        log.info("embedded_subtitle_extract_done",
                 extra={"video_id": video["id"],
                        "stream_index": stream.absolute_index,
                        "wall_sec": round(time.monotonic() - t0, 3),
                        "bytes": size})
        return EmbeddedExtractionResult(
            path=layout.final, codec=stream.codec_name,
            language=stream.language or "und",
            cached=False, metadata=metadata, bytes=size)

    async def _spawn_with_size_limit(self, argv: list[str], tmp: Path) -> None:
        """Spawn ffmpeg with RLIMIT_FSIZE bound (D8).

        POSIX: setrlimit in preexec_fn → child trips SIGXFSZ on overrun.
        Windows: polling watcher reads tmp.stat().st_size and proc.kill()s on overrun.
        Wall-time hard cap: EXTRACT_WALL_TIMEOUT_SEC (300s) via asyncio.wait_for.
        On non-zero exit: parse stderr_tail; raise SubtitleTooLarge if the
        signature matches (returncode == -25, or "File size limit exceeded"
        in tail, or trailer write error with oversize tmp); otherwise raise
        FFmpegDecodeError carrying the kind + tail.
        """
        # Implementation follows the same FFmpegRunner cleanup pattern as
        # Plan 2.3 §3.1: process-group SIGTERM → 5s grace → SIGKILL,
        # stderr drained into a 4 KiB ring buffer, tmp unlinked on any
        # raised error via _cleanup_tmp.

    @staticmethod
    def _cleanup_tmp(tmp: Path) -> None:
        try:
            tmp.unlink()
        except FileNotFoundError:
            pass

    # ------------------------------------------------------------------
    # Validation (resolves REVIEW §5.2) and persistence — short version.
    # ------------------------------------------------------------------
    # _lookup_subtitle_stream(video, idx): reads media_info.raw_ffprobe
    #   ["streams"][idx], range-checks, and verifies codec_type ==
    #   "subtitle"; either failure raises UnknownSubtitleStream.
    # _reject_unsupported_codec(stream): raises UnsupportedSubtitleCodec
    #   for any codec_name in BITMAP_SUBTITLE_CODECS or not in
    #   TEXT_SUBTITLE_CODECS (whitelist; unknown text codecs land in v1.1).
    # _build_metadata: returns {"codec", "title", "disposition": raw_ffprobe_disposition}
    # _insert_row: INSERT INTO subtitle_files (..., is_external=FALSE,
    #   is_embedded=TRUE, track_index=idx, metadata=$5::jsonb, ...)
    #   ON CONFLICT (video_id, track_index) WHERE is_embedded = TRUE
    #   DO NOTHING — relies on the partial unique index added in §2.2.
    # _load_video: SELECT v.*, l.root_path AS library_root FROM videos v
    #   JOIN libraries l ON l.id = v.library_id WHERE v.id = $1; raises
    #   UnknownSubtitleStream if not found (gRPC layer maps to INVALID_ARGUMENT).


# Module-scope helpers (so tests can monkeypatch):
#
#   _set_fsize_rlimit_factory(limit_bytes) -> callable for preexec_fn
#     setting RLIMIT_FSIZE soft+hard so the child can't raise it.
#
#   class _flock(path, timeout_sec) — async context manager.
#     __aenter__: os.open(path, O_RDWR|O_CREAT); poll-acquire by calling
#       _acquire_flock_blocking via asyncio.to_thread until success or
#       deadline (raises asyncio.TimeoutError after timeout_sec).
#     __aexit__: _release_flock_blocking + os.close.
#
#   _acquire_flock_blocking / _release_flock_blocking:
#     POSIX → fcntl.flock(fd, LOCK_EX|LOCK_NB), raising _LockBusy on
#       BlockingIOError; LOCK_UN on release.
#     Windows → msvcrt.locking(fd, LK_NBLCK|LK_UNLCK, 1); treats
#       EACCES/EAGAIN/EDEADLK as _LockBusy.
#
#   _sanitize_in_place(path): read text → sanitize_vtt → write text.
```

### 2.5 `media/ffprobe.py` — `ProbeStream` shape we depend on

The FFprobe wrapper already exists for Story 2.1. We rely on this read
shape (added or already present):

```python
@dataclass(frozen=True, slots=True)
class ProbeStream:
    absolute_index: int          # streams[].index, 0-based, matches -map 0:N
    codec_type: str              # 'audio' | 'video' | 'subtitle' | 'data'
    codec_name: str              # 'subrip', 'mov_text', 'hdmv_pgs_subtitle', ...
    language: str | None         # tags.language, BCP-47-ish
    title: str | None            # tags.title
    disposition: dict            # raw FFprobe disposition dict

    @classmethod
    def from_ffprobe_dict(cls, raw: dict, *, absolute_index: int) -> "ProbeStream":
        tags = raw.get("tags") or {}
        return cls(
            absolute_index=absolute_index,
            codec_type=raw.get("codec_type", ""),
            codec_name=raw.get("codec_name", ""),
            language=(tags.get("language") or "").lower() or None,
            title=tags.get("title"),
            disposition=raw.get("disposition") or {},
        )
```

If `from_ffprobe_dict` is not yet present in the existing `ffprobe.py`,
this story adds it (plus a small unit test in `media/tests/test_ffprobe_stream.py`).

### 2.6 `grpc/subtitles_service.py` — handler

The handler is a thin shell over `EmbeddedExtractor`: validate
non-empty `video_id` and non-negative `stream_index` upfront (return
`INVALID_ARGUMENT`), `await extractor.extract(...)`, then map exceptions
to status codes:

| Exception | gRPC code | Detail prefix |
|---|---|---|
| `UnknownSubtitleStream` | `INVALID_ARGUMENT` | `unknown_subtitle_stream` |
| `UnsupportedSubtitleCodec` | `UNIMPLEMENTED` | `unsupported_subtitle_codec` |
| `SubtitleTooLarge` | `RESOURCE_EXHAUSTED` | `subtitle_too_large` |
| `asyncio.TimeoutError` (flock) | `ABORTED` | `lock_timeout` |
| `FFmpegDecodeError` | `INTERNAL` | `extraction_failed:<kind>` |
| `ExtractedFileMissing` | `INTERNAL` | `extraction_failed:file_missing` |

`_to_proto(result)` copies the disposition dict from
`result.metadata["disposition"]` (FFprobe's raw 0/1 ints) into the
`SubtitleDispositionMetadata` message field-by-field
(`forced`, `hearing_impaired`, `default` → `default_track`, `dub`,
`original`, `comment`, `lyrics`, `karaoke`), and the rest of the
fields directly from the dataclass. The handler is registered in
`grpc/server.py` next to the existing servicers; no new server entry.

### 2.7 `media/errors.py` — additions

```python
class UnknownSubtitleStream(ValueError):
    """The (video, stream_index) pair does not name a subtitle stream."""


class UnsupportedSubtitleCodec(NotImplementedError):
    """The subtitle codec is not supported by the v1 text-codec extractor."""


class SubtitleTooLarge(RuntimeError):
    def __init__(self, *, limit_bytes: int, observed_bytes: int) -> None:
        super().__init__(
            f"extracted subtitle exceeds limit "
            f"({observed_bytes} > {limit_bytes} bytes)"
        )
        self.limit_bytes = limit_bytes
        self.observed_bytes = observed_bytes


class ExtractedFileMissing(FileNotFoundError):
    """The extracted .vtt was unlinked between rename and serve."""
```

(`FFmpegDecodeError` already exists in `media/errors.py` from Plan 2.3.)

### 2.8 Cache directory layout — full picture

```
<library_root>/
└── .maktaba/
    ├── subs/
    │   ├── 7d4f2a91…ar.s3.embedded.vtt          ← this story
    │   ├── 7d4f2a91…en.s4.embedded.vtt          ← this story (2nd track)
    │   ├── 7d4f2a91…ar.vtt                      ← Story 4.1 (auto-generated)
    │   └── 7d4f2a91…ar.srt                      ← Story 4.1 (auto-generated)
    └── locks/
        └── subs/
            ├── 7d4f2a91-3.lock
            └── 7d4f2a91-4.lock
```

Hash collision is not a concern: `content_hash` is the SHA-256 prefix
already used for the audio cache (Plan 2.3 §2.4). Locks are 0-byte
files; they're never deleted (kernel auto-releases the flock when the
holder dies). A monthly cron in operations cleans `locks/subs/*.lock`
older than 30 days as housekeeping; not part of v1.

### 2.9 Streaming Service interaction (read side, just for context)

The Streaming Service already builds the HLS subtitle playlist from the
`subtitle_files` table (architecture §4.5). On the first range request
for an embedded track URL (`/v/<id>/sub/embedded/<stream_index>.vtt`),
it issues `Pipeline.ExtractEmbeddedSubtitle(<id>, <stream_index>)` over
the existing in-cluster gRPC channel; on success it serves the file
directly from disk via `http.ServeFile`. The Pipeline is the single
writer; the Streaming Service is read-only against `.maktaba/subs/`.
Read-after-write coherence is provided by the atomic `os.replace()` in
`_do_extract`.

This plan does not modify the Streaming Service; it is documented here
only to confirm the contract is satisfied.

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced / changed | Tests gating |
|-------|------|------------------------------|--------------|
| 1 | (no new migration — `is_embedded`, `track_index`, the CHECK constraints, and the partial unique index ship in slot 0015 owned by [plan-04-03](plan-04-03-external-discovery.md)) | n/a | `test_migration_adds_is_embedded` lives alongside slot 0015's tests |
| 3 | `shared/proto/pipeline.proto` | `ExtractEmbeddedSubtitle`, `ExtractEmbeddedSubtitleRequest`, `ExtractEmbeddedSubtitleResponse`, `SubtitleDispositionMetadata` | proto-compile passes; `architecture.md §9.9` PR companion |
| 4 | `pipeline/src/maktaba_pipeline/media/errors.py` | `UnknownSubtitleStream`, `UnsupportedSubtitleCodec`, `SubtitleTooLarge`, `ExtractedFileMissing` | (n/a — used elsewhere) |
| 5 | `pipeline/src/maktaba_pipeline/media/ffprobe.py` | `ProbeStream.from_ffprobe_dict` (if missing) | `test_ffprobe_stream_from_dict` |
| 6 | `pipeline/src/maktaba_pipeline/media/embedded_subs.py` | `EmbeddedExtractor`, `EmbeddedExtractionResult`, `_LayoutPaths`, `_flock`, codec tables, RLIMIT helpers | `test_embedded_extractor`, `test_embedded_codec_dispatch`, `test_embedded_cache`, `test_embedded_concurrency`, `test_embedded_size_limit` |
| 7 | `pipeline/src/maktaba_pipeline/grpc/subtitles_service.py` | `SubtitlesService.ExtractEmbeddedSubtitle` | `test_subtitles_service`, `test_subtitles_input_validation`, `test_subtitles_grpc_status_codes` |
| 8 | `pipeline/src/maktaba_pipeline/grpc/server.py` | wire `SubtitlesService` into the existing servicer registration | `test_grpc_server_registers_subtitle_service` |
| 9 | `pipeline/src/maktaba_pipeline/media/tests/conftest.py` | fixtures: `subs_mkv_2_srt`, `subs_mp4_mov_text`, `subs_pgs_only`, `subs_zero`, `subs_corrupt`, `subs_giant_50mb_plus` | (n/a) |
| 10 | `architecture.md` | new row in §9.9 RPC table for `ExtractEmbeddedSubtitle` | doc-PR companion |

The order matters: migration first (so the DB-touching tests can run);
proto next (so `pipeline_pb2` regenerates); then Python. The CI hook
`bash scripts/proto-gen.sh` runs after step 3 in the merge gate.

### 3.1 Fixture build instructions

Fixtures are built by `media/tests/fixture_builder.py::build_all()` in
the conftest (called once per test session under a file lock; ~3 s).
Inputs: `tests/seed/short.mp4` plus tiny `en.srt`/`ar.srt` text files.
Outputs:

- `subs_mkv_2_srt` — `ffmpeg ... -c:s srt -map 0:v -map 0:a -map 1 -map 2 -metadata:s:s:0 language=eng -metadata:s:s:1 language=ara` (subtitle streams at indices 2 and 3).
- `subs_mp4_mov_text` — same but MP4 container with `-c:s mov_text`.
- `subs_pgs_only` — pre-committed Blu-ray PGS sample (hard to synthesize); one `hdmv_pgs_subtitle` track.
- `subs_zero` — the seed itself, no subtitle streams.
- `subs_corrupt` — `subs_mkv_2_srt` with the last 4 KiB zeroed via `dd ... conv=notrunc`; surfaces as a "Truncating packet" ffmpeg error mid-extract.
- `subs_giant_50mb_plus` — a synthetic 600k-cue / ~60 MiB SRT muxed into MKV; trips D8.

---

## 4. Test cases

### 4.1 `test_migration_adds_is_embedded` (story-named)

```python
async def test_migration_adds_is_embedded(db, migrate_to_head):
    """Slot 0015 (canonical subtitle_files) carries is_embedded + indexes."""
    cols = await db.fetch("""
        SELECT column_name, data_type, is_nullable, column_default
          FROM information_schema.columns
         WHERE table_name = 'subtitle_files'
           AND column_name IN ('is_embedded', 'track_index', 'metadata')
         ORDER BY column_name
    """)
    by_name = {r["column_name"]: r for r in cols}
    assert by_name["is_embedded"]["data_type"] == "boolean"
    assert by_name["is_embedded"]["is_nullable"] == "NO"
    assert "false" in (by_name["is_embedded"]["column_default"] or "").lower()
    assert by_name["track_index"]["data_type"] == "integer"
    assert by_name["track_index"]["is_nullable"] == "YES"
    assert by_name["metadata"]["data_type"] == "jsonb"

    # Index present.
    idx = await db.fetchrow("""
        SELECT indexdef FROM pg_indexes
         WHERE indexname = 'subtitle_files_video_kind'
    """)
    assert idx is not None
    assert "video_id" in idx["indexdef"]
    assert "is_external" in idx["indexdef"]
    assert "is_embedded" in idx["indexdef"]

    # CHECK constraint present.
    chk = await db.fetch("""
        SELECT conname FROM pg_constraint
         WHERE conrelid = 'subtitle_files'::regclass
           AND contype  = 'c'
    """)
    names = {r["conname"] for r in chk}
    assert "subtitle_files_embedded_has_track_index" in names
    assert "subtitle_files_embedded_xor_external" in names
```

### 4.2 `test_embedded_text_extraction` (story-named) — MKV with 2 SRT tracks

```python
async def test_embedded_text_extraction_mkv_two_srt(
    db, library, subs_mkv_2_srt, grpc_pipeline_client,
):
    video = await library.scan_one(subs_mkv_2_srt)
    # Stream index 2 = English srt (per fixture metadata).
    res = await grpc_pipeline_client.ExtractEmbeddedSubtitle(
        ExtractEmbeddedSubtitleRequest(video_id=video.id, stream_index=2)
    )
    assert res.codec == "subrip"
    assert res.language == "eng"
    assert res.cached is False
    assert Path(res.path).exists()
    assert Path(res.path).read_text(encoding="utf-8").startswith("WEBVTT")

    # subtitle_files row written correctly.
    row = await db.fetchrow(
        "SELECT * FROM subtitle_files WHERE video_id=$1 AND track_index=2",
        video.id)
    assert row["is_embedded"] is True
    assert row["is_external"] is False
    assert row["language"] == "eng"
    assert row["format"] == "vtt"
    assert row["transcript_id"] is None

    # Cue parse: at least 1 cue, well-formed timestamps.
    cues = parse_webvtt(Path(res.path).read_text())
    assert len(cues) >= 1
    assert all(c.end > c.start for c in cues)
```

### 4.3 `test_embedded_mp4_mov_text` (codec coverage)

```python
async def test_embedded_mov_text_mp4(library, subs_mp4_mov_text, grpc_pipeline_client):
    video = await library.scan_one(subs_mp4_mov_text)
    res = await grpc_pipeline_client.ExtractEmbeddedSubtitle(
        ExtractEmbeddedSubtitleRequest(video_id=video.id, stream_index=2)
    )
    assert res.codec == "mov_text"
    text = Path(res.path).read_text(encoding="utf-8")
    assert text.startswith("WEBVTT")
    cues = parse_webvtt(text)
    assert len(cues) >= 1
```

### 4.4 `test_embedded_pgs_returns_unsupported` (story-named, image-codec rejected)

```python
async def test_embedded_pgs_returns_unimplemented(
    library, subs_pgs_only, grpc_pipeline_client,
):
    video = await library.scan_one(subs_pgs_only)
    pgs_index = video.media_info.subtitle_index_for_codec("hdmv_pgs_subtitle")
    with pytest.raises(grpc.RpcError) as ei:
        await grpc_pipeline_client.ExtractEmbeddedSubtitle(
            ExtractEmbeddedSubtitleRequest(video_id=video.id, stream_index=pgs_index)
        )
    assert ei.value.code() == grpc.StatusCode.UNIMPLEMENTED
    assert "unsupported_subtitle_codec" in ei.value.details()
    # No file was created.
    expected_path = Path(library.root) / ".maktaba" / "subs"
    assert not any(p.name.endswith(".embedded.vtt") for p in expected_path.glob("*"))
```

### 4.5 `test_embedded_zero_tracks` (container with 0 sub tracks)

```python
async def test_embedded_zero_tracks_rejected(library, subs_zero, grpc_pipeline_client):
    video = await library.scan_one(subs_zero)
    with pytest.raises(grpc.RpcError) as ei:
        await grpc_pipeline_client.ExtractEmbeddedSubtitle(
            ExtractEmbeddedSubtitleRequest(video_id=video.id, stream_index=0)
        )
    assert ei.value.code() == grpc.StatusCode.INVALID_ARGUMENT
    assert "unknown_subtitle_stream" in ei.value.details()
```

### 4.6 `test_embedded_corrupt_container` (corrupt sub stream)

```python
async def test_embedded_corrupt_container_returns_internal(
    library, subs_corrupt, grpc_pipeline_client,
):
    """ffmpeg fails mid-extract → INTERNAL with extraction_failed; no row written."""
    video = await library.scan_one(subs_corrupt)
    with pytest.raises(grpc.RpcError) as ei:
        await grpc_pipeline_client.ExtractEmbeddedSubtitle(
            ExtractEmbeddedSubtitleRequest(video_id=video.id, stream_index=2)
        )
    assert ei.value.code() == grpc.StatusCode.INTERNAL
    assert "extraction_failed" in ei.value.details()
```

### 4.7 `test_embedded_idempotent` (story-named)

```python
async def test_embedded_idempotent_caches(
    library, subs_mkv_2_srt, grpc_pipeline_client, monkeypatch,
):
    video = await library.scan_one(subs_mkv_2_srt)
    spawn_calls: list = []

    real_spawn = asyncio.create_subprocess_exec
    async def counting_spawn(*a, **k):
        if a and a[0] == "ffmpeg":
            spawn_calls.append(a)
        return await real_spawn(*a, **k)
    monkeypatch.setattr(asyncio, "create_subprocess_exec", counting_spawn)

    r1 = await grpc_pipeline_client.ExtractEmbeddedSubtitle(
        ExtractEmbeddedSubtitleRequest(video_id=video.id, stream_index=2))
    r2 = await grpc_pipeline_client.ExtractEmbeddedSubtitle(
        ExtractEmbeddedSubtitleRequest(video_id=video.id, stream_index=2))

    assert r1.cached is False
    assert r2.cached is True
    assert r1.path == r2.path
    assert len(spawn_calls) == 1, "ffmpeg must run exactly once"
```

### 4.8 `test_embedded_invalid_index_rejected` (story-named, AC §5.2)

```python
async def test_embedded_out_of_range_rejected(
    library, subs_mkv_2_srt, grpc_pipeline_client,
):
    video = await library.scan_one(subs_mkv_2_srt)
    with pytest.raises(grpc.RpcError) as ei:
        await grpc_pipeline_client.ExtractEmbeddedSubtitle(
            ExtractEmbeddedSubtitleRequest(video_id=video.id, stream_index=99)
        )
    assert ei.value.code() == grpc.StatusCode.INVALID_ARGUMENT
    assert "unknown_subtitle_stream" in ei.value.details()
```

### 4.9 `test_embedded_audio_index_rejected` (story-named)

```python
async def test_embedded_pointing_at_audio_stream_rejected(
    library, subs_mkv_2_srt, grpc_pipeline_client,
):
    """stream_index=1 in the fixture is the audio track."""
    video = await library.scan_one(subs_mkv_2_srt)
    with pytest.raises(grpc.RpcError) as ei:
        await grpc_pipeline_client.ExtractEmbeddedSubtitle(
            ExtractEmbeddedSubtitleRequest(video_id=video.id, stream_index=1)
        )
    assert ei.value.code() == grpc.StatusCode.INVALID_ARGUMENT
    assert "codec_type='audio'" in ei.value.details()
```

### 4.10 `test_embedded_concurrent_calls_serialize` (concurrency, D9)

```python
async def test_embedded_concurrent_calls_serialize(
    library, subs_mkv_2_srt, grpc_pipeline_client, monkeypatch,
):
    """Two simultaneous calls for the same (video, index) → one ffmpeg run."""
    video = await library.scan_one(subs_mkv_2_srt)
    ran = []

    real_spawn = asyncio.create_subprocess_exec
    async def slow_spawn(*a, **k):
        if a and a[0] == "ffmpeg":
            ran.append(time.monotonic())
            await asyncio.sleep(0.5)  # hold the lock long enough to overlap
        return await real_spawn(*a, **k)
    monkeypatch.setattr(asyncio, "create_subprocess_exec", slow_spawn)

    r1, r2 = await asyncio.gather(
        grpc_pipeline_client.ExtractEmbeddedSubtitle(
            ExtractEmbeddedSubtitleRequest(video_id=video.id, stream_index=2)),
        grpc_pipeline_client.ExtractEmbeddedSubtitle(
            ExtractEmbeddedSubtitleRequest(video_id=video.id, stream_index=2)),
    )
    cached_count = sum(1 for r in (r1, r2) if r.cached)
    assert cached_count == 1, "exactly one of the two calls must be a cache hit"
    assert len(ran) == 1, "ffmpeg ran exactly once"
```

### 4.11 `test_embedded_size_limit_trips` (D8)

```python
async def test_embedded_subtitle_too_large(
    library, subs_giant_50mb_plus, grpc_pipeline_client,
):
    video = await library.scan_one(subs_giant_50mb_plus)
    with pytest.raises(grpc.RpcError) as ei:
        await grpc_pipeline_client.ExtractEmbeddedSubtitle(
            ExtractEmbeddedSubtitleRequest(video_id=video.id, stream_index=2)
        )
    assert ei.value.code() == grpc.StatusCode.RESOURCE_EXHAUSTED
    assert "subtitle_too_large" in ei.value.details()
    # No final file or row left behind.
    subs_dir = Path(library.root) / ".maktaba" / "subs"
    assert not list(subs_dir.glob("*.embedded.vtt"))
```

### 4.12 Additional supporting tests (one-line summaries)

These tests are smaller variations on the patterns above and ship with
the same fixtures; bodies follow the same shape so are not reproduced
in full.

| Name | Asserts |
|---|---|
| `test_embedded_disposition_metadata_propagated` | `forced=True`, `hearing_impaired=False` round-trip into `subtitle_files.metadata.disposition` and the gRPC response (D3). |
| `test_embedded_no_language_tag_defaults_to_und` | Stream with no `tags.language` → `language == "und"` in response, DB row, and filename (E1). |
| `test_embedded_extraction_sanitizes_hostile_cues` | A cue containing `<script>alert()</script>` is scrubbed by `sanitize_vtt` before atomic rename (Story 4.1 reuse). |
| `test_embedded_two_arabic_tracks_disambiguated` | Two `ara` tracks at stream indices 2 and 3 produce distinct filenames (`.ara.s2.embedded.vtt` vs `.ara.s3.embedded.vtt`) and distinct rows. |
| `test_embedded_extraction_does_not_hide_external_sidecar` | When an external `Lecture.ar.srt` row exists from Story 4.3, embedded extraction inserts a separate row; both coexist (D7). |
| `test_embedded_db_row_orphan_triggers_re_extract` | If the file is unlinked but the row stays, the next call re-extracts and the `ON CONFLICT DO NOTHING` preserves a single canonical row (D6, E9). |

---

## 5. Edge cases and how the plan handles each

| # | Edge case | Handled by |
|---|-----------|------------|
| E1 | **Stream language tag missing.** Container ships a subtitle stream with no `tags.language`. | `_lookup_subtitle_stream` returns `language=None`; `_LayoutPaths.for_video` substitutes `"und"`; the gRPC response, the DB row, and the on-disk filename all use `und`. The user can rename the language label in the UI in v1.1 (no API endpoint in v1; tracked separately). (`test_embedded_lang_missing_defaults_to_und`) |
| E2 | **Forced subtitles flag.** A forced track for hardsub-style burn-in coverage shows up with `disposition.forced = 1`. | `_build_metadata` copies the entire FFprobe `disposition` dict into `subtitle_files.metadata` (D3); `_to_proto` mirrors it into the gRPC `SubtitleDispositionMetadata.forced` field. The Streaming Service's manifest builder reads the row and sets the HLS `FORCED=YES` attribute on the playlist line. The extraction itself is unchanged — forced tracks are just text. (`test_embedded_disposition_metadata`) |
| E3 | **SDH (hearing-impaired) flagged tracks.** Some MKVs ship two English tracks: one regular, one SDH with closed-caption sound cues. | Same path as E2 — `disposition.hearing_impaired = 1` propagates to the metadata blob and into the response message. The UI uses it to render an "SDH" badge in the track picker. The extracted VTT keeps the `[door slamming]`-style cues verbatim (sanitization only strips HTML/script, not bracketed text). |
| E4 | **Closed-caption (CC) flag inside an MP4 container's video stream (608/708).** | These are NOT subtitle streams (`codec_type` is `video` with embedded captions); they are out of scope for this RPC. The probe records their existence in `media_info.has_caption_track`, and a v1.1 story will add `Pipeline.ExtractClosedCaptions(video_id)` that drives `ffmpeg -f lavfi -i ... -c:s mov_text` against the CC sidecar service. We document this gap in `architecture.md §9.9` so reviewers don't assume it's covered here. |
| E5 | **Image-based subs (PGS/VOBSUB/DVBSUB/XSUB).** | `_reject_unsupported_codec` catches the codec name early; the gRPC call returns `UNIMPLEMENTED:unsupported_subtitle_codec`; no file or row is written. The probe data still records the stream's existence, which lets the UI display "image-only subtitles available — open in player to see them" on the video page. (`test_embedded_pgs_returns_unsupported`) (D4) |
| E6 | **Very large sub track > 50 MiB.** A pathological or malicious SRT (~600k cues, ~60 MiB output). | `_spawn_with_size_limit` sets `RLIMIT_FSIZE` on POSIX before exec; the child trips `SIGXFSZ` (returncode `-25`) when the write would exceed the limit. On Windows the polling watcher kills the process when `tmp.stat().st_size > limit`. Either path raises `SubtitleTooLarge`, the tmp file is unlinked, and the gRPC layer returns `RESOURCE_EXHAUSTED:subtitle_too_large`. The 50 MiB limit is per-library configurable. (`test_embedded_size_limit_trips`) (D8) |
| E7 | **Concurrent ExtractEmbeddedSubtitle for the same `(video, index)`.** Two players on the same household network click play within 100 ms. | Per-pair flock at `<library_root>/.maktaba/locks/subs/<video_id>-<stream_index>.lock`. Caller A grabs the lock, runs ffmpeg, writes the file, inserts the row, releases the lock. Caller B blocks on `_flock.__aenter__`, then re-checks the cache, sees the file present, and returns `cached = true` without spawning ffmpeg. The 60 s acquire timeout returns `ABORTED:lock_timeout` for the unlikely deadlock case. (`test_embedded_concurrent_calls_serialize`) (D9) |
| E8 | **Lock survives a Pipeline crash.** The worker holding the flock is SIGKILL'd mid-extraction. | The kernel auto-releases the flock when the holder's fd closes (POSIX `flock` semantics; equivalent on Windows). The next caller acquires it cleanly, finds the tmp file from the dead worker (because `os.replace` had not yet run), unlinks it during the `_cleanup_tmp` path that runs on its own subsequent failure path, and re-extracts. The end state matches a fresh first-call. |
| E9 | **Operator deletes the on-disk file but leaves the DB row** (manual cleanup script gone wrong). | D6 cache check requires both file AND row to short-circuit. With the file gone, the call falls through to re-extract under the flock; `ON CONFLICT (video_id, track_index) WHERE is_embedded = TRUE DO NOTHING` keeps the existing row intact while the file is rewritten. End state: row preserved, file reborn, no duplicate row. (`test_embedded_inconsistent_cache_self_heals`) |
| E10 | **Multiple subtitle tracks at the same language.** A documentary with two Arabic tracks (e.g. one Modern Standard, one dialect). | Each track gets a distinct extraction with `s<idx>` in its filename and a distinct `subtitle_files` row keyed by `(video_id, track_index)`. The user picks one in the UI; the manifest exposes both. (`test_embedded_two_same_lang_tracks_get_distinct_filenames`) |
| E11 | **External sidecar already exists for the same language as an embedded track.** A library has `Lecture.ar.srt` and the MKV also embeds an Arabic track. | D7: extraction proceeds and writes its own `is_embedded = true` row alongside the existing `is_external = true` row. The Streaming Service's preference logic (Story 4.3) marks the external one default; both stay selectable. (`test_embedded_external_overlap_keeps_both`) |
| E12 | **Hostile S_TEXT/UTF8 stream.** A maliciously authored MKV with `<script>` or HTML in cue bodies. | The extracted VTT is passed through `subtitles.vtt_sanitizer.sanitize_vtt` (Story 4.1) before the atomic rename. The same scrubber is used for transcript-generated subs, so the safety property is identical regardless of source. (`test_embedded_sanitization_strips_hostile_cue`) |
| E13 | **Probe data is stale relative to the file** (the file was replaced between scan and the RPC; e.g. a librarian dropped a re-encoded version with a different stream layout). | `_lookup_subtitle_stream` reads from `media_info.raw_ffprobe`, which is whatever the last scan recorded. If the new file's stream `N` is now an audio track, ffmpeg will fail at the `-map` step with `Stream specifier '0:N' in filtergraph matches no streams`; the resulting `FFmpegDecodeError` returns `INTERNAL`. The downstream remediation is "re-scan the video", which the API surfaces as a banner. We do not auto-reprobe inside this RPC — that's a per-call extra ffprobe and would mask drift the operator should know about. |
| E14 | **The video's content_hash changes** between the scan that wrote the row and the RPC that reads it (file replaced under the same path). | The cache layout uses the *current* `content_hash` from the video row. If the row was updated by a re-scan, the new hash gives a different filename, so old extracted VTTs become orphaned (cleaned by the same monthly housekeeping cron that handles stale `locks/`). The DB row remains queryable; the next RPC call re-extracts under the new hash. No wrong file is ever served. |
| E15 | **gRPC client cancels mid-extraction.** | `grpc.aio.ServicerContext` cancellation propagates to the awaited extractor task; `EmbeddedExtractor.extract` is cooperative — the ffmpeg subprocess receives SIGTERM via the same FFmpegRunner cleanup path used in Plan 2.3. The flock is released (the `async with` finally runs); tmp files are cleaned in `_cleanup_tmp`. No partial DB row exists because the INSERT only runs after `os.replace`. |

---

## 6. Acceptance checklist

- [ ] **A1** The canonical `subtitle_files` migration (slot 0015, owned by [plan-04-03](plan-04-03-external-discovery.md)) carries the columns this story needs. Verified by `test_migration_adds_is_embedded` against a fresh DB:
    - Column `is_embedded BOOLEAN NOT NULL DEFAULT FALSE`
    - Column `track_index INTEGER NULL`
    - Column `metadata JSONB NULL`
    - Index `subtitle_files_video_kind (video_id, is_external, is_embedded)`
    - Partial unique index `subtitle_files_embedded_unique_per_track (video_id, track_index) WHERE is_embedded = TRUE`
    - Two CHECK constraints: `subtitle_files_embedded_has_track_index`, `subtitle_files_embedded_xor_external`
    Existing rows are backfilled to `is_embedded = false`. (`test_migration_adds_is_embedded`, `test_migration_round_trip`)
- [ ] **A2** `shared/proto/pipeline.proto` adds `ExtractEmbeddedSubtitle`, `ExtractEmbeddedSubtitleRequest`, `ExtractEmbeddedSubtitleResponse`, and `SubtitleDispositionMetadata` exactly as in §2.3, and the regenerated `pipeline_pb2.py` and `pipeline_pb2_grpc.py` are committed. `architecture.md §9.9` gains a new table row documenting the RPC. (Visual diff check.)
- [ ] **A3** The probe ([Story 2.1](../02-audio-extraction/story-02-01-audio-probe.md)) populates `media_info.has_subtitles` and `media_info.raw_ffprobe[].codec_type == 'subtitle'`; `EmbeddedExtractor` reads only from this snapshot for validation and never re-runs ffprobe at extract time. (Out-of-scope to this story to *test* the probe write, but in-scope to assert the read works against the documented shape — see `test_ffprobe_stream_from_dict`.)
- [ ] **A4** `Pipeline.ExtractEmbeddedSubtitle(video_id, stream_index)` returns the absolute path to a parseable WebVTT file at `<library_root>/.maktaba/subs/<hash>.<lang>.s<idx>.embedded.vtt` for any text-codec stream listed in `media_info.raw_ffprobe`. (`test_embedded_text_extraction_mkv_two_srt`, `test_embedded_mov_text_mp4`)
- [ ] **A5** A second call for the same `(video_id, stream_index)` returns `cached = true`, the same `path`, and runs ffmpeg exactly zero additional times. (`test_embedded_idempotent_caches`)
- [ ] **A6** Concurrent calls for the same `(video_id, stream_index)` serialize through a per-pair filesystem lock; exactly one ffmpeg invocation is made; one caller gets `cached = false` and the other gets `cached = true`. (`test_embedded_concurrent_calls_serialize`)
- [ ] **A7** `stream_index` validation: out-of-range, negative, and pointing-at-a-non-subtitle-stream all return gRPC `INVALID_ARGUMENT` with detail `unknown_subtitle_stream` (or the codec_type-mismatch variant); no file is written. (Resolves REVIEW §5.2.) (`test_embedded_out_of_range_rejected`, `test_embedded_pointing_at_audio_stream_rejected`, `test_embedded_zero_tracks_rejected`)
- [ ] **A8** Image-based codecs (`hdmv_pgs_subtitle`, `dvd_subtitle`, `dvb_subtitle`, `xsub`) and unrecognized codecs return gRPC `UNIMPLEMENTED` with detail `unsupported_subtitle_codec`; no file or row is written. The probe data remains intact so the UI can show a "burned-in only" indicator. (`test_embedded_pgs_returns_unimplemented`)
- [ ] **A9** A successful extraction inserts exactly one `subtitle_files` row with `is_external = false`, `is_embedded = true`, `track_index = <stream_index>`, `format = 'vtt'`, `transcript_id = NULL`, `language = <stream-lang or 'und'>`, `path = <absolute>`, and `metadata.codec` populated. (`test_embedded_text_extraction_mkv_two_srt`)
- [ ] **A10** Cue text in the extracted VTT is sanitized via `subtitles.vtt_sanitizer.sanitize_vtt` (the same sanitizer used by Story 4.1 segment-generated subs) before the atomic rename; HTML, scripts, and other hostile payloads are stripped. (`test_embedded_extraction_sanitizes_hostile_cues`)
- [ ] **A11** Disposition flags (`forced`, `hearing_impaired`, `default`, `dub`, `original`, `comment`, `lyrics`, `karaoke`) are propagated from FFprobe into `subtitle_files.metadata.disposition` and mirrored in `ExtractEmbeddedSubtitleResponse.metadata`. (`test_embedded_disposition_metadata`)
- [ ] **A12** Two embedded tracks at the same language end up at distinct paths (`s<idx>` suffix in filename) and produce distinct rows. (`test_embedded_two_arabic_tracks_disambiguated`)
- [ ] **A13** An embedded extraction for a language that already has an external sidecar from Story 4.3 leaves the external row in place and writes a new `is_embedded` row alongside it. (`test_embedded_extraction_does_not_hide_external_sidecar`)
- [ ] **A14** Extracted files larger than `pipeline.subtitles.max_extracted_bytes` (default 50 MiB) abort with gRPC `RESOURCE_EXHAUSTED:subtitle_too_large`; no tmp or final file remains; no row is written. The limit is settable per-library. (`test_embedded_subtitle_too_large`)
- [ ] **A15** A missing language tag yields `language = "und"` in the path, the gRPC response, and the DB row. (`test_embedded_no_language_tag_defaults_to_und`)
- [ ] **A16** A corrupt subtitle stream surfaces as gRPC `INTERNAL:extraction_failed` with the FFmpeg `kind` ("ffmpeg_decode" or "ffmpeg_timeout") visible in the structured log; no row is written; the tmp file is cleaned. (`test_embedded_corrupt_container_returns_internal`)
- [ ] **A17** A `(file present, row missing)` cache state self-heals — the next call re-runs ffmpeg, the rename is a no-op overwrite, the INSERT runs (or no-ops on conflict), and a single canonical row is left behind. (`test_embedded_db_row_orphan_triggers_re_extract`)
- [ ] **A18** No code path in this story enqueues a `processing_jobs` row, advances `videos.state`, or otherwise mutates the pipeline state machine. The extraction RPC is purely side-channel. (Static check: grep for `INSERT INTO processing_jobs` and `UPDATE videos SET state` in the new module — must be zero matches.)
- [ ] **A19** The `architecture.md §9.9` Pipeline RPC table is updated to list `ExtractEmbeddedSubtitle` and the same updated section explicitly references this story as the owner. (Doc PR companion to the code PR.)
- [ ] **A20** The slot-0015 down-migration cleanly reverts the schema to its pre-migration state on an empty DB. (`test_migration_round_trip` is owned by plan-04-03's test harness.)
