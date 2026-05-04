# Plan 9.8 — Auto-categorization: language tag — implementation

> Implementation plan for [story-09-08-language-tag.md](story-09-08-language-tag.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: depends on the transcribe stage commit
> path from Epic 3 ([Plan 3.6](../03-transcription/plan-03-06-segment-commit.md)
> — we share its DB transaction, not a separate one); writes
> `videos.detected_language` which the search filter (Epic 5) and the
> stats trigger (Plan 9.7) read; the user-pinned override surfaces via
> `PATCH /api/videos/{id}` extension to Epic 7 Story 7.4. Multi-audio
> primary-track selection is owned here.

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Language assignment runs INSIDE the transcribe stage's state-flip transaction.** The transcribe worker, as part of `flip_to_transcribed(...)` (Plan 3.6), calls a single helper `assign_detected_language(conn, video_id, library, transcripts_for_video)` that writes `videos.detected_language` in the same `BEGIN; … COMMIT;` that flips state to `TRANSCRIBED`. | Story AC-1: "in the same transaction that flips state". | Splitting into a separate transaction would mean a window where `state='TRANSCRIBED'` but `detected_language IS NULL`, which the stats trigger (Plan 9.7 D3) would count as `und` and then re-bucket on a later UPDATE — two cache writes for one logical event. One transaction means exactly one trigger fire, one stats-cache update. |
| D2 | **Confidence threshold 0.6 → `und` when below.** When Whisper reports `language_probability < 0.6`, we write `detected_language = 'und'`. Whisper's per-language probability lives on the transcript-level metadata (`transcripts.metrics->>'language_probability'`); we read it back from the just-inserted transcript row inside the same transaction. | Story AC-3 explicitly: "Whisper's language detection confidence < 0.6 → `detected_language = 'und'`". | The 0.6 threshold matches Whisper's own published "reliable detection" cutoff (the `large-v3` paper reports >95% accuracy at p ≥ 0.6). Below that, the language code is little better than a guess; tagging `und` is honest and lets the user's PATCH override stick. |
| D3 | **User-pinned library language always wins.** When `library.settings.language IS NOT NULL` (the library is "forced" to a language), we ignore Whisper's detection and write `videos.detected_language = library.settings.language`. We mark the source via `videos.language_source = 'library_pinned'` (a new column added by `0027_videos_language_source.sql`). | Story edge case: "A library with `language: 'ar'` (forced) — the user-pinned value is always written, regardless of STT confidence". | The user knows their archive better than Whisper does; tagging an Arabic-only library as `'und'` for one quiet 5-second clip would harm the search filter without informing anyone. The `language_source` column lets the UI render "library default" vs "auto-detected" badges. |
| D4 | **User PATCH override is sticky across re-processing.** When a PATCH writes `detected_language`, we set `videos.language_source = 'user'`. The transcribe stage's `assign_detected_language` reads `language_source` first; if `'user'`, it skips the auto-assignment entirely. | Story AC-3: "the user can manually set it via `PATCH /api/videos/{id}`"; story test "PATCH user-set language overrides STT's value and is preserved across re-processing". | Without a source flag, a re-transcribe would silently overwrite the user's choice. The stickiness is the contract the user expects. |
| D5 | **Multi-audio: the primary track wins.** For `library.settings.multi_audio = true`, multiple transcripts exist per video (one per audio track). The "primary" track is selected by, in order: (a) the track with `audio_tracks.is_default = true`; (b) the lowest `audio_tracks.index`. The primary's transcript language goes on `videos.detected_language`; the per-track languages stay on the `transcripts.language` column already there. | Story AC-2: "the *primary* track's language goes on `videos.detected_language`; per-track languages live on the transcripts rows". | Most multi-audio files declare the original-language track as default (FFmpeg's `default` disposition). Falling back to lowest index matches FFmpeg's default-track convention. The library setting `multi_audio` already exists from Story 9.1; this story only consumes it. |
| D6 | **The helper takes `conn` (an already-open transaction), not a pool.** The transcribe stage opens the transaction, inserts the transcript rows, calls `assign_detected_language(conn, ...)`, then flips state. Atomicity is the caller's responsibility; we make it natural by accepting a connection. | Refines the story (which is silent on the API). | Passing a `pool` would make the helper open its own transaction, breaking D1's single-transaction guarantee. The connection-passing pattern is the same as Plan 5.7's `replace_in_active_flip`. |
| D7 | **Code-switched audio: Whisper's choice stands.** Whisper picks one language for the whole audio; we tag that. Per-segment language switches live in `transcript_segments.language` (from Story 3.x). This story does NOT introduce a `'mixed'` language code on `videos.detected_language` — that field stays single-valued ISO 639-1. | Story edge case: "A code-switched audio (Arabic-English mix) — Whisper picks one; cross-language search via Chroma still works because embeddings are multilingual". | The library-level `detected_language` is used by the simple filter `?lang=ar`. A `'mixed'` value would force every consumer (filter, stats, UI badge) to add a special case. Cross-language search is solved by the multilingual embedder, which is the right place for that capability. |
| D8 | **`und` is a real language code in our schema.** `detected_language = 'und'` is the documented "undetermined" value (ISO 639-3 reserves `und`). `videos.detected_language` is `TEXT NOT NULL DEFAULT 'und'` after this migration. | Refines architecture §8.1 (which has `detected_language TEXT` nullable). | NULL semantics for "undetermined" forces every consumer to handle two cases (NULL and 'und'). One value, one code path. The migration backfills NULLs to `'und'`. |
| D9 | **No background re-categorization.** When `library.settings.language` is changed (e.g., user pins a previously-auto library), we do **not** automatically rewrite existing rows. A separate maintenance command `maktaba-api lang-rebuild --library-id=X` does so on demand. | Refines the story; needed to bound surprise. | Auto-rewriting touches every video in the library and fires a stats trigger per row — a 50k-row library would generate 50k cache UPDATEs in a single user-facing PATCH. The opt-in command is the right ergonomics. |
| D10 | **Confidence threshold and the override knob are config, not constants.** `pipeline/src/maktaba_pipeline/config/defaults.py` carries `LANGUAGE_DETECT_THRESHOLD = 0.6`; per-library override at `library.settings.language_threshold`. | Story AC-3 (default 0.6); refines: per-library override allows tightening for high-stakes archives. | A scholarly Arabic library may want 0.8 to avoid `und` poisoning; a household library is fine at 0.6. Config-as-data is the standard pattern. |

If D1 is rejected (separate transaction): the stats trigger fires twice per transcribe (once for state flip, once for language write), doubling the trigger cost. Worse, a crash between the two transactions leaves an inconsistent row that the user sees in the UI.

If D5 is rejected (per-track choice): the simple `?lang=ar` filter would have to JOIN through `transcripts → audio_tracks` and dedupe, adding ~30 ms to every search. Tagging the primary on `videos` keeps the filter index-keyed.

---

## 1. Architecture diagram — language assignment flow

```
   Transcribe stage worker (Plan 3.6)
            │
            ▼
   ┌──────────────────────────────────────────┐
   │ Inside the state-flip transaction:       │
   │                                          │
   │   BEGIN;                                 │
   │   INSERT INTO transcripts (...)          │
   │     for each audio_track                 │
   │   INSERT INTO transcript_segments (...)  │
   │                                          │
   │   ┌─────────────────────────────────┐    │
   │   │ assign_detected_language(       │    │
   │   │   conn, video_id, library)      │    │
   │   │                                 │    │
   │   │ 1. Read language_source from    │    │
   │   │    videos (FOR UPDATE).         │    │
   │   │                                 │    │
   │   │    if 'user': return (D4)       │    │
   │   │                                 │    │
   │   │ 2. If library.settings.language │    │
   │   │    is set:                      │    │
   │   │      lang = library_pinned      │    │
   │   │      source = 'library_pinned'  │    │
   │   │    else:                        │    │
   │   │      pick primary transcript    │    │
   │   │        (D5)                     │    │
   │   │      read its lang + prob       │    │
   │   │      if prob >= 0.6:            │    │
   │   │        lang = transcript.lang   │    │
   │   │        source = 'auto'          │    │
   │   │      else:                      │    │
   │   │        lang = 'und'             │    │
   │   │        source = 'auto_low_conf' │    │
   │   │                                 │    │
   │   │ 3. UPDATE videos SET             │   │
   │   │      detected_language = $1,    │    │
   │   │      language_source = $2       │    │
   │   │    WHERE id = $3                │    │
   │   └─────────────────────────────────┘    │
   │                                          │
   │   UPDATE videos SET state='TRANSCRIBED'  │
   │   COMMIT;                                │
   └──────────────────────────────────────────┘
            │
            ▼
   ┌──────────────────────────────────────────┐
   │ Stats trigger (Plan 9.7) fires once,     │
   │ adjusting by_language_jsonb in one go.   │
   └──────────────────────────────────────────┘

   Independent path: PATCH /api/videos/{id}
   ───────────────────────────────────────
   Go API handler:
     UPDATE videos
        SET detected_language = $body.language,
            language_source   = 'user'
      WHERE id = $1
   → stats trigger rebalances by_language_jsonb
   → next transcribe re-run for this video sees
     'user' source and skips auto-assignment (D4).
```

---

## 2. Detailed implementation

### 2.1 Package layout — Python (Pipeline Service)

```
pipeline/src/maktaba_pipeline/
├── language/
│   ├── __init__.py                     // public surface
│   ├── assigner.py                     // assign_detected_language(conn, ...)
│   ├── primary_track.py                // pick_primary_transcript(transcripts, audio_tracks)
│   ├── source.py                       // LanguageSource enum
│   ├── errors.py
│   └── tests/
│       ├── conftest.py
│       ├── test_assigner_high_confidence.py
│       ├── test_assigner_low_confidence_und.py
│       ├── test_assigner_library_pinned.py
│       ├── test_assigner_user_override_preserved.py
│       ├── test_primary_track_selection.py
│       └── test_assigner_multi_audio.py
└── pipeline/
    └── stages/
        └── transcribe.py               // extended: call assigner before state flip
```

### 2.2 Schema migration — `language_source` column

```sql
-- shared/db/migrations/0027_videos_language_source.sql
BEGIN;

-- Backfill existing NULL detected_language to 'und' (D8).
UPDATE videos SET detected_language = 'und'
 WHERE detected_language IS NULL;

ALTER TABLE videos
    ALTER COLUMN detected_language SET NOT NULL,
    ALTER COLUMN detected_language SET DEFAULT 'und';

ALTER TABLE videos
    ADD COLUMN language_source TEXT NOT NULL DEFAULT 'auto'
        CHECK (language_source IN (
            'auto', 'auto_low_conf', 'library_pinned', 'user'));

CREATE INDEX videos_language_source ON videos (library_id, language_source);

COMMIT;
```

### 2.3 Python — `source.py`

```python
"""LanguageSource — provenance for videos.detected_language."""
from __future__ import annotations
from enum import StrEnum


class LanguageSource(StrEnum):
    AUTO = "auto"                          # Whisper, p ≥ threshold
    AUTO_LOW_CONF = "auto_low_conf"        # Whisper said something, p < threshold → 'und'
    LIBRARY_PINNED = "library_pinned"      # library.settings.language enforced
    USER = "user"                          # PATCH /api/videos/{id} set this
```

### 2.4 Python — `primary_track.py` (D5)

```python
"""primary_track.py — pick the primary transcript for a multi-audio video.

Selection rule:
  1. If exactly one transcript exists, that one is primary.
  2. Else, find the transcript whose audio_track_id has is_default=true.
  3. Else, the transcript whose audio_track has the lowest index.
"""
from __future__ import annotations
from dataclasses import dataclass


@dataclass(frozen=True)
class TranscriptForLang:
    transcript_id: str
    audio_track_id: int
    audio_track_index: int
    audio_track_is_default: bool
    language: str
    language_probability: float | None


def pick_primary(transcripts: list[TranscriptForLang]) -> TranscriptForLang:
    if not transcripts:
        raise ValueError("no transcripts to pick from")
    if len(transcripts) == 1:
        return transcripts[0]
    # Default-disposition track first.
    for t in transcripts:
        if t.audio_track_is_default:
            return t
    # Otherwise, lowest index.
    return min(transcripts, key=lambda t: t.audio_track_index)
```

### 2.5 Python — `assigner.py` (D1, D2, D3, D4, D6, D10)

```python
"""assigner.py — single transactional helper.

Called from the transcribe stage tail, INSIDE the open transaction.
Reads videos.language_source FOR UPDATE; never auto-overrides 'user'.
"""
from __future__ import annotations
import logging
from dataclasses import dataclass

from .primary_track import TranscriptForLang, pick_primary
from .source import LanguageSource

log = logging.getLogger(__name__)

DEFAULT_THRESHOLD = 0.6


@dataclass(frozen=True)
class AssignResult:
    language: str
    source: LanguageSource
    probability: float | None
    primary_transcript_id: str | None


_READ_VIDEO_SQL = """
SELECT detected_language, language_source
  FROM videos WHERE id = $1
  FOR UPDATE
"""

_UPDATE_VIDEO_SQL = """
UPDATE videos
   SET detected_language = $1,
       language_source   = $2,
       updated_at        = now()
 WHERE id = $3
   AND language_source <> 'user'      -- D4: never overwrite USER
"""

_LOAD_TRANSCRIPTS_SQL = """
SELECT t.id::text       AS transcript_id,
       t.audio_track_id,
       a.index          AS audio_track_index,
       a.is_default     AS audio_track_is_default,
       t.language,
       (t.metrics->>'language_probability')::float AS language_probability
  FROM transcripts t
  JOIN audio_tracks a ON a.id = t.audio_track_id
 WHERE t.video_id = $1
   AND t.created_at >= $2     -- only transcripts from this run
 ORDER BY a.is_default DESC, a.index ASC
"""


def _resolve_threshold(library_settings: dict | None) -> float:
    if not library_settings:
        return DEFAULT_THRESHOLD
    val = library_settings.get("language_threshold")
    return float(val) if val is not None else DEFAULT_THRESHOLD


async def assign_detected_language(
    conn,
    *,
    video_id: str,
    library_id: str,
    library_settings: dict | None,
    run_started_at,
) -> AssignResult:
    # Step 1: lock the row, read current source.
    cur = await conn.fetchrow(_READ_VIDEO_SQL, video_id)
    if cur is None:
        raise LookupError(f"video {video_id} disappeared mid-transaction")
    if cur["language_source"] == LanguageSource.USER.value:
        # D4: user override sticks, skip entirely.
        return AssignResult(language=cur["detected_language"],
                            source=LanguageSource.USER,
                            probability=None,
                            primary_transcript_id=None)

    # Step 2: library-pinned wins (D3).
    pinned = (library_settings or {}).get("language")
    if pinned:
        await conn.execute(_UPDATE_VIDEO_SQL, pinned,
                           LanguageSource.LIBRARY_PINNED.value, video_id)
        return AssignResult(language=pinned,
                            source=LanguageSource.LIBRARY_PINNED,
                            probability=None,
                            primary_transcript_id=None)

    # Step 3: pick primary transcript (D5) and decide.
    rows = await conn.fetch(_LOAD_TRANSCRIPTS_SQL, video_id, run_started_at)
    if not rows:
        # Shouldn't happen: transcribe stage just wrote at least one transcript.
        log.warning("no_transcripts_for_lang_assign", extra={"video_id": video_id})
        await conn.execute(_UPDATE_VIDEO_SQL, "und",
                           LanguageSource.AUTO_LOW_CONF.value, video_id)
        return AssignResult(language="und",
                            source=LanguageSource.AUTO_LOW_CONF,
                            probability=None,
                            primary_transcript_id=None)

    transcripts = [TranscriptForLang(
        transcript_id=r["transcript_id"],
        audio_track_id=r["audio_track_id"],
        audio_track_index=r["audio_track_index"],
        audio_track_is_default=r["audio_track_is_default"],
        language=r["language"],
        language_probability=r["language_probability"],
    ) for r in rows]
    primary = pick_primary(transcripts)

    threshold = _resolve_threshold(library_settings)
    prob = primary.language_probability or 0.0
    if prob >= threshold:
        lang, src = primary.language, LanguageSource.AUTO
    else:
        lang, src = "und", LanguageSource.AUTO_LOW_CONF

    await conn.execute(_UPDATE_VIDEO_SQL, lang, src.value, video_id)
    return AssignResult(
        language=lang, source=src,
        probability=primary.language_probability,
        primary_transcript_id=primary.transcript_id,
    )
```

### 2.6 Python — transcribe stage tail integration

```python
# pipeline/src/maktaba_pipeline/pipeline/stages/transcribe.py  (excerpt)
from maktaba_pipeline.language.assigner import assign_detected_language

async def commit_transcripts_and_flip_state(ctx, claimed_job, run_started_at):
    async with ctx.db_pool.acquire() as conn:
        async with conn.transaction():
            # 1. Insert transcripts + segments (Plan 3.6).
            await _insert_all_transcripts(conn, claimed_job)

            # 2. Assign language INSIDE the same xact (D1).
            await assign_detected_language(
                conn,
                video_id=claimed_job.video_id,
                library_id=claimed_job.library_id,
                library_settings=claimed_job.library_settings,
                run_started_at=run_started_at,
            )

            # 3. Flip state.
            await conn.execute(
                "UPDATE videos SET state='TRANSCRIBED', updated_at=now() WHERE id=$1",
                claimed_job.video_id)
```

### 2.7 Go — PATCH override extension (D4)

The PATCH handler already exists in Epic 7 Story 7.4; this plan adds two
lines.

```go
// apps/api/internal/http/videos/patch.go  (excerpt — only the new path)
type PatchBody struct {
    Title             *string `json:"title,omitempty"`
    DetectedLanguage  *string `json:"detected_language,omitempty"`
    // ... existing fields ...
}

func (h *Handler) Patch(w http.ResponseWriter, r *http.Request) {
    // ... existing parse + auth ...

    if body.DetectedLanguage != nil {
        if err := validateISO639(*body.DetectedLanguage); err != nil {
            http.Error(w, err.Error(), http.StatusBadRequest)
            return
        }
        // D4: PATCH writes language_source='user' — sticks across re-processing.
        _, err := h.pool.Exec(r.Context(), `
            UPDATE videos
               SET detected_language = $1,
                   language_source   = 'user',
                   updated_at        = now()
             WHERE id = $2
        `, *body.DetectedLanguage, videoID)
        if err != nil {
            http.Error(w, "update failed", http.StatusInternalServerError)
            return
        }
    }
    // ... other fields ...
}

func validateISO639(code string) error {
    if code == "und" {
        return nil
    }
    if len(code) != 2 || !isLowerASCII(code) {
        return fmt.Errorf("invalid language code: %q", code)
    }
    return nil
}
```

### 2.8 CLI — `lang-rebuild` (D9)

```go
// apps/api/internal/cmd/lang_rebuild.go (sketch)
//
// Iterates videos in the library; for each, reads the primary
// transcript and re-applies the language assignment logic, EXCEPT for
// rows with language_source='user' (D4). Useful after a user pins
// library.settings.language and wants existing rows updated.
//
// Usage:
//   maktaba-api lang-rebuild --library-id=<uuid> [--dry-run]
```

---

## 3. File-by-file scaffolding checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `shared/db/migrations/0027_videos_language_source.sql` | `language_source` column, CHECK, idx; backfill | `TestMigration0027` |
| 2 | `pipeline/src/maktaba_pipeline/language/__init__.py` | re-exports | (n/a) |
| 3 | `pipeline/src/maktaba_pipeline/language/source.py` | `LanguageSource` | `test_source_enum` |
| 4 | `pipeline/src/maktaba_pipeline/language/primary_track.py` | `TranscriptForLang`, `pick_primary` | `test_primary_track_selection` |
| 5 | `pipeline/src/maktaba_pipeline/language/assigner.py` | `assign_detected_language`, `AssignResult`, `_resolve_threshold` | `test_assigner_*` |
| 6 | `pipeline/src/maktaba_pipeline/language/errors.py` | `LanguageAssignError` | (n/a) |
| 7 | `pipeline/src/maktaba_pipeline/pipeline/stages/transcribe.py` (extend) | call into assigner before state flip | `test_transcribe_calls_assigner` |
| 8 | `apps/api/internal/http/videos/patch.go` (extend) | `detected_language` PATCH path | `TestPatchVideo_LanguageOverride` |
| 9 | `apps/api/internal/cmd/lang_rebuild.go` | `NewLangRebuildCmd` | `TestLangRebuild` |

---

## 4. Test cases

### 4.1 `test_assigner_high_confidence_writes_lang` (AC-1)

```python
async def test_high_confidence_arabic_writes_ar(
    conn_in_xact, video_factory, transcript_factory,
):
    """Whisper says 'ar' with prob 0.92 → videos.detected_language='ar'."""
    video = await video_factory.fresh()
    await transcript_factory.fresh(
        video_id=video.id, language="ar", language_probability=0.92,
        is_default_track=True)

    result = await assign_detected_language(
        conn=conn_in_xact, video_id=video.id, library_id=video.library_id,
        library_settings=None, run_started_at=video.created_at)

    assert result.language == "ar"
    assert result.source == LanguageSource.AUTO
    row = await conn_in_xact.fetchrow(
        "SELECT detected_language, language_source FROM videos WHERE id=$1",
        video.id)
    assert row["detected_language"] == "ar"
    assert row["language_source"] == "auto"
```

### 4.2 `test_assigner_low_confidence_yields_und` (AC-3)

```python
async def test_low_confidence_writes_und(
    conn_in_xact, video_factory, transcript_factory,
):
    """Whisper prob 0.4 → 'und', source='auto_low_conf'."""
    video = await video_factory.fresh()
    await transcript_factory.fresh(
        video_id=video.id, language="ar", language_probability=0.4,
        is_default_track=True)

    result = await assign_detected_language(
        conn=conn_in_xact, video_id=video.id, library_id=video.library_id,
        library_settings=None, run_started_at=video.created_at)
    assert result.language == "und"
    assert result.source == LanguageSource.AUTO_LOW_CONF
```

### 4.3 `test_assigner_library_pinned_overrides_low_confidence` (D3, edge)

```python
async def test_library_pinned_writes_pinned_lang(
    conn_in_xact, video_factory, transcript_factory,
):
    """Library settings.language='ar' wins even if Whisper said 'en' p=0.95."""
    video = await video_factory.fresh()
    await transcript_factory.fresh(
        video_id=video.id, language="en", language_probability=0.95,
        is_default_track=True)

    result = await assign_detected_language(
        conn=conn_in_xact, video_id=video.id, library_id=video.library_id,
        library_settings={"language": "ar"}, run_started_at=video.created_at)
    assert result.language == "ar"
    assert result.source == LanguageSource.LIBRARY_PINNED
```

### 4.4 `test_user_override_sticks_across_reprocess` (AC-3, D4, story-named)

```python
async def test_user_pinned_lang_preserved_on_retranscribe(
    db, conn_in_xact, video_factory, transcript_factory,
):
    """videos.language_source='user' → assigner is a no-op."""
    video = await video_factory.fresh()
    # User PATCH'd lang manually.
    await db.execute(
        "UPDATE videos SET detected_language='ar', language_source='user' WHERE id=$1",
        video.id)
    # Re-transcribe runs and Whisper says 'en' with high confidence.
    await transcript_factory.fresh(
        video_id=video.id, language="en", language_probability=0.95,
        is_default_track=True)

    result = await assign_detected_language(
        conn=conn_in_xact, video_id=video.id, library_id=video.library_id,
        library_settings=None, run_started_at=video.created_at)
    assert result.source == LanguageSource.USER
    row = await conn_in_xact.fetchrow(
        "SELECT detected_language, language_source FROM videos WHERE id=$1",
        video.id)
    assert row["detected_language"] == "ar"     # unchanged
    assert row["language_source"]   == "user"
```

### 4.5 `test_assigner_multi_audio_picks_default_track` (AC-2, D5)

```python
async def test_multi_audio_default_track_wins(
    conn_in_xact, video_factory, transcript_factory,
):
    """Two audio tracks: ar-default and en-secondary → videos.lang='ar'."""
    video = await video_factory.fresh()
    await transcript_factory.fresh(
        video_id=video.id, language="ar", language_probability=0.95,
        audio_track_index=0, is_default_track=True)
    await transcript_factory.fresh(
        video_id=video.id, language="en", language_probability=0.95,
        audio_track_index=1, is_default_track=False)

    result = await assign_detected_language(
        conn=conn_in_xact, video_id=video.id, library_id=video.library_id,
        library_settings={"multi_audio": True}, run_started_at=video.created_at)
    assert result.language == "ar"
```

### 4.6 `test_primary_track_selection_lowest_index_when_no_default`

```python
def test_pick_primary_lowest_index_when_no_default():
    transcripts = [
        TranscriptForLang(transcript_id="t1", audio_track_id=1,
                          audio_track_index=2, audio_track_is_default=False,
                          language="en", language_probability=0.9),
        TranscriptForLang(transcript_id="t2", audio_track_id=2,
                          audio_track_index=0, audio_track_is_default=False,
                          language="ar", language_probability=0.9),
    ]
    primary = pick_primary(transcripts)
    assert primary.transcript_id == "t2"  # lowest index wins
```

### 4.7 `test_one_transaction_one_stats_trigger_fire` (D1)

```python
async def test_lang_assign_and_state_flip_one_xact(
    db, video_factory, transcript_factory,
):
    """The stats cache should see exactly ONE delta from this commit."""
    video = await video_factory.fresh(state='AUDIO_EXTRACTED')
    await transcript_factory.fresh(
        video_id=video.id, language="ar", language_probability=0.9,
        is_default_track=True, persisted=False)  # write inside our xact

    # Snapshot cache row's by_language.
    before = await db.fetchval(
        "SELECT by_language_jsonb FROM library_stats_cache WHERE library_id=$1",
        video.library_id)

    async with db.acquire() as conn:
        async with conn.transaction():
            await transcript_factory.persist(conn, video_id=video.id)
            await assign_detected_language(
                conn=conn, video_id=video.id, library_id=video.library_id,
                library_settings=None, run_started_at=video.created_at)
            await conn.execute(
                "UPDATE videos SET state='TRANSCRIBED' WHERE id=$1", video.id)

    after = await db.fetchval(
        "SELECT by_language_jsonb FROM library_stats_cache WHERE library_id=$1",
        video.library_id)
    # Exactly one increment under 'ar'.
    assert (after.get("ar", 0) - before.get("ar", 0)) == 1
```

### 4.8 `TestPatchVideo_LanguageOverride` (Go, AC-3)

```go
func TestPatchVideo_DetectedLanguage_SetsSourceUser(t *testing.T) {
    db := testdb.Fresh(t)
    libID := testdb.SeedLibrary(t, db, "videos")
    vid := testdb.SeedVideo(t, db, libID, "TRANSCRIBED")

    body := `{"detected_language":"ar"}`
    req := httptest.NewRequest("PATCH", "/api/videos/"+vid.String(),
        strings.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    req = withChiCtx(req, "id", vid.String())
    rr := httptest.NewRecorder()
    h := videos.NewHandler(db.Pool, slog.Default())
    h.Patch(rr, req)
    require.Equal(t, http.StatusOK, rr.Code)

    var lang, src string
    require.NoError(t, db.Pool.QueryRow(t.Context(),
        `SELECT detected_language, language_source FROM videos WHERE id=$1`,
        vid).Scan(&lang, &src))
    require.Equal(t, "ar", lang)
    require.Equal(t, "user", src)
}
```

---

## 5. Edge cases and how the plan handles each

| #   | Edge case | Handled by |
|-----|-----------|------------|
| E1  | **Whisper returns no `language_probability` field** (older backend). The probability is NULL; `prob` resolves to 0.0; threshold check fails; we tag `'und'` with source `auto_low_conf`. | `test_assigner_no_probability_field` |
| E2  | **Library forces a language** (story-named edge). Library `settings.language='ar'` always writes `'ar'` regardless of Whisper or threshold. The `language_source='library_pinned'` flag tells the UI not to render "auto-detected". | `test_assigner_library_pinned_overrides_low_confidence` |
| E3  | **Code-switched audio** (story-named edge). Whisper picks one language for the whole audio; we store that. Per-segment switches stay on `transcript_segments.language`. The `'mixed'` value is NOT introduced for `videos.detected_language` (D7). | (Documented; no special test — same path as E1 with single language.) |
| E4  | **User PATCH then re-transcribe** (story-named test). `language_source='user'` makes the assigner short-circuit; user value preserved. | `test_user_pinned_lang_preserved_on_retranscribe` |
| E5  | **Transcribe stage crashes between INSERT transcripts and assigner call.** Both are inside the same DB transaction (D1, D6); the crash rolls back both. The next claim will redo the work atomically. | `test_assigner_atomic_with_state_flip` |
| E6  | **Multi-audio with no default track flagged.** Lowest-index track wins (D5 fallback). Most multi-audio files have a default; this is a defensive path. | `test_pick_primary_lowest_index_when_no_default` |
| E7  | **Multi-audio with mixed languages and primary's confidence low.** Whisper picks a language per track; the primary's track may have low confidence even if a secondary is high-confidence. We honour the primary's confidence — if low → `'und'`. The user can PATCH to override. | `test_multi_audio_low_confidence_primary_yields_und` |
| E8  | **`language` column has 3-letter codes from a non-Whisper backend.** Validation rejects PATCH bodies that aren't 2-letter ISO 639-1 (or `'und'`); the assigner trusts the backend's value but we document that backends MUST emit ISO 639-1 codes (Story 3.x backend protocol). | `validateISO639` rejects mismatched codes |
| E9  | **PATCH to `'und'`** (user explicitly says "I don't know"). Allowed; `language_source='user'` is set; future re-transcribes don't overwrite. | `TestPatchVideo_DetectedLanguage_AllowsUnd` |
| E10 | **Library deletion cascade.** `videos` rows cascade-delete; the assigner is never called for a deleted row. The stats trigger handles the cache update on cascade. | DB-level `ON DELETE CASCADE` |
| E11 | **Concurrent PATCH and transcribe.** PATCH UPDATE ... WHERE id=$1 takes a row lock; the transcribe stage's `SELECT ... FOR UPDATE` blocks behind the PATCH. Whichever commits last wins, but PATCH sets `language_source='user'` so the assigner's `WHERE language_source <> 'user'` clause prevents accidental overwrite. | `test_concurrent_patch_and_assigner_no_overwrite` |
| E12 | **Threshold tightening per library** (D10). `library.settings.language_threshold = 0.8` for a high-stakes archive; the assigner reads it and applies. | `test_library_threshold_override` |

---

## 6. Acceptance checklist

- [ ] **A1** Single-language assignment: a transcript with `language='ar'` and `prob ≥ 0.6` writes `videos.detected_language='ar'` in the same transaction that flips state to `TRANSCRIBED`. (`test_high_confidence_arabic_writes_ar`, `test_lang_assign_and_state_flip_one_xact`)
- [ ] **A2** Multi-audio: the primary track (default disposition or lowest index) decides `videos.detected_language`; per-track languages remain on `transcripts.language`. (`test_multi_audio_default_track_wins`, `test_pick_primary_lowest_index_when_no_default`)
- [ ] **A3** Confidence threshold 0.6: Whisper `language_probability < 0.6` → `videos.detected_language='und'` with `language_source='auto_low_conf'`. (`test_low_confidence_writes_und`)
- [ ] **A4** Library pinning: `library.settings.language='ar'` always writes `'ar'` regardless of Whisper, with `language_source='library_pinned'`. (`test_library_pinned_writes_pinned_lang`)
- [ ] **A5** User PATCH override: `PATCH /api/videos/{id}` with `{"detected_language":"ar"}` sets `language_source='user'`; subsequent re-transcribe does NOT overwrite. (`TestPatchVideo_DetectedLanguage_SetsSourceUser`, `test_user_pinned_lang_preserved_on_retranscribe`)
- [ ] **A6** Migration `0027_videos_language_source.sql` adds the column with CHECK, the index, and backfills NULL `detected_language` to `'und'` while making the column NOT NULL. (`TestMigration0027`)
- [ ] **A7** The assigner is called inside the transcribe state-flip transaction (D1); the stats trigger (Plan 9.7) sees exactly one rebalance per commit. (`test_lang_assign_and_state_flip_one_xact`)
- [ ] **A8** Per-library threshold override via `library.settings.language_threshold`. (`test_library_threshold_override`)
- [ ] **A9** PATCH validates the body language code: 2-letter ISO 639-1 or `'und'`; everything else returns 400. (`TestPatchVideo_DetectedLanguage_RejectsInvalidCode`)
- [ ] **A10** No background re-categorization on `library.settings.language` change; the opt-in `maktaba-api lang-rebuild` covers it. (`TestLangRebuild`)

---

## 7. Performance budget

The language-assignment path is dominated by the existing transcribe
commit transaction; we only add one read and one UPDATE.

| Phase | Cost (per video) | Notes |
|-------|------------------|-------|
| `SELECT language_source FROM videos … FOR UPDATE` | ~50 µs | primary-key lookup; lock already taken by the transcribe state-flip path. |
| `SELECT … FROM transcripts JOIN audio_tracks` (1 video) | ~200 µs | indexed on `(video_id, created_at)`; typically 1–3 rows. |
| `pick_primary` | < 1 µs | in-memory sort over ≤ ~5 elements. |
| `UPDATE videos SET detected_language, language_source` | ~100 µs | one indexed UPDATE; row already locked. |
| **Total** | **~350 µs** added to the transcribe commit transaction | negligible against the ~10 ms commit cost. |

The PATCH override path is a single `UPDATE` — ~1 ms end-to-end.

---

## 8. Operational notes

- **Backfill plan after migration 0027:** No-op for libraries with NULL
  `detected_language` — the migration backfills to `'und'`. Existing
  rows with `'ar'` etc. retain their value and get `language_source='auto'`.
- **Search filter compatibility:** `?lang=ar` continues to use
  `WHERE detected_language = 'ar'` on the existing index
  `videos (detected_language)` from architecture §8.1.
- **Metrics:**
  - `language_assign_total{source}` — counter, labelled by `LanguageSource`.
  - `language_assign_low_confidence_total{library_id}` — counter, fires on every `auto_low_conf` outcome; high values flag a library that may need pinning.
- **`maktaba-api lang-rebuild`** is an idempotent one-shot; safe to run
  during normal operation. It does NOT bypass `language_source='user'`.
