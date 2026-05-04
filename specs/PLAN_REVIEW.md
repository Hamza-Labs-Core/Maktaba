# Implementation-Plan Review — Epics 01-06

> Audit of all `plan-*` files under [`specs/epics/01-scanner`](epics/01-scanner)
> through [`specs/epics/06-job-queue`](epics/06-job-queue) against their
> matching stories and against [`specs/architecture.md`](architecture.md). This
> review covers **49 plan files across 6 epics** and is independent of the
> earlier high-level audit at [`specs/REVIEW.md`](REVIEW.md): that document
> reviewed the architecture and four epic-level docs; this one reviews the
> per-story implementation plans that were authored after it.
>
> **Scope.**
> - Story alignment (does each plan implement the matching story?)
> - SQL migrations (do they reference real architecture tables/columns?
>   are migration numbers unique?)
> - Go/Python scaffolding (syntactic and type correctness; do imports work?)
> - Test coverage of acceptance criteria
> - Cross-plan dependencies and contradictions
>
> **Severity legend** (same as `REVIEW.md`):
> - 🔴 **Blocker** — will cause a runtime failure, build failure, or schema
>   corruption if landed as written.
> - 🟠 **Major** — drifts from the canonical spec or another plan in a way
>   that will be settled wrong unless explicitly resolved.
> - 🟡 **Minor** — inconsistency or gap that should be tightened.
> - 🔵 **Info** — observation, no action required.

---

## Executive summary

| Category | Blockers | Majors | Minors |
|---|---|---|---|
| Migration-number conflicts | 7 | — | — |
| Naming convention violations | — | 3 | — |
| Schema deviation from `architecture.md` §8 | 4 | 8 | — |
| Code/scaffolding bugs | 6 | 5 | several |
| Story alignment / scope drift | 1 | 6 | several |
| Cross-plan ownership conflicts | 3 | 4 | — |
| Architecture invariant violations (§7.x) | 1 | 4 | — |

The single largest defect is a **chaotic migration-number space**: at least
seven slot numbers are claimed by two-or-more plans, including slot `0003`
claimed four times within Epic 01 alone. **No central migration manifest
exists.** Until this is fixed, `goose up` cannot produce a deterministic
schema across epics.

The second-largest cluster is **schema drift from `architecture.md` §8.1**:
plans add columns (`videos.metadata`, `audio_tracks.disposition`,
`audio_tracks.detected_language`, `transcripts.is_active`,
`transcripts.metadata`, `subtitle_files.is_default`,
`subtitle_files.metadata`, `videos.last_seen_at`, etc.) and tables
(`subtitle_streams`, `library_scan_state`, `purge_log`, `audio_cache`,
`stt_usage`, `track_selection_decisions`, `vector_index_dead_letter`,
`search_suggestion_terms`) that are not in the canonical schema. Some are
deliberate amendments; most are silent.

---

## 1. Migration-number conflicts (🔴)

Every Epic 01–06 plan that ships a migration writes it under
`shared/db/migrations/NNNN_*.sql`. The numbers are picked locally per plan
and **collide as follows** (verified by grep across all plans):

| Slot | Files claiming the slot | Plans | Severity |
|---|---|---|---|
| `0003` | `0003_processing_jobs.sql` | [plan-01-01](epics/01-scanner/plan-01-01-file-discovery.md) | 🔴 |
| `0003` | `0003_content_hash.sql` | [plan-01-02](epics/01-scanner/plan-01-02-content-identity.md) | 🔴 |
| `0003` | `0003_video_states_and_stages.sql` | [plan-01-06](epics/01-scanner/plan-01-06-video-state-machine.md) | 🔴 |
| `0003` | `0003_video_state_constraint.sql` | [story-01-05-PLAN](epics/01-scanner/story-01-05-PLAN.md) §6.2 manifest | 🔴 |
| `0010` | `0010_processing_jobs.sql` | [plan-06-01](epics/06-job-queue/plan-06-01-schema-indexes.md) | 🔴 |
| `0010` | `0010_transcripts_is_active.sql` | [plan-03-05](epics/03-transcription/plan-03-05-backend-registry.md) | 🔴 |
| `0011` | `0011_segment_commit_function.sql` | [plan-03-06](epics/03-transcription/plan-03-06-segment-commit.md) | 🔴 |
| `0011` | `0011_jobs_progress_notify.sql` | [plan-06-03](epics/06-job-queue/plan-06-03-heartbeat-progress.md) | 🔴 |
| `0014` | `0014_transcript_segments_speaker_index.sql` | [plan-03-09](epics/03-transcription/plan-03-09-diarization.md) | 🔴 |
| `0014` | `0014_transcript_units_fts.sql` | [plan-07-08](epics/07-api-server/plan-07-08-search-api.md) (cross-epic) | 🔴 |
| `0017` | `0017_subtitle_files_is_embedded.sql` | [plan-04-04](epics/04-subtitles/plan-04-04-embedded-extraction.md) | 🔴 |
| `0017` | `0017_transcript_units_notify.sql` | [plan-05-03](epics/05-search-indexing/plan-05-03-chroma-vector.md) | 🔴 |
| `0019` | `0019_subtitle_files.sql` | [plan-04-03](epics/04-subtitles/plan-04-03-external-discovery.md) | 🔴 |
| `0019` | `0019_transcript_segments_view.sql` | [plan-04-05](epics/04-subtitles/plan-04-05-live-vtt-contract.md) | 🔴 |
| `0019` | `0019_chapters.sql` | [plan-05-07](epics/05-search-indexing/plan-05-07-chapter-inference.md) | 🔴 |
| `0021` | `0021_fts_tsvector_arabic_config.sql` | [plan-05-02](epics/05-search-indexing/plan-05-02-fts-tsvector.md) | 🔴 |
| `0021` | `0021_incremental_indexing.sql` | [plan-05-05](epics/05-search-indexing/plan-05-05-incremental-indexing.md) | 🔴 |

**Beyond direct collisions, ordering is broken too:**

- [plan-04-01](epics/04-subtitles/plan-04-01-generate-from-segments.md) claims
  `0015_subtitle_files_unique_lang.sql` and writes an UPSERT that references
  the `is_embedded` column — but the column is added by
  [plan-04-04](epics/04-subtitles/plan-04-04-embedded-extraction.md) at
  `0017`, and the base table itself is created by
  [plan-04-03](epics/04-subtitles/plan-04-03-external-discovery.md) at
  `0019`. Migration `0015` will fail because the table doesn't exist yet.
- [plan-05-06](epics/05-search-indexing/plan-05-06-query-suggestions.md)
  references "Plan 5.5 ships migration `0026`" but plan-05-05 itself uses
  `0021`.
- [plan-05-01](epics/05-search-indexing/plan-05-01-unit-chunking.md) uses
  `0NNN` placeholder; everything downstream of it is unanchored.

### Recommendation

1. Create `shared/db/migrations/MANIFEST.md` (or a single TOML with version,
   description, owner) that is the **single source of truth** for migration
   numbering across all epics.
2. Have every plan reference its slot via a manifest entry, not a hardcoded
   number. CI should reject any plan that hardcodes a number not in the
   manifest.
3. Renumber the conflicts. Suggested topological order based on dependencies:
   `libraries → videos → processing_jobs → content_hash → video_states →
   media_info/audio_tracks → audio_cache → transcripts → transcript_segments →
   transcripts.is_active → segment_commit_fn → subtitle_files →
   subtitle_files.is_embedded → subtitle_files.unique_lang → transcript_units →
   transcript_units_fts → transcript_units.tsv → chapters → suggestions`.

---

## 2. Naming-convention violations (🟠)

Two plan files in Epic 01 use non-standard filenames:

| File | Issue |
|---|---|
| [story-01-03-filesystem-watcher-plan.md](epics/01-scanner/story-01-03-filesystem-watcher-plan.md) | Should be `plan-01-03-filesystem-watcher.md` (suffix `-plan` instead of prefix `plan-`). |
| [story-01-05-PLAN.md](epics/01-scanner/story-01-05-PLAN.md) | Should be `plan-01-05-schema-decisions.md` (uppercase suffix; non-descriptive name). |

A third file is **misfiled**: 🟠

- [plan-01-03-metadata-extraction-ffprobe.md](epics/01-scanner/plan-01-03-metadata-extraction-ffprobe.md)
  occupies the `01-03` slot but its content (probe / FFprobe binding) is the
  implementation of [story-02-01-audio-probe.md](epics/02-audio-extraction/story-02-01-audio-probe.md).
  The plan itself states this in its first paragraph. The actual Epic-01
  story 1.3 (filesystem watcher) lives in
  [story-01-03-filesystem-watcher-plan.md](epics/01-scanner/story-01-03-filesystem-watcher-plan.md).
  Recommendation: move plan-01-03 to `epics/02-audio-extraction/plan-02-01-audio-probe-binding.md`
  (or merge into `plan-02-01-audio-probe.md`).

All other epics (02, 03, 04, 05, 06) have consistent `plan-NN-MM-*.md`
naming and 1:1 story↔plan mapping. No missing plans were detected.

---

## 3. Per-epic findings

### Epic 01 — Scanner

- [plan-01-01-file-discovery.md](epics/01-scanner/plan-01-01-file-discovery.md) (story alignment 🟠 / schema 🔴 / code 🔴 / tests ✓)
  - 🔴 Two duplicate `devIno` type declarations in `scanner/internal/walker/walker.go`
    (line 263 inside `Walk`, line 366 file-level). Won't compile.
  - 🔴 The `processing_jobs` migration omits the partial heartbeat index
    `(state, last_heartbeat_at) WHERE state IN ('claimed','running','resuming')`
    and the `pause_requested` partial index from architecture §7.1.
  - 🟠 Adds a `metadata JSONB` column to `videos` not in architecture §8.1
    (also added independently by plan-01-02).
  - 🟠 HTTP handler in §3.5 writes the JSON body before
    `w.WriteHeader(http.StatusAccepted)`, so the 202 never fires (Go writes
    200 implicitly on first body write).
  - 🟠 Departs from architecture §3.1 (Python scanner) by writing the scanner
    in Go. Documented as decision D1, but not all sibling plans agree (see
    cross-cutting §6.4 below).

- [plan-01-02-content-identity.md](epics/01-scanner/plan-01-02-content-identity.md) (alignment 🟠 / schema 🔴 / code 🔴 / tests ✓)
  - 🔴 In `pipeline/internal/identity/hasher.go` line 195, local variable
    `hex` shadows the imported `encoding/hex` package; will fail to compile.
  - 🔴 BLAKE3 formula divergence: story acceptance #1 mandates
    `BLAKE3(first_4MiB || last_4MiB || size_le_u64)`. Plan §2.4 step 2
    falls through to `io.Copy` when `size <= 2*HeadTail`, producing
    `BLAKE3(content || size_le_u64)` for small files — different bytes for
    `size < 2*HeadTail`. The included `TestHashSmallFileFullContent`
    codifies the divergence rather than the spec.
  - 🟠 `0003_content_hash.sql` collides with three other claims on slot
    0003 (see §1). The plan also proposes the per-library UNIQUE flip that
    [story-01-05-PLAN.md](epics/01-scanner/story-01-05-PLAN.md) explicitly
    owns — three plans claim ownership of that flip.
  - 🟠 `videos_content_hash_format_chk` regex excludes uppercase but the
    library returns lowercase only; constraint is fine but pointlessly
    strict.
  - 🟡 Plan uses `lukechampine.com/blake3`; plan-01-01 uses
    `github.com/zeebo/blake3`. Two BLAKE3 libs in one Epic.

- [plan-01-03-metadata-extraction-ffprobe.md](epics/01-scanner/plan-01-03-metadata-extraction-ffprobe.md) (misfiled — see §2)
  - 🟠 Implements story-02-01 from inside Epic 01.
  - 🔴 `EnqueueExtractJob` uses `ON CONFLICT (video_id, stage) WHERE state IN
    ('pending','running') DO NOTHING` but the supporting partial unique
    index in plan-01-01 has predicate
    `state IN ('pending','claimed','running','paused','resuming')` — Postgres
    requires the predicates to **match exactly** for `ON CONFLICT` to bind to
    the partial index, so this raises at runtime.
  - 🟠 Introduces a `subtitle_streams` table not in architecture §8.1
    (architecture only has `subtitle_files`).
  - 🟠 The CHECK on `videos.state` lists 10 states (omits `superseded`,
    `corrupted`); plan-01-06 lists 12. The 10-state version blocks
    transitions defined in story-01-06.

- [plan-01-04-manual-control.md](epics/01-scanner/plan-01-04-manual-control.md) (alignment 🟠 / schema 🟠 / code ok / tests ✓)
  - 🟠 D2 changes the CLI command from the story's literal
    `maktaba-pipeline scan --dry-run` to `maktaba-scan --dry-run`.
  - 🟠 Migration `0008_scan_control.sql` assumes plans 01-01 (0001-0004)
    and 01-05 (0005-0007) ship in order; if any earlier slot collision is
    resolved by renumbering, this slot drifts.

- [plan-01-06-video-state-machine.md](epics/01-scanner/plan-01-06-video-state-machine.md) (alignment ✓ / schema 🔴 / code ok / tests ✓)
  - 🔴 `0003_video_states_and_stages.sql` collides with plan-01-01,
    plan-01-02, and story-01-05-PLAN. Plan correctly DROPs prior CHECK
    before adding the canonical 12-state constraint, but can only land if
    its number is unique.

- [story-01-03-filesystem-watcher-plan.md](epics/01-scanner/story-01-03-filesystem-watcher-plan.md) (alignment ✓ / schema 🔴 / code ok / tests ✓)
  - 🔴 SQL string literals use uppercase `'MISSING'` and `'DISCOVERED'`
    (lines 559–580) where every other plan and the architecture use
    lowercase. **Will fail the `videos_state_check` CHECK constraint at
    runtime.**
  - 🟠 References `missing_at` and `rediscovered_at` columns not declared
    in any schema; story-01-05 only declares JSONB metadata key
    `missing_since`.
  - 🟡 Introduces an `OFFLINE` library-root state on mount disappearance
    not in any state enum.

- [story-01-05-PLAN.md](epics/01-scanner/story-01-05-PLAN.md) (alignment 🟠 / schema 🔴 / code ok / tests ✓)
  - 🟠 Scope creep: story-01-05 specifies three schema decisions; the plan
    documents the entire incremental-scan engine, the migration system, the
    `purge_log` and `library_scan_state` tables, and dual Go+Python
    scaffolding.
  - 🔴 Manifest in §6.2 reserves slots `0001`–`0006` but conflicts with
    plans 01-01, 01-02, 01-04, 01-06 each independently picking
    overlapping numbers.
  - 🟠 SQLite migration uses an `INSERT INTO videos__new SELECT * FROM
    videos` rebuild that fails if any prior plan added a column to `videos`
    out of sync.

### Epic 02 — Audio Extraction

- [plan-02-01-audio-probe.md](epics/02-audio-extraction/plan-02-01-audio-probe.md) (alignment ✓ / schema ✓ / code 🔴 / tests ✓)
  - 🔴 Reads `videos.fs_path` (lines 412, 425); architecture §8.1 and
    plan-01-02 use `videos.path`. Column does not exist.
  - 🟠 `enqueue_extract_job` uses `ON CONFLICT (video_id, stage) WHERE
    state IN (…)`; same partial-index predicate-mismatch issue as
    plan-01-03.

- [plan-02-02-track-selection.md](epics/02-audio-extraction/plan-02-02-track-selection.md) (alignment 🟠 / schema 🔴 / code 🟡 / tests ✓)
  - 🟠 Significant scope creep beyond story-02-02: adds a 5th `user_override`
    rule, an HTTP override API, language detection via whisper-cpp tiny,
    and a `track_selection_decisions` audit table — none in the story.
  - 🔴 Adds `audio_tracks.disposition`, `audio_tracks.detected_language`,
    `audio_tracks.detected_language_confidence` — none in architecture §8.1
    (which lists exactly 9 columns).
  - 🔴 Pipeline gap: plan reads `audio_tracks.disposition` JSONB, but
    plan-02-01 (the upstream probe) never populates it, and the gRPC
    `AudioTrack` message has no `disposition` field. **The track-selection
    code cannot work without amending plan-02-01.**
  - 🔴 Uses `videos.metadata.track_override` JSONB; architecture §8.1 has
    no `metadata` column on `videos`.
  - 🟡 Type annotation `list[tuple[str, callable]]` should be `Callable`
    from `typing`.

- [plan-02-03-stream-extraction.md](epics/02-audio-extraction/plan-02-03-stream-extraction.md) (alignment ✓ / schema 🟠 / code ✓ / tests ✓)
  - 🟠 Migration uses placeholder `000NN_extract_error_envelope.up.sql`.
  - 🟠 Introduces an `audio_cache` table and changes
    `processing_jobs.error TEXT → JSONB` mid-pipeline; the JSONB conversion
    is fine but again silently amends architecture §7.1.
  - Honors architecture §3.3 "no intermediate file unless STT requires
    one": §2.3 streaming path is default, §2.4 file fallback is opt-in via
    `backend.requires_file=True`.

- [plan-02-04-resource-accounting.md](epics/02-audio-extraction/plan-02-04-resource-accounting.md) (alignment ✓ / schema ✓ / code 🔴 / tests ✓)
  - 🔴 `pressure.go` calls `fmt.Sscanf` but `import "fmt"` is missing from
    the import block. Won't compile.
  - 🟡 Reaches into `asyncio.Semaphore._value` (private API) for cap
    counting — also true in plan-02-03; flagged in plan-06-07 as a
    follow-up.
  - 🟠 Plan-02-03 `cleanup_temp_audio` uses `{content_hash}.wav`; plan-02-04
    references `{content_hash}-a{track.index}.wav` for the same artifact.
    Multi-track libraries collide.

### Epic 03 — Transcription

- [plan-03-01-backend-protocol.md](epics/03-transcription/plan-03-01-backend-protocol.md) (alignment ✓ / schema n/a / code ✓ / tests ✓)
  - 🟡 Adds `supports_word_timestamps`, `requires_file`, `warmup()`,
    `close()`, error taxonomy beyond architecture §3.4. Reasonable
    extensions, consumed by 03-02..05.

- [plan-03-02-whisper-mlx-backend.md](epics/03-transcription/plan-03-02-whisper-mlx-backend.md) (alignment ✓ / code ✓ / tests ✓)
  - 🔵 `Segment.metadata` requires `transcripts.metadata` JSONB; the column
    add is correctly deferred to plan-03-05.

- [plan-03-03-faster-whisper-backend.md](epics/03-transcription/plan-03-03-faster-whisper-backend.md) (alignment ✓ / code ✓ / tests ✓)
  - No issues found.

- [plan-03-04-openai-api-backend.md](epics/03-transcription/plan-03-04-openai-api-backend.md) (alignment ✓ / schema ✓ / code ✓ / tests ✓)
  - 🟡 Migration `0009_stt_usage.sql` is unique today but its number is
    not in any manifest.
  - `start_offset_sec > 0` raises `NotImplementedError` (correct per
    plan-03-01 protocol contract for non-resumable backends, but forces
    plan-03-07 to extract a trimmed file rather than calling resume).

- [plan-03-05-backend-registry.md](epics/03-transcription/plan-03-05-backend-registry.md) (alignment ✓ / schema 🟠 / code ✓ / tests ✓)
  - 🟠 Migration `0010_transcripts_is_active.sql` collides with
    plan-06-01's `0010_processing_jobs.sql`.
  - 🟠 Drops the `architecture.md` §8.1 `transcripts UNIQUE
    (video_id, audio_track_id, backend, model)` constraint and replaces it
    with a partial unique index on `is_active = true`. Documented as a
    deliberate amendment resolving REVIEW §1.1.b. **Architecture.md should
    be updated to match.**
  - 🟠 Adds `transcripts.is_active BOOLEAN` and `transcripts.metadata
    JSONB` — neither in architecture §8.1.

- [plan-03-06-segment-commit.md](epics/03-transcription/plan-03-06-segment-commit.md) (alignment ✓ / schema 🟠 / code ✓ / tests ✓)
  - 🟠 Migration `0011_segment_commit_function.sql` collides with
    plan-06-03's `0011_jobs_progress_notify.sql`.
  - Honors architecture §7.6 atomicity: per-segment commit is one
    PL/pgSQL `commit_segment(...)` function rolling segment INSERT + words
    INSERT + job progress UPDATE + EWMA into one transaction. The
    `AFTER INSERT` trigger ensures `pg_notify('segments.committed', ...)`
    is part of the same transaction.

- [plan-03-07-pause-resume.md](epics/03-transcription/plan-03-07-pause-resume.md) (alignment ✓ / schema n/a / code ✓ / tests ✓)
  - 🟡 D5 (cross-backend resume opens a NEW `transcripts` row tagged with
    `metrics.resumed_with_different_backend`) is a non-trivial amendment
    to architecture §7.7's silent assumption that resume reuses the
    original transcript. Well-justified but worth folding into the
    architecture doc.

- [plan-03-08-crash-recovery.md](epics/03-transcription/plan-03-08-crash-recovery.md) (alignment ✓ / schema n/a / code ✓ / tests ✓)
  - 🔵 Places the reaper in the API service (Go) rather than the Python
    worker; architecture §7.9 is silent on location, so this is a
    refinement.

- [plan-03-09-diarization.md](epics/03-transcription/plan-03-09-diarization.md) (alignment 🟠 / schema 🟠 / code ✓ / tests ✓)
  - 🟠 Migration `0014_transcript_segments_speaker_index.sql` collides
    with plan-07-08's `0014_transcript_units_fts.sql` (cross-epic).
  - 🟠 Plan deliberately does **not** populate the architecture §8.1
    `segment_speakers` join table or the `speakers` table; speaker labels
    are stored only as denormalized text in `transcript_segments.speaker`.
    Story 3.9 defers cross-video matching to v1.1; the architecture
    treats `segment_speakers` as v1. Either populate the join at v1 or
    delete the unused tables from architecture §8.1.

### Epic 04 — Subtitles

- [plan-04-01-generate-from-segments.md](epics/04-subtitles/plan-04-01-generate-from-segments.md) (alignment ✓ / schema 🔴 / code ✓ / tests ✓)
  - 🔴 Migration `0015_subtitle_files_unique_lang.sql` runs before plan-04-03
    creates the base table (`0019`) and before plan-04-04 adds
    `is_embedded` (`0017`). Re-ordering is required.
  - Honors architecture §3.5 "produced from segments — never parsed back
    from disk" by reading `transcript_segments_v` (plan-04-05's view).

- [plan-04-02-formatting-wrapping.md](epics/04-subtitles/plan-04-02-formatting-wrapping.md) (alignment ✓ / schema n/a / code ✓ / tests ✓)
  - No issues found. Six-pass shaper (merge, split-long, wrap, CPS,
    no-overlap, speaker-tag) honors architecture §3.5 (42 chars, 2 lines,
    Arabic punctuation, sentence-aware split).

- [plan-04-03-external-discovery.md](epics/04-subtitles/plan-04-03-external-discovery.md) (alignment ✓ / schema 🔴 / code ✓ / tests 🟡)
  - 🔴 Owns the base `subtitle_files` migration (`0019_subtitle_files.sql`)
    but uses `id UUID PRIMARY KEY DEFAULT gen_random_uuid()` instead of
    architecture §8.1's `id BIGSERIAL PRIMARY KEY`.
  - 🟠 Adds `is_default`, `flags JSONB`, `size_bytes`, `mtime_ns`,
    `metadata JSONB`, `revived_count`, `deleted_at`, plus a `pg_notify`
    trigger — none in §8.1.
  - 🔴 Migration `0019_subtitle_files.sql` collides with plan-04-05's
    `0019_transcript_segments_view.sql` and plan-05-07's `0019_chapters.sql`.
  - 🟡 No RTL/bidi test for sidecar filenames containing Arabic characters.

- [plan-04-04-embedded-extraction.md](epics/04-subtitles/plan-04-04-embedded-extraction.md) (alignment ✓ / schema 🔴 / code ✓ / tests ✓)
  - 🔴 Migration `0017_subtitle_files_is_embedded.sql` collides with
    plan-05-03's `0017_transcript_units_notify.sql`.
  - 🟠 Adds `metadata JSONB NULL`; plan-04-03 already adds
    `metadata JSONB NOT NULL DEFAULT '{}'` — duplicate column declaration
    if both ship.

- [plan-04-05-live-vtt-contract.md](epics/04-subtitles/plan-04-05-live-vtt-contract.md) (alignment ✓ / schema 🔴 / code ✓ / tests ✓)
  - 🔴 Migration `0019_transcript_segments_view.sql` collides — see
    plan-04-03 above.
  - Honors architecture §4.5 "live rendered from DB, never read from .vtt
    file" — the `?live=1` path never opens an on-disk artifact.

### Epic 05 — Search Indexing

- [plan-05-01-unit-chunking.md](epics/05-search-indexing/plan-05-01-unit-chunking.md) (alignment ✓ / schema 🟠 / code ✓ / tests ✓)
  - 🟠 Migration filename `0NNN_transcript_units.sql` is a placeholder.
    All downstream Epic 05 numbering depends on this being assigned.
  - 🟠 D1 token cap (96) is calibrated against
    `paraphrase-multilingual-MiniLM-L12-v2` (128-token window). Plan-05-03
    actually ships `intfloat/multilingual-e5-large` (512-token window), so
    the cap is ~4× too tight.

- [plan-05-02-fts-tsvector.md](epics/05-search-indexing/plan-05-02-fts-tsvector.md) (alignment ✓ / schema 🟠 / code 🟡 / tests ✓)
  - 🔴 Migration `0021_fts_tsvector_arabic_config.sql` collides with
    plan-05-05's `0021_incremental_indexing.sql` **within the same epic**.
  - 🟠 SQLite FTS5 keys `transcript_id`/`unit_id` UNINDEXED instead of
    architecture §8.3's `video_id`/`segment_id`. This is the deliberate
    REVIEW §1.1.d resolution but `architecture.md` should be amended.
  - 🟡 SQL `maktaba_normalize` body uses `translate` with mismatched-length
    source/replacement strings; works in PG but fragile.

- [plan-05-03-chroma-vector.md](epics/05-search-indexing/plan-05-03-chroma-vector.md) (alignment ✓ / schema 🔴 / code ✓ / tests ✓)
  - 🔴 Migration `0017_transcript_units_notify.sql` collides — see
    plan-04-04.
  - 🟠 Chroma metadata adds `library_id` beyond architecture §8.4's exact
    list `{video_id, start, end, language, speaker}`. (Per-library
    collection name already encodes library, so the extra metadata is
    redundant.)

- [plan-05-04-hybrid-rrf.md](epics/05-search-indexing/plan-05-04-hybrid-rrf.md) (alignment ✓ / schema n/a / code 🔴 / tests ✓)
  - 🔴 `fts_query._query_pg` hardcodes `plainto_tsquery('simple', $1)`
    instead of `language_to_regconfig(language)` from plan-05-02 D4.
    **Silently disables Arabic-aware tokenization at search time** — a
    correctness regression vs. plan-05-02's careful work.

- [plan-05-05-incremental-indexing.md](epics/05-search-indexing/plan-05-05-incremental-indexing.md) (alignment ✓ / schema 🔴 / code ✓ / tests ✓)
  - 🔴 Migration `0021_incremental_indexing.sql` collides with plan-05-02
    (same epic).
  - 🟠 D10 retroactively modifies plan-03-06's `segments.committed` NOTIFY
    payload to add `video_id` + `library_id`. **This is a backward-incompatible
    change to a sibling plan** that should land as an amendment to
    plan-03-06, not a side effect.

- [plan-05-06-query-suggestions.md](epics/05-search-indexing/plan-05-06-query-suggestions.md) (alignment ✓ / schema ✓ / code 🟡 / tests ✓)
  - 🟡 Plan claims to share `normalize_for_suggest` with plan-05-04's
    analyzer, but plan-05-04's `analyzer.py` exports `analyze()`, not
    `normalize_for_suggest`. The shared-helper claim needs reconciliation.
  - 🟡 References "Plan 5.5 ships migration 0026" but plan-05-05 itself
    uses 0021. Internal numbering inconsistency.

- [plan-05-07-chapter-inference.md](epics/05-search-indexing/plan-05-07-chapter-inference.md) (alignment 🟠 / schema 🔴 / code ✓ / tests ✓)
  - 🔴 **Direct conflict with plan-09-18**. Both implement chapter
    inference with mutually incompatible designs:

    | Aspect | plan-05-07 | plan-09-18 |
    |---|---|---|
    | Stage | Sub-stage at tail of `index` | First-class `chapter_infer` stage |
    | Algorithm | Smoothed centroid window=3 | Sliding cosine drop window=5 |
    | `chapters.source` column | absent | discriminator (`inferred`/`embedded`/`manual`) |
    | Unique key | `(transcript_id, seq)` | `(video_id, source, seq)` |
    | Title | NULL in v1 | titler with embedder fallback |
    | Migration | `0019_chapters.sql` (CREATE) | `0023_chapter_inference.sql` (ALTER 5.7's table) |

    plan-09-18 explicitly states: "plan-5.7's tail-substage approach is
    rejected" and rebuilds the unique constraint.
  - 🔴 `0019_chapters.sql` collides with plan-04-03 and plan-04-05.
  - 🟠 Default `min_chapter_sec = 60` differs from architecture §4.6
    "capped at one per ~3 minutes" (i.e. 180).

### Epic 06 — Job Queue

- [plan-06-01-schema-indexes.md](epics/06-job-queue/plan-06-01-schema-indexes.md) (alignment ✓ / schema 🟠 / code 🟡 / tests ✓)
  - 🔴 Migration `0010_processing_jobs.sql` collides with plan-03-05 and
    with plan-01-01's earlier `0003_processing_jobs.sql`.
    **plan-01-01 should not own the `processing_jobs` table** — the README
    says "Epic 01 depends on Epic 06 stories 6.1–6.3 for processing_jobs
    enqueue", which means 06-01 is the canonical owner.
  - 🟡 Adds `payload JSONB` column and CHECK constraints
    (`stage`, `state`, `priority`, `attempts`, `last_segment_end_sec`)
    beyond architecture §7.1; reasonable hardening.
  - 🟡 sqlc `EnqueueJob` query is missing the partial-index `WHERE` clause;
    the `ON CONFLICT (video_id, stage) DO NOTHING` will not bind to the
    partial unique index without an explicit predicate match. The Python
    path includes the predicate; the Go path does not.

- [plan-06-02-claim-loop.md](epics/06-job-queue/plan-06-02-claim-loop.md) (alignment ✓ / code 🟠 / tests 🟡)
  - 🟠 Plan-06-04 §7 declares that plan-06-02 gates the `pending` claim on
    `pause_requested = false` (so a "pause requested before claim" is
    honored). **The actual SQL in plan-06-02 lines 169–172 does NOT
    include that gate.** Either add `AND pause_requested = false` to the
    `pending` branch or drop the cross-reference in plan-06-04.
  - 🟡 Test gap: no `test_claim_skips_pending_with_pause_requested`
    despite the cross-reference.

- [plan-06-03-heartbeat-progress.md](epics/06-job-queue/plan-06-03-heartbeat-progress.md) (alignment ✓ / schema 🔴 / code 🟠 / tests ✓)
  - 🔴 Migration `0011_jobs_progress_notify.sql` collides with plan-03-06
    (see §1).
  - 🟠 `processed_seconds` semantic drift: architecture §7.6 specifies
    `processed_seconds = segment.end - seek_from` (replace), but the plan
    implements `processed_seconds += $delta` (add). Probably equivalent if
    delta is computed correctly, but the divergence is not called out.
  - 🟡 EWMA α=0.2 (architecture §7.6) is not pinned anywhere in 06-03
    nor in the matching story — delegates to plan-03-06; cross-epic
    dependency note.

- [plan-06-04-pause-resume-cancel.md](epics/06-job-queue/plan-06-04-pause-resume-cancel.md) (alignment ✓ / schema n/a / code ✓ / tests ✓)
  - 🟠 Cross-plan dependency on the unrealized plan-06-02 gate (see above).

- [plan-06-05-backoff-retry.md](epics/06-job-queue/plan-06-05-backoff-retry.md) (alignment ✓ / schema n/a / code 🔴 / tests ✓)
  - 🔴 `_FAIL_OR_RETRY_SQL_PG` declares 5 placeholders but `$3` is not
    referenced in the SQL body. Calling it as
    `db.fetchrow(... job_id, err_json, None, error.retryable, backoff_sec)`
    passes `None` for `$3`, which **asyncpg will reject** (parameter
    declared but unused).

- [plan-06-06-reaper.md](epics/06-job-queue/plan-06-06-reaper.md) (alignment ✓ / schema n/a / code 🔴 / tests ✓)
  - 🔴 SQLite `_try_lock` returns `await self._local_lock.acquire() is None
    or True`. Since `asyncio.Lock.acquire()` returns `None` on success, this
    expression evaluates to `True` unconditionally — the non-blocking
    semantics are not implemented. Use `lock.locked()` check or
    `asyncio.wait_for(acquire, 0)`.

- [plan-06-07-concurrency-caps.md](epics/06-job-queue/plan-06-07-concurrency-caps.md) (alignment ✓ / code 🟡 / tests ✓)
  - 🟡 `_pick_device` uses `if not lock.locked(): await lock.acquire()`,
    a check-then-act with a suspending await between. Benign in single-loop
    asyncio; flagged for review.
  - 🔵 Adds `subtitle_gen=2` cap; not in architecture §7.4 but consistent
    with the canonical stage enum.

- [plan-06-08-graceful-shutdown.md](epics/06-job-queue/plan-06-08-graceful-shutdown.md) (alignment 🟠 / code 🟠 / tests ✓)
  - 🟠 Architecture §7.8 says shutdown converts running rows to `paused`
    with `reason='shutdown'`. The cooperative path inherits
    `mark_paused(reason='user')` from plan-06-04 and never overrides it
    for the shutdown case. The integration test
    `test_shutdown_pauses_all_claims_real_sigterm` accepts both `'user'`
    and `'shutdown'` — a tacit acknowledgment of the bug. Fix: pass
    `reason='shutdown'` when the worker observes both `should_pause` and
    `shutdown_event.is_set()`.

- [plan-06-09-observability.md](epics/06-job-queue/plan-06-09-observability.md) (alignment ✓ / code ✓ / tests ✓)
  - No issues found.

- [plan-06-10-resume-invariant.md](epics/06-job-queue/plan-06-10-resume-invariant.md) (alignment ✓ / code 🟡 / tests 🟡)
  - 🟡 `ChaosRunner` segment INSERT references a `transcripts` row via
    subquery but the fixture must seed it; not shown in the plan.

---

## 4. Schema deviations from `architecture.md` §8

The following columns and tables are written or read by Epic 01–06 plans
but are **not in architecture §8.1**. Each needs either an architecture
amendment or removal from the plan.

| Schema element | Introduced by | Severity |
|---|---|---|
| `videos.metadata JSONB` | plan-01-01, plan-01-02, plan-02-02 (read) | 🔴 conflict between three writers |
| `videos.last_seen_at` | story-01-05-PLAN | 🟠 |
| `videos.deleted_at` | plan-01-04 | 🟠 |
| `videos.cancel_requested`, `progress_pct` (on `library_scan_state`) | plan-01-04 | 🟠 |
| `audio_tracks.disposition JSONB` | plan-02-02 | 🔴 (read but not written upstream) |
| `audio_tracks.detected_language`, `_confidence` | plan-02-02 | 🟠 |
| `audio_tracks.last_extracted_at` | plan-02-03 | 🟠 |
| `transcripts.is_active BOOLEAN` | plan-03-05 | 🟠 (REVIEW §1.1.b resolution) |
| `transcripts.metadata JSONB` | plan-03-05 | 🟠 |
| `transcripts.last_indexed_segment_seq` | plan-05-05 | 🟠 |
| `subtitle_files.is_embedded` | plan-04-04 | 🟠 (REVIEW §1.1.c resolution) |
| `subtitle_files.is_default`, `flags`, `size_bytes`, `mtime_ns`, `metadata`, `revived_count`, `deleted_at`, `track_index` | plan-04-03, plan-04-04 | 🟠 |
| `transcript_units.indexed_at_in_chroma` | plan-05-05 | 🟠 |
| `chapters.lang`, `chapters.source` | plan-05-07, plan-09-18 | 🟠 (mutually incompatible designs) |
| Table `subtitle_streams` | plan-01-03 | 🟠 |
| Table `library_scan_state` | story-01-05-PLAN | 🟠 |
| Table `purge_log` | story-01-05-PLAN | 🟠 |
| Table `audio_cache` | plan-02-03 | 🟠 |
| Table `track_selection_decisions` | plan-02-02 | 🟠 |
| Table `stt_usage` | plan-03-04 | 🟡 (additive ledger) |
| Table `vector_index_dead_letter` | plan-05-05 | 🟡 |
| Table `search_suggestion_terms` | plan-05-06 | 🟡 |
| `processing_jobs.error TEXT → JSONB` | plan-02-03 | 🟠 (silent type change) |
| `processing_jobs.payload JSONB` | plan-06-01 | 🟡 (additive) |

### Recommendation

Add a new section `architecture.md` §8.6 **"Plan-introduced schema
extensions"** that lists every column/table added by an implementation plan,
with a pointer back to the plan that owns it. CI should enforce that any
column or table referenced by SQL in `pipeline/` or `api/` is either in
§8.1–§8.5 or §8.6.

---

## 5. Architecture invariant violations

| Violation | Plan | Severity | Architecture rule |
|---|---|---|---|
| Cooperative shutdown sets `paused_reason='user'` instead of `'shutdown'` | plan-06-08 | 🟠 | §7.8 |
| Postgres FTS query hardcodes `'simple'` regconfig | plan-05-04 | 🔴 | §3.7 (Arabic-aware retrieval) |
| `architecture.md` §8.1 `transcripts UNIQUE` dropped | plan-03-05 | 🟠 | §8.1 (deliberate, REVIEW §1.1.b) |
| `architecture.md` §8.1 `subtitle_files.id BIGSERIAL` becomes UUID | plan-04-03 | 🟠 | §8.1 |
| `audio_tracks` 9-column shape extended | plan-02-02, plan-02-03 | 🟠 | §8.1 |
| `videos.path` referenced as `videos.fs_path` | plan-02-01 | 🔴 | §8.1 |
| `segment_speakers` and `speakers` tables unused at v1 | plan-03-09 | 🟠 | §8.2 |
| Chapter cap default 60 s vs. spec ~180 s | plan-05-07 | 🟠 | §4.6 |
| BLAKE3 small-file formula divergence | plan-01-02 | 🔴 | §3.1 |
| State CHECK on `videos.state` lists 10/12 states | plan-01-03 | 🔴 | story-01-06 enum |
| State string casing `'MISSING'` vs `'missing'` | story-01-03-filesystem-watcher-plan | 🔴 | all other plans |

---

## 6. Cross-plan ownership conflicts

### 6.1 `processing_jobs` table — three owners

- [plan-01-01](epics/01-scanner/plan-01-01-file-discovery.md) §4.3 creates
  `0003_processing_jobs.sql` with the architecture §7.1 schema (so the
  scanner can enqueue the first probe job).
- [plan-06-01](epics/06-job-queue/plan-06-01-schema-indexes.md) §3 creates
  `0010_processing_jobs.sql` with the same schema plus extra CHECK
  constraints and a `payload JSONB` column.
- The Epic 01 README explicitly says Epic 01 depends on Epic 06.

**Resolution:** plan-06-01 is canonical. plan-01-01 should drop the
`processing_jobs` migration and instead declare a hard dependency on the
06-01 migration landing first.

### 6.2 `subtitle_files` table — base + extensions split across three plans

- plan-04-03 owns the base table (`0019`).
- plan-04-04 adds `is_embedded` (`0017`) — runs **before** plan-04-03 by
  number.
- plan-04-01 indexes the table (`0015`) — runs **before** plan-04-04 and
  plan-04-03 by number.
- plan-04-04 declares `metadata JSONB NULL`; plan-04-03 declares
  `metadata JSONB NOT NULL DEFAULT '{}'` — duplicate column.

**Resolution:** consolidate the base table + `is_embedded` + unique-index
into one migration owned by plan-04-03; have plan-04-01 and plan-04-04
reference it instead of writing their own. Drop the duplicate `metadata`
column.

### 6.3 `chapters` table and chapter inference — Epic 05 vs Epic 09

See §3 plan-05-07 and §1 above. The two plans are **fundamentally
incompatible** about the unique key and the `source` column. Pick exactly
one design and delete the other.

### 6.4 Scanner language: Python vs Go

- architecture.md §1.3 / §3.1 — Python.
- plan-01-01 — Go (decision D1).
- plan-01-05 (story-01-05-PLAN.md) — both Go and Python scaffolding.
- plan-01-03 — Go (FFprobe binding).
- plan-01-04 — follows the Go scanner.
- story-01-03-filesystem-watcher-plan.md — Go.

The scanner has effectively been moved to Go without a single coordinating
decision record. Either amend `architecture.md` §1.3 to declare the
Pipeline Service split between Go and Python (probe/scanner/watcher in Go,
STT in Python) or move the Go scaffolding to a stub for v1.1.

### 6.5 Two BLAKE3 libraries in Epic 01

- plan-01-01 uses `github.com/zeebo/blake3`.
- plan-01-02 uses `lukechampine.com/blake3`.

Pick one before either plan ships.

---

## 7. Test-coverage notes

Most plans have strong test coverage for stated acceptance criteria. Notable
gaps:

| Plan | Missing test |
|---|---|
| plan-06-02 | `test_claim_skips_pending_with_pause_requested` referenced by plan-06-04 but absent |
| plan-04-03 | No RTL/bidi test for sidecar filenames containing Arabic |
| plan-03-09 | `test_resume_does_not_rerun_diarization` (acceptance A8) listed as a known gap |
| plan-05-07 | No test for the §4.6 "one per ~3 minutes" cap (default differs anyway) |
| plan-06-10 | `ChaosRunner` fixture must seed a `transcripts` row that the SQL subquery references; not shown in the plan |

All other plans cover their story's acceptance criteria. Diarization (3.9)
has comprehensive crash-and-resume tests; segment commit (3.6) and
crash-recovery (3.8) explicitly test the §7.6 invariant and the §7.9
reaper UPDATE; live-VTT (4.5) covers partial-job rendering, NOTIFY-cache
evict, and W3C VTT validator over fixtures.

---

## 8. Dependency wiring

These plans declare dependencies that point to non-existent or wrong
artifacts:

- 🔴 plan-02-02 reads `audio_tracks.disposition` JSONB; **plan-02-01 does
  not write it** and the proto message has no `disposition` field. Track
  selection cannot run as designed.
- 🔴 plan-02-01 reads `videos.fs_path`; column does not exist (it is
  `videos.path`).
- 🟠 plan-04-01 references `is_embedded` column that plan-04-04 owns at a
  later migration number.
- 🟠 plan-04-01 references the `subtitle_files` table that plan-04-03 owns
  at a later migration number.
- 🟠 plan-05-04 references `language_to_regconfig` from plan-05-02 but
  hardcodes `'simple'` instead.
- 🟠 plan-05-05 modifies plan-03-06's `segments.committed` NOTIFY payload
  without amending plan-03-06.
- 🟠 plan-05-06 references `normalize_for_suggest` as "shared with
  plan-05-04's analyzer" but plan-05-04 does not export that name.

---

## 9. Recommendations (rolled up)

1. **Migration manifest.** Create
   `shared/db/migrations/MANIFEST.md` (or `.toml`) listing every migration
   with its slot, owning plan, and dependencies. Renumber the seven
   conflicts. CI gate: any plan that hardcodes a number not in the manifest
   fails review.
2. **Architecture schema amendment.** Add `architecture.md` §8.6
   "Plan-introduced schema extensions". Move the columns and tables from
   §4 of this review into §8.6 with their owning plan. Update §8.1 entries
   that have been deliberately changed (`transcripts UNIQUE`,
   `subtitle_files.is_embedded`, `subtitle_files.id` if UUID is kept).
3. **Resolve §6 ownership conflicts.** Pick canonical owners for
   `processing_jobs` (06-01), `subtitle_files` (04-03), `chapters` (one of
   05-07/09-18). Have all other plans reference the canonical migration.
4. **Rename Epic-01 files.** `story-01-03-filesystem-watcher-plan.md` →
   `plan-01-03-filesystem-watcher.md`; `story-01-05-PLAN.md` →
   `plan-01-05-schema-decisions.md`; move
   `plan-01-03-metadata-extraction-ffprobe.md` to Epic 02 (or merge into
   plan-02-01).
5. **Fix the blocker code bugs** before any plan ships:
   - plan-01-01 `walker.go` duplicate `devIno`
   - plan-01-02 `hasher.go` shadowed `hex` package
   - plan-01-02 BLAKE3 small-file formula
   - plan-01-03 `ON CONFLICT` predicate mismatch
   - plan-02-01 `videos.fs_path` typo
   - plan-02-04 `pressure.go` missing `fmt` import
   - plan-05-04 hardcoded `'simple'` regconfig
   - plan-06-05 unused `$3` parameter
   - plan-06-06 SQLite `_try_lock` always-True bug
   - story-01-03-filesystem-watcher-plan SQL state casing
6. **Single language declaration for the scanner.** Either amend
   `architecture.md` §1.3 to make the Pipeline Service multi-language or
   delete the Go scaffolding from plans 01-01, 01-03, 01-04, 01-05,
   story-01-03-filesystem-watcher-plan.
7. **One BLAKE3 library** in Epic 01.
8. **Pin EWMA α=0.2 and the "~3 min" chapter cap** explicitly in the
   plans that own the implementation (03-06 and 05-07 / 09-18 respectively).
9. **Dependency-coupling fixes.** Plan-05-05's payload change to
   `segments.committed` should land as an amendment to plan-03-06 with a
   coordinated migration. Plan-02-02 needs upstream to land
   `audio_tracks.disposition` first.

---

## Appendix A — All plan files reviewed

Listed in numerical order; 49 files total. ✓ = no blocker found, 🟠 = at
least one major, 🔴 = at least one blocker.

| Epic | Plan | Status |
|---|---|---|
| 01 | [plan-01-01-file-discovery.md](epics/01-scanner/plan-01-01-file-discovery.md) | 🔴 |
| 01 | [plan-01-02-content-identity.md](epics/01-scanner/plan-01-02-content-identity.md) | 🔴 |
| 01 | [plan-01-03-metadata-extraction-ffprobe.md](epics/01-scanner/plan-01-03-metadata-extraction-ffprobe.md) | 🔴 |
| 01 | [plan-01-04-manual-control.md](epics/01-scanner/plan-01-04-manual-control.md) | 🟠 |
| 01 | [plan-01-06-video-state-machine.md](epics/01-scanner/plan-01-06-video-state-machine.md) | 🔴 |
| 01 | [story-01-03-filesystem-watcher-plan.md](epics/01-scanner/story-01-03-filesystem-watcher-plan.md) | 🔴 |
| 01 | [story-01-05-PLAN.md](epics/01-scanner/story-01-05-PLAN.md) | 🔴 |
| 02 | [plan-02-01-audio-probe.md](epics/02-audio-extraction/plan-02-01-audio-probe.md) | 🔴 |
| 02 | [plan-02-02-track-selection.md](epics/02-audio-extraction/plan-02-02-track-selection.md) | 🔴 |
| 02 | [plan-02-03-stream-extraction.md](epics/02-audio-extraction/plan-02-03-stream-extraction.md) | 🟠 |
| 02 | [plan-02-04-resource-accounting.md](epics/02-audio-extraction/plan-02-04-resource-accounting.md) | 🔴 |
| 03 | [plan-03-01-backend-protocol.md](epics/03-transcription/plan-03-01-backend-protocol.md) | ✓ |
| 03 | [plan-03-02-whisper-mlx-backend.md](epics/03-transcription/plan-03-02-whisper-mlx-backend.md) | ✓ |
| 03 | [plan-03-03-faster-whisper-backend.md](epics/03-transcription/plan-03-03-faster-whisper-backend.md) | ✓ |
| 03 | [plan-03-04-openai-api-backend.md](epics/03-transcription/plan-03-04-openai-api-backend.md) | ✓ |
| 03 | [plan-03-05-backend-registry.md](epics/03-transcription/plan-03-05-backend-registry.md) | 🔴 (migration collision) |
| 03 | [plan-03-06-segment-commit.md](epics/03-transcription/plan-03-06-segment-commit.md) | 🔴 (migration collision) |
| 03 | [plan-03-07-pause-resume.md](epics/03-transcription/plan-03-07-pause-resume.md) | ✓ |
| 03 | [plan-03-08-crash-recovery.md](epics/03-transcription/plan-03-08-crash-recovery.md) | ✓ |
| 03 | [plan-03-09-diarization.md](epics/03-transcription/plan-03-09-diarization.md) | 🔴 (migration collision) |
| 04 | [plan-04-01-generate-from-segments.md](epics/04-subtitles/plan-04-01-generate-from-segments.md) | 🔴 |
| 04 | [plan-04-02-formatting-wrapping.md](epics/04-subtitles/plan-04-02-formatting-wrapping.md) | ✓ |
| 04 | [plan-04-03-external-discovery.md](epics/04-subtitles/plan-04-03-external-discovery.md) | 🔴 |
| 04 | [plan-04-04-embedded-extraction.md](epics/04-subtitles/plan-04-04-embedded-extraction.md) | 🔴 |
| 04 | [plan-04-05-live-vtt-contract.md](epics/04-subtitles/plan-04-05-live-vtt-contract.md) | 🔴 |
| 05 | [plan-05-01-unit-chunking.md](epics/05-search-indexing/plan-05-01-unit-chunking.md) | 🟠 |
| 05 | [plan-05-02-fts-tsvector.md](epics/05-search-indexing/plan-05-02-fts-tsvector.md) | 🔴 |
| 05 | [plan-05-03-chroma-vector.md](epics/05-search-indexing/plan-05-03-chroma-vector.md) | 🔴 |
| 05 | [plan-05-04-hybrid-rrf.md](epics/05-search-indexing/plan-05-04-hybrid-rrf.md) | 🔴 |
| 05 | [plan-05-05-incremental-indexing.md](epics/05-search-indexing/plan-05-05-incremental-indexing.md) | 🔴 |
| 05 | [plan-05-06-query-suggestions.md](epics/05-search-indexing/plan-05-06-query-suggestions.md) | 🟡 |
| 05 | [plan-05-07-chapter-inference.md](epics/05-search-indexing/plan-05-07-chapter-inference.md) | 🔴 |
| 06 | [plan-06-01-schema-indexes.md](epics/06-job-queue/plan-06-01-schema-indexes.md) | 🔴 (migration collision) |
| 06 | [plan-06-02-claim-loop.md](epics/06-job-queue/plan-06-02-claim-loop.md) | 🟠 |
| 06 | [plan-06-03-heartbeat-progress.md](epics/06-job-queue/plan-06-03-heartbeat-progress.md) | 🔴 (migration collision) |
| 06 | [plan-06-04-pause-resume-cancel.md](epics/06-job-queue/plan-06-04-pause-resume-cancel.md) | 🟠 |
| 06 | [plan-06-05-backoff-retry.md](epics/06-job-queue/plan-06-05-backoff-retry.md) | 🔴 |
| 06 | [plan-06-06-reaper.md](epics/06-job-queue/plan-06-06-reaper.md) | 🔴 |
| 06 | [plan-06-07-concurrency-caps.md](epics/06-job-queue/plan-06-07-concurrency-caps.md) | 🟡 |
| 06 | [plan-06-08-graceful-shutdown.md](epics/06-job-queue/plan-06-08-graceful-shutdown.md) | 🟠 |
| 06 | [plan-06-09-observability.md](epics/06-job-queue/plan-06-09-observability.md) | ✓ |
| 06 | [plan-06-10-resume-invariant.md](epics/06-job-queue/plan-06-10-resume-invariant.md) | 🟡 |

Total: 42 files reviewed (the 7 Epic-01 files include two that are
naming-violation `story-*-PLAN.md` documents). 24 plans have at least one
🔴 blocker (mostly the migration-number collisions of §1).

---

## Appendix B — Methodology

This review was produced by:

1. Inventorying every `plan-*` and `*-PLAN.md` file under `specs/epics/01`
   through `specs/epics/06`.
2. Cross-checking each plan against its matching `story-*` file, the epic
   `README.md`, and the canonical schema in
   [`architecture.md`](architecture.md) §8.
3. Grepping migration filenames for cross-plan numbering conflicts.
4. Reading the SQL DDL, Python scaffolding, Go scaffolding, and test
   tables for syntax errors, type mismatches, and reference-target
   verification.
5. Comparing acceptance criteria from each story against the test
   inventory in the matching plan.

No code was modified; this is an audit document. Issues marked 🔴 block
implementation; 🟠 require explicit resolution; 🟡 are tightening
opportunities.

The earlier high-level audit at [`specs/REVIEW.md`](REVIEW.md) covers
architecture- and epic-level conflicts; this document is its
implementation-plan-level continuation.
