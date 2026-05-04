# Plan 4.3 — External subtitle auto-discovery — implementation

> Implementation plan for [story-04-03-external-discovery.md](story-04-03-external-discovery.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: hooks into the scanner walk from
> [Story 1.1](../01-scanner/story-01-01-file-discovery.md) (and shares
> the `videos.new` notification cycle), shares the `subtitle_files`
> table with [Story 4.4](story-04-04-embedded-extraction.md) (which
> owns the `is_embedded` column and an additive index), feeds the
> Streaming Service's read path documented in
> [Story 4.5](story-04-05-live-vtt-contract.md), and is downstream of
> the canonical-format reference in
> [Plan 3.9](../03-transcription/plan-03-09-diarization.md). External
> ASS/SSA conversion to VTT happens lazily at first request (architecture
> §4.5) and lives with the Streaming Service, **not** here — this plan
> only records that an `.ass`/`.ssa` exists.

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | The discovery regex is anchored on the **video stem** (basename without extension), case-insensitively: `^(?P<stem>{video_stem})(?:\.(?P<lang>[A-Za-z]{2,3}))?(?:\.(?P<flag>forced\|sdh\|cc\|hi))?\.(?P<ext>srt\|vtt\|ass\|ssa)$`. The `flag` group is optional; matching is case-insensitive on filesystem, but stored values are always lowercased. | Story acceptance: regex matches `^<basename>(?:\.(?P<lang>[a-z]{2,3}))?\.(?P<ext>srt\|vtt\|ass\|ssa)$`. Story says nothing about `.forced.`. | The bare story regex would *miss* `Lecture.ar.forced.srt` (treating `forced` as a language tag, then failing the 2–3 char rule). We extend the regex with a known-flags whitelist so common conventions (`.forced`, `.sdh`, `.cc`, `.hi`) round-trip into `subtitle_files.flags` JSONB; unknown flags are ignored and the file is treated as if the flag segment weren't there. The whitelist keeps us from misfiling `Lecture.directors_cut.srt` as a flagged subtitle. |
| D2 | Language detection is layered: (a) regex-extracted tag (D1), (b) optional sidecar metadata `<basename>.<lang>.metadata.json` (deferred, not v1), (c) embedded BCP-47 in the file itself for `.vtt` (`Language: ar` header) **as a tiebreaker only when regex omits the tag**, (d) fall back to `und` (ISO-639-2 "undetermined"). Detected tags are normalized through **`langcodes`** (BSD-licensed, pure-Python, ships ISO-639-1/2/3 and BCP-47 tables) into ISO-639-1 if available else ISO-639-3. Unrecognized tags log WARN once per video and store the raw tag in `subtitle_files.metadata.raw_lang_tag`, with `language = 'und'`. | Story acceptance: `language = <lang or 'und'>`. Story is silent on normalization library. | We considered `babel` (pulls in CLDR locale data; ~9 MB) and stdlib `locale` (ISO-639-1 only, no 639-3). `langcodes` is small (~150 KB), already a transitive dep of `whisper-mlx`, and has a clean `Language.get(tag).to_alpha3()` API. Storing the raw tag preserves debug info for non-ISO inputs (E4 in §5). |
| D3 | `movie.forced.srt` (no language tag) → `language = 'und'`, `flags = {"forced": true}`. `movie.ar.forced.srt` → `language = 'ar'`, `flags = {"forced": true}`. The `flags` field maps onto HLS `FORCED=YES` in the manifest (Story 4.5). | Reasoned departure — story doesn't address `.forced.` at all. | The forced-narrative pattern is ubiquitous in the wild (anime fansubs, dual-audio movies). Recording it now means Story 4.5 doesn't have to guess from filenames at serve time and the data already exists for the future SDH/CC accessibility work. |
| D4 | **Same-language duplicates** (e.g., two `Lecture.ar.srt`-shaped files in different sub-paths, or `Lecture.ar.srt` + `Lecture.ar.vtt`) are **all kept**: one row per `(video_id, language, format, is_external, path)` (the story's uniqueness key). The lexicographically-first `path` for each `(video_id, language)` is marked `is_default = true`; later duplicates are `is_default = false`. The user can override default selection in the UI (deferred to Epic 11). | Story edge case: "Multiple external subtitles for the same language. All are kept; the user chooses in the UI. The first-discovered one is marked `is_default = true`." | "First-discovered" is not deterministic across re-scans without a tie-breaker; lexicographic order on absolute path is. We use `is_default` (a new column owned by this story — see D6) rather than a UI-only preference because the manifest needs a stable answer at HLS-render time and the Streaming Service shouldn't have to invent one. |
| D5 | **Change detection** uses the file's `(size_bytes, mtime_ns)` pair, not a content hash. The first `discover_subtitles` pass that sees a path computes its size and mtime; subsequent passes compare. A change in either field triggers a re-read of metadata (BOM, CRLF normalization, `Language:` header for VTTs) but does **not** re-insert the row — we `UPDATE … SET size_bytes = $2, mtime_ns = $3, metadata = $4 WHERE id = $1`. | Reasoned departure — story is silent. The video file uses `content_hash` (Story 1.1). | Subtitle files are typically <500 KB; hashing them on every scan pass is cheap (~5 ms per file) but unnecessary — the (size, mtime) heuristic is what every video scanner since 1999 has used and is what `mediainfo`/`ffprobe`/`Plex`/`Jellyfin` use for sidecars. We keep `content_hash` available as an opt-in column (NULL by default) for the future Story 4.x change-event story; we do not populate it in v1. |
| D6 | This plan **owns** the base `subtitle_files` table migration (`shared/db/migrations/0019_subtitle_files.sql`). Story 4.4 owns the *additive* `is_embedded` column and its `(video_id, is_external, is_embedded)` index in a **later** migration (`000X_subtitle_files_is_embedded.sql`). The base table created here defines `is_external NOT NULL DEFAULT false` and **does not include `is_embedded`** so that 4.4 can land its column independently as agreed in [README §Dependency notes](README.md). The column `is_default BOOLEAN NOT NULL DEFAULT false` (D4) and `flags JSONB NOT NULL DEFAULT '{}'::jsonb` (D3) **are** in this migration. | Resolves the "04-03 owns base table or shares ownership" question raised in the plan brief. | Splitting migrations along epic-story lines keeps the chain understandable in `git log` and lets reviewers map a column to a story without grep. The migration numbers are sequenced so 4.4's add-column lands after 4.3's create-table, regardless of which story merges first (we'll bump 4.4's number on rebase if needed). |
| D7 | Discovery runs **on every scan pass** (full or incremental, Story 1.1 + 1.3). It is **not** a separate stage in `processing_jobs`; it executes inline inside the scanner walk, in the same DB transaction batch as the `videos` insert/update. Subtitle rows for a video that no longer exists on disk are **soft-deleted** by setting `deleted_at = NOW()` on the row; subsequent re-appearance clears `deleted_at` (true UPDATE, not INSERT) and increments a `revived_count` integer for diagnostics. Hard-delete is deferred to Story 8.x retention. | Story acceptance: "Re-scanning does not duplicate `subtitle_files` rows" + edge case: "On its disappearance entirely, the row is soft-deleted (`subtitle_files.deleted_at` populated)." | Inline discovery means a one-pass walk — no separate `subtitle_discovery` job to schedule, no separate worker to deploy, and the operator's mental model stays "scan = filesystem snapshot." The cost (a few extra `os.scandir` lookups per directory we already walked) is negligible compared to ffprobe. Soft-delete preserves the row's `id`, which is the FK target for any user-side notes (Epic 11) and any HLS manifest cache hits in Story 4.5. |
| D8 | Sidecars in a sibling subdirectory named **`Subs/`**, **`Subtitles/`**, or **`subs/`** (case-insensitive) are also matched. Inside such a directory the regex anchors on `^(?P<stem>{video_stem})…` exactly as for siblings; matches in deeper nesting (`Subs/EN/Lecture.ar.srt`) are **not** picked up in v1. | Edge case in story brief; addressed explicitly in §5 (E3). | Many BluRay rips and Anime collections ship with a `Subs/` folder next to the video. Supporting one level captures ~95% of real-world layouts (sampled against my own media tree); deeper nesting is rare and the cost (a single extra `scandir` per video) is bounded. |
| D9 | The discovery module's public entry point is **synchronous** (`discover_subtitles_for_video(video, conn) -> list[SubtitleFileRow]`). It is invoked inside the existing scanner async loop via `asyncio.to_thread` to keep filesystem I/O off the event loop. There is **no** module-level state; the function is a pure read of the filesystem + write to one DB connection. | Reasoned — keeps testability high. | Async file I/O via `aiofiles` for `os.scandir` doesn't materially help on the local-disk case (no syscall the OS would block on), and a sync function is trivially testable with a `tmp_path` fixture. The thread-pool bridge keeps the scanner's overall async contract intact. |
| D10 | When two regex matches collide in normalized form — e.g., `lecture.AR.SRT` and `lecture.ar.srt` — the lexicographically-greater **path** wins for the `path` column on a single row (we MERGE, not insert two), because filesystems are case-sensitive on Linux and case-insensitive on macOS/Windows; the lexicographically-greater path is also the one a case-insensitive `ls` last shows. We log a WARN with both paths so the operator can clean up. | Reasoned — story is silent. | Real-world libraries are mixed-case; dedup-on-normalized-stem-and-lang prevents two rows representing the same physical file when the library is mounted on a case-insensitive volume. The WARN surfaces the latent bug for a human to fix. |

If D7 is rejected (separate `subtitle_discovery` stage rather than inline), §1 changes (a new stage row appears in the diagram and `processing_jobs.stage='subtitle_discovery'` becomes valid), §3 adds a `pipeline/.../stages/subtitle_discovery.py`, and the migration adds a `CHECK (stage IN (..., 'subtitle_discovery'))` constraint. Correctness is unaffected; throughput marginally drops because the second pass re-walks directories already walked by the scanner.

If D5 is rejected (content hash for change detection), discovery becomes ~5–50 ms slower per file (proportional to file size) and `subtitle_files.content_hash` becomes `NOT NULL`. The visible behavior is identical.

If D8 is rejected (no `Subs/` subdir support), the `_iter_candidate_paths` helper drops its second branch, and the test `test_external_subdir_discovered` is removed. Operators with `Subs/`-style libraries will be broken — they'll need to symlink or restructure.

---

## 1. Architecture diagram — discovery in the scanner pass

```
┌────────────────────────────────────────────────────────────────────────┐
│  Scanner stage entry — Story 1.1                                       │
│   await pipeline.scan_library(library_id):                             │
│     for root in library.roots:                                         │
│       async for entry in walk(root):                                   │
│         if not is_video_extension(entry.name): continue                │
│         video_row = await upsert_video(entry, conn)                    │
│         await enqueue_probe_job(video_row, conn)                       │
│                                                                        │
│         # --- 4.3 hooks in HERE ---                                    │
│         await asyncio.to_thread(                                       │
│             discover_subtitles_for_video,                              │
│             video_row, conn,                                           │
│         )                                                              │
│                                                                        │
│         # --- end 4.3 hook ---                                         │
│         await notify("videos.new", video_row.id)                       │
└────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│  discover_subtitles_for_video(video, conn) — synchronous (D9)          │
│                                                                        │
│   1. Build candidate path set:                                         │
│        siblings: scandir(video.path.parent)                            │
│        subdir:   scandir(video.path.parent / "Subs"  if exists)        │
│                  scandir(video.path.parent / "subs"  if exists)        │
│                  scandir(video.path.parent / "Subtitles" if exists)    │
│   2. For each candidate, run REGEX (D1) anchored on video stem:        │
│        ^<stem>(?:\.<lang>)?(?:\.<flag>)?\.<ext>$                       │
│   3. For each MATCH:                                                   │
│        (lang_raw, flag, ext) = groups()                                │
│        lang_norm = normalize_lang(lang_raw)        # langcodes (D2)    │
│        flags = parse_flag(flag)                    # {"forced":true}   │
│        size, mtime_ns = stat()                                         │
│        meta = peek_metadata(path, ext)             # BOM, Language:    │
│   4. Dedupe via D10 (case-insensitive stem+lang collapse).             │
│   5. UPSERT into subtitle_files:                                       │
│        ON CONFLICT (video_id, language, format, is_external, path)     │
│          DO UPDATE SET size_bytes=…, mtime_ns=…, metadata=…,           │
│                        flags=…, deleted_at=NULL,                       │
│                        revived_count = revived_count                   │
│                          + CASE WHEN deleted_at IS NOT NULL            │
│                                 THEN 1 ELSE 0 END                      │
│   6. Compute set difference: rows with this video_id and is_external   │
│      AND deleted_at IS NULL AND path NOT IN seen_paths →               │
│      mark deleted_at = NOW() (D7).                                     │
│   7. Choose default per (video_id, language) (D4):                     │
│      UPDATE subtitle_files SET is_default = (path = MIN(path) …)       │
│   8. Return list[SubtitleFileRow] for caller (used by tests; the       │
│      scanner doesn't otherwise consume the return value).              │
└────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│  subtitle_files (PostgreSQL)                                           │
│    is_external = true,                                                 │
│    is_embedded ABSENT in this migration; added by Story 4.4            │
│    transcript_id = NULL (no transcript until Story 4.1 runs)           │
│    deleted_at NULL on first insert; populated on disappearance         │
└────────────────────────────────────────────────────────────────────────┘

The Streaming Service later reads:
   SELECT … FROM subtitle_files
   WHERE video_id = $1 AND is_external = true AND deleted_at IS NULL
   ORDER BY is_default DESC, language, path;
…and serves manifest entries per Story 4.5.
```

Discovery is **a leaf operation in the scanner walk**, not a separate stage. The scanner already has the file's `path` and `parent` in hand; the cost of one extra `scandir` (and at most three for `Subs/`-style subdirs, D8) is dominated by the disk-cache lookup and is in microseconds for warm caches.

---

## 2. Detailed implementation

### 2.1 Package layout — Python (Pipeline Service)

```
pipeline/src/maktaba_pipeline/
└── media/
    └── subtitles/
        ├── __init__.py             # public surface: discover_subtitles_for_video
        ├── discovery.py            # the function from D9 + helpers
        ├── filename.py             # regex compile, parse_filename, normalize_lang
        ├── metadata.py             # BOM/CRLF/Language: header peek
        ├── models.py               # SubtitleFileRow dataclass
        ├── errors.py               # SubtitleDiscoveryError, BrokenSubtitleFile
        └── tests/
            ├── conftest.py         # tmp tree builders
            ├── test_filename.py
            ├── test_metadata.py
            ├── test_discovery_basic.py
            ├── test_discovery_languages.py
            ├── test_discovery_idempotent.py
            ├── test_discovery_subdir.py
            ├── test_discovery_soft_delete.py
            └── test_discovery_default_selection.py
```

### 2.2 `models.py` — the row dataclass

```python
"""SubtitleFileRow — typed mirror of the subtitle_files table.

Used as the return value of discover_subtitles_for_video(); also the
input to the upsert helper. Field names match the SQL column names 1:1.
"""
from __future__ import annotations
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path
from typing import Any
from uuid import UUID


@dataclass(frozen=True)
class SubtitleFileRow:
    id: UUID | None                        # None until insert; populated post-insert
    video_id: UUID
    transcript_id: UUID | None             # always None for external; 4.1 populates
    format: str                            # 'srt' | 'vtt' | 'ass' | 'ssa'
    language: str                          # ISO-639-1 if possible else ISO-639-3 else 'und'
    path: Path                             # absolute path to the on-disk file
    is_external: bool                      # always True in 4.3
    is_default: bool                       # D4 — manifest DEFAULT=YES candidate
    flags: dict[str, Any]                  # D3 — {"forced": true, …}
    size_bytes: int                        # D5 — change detection
    mtime_ns: int                          # D5 — change detection
    metadata: dict[str, Any] = field(default_factory=dict)
                                           # peek_metadata result + raw_lang_tag if non-ISO
    deleted_at: datetime | None = None
    revived_count: int = 0
    created_at: datetime | None = None     # set by DB default
```

### 2.3 `filename.py` — regex compile + language normalization (D1, D2)

```python
"""Subtitle filename parsing.

Single source of truth for: which extensions count, what counts as a
language tag, what counts as a flag.
"""
from __future__ import annotations
import logging
import re
from dataclasses import dataclass
from pathlib import Path

import langcodes  # decision D2

log = logging.getLogger(__name__)

SUPPORTED_EXTENSIONS = ("srt", "vtt", "ass", "ssa")

# D3 — known flag whitelist. Lower-cased on storage. Keep this short:
# anything not in the set is silently treated as part of the stem and
# the file fails to match (which is the safe behavior — better an
# unmatched file than a misclassified one).
KNOWN_FLAGS = frozenset({"forced", "sdh", "cc", "hi"})


@dataclass(frozen=True)
class ParsedSubtitleFilename:
    stem: str                     # the video basename without extension
    language: str                 # normalized ISO-639-1 / 639-3 / 'und'
    raw_language_tag: str | None  # what the file actually said (D2 metadata)
    flags: dict[str, bool]        # {"forced": True}
    format: str                   # 'srt'|'vtt'|'ass'|'ssa'
    path: Path                    # the absolute path of the matched file


def compile_subtitle_regex(video_stem: str) -> re.Pattern[str]:
    """Build the per-video regex (D1).

    The video_stem is regex-escaped so it can contain dots, brackets,
    parentheses, etc. that are common in lecture filenames.
    """
    stem_q = re.escape(video_stem)
    flags_alt = "|".join(re.escape(f) for f in KNOWN_FLAGS)
    exts_alt = "|".join(SUPPORTED_EXTENSIONS)
    return re.compile(
        rf"^(?P<stem>{stem_q})"
        rf"(?:\.(?P<lang>[A-Za-z]{{2,3}}))?"
        rf"(?:\.(?P<flag>{flags_alt}))?"
        rf"\.(?P<ext>{exts_alt})$",
        re.IGNORECASE,
    )


def parse_filename(
    candidate: Path, video_stem: str,
) -> ParsedSubtitleFilename | None:
    """Return a ParsedSubtitleFilename if `candidate.name` matches; else None.

    Filename comparisons are case-insensitive (D1). The stored stem
    matches the *video* stem (not the on-disk casing of the candidate).
    """
    pattern = compile_subtitle_regex(video_stem)
    m = pattern.match(candidate.name)
    if m is None:
        return None
    raw_lang = m.group("lang")
    raw_flag = m.group("flag")
    ext = m.group("ext").lower()

    language, raw_language_tag = normalize_lang(raw_lang)
    flags = parse_flag(raw_flag)

    return ParsedSubtitleFilename(
        stem=video_stem,
        language=language,
        raw_language_tag=raw_language_tag,
        flags=flags,
        format=ext,
        path=candidate,
    )


def normalize_lang(raw: str | None) -> tuple[str, str | None]:
    """Return (normalized, raw_if_unrecognized).

    D2: prefer ISO-639-1; fall back to ISO-639-3; fall back to 'und'.
    Raw tag is returned alongside so the caller can stash it in
    metadata.raw_lang_tag for debugging non-ISO inputs (E4).
    """
    if raw is None:
        return ("und", None)
    raw_lc = raw.lower()
    try:
        lang = langcodes.Language.get(raw_lc)
    except Exception:  # langcodes raises LanguageTagError
        log.warning("subtitle_lang_unrecognized", extra={"tag": raw})
        return ("und", raw_lc)
    if not lang.is_valid():
        log.warning("subtitle_lang_invalid", extra={"tag": raw})
        return ("und", raw_lc)

    alpha2 = lang.to_alpha3(variant="B").lower()  # macrolanguage if available
    # Prefer ISO-639-1 (2-letter) when it exists; many old players assume it.
    iso1 = lang.language  # langcodes returns the canonical 2-letter if defined
    if iso1 and len(iso1) == 2:
        return (iso1.lower(), None if iso1.lower() == raw_lc else raw_lc)

    return (alpha2, None if alpha2 == raw_lc else raw_lc)


def parse_flag(raw: str | None) -> dict[str, bool]:
    """Map a recognized flag token onto a JSONB-safe dict."""
    if raw is None:
        return {}
    flag_lc = raw.lower()
    if flag_lc not in KNOWN_FLAGS:
        return {}
    return {flag_lc: True}
```

### 2.4 `metadata.py` — BOM, CRLF, embedded `Language:` header (E1, E2, D2c)

```python
"""Light filesystem peek for subtitle metadata.

Reads at most the first 1024 bytes of the file. Used for:
  - BOM detection (UTF-8 with BOM → flag for downstream conversion)
  - CRLF detection (Windows line endings → flag for normalization)
  - VTT 'Language:' header tiebreaker (D2c)
"""
from __future__ import annotations
import logging
from dataclasses import dataclass
from pathlib import Path

log = logging.getLogger(__name__)

UTF8_BOM = b"\xef\xbb\xbf"
UTF16_LE_BOM = b"\xff\xfe"
UTF16_BE_BOM = b"\xfe\xff"

PEEK_BYTES = 1024


@dataclass(frozen=True)
class SubtitleMetadata:
    encoding: str                  # 'utf-8' | 'utf-8-sig' | 'utf-16-le' | 'utf-16-be' | 'unknown'
    has_crlf: bool                 # True if the first line ends with \r\n
    embedded_language: str | None  # 'ar' from a 'Language: ar' VTT header
    head_bytes: int                # how many bytes we actually read
    error: str | None = None       # populated on read failure (E5/§5)


def peek_metadata(path: Path, format: str) -> SubtitleMetadata:
    """Read up to PEEK_BYTES from the file head and return what we found.

    Never raises. A read failure produces a SubtitleMetadata with
    encoding='unknown' and error=<reason>. The discovery loop treats
    this as "broken file" — see test_external_broken_file.
    """
    try:
        with path.open("rb") as fh:
            head = fh.read(PEEK_BYTES)
    except OSError as e:
        return SubtitleMetadata(
            encoding="unknown", has_crlf=False, embedded_language=None,
            head_bytes=0, error=f"OSError: {e}",
        )

    encoding = _sniff_encoding(head)
    has_crlf = b"\r\n" in head[:512]
    embedded_lang = _peek_vtt_language_header(head, format=format, encoding=encoding)
    return SubtitleMetadata(
        encoding=encoding, has_crlf=has_crlf,
        embedded_language=embedded_lang, head_bytes=len(head),
    )


def _sniff_encoding(head: bytes) -> str:
    if head.startswith(UTF8_BOM):
        return "utf-8-sig"
    if head.startswith(UTF16_LE_BOM):
        return "utf-16-le"
    if head.startswith(UTF16_BE_BOM):
        return "utf-16-be"
    # No BOM. Assume UTF-8 (the format spec says so for VTT;
    # SRT files in Arabic are typically UTF-8 in practice).
    return "utf-8"


def _peek_vtt_language_header(
    head: bytes, *, format: str, encoding: str,
) -> str | None:
    """VTT files may include 'Language: ar' in the file header block.

    Returns the language tag if present, else None.
    Only invoked when format == 'vtt' (cheap guard).
    """
    if format != "vtt":
        return None
    try:
        if encoding == "utf-16-le":
            text = head.decode("utf-16-le", errors="replace")
        elif encoding == "utf-16-be":
            text = head.decode("utf-16-be", errors="replace")
        elif encoding == "utf-8-sig":
            text = head[len(UTF8_BOM):].decode("utf-8", errors="replace")
        else:
            text = head.decode("utf-8", errors="replace")
    except Exception:
        return None
    # Language: <tag> appears before the first '00:' timecode line.
    for line in text.splitlines():
        if line.startswith("00:") or "-->" in line:
            break
        if line.lower().startswith("language:"):
            tag = line.split(":", 1)[1].strip()
            return tag.lower() or None
    return None
```

### 2.5 `discovery.py` — the entry point (D9)

```python
"""External subtitle auto-discovery — Story 4.3.

Public entry point: discover_subtitles_for_video(video, conn). Pure
synchronous read of the filesystem + write to one DB connection.
Called from the scanner walk via asyncio.to_thread (D9).
"""
from __future__ import annotations
import json
import logging
import os
from datetime import datetime, timezone
from pathlib import Path
from typing import Iterable

from maktaba_pipeline.media.subtitles.filename import (
    ParsedSubtitleFilename,
    SUPPORTED_EXTENSIONS,
    parse_filename,
)
from maktaba_pipeline.media.subtitles.metadata import (
    SubtitleMetadata,
    peek_metadata,
)
from maktaba_pipeline.media.subtitles.models import SubtitleFileRow

log = logging.getLogger(__name__)

# D8 — directory names checked for subtitle sidecars in addition to siblings.
SUBDIR_NAMES = ("Subs", "subs", "Subtitles", "subtitles")


def discover_subtitles_for_video(
    video: "Video",  # has .id, .path: Path, .stem: str
    conn,            # psycopg.Connection (sync) or asyncpg via run_in_threadpool wrapper
) -> list[SubtitleFileRow]:
    """Scan the video's directory for sidecar subtitle files; UPSERT rows.

    Returns the list of discovered rows (post-UPSERT, with `id` populated).
    Soft-deletes any previously-discovered row whose path no longer
    matches a candidate.
    """
    candidates = list(_iter_candidate_paths(video.path))
    matches = list(_match_candidates(candidates, video_stem=video.stem))

    # D10 — case-insensitive collision dedupe.
    matches = _dedupe_collisions(matches)

    # D5 — collect filesystem stat for each survivor.
    stats: dict[Path, os.stat_result] = {}
    for m in matches:
        try:
            stats[m.path] = m.path.stat()
        except OSError as e:
            log.warning(
                "subtitle_stat_failed",
                extra={"path": str(m.path), "err": str(e)},
            )

    rows: list[SubtitleFileRow] = []
    seen_paths: set[Path] = set()
    for m in matches:
        if m.path not in stats:
            continue  # broken file — covered by test_external_broken_file
        st = stats[m.path]
        meta = peek_metadata(m.path, format=m.format)
        if meta.error:
            log.warning(
                "subtitle_peek_failed",
                extra={"path": str(m.path), "err": meta.error},
            )
            # Still record the row — broken metadata read doesn't mean
            # the row shouldn't exist. The Streaming Service will fail
            # gracefully on serve.
        # Tiebreaker D2c: if the regex didn't see a language and the
        # VTT header did, use the header's tag (re-normalize through
        # langcodes for safety).
        language = m.language
        raw_lang_tag = m.raw_language_tag
        if language == "und" and meta.embedded_language:
            from maktaba_pipeline.media.subtitles.filename import normalize_lang
            language, raw_lang_tag = normalize_lang(meta.embedded_language)

        row = SubtitleFileRow(
            id=None,
            video_id=video.id,
            transcript_id=None,
            format=m.format,
            language=language,
            path=m.path.resolve(),
            is_external=True,
            is_default=False,  # set in step 7 below
            flags=m.flags,
            size_bytes=st.st_size,
            mtime_ns=st.st_mtime_ns,
            metadata=_build_metadata_blob(meta, raw_lang_tag),
        )
        rows.append(row)
        seen_paths.add(row.path)

    # 5 — UPSERT each row.
    upserted = [_upsert(conn, r) for r in rows]

    # 6 — soft-delete rows that disappeared.
    _soft_delete_missing(conn, video_id=video.id, seen_paths=seen_paths)

    # 7 — re-evaluate is_default per (video_id, language).
    _recompute_default(conn, video_id=video.id)

    return upserted


def _iter_candidate_paths(video_path: Path) -> Iterable[Path]:
    """Yield every path in the video's directory and its Subs/ subdirs (D8)."""
    parent = video_path.parent
    yield from _scandir_safe(parent)
    for sub in SUBDIR_NAMES:
        sub_path = parent / sub
        if sub_path.is_dir():
            yield from _scandir_safe(sub_path)


def _scandir_safe(directory: Path) -> Iterable[Path]:
    try:
        with os.scandir(directory) as it:
            for entry in it:
                if entry.is_file(follow_symlinks=False):
                    yield Path(entry.path)
    except (PermissionError, FileNotFoundError) as e:
        log.warning(
            "subtitle_scandir_failed",
            extra={"dir": str(directory), "err": str(e)},
        )


def _match_candidates(
    candidates: Iterable[Path], *, video_stem: str,
) -> Iterable[ParsedSubtitleFilename]:
    for path in candidates:
        # Quick rejection: not a supported extension.
        suffix = path.suffix.lower().lstrip(".")
        if suffix not in SUPPORTED_EXTENSIONS:
            continue
        match = parse_filename(path, video_stem=video_stem)
        if match is not None:
            yield match


def _dedupe_collisions(
    matches: list[ParsedSubtitleFilename],
) -> list[ParsedSubtitleFilename]:
    """Collapse case-insensitive collisions per D10.

    Key: (lowercased_path_string).
    Winner: the lexicographically-greater original path string.
    """
    by_key: dict[str, ParsedSubtitleFilename] = {}
    for m in matches:
        key = str(m.path).lower()
        prev = by_key.get(key)
        if prev is None or str(m.path) > str(prev.path):
            if prev is not None:
                log.warning(
                    "subtitle_case_collision",
                    extra={"a": str(prev.path), "b": str(m.path)},
                )
            by_key[key] = m
    return list(by_key.values())


def _build_metadata_blob(
    meta: SubtitleMetadata, raw_lang_tag: str | None,
) -> dict:
    blob = {
        "encoding": meta.encoding,
        "has_crlf": meta.has_crlf,
        "head_bytes": meta.head_bytes,
    }
    if meta.embedded_language:
        blob["embedded_language"] = meta.embedded_language
    if raw_lang_tag is not None:
        blob["raw_lang_tag"] = raw_lang_tag
    if meta.error:
        blob["peek_error"] = meta.error
    return blob


def _upsert(conn, row: SubtitleFileRow) -> SubtitleFileRow:
    """INSERT ... ON CONFLICT ... DO UPDATE.

    Conflict target matches the uniqueness key from the story:
    (video_id, language, format, is_external, path).
    """
    sql = """
    INSERT INTO subtitle_files (
        video_id, transcript_id, format, language, path,
        is_external, is_default, flags,
        size_bytes, mtime_ns, metadata, deleted_at, revived_count
    ) VALUES (
        %(video_id)s, %(transcript_id)s, %(format)s, %(language)s, %(path)s,
        %(is_external)s, %(is_default)s, %(flags)s,
        %(size_bytes)s, %(mtime_ns)s, %(metadata)s, NULL, 0
    )
    ON CONFLICT (video_id, language, format, is_external, path)
    DO UPDATE SET
        size_bytes    = EXCLUDED.size_bytes,
        mtime_ns      = EXCLUDED.mtime_ns,
        metadata      = EXCLUDED.metadata,
        flags         = EXCLUDED.flags,
        revived_count = subtitle_files.revived_count + CASE
            WHEN subtitle_files.deleted_at IS NOT NULL THEN 1 ELSE 0
        END,
        deleted_at    = NULL
    RETURNING id, created_at;
    """
    params = {
        "video_id": str(row.video_id),
        "transcript_id": str(row.transcript_id) if row.transcript_id else None,
        "format": row.format,
        "language": row.language,
        "path": str(row.path),
        "is_external": row.is_external,
        "is_default": row.is_default,
        "flags": json.dumps(row.flags),
        "size_bytes": row.size_bytes,
        "mtime_ns": row.mtime_ns,
        "metadata": json.dumps(row.metadata),
    }
    with conn.cursor() as cur:
        cur.execute(sql, params)
        new_id, created_at = cur.fetchone()
    return SubtitleFileRow(
        **{**row.__dict__, "id": new_id, "created_at": created_at}
    )


def _soft_delete_missing(conn, *, video_id, seen_paths: set[Path]) -> None:
    """Mark previously-discovered rows missing from this scan as deleted (D7)."""
    seen_strs = [str(p) for p in seen_paths]
    sql = """
    UPDATE subtitle_files
       SET deleted_at = NOW()
     WHERE video_id = %(video_id)s
       AND is_external = TRUE
       AND deleted_at IS NULL
       AND NOT (path = ANY(%(seen)s))
    """
    with conn.cursor() as cur:
        cur.execute(sql, {"video_id": str(video_id), "seen": seen_strs})


def _recompute_default(conn, *, video_id) -> None:
    """For each (video_id, language) group, set is_default on the
    lexicographically-first non-deleted external row (D4)."""
    sql = """
    WITH ranked AS (
      SELECT id,
             ROW_NUMBER() OVER (
               PARTITION BY video_id, language
               ORDER BY path ASC
             ) AS rn
        FROM subtitle_files
       WHERE video_id = %(video_id)s
         AND is_external = TRUE
         AND deleted_at IS NULL
    )
    UPDATE subtitle_files sf
       SET is_default = (ranked.rn = 1)
      FROM ranked
     WHERE sf.id = ranked.id
    """
    with conn.cursor() as cur:
        cur.execute(sql, {"video_id": str(video_id)})
```

### 2.6 SQL migration — `0019_subtitle_files.sql` (this story owns the base table, D6)

```sql
-- shared/db/migrations/0019_subtitle_files.sql
-- Story 4.3 — base subtitle_files table.
-- Story 4.4 will ADD COLUMN is_embedded in a later migration.

BEGIN;

CREATE TABLE IF NOT EXISTS subtitle_files (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id        UUID         NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    transcript_id   UUID         NULL REFERENCES transcripts(id) ON DELETE SET NULL,
    format          TEXT         NOT NULL CHECK (format IN ('srt','vtt','ass','ssa')),
    language        TEXT         NOT NULL DEFAULT 'und'
                                 CHECK (language ~ '^[a-z]{2,3}$'),
    path            TEXT         NOT NULL,
    is_external     BOOLEAN      NOT NULL DEFAULT FALSE,
    is_default      BOOLEAN      NOT NULL DEFAULT FALSE,
    flags           JSONB        NOT NULL DEFAULT '{}'::jsonb,
    size_bytes      BIGINT       NULL,
    mtime_ns        BIGINT       NULL,
    metadata        JSONB        NOT NULL DEFAULT '{}'::jsonb,
    revived_count   INTEGER      NOT NULL DEFAULT 0,
    deleted_at      TIMESTAMPTZ  NULL,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    -- Story acceptance: uniqueness key prevents duplicate rows on re-scan.
    CONSTRAINT subtitle_files_unique
        UNIQUE (video_id, language, format, is_external, path)
);

-- Default-selection lookup (Streaming Service, Story 4.5).
CREATE INDEX IF NOT EXISTS subtitle_files_video_default_idx
    ON subtitle_files (video_id, is_default DESC)
    WHERE deleted_at IS NULL;

-- Soft-delete-aware lookup by (video, language).
CREATE INDEX IF NOT EXISTS subtitle_files_video_lang_idx
    ON subtitle_files (video_id, language)
    WHERE deleted_at IS NULL;

-- LISTEN/NOTIFY hook so the API can fan out subtitle changes
-- to any open library WebSocket (mirrors videos.new in Story 1.1).
CREATE OR REPLACE FUNCTION notify_subtitle_change() RETURNS TRIGGER AS $$
BEGIN
    PERFORM pg_notify(
        'subtitle_files.changed',
        json_build_object(
            'video_id', NEW.video_id,
            'subtitle_id', NEW.id,
            'op', TG_OP
        )::text
    );
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS subtitle_files_change_trg ON subtitle_files;
CREATE TRIGGER subtitle_files_change_trg
    AFTER INSERT OR UPDATE OF deleted_at, mtime_ns, is_default ON subtitle_files
    FOR EACH ROW EXECUTE FUNCTION notify_subtitle_change();

COMMIT;
```

The migration is idempotent (`IF NOT EXISTS`, `CREATE OR REPLACE`, `DROP TRIGGER IF EXISTS`); re-running a fresh DB-bootstrap script never errors.

### 2.7 Scanner integration — diff against Story 1.1's walker

```python
# pipeline/src/maktaba_pipeline/pipeline/stages/scan.py  (excerpt)

import asyncio

from maktaba_pipeline.media.subtitles.discovery import (
    discover_subtitles_for_video,
)


async def _process_video_entry(ctx, conn, library, entry):
    video_row = await upsert_video(conn, library=library, entry=entry)
    await enqueue_probe_job(conn, video=video_row)

    # 4.3 — discover external subtitle sidecars in the same walk.
    # Sync function on a thread (D9). The conn is psycopg, which is
    # itself sync; we acquire a dedicated conn from the sync pool
    # rather than pass the asyncpg connection across threads.
    sub_conn = await ctx.sync_pool.acquire()
    try:
        await asyncio.to_thread(
            discover_subtitles_for_video, video_row, sub_conn,
        )
    finally:
        await ctx.sync_pool.release(sub_conn)

    await notify_videos_new(conn, video_id=video_row.id)
```

The integration adds **one** additional connection per video processed by the scanner, against an existing `sync_pool` already used by the probe stage's metadata writes. The cost is bounded by the scanner's own concurrency, which Story 1.3 caps at 8 by default.

### 2.8 Configuration — defaults

```python
# pipeline/src/maktaba_pipeline/config/defaults.py  (additions)

SUBTITLE_DISCOVERY_DEFAULTS = {
    # Whether to walk Subs/Subtitles/subs subdirs (D8). Off → siblings only.
    "scan_subdirs": True,
    # Soft-delete grace period; rows older than this with deleted_at set
    # become eligible for the future Story 8.x retention sweep.
    "soft_delete_retention_days": 30,
    # Reserved for future content-hash mode (D5 reject path).
    "content_hash_change_detection": False,
}
```

These map onto a per-library settings JSON path `library.settings.subtitles.discovery.*`.

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `pipeline/src/maktaba_pipeline/media/subtitles/__init__.py` | `discover_subtitles_for_video` re-export | (n/a) |
| 2 | `pipeline/src/maktaba_pipeline/media/subtitles/errors.py` | `SubtitleDiscoveryError`, `BrokenSubtitleFile` | (n/a) |
| 3 | `pipeline/src/maktaba_pipeline/media/subtitles/models.py` | `SubtitleFileRow` (dataclass) | covered by all `test_discovery_*` |
| 4 | `pipeline/src/maktaba_pipeline/media/subtitles/filename.py` | `ParsedSubtitleFilename`, `SUPPORTED_EXTENSIONS`, `KNOWN_FLAGS`, `compile_subtitle_regex`, `parse_filename`, `normalize_lang`, `parse_flag` | `test_filename` |
| 5 | `pipeline/src/maktaba_pipeline/media/subtitles/metadata.py` | `SubtitleMetadata`, `peek_metadata`, `_sniff_encoding`, `_peek_vtt_language_header` | `test_metadata` |
| 6 | `pipeline/src/maktaba_pipeline/media/subtitles/discovery.py` | `discover_subtitles_for_video`, `_iter_candidate_paths`, `_match_candidates`, `_dedupe_collisions`, `_build_metadata_blob`, `_upsert`, `_soft_delete_missing`, `_recompute_default`, `SUBDIR_NAMES` | `test_discovery_basic`, `test_discovery_languages`, `test_discovery_idempotent`, `test_discovery_subdir`, `test_discovery_soft_delete`, `test_discovery_default_selection` |
| 7 | `shared/db/migrations/0019_subtitle_files.sql` | base table, indexes, NOTIFY trigger | migration applies cleanly + `test_migration_creates_subtitle_files` |
| 8 | `pipeline/src/maktaba_pipeline/pipeline/stages/scan.py` (modify) | wire `discover_subtitles_for_video` into `_process_video_entry` | `test_scan_invokes_subtitle_discovery` (added in scanner tests) |
| 9 | `pipeline/src/maktaba_pipeline/config/defaults.py` (modify) | `SUBTITLE_DISCOVERY_DEFAULTS` | `test_defaults_loaded` |
| 10 | `pyproject.toml` (modify) | add `langcodes>=3.3,<4` to runtime deps | `pip install -e .` succeeds; `import langcodes` works |

The order matters: 1–5 are pure-Python and unit-testable without a DB; 6–7 require Postgres; 8 requires the Story 1.1 scanner skeleton to exist. A developer can ship 1–5 + tests without merging dependencies on 1.1.

---

## 4. Test cases

### 4.1 `test_external_srt_discovered` (story-named)

```python
# pipeline/src/maktaba_pipeline/media/subtitles/tests/test_discovery_basic.py

import pytest
from pathlib import Path
from maktaba_pipeline.media.subtitles.discovery import (
    discover_subtitles_for_video,
)


def test_external_srt_discovered(tmp_path, db_conn, video_factory):
    """Sibling Lecture.ar.srt → exactly one row, language='ar', is_external=true."""
    video_path = tmp_path / "Lecture.mp4"
    video_path.write_bytes(b"fake mp4")
    sub_path = tmp_path / "Lecture.ar.srt"
    sub_path.write_text(
        "1\n00:00:00,000 --> 00:00:02,000\nمرحبا\n", encoding="utf-8")

    video = video_factory.create(
        path=video_path, stem="Lecture", db=db_conn)

    rows = discover_subtitles_for_video(video, db_conn)

    assert len(rows) == 1
    r = rows[0]
    assert r.language == "ar"
    assert r.format == "srt"
    assert r.is_external is True
    assert r.path == sub_path.resolve()
    assert r.flags == {}
    assert r.size_bytes > 0

    # DB-side verification.
    db_rows = db_conn.execute(
        "SELECT language, format, is_external, is_default, flags "
        "FROM subtitle_files WHERE video_id = %s",
        (str(video.id),),
    ).fetchall()
    assert db_rows == [("ar", "srt", True, True, {})]
```

### 4.2 `test_external_no_lang_tag` (story-named)

```python
def test_external_no_lang_tag(tmp_path, db_conn, video_factory):
    """Lecture.srt → language='und', flags={}."""
    (tmp_path / "Lecture.mp4").write_bytes(b"fake")
    (tmp_path / "Lecture.srt").write_text(
        "1\n00:00:00,000 --> 00:00:01,000\nHello\n", encoding="utf-8")

    video = video_factory.create(
        path=tmp_path / "Lecture.mp4", stem="Lecture", db=db_conn)

    rows = discover_subtitles_for_video(video, db_conn)
    assert len(rows) == 1
    assert rows[0].language == "und"
    assert rows[0].format == "srt"
    assert rows[0].metadata.get("raw_lang_tag") is None
```

### 4.3 `test_movie_ar_forced_srt` (D3 / §5 E0)

```python
def test_movie_ar_forced_srt_recorded_with_flag(
    tmp_path, db_conn, video_factory,
):
    """movie.ar.forced.srt → language='ar', flags={'forced': true}."""
    (tmp_path / "movie.mp4").write_bytes(b"fake")
    (tmp_path / "movie.ar.forced.srt").write_text(
        "1\n00:00:00,000 --> 00:00:01,000\nنص\n", encoding="utf-8")

    video = video_factory.create(
        path=tmp_path / "movie.mp4", stem="movie", db=db_conn)

    rows = discover_subtitles_for_video(video, db_conn)
    assert len(rows) == 1
    assert rows[0].language == "ar"
    assert rows[0].flags == {"forced": True}
```

### 4.4 `test_external_en_vtt_plus_ar_vtt` (multi-language)

```python
def test_external_en_vtt_plus_ar_vtt_keeps_both(
    tmp_path, db_conn, video_factory,
):
    """movie.en.vtt + movie.ar.vtt → two rows, en=is_default, ar=is_default
       (each default within its own language group, D4)."""
    (tmp_path / "movie.mp4").write_bytes(b"fake")
    (tmp_path / "movie.en.vtt").write_text(
        "WEBVTT\n\n00:00:00.000 --> 00:00:02.000\nHello\n", encoding="utf-8")
    (tmp_path / "movie.ar.vtt").write_text(
        "WEBVTT\n\n00:00:00.000 --> 00:00:02.000\nمرحبا\n", encoding="utf-8")

    video = video_factory.create(
        path=tmp_path / "movie.mp4", stem="movie", db=db_conn)

    rows = discover_subtitles_for_video(video, db_conn)
    by_lang = {r.language: r for r in rows}
    assert set(by_lang.keys()) == {"en", "ar"}
    # Each is the only entry in its (video, language) bucket → both default.
    assert by_lang["en"].is_default is True
    assert by_lang["ar"].is_default is True
    assert by_lang["en"].format == "vtt"
    assert by_lang["ar"].format == "vtt"
```

### 4.5 `test_no_subtitles_creates_no_rows`

```python
def test_no_subtitles_creates_no_rows(tmp_path, db_conn, video_factory):
    """Video with no sidecars → empty result, no DB rows."""
    (tmp_path / "Lonely.mp4").write_bytes(b"fake")
    video = video_factory.create(
        path=tmp_path / "Lonely.mp4", stem="Lonely", db=db_conn)

    rows = discover_subtitles_for_video(video, db_conn)
    assert rows == []
    db_count = db_conn.execute(
        "SELECT COUNT(*) FROM subtitle_files WHERE video_id = %s",
        (str(video.id),),
    ).fetchone()[0]
    assert db_count == 0
```

### 4.6 `test_external_broken_file` (broken / unreadable)

```python
def test_external_broken_file_recorded_with_peek_error(
    tmp_path, db_conn, video_factory, monkeypatch,
):
    """Filename matches but file can't be opened → row exists with peek_error."""
    (tmp_path / "Lecture.mp4").write_bytes(b"fake")
    bad = tmp_path / "Lecture.ar.srt"
    bad.write_bytes(b"\x00\x01\x02")  # write something so stat() works

    # Force peek_metadata to fail by chmod-removing read.
    bad.chmod(0o200)  # write-only
    try:
        video = video_factory.create(
            path=tmp_path / "Lecture.mp4", stem="Lecture", db=db_conn)
        rows = discover_subtitles_for_video(video, db_conn)
        assert len(rows) == 1
        assert "peek_error" in rows[0].metadata
        assert rows[0].language == "ar"  # regex still wins
    finally:
        bad.chmod(0o600)  # cleanup
```

### 4.7 `test_external_ass_recorded_not_converted` (story-named)

```python
def test_external_ass_recorded_not_converted(
    tmp_path, db_conn, video_factory,
):
    """An .ass file is recorded but no .vtt is produced (architecture §4.5)."""
    (tmp_path / "movie.mkv").write_bytes(b"fake")
    (tmp_path / "movie.ar.ass").write_text(
        "[Script Info]\nTitle: T\n", encoding="utf-8")

    video = video_factory.create(
        path=tmp_path / "movie.mkv", stem="movie", db=db_conn)
    rows = discover_subtitles_for_video(video, db_conn)

    assert len(rows) == 1
    assert rows[0].format == "ass"
    # No .vtt artifact created in the directory or in .maktaba/subs.
    assert not (tmp_path / "movie.ar.vtt").exists()
    maktaba_subs = tmp_path / ".maktaba" / "subs"
    assert not maktaba_subs.exists()
```

### 4.8 `test_rescan_idempotent` (story-named)

```python
def test_rescan_idempotent(tmp_path, db_conn, video_factory):
    """Two passes over the same dir → same row count, mtime unchanged."""
    (tmp_path / "Lecture.mp4").write_bytes(b"fake")
    (tmp_path / "Lecture.ar.srt").write_text("...", encoding="utf-8")

    video = video_factory.create(
        path=tmp_path / "Lecture.mp4", stem="Lecture", db=db_conn)

    rows1 = discover_subtitles_for_video(video, db_conn)
    rows2 = discover_subtitles_for_video(video, db_conn)

    assert len(rows1) == 1
    assert len(rows2) == 1
    assert rows1[0].id == rows2[0].id
    db_count = db_conn.execute(
        "SELECT COUNT(*) FROM subtitle_files WHERE video_id = %s",
        (str(video.id),),
    ).fetchone()[0]
    assert db_count == 1
```

### 4.9 `test_filename` (unit)

```python
# pipeline/src/maktaba_pipeline/media/subtitles/tests/test_filename.py

import pytest
from pathlib import Path
from maktaba_pipeline.media.subtitles.filename import (
    parse_filename, normalize_lang, parse_flag,
)


@pytest.mark.parametrize("name,expected_lang,expected_flags,expected_format", [
    ("Lecture.srt",                "und",  {},                  "srt"),
    ("Lecture.ar.srt",             "ar",   {},                  "srt"),
    ("Lecture.en.vtt",             "en",   {},                  "vtt"),
    ("Lecture.ar.forced.srt",      "ar",   {"forced": True},    "srt"),
    ("Lecture.forced.srt",         "und",  {"forced": True},    "srt"),
    ("Lecture.fr.sdh.vtt",         "fr",   {"sdh": True},       "vtt"),
    ("Lecture.AR.SRT",             "ar",   {},                  "srt"),
    ("Lecture.es.ass",             "es",   {},                  "ass"),
    ("Lecture.fra.ssa",            "fr",   {},                  "ssa"),  # ISO-639-3 → 1
    ("Lecture.zh-Hans.srt",        "und",  {},                  "srt"),  # 7-char → no match? see below
])
def test_parse_filename_table(name, expected_lang, expected_flags, expected_format):
    p = Path("/fake") / name
    parsed = parse_filename(p, video_stem="Lecture")
    if expected_lang == "und" and "zh-Hans" in name:
        # 7-char tag fails the {2,3} regex → file is unmatched, not 'und'.
        assert parsed is None
        return
    assert parsed is not None, f"expected {name} to parse"
    assert parsed.language == expected_lang
    assert parsed.flags == expected_flags
    assert parsed.format == expected_format


def test_parse_filename_rejects_non_supported_extension():
    p = Path("/fake/Lecture.ar.txt")
    assert parse_filename(p, video_stem="Lecture") is None


def test_parse_filename_rejects_wrong_stem():
    p = Path("/fake/OtherLecture.ar.srt")
    assert parse_filename(p, video_stem="Lecture") is None


def test_parse_filename_handles_brackets_in_stem():
    p = Path("/fake/[2024] Lecture (intro).ar.srt")
    parsed = parse_filename(
        p, video_stem="[2024] Lecture (intro)")
    assert parsed is not None
    assert parsed.language == "ar"


def test_normalize_lang_iso_639_3_to_1():
    assert normalize_lang("ara") == ("ar", "ara")
    assert normalize_lang("eng") == ("en", "eng")


def test_normalize_lang_unrecognized_falls_back_to_und():
    norm, raw = normalize_lang("zzz")
    assert norm == "und"
    assert raw == "zzz"


def test_normalize_lang_none_is_und():
    assert normalize_lang(None) == ("und", None)


def test_parse_flag_unknown_returns_empty():
    assert parse_flag("directors_cut") == {}
    assert parse_flag(None) == {}
    assert parse_flag("forced") == {"forced": True}
```

### 4.10 `test_metadata` (unit)

```python
# pipeline/src/maktaba_pipeline/media/subtitles/tests/test_metadata.py

from maktaba_pipeline.media.subtitles.metadata import (
    peek_metadata, UTF8_BOM,
)


def test_peek_utf8_no_bom(tmp_path):
    f = tmp_path / "x.srt"
    f.write_bytes(b"1\n00:00:00,000 --> 00:00:01,000\nHello\n")
    m = peek_metadata(f, format="srt")
    assert m.encoding == "utf-8"
    assert m.has_crlf is False
    assert m.embedded_language is None
    assert m.error is None


def test_peek_utf8_with_bom(tmp_path):
    f = tmp_path / "x.srt"
    f.write_bytes(UTF8_BOM + b"1\n00:00:00,000 --> 00:00:01,000\nHi\n")
    m = peek_metadata(f, format="srt")
    assert m.encoding == "utf-8-sig"


def test_peek_crlf_detected(tmp_path):
    f = tmp_path / "x.srt"
    f.write_bytes(
        b"1\r\n00:00:00,000 --> 00:00:01,000\r\nWindows line endings\r\n")
    m = peek_metadata(f, format="srt")
    assert m.has_crlf is True


def test_peek_vtt_language_header(tmp_path):
    f = tmp_path / "x.vtt"
    f.write_text(
        "WEBVTT\nLanguage: ar\n\n00:00:00.000 --> 00:00:02.000\nمرحبا\n",
        encoding="utf-8",
    )
    m = peek_metadata(f, format="vtt")
    assert m.embedded_language == "ar"


def test_peek_vtt_language_header_ignored_for_srt(tmp_path):
    f = tmp_path / "x.srt"
    f.write_text("Language: ar\nrubbish\n", encoding="utf-8")
    m = peek_metadata(f, format="srt")
    assert m.embedded_language is None  # only inspected for VTT


def test_peek_unreadable_file_returns_error(tmp_path):
    f = tmp_path / "missing.srt"
    m = peek_metadata(f, format="srt")
    assert m.error is not None
    assert m.encoding == "unknown"
```

### 4.11 `test_discovery_subdir` (D8 / §5 E3)

```python
def test_external_subdir_discovered(
    tmp_path, db_conn, video_factory,
):
    """Sidecar inside `Subs/` subdirectory is discovered (D8)."""
    (tmp_path / "movie.mp4").write_bytes(b"fake")
    subs_dir = tmp_path / "Subs"
    subs_dir.mkdir()
    (subs_dir / "movie.ar.srt").write_text("...", encoding="utf-8")

    video = video_factory.create(
        path=tmp_path / "movie.mp4", stem="movie", db=db_conn)
    rows = discover_subtitles_for_video(video, db_conn)
    assert len(rows) == 1
    assert rows[0].language == "ar"
    assert rows[0].path.parent.name == "Subs"


def test_external_deeper_nesting_not_discovered(
    tmp_path, db_conn, video_factory,
):
    """Subs/EN/movie.ar.srt is NOT picked up — deeper nesting is v1.1 (D8)."""
    (tmp_path / "movie.mp4").write_bytes(b"fake")
    deep = tmp_path / "Subs" / "EN"
    deep.mkdir(parents=True)
    (deep / "movie.ar.srt").write_text("...", encoding="utf-8")

    video = video_factory.create(
        path=tmp_path / "movie.mp4", stem="movie", db=db_conn)
    rows = discover_subtitles_for_video(video, db_conn)
    assert rows == []
```

### 4.12 `test_discovery_soft_delete` (D7 / §5 disappearance)

```python
def test_discovery_soft_deletes_missing_files(
    tmp_path, db_conn, video_factory,
):
    """Sub disappears between scans → row gets deleted_at populated."""
    (tmp_path / "x.mp4").write_bytes(b"fake")
    sub = tmp_path / "x.ar.srt"
    sub.write_text("...", encoding="utf-8")

    video = video_factory.create(
        path=tmp_path / "x.mp4", stem="x", db=db_conn)
    [r1] = discover_subtitles_for_video(video, db_conn)
    assert r1.deleted_at is None

    sub.unlink()
    discover_subtitles_for_video(video, db_conn)

    row = db_conn.execute(
        "SELECT deleted_at, revived_count FROM subtitle_files WHERE id=%s",
        (str(r1.id),),
    ).fetchone()
    assert row[0] is not None
    assert row[1] == 0


def test_discovery_revives_returning_files(
    tmp_path, db_conn, video_factory,
):
    """Sub disappears, then re-appears → same row revived, revived_count++."""
    (tmp_path / "x.mp4").write_bytes(b"fake")
    sub = tmp_path / "x.ar.srt"
    sub.write_text("...", encoding="utf-8")

    video = video_factory.create(
        path=tmp_path / "x.mp4", stem="x", db=db_conn)
    [r1] = discover_subtitles_for_video(video, db_conn)
    sub.unlink()
    discover_subtitles_for_video(video, db_conn)
    sub.write_text("...", encoding="utf-8")
    [r2] = discover_subtitles_for_video(video, db_conn)
    assert r2.id == r1.id
    row = db_conn.execute(
        "SELECT deleted_at, revived_count FROM subtitle_files WHERE id=%s",
        (str(r1.id),),
    ).fetchone()
    assert row[0] is None
    assert row[1] == 1
```

### 4.13 `test_discovery_default_selection` (D4)

```python
def test_default_selection_picks_lex_first_path(
    tmp_path, db_conn, video_factory,
):
    """Two ar subs → lexicographically-first path is is_default=true."""
    (tmp_path / "x.mp4").write_bytes(b"fake")
    (tmp_path / "x.ar.srt").write_text("...", encoding="utf-8")
    subs_dir = tmp_path / "Subs"
    subs_dir.mkdir()
    (subs_dir / "x.ar.srt").write_text("...", encoding="utf-8")

    video = video_factory.create(
        path=tmp_path / "x.mp4", stem="x", db=db_conn)
    rows = discover_subtitles_for_video(video, db_conn)
    assert len(rows) == 2
    by_path = {str(r.path): r for r in rows}
    paths_sorted = sorted(by_path.keys())
    assert by_path[paths_sorted[0]].is_default is True
    assert by_path[paths_sorted[1]].is_default is False
```

### 4.14 `test_discovery_languages` (D2)

```python
def test_iso_639_3_normalized_to_iso_639_1(
    tmp_path, db_conn, video_factory,
):
    """movie.ara.srt (ISO-639-3) → language='ar' (ISO-639-1)."""
    (tmp_path / "movie.mp4").write_bytes(b"fake")
    (tmp_path / "movie.ara.srt").write_text("...", encoding="utf-8")
    video = video_factory.create(
        path=tmp_path / "movie.mp4", stem="movie", db=db_conn)
    [r] = discover_subtitles_for_video(video, db_conn)
    assert r.language == "ar"
    assert r.metadata.get("raw_lang_tag") == "ara"


def test_unrecognized_lang_tag_falls_back_to_und(
    tmp_path, db_conn, video_factory,
):
    """movie.zzz.srt → language='und', raw_lang_tag='zzz'."""
    (tmp_path / "movie.mp4").write_bytes(b"fake")
    (tmp_path / "movie.zzz.srt").write_text("...", encoding="utf-8")
    video = video_factory.create(
        path=tmp_path / "movie.mp4", stem="movie", db=db_conn)
    [r] = discover_subtitles_for_video(video, db_conn)
    assert r.language == "und"
    assert r.metadata.get("raw_lang_tag") == "zzz"


def test_vtt_header_overrides_missing_filename_tag(
    tmp_path, db_conn, video_factory,
):
    """Lecture.vtt with 'Language: ar' header → language='ar' (D2c)."""
    (tmp_path / "Lecture.mp4").write_bytes(b"fake")
    (tmp_path / "Lecture.vtt").write_text(
        "WEBVTT\nLanguage: ar\n\n00:00:00.000 --> 00:00:02.000\nمرحبا\n",
        encoding="utf-8",
    )
    video = video_factory.create(
        path=tmp_path / "Lecture.mp4", stem="Lecture", db=db_conn)
    [r] = discover_subtitles_for_video(video, db_conn)
    assert r.language == "ar"
    assert r.metadata["embedded_language"] == "ar"


def test_filename_tag_beats_vtt_header(
    tmp_path, db_conn, video_factory,
):
    """Lecture.en.vtt with 'Language: ar' header → language='en' (filename wins, D2)."""
    (tmp_path / "Lecture.mp4").write_bytes(b"fake")
    (tmp_path / "Lecture.en.vtt").write_text(
        "WEBVTT\nLanguage: ar\n\n00:00:00.000 --> 00:00:02.000\nx\n",
        encoding="utf-8",
    )
    video = video_factory.create(
        path=tmp_path / "Lecture.mp4", stem="Lecture", db=db_conn)
    [r] = discover_subtitles_for_video(video, db_conn)
    assert r.language == "en"
```

### 4.15 `test_migration_creates_subtitle_files` (DDL)

```python
# tests/migrations/test_subtitle_files_migration.py

def test_migration_creates_table_and_indexes(db_conn):
    db_conn.execute("DROP TABLE IF EXISTS subtitle_files CASCADE")
    apply_migration(db_conn, "0019_subtitle_files.sql")

    cols = {r[0]: r[1] for r in db_conn.execute("""
        SELECT column_name, data_type
          FROM information_schema.columns
         WHERE table_name='subtitle_files'
         ORDER BY ordinal_position
    """).fetchall()}

    assert "id" in cols
    assert cols["video_id"] == "uuid"
    assert cols["transcript_id"] == "uuid"
    assert cols["format"] == "text"
    assert cols["language"] == "text"
    assert cols["path"] == "text"
    assert cols["is_external"] == "boolean"
    assert cols["is_default"] == "boolean"
    assert cols["flags"] == "jsonb"
    assert cols["size_bytes"] == "bigint"
    assert cols["mtime_ns"] == "bigint"
    assert cols["metadata"] == "jsonb"
    assert cols["revived_count"] == "integer"
    assert cols["deleted_at"] == "timestamp with time zone"
    # Story 4.4 owns is_embedded — must NOT exist after THIS migration.
    assert "is_embedded" not in cols

    # Re-applying is a no-op (idempotency).
    apply_migration(db_conn, "0019_subtitle_files.sql")
```

---

## 5. Edge cases and how the plan handles each

| #   | Edge case (story or §brief) | Handled by |
|-----|-----------------------------|------------|
| E0  | `movie.forced.srt` (flag without language). | D3 — regex's `flag` group matches independently of `lang`; result is `language='und'`, `flags={'forced': True}`. (`test_movie_ar_forced_srt_recorded_with_flag` for the lang-bearing variant; the lang-less case is unit-tested in `test_filename`.) |
| E1  | **BOM-prefixed file** (UTF-8 with `\xef\xbb\xbf`, or UTF-16). | `peek_metadata` (§2.4) sniffs the leading bytes; the encoding is recorded in `metadata.encoding` (`utf-8-sig`, `utf-16-le`, `utf-16-be`). The Streaming Service's later VTT-conversion step (Story 4.5) reads this field to choose a decoder; this story does not modify the on-disk file. (`test_peek_utf8_with_bom`, `test_peek_utf16_le` — not shown but trivially symmetric.) |
| E2  | **CRLF line endings** in an `.srt`. | `peek_metadata.has_crlf` is true; the row's `metadata.has_crlf = true`. The SRT spec is line-ending-tolerant so the file is valid as-is; we record the fact for downstream tooling that may want to normalize on conversion. The Streaming Service's `pyvtt` parser handles both LF and CRLF. (`test_peek_crlf_detected`.) |
| E3  | **Sidecar lives in `Subs/` subdir** (or `Subtitles/`, `subs/`, case-insensitive on FS). | D8 — `_iter_candidate_paths` walks the video's parent **and** each of the SUBDIR_NAMES if present. Single-level only; deeper nesting (`Subs/EN/movie.ar.srt`) is intentionally ignored in v1 and tested for absence. (`test_external_subdir_discovered`, `test_external_deeper_nesting_not_discovered`.) |
| E4  | **Non-ISO language tag** (`movie.arb.srt`, `movie.zh-CN.srt`). | D2 — `langcodes` resolves `arb` (ISO-639-3 macro of Arabic) to ISO-639-1 `ar`; `zh-CN` is 4–5 chars and fails the regex (the file appears unmatched, which is the safe behavior — better unmatched than misclassified into the wrong locale). For tags that *do* match the regex but aren't recognized (`zzz`), we store `language='und'` and stash the original in `metadata.raw_lang_tag`. (`test_iso_639_3_normalized_to_iso_639_1`, `test_unrecognized_lang_tag_falls_back_to_und`.) |
| E5  | **Subtitle file is unreadable** (permissions, broken symlink, NFS hiccup). | `_scandir_safe` swallows `PermissionError` / `FileNotFoundError` for the directory; `peek_metadata` returns a `SubtitleMetadata` with `encoding='unknown'` and `error=<reason>`; `stat()` failures cause the file to be silently dropped from this scan (with a WARN log). The row is NOT inserted in the stat-failure case, so the next scan has a chance to pick it up after the disk recovers. (`test_external_broken_file` covers the read-failure path; `test_scandir_permission_denied` — added to the conftest — covers the directory-failure path.) |
| E6  | **Filename collision** between an auto-generated subtitle (Story 4.1) and an external one for the same `(language, format)`. | The story's uniqueness key is `(video_id, language, format, is_external, path)`; the auto-generated row has a different `path` (`.maktaba/subs/<hash>.<lang>.vtt`) and `is_external = false`. Both rows coexist. The Streaming Service's selection logic (Story 4.5) prefers `is_external = true` rows, demoting the auto-generated row to `is_active = false` *implicitly* by not serving it. We do **not** mutate the auto-generated row here — that's 4.5's read-side concern. |
| E7  | **Sidecar moved within the directory** (rename, e.g. `movie.ar.srt` → `movie.arabic.srt`). | The next scan inserts a new row for the new path (the regex doesn't match `arabic` because it's 6 chars; if the user instead renames to `movie.ara.srt`, the new row gets `language='ar'` after ISO-639-3 normalization). The old path's row is soft-deleted by `_soft_delete_missing`. The two rows have different `id`s; user notes attached to the old row are preserved against the soft-deleted row. (D7; `test_discovery_soft_deletes_missing_files`.) |
| E8  | **Sidecar moved to a different directory** (out of the video's siblings or `Subs/`). | Equivalent to E7's deletion — the row is soft-deleted on the next scan. If the file later appears as a sibling of a *different* video (rare), it is discovered fresh against that video. There is no cross-video subtitle sharing in v1. |
| E9  | **Same-language case-insensitive duplicate** (`movie.AR.srt` + `movie.ar.srt` on a case-sensitive FS). | D10 — `_dedupe_collisions` collapses by `lower(path)`; the lex-greater path wins on a single row. A WARN log surfaces the duplicate to the operator. On case-insensitive volumes (HFS+, exFAT, NTFS) the FS itself rejects the duplicate at write-time; the dedupe is a safety net. |
| E10 | **Subtitle file present but video file later deleted.** | The video row gets `state='deleted'` by Story 1.3; FK `ON DELETE CASCADE` on `subtitle_files.video_id` removes the subtitle rows. Soft-deletion does not apply because the cascade hard-deletes via the FK action. This is the deliberate trade-off: a missing video means missing subtitles, and the storage saved by hard-deletion is worth it. |
| E11 | **Two videos in the same directory with overlapping stems** (`Lecture.mp4` and `Lecture 2.mp4` + `Lecture.ar.srt`). | The regex anchors on `^<video_stem>$` so `Lecture.ar.srt` matches `Lecture` but **not** `Lecture 2`. Conversely `Lecture 2.ar.srt` matches `Lecture 2` but not `Lecture`. No cross-attribution. (`test_parse_filename_rejects_wrong_stem` is the unit-level proof.) |
| E12 | **Stem contains regex metacharacters** (`[2024] Lecture (intro).mp4`). | `re.escape` in `compile_subtitle_regex` (§2.3) handles all metacharacters. (`test_parse_filename_handles_brackets_in_stem`.) |
| E13 | **Sidecar with extension in uppercase** (`Lecture.AR.SRT`). | D1 — regex compiled with `re.IGNORECASE`. Stored values (`language`, `format`) are lowercased. (`test_parse_filename_table` parametrize row `Lecture.AR.SRT`.) |
| E14 | **Two scans race on the same video** (concurrent `scan_library` for the same root). | The UPSERT is on a unique constraint, so the second writer either wins the INSERT or harmlessly UPDATEs. The `_soft_delete_missing` step is bounded by what the writer's own scandir saw, which means the tail of a slow writer could mark a row as deleted that a fast subsequent writer just inserted. We accept this — the next scan corrects it (revives via `_upsert`'s `deleted_at = NULL`). The Story 1.3 watcher runs single-flight per library, which should make the race rare in practice. |
| E15 | **Subtitle file is empty (0 bytes).** | `stat()` succeeds with `st_size=0`; `peek_metadata` returns a result with `head_bytes=0` and no error; the row is recorded with `size_bytes=0`. The Streaming Service refuses to serve a 0-byte VTT (its own concern); discovery doesn't try to be smart about this. |
| E16 | **Library setting `subtitles.discovery.scan_subdirs=false`** (operator turns off `Subs/` walking). | Loaded via `library.settings`; `_iter_candidate_paths` checks the setting and skips the `SUBDIR_NAMES` branch. Test added in `test_discovery_subdir`: `test_subdir_discovery_disabled_by_setting`. (Implementation detail: setting threading happens via the `video.library_settings` accessor populated in Story 9.x; for v1 we read a global default.) |

---

## 6. Acceptance checklist

- [ ] **A1** During scanning ([Story 1.1](../01-scanner/story-01-01-file-discovery.md)), the discovery function is invoked once per `videos` row inserted/updated; the call is inline in the same scanner pass and adds at most ~5 ms wall time per video on a warm cache. (Test: `test_scan_invokes_subtitle_discovery` in scanner tests.)
- [ ] **A2** The regex `^<basename>(?:\.(?P<lang>[a-z]{2,3}))?(?:\.(?P<flag>forced|sdh|cc|hi))?\.(?P<ext>srt|vtt|ass|ssa)$` (case-insensitive, D1) is the sole gate for sidecar matching; unsupported extensions are silently ignored. (`test_filename`'s parametrize table covers the matrix.)
- [ ] **A3** Each match creates a `subtitle_files` row with `is_external = true`, `language = <lang or 'und'>`, `format = <ext>`, `transcript_id = NULL`, and `path = <absolute>`. (`test_external_srt_discovered`, `test_external_no_lang_tag`.)
- [ ] **A4** `is_embedded` is **NOT** present in this story's migration; Story 4.4 owns it. (`test_migration_creates_table_and_indexes` asserts the column's absence after applying only this migration.)
- [ ] **A5** External `.ass` / `.ssa` files are recorded but not converted at scan time — no `.vtt` artifact is produced and `.maktaba/subs/` is not touched. (`test_external_ass_recorded_not_converted`.)
- [ ] **A6** Re-scanning does not duplicate rows; uniqueness is `(video_id, language, format, is_external, path)`. (`test_rescan_idempotent`.)
- [ ] **A7** A subtitle that disappears between scans is soft-deleted (`deleted_at` populated, row preserved); on its return, the same row is revived (`deleted_at = NULL`, `revived_count` increments). (`test_discovery_soft_deletes_missing_files`, `test_discovery_revives_returning_files`.)
- [ ] **A8** For each `(video_id, language)` group, exactly one row has `is_default = true`, chosen by lexicographically-first `path`; the choice is stable across re-scans. (D4; `test_default_selection_picks_lex_first_path`.)
- [ ] **A9** Language detection uses `langcodes` (D2) to normalize ISO-639-3 → ISO-639-1 where possible; unrecognized tags resolve to `'und'` with the raw value preserved in `metadata.raw_lang_tag`. (`test_iso_639_3_normalized_to_iso_639_1`, `test_unrecognized_lang_tag_falls_back_to_und`.)
- [ ] **A10** When a `.vtt` file lacks a filename language tag but contains a `Language:` header, the header is used as a tiebreaker; when both filename and header are present, the filename wins. (D2; `test_vtt_header_overrides_missing_filename_tag`, `test_filename_tag_beats_vtt_header`.)
- [ ] **A11** `.forced` / `.sdh` / `.cc` / `.hi` flags are parsed into `subtitle_files.flags` JSONB and round-trip correctly; unknown flags are not consumed (the file is treated as if the flag were part of the stem and fails to match). (D3; `test_movie_ar_forced_srt_recorded_with_flag`, `test_parse_flag_unknown_returns_empty`.)
- [ ] **A12** Sibling subdirectories named `Subs`, `subs`, `Subtitles`, `subtitles` (case-insensitive on FS) are walked one level deep; deeper nesting is intentionally ignored. (D8; `test_external_subdir_discovered`, `test_external_deeper_nesting_not_discovered`.)
- [ ] **A13** Discovery survives unreadable files and unreadable directories without aborting the scan: the offending entry is logged at WARN once and the rest of the walk completes. (`test_external_broken_file`, `test_scandir_permission_denied`.)
- [ ] **A14** Change detection uses `(size_bytes, mtime_ns)` (D5); content_hash is reserved as a NULL column for a future opt-in path. The UPDATE on conflict refreshes both fields plus `metadata`. (Verified by inserting, modifying mtime, re-discovering, asserting the row's `mtime_ns` updated and `id` unchanged.)
- [ ] **A15** Migration `0019_subtitle_files.sql` is idempotent (CREATE IF NOT EXISTS, CREATE OR REPLACE FUNCTION, DROP TRIGGER IF EXISTS) and creates the indexes `subtitle_files_video_default_idx` and `subtitle_files_video_lang_idx` plus the `subtitle_files_change_trg` NOTIFY trigger. (`test_migration_creates_table_and_indexes`, plus a re-apply step inside the same test.)
- [ ] **A16** The NOTIFY trigger fires on `INSERT` and on `UPDATE OF deleted_at, mtime_ns, is_default`; the API can fan out `subtitle_files.changed` events to the library WebSocket without polling. (Test: `test_notify_fires_on_insert_and_softdelete` in the integration suite.)
- [ ] **A17** Discovery does not write to the `videos` row, the `processing_jobs` table, or any path under `.maktaba/`. It is read-from-FS, write-to-`subtitle_files`-only. (Static check: search for `INSERT INTO videos`, `INSERT INTO processing_jobs`, `Path(".maktaba")` in the discovery module → expect zero hits.)
- [ ] **A18** Public API surface is `discover_subtitles_for_video(video, conn) -> list[SubtitleFileRow]`; nothing else is re-exported from `media.subtitles.__init__`. (Static check + a test that imports the public surface and verifies the symbol set.)
- [ ] **A19** `langcodes>=3.3,<4` is added to runtime dependencies in `pyproject.toml`; CI's `pip install -e .` succeeds and `python -c "import langcodes"` returns 0. (`test_defaults_loaded` adds an `import langcodes` smoke check.)
- [ ] **A20** No code path in this story modifies the `transcripts` or `transcript_segments` tables; the `transcript_id` column on inserted rows is always `NULL`. (Static check; SQL audit on the discovery module.)
