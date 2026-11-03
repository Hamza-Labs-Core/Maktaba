# Story 52.14 — Settings, thresholds, budget and queue health

> Epic 52 · Phase 2 · Depends on: 52.6, 52.16 · Needed by: 52.11
> Epic 51: `review.words` owns `rules.word_review` (51.13); its health is 51.14's.

## Why

How many words a person is asked to check is a trade-off. Too many and the queue is ignored; too
few and mistakes stay. That trade-off is the admin's, per library: a Quran library, a
lecture library and an English documentary library want different settings. The new stages also
have to show their health like every other queue (Epic 40), and must not slow transcription on the
home Mac.

## Description

### 1. The home default `rules.word_review` and the library setting `word_review`

Two layers, with the same keys:

- **Home defaults**: the rules section `rules.word_review`, a config class in
  `pipeline/src/maktaba_pipeline/word_review/settings.py` registered in `rules.py` `SECTIONS`.
  It is the section `review.words` owns in the catalog (`settings = ["rules.word_review"]`), so
  Epic 51 story 51.13 applies as built: it declares `SCHEMA_VERSION = (1, 0)`
  (`pipeline/src/maktaba_pipeline/settings.py`), a field removed or retyped later needs a major
  bump and a migration registered with `settings.register_migration`, and the stored version sits
  in `rules_version.word_review`. It is edited with `PATCH /api/rules/word_review` (validated
  against `api/internal/handlers/rules/schema.json`, version checked). Disabling, stopping or
  removing `review.words` keeps it; `POST /api/components/review.words/settings/reset` (or
  `DELETE /api/components/review.words?purge=true`) deletes it after asking, with Epic 51's
  `settings.reset` audit row. Neither touches flags, fixes, vocabulary or the per-library values.
- **Per library**: the library's settings JSON key `word_review` (next to `stt`), read through
  `library_setting` by the stage. A key the library does not set takes the home default.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `enabled` | bool | `true` | `word_check` runs on this library's new transcripts |
| `backfill` | bool | `true` | Also check this library's existing transcripts (text-only signals) in the background at the lowest priority (README §8, decision 2) |
| `sensitivity` | `low` / `normal` / `high` | `normal` | Moves every reason's threshold together: low = fewer, surer flags |
| `budget_per_hour` | int 10-200 | `40` | The most flags per hour of audio (README §4.6) |
| `reasons` | map reason → bool | all `true` | Turn a reason off (for example `not_in_dictionary` on a dialect library, or `quran_mismatch` to protect verse runs unchecked, 52.15) |
| `reviewers_note` | string | `""` | A note shown at the top of the queue ("spell names as on the book covers") |

There are no LLM settings: the `llm_judge.*` keys of the first draft are removed with story 52.7
(README §8, decision 3). A stored `llm_judge` key from a test build is ignored with a warning.

The sensitivity steps map to calibrated error thresholds per reason. Normal is 0.5, low 0.65,
high 0.35. They live in the pack-independent scoring table in `review/scoring.py`.

`word_check`'s input fingerprint includes this setting's hash. Changing it re-checks only that
library's transcripts, at low priority. It never touches `transcribe`.

### 2. API and page

- `GET /api/libraries/{id}/word-review`, `PUT /api/libraries/{id}/word-review`: **admin only**,
  checked inline like `transcription.go:304`. The PUT validates the ranges and the reason names.
- Page: a **Word review** section in the library's settings (Manage libraries, Epic 45) with the
  fields above, plain explanations, and an estimate line, e.g. "about 1,200 flags for this
  library's 30 hours at this setting". The estimate comes from the current flags' priority
  distribution.
- **Backfill control:** the backfill of all existing transcripts is on by default (README §8,
  decision 2) and runs in the background at the lowest priority, only while `transcribe` is idle
  (52.6 §7). The section shows its progress for the library (transcripts checked of total, audio
  hours, CPU-time left at ≤ 10 CPU-s per audio hour). Turning `backfill` off cancels the
  library's pending backfill jobs; turning it back on enqueues the unchecked ones again. The whole
  stage can be paused like any queue stage (`POST /api/queues/word_check/pause`).
- **Language packs (home-wide):** a page under Settings lists, for each language heard in the
  home's transcripts, its pack (from the `language_packs` report, 52.3: code, name in English and
  Arabic, data version, state) next to its Epic 51 component (`lang.ar`, `lang.en`,
  `lang.hunspell.<code>`: phase, reason and `install_size_mb` from `GET /api/components`), or
  "fallback in use". The switches are Epic 51's, called from this page: **Install** /
  «تثبيت» is `POST /api/components/lang.hunspell.<code>/install` (with its `progress` shown as
  `components.progress.installing`), **Remove** is `DELETE /api/components/<id>` (an asset is on
  disk or not; refused with
  `409 component_required_by` while something enabled requires it). There is no second switch in
  this epic. A removed pack's language falls to the fallback at the next check. Route of this epic:
  `GET /api/language-packs` (admin, read only); the Components page (group `languages`) shows the
  same assets.

### 3. Throughput guard

- `word_check` runs in the `cpu` pool at a priority below `transcribe` and `subtitle_gen`.
- **Backfill** `word_check` jobs run at the lowest priority and **only while `transcribe` has no
  pending or running jobs**. Their claim query checks that; a job that finds transcription busy
  goes back (`JobDeferred`, no attempt used).
- It respects `wait_for_memory`'s floor, like transcribe. No stage of this epic uses the GPU or
  the `llm` pool.

### 4. Queue health (Epic 40)

- `word_check` gets a queue tile like every stage: pending, running, failed, and failure classes
  (`pack_unavailable`, `pack_failed`, `transcript_gone`; not Epic 51's `pack_missing` reason,
  which is about installing a pack), plus the backfill's progress (checked of ~45k).
- **Off is not red.** While `review.words` is off or held, the tile is Epic 51's: `state = off`,
  and its waiting jobs carry `component = review.words` and `phase = off | stopped` (51.14), shown
  as "waiting: word review is off". The component's own health (phase, `since`, `restarts`,
  `last_error_code`) is the `health` block of `GET /api/components/review.words`, and a failed or
  degraded `review.words` raises the system health item of kind `components` like any other.
- Backfill jobs waiting on the idle rule show as **waiting (transcription busy)** / «بانتظار
  انتهاء التفريغ», not red.
- The Transcription page (`web/src/pages/transcription/Transcription.tsx`) shows open flags per
  library and language, flags this week, fixes this week, the flag rate (per 1,000 words), and the
  share of flags resolved as "accepted as is". That share is the live precision signal that tells
  an admin whether the sensitivity is right.
- Strings for all of this are in English and Arabic (`wordReview.settings.*`,
  `queue.stage.word_check`, `queue.backfill.*`, `transcription.flags.*`).

## Acceptance criteria

1. **Given** an admin, **when** they set sensitivity to `low` on a library, **then** the setting is
   saved, `word_check` is re-queued for that library's transcripts only, and their new flags use
   the low thresholds.
2. **Given** a user with `write` or the library `admin` role (not a home admin), **when** they
   call `GET` or `PUT /api/libraries/{id}/word-review`, **then** 403. A viewer gets 403 too. With
   no session, 401.
3. **Given** `budget_per_hour = 300` or an unknown reason, **when** it is saved, **then** 400 with
   the field named.
4. **Given** a library never saved, **then** `backfill` reads `true`, and its existing transcripts
   are in the backfill at the lowest priority.
5. **Given** `backfill` turned off for a library, **then** its pending backfill jobs are cancelled;
   turned on again, only its unchecked transcripts are enqueued.
6. **Given** `transcribe` has pending jobs, **when** a backfill `word_check` job tries to claim,
   **then** it does not run, and the tile says "waiting (transcription busy)" in English and
   Arabic.
7. **Given** `reasons.not_in_dictionary = false`, **when** `word_check` runs, **then** no flag has
   only that reason.
8. **Given** `lang.ar` removed (`DELETE /api/components/lang.ar`), **when** an Arabic transcript is checked, **then** the
   fallback pack is used, `word_flags.pack` says so, and no `not_in_dictionary` flags are made from
   a missing dictionary.
9. **Given** the Transcription page, **then** it shows open flags, the flag rate and the
   "accepted as is" share per library, in English and Arabic.
10. **Given** a `word_check` failure, **then** its tile turns red with the failure class, as in
    Epic 40.
11. **Given** `review.words` disabled, **then** the tile shows "waiting: word review is off" (not
    red), and `GET /api/components/review.words` shows phase `off`.
12. **Given** `POST /api/components/review.words/settings/reset` by an admin, **then**
    `rules.word_review` and `rules_version.word_review` are deleted with a `settings.reset` audit
    row, the per-library values are kept, and a viewer gets 403.

## Build plan

### Files

- `pipeline/src/maktaba_pipeline/word_review/settings.py`: parse and validate `word_review`, with the
  defaults.
- `pipeline/src/maktaba_pipeline/word_review/scoring.py`: the sensitivity → threshold table.
- `pipeline/src/maktaba_pipeline/engine/stages/word_check.py`: the setting in the fingerprint,
  and the reasons switch.
- `pipeline/src/maktaba_pipeline/word_review/backfill.py` (52.6): the idle-only claim rule for
  backfill jobs and the per-library cancel.
- `pipeline/src/maktaba_pipeline/engine/graph.py`: stage priorities and pools.
- `api/internal/handlers/libraries/wordreview.go`: GET and PUT.
- `api/internal/handlers/languagepacks/languagepacks.go`: the read-only list, joined with the
  component service (`api/internal/components` `Service.List`). Both routes are mounted in
  `router/p6.go`.
- `pipeline/src/maktaba_pipeline/rules.py`: `word_review` in `SECTIONS`; `schema.json`
  regenerated (`test_the_api_schema_is_current`).
- `web/src/pages/libraries/WordReviewSettings.tsx`, `web/src/pages/settings/LanguagePacks.tsx`.
- `web/src/pages/transcription/Transcription.tsx`: flag counts.
- `web/src/components/queue/QueueCard.tsx`: the waiting label.
- `web/src/i18n/en.json`, `web/src/i18n/ar.json`.

### Tests

- `test_review_settings.py`: defaults, ranges, unknown keys ignored with a warning, and the
  sensitivity mapping.
- `test_word_check.py`: a setting change changes the fingerprint for that library only; and the
  reasons switch.
- `test_backfill.py`: the claim is refused while transcribe has pending work; a deferral uses no
  attempt; turning `backfill` off cancels pending jobs.
- `test_rules_word_review.py`: `SCHEMA_VERSION`, the section in `schema.json`, the per-library
  key overriding the home default.
- `wordreview_test.go` and `languagepacks_test.go`: roles in the `TestAdminOnly` style (nobody,
  viewer, `write` on the library, library `admin` role without home admin → 401/403; home admin →
  200), and validation errors; a stored `llm_judge` key is ignored.
- Web tests: the settings form, the backfill progress, the waiting tile label, the "install the
  Hunspell pack" link, and the Arabic strings.

### Rollout

Phase 2. Both defaults are on: new transcripts are checked, and the backfill of all existing ones
runs in idle time (README §8, decision 2).

## Edge cases

- **A library with no `word_review` key:** the defaults apply, and nothing is written until an
  admin saves.
- **An admin lowers the budget:** existing flags over the new budget are marked `skipped`
  (`over_budget`) at the next check, never deleted.
- **A multi-language library:** sensitivity and budget are per library, and the thresholds are per
  language inside the scoring table.
- **Transcription never idles for days** (a big import): the backfill waits; new transcripts are
  still checked at once. The tile shows the wait, not an error.
