# Story 52.14 — Settings, thresholds, budget and queue health

> Epic 52 · Phase 2 · Depends on: 52.6 · Needed by: 52.11

## Why

How many words a person is asked to check is a trade-off. Too many and the queue is ignored; too
few and mistakes stay. That trade-off belongs to the owner, per library: a Quran library, a
lecture library and an English documentary library want different settings. The new stages also
have to show their health like every other queue (Epic 40), and must not slow transcription on the
home Mac.

## Description

### 1. The library setting `word_review`

It is stored in the library's settings JSON (next to `stt`), read through `library_setting` by the
stages.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `enabled` | bool | `true` | `word_check` runs on this library's new transcripts |
| `backfill` | bool | `false` | Also check existing transcripts (text-only signals) |
| `sensitivity` | `low` / `normal` / `high` | `normal` | Moves every reason's threshold together: low = fewer, surer flags |
| `budget_per_hour` | int 10-200 | `40` | The most flags per hour of audio (README §4.6) |
| `reasons` | map reason → bool | all `true` | Turn a reason off (for example `not_in_dictionary` on a dialect library) |
| `llm_judge.enabled` | bool | `false` | Run `word_judge` (52.7) |
| `llm_judge.model` | string | `""` | A model name the cluster's LLM runtimes have (listed from `llm.models`) |
| `llm_judge.alone_may_flag` | bool | `false` | May the LLM's verdict alone create a flag |
| `reviewers_note` | string | `""` | A note shown at the top of the queue ("spell names as on the book covers") |

The sensitivity steps map to calibrated error thresholds per reason. Normal is 0.5, low 0.65,
high 0.35. They live in the pack-independent scoring table in `review/scoring.py`.

`word_check`'s input fingerprint includes this setting's hash. Changing it re-checks only that
library's transcripts, at low priority. It never touches `transcribe`.

### 2. API and page

- `GET /api/libraries/{id}/word-review`, `PUT /api/libraries/{id}/word-review`: **admin only**,
  checked inline like `transcription.go:304`. The PUT validates the ranges and the reason names,
  and checks that the model exists on the cluster.
- Page: a **Word review** section in the library's settings (Manage libraries, Epic 45) with the
  fields above, plain explanations, and an estimate line, e.g. "about 1,200 flags for this
  library's 30 hours at this setting". The estimate comes from the current flags' priority
  distribution.
- **Backfill control:** turning `backfill` on first shows the transcripts and audio hours to
  check and a CPU-time estimate (≤ 10 CPU-s per audio hour, README §4.10). It then enqueues
  `word_check` for those transcripts at the lowest priority. It can be paused like any queue
  stage (`POST /api/queues/word_check/pause`).
- **Language packs (home-wide):** a page under Settings lists the `language_packs` rows (code,
  name in English and Arabic, version, enabled, source, size, installed at). Admins may enable or
  disable a pack. Install and update go through Epic 51's components page, linked from here.
  Disabling a pack makes its words fall to the fallback pack at the next check. Routes:
  `GET /api/language-packs`, `PATCH /api/language-packs/{code}` (admin).

### 3. Throughput guard

- `word_check` runs in the `cpu` pool at a priority below `transcribe` and `subtitle_gen`.
- `word_judge` runs in the `llm` pool **only while `transcribe` has no pending or running jobs**.
  Its claim query checks that, so it waits instead of competing for the GPU. A job that finds the
  GPU busy goes back (`JobDeferred`, no attempt used).
- Both respect `wait_for_memory`'s floor, like transcribe.

### 4. Queue health (Epic 40)

- `word_check` and `word_judge` get queue tiles like every stage: pending, running, failed, and
  failure classes (`pack_missing`, `pack_failed`, `llm_unavailable`, `transcript_gone`).
- `word_judge` waiting on the idle rule shows as **waiting (transcription busy)**, not red.
- The Transcription page (`web/src/pages/transcription/Transcription.tsx`) shows open flags per
  library and language, flags this week, fixes this week, the flag rate (per 1,000 words), and the
  share of flags resolved as "accepted as is". That share is the live precision signal that tells
  the owner whether the sensitivity is right.
- Strings for all of this are in English and Arabic (`wordReview.settings.*`,
  `queue.stage.word_check`, `queue.stage.word_judge`, `transcription.flags.*`).

## Acceptance criteria

1. **Given** an admin, **when** they set sensitivity to `low` on a library, **then** the setting is
   saved, `word_check` is re-queued for that library's transcripts only, and their new flags use
   the low thresholds.
2. **Given** a non-admin reviewer, **when** they call `PUT /api/libraries/{id}/word-review`,
   **then** 403. A viewer gets 403 on GET too. With no session, 401.
3. **Given** `budget_per_hour = 300` or an unknown reason, **when** it is saved, **then** 400 with
   the field named.
4. **Given** `llm_judge.model` that no runtime has, **when** it is saved, **then** 400
   `model_not_found` with the models that exist.
5. **Given** `backfill` turned on, **then** the admin sees the hours and the CPU-time estimate
   before anything is enqueued. After confirming, the jobs are at the lowest priority.
6. **Given** `transcribe` has pending jobs, **when** `word_judge` tries to claim, **then** it does
   not run, and its tile says "waiting (transcription busy)".
7. **Given** `reasons.not_in_dictionary = false`, **when** `word_check` runs, **then** no flag has
   only that reason.
8. **Given** the Arabic pack is disabled, **when** an Arabic transcript is checked, **then** the
   fallback pack is used, `word_flags.pack` says so, and no `not_in_dictionary` flags are made from
   a missing dictionary.
9. **Given** the Transcription page, **then** it shows open flags, the flag rate and the
   "accepted as is" share per library, in English and Arabic.
10. **Given** a `word_check` failure, **then** its tile turns red with the failure class, as in
    Epic 40.

## Build plan

### Files

- `pipeline/src/maktaba_pipeline/word_review/settings.py`: parse and validate `word_review`, with the
  defaults.
- `pipeline/src/maktaba_pipeline/word_review/scoring.py`: the sensitivity → threshold table.
- `pipeline/src/maktaba_pipeline/engine/stages/word_check.py`: the setting in the fingerprint,
  and the reasons switch.
- `pipeline/src/maktaba_pipeline/engine/stages/word_judge.py`: the idle-only claim rule.
- `pipeline/src/maktaba_pipeline/engine/graph.py`: stage priorities and pools.
- `api/internal/handlers/libraries/wordreview.go`: GET and PUT.
- `api/internal/handlers/languagepacks/languagepacks.go`: the list and enable/disable routes.
  Both are mounted in `router/p6.go`.
- `web/src/pages/libraries/WordReviewSettings.tsx`, `web/src/pages/settings/LanguagePacks.tsx`.
- `web/src/pages/transcription/Transcription.tsx`: flag counts.
- `web/src/components/queue/QueueCard.tsx`: the waiting label.
- `web/src/i18n/en.json`, `web/src/i18n/ar.json`.

### Tests

- `test_review_settings.py`: defaults, ranges, unknown keys ignored with a warning, and the
  sensitivity mapping.
- `test_word_check.py`: a setting change changes the fingerprint for that library only; and the
  reasons switch.
- `test_word_judge.py`: the claim is refused while transcribe has pending work; and a deferral
  uses no attempt.
- `wordreview_test.go` and `languagepacks_test.go`: roles in the `TestAdminOnly` style,
  validation errors, and model lookup against a fake `llm.models`.
- Web tests: the settings form, the backfill confirm with the estimate, the waiting tile label,
  and the Arabic strings.

### Rollout

Phase 2. The defaults are on for new transcripts and off for backfill (README R2). The owner turns
backfill on per library after answering open question 2.

## Edge cases

- **A library with no `word_review` key:** the defaults apply, and nothing is written until an
  admin saves.
- **The owner lowers the budget:** existing flags over the new budget are marked `skipped`
  (`over_budget`) at the next check, never deleted.
- **A multi-language library:** sensitivity and budget are per library, and the thresholds are per
  language inside the scoring table.
- **The judge's model disappears from the cluster:** `word_judge` jobs fail with
  `llm_unavailable`, and the tile shows it. `word_check` flags are unaffected.
