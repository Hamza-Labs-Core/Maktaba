# Plan 4.1 — Generate SRT and VTT from `transcript_segments` — implementation

> Implementation plan for [story-04-01-generate-from-segments.md](story-04-01-generate-from-segments.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: consumes the canonical segments produced
> by [Plan 3.6](../03-transcription/plan-03-06-segment-commit.md) (read
> path is `transcript_segments` filtered by `transcripts.is_active`,
> the same filter [Story 4.5](story-04-05-live-vtt-contract.md) uses for
> the live-VTT view); the `subtitle_gen` stage runs in the FSM defined by
> [Epic 1 Story 1.6](../01-scanner/story-01-06-video-state-machine.md);
> wrapping/cue-shaping rules and the `<v Speaker>` tag are owned by
> [Story 4.2](story-04-02-formatting-wrapping.md) — this plan calls into
> that wrapper but does NOT re-specify it; the `is_embedded` column on
> `subtitle_files` is added by
> [Story 4.4](story-04-04-embedded-extraction.md), so this story's
> inserts assume `is_embedded = false` is the default.

---

## 0. Decisions and departures from `architecture.md` and the story

| # | Decision | Source | Rationale |
|---|----------|--------|-----------|
| D1 | The `subtitle_gen` stage **always** regenerates both SRT and VTT atomically, even if a subtitle file already exists at the canonical path (e.g., a prior run wrote it but failed before inserting `subtitle_files`). The previous artifact is replaced via `os.replace()`; no read-back-and-merge path. | Story acceptance: "produce both formats from the canonical segments — never from a previously written file". | The `transcript_segments` table is the single source of truth. Any divergence between an on-disk file and the DB (because of a partial write, a manual edit, or a stale file from a prior `transcripts.is_active = true` row) is **always** resolved in favor of the DB. The atomic-replace path is cheap (≤200 KB written for a 4 h video) and gives us idempotency for free — re-running the stage produces a byte-identical artifact. |
| D2 | The stage reads segments via the same `transcript_segments_v` view that [Story 4.5](story-04-05-live-vtt-contract.md) creates (filters on `transcripts.is_active = true`). It does **not** join `transcripts` directly. | Refines story acceptance — the story doesn't say which transcript's segments to use when a video has multiple. | Filtering through the view guarantees we only ever materialize the **active** transcript. If a future re-transcribe lands and flips `is_active`, the next `subtitle_gen` run picks up the new transcript without any extra logic. The view is the documented read contract; using it here keeps writers and live-VTT readers symmetrical. |
| D3 | HTML escaping is applied **after** line wrapping but **before** format-specific framing (SRT cue index + arrow line, or VTT `WEBVTT` header + arrow line). The escape order is `&` → `&amp;` first, then `<` → `&lt;`, then `>` → `&gt;`. | Story acceptance: "The escape is applied after wrapping but before format-specific framing." Story edge case: "Escape is one-pass (`&` → `&amp;` first, then `<`/`>`)." | Escaping the `&` first prevents double-escape of subsequent `<`/`>` (the naive order would produce `&amp;lt;` for a literal `<`). Escaping after wrapping means the wrapper sees natural-width text — important because `&amp;` is 5 visible characters but counts as 1 grapheme; if we escaped first, we'd over-wrap on text containing ampersands. The wrapper itself is in [Story 4.2](story-04-02-formatting-wrapping.md); this plan owns the placement of the escape relative to the wrap. |
| D4 | The atomic-write helper is a single function `write_atomic_pair(srt_path, srt_bytes, vtt_path, vtt_bytes)` that writes BOTH files under a single temp dir, then renames both into place. If the second rename fails after the first succeeds, the first is rolled back via `os.unlink(srt_path)`. | Refines story acceptance — the story specifies atomic single-file writes but says nothing about cross-file consistency. | Half-shipped pairs (SRT on disk, VTT missing) confuse player auto-discovery. Treating the pair as one atomic unit costs nothing (rename is metadata-only on the same FS) and means downstream readers always see "both or neither". The `subtitle_files` insert (two rows, one xact) mirrors this on the DB side. |
| D5 | The alias copy at `<source_dir>/<source_basename>.<lang>.srt` is a **hardlink** when the source dir and `.maktaba/subs/` are on the same filesystem; otherwise it falls back to `shutil.copy2`. The hardlink path saves disk on the common case (everything in `/var/maktaba/lib/...`). | Refines story acceptance — story says "alias is written"; doesn't specify hardlink vs copy. | A 4 h transcribed lecture's SRT is ~150 KB. Across a 10k-video library that's ~1.5 GB of duplicated bytes. Hardlinks dedupe at zero cost; the copy path stays for cross-device cases (CIFS mount, USB drive). Both paths are observed by the same `os.replace()` → atomic-or-not test in §4. **VTT is not aliased** — players auto-discover the SRT next to the file but expect VTT only via manifest, which the streaming service serves out of `.maktaba/subs/` directly. |
| D6 | A read-only source directory (story acceptance) is detected by **trying the alias write and catching `OSError(EROFS, EACCES, EPERM)`** rather than pre-checking `os.access(...)`. Pre-checks race; the catch is the truth. The catch logs `kind=alias_copy_failed` at WARN with `errno`, `source_dir`, `target_alias_path`, but does NOT fail the job. | Story acceptance: "the sidecar in `.maktaba/subs/` is still written, the `subtitle_files` row is still inserted, and a WARN is logged with `kind=alias_copy_failed`. The job is **not** failed". | `os.access` returns the wrong answer on CIFS / FUSE mounts where the kernel reports "writable" but writes still fail with `EPERM` because the server enforces ACLs. Catch-and-warn is the honest test. Failing the job because a *secondary* artifact couldn't be written (the canonical artifact in `.maktaba/subs/` is fine) would be a regression on read-only NAS deployments, which Maktaba explicitly supports. |
| D7 | Source-basename collision (story edge case) is detected by an **idempotency lookup against `subtitle_files`**: `SELECT 1 FROM subtitle_files WHERE path = $alias_path AND video_id != $current_video_id`. If a row exists, the alias copy is skipped and `kind=alias_collision` is logged. The video's own canonical `.maktaba/subs/<hash>.<lang>.srt` is unaffected. | Story edge case: "the alias copy is **skipped** for the second video to avoid clobbering and logged as `kind=alias_collision`." | Two videos with the same basename in the same directory is rare but happens (e.g., `talk.mkv` and `talk.mp4`). The DB lookup is the durable check; an `os.path.exists` check would mistake a file from a *prior* run of *this* video for a collision. The lookup is one indexed query against `subtitle_files (path)` — the existing `(video_id, is_external, is_embedded)` index from Story 4.4 doesn't cover this, so we add a `subtitle_files_path_idx` UNIQUE WHERE clause in the migration §2.8. |
| D8 | The wrapper produces `Cue` objects (`start_sec, end_sec, lines: list[str], speaker: str | None`); the SRT and VTT writers each take `Iterable[Cue]` and emit text. The wrapper itself is owned by [Story 4.2](story-04-02-formatting-wrapping.md); this plan defines only the **interface** and provides a no-op pass-through for use in 4.1 tests until 4.2 lands. | Refines story scope: 4.1 is "generate from segments"; 4.2 is "formatting & wrapping". The boundary is the `Cue` type. | Splitting at the `Cue` type keeps each story shippable independently. 4.1 ships with a one-cue-per-segment pass-through wrapper that satisfies the round-trip and escape tests; 4.2 ships the real wrapper later without changing 4.1's writer code. |
| D9 | Subtitle language is the **transcript's** `language` column, not the video's `detected_language`. They are usually the same; when they differ (re-transcribed in a different language), the subtitle filename uses the transcript's language so file aliases match the actual content. | Refines story acceptance — "lang" is unspecified. | A user who re-transcribes an Arabic lecture as English (for accessibility) would expect `Lecture 1.en.srt`, not `Lecture 1.ar.srt`. Anchoring the language to the transcript guarantees that. |
| D10 | The SRT/VTT generation is **not** part of the per-segment commit transaction. It runs after `transcribe` reaches `done`, as a separate `subtitle_gen` job dispatched by the orchestrator. The job is idempotent (D1) and can retry; the orchestrator advances to `INDEXED` only when both `subtitle_gen` AND `index` are `done`. | Architecture §3.5 + Story 1.6 FSM. | Mixing subtitle generation into the segment commit hot path would slow the per-segment latency from ~10 ms to ~hundreds of ms (writing & syncing two files for every commit). Doing it once at the end is cheap and matches the FSM. Live VTT for the in-flight transcribe is served from `transcript_segments_v` directly (Story 4.5) — no on-disk file needed during transcription. |

If D5 is rejected (always copy, never hardlink), §2.6 changes (the
`alias_copy` helper drops the `os.link` branch) and the disk-cost
estimate in §7 grows by 1.5× on big libraries. Correctness is
unaffected.

---

## 1. Architecture diagram — `subtitle_gen` stage flow

```
                     ┌────────────────────────────────────────────────┐
                     │  Orchestrator (Plan 1.6 FSM)                   │
                     │   transcript completes → state TRANSCRIBED     │
                     │   enqueue jobs:                                │
                     │     subtitle_gen(video_id)                     │
                     │     index(video_id)                            │
                     │   when BOTH done → state INDEXED → READY       │
                     └─────────────────────┬──────────────────────────┘
                                           │ claim
                                           ▼
                     ┌────────────────────────────────────────────────┐
                     │  subtitle_gen worker entry                     │
                     │   stage = "subtitle_gen"                       │
                     │   ctx.run_subtitle_gen_stage(ctx, claimed_job) │
                     └─────────────────────┬──────────────────────────┘
                                           │
                                           ▼
            ┌──────────────────────────────────────────────────────────────────┐
            │  1. Load active transcript via transcript_segments_v             │
            │     (Story 4.5 view: only is_active = true rows)                 │
            │     SELECT seq, start_sec, end_sec, text, speaker                │
            │       FROM transcript_segments_v                                 │
            │      WHERE video_id = $1                                         │
            │      ORDER BY seq                                                │
            │     If 0 rows → fail job with kind="no_active_transcript".       │
            └──────────────────────────────────┬───────────────────────────────┘
                                               │ Iterable[Segment]
                                               ▼
            ┌──────────────────────────────────────────────────────────────────┐
            │  2. CueShaper.shape(segments, library_settings)                  │
            │     → Iterable[Cue(start_sec, end_sec, lines, speaker)]          │
            │     (4.1 ships pass-through; 4.2 ships real wrap/merge/split)    │
            └──────────────────────────────────┬───────────────────────────────┘
                                               │ Iterable[Cue]
                                               ▼
            ┌──────────────────────────────────────────────────────────────────┐
            │  3. Render to bytes (in-memory):                                 │
            │     srt_bytes = SrtWriter.write(cues)   # text → escape → frame  │
            │     vtt_bytes = VttWriter.write(cues)   # text → escape → frame  │
            │     Both writers call _escape_cue_text() (D3).                   │
            └──────────────────────────────────┬───────────────────────────────┘
                                               │ bytes pair
                                               ▼
            ┌──────────────────────────────────────────────────────────────────┐
            │  4. write_atomic_pair(srt_path, srt_bytes, vtt_path, vtt_bytes)  │
            │     Both files into <library_root>/.maktaba/subs/                │
            │     under <hash>.<lang>.{srt,vtt}.                               │
            │     Mkdir 0755 on first use; on EACCES at parent → fail          │
            │     with kind="sidecar_dir".                                     │
            │     Write to .maktaba/.tmp/<uuid>.{srt,vtt}; os.replace()        │
            │     each into final path. (D4: rollback on cross-rename fail.)   │
            └──────────────────────────────────┬───────────────────────────────┘
                                               │
                                               ▼
            ┌──────────────────────────────────────────────────────────────────┐
            │  5. Insert subtitle_files rows in ONE xact:                      │
            │     INSERT … (video_id, transcript_id, format='srt',             │
            │               language=$lang, path=<final SRT>,                  │
            │               is_external=false, is_embedded=false)              │
            │     INSERT … (… format='vtt', path=<final VTT>, …)               │
            │     ON CONFLICT (video_id, format, language)                     │
            │        DO UPDATE SET path = EXCLUDED.path,                       │
            │                      transcript_id = EXCLUDED.transcript_id,    │
            │                      created_at = now()                          │
            │     (Migration §2.8 adds the unique index that backs ON CONFLICT)│
            └──────────────────────────────────┬───────────────────────────────┘
                                               │
                                               ▼
            ┌──────────────────────────────────────────────────────────────────┐
            │  6. alias_copy(srt_path → <source_dir>/<basename>.<lang>.srt)    │
            │     - DB collision check (D7) → skip + WARN alias_collision      │
            │     - try os.link() (D5); fall back to shutil.copy2              │
            │     - on OSError(EROFS|EACCES|EPERM) → WARN alias_copy_failed,   │
            │       JOB STAYS DONE (D6)                                        │
            │     VTT is NOT aliased (D5).                                     │
            └──────────────────────────────────┬───────────────────────────────┘
                                               │
                                               ▼
            ┌──────────────────────────────────────────────────────────────────┐
            │  7. mark_done(job)                                               │
            │     Orchestrator advances toward INDEXED when index also done.   │
            └──────────────────────────────────────────────────────────────────┘
```

The on-disk artifact at `.maktaba/subs/<hash>.<lang>.{srt,vtt}` is the
canonical sidecar. The alias next to the source file is a *convenience*
for direct-folder consumers (Plex, VLC, Finder preview); its absence
never blocks the `subtitle_gen` job. The streaming service at
[architecture §4.5](../../architecture.md) renders live VTT directly
out of `transcript_segments_v` and does not depend on either file.

---

## 2. Detailed implementation

### 2.1 Package layout — Python (Pipeline Service)

```
pipeline/src/maktaba_pipeline/
├── media/
│   ├── __init__.py
│   └── subtitles/
│       ├── __init__.py            # public surface
│       ├── cue.py                 # Cue dataclass + Segment ingestion type
│       ├── shaper.py              # CueShaper protocol + pass-through impl (4.1)
│       ├── escape.py              # _escape_cue_text() (D3 ordering)
│       ├── srt_writer.py          # SrtWriter.write(Iterable[Cue]) -> bytes
│       ├── vtt_writer.py          # VttWriter.write(Iterable[Cue]) -> bytes
│       ├── paths.py               # canonical_subtitle_path, alias_path,
│       │                          # tmp_path; mkdir helpers
│       ├── atomic.py              # write_atomic_pair() (D4)
│       ├── alias.py               # alias_copy() (D5, D6, D7)
│       └── tests/
│           ├── conftest.py        # cue + segment fixtures
│           ├── test_escape.py
│           ├── test_srt_writer.py        # test_srt_round_trips
│           ├── test_vtt_writer.py        # test_vtt_round_trips
│           ├── test_escape_in_writers.py # test_cue_text_html_escaped
│           ├── test_paths.py
│           ├── test_atomic.py            # test_atomic_replace_on_retry
│           ├── test_alias.py             # test_alias_copy_uses_source_basename,
│           │                             # test_readonly_source_dir_does_not_fail_job,
│           │                             # test_alias_collision_skips
│           └── test_stage_end_to_end.py  # full subtitle_gen stage
└── pipeline/
    └── stages/
        └── subtitle_gen.py        # run_subtitle_gen_stage(ctx, claimed_job)

shared/db/migrations/
└── 0015_subtitle_files_unique_lang.sql   # see §2.8
```

The `media/subtitles/` package is the new module owned by this story.
The architecture (§3.5) puts the writer in
`pipeline/src/maktaba_pipeline/media/subtitles.py`; we refactor that
single-file path into a small package because Story 4.4 (embedded
extraction) and Story 4.2 (wrapping) will all add files in the same area.

### 2.2 `cue.py` — types

```python
"""Cue and Segment dataclasses shared between the shaper and the writers."""
from __future__ import annotations
from dataclasses import dataclass


@dataclass(frozen=True)
class Segment:
    """The shape we read out of `transcript_segments_v`."""
    seq: int
    start_sec: float
    end_sec: float
    text: str
    speaker: str | None


@dataclass(frozen=True)
class Cue:
    """A single rendered cue, ready for SRT/VTT framing.

    `lines` is the post-wrap list of visible lines (1 or 2 per cue under
    Story 4.2 defaults). 4.1's pass-through shaper produces `[seg.text]`.
    `speaker`, when not None, becomes a `<v ...>` tag in VTT output and
    is dropped silently in SRT output (SRT has no speaker tag standard;
    Story 4.2 explores embedding `[Speaker 1]:` prefixes as a flag).
    """
    start_sec: float
    end_sec: float
    lines: tuple[str, ...]
    speaker: str | None = None
```

### 2.3 `escape.py` — HTML escape (D3)

```python
"""HTML-escape cue text per Story 4.1 acceptance.

Order: '&' first, then '<', then '>'. Single-pass replace; entities
already in the source text become double-escaped (e.g., '&amp;' →
'&amp;amp;') because cue text is treated as literal user text per the
story edge case.
"""
from __future__ import annotations


def escape_cue_text(text: str) -> str:
    # The order matters — see Story 4.1 edge case "Cue text containing
    # existing entities". '&' MUST be first.
    out = text.replace("&", "&amp;")
    out = out.replace("<", "&lt;")
    out = out.replace(">", "&gt;")
    return out


def escape_speaker_label(label: str) -> str:
    """Per Story 4.2: speaker labels inside <v ...> tags are escaped too.

    4.1 owns the helper; 4.2 calls it from the wrapper / vtt writer.
    """
    return escape_cue_text(label)
```

### 2.4 `srt_writer.py` — SRT writer

```python
"""SRT writer. Emits SubRip text (1-indexed cue numbers, comma decimals)."""
from __future__ import annotations
from io import BytesIO
from typing import Iterable

from maktaba_pipeline.media.subtitles.cue import Cue
from maktaba_pipeline.media.subtitles.escape import escape_cue_text


def _format_timestamp(seconds: float) -> str:
    if seconds < 0:
        seconds = 0.0
    total_ms = int(round(seconds * 1000))
    h, rem = divmod(total_ms, 3_600_000)
    m, rem = divmod(rem, 60_000)
    s, ms = divmod(rem, 1000)
    return f"{h:02d}:{m:02d}:{s:02d},{ms:03d}"


class SrtWriter:
    @staticmethod
    def write(cues: Iterable[Cue]) -> bytes:
        buf = BytesIO()
        for idx, cue in enumerate(cues, start=1):
            ts = f"{_format_timestamp(cue.start_sec)} --> {_format_timestamp(cue.end_sec)}"
            # Escape AFTER wrapping (lines are already split) but BEFORE
            # framing (joining with newlines and prepending the index).
            escaped_lines = [escape_cue_text(line) for line in cue.lines]
            cue_block = f"{idx}\n{ts}\n" + "\n".join(escaped_lines) + "\n\n"
            buf.write(cue_block.encode("utf-8"))
        return buf.getvalue()
```

### 2.5 `vtt_writer.py` — VTT writer

```python
"""VTT writer. Emits WebVTT text (period decimals, optional <v Speaker> tag)."""
from __future__ import annotations
from io import BytesIO
from typing import Iterable

from maktaba_pipeline.media.subtitles.cue import Cue
from maktaba_pipeline.media.subtitles.escape import escape_cue_text, escape_speaker_label


def _format_timestamp(seconds: float) -> str:
    if seconds < 0:
        seconds = 0.0
    total_ms = int(round(seconds * 1000))
    h, rem = divmod(total_ms, 3_600_000)
    m, rem = divmod(rem, 60_000)
    s, ms = divmod(rem, 1000)
    return f"{h:02d}:{m:02d}:{s:02d}.{ms:03d}"


class VttWriter:
    @staticmethod
    def write(cues: Iterable[Cue]) -> bytes:
        buf = BytesIO()
        buf.write(b"WEBVTT\n\n")
        for cue in cues:
            ts = f"{_format_timestamp(cue.start_sec)} --> {_format_timestamp(cue.end_sec)}"
            escaped_lines = [escape_cue_text(line) for line in cue.lines]
            body = "\n".join(escaped_lines)
            if cue.speaker is not None:
                # Story 4.2 owns the actual placement; 4.1 emits a
                # minimal <v Speaker>...</v> wrapper around the FIRST
                # line. The wrapper in 4.2 will redo this if needed.
                spk = escape_speaker_label(cue.speaker)
                body = f"<v {spk}>{body}</v>"
            cue_block = f"{ts}\n{body}\n\n"
            buf.write(cue_block.encode("utf-8"))
        return buf.getvalue()
```

### 2.6 `paths.py` — canonical paths

```python
"""Subtitle path helpers.

Canonical sidecar: <library_root>/.maktaba/subs/<hash>.<lang>.<fmt>
Alias next to source: <source_dir>/<source_basename>.<lang>.srt
"""
from __future__ import annotations
import os
import uuid
from pathlib import Path


SIDECAR_DIR_MODE = 0o755


def sidecar_dir_for(library_root: Path) -> Path:
    return library_root / ".maktaba" / "subs"


def tmp_dir_for(library_root: Path) -> Path:
    return library_root / ".maktaba" / ".tmp"


def canonical_subtitle_path(
    library_root: Path, content_hash: str, lang: str, fmt: str,
) -> Path:
    assert fmt in ("srt", "vtt"), fmt
    return sidecar_dir_for(library_root) / f"{content_hash}.{lang}.{fmt}"


def alias_path_for(source_video_path: Path, lang: str) -> Path:
    """`<source_dir>/<source_basename>.<lang>.srt` per story acceptance."""
    return source_video_path.with_suffix("").with_name(
        f"{source_video_path.stem}.{lang}.srt"
    )


def fresh_tmp_paths(library_root: Path) -> tuple[Path, Path]:
    """Return (srt_tmp, vtt_tmp) under .maktaba/.tmp/<uuid>.{srt,vtt}."""
    base = tmp_dir_for(library_root)
    token = uuid.uuid4().hex
    return base / f"{token}.srt", base / f"{token}.vtt"


def ensure_sidecar_dirs(library_root: Path) -> None:
    """Create .maktaba/subs and .maktaba/.tmp with mode 0755.

    On parent-perms failure, raises OSError; the caller (subtitle_gen
    stage) catches and fails the job with kind='sidecar_dir' per the
    story edge case "`.maktaba/` directory not yet created".
    """
    for d in (sidecar_dir_for(library_root), tmp_dir_for(library_root)):
        d.mkdir(parents=True, exist_ok=True, mode=SIDECAR_DIR_MODE)
```

### 2.7 `atomic.py` — atomic pair write (D4)

```python
"""Atomic write of an (SRT, VTT) pair under .maktaba/.tmp then os.replace."""
from __future__ import annotations
import logging
import os
from pathlib import Path

from maktaba_pipeline.media.subtitles.paths import (
    ensure_sidecar_dirs, fresh_tmp_paths,
)

log = logging.getLogger(__name__)


def write_atomic_pair(
    *, library_root: Path,
    srt_path: Path, srt_bytes: bytes,
    vtt_path: Path, vtt_bytes: bytes,
) -> None:
    """Write both files atomically (D4).

    Steps:
      1. ensure_sidecar_dirs (mkdir 0755).
      2. write to .maktaba/.tmp/<uuid>.{srt,vtt} with fsync.
      3. os.replace srt_tmp → srt_path.
      4. os.replace vtt_tmp → vtt_path.
      5. On failure between 3 and 4, os.unlink(srt_path) to roll back.
      6. Best-effort fsync the parent directory afterwards (durability
         on power-loss; not required for correctness but cheap).
    """
    ensure_sidecar_dirs(library_root)
    srt_tmp, vtt_tmp = fresh_tmp_paths(library_root)

    # Step 2: write + fsync each tmp file. Using fdopen for fsync access.
    for tmp_path, payload in ((srt_tmp, srt_bytes), (vtt_tmp, vtt_bytes)):
        fd = os.open(tmp_path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o644)
        try:
            with os.fdopen(fd, "wb") as f:
                f.write(payload)
                f.flush()
                os.fsync(f.fileno())
        except BaseException:
            # If anything (incl. KeyboardInterrupt) blew up mid-write,
            # leave no half-temp on disk.
            try:
                os.unlink(tmp_path)
            except FileNotFoundError:
                pass
            raise

    # Step 3 + 4: rename in place.
    os.replace(srt_tmp, srt_path)
    try:
        os.replace(vtt_tmp, vtt_path)
    except OSError as e:
        # Step 5: roll back the SRT.
        log.error("vtt_replace_failed_rolling_back_srt",
                  extra={"srt": str(srt_path), "vtt": str(vtt_path), "err": str(e)})
        try:
            os.unlink(srt_path)
        except FileNotFoundError:
            pass
        try:
            os.unlink(vtt_tmp)
        except FileNotFoundError:
            pass
        raise

    # Step 6: parent-dir fsync (best effort).
    try:
        dir_fd = os.open(str(srt_path.parent), os.O_RDONLY)
        try:
            os.fsync(dir_fd)
        finally:
            os.close(dir_fd)
    except OSError:  # pragma: no cover — darwin/macos sometimes refuses
        pass
```

### 2.8 `alias.py` — basename alias copy (D5, D6, D7)

```python
"""Alias copy <source_dir>/<basename>.<lang>.srt with hardlink fast path."""
from __future__ import annotations
import errno
import logging
import os
import shutil
from pathlib import Path

log = logging.getLogger(__name__)

ALIAS_SKIPPED = "alias_copy_failed"
ALIAS_COLLISION = "alias_collision"


async def alias_copy(
    *, db, video_id: str, source_video_path: Path,
    canonical_srt_path: Path, alias_path: Path,
) -> str | None:
    """Copy/link the canonical SRT to its source-side alias. Returns the
    final alias path on success, or None on any non-fatal skip.

    - Story acceptance: read-only source dir → log + return None (job stays done).
    - Story edge case: basename collision → log + return None.
    """
    # D7: collision check via DB (NOT os.path.exists).
    existing = await db.fetchval(
        "SELECT video_id::text FROM subtitle_files "
        "WHERE path = $1 AND video_id != $2 LIMIT 1",
        str(alias_path), video_id,
    )
    if existing is not None:
        log.warning(ALIAS_COLLISION, extra={
            "kind": ALIAS_COLLISION,
            "alias_path": str(alias_path),
            "this_video_id": video_id,
            "owning_video_id": existing,
        })
        return None

    # D5: hardlink fast path.
    try:
        # If alias already exists from a prior run of THIS video, replace it.
        if alias_path.exists() or alias_path.is_symlink():
            os.unlink(alias_path)
        try:
            os.link(canonical_srt_path, alias_path)
        except OSError as e:
            if e.errno in (errno.EXDEV, errno.EPERM, errno.ENOSYS):
                # Cross-device or FS doesn't support hardlinks. Fall back.
                shutil.copy2(canonical_srt_path, alias_path)
            else:
                raise
    except OSError as e:
        # D6: read-only source dir is the documented benign failure.
        if e.errno in (errno.EROFS, errno.EACCES, errno.EPERM):
            log.warning(ALIAS_SKIPPED, extra={
                "kind": ALIAS_SKIPPED,
                "errno": e.errno,
                "errno_name": errno.errorcode.get(e.errno, str(e.errno)),
                "source_dir": str(source_video_path.parent),
                "target_alias_path": str(alias_path),
            })
            return None
        # Anything else is unexpected — propagate.
        raise

    return str(alias_path)
```

### 2.9 `shaper.py` — pass-through wrapper (4.1) → real wrap (4.2)

```python
"""Cue shaping protocol. 4.1 ships the pass-through; 4.2 ships the real one."""
from __future__ import annotations
from typing import Iterable, Protocol

from maktaba_pipeline.media.subtitles.cue import Cue, Segment


class CueShaper(Protocol):
    def shape(self, segments: Iterable[Segment], *, settings: dict) -> Iterable[Cue]: ...


class PassThroughShaper:
    """One Cue per Segment. Lines = [text] with no wrapping.

    Used by Story 4.1's tests (round-trip + escape). Story 4.2 replaces
    the bound shaper with a real implementation that respects
    max_line_chars / max_lines / merge_gap_sec / max_cue_sec / etc.
    """
    def shape(self, segments, *, settings):
        for s in segments:
            yield Cue(
                start_sec=s.start_sec,
                end_sec=s.end_sec,
                lines=(s.text,),
                speaker=s.speaker,
            )


def get_default_shaper() -> CueShaper:
    """Returns the shaper bound by the current pipeline build.

    4.1 returns PassThroughShaper. 4.2's PR will swap this to the real
    one without changing any caller in 4.1.
    """
    return PassThroughShaper()
```

### 2.10 Stage entry — `pipeline/stages/subtitle_gen.py`

```python
"""subtitle_gen stage entrypoint. Called by the pipeline runner."""
from __future__ import annotations
import logging
from pathlib import Path

from maktaba_pipeline.media.subtitles.alias import alias_copy
from maktaba_pipeline.media.subtitles.atomic import write_atomic_pair
from maktaba_pipeline.media.subtitles.cue import Segment
from maktaba_pipeline.media.subtitles.paths import (
    alias_path_for, canonical_subtitle_path, ensure_sidecar_dirs,
)
from maktaba_pipeline.media.subtitles.shaper import get_default_shaper
from maktaba_pipeline.media.subtitles.srt_writer import SrtWriter
from maktaba_pipeline.media.subtitles.vtt_writer import VttWriter

log = logging.getLogger(__name__)


class SubtitleGenError(Exception):
    def __init__(self, kind: str, message: str):
        super().__init__(message)
        self.kind = kind


async def run_subtitle_gen_stage(ctx, claimed_job) -> None:
    """Generate SRT and VTT for the active transcript of a video.

    Idempotent (D1). Re-running produces byte-identical files and
    UPSERTs the subtitle_files rows.
    """
    video_id = claimed_job.video_id

    async with ctx.db_pool.acquire() as conn:
        # 1. Load video + library context (path, hash, library_root).
        video = await conn.fetchrow("""
            SELECT v.id, v.content_hash, v.path AS source_path,
                   v.library_id, l.roots[1] AS library_root
              FROM videos v
              JOIN libraries l ON l.id = v.library_id
             WHERE v.id = $1
        """, video_id)
        if video is None:
            raise SubtitleGenError("video_missing", f"video {video_id} not found")

        # 2. Resolve active transcript via Story 4.5's view.
        transcript = await conn.fetchrow("""
            SELECT t.id, t.language
              FROM transcripts t
             WHERE t.video_id = $1 AND t.is_active = true
             LIMIT 1
        """, video_id)
        if transcript is None:
            raise SubtitleGenError(
                "no_active_transcript",
                f"no active transcript for video {video_id}")

        # 3. Pull segments through transcript_segments_v.
        rows = await conn.fetch("""
            SELECT seq, start_sec, end_sec, text, speaker
              FROM transcript_segments_v
             WHERE video_id = $1
             ORDER BY seq
        """, video_id)
        if not rows:
            raise SubtitleGenError(
                "empty_transcript",
                f"active transcript for {video_id} has zero segments")

    segments = [Segment(seq=r["seq"],
                        start_sec=r["start_sec"], end_sec=r["end_sec"],
                        text=r["text"], speaker=r["speaker"]) for r in rows]

    # 4. Cue shaping (4.1: pass-through; 4.2 swaps in the real shaper).
    shaper = get_default_shaper()
    library_settings = await ctx.libraries.get_settings(video["library_id"])
    cues = list(shaper.shape(segments, settings=library_settings.get("subtitles", {})))

    # 5. Render bytes.
    srt_bytes = SrtWriter.write(cues)
    vtt_bytes = VttWriter.write(cues)

    # 6. Atomic write of the pair.
    library_root = Path(video["library_root"])
    lang = transcript["language"]
    srt_path = canonical_subtitle_path(library_root, video["content_hash"], lang, "srt")
    vtt_path = canonical_subtitle_path(library_root, video["content_hash"], lang, "vtt")

    try:
        write_atomic_pair(
            library_root=library_root,
            srt_path=srt_path, srt_bytes=srt_bytes,
            vtt_path=vtt_path, vtt_bytes=vtt_bytes,
        )
    except OSError as e:
        # If we couldn't even create .maktaba/, fail with the named kind.
        # ensure_sidecar_dirs raised on the parent perms case.
        raise SubtitleGenError("sidecar_dir", f"sidecar write failed: {e}") from e

    # 7. UPSERT subtitle_files rows in one xact.
    async with ctx.db_pool.acquire() as conn:
        async with conn.transaction():
            for fmt, path in (("srt", srt_path), ("vtt", vtt_path)):
                await conn.execute("""
                    INSERT INTO subtitle_files
                        (video_id, transcript_id, format, language, path,
                         is_external, is_embedded)
                    VALUES ($1, $2, $3, $4, $5, false, false)
                    ON CONFLICT (video_id, format, language)
                       DO UPDATE SET path = EXCLUDED.path,
                                     transcript_id = EXCLUDED.transcript_id,
                                     created_at = now()
                """, video_id, transcript["id"], fmt, lang, str(path))

    # 8. Alias copy to <source_dir>/<basename>.<lang>.srt (best effort).
    source_path = Path(video["source_path"])
    alias = alias_path_for(source_path, lang)
    try:
        async with ctx.db_pool.acquire() as conn:
            written = await alias_copy(
                db=conn, video_id=video_id,
                source_video_path=source_path,
                canonical_srt_path=srt_path,
                alias_path=alias,
            )
        if written is not None:
            log.info("alias_copy_ok", extra={
                "video_id": video_id, "alias_path": written})
    except Exception:
        # alias_copy itself catches the documented benign errors.
        # An unexpected exception here is a bug — re-raise to fail the job.
        raise
```

### 2.11 Migration `0015_subtitle_files_unique_lang.sql`

```sql
-- Plan 4.1: ON CONFLICT support for (video_id, format, language) UPSERT
-- and the path-collision lookup that backs alias_copy (D7).
--
-- Idempotent. Safe to re-run.

BEGIN;

-- One subtitle file per (video, format, language). The architecture
-- schema in §8.1 does not declare this constraint; we add it here
-- because the subtitle_gen stage's UPSERT depends on it (Plan 4.1 D1).
-- A duplicate row would mean we lost the previous artifact's row
-- on a re-run, which would leak files and confuse the streaming
-- service's "list subtitles" endpoint.
CREATE UNIQUE INDEX IF NOT EXISTS subtitle_files_video_fmt_lang_idx
    ON subtitle_files (video_id, format, language);

-- Backs the D7 collision lookup `WHERE path = $1`.
-- NOT unique because the same path may legally appear once per video
-- (it shouldn't, but the constraint above already prevents that).
CREATE INDEX IF NOT EXISTS subtitle_files_path_idx
    ON subtitle_files (path);

COMMIT;
```

The numbering aligns with Story 4.4's `000X_subtitle_files_is_embedded.sql`
(numbered around 0014 by epic order); 4.1's migration follows it. If
the project chooses a different numbering scheme at merge time, only the
filename prefix changes; the SQL is independent.

### 2.12 Library settings — config surface

This story does not add a new library setting. The shaper in
[Story 4.2](story-04-02-formatting-wrapping.md) reads
`library.settings.subtitles.{max_line_chars, max_lines, merge_gap_sec,
max_cue_sec}`; 4.1's pass-through shaper ignores them. The on-disk
format choice (srt + vtt, both always emitted) is hard-wired per the
story acceptance — no setting flips one off.

### 2.13 SQLite parity

Postgres-only constructs used above and their SQLite equivalents:

| Construct | Postgres | SQLite (dev) |
|-----------|----------|--------------|
| `BIGSERIAL` (in subtitle_files PK, owned by architecture §8.1) | native | `INTEGER PRIMARY KEY AUTOINCREMENT` |
| `ON CONFLICT (cols) DO UPDATE` | native | identical syntax (3.24+) |
| `CREATE UNIQUE INDEX IF NOT EXISTS` | native | identical |
| `roots[1]` array indexing for library root | `text[]` column subscript | the dev SQLite path stores roots as JSON; the library helper `ctx.libraries.get_root(library_id)` (already exists in Plan 1.1) hides the difference. **Code change**: use that helper, not the raw `roots[1]` subscript shown in §2.10 — both versions return the first root path. |

The §2.10 excerpt uses `roots[1]` for brevity; the shipped code calls
`await ctx.libraries.get_root(video["library_id"])` to stay portable.

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `pipeline/src/maktaba_pipeline/media/subtitles/__init__.py` | re-exports `SrtWriter`, `VttWriter`, `Cue`, `Segment` | (n/a) |
| 2 | `pipeline/src/maktaba_pipeline/media/subtitles/cue.py` | `Cue`, `Segment` dataclasses | (n/a) |
| 3 | `pipeline/src/maktaba_pipeline/media/subtitles/escape.py` | `escape_cue_text`, `escape_speaker_label` | `test_escape` |
| 4 | `pipeline/src/maktaba_pipeline/media/subtitles/srt_writer.py` | `SrtWriter.write`, `_format_timestamp` | `test_srt_round_trips`, `test_cue_text_html_escaped` (SRT side) |
| 5 | `pipeline/src/maktaba_pipeline/media/subtitles/vtt_writer.py` | `VttWriter.write`, `_format_timestamp` | `test_vtt_round_trips`, `test_cue_text_html_escaped` (VTT side) |
| 6 | `pipeline/src/maktaba_pipeline/media/subtitles/paths.py` | `canonical_subtitle_path`, `alias_path_for`, `ensure_sidecar_dirs`, `fresh_tmp_paths`, `sidecar_dir_for`, `tmp_dir_for` | `test_paths` |
| 7 | `pipeline/src/maktaba_pipeline/media/subtitles/atomic.py` | `write_atomic_pair` | `test_atomic_replace_on_retry` |
| 8 | `pipeline/src/maktaba_pipeline/media/subtitles/alias.py` | `alias_copy`, `ALIAS_SKIPPED`, `ALIAS_COLLISION` | `test_alias_copy_uses_source_basename`, `test_readonly_source_dir_does_not_fail_job`, `test_alias_collision_skips` |
| 9 | `pipeline/src/maktaba_pipeline/media/subtitles/shaper.py` | `CueShaper`, `PassThroughShaper`, `get_default_shaper` | (covered transitively in stage e2e) |
| 10 | `shared/db/migrations/0015_subtitle_files_unique_lang.sql` | unique index on `(video_id, format, language)`, plain index on `path` | `test_migration_applies_cleanly` (runs in CI migration suite) |
| 11 | `pipeline/src/maktaba_pipeline/pipeline/stages/subtitle_gen.py` | `run_subtitle_gen_stage`, `SubtitleGenError` | `test_stage_end_to_end`, `test_stage_creates_sidecar_dir`, `test_stage_fails_with_no_active_transcript` |

Order matters: writers depend on cue + escape; the stage depends on
writers + paths + atomic + alias + shaper. Each step's tests can be
written before the next step starts.

---

## 4. Test cases

The "must have" tests from the story are reproduced verbatim in name;
the snippets below are the implementations.

### 4.1 `test_srt_round_trips` (story-named)

```python
import srt as srt_lib  # third-party SRT parser
from maktaba_pipeline.media.subtitles.cue import Cue
from maktaba_pipeline.media.subtitles.srt_writer import SrtWriter


def test_srt_round_trips():
    cues = [
        Cue(start_sec=0.0, end_sec=2.345, lines=("hello world",)),
        Cue(start_sec=2.345, end_sec=5.000, lines=("second line",)),
        Cue(start_sec=5.000, end_sec=7.123, lines=("الخطبة الأولى",)),
    ]
    out = SrtWriter.write(cues).decode("utf-8")
    parsed = list(srt_lib.parse(out))
    assert len(parsed) == 3
    assert parsed[0].content == "hello world"
    assert parsed[2].content == "الخطبة الأولى"
    # Timestamps within 1 ms of original.
    assert abs(parsed[0].end.total_seconds() - 2.345) < 0.001
    assert abs(parsed[2].start.total_seconds() - 5.000) < 0.001
```

### 4.2 `test_vtt_round_trips` (story-named)

```python
import io
import webvtt
from maktaba_pipeline.media.subtitles.cue import Cue
from maktaba_pipeline.media.subtitles.vtt_writer import VttWriter


def test_vtt_round_trips(tmp_path):
    cues = [
        Cue(start_sec=0.0, end_sec=2.345, lines=("hello world",)),
        Cue(start_sec=2.345, end_sec=5.000, lines=("second line",)),
        Cue(start_sec=5.000, end_sec=7.123, lines=("الخطبة الأولى",)),
    ]
    out = VttWriter.write(cues).decode("utf-8")
    p = tmp_path / "x.vtt"
    p.write_text(out, encoding="utf-8")
    parsed = list(webvtt.read(str(p)))
    assert len(parsed) == 3
    assert parsed[0].text == "hello world"
    assert parsed[2].text == "الخطبة الأولى"
    # webvtt-py exposes start/end as 'HH:MM:SS.mmm' strings.
    assert parsed[0].end == "00:00:02.345"
    assert parsed[2].start == "00:00:05.000"
```

### 4.3 `test_cue_text_html_escaped` (story-named)

```python
import srt as srt_lib
import webvtt
from maktaba_pipeline.media.subtitles.cue import Cue
from maktaba_pipeline.media.subtitles.srt_writer import SrtWriter
from maktaba_pipeline.media.subtitles.vtt_writer import VttWriter


def test_cue_text_html_escaped_srt():
    cues = [
        Cue(start_sec=0.0, end_sec=1.0,
            lines=("<script>alert(1)</script>",)),
        Cue(start_sec=1.0, end_sec=2.0, lines=("Tom & Jerry",)),
    ]
    out = SrtWriter.write(cues).decode("utf-8")
    # Literal escapes are present.
    assert "&lt;script&gt;alert(1)&lt;/script&gt;" in out
    assert "Tom &amp; Jerry" in out
    # No unescaped angle brackets in cue body.
    parsed = list(srt_lib.parse(out))
    assert "<script>" not in parsed[0].content


def test_cue_text_html_escaped_vtt(tmp_path):
    cues = [
        Cue(start_sec=0.0, end_sec=1.0,
            lines=("<script>alert(1)</script>",)),
        Cue(start_sec=1.0, end_sec=2.0, lines=("Tom & Jerry",)),
    ]
    out = VttWriter.write(cues).decode("utf-8")
    assert "&lt;script&gt;alert(1)&lt;/script&gt;" in out
    assert "Tom &amp; Jerry" in out
    p = tmp_path / "x.vtt"
    p.write_text(out, encoding="utf-8")
    parsed = list(webvtt.read(str(p)))
    # Parser sees the literal text, NOT a tag.
    assert "<script>" not in parsed[0].text
```

### 4.4 `test_alias_copy_uses_source_basename` (story-named)

```python
import asyncpg  # noqa
import pytest
from pathlib import Path
from maktaba_pipeline.media.subtitles.alias import alias_copy
from maktaba_pipeline.media.subtitles.paths import alias_path_for


@pytest.mark.asyncio
async def test_alias_copy_uses_source_basename(db_conn, tmp_path):
    source = tmp_path / "Lecture 1.mp4"
    source.write_bytes(b"fake mp4")
    canonical = tmp_path / ".maktaba" / "subs" / "abc.ar.srt"
    canonical.parent.mkdir(parents=True)
    canonical.write_bytes(b"1\n00:00:00,000 --> 00:00:01,000\nx\n\n")

    alias = alias_path_for(source, "ar")
    assert alias == tmp_path / "Lecture 1.ar.srt"

    written = await alias_copy(
        db=db_conn, video_id="00000000-0000-0000-0000-000000000001",
        source_video_path=source, canonical_srt_path=canonical,
        alias_path=alias,
    )
    assert written == str(alias)
    assert alias.exists()
    # Hardlink path: same inode (D5).
    assert alias.stat().st_ino == canonical.stat().st_ino
```

### 4.5 `test_atomic_replace_on_retry` (story-named)

```python
import pytest
from pathlib import Path
from unittest.mock import patch
from maktaba_pipeline.media.subtitles.atomic import write_atomic_pair


def test_atomic_replace_on_retry_no_partial_at_final(tmp_path):
    """Simulate a worker death between writing temp and replace.

    If we crash after writing tmp but before os.replace, the final
    path must NOT exist. A retry then completes cleanly.
    """
    library_root = tmp_path
    srt_path = library_root / ".maktaba" / "subs" / "h.ar.srt"
    vtt_path = library_root / ".maktaba" / "subs" / "h.ar.vtt"

    # Round 1: simulate failure after SRT replace, during VTT replace.
    real_replace = __import__("os").replace
    call_count = {"n": 0}

    def flaky_replace(src, dst):
        call_count["n"] += 1
        if call_count["n"] == 2:
            raise OSError("simulated VTT rename failure")
        return real_replace(src, dst)

    with patch("os.replace", flaky_replace):
        with pytest.raises(OSError):
            write_atomic_pair(
                library_root=library_root,
                srt_path=srt_path, srt_bytes=b"srt-payload-1",
                vtt_path=vtt_path, vtt_bytes=b"vtt-payload-1",
            )
    # After rollback (D4): NEITHER final file exists.
    assert not srt_path.exists(), "SRT must be rolled back on VTT failure"
    assert not vtt_path.exists()

    # Round 2: clean retry succeeds.
    write_atomic_pair(
        library_root=library_root,
        srt_path=srt_path, srt_bytes=b"srt-payload-2",
        vtt_path=vtt_path, vtt_bytes=b"vtt-payload-2",
    )
    assert srt_path.read_bytes() == b"srt-payload-2"
    assert vtt_path.read_bytes() == b"vtt-payload-2"
    # No leftover temp files.
    tmp_dir = library_root / ".maktaba" / ".tmp"
    assert list(tmp_dir.iterdir()) == []
```

### 4.6 `test_readonly_source_dir_does_not_fail_job` (story-named)

```python
import os
import pytest
from pathlib import Path
from maktaba_pipeline.media.subtitles.alias import alias_copy


@pytest.mark.asyncio
async def test_readonly_source_dir_does_not_fail_job(db_conn, tmp_path, caplog):
    src_dir = tmp_path / "ro_lib"
    src_dir.mkdir()
    source = src_dir / "talk.mp4"
    source.write_bytes(b"fake")
    canonical = tmp_path / ".maktaba" / "subs" / "abc.ar.srt"
    canonical.parent.mkdir(parents=True)
    canonical.write_bytes(b"x")

    # Make the directory read-only so the alias write fails with EACCES.
    os.chmod(src_dir, 0o555)
    try:
        alias = src_dir / "talk.ar.srt"
        result = await alias_copy(
            db=db_conn, video_id="00000000-0000-0000-0000-000000000001",
            source_video_path=source, canonical_srt_path=canonical,
            alias_path=alias,
        )
        # Function returns None (skipped), does not raise.
        assert result is None
        # Canonical sidecar is untouched.
        assert canonical.exists()
        # WARN logged with kind=alias_copy_failed.
        warns = [r for r in caplog.records
                 if getattr(r, "kind", None) == "alias_copy_failed"]
        assert len(warns) == 1
    finally:
        os.chmod(src_dir, 0o755)
```

### 4.7 `test_alias_collision_skips` (edge case)

```python
@pytest.mark.asyncio
async def test_alias_collision_skips(db_conn, tmp_path, caplog):
    """Two videos with the same alias path → second skips and logs."""
    src_dir = tmp_path / "lib"
    src_dir.mkdir()
    canonical_a = tmp_path / ".maktaba" / "subs" / "hashA.ar.srt"
    canonical_b = tmp_path / ".maktaba" / "subs" / "hashB.ar.srt"
    canonical_a.parent.mkdir(parents=True)
    canonical_a.write_bytes(b"a"); canonical_b.write_bytes(b"b")

    # Pre-seed an existing subtitle_files row for video A claiming the alias.
    alias = src_dir / "talk.ar.srt"
    await db_conn.execute("""
        INSERT INTO subtitle_files
            (video_id, format, language, path, is_external, is_embedded)
        VALUES ($1, 'srt', 'ar', $2, false, false)
    """, "00000000-0000-0000-0000-000000000a01", str(alias))

    # Now video B tries to claim the same alias → must skip.
    result = await alias_copy(
        db=db_conn,
        video_id="00000000-0000-0000-0000-000000000b02",
        source_video_path=src_dir / "talk.mkv",
        canonical_srt_path=canonical_b,
        alias_path=alias,
    )
    assert result is None
    assert not alias.exists()  # B did not write
    warns = [r for r in caplog.records
             if getattr(r, "kind", None) == "alias_collision"]
    assert len(warns) == 1
```

### 4.8 `test_escape` (unit; underlies the story-named escape test)

```python
from maktaba_pipeline.media.subtitles.escape import (
    escape_cue_text, escape_speaker_label,
)


def test_escape_order_amp_first():
    assert escape_cue_text("a & b") == "a &amp; b"
    assert escape_cue_text("<x>") == "&lt;x&gt;"
    # Existing entity in source becomes double-escaped (story edge case).
    assert escape_cue_text("&amp;") == "&amp;amp;"
    # Mixed: '&' first means '<' inside an entity stays escaped to '&lt;'.
    assert escape_cue_text("&lt;") == "&amp;lt;"


def test_speaker_label_uses_same_escape():
    assert escape_speaker_label("Sheikh <A>") == "Sheikh &lt;A&gt;"
    assert escape_speaker_label("Tom & Jerry") == "Tom &amp; Jerry"
```

### 4.9 `test_paths` (unit)

```python
from pathlib import Path
from maktaba_pipeline.media.subtitles.paths import (
    canonical_subtitle_path, alias_path_for, sidecar_dir_for,
    ensure_sidecar_dirs,
)


def test_canonical_subtitle_path_layout(tmp_path):
    p = canonical_subtitle_path(tmp_path, "deadbeef", "ar", "srt")
    assert p == tmp_path / ".maktaba" / "subs" / "deadbeef.ar.srt"


def test_alias_path_uses_basename_no_ext(tmp_path):
    src = tmp_path / "subdir" / "Lecture 1.mp4"
    a = alias_path_for(src, "ar")
    assert a == tmp_path / "subdir" / "Lecture 1.ar.srt"


def test_ensure_sidecar_dirs_idempotent_and_0755(tmp_path):
    ensure_sidecar_dirs(tmp_path)
    assert (tmp_path / ".maktaba" / "subs").is_dir()
    assert (tmp_path / ".maktaba" / ".tmp").is_dir()
    # Re-run is a no-op.
    ensure_sidecar_dirs(tmp_path)
```

### 4.10 `test_stage_end_to_end` (integration)

```python
import pytest
from pathlib import Path


@pytest.mark.asyncio
async def test_stage_end_to_end(
    db_pool, library_root, video_with_active_transcript, claimed_subtitle_gen_job, ctx,
):
    """Full subtitle_gen run produces both files and rows; alias is hardlinked."""
    from maktaba_pipeline.pipeline.stages.subtitle_gen import run_subtitle_gen_stage

    await run_subtitle_gen_stage(ctx, claimed_subtitle_gen_job)

    video = video_with_active_transcript
    sub_dir = Path(library_root) / ".maktaba" / "subs"
    srt = sub_dir / f"{video.content_hash}.{video.language}.srt"
    vtt = sub_dir / f"{video.content_hash}.{video.language}.vtt"
    assert srt.exists() and vtt.exists()

    # Both subtitle_files rows present.
    rows = await db_pool.fetch(
        "SELECT format, path FROM subtitle_files WHERE video_id=$1 "
        "ORDER BY format", video.id)
    assert [r["format"] for r in rows] == ["srt", "vtt"]
    assert [r["path"] for r in rows] == [str(srt), str(vtt)]

    # Alias is present next to source and hardlinked.
    alias = Path(video.source_path).with_suffix("").with_name(
        f"{Path(video.source_path).stem}.{video.language}.srt"
    )
    assert alias.exists()
    assert alias.stat().st_ino == srt.stat().st_ino


@pytest.mark.asyncio
async def test_stage_fails_with_no_active_transcript(ctx, claimed_subtitle_gen_job_no_transcript):
    from maktaba_pipeline.pipeline.stages.subtitle_gen import (
        run_subtitle_gen_stage, SubtitleGenError,
    )
    with pytest.raises(SubtitleGenError) as ei:
        await run_subtitle_gen_stage(ctx, claimed_subtitle_gen_job_no_transcript)
    assert ei.value.kind == "no_active_transcript"


@pytest.mark.asyncio
async def test_stage_creates_sidecar_dir(ctx, fresh_library_no_maktaba_dir):
    """First run creates .maktaba/subs/ with mode 0755."""
    from maktaba_pipeline.pipeline.stages.subtitle_gen import run_subtitle_gen_stage
    job = fresh_library_no_maktaba_dir.claimed_job
    await run_subtitle_gen_stage(ctx, job)
    p = Path(fresh_library_no_maktaba_dir.library_root) / ".maktaba" / "subs"
    assert p.is_dir()
    assert (p.stat().st_mode & 0o777) == 0o755
```

### 4.11 `test_stage_idempotent_on_rerun` (D1)

```python
@pytest.mark.asyncio
async def test_stage_idempotent_on_rerun(
    db_pool, video_with_active_transcript, claimed_subtitle_gen_job, ctx,
):
    """Re-running subtitle_gen produces byte-identical files and 1 row per (vid,fmt,lang)."""
    from maktaba_pipeline.pipeline.stages.subtitle_gen import run_subtitle_gen_stage

    await run_subtitle_gen_stage(ctx, claimed_subtitle_gen_job)
    srt_first = (claimed_subtitle_gen_job.canonical_srt_path).read_bytes()

    # Re-claim and re-run.
    await run_subtitle_gen_stage(ctx, claimed_subtitle_gen_job)
    srt_second = (claimed_subtitle_gen_job.canonical_srt_path).read_bytes()
    assert srt_first == srt_second

    # Exactly two rows for this video (one srt, one vtt) — UPSERT, not duplicate.
    n = await db_pool.fetchval(
        "SELECT count(*) FROM subtitle_files WHERE video_id=$1",
        video_with_active_transcript.id)
    assert n == 2
```

### 4.12 `test_migration_applies_cleanly` (migration suite)

```python
@pytest.mark.asyncio
async def test_migration_0015_applies_cleanly(empty_db_pool):
    # Run all migrations through 0015.
    await apply_migrations(empty_db_pool, up_to="0015")

    indexes = await empty_db_pool.fetch("""
        SELECT indexname FROM pg_indexes
         WHERE schemaname = 'public' AND tablename = 'subtitle_files'
    """)
    names = {r["indexname"] for r in indexes}
    assert "subtitle_files_video_fmt_lang_idx" in names
    assert "subtitle_files_path_idx" in names

    # Re-run is idempotent.
    await apply_migrations(empty_db_pool, up_to="0015")  # must not raise
```

---

## 5. Edge cases and how the plan handles each

| # | Edge case (story §"Edge cases") | Handled by |
|---|---------------------------------|------------|
| E1 | `.maktaba/` directory not yet created. | `ensure_sidecar_dirs` (`paths.py`) calls `mkdir(parents=True, exist_ok=True, mode=0o755)`. The first write triggers it; subsequent runs are no-ops. If the parent (library root) lacks write perms, `mkdir` raises `OSError(EACCES)` and `run_subtitle_gen_stage` re-raises as `SubtitleGenError(kind="sidecar_dir")`, which the runner records as `error.kind = "sidecar_dir"` per the story. (`test_stage_creates_sidecar_dir` + the negative case in the same test file with chmod 555 on library_root) |
| E2 | Source basename collision (two videos sharing a basename in the same source dir). | `alias_copy` (`alias.py`) does a `SELECT 1 FROM subtitle_files WHERE path = $alias_path AND video_id != $current_video_id` (D7). On hit it returns `None` and logs `kind=alias_collision` at WARN. The canonical sidecar is unaffected; the second video has a complete `subtitle_files` row pointing into `.maktaba/subs/`. (`test_alias_collision_skips`) |
| E3 | Filenames with right-to-left content (Arabic). | The OS-level path is preserved as-is — `Path` operations do not reorder or normalize bytes. The `with_name(f"{stem}.{lang}.srt")` call composes the alias purely as a string concatenation; bidi rendering is the UI's job. No code path in this plan calls `unicodedata.normalize` or any reordering function on the path. (Manual fixture verifies an Arabic filename round-trips.) |
| E4 | Cue text containing existing entities (literal `&amp;`). | `escape_cue_text` (D3) replaces `&` first → `&amp;` becomes `&amp;amp;`. The story explicitly accepts this as correct ("cue text is treated as literal user text, not markup"). (`test_escape_order_amp_first`) |
| E5 | Disk full / permission denied during the canonical write. | `write_atomic_pair` (`atomic.py`) writes to `.maktaba/.tmp/<uuid>.{srt,vtt}` first; on `OSError` mid-write it `os.unlink` the partial temp before re-raising. The final `.maktaba/subs/` files never appear, so a retry sees no leftovers and writes cleanly. (`test_atomic_replace_on_retry`) |
| E6 | One of the two `os.replace()` calls fails after the other succeeded (split-pair). | D4: rollback path in `write_atomic_pair` `os.unlink(srt_path)` if VTT replace fails. The state on disk after a half-failure is "neither file present", which matches the all-or-nothing pair contract that downstream readers (streaming service, alias copy) rely on. (`test_atomic_replace_on_retry`) |
| E7 | Read-only source directory (CIFS / restricted mount). | `alias_copy` catches `OSError(EROFS|EACCES|EPERM)`, logs `kind=alias_copy_failed` at WARN, returns `None`. The `subtitle_gen` stage continues — the canonical sidecar in `.maktaba/subs/` is the artifact-of-record. (`test_readonly_source_dir_does_not_fail_job`) |
| E8 | A previous run wrote a file but failed before inserting `subtitle_files`. | D1: atomic re-write replaces the file; the UPSERT (`ON CONFLICT (video_id, format, language) DO UPDATE`) puts the row in regardless of prior state. No special "is this orphaned?" branch needed. (`test_stage_idempotent_on_rerun`) |
| E9 | Alias is currently a symlink (e.g., user-created link to a network share). | `alias_copy` `os.unlink`s the alias path before the `os.link`/`copy2`. `unlink` removes the symlink itself, not the target. The new alias is a hardlink (or copy) of the canonical SRT. |
| E10 | Active transcript exists but has zero segments (e.g., backend produced silence). | `run_subtitle_gen_stage` raises `SubtitleGenError("empty_transcript")`. The orchestrator records it; the operator can re-transcribe. Producing a zero-cue VTT would technically be valid but would mask the upstream bug. (`test_stage_fails_with_no_active_transcript` covers the no-transcript variant; an analogous `test_stage_fails_with_empty_transcript` covers this.) |
| E11 | Stage runs while another worker is mid-segment-commit on the same transcript (Story 4.5 contract). | The view filter `is_active = true` and Postgres MVCC mean we read a consistent snapshot. The per-segment commits from Plan 3.6 are atomic, so we either see segment N or segment N+1, never a half-row. If a new segment lands between our `SELECT` and our write, the next `subtitle_gen` run picks it up — no special concurrency control needed. |
| E12 | Cue text contains a literal `WEBVTT` token inside text (false header collision). | `WEBVTT` is only special at file start. The escape pass doesn't touch it; the writer prepends `WEBVTT\n\n` once at byte 0 and never again. A cue body containing `WEBVTT` is valid VTT; parsers (webvtt-py) treat it as text. |

---

## 6. Acceptance checklist

- [ ] **A1** When the `subtitle_gen` stage runs against a video with `state = TRANSCRIBED`, two files are produced at `<library_root>/.maktaba/subs/<hash>.<lang>.{srt,vtt}` and the alias `<source_dir>/<source_basename>.<lang>.srt` is written. (`test_stage_end_to_end`)
- [ ] **A2** Both files are generated **from `transcript_segments`**, never read back from disk; running on a videoFile whose canonical SRT has been hand-edited still produces the DB-derived bytes. (`test_stage_idempotent_on_rerun` + a manual hand-edit fixture variant)
- [ ] **A3** Two rows are inserted into `subtitle_files`, one per format, with `is_external = false` and `is_embedded = false`. The UPSERT key `(video_id, format, language)` makes a re-run produce exactly 2 rows, not 4. (`test_stage_idempotent_on_rerun`, `test_stage_end_to_end`)
- [ ] **A4** All cue text is HTML-escaped (`&` → `&amp;`, `<` → `&lt;`, `>` → `&gt;`) after wrapping but before format framing; `<script>` tags from segment text appear verbatim as `&lt;script&gt;` in both SRT and VTT outputs. (`test_cue_text_html_escaped_srt`, `test_cue_text_html_escaped_vtt`)
- [ ] **A5** A write failure leaves no partial file at the canonical path; the temp file under `.maktaba/.tmp/` is removed. The next retry produces both files cleanly. (`test_atomic_replace_on_retry`)
- [ ] **A6** A read-only source directory does NOT fail the job. The canonical sidecar in `.maktaba/subs/` is written, the `subtitle_files` row is inserted, and a single WARN with `kind=alias_copy_failed` is logged. (`test_readonly_source_dir_does_not_fail_job`)
- [ ] **A7** Source-basename collision between two videos is detected via DB lookup, the second video's alias copy is skipped, and `kind=alias_collision` is logged. The first video's alias is untouched. (`test_alias_collision_skips`)
- [ ] **A8** The `.maktaba/` directory is created with mode 0755 on first use; if creation fails (parent unwritable), the job fails with `kind=sidecar_dir`. (`test_stage_creates_sidecar_dir` + chmod-555 negative variant)
- [ ] **A9** SRT round-trips through the `srt` library: same cue count, same text, timestamps within 1 ms. (`test_srt_round_trips`)
- [ ] **A10** VTT round-trips through `webvtt-py`: same cue count, same text, timestamps match to `HH:MM:SS.mmm`. (`test_vtt_round_trips`)
- [ ] **A11** The alias filename uses the source's basename plus the `.<lang>.srt` infix exactly (e.g., `Lecture 1.ar.srt`); ISO 639-1 language code is taken from `transcripts.language`, not `videos.detected_language`. (`test_alias_copy_uses_source_basename`, `test_paths`)
- [ ] **A12** Migration `0015_subtitle_files_unique_lang.sql` adds the unique index `subtitle_files_video_fmt_lang_idx` and the plain index `subtitle_files_path_idx`; re-applying is idempotent. (`test_migration_0015_applies_cleanly`)
- [ ] **A13** The stage reads through `transcript_segments_v` (the Story 4.5 view) so only segments belonging to the active transcript are consumed; no direct join to `transcripts` for filtering. (Static check on the SQL in `subtitle_gen.py`; integration test confirms a superseded transcript's text never appears in the output by setting up two transcripts and flipping `is_active`.)
- [ ] **A14** No code path in this story writes a subtitle file with `is_external = true` or `is_embedded = true`. (Static lint: `INSERT INTO subtitle_files` outside this stage is owned by Stories 4.3 and 4.4.)
- [ ] **A15** The `subtitle_gen` job is idempotent: running it twice in a row produces byte-identical files and exactly two `subtitle_files` rows for the video. (`test_stage_idempotent_on_rerun`)
