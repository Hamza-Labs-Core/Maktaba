# Implementation Plan — Story 9.8 Auto-categorization: Language Tag

> Companion to [story-09-08-language-tag.md](story-09-08-language-tag.md).
> The story states *what* and *why*; this plan states *how*.
> Builds on the transcribe stage (Epic 3) and Story 9.1's
> per-library `language` setting.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Trigger point | Inside the transcribe stage's commit transaction (Epic 3 Story 3.6). The `transcribe_worker` already writes `transcripts` and updates `videos.state='transcribed'`; this story adds the `videos.detected_language` and `videos.language_source` writes in the same tx. |
| Confidence column | `transcripts.detected_language` and `transcripts.language_confidence` are canonical now (architecture §8.1) — written by Pipeline Story 3.7. This story consumes them, applies the confidence threshold, and writes the per-video result. No defensive ALTER TABLE needed. |
| Per-track override (multi_audio) | When `library.multi_audio = True`, the *primary* track's language goes on `videos.detected_language`. "Primary" is the first track ordered by `(is_default DESC, track_index ASC)` — already encoded in `audio_tracks` per Epic 2 Story 2.3. |
| User override | A new `videos.language_source` enum: `'stt'`, `'user'`, `'forced'`. PATCH from Epic 7 Story 7.4 sets it to `'user'`; once `user`, the auto-tagger never overwrites. |
| Re-tag trigger | Out of scope here — Epic 7 Story 7.5's reprocess endpoint owns it. This story only ensures the auto-tagger respects existing user/forced values. |
| Out of scope | The actual STT detection (Epic 3); the PATCH endpoint (Epic 7); the cross-language Chroma search behaviour. |

## 1. Architecture diagram

```
   transcribe_worker.commit_segment_set(transcript)
        │
        ├─→ INSERT INTO transcripts (..., detected_language, language_confidence)
        │
        └─→ language_tagger.tag(video_id, transcript, library)
                │
                ├─ if library.language ∈ {None, "auto"}:
                │     candidate = transcript.detected_language
                │     conf      = transcript.language_confidence
                │     if conf < 0.6: candidate = "und"
                │     source    = "stt"
                │
                ├─ else:           # forced via library config
                │     candidate = library.language
                │     source    = "forced"
                │
                ├─ if multi_audio:
                │     candidate = primary_track.detected_language
                │
                └─ UPDATE videos
                      SET detected_language = $candidate,
                          language_source   = COALESCE(  -- user wins
                                              language_source = 'user' ?
                                                  language_source :
                                                  $source),
                          updated_at = now()
                    WHERE id = $video_id
                      AND (language_source <> 'user' OR language_source IS NULL)
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `pipeline/src/maktaba_pipeline/language/tagger.py` | The tagging function and the threshold constant. |
| `pipeline/tests/language/test_tagger.py` | Unit tests per §6.1. |
| `shared/db/migrations/0036_videos_language_source.sql` | Adds `language_source` enum column. |
| `shared/db/migrations/0036_videos_language_source.sqlite.sql` | SQLite variant. |
| `shared/db/queries/language.sql` | sqlc input — `UpdateVideoLanguageIfNotUserSet`. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/transcribe/commit.py` | After segment commit, call `tag_language(...)`. Same tx. |
| `api/internal/handlers/videos/patch.go` | When the user PATCHes `detected_language`, also set `language_source = 'user'`. |
| `specs/epics/09-library-management/README.md` | Tick story 9.8. |

### 2.3 Type definitions

```python
# pipeline/src/maktaba_pipeline/language/tagger.py
from __future__ import annotations
from dataclasses import dataclass
from enum import StrEnum

LANGUAGE_CONFIDENCE_THRESHOLD = 0.6
UNDETERMINED = "und"


class LanguageSource(StrEnum):
    STT    = "stt"
    USER   = "user"
    FORCED = "forced"


@dataclass(slots=True, frozen=True)
class TaggedLanguage:
    code: str                # 'ar' | 'en' | ... | 'und'
    source: LanguageSource
```

### 2.4 Function signature

```python
async def tag_language(
    db,
    *,
    video_id: UUID,
    library_settings: EffectiveLibrarySettings,
    primary_transcript: Transcript,           # the primary-track transcript row
) -> TaggedLanguage:
    """Decide and persist videos.detected_language. Honors language_source='user'
    by NOT overwriting. Returns the (decided, source) pair (for the caller's
    log/metric)."""
```

## 3. Database migration

### 3.1 Postgres — `0036_videos_language_source.sql`

```sql
-- +goose Up
-- +goose StatementBegin

-- Existing column from architecture: videos.detected_language TEXT.
-- New: a source-of-truth enum so PATCH from the user pins behavior.
ALTER TABLE videos
    ADD COLUMN IF NOT EXISTS language_source TEXT
        CHECK (language_source IS NULL OR language_source IN
               ('stt', 'user', 'forced'));

-- Backfill: existing rows with detected_language assumed STT-set.
UPDATE videos
   SET language_source = 'stt'
 WHERE detected_language IS NOT NULL
   AND language_source IS NULL;

-- Useful for the stats `by_language` accumulator and the manual filter.
CREATE INDEX IF NOT EXISTS videos_detected_language_lookup
    ON videos (library_id, detected_language);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS videos_detected_language_lookup;
ALTER TABLE videos DROP COLUMN IF EXISTS language_source;
-- +goose StatementEnd
```

### 3.2 SQLite variant

Same `ADD COLUMN` and `CREATE INDEX`; CHECK is identical (SQLite
supports column CHECKs).

### 3.3 `shared/db/queries/language.sql`

```sql
-- name: UpdateVideoLanguageIfNotUserSet :execrows
UPDATE videos
   SET detected_language = $2,
       language_source   = $3,
       updated_at        = now()
 WHERE id = $1
   AND (language_source IS DISTINCT FROM 'user');
```

`$2 = candidate` and `$3 = source` (`'stt'` or `'forced'`). The
`IS DISTINCT FROM` (or `(language_source != 'user' OR language_source IS NULL)`
on SQLite) is the gate that protects user overrides.

## 4. Code scaffolding

### 4.1 The tagger

```python
# pipeline/src/maktaba_pipeline/language/tagger.py
from __future__ import annotations
import logging
from uuid import UUID

log = logging.getLogger(__name__)


async def tag_language(
    db, *,
    video_id: UUID,
    library_settings,
    primary_transcript,
) -> TaggedLanguage:
    if library_settings.language and library_settings.language != "auto":
        candidate = library_settings.language
        source = LanguageSource.FORCED
    else:
        candidate = primary_transcript.detected_language or UNDETERMINED
        conf = primary_transcript.language_confidence or 0.0
        if conf < LANGUAGE_CONFIDENCE_THRESHOLD:
            candidate = UNDETERMINED
        source = LanguageSource.STT

    rows = await db.execute(
        "UPDATE videos "
        "   SET detected_language = $1, "
        "       language_source   = $2, "
        "       updated_at = now() "
        " WHERE id = $3 "
        "   AND (language_source IS DISTINCT FROM 'user')",
        candidate, source.value, video_id,
    )

    log.info("language_tagged video_id=%s code=%s source=%s "
             "user_override_skipped=%s",
             video_id, candidate, source.value, rows == 0)

    return TaggedLanguage(code=candidate, source=source)
```

### 4.2 Wire-up in `transcribe.commit`

```python
# pipeline/src/maktaba_pipeline/transcribe/commit.py — diff
async def commit_segment_set(db, video_id, transcripts: list[Transcript],
                             library_settings) -> None:
    async with db.transaction():
        for t in transcripts:
            await db.execute(_INSERT_TRANSCRIPT, ...)
        await _flip_state_to_transcribed(db, video_id)

        # Multi-audio: primary track's transcript drives the language.
        primary = _pick_primary_transcript(transcripts, library_settings)
        await tag_language(db, video_id=video_id,
                           library_settings=library_settings,
                           primary_transcript=primary)


def _pick_primary_transcript(transcripts, settings):
    if not transcripts:
        return Transcript(detected_language=None, language_confidence=0.0)
    if not settings.multi_audio:
        return transcripts[0]
    # multi_audio: primary track = is_default DESC, track_index ASC.
    return min(
        transcripts,
        key=lambda t: (not t.audio_track.is_default, t.audio_track.index),
    )
```

### 4.3 Go API — PATCH respects `language_source = 'user'`

```go
// api/internal/handlers/videos/patch.go — diff
if patch.DetectedLanguage != nil {
    if err := d.Queries.PatchVideoLanguageByUser(ctx, db.PatchVideoLanguageByUserParams{
        ID: videoID, DetectedLanguage: *patch.DetectedLanguage,
    }); err != nil { ... }
}
```

```sql
-- name: PatchVideoLanguageByUser :exec
UPDATE videos
   SET detected_language = $2,
       language_source   = 'user',
       updated_at        = now()
 WHERE id = $1;
```

The user write force-stamps `'user'`; subsequent re-runs of the tagger
skip the row.

## 5. Test plan

### 5.1 Unit tests (`test_tagger.py`)

| Test | What it pins |
|---|---|
| `test_high_confidence_writes_stt_code` | Transcript `lang='ar', conf=0.9` → `videos.detected_language='ar'`, `language_source='stt'`. AC-1. |
| `test_low_confidence_writes_und` | `lang='ar', conf=0.55` → `detected_language='und'`, `language_source='stt'`. AC-3. |
| `test_library_forced_overrides_stt` | Library `language='ar'`, transcript `lang='en'` → `detected_language='ar'`, `source='forced'`. (AC edge case "library with `language: 'ar'` — the user-pinned value is always written".) |
| `test_user_set_language_not_overwritten` | Pre-state `(detected_language='ar', language_source='user')` → tagger skips; rows-affected=0; warning log. |
| `test_multi_audio_primary_track_wins` | Three transcripts: idx=2 is_default=False, idx=0 is_default=True, idx=1 is_default=False; library.multi_audio=True → tagger uses idx=0's language. AC-2. |
| `test_multi_audio_off_uses_first_transcript` | library.multi_audio=False → first transcript's language wins. |
| `test_no_transcript_writes_und` | Empty `transcripts` → `und`. (Defensive; should not occur in practice.) |
| `test_concurrent_user_patch_during_transcribe_wins` | Race: user PATCHes `detected_language='en'` *during* the transcribe tx. The PATCH sets `language_source='user'`. The tagger's UPDATE filter `language_source IS DISTINCT FROM 'user'` wins last-write semantics: if PATCH commits first, tagger no-ops; if tagger commits first, PATCH overwrites. Both end states are valid. |

### 5.2 Integration test

`test_language_tag_integration.py`:

| Test | What it pins |
|---|---|
| `test_full_transcribe_commit_writes_language` | Stand up a fixture transcribe job; commit; assert `videos.detected_language` and `language_source` are set. |
| `test_user_patch_persists_across_reprocess` | Tag → PATCH → re-tag (simulated): final value is the user's. |
| `test_stats_cache_reflects_language_change` | Trigger from Story 9.7 fires; `library_stats_cache.by_language_jsonb` updates. |

## 6. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| User sets `detected_language` to a value Whisper would have set anyway | `language_source='user'`; tagger never re-runs the comparison; future re-tags skip. | `test_user_set_language_not_overwritten` |
| User clears the language (sets to `null`/empty) | Not allowed by API validator; return 422. The DB column is nullable but only the auto-tagger writes NULL (intermediate state during processing). | API validation (Epic 7 Story 7.4); not tested here |
| Code-switched audio | Whisper picks one; we honor it. Cross-language search via Chroma compensates (architecture §5.2). Documented edge case from the story. | Documented |
| Library `language: "auto"` with a transcript whose language Whisper rejected (returned None) | Treated as low-confidence → `und`. | `test_no_transcript_writes_und` (variant) |
| Library `language: "ar"` (forced) but transcript primary track says English with high conf | Forced wins; `source='forced'`. The user's archive knowledge beats Whisper's. | `test_library_forced_overrides_stt` |
| Race between transcribe commit and user PATCH | DB last-writer-wins is fine; both possible end states are valid. | `test_concurrent_user_patch_during_transcribe_wins` |

## 7. Configuration

| Key | Default | Effect |
|---|---|---|
| `language_confidence_threshold` (constant in code) | 0.6 | Below this, `und` is written. Per the story; no per-library override. |
| `library_settings.language` | `"auto"` | When set to an ISO-639-1, forces the value. |

## 8. Dependencies

| Dep | Source | Why |
|---|---|---|
| `transcripts.detected_language`, `transcripts.language_confidence` | Pipeline Story 3.7 | Source of the candidate value. |
| `audio_tracks` `is_default`, `track_index` | Epic 2 Story 2.3 | Primary-track selection. |
| Story 9.1 `EffectiveLibrarySettings` | required | `language` and `multi_audio`. |

## 9. Acceptance checklist

**Code**
- [ ] `pipeline/src/maktaba_pipeline/language/tagger.py` implements the function and the threshold.
- [ ] `transcribe.commit` calls `tag_language` inside the same tx as state flip.
- [ ] Go PATCH handler stamps `language_source='user'`.

**Migration**
- [ ] `videos.language_source` column added with the CHECK; existing rows backfilled to `'stt'`.
- [ ] `videos_detected_language_lookup` index exists.

**Behaviour (story acceptance criteria)**
- [ ] AC-1: high-confidence STT result populates `detected_language`.
- [ ] AC-2: in `multi_audio=True` libraries, the primary track wins.
- [ ] AC-3: confidence < 0.6 → `und`.

**Observability**
- [ ] Counter `maktaba_language_tag_total{source, code}`.
- [ ] Counter `maktaba_language_tag_user_override_skipped_total`.

**Docs**
- [ ] `specs/epics/09-library-management/README.md` ticks story 9.8.
- [ ] API reference documents the `language_source` field and the user-pin contract.
