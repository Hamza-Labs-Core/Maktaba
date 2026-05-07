# Story 1.6 — Video state machine

## Description

Resolves REVIEW §1.3.a (missing states) and §1.3.b (missing
`subtitle_gen` stage). The architecture document's FSM
(`DISCOVERED → PROBED → AUDIO_EXTRACTED → TRANSCRIBED → INDEXED →
THUMBNAILED → READY`, plus `FAILED`) is incomplete: stories across all
four epic groups introduce four additional states (`MISSING`,
`READY_NO_AUDIO`, `SUPERSEDED`, `CORRUPTED`) and one additional stage
(`subtitle_gen`). This story owns the canonical FSM and stage enum so
every other epic can reference one source of truth.

The video state lives in `videos.state TEXT NOT NULL`. The
`processing_jobs.stage` enum is defined in `architecture.md §7.1`.

## Canonical state set

| State | Terminal? | Owner story | Notes |
|-------|-----------|-------------|-------|
| `DISCOVERED` | No | [01-scanner](story-01-01-file-discovery.md) | Default after insert. |
| `PROBED` | No | [02-audio-extraction](../02-audio-extraction/story-02-01-audio-probe.md) | Set when probe completes. |
| `AUDIO_EXTRACTED` | No | [02-audio-extraction](../02-audio-extraction/story-02-03-stream-extraction.md) | Set when extract finishes (or skipped if streaming-only path). |
| `TRANSCRIBED` | No | [03-transcription](../03-transcription/story-03-06-segment-commit.md) | Set when the active transcript flips to `is_active=true` and `processed_seconds == total_duration_seconds`. |
| `INDEXED` | No | [05-search-indexing](../05-search-indexing/story-05-05-incremental-indexing.md) | Set when both `subtitle_gen` and `index` stages finish. |
| `THUMBNAILED` | No | Pipeline thumbnail story (Epic 22 / Stories 6.7+ in Job Queue) | Set when thumbnails write. |
| `READY` | Yes (terminal-good) | Pipeline orchestrator | Set when `THUMBNAILED` and no required stage is pending. |
| `READY_NO_AUDIO` | Yes (terminal-good) | [02-audio-extraction](../02-audio-extraction/story-02-01-audio-probe.md) | Reached from `PROBED` when the file has zero audio tracks. Indexable on title/description; no transcript. |
| `MISSING` | No (sink) | [01-scanner](story-01-03-filesystem-watcher.md) | Reached from any state when the source file disappears. Single transition out: `→ DISCOVERED` on rediscovery (matched by `content_hash`). |
| `SUPERSEDED` | Yes (terminal-soft) | Epic 9 (Library) — library merge / deletion | Reached when a video row is replaced by another (e.g., library re-mount under a new ID). Derived data preserved for audit. |
| `CORRUPTED` | Yes (terminal-bad) | Epic 24 (Disaster Recovery) — integrity check | Reached when an integrity sweep detects an unrecoverable error (hash mismatch on a non-network filesystem). |
| `FAILED` | Yes (terminal-bad) | Pipeline orchestrator | Reached when any stage's `processing_jobs.state = 'failed'` and `attempts >= max_attempts`. |

## Allowed transitions

```
                        ┌──────────────┐
                        │  DISCOVERED  │◄─────────────────┐
                        └──────┬───────┘                  │
                               │ probe ok                 │
                               ▼                          │
                        ┌──────────────┐                  │
                        │    PROBED    │                  │
                        └──────┬───────┘                  │
                  no audio │       │ extract ok           │
                           ▼       ▼                      │
                ┌──────────────┐ ┌──────────────────┐     │
                │READY_NO_AUDIO│ │ AUDIO_EXTRACTED  │     │
                └──────────────┘ └────────┬─────────┘     │
                                          │ transcribe ok │
                                          ▼               │
                                  ┌──────────────┐        │
                                  │ TRANSCRIBED  │        │
                                  └──────┬───────┘        │
                            subtitle_gen+index ok         │
                                          ▼               │
                                  ┌──────────────┐        │
                                  │   INDEXED    │        │
                                  └──────┬───────┘        │
                                         │ thumbnail ok   │
                                         ▼                │
                                  ┌──────────────┐        │
                                  │ THUMBNAILED  │        │
                                  └──────┬───────┘        │
                                         │ all gates ok   │
                                         ▼                │
                                  ┌──────────────┐        │
                                  │    READY     │        │
                                  └──────────────┘        │
                                                          │
   Any state ───file deleted──► MISSING ──rediscovered────┘
   Any state ───stage fail (terminal)──► FAILED
   Any state ───integrity fail──► CORRUPTED
   Any state ───library merge / replace──► SUPERSEDED
```

## Canonical stage set

`processing_jobs.stage` may take **exactly** these values:

```
scan | probe | extract | transcribe | subtitle_gen | index | thumbnail
```

`subtitle_gen` is added as a real stage (resolves REVIEW §1.3.b). It runs
after `transcribe` finishes for a given `transcript_id` and writes the
SRT/VTT artifacts (Epic 4 Story 4.1). It does **not** introduce a new
video state; the video remains in `TRANSCRIBED` until both `subtitle_gen`
and `index` reach `done`, at which point the orchestrator advances to
`INDEXED`.

The architecture document's stage comment (`scan|probe|extract|transcribe|index|thumb`)
and the "thumb" vs "thumbnail" disagreement (REVIEW §1.3.c) are settled
here: the canonical stage name is **`thumbnail`**.

## Acceptance criteria

- The enum in `pipeline/src/maktaba_pipeline/domain/states.py` lists
  exactly the 12 states above. Any code path that writes a value not in
  this set fails a unit test.
- The enum in `pipeline/src/maktaba_pipeline/domain/stages.py` lists
  exactly the 7 stage names above. The `processing_jobs.stage` CHECK
  constraint matches.
- A migration `shared/db/migrations/000X_video_states_and_stages.sql`
  adds a `CHECK (state IN (…))` constraint to `videos.state` and a
  `CHECK (stage IN (…))` constraint to `processing_jobs.stage`. Existing
  rows whose values are already inside the set pass; rows with legacy
  values (`thumb`) are rewritten in the same migration.
- A transition table is exposed via
  `pipeline.domain.states.allowed_transitions: dict[State, set[State]]`.
  Every state-changing UPDATE in `pipeline/` reads from this table; an
  attempt to transition outside the allowed set raises
  `IllegalStateTransition`.
- The orchestrator's `advance_after_stage(video_id, stage, outcome)`
  function is the **only** code path that mutates `videos.state`. All
  other writers (Epic 9 library deletion, Epic 24 integrity sweep) call
  this function rather than UPDATE-ing the column directly.

## Test cases

- `test_state_enum_matches_spec` — `set(State) == {DISCOVERED, PROBED,
  AUDIO_EXTRACTED, TRANSCRIBED, INDEXED, THUMBNAILED, READY,
  READY_NO_AUDIO, MISSING, SUPERSEDED, CORRUPTED, FAILED}`.
- `test_stage_enum_matches_spec` — `set(Stage) == {scan, probe, extract,
  transcribe, subtitle_gen, index, thumbnail}`.
- `test_check_constraints_reject_legacy` — INSERT of `videos(state='thumb')`
  fails; INSERT of `processing_jobs(stage='thumb')` fails.
- `test_allowed_transitions_table_complete` — for every pair (from, to)
  in the source diagram, the table allows it; every pair not in the
  diagram is rejected.
- `test_missing_to_discovered_round_trip` — set a video to MISSING; assert
  `advance_after_stage(video, 'scan', 'rediscovered')` flips it to
  DISCOVERED with no derived-data loss.
- `test_subtitle_gen_does_not_advance_to_indexed_alone` — finish only
  `subtitle_gen`, leave `index` pending → state remains `TRANSCRIBED`.
  Finish `index` too → state advances to `INDEXED`.
- `test_supersede_preserves_transcripts` — set a video to SUPERSEDED;
  `transcripts` rows for that `video_id` remain queryable for audit.
- `test_corrupted_blocks_further_processing` — a video in CORRUPTED
  state has no further `processing_jobs` enqueued by `advance_after_stage`.

## Edge cases

- **Race between stage finish and library deletion.** Library deletion
  drives `→ SUPERSEDED`; stage finish drives `→ INDEXED` (etc). The
  `advance_after_stage` function takes a row-level lock on `videos` and
  re-reads `state` before deciding; if the current state is terminal-soft
  (`SUPERSEDED`) or terminal-bad (`CORRUPTED`, `FAILED`), the stage's
  finish update is dropped (the work is wasted but the state is correct).
- **`MISSING → DISCOVERED` while a stale `processing_jobs(stage='probe')`
  row is still pending from before the disappearance.** The probe job is
  cancelled (Epic 6 Story 6.4) and a fresh probe is enqueued so the
  decision to re-run downstream stages is taken with current
  `media_info`.
- **A stage finishes after the video moved to `FAILED`.** The orchestrator
  ignores the success and logs `late_stage_finish`. The job row's own
  `state` is left at `done` so observability still reflects what happened.
- **Adding a new state in a future version.** Add to the enum, add
  rows to `allowed_transitions`, and run a migration to extend the CHECK
  constraint. The constraint must be `DROP CONSTRAINT … ; ADD CONSTRAINT
  … ;` to avoid a long-held lock on large `videos` tables.
