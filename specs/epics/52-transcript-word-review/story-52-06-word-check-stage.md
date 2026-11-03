# Story 52.06 — `word_check`: flags, names and old transcripts

> Epic 52 · Phase 1 · Depends on: 52.1, 52.2, 52.3, 52.4, 52.5, 52.16, 52.17 · Needed by: 52.8, 52.14, 52.15
> Epic 51 component: `review.words` (kind `stages`, owns `word_check`).

## Why

This is the queue job the epic is for: it reviews each transcript and saves the place of every
word that is not in the dictionary, looks wrong or has low confidence, for a person. It turns 52.1's evidence and the packs' knowledge into
`word_flags` rows: where the word is, why it is suspect, how sure we are, and what it might be.
It runs on the CPU, so it can run again whenever the packs, the vocabulary or the thresholds
change, without the GPU.

## Description

### 1. The stage

- Name `word_check`, pool `cpu`, one job per video, in `engine/graph.py`. Its one owner in
  `shared/components/catalog.toml` is `review.words` (52.16); the `catalog` CI job fails while a
  `StageDef` has no owner.
- **On and off are Epic 51's** (51.06): while `review.words` is not enabled, or is held, the
  pipeline runner drops `word_check` from the served set (`stages_off = component_off:review.words`
  or `component_held:review.words`), drains for `drain.grace_s` (30 s; a job is one transcript and
  has no checkpoint), and force-pauses what is left. Upstream edges record
  `skipped_component_off` instead of enqueueing while it is off; a hold lets jobs queue and wait.
  When it comes back, `component_catchup` enqueues every transcript stored while it was off. The
  per-library `word_review.enabled` (52.14) only narrows inside an enabled component.
- Edge `transcribe → word_check` (`same`). It also runs when the vocabulary or a pack changes
  (52.10 queues the affected transcripts), and when the library's `word_review` settings change.
- **Input fingerprint**: active transcript id, `transcripts.revision` (52.9; 0 until then),
  `registry.versions()` of the packs this transcript's scripts use, the library's vocabulary
  version (52.10; 0 until then), the library's thresholds and budget (52.14), and
  `WORD_CHECK_VERSION`. Same input, no work.
- Skips when `word_review.enabled` is off for the library, or the video has no active transcript.

### 2. Per sentence

1. `word_data` → Whisper words, or none (old transcript).
2. Script runs → pack tokens (52.3). Map each token to the Whisper words that overlap it in code
   points: its `prob` is the lowest of them, its flags the OR of theirs.
3. The pack's `scripture()` runs first (Arabic: the Quran check, 52.15). Words of a verse run
   that equal the verse are protected; words that differ get `quran_mismatch` and skip the
   ordinary signals below.
4. Signals for each other word token (numbers, punctuation and protected tokens are skipped):

| Signal | Source | Error probability |
|---|---|---|
| `low_confidence` | `prob` through the language's calibration curve (52.2) | `p_cal` |
| `listeners_disagree` | `word_data` bit 0/1, `metadata.alt` | 0.6 by default, fitted on the gold set |
| `not_in_dictionary` | pack `known()`: `UNKNOWN` 0.5, `UNSURE` 0.1 | as listed, fitted |
| `named_entity` | gazetteer match (§3) | multiplies the total by 0.5 |
| `near_vocabulary` | one edit from a vocabulary term (52.10) | 0.5, and the term is a candidate |
| `quran_mismatch` | 52.15's alignment against the matched verse | 0.8, fitted; the verse's word is a candidate |

5. `priority = 1 − Π(1 − p_i)`; × 0.5 for a name match. A word is flagged when `priority` ≥ the
   library's threshold (default 0.5, moved by sensitivity, 52.14).
6. Adjacent flagged words in one sentence merge into one span (at most 4 words).

Every signal is deterministic (README §8, decision 3: no LLM judge). Running the stage twice on
the same input writes byte-identical flags.

### 3. Names (the gazetteer)

Built once per library per job run, cached by the library's metadata version:

- library and series titles, video titles, speakers (`speakers` table), channel names;
- TMDB cast and character names and YouTube channel names, from what enrich already stored
  (`enrich/cache.py`, the video's enrichment rows);
- vocabulary entries of kind `name` (52.10).

Each name is split into tokens with the pack and stored by `key`. A token matching a name
(exactly by key, or one edit away for names of 6+ letters) is a `named_entity`; the gazetteer's
spelling is a candidate. An English capitalised `UNSURE` word with no match stays `UNSURE`. An
optional NER model is **not** in this story (README research: metadata is the better gazetteer
for this library); a pack may add one later through `known()`'s analysis.

### 4. Candidates

Up to 5, best first, deduplicated by `key` (but different spellings of one key are kept, since
a spelling fix is the point): second listen's word (`metadata.alt`), Whisper's replaced word,
gazetteer spelling, vocabulary term, pack `suggest()`; for a `quran_mismatch`, the verse's word
first (source `quran`). Each with its source and a score.

### 5. Budget and the poor-transcript flag

- Flags are ranked by priority. The top `budget × audio_hours` (default 40 per hour, at least 5)
  are `open`. The rest are stored `skipped` with `scores.over_budget = true`, so a later re-check
  with a bigger budget, or 52.10's re-scoring, can open them without recomputing.
- When more than 25% of a file's would-be flags are over budget, or more than 8% of its words
  are suspect, one extra flag of kind `transcript` (`segment_seq = -1`) says "this transcript may
  be poor: N suspect words", with the reasons' counts. Reviewers can open the file, mark it fine,
  or ask for it to be transcribed again (52.14's action).

### 6. Writing

In one transaction: delete the transcript's `open` flags that this run does not produce again;
upsert the rest on `(transcript_id, segment_seq, char_start)`; never touch flags that are
resolved (`accepted`, `corrected`, `name`, `dictionary`). A resolved word is not flagged again in
the same transcript revision.

### 7. Old transcripts (text only) and the backfill of all of them

A sentence without `word_data` gets only `not_in_dictionary`, `named_entity`, `near_vocabulary`
and `quran_mismatch` (52.15 finds verse runs from the text). Its words' times are **estimated** by
code-point position between the sentence's `start_sec` and `end_sec`, and the flag has
`scores.time_estimated = true`, so the clip (52.8) pads more. The text-only threshold is raised by
0.1 (one signal is weaker evidence).

Decision 2 (README §8) is to text-check **all ~45k existing transcripts**:

- When the stage first ships (and again whenever a library is added with old transcripts), a
  one-time **backfill planner** enqueues `word_check` for every active transcript that has never
  been checked, at the **lowest priority** of any stage, newest videos first.
- The backfill claim is refused while `transcribe` has pending or running jobs (`JobDeferred`, no
  attempt used), so it only runs in idle time and never delays transcription or `subtitle_gen`.
  It also respects `wait_for_memory`.
- `word_review.backfill` (52.14) defaults to **on**. An admin may turn it off for a library; its
  pending backfill jobs are then cancelled, and new transcripts are still checked.
- Progress (checked of total, estimated CPU-hours left) shows on the `word_check` queue tile.
- Backfill is resumable: the fingerprint makes a checked transcript `unchanged`, so a restart or
  a planner run again costs nothing for work already done.

### 8. Migration

`word_flags` as in README §4.3, plus `kind TEXT NOT NULL DEFAULT 'word'` (`word` |
`transcript`). Indexes `(library_id, status, priority DESC)`, `(key, language, status)`,
`(transcript_id)`.

## Acceptance criteria

1. **Given** a new transcript with `word_data`, **when** it is stored, **then** a `word_check` job
   is queued and finishes, and flags appear within 2 minutes on an idle queue.
2. **Given** a word with calibrated error 0.7, **when** checked, **then** it is flagged with
   reason `low_confidence`, `scores.prob` and `scores.prob_cal` set, and its `start_sec`/`end_sec`
   from `word_data`.
3. **Given** a word the second listen replaced, **when** checked, **then** reasons include
   `listeners_disagree`, and both Whisper's and Cohere's texts are candidates.
4. **Given** a word that matches a speaker's name in the library, **when** checked, **then**
   `named_entity` is a reason, its priority is halved, and the speaker's spelling is a candidate.
5. **Given** an English word inside an Arabic sentence, **when** checked, **then** the flag's
   `language` is `en` and the English pack decided it.
6. **Given** a correctly heard Quran run, **when** checked, **then** none of its words is flagged;
   **given** one misheard word in it, **then** only that word is flagged, with reason
   `quran_mismatch` alone (52.15).
7. **Given** a file with 300 suspect words in 1 audio hour and a budget of 40, **when** checked,
   **then** 40 flags are `open`, 260 `skipped` with `over_budget`, and one `transcript` flag exists.
8. **Given** a resolved flag, **when** the same transcript is checked again, **then** the flag is
   unchanged and no new flag appears at that word.
9. **Given** an old transcript without `word_data`, **when** checked, **then** only text signals
   appear, times are estimated with `time_estimated`, and no `low_confidence` flag exists.
10. **Given** the same input, **when** the job runs twice, **then** the second run is
    `unchanged` and writes nothing.
11. **Given** a pack version bump, **when** the engine re-plans, **then** only transcripts in that
    pack's language are queued again.
12. **Given** the gold set, **when** `word_check` runs on it, **then** Arabic precision ≥ 0.50 and
    recall ≥ 0.60, English ≥ 0.60 / 0.60, and ≥ 18 of the 20 hearing-test spots are flagged.
13. **Given** the stage first deployed on a home with existing transcripts, **then** the backfill
    planner enqueues every unchecked active transcript at the lowest priority, and **given**
    `transcribe` has pending jobs, **then** no backfill job runs until they finish.
14. **Given** a library with `backfill = false`, **then** none of its old transcripts is
    enqueued, and its pending backfill jobs are cancelled.
15. **Given** `review.words` disabled through `PUT /api/components/review.words`, **when** a
    transcript is stored, **then** no `word_check` job is created (`skipped_component_off`), the
    queued ones stay queued, and enabling it again runs `component_catchup` for the missed
    transcripts; a hold (`POST …/stop`) instead lets new jobs queue and wait.

## Build plan

### Files

- `pipeline/src/maktaba_pipeline/engine/stages/word_check.py` (new): the stage.
- `pipeline/src/maktaba_pipeline/word_review/signals.py`, `scoring.py`, `gazetteer.py`,
  `candidates.py`, `store.py` (new): pure functions plus the writer.
- `pipeline/src/maktaba_pipeline/engine/graph.py`: the stage, the edge, `WORD_CHECK_VERSION`.
- `pipeline/src/maktaba_pipeline/word_review/backfill.py` (new): the planner and the idle-only
  claim rule for backfill jobs.
- `pipeline/src/maktaba_pipeline/engine/stages/registry.py`: register it.
- `shared/db/migrations/` next free slot, 0182 (0181 is the highest once Epic 51 merges):
  `word_flags`, Postgres and SQLite twins.
- `shared/components/catalog.toml` (and the pipeline copy, `make catalog-sync`): `word_check` in
  `review.words`'s `stages` (the entry itself is 52.16).
- `api/internal/handlers/review/` gets a read-only admin list in this story (full API in 52.8),
  so phase 1 flags can be looked at.

### Tests

- Unit: scoring (combination, name halving, thresholds, sensitivity), span merging, budget,
  poor-transcript rule, token↔word mapping on mixed scripts, time estimation.
- Stage tests on SQLite and scratch Postgres: fingerprint, upsert keeps resolved flags, delete of
  stale open flags, `unchanged` second run (byte-identical flags).
- Backfill: the planner enqueues only unchecked transcripts at the lowest priority; the claim is
  deferred without using an attempt while `transcribe` has work; `backfill = false` cancels.
- Gazetteer: names from fixtures of `speakers`, titles and enrich rows.
- Eval: `eval --set ci` through the real `word_check` scoring, gated by 52.2's baseline.
- Performance: a 9,000-word hour checks in ≤ 10 CPU-seconds on the home Mac (warm pack caches),
  recorded in the PR. A CI test asserts ≤ 30 s on the CI runner.
- conftest guard: no model loads. Packs use fixture data.

### Rollout

- `word_review.enabled` and `word_review.backfill` both default to on. New transcripts are checked
  at once; the backfill of all ~45k existing transcripts starts in idle time behind
  `transcribe`.
- Live check after a day (read-only): flag rate ≤ 3% of words, open flags per audio hour ≤ the
  budget. If over, lower sensitivity before 52.11 opens the queue.
- Back out: `maktaba-server components disable review.words` (or `PUT /api/components/review.words`
  `{"want":"installed"}`; queued jobs are kept, not lost), or revert. Flags are only rows.
  Deleting them is safe.

## Edge cases

- A transcript replaced while its job runs: the writer checks the transcript is still active in
  the transaction, else exits `unchanged`.
- A sentence edited by a fix (52.9) since the flag: revision changed, so the job re-checks. Open
  flags whose text no longer matches become `stale`.
- Very short files (under 1 minute): the budget floor of 5 applies.
- A library with no metadata: empty gazetteer, no name signal.
- Hallucinated sentences over silence (high `no_speech_prob`): one `transcript`-kind flag per run
  of them, not one flag per word.
