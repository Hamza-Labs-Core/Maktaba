# Story 52.10 — Vocabulary: learning from fixes

> Epic 52 · Phase 3 · Depends on: 52.9 · Needed by: 52.7 (its glossary)

## Why

A lecturer's name, a book's title or a place mentioned in 300 lectures will be flagged 300 times
unless the system learns it. The owner wants each fix to count. This story turns fixes into a
vocabulary. The vocabulary stops re-flagging, finds the same word elsewhere, and helps the second
listen, the LLM judge and new transcriptions spell it right. It never re-transcribes the library
by itself.

## Description

### 1. Tables (slot 0173+)

- `vocabulary (id, scope 'home'|'library', library_id NULL, language, term, key, kind
  'word'|'name'|'term', variants TEXT[], source 'review'|'manual'|'import', uses INT, added_by,
  created_at)`. Unique on `(scope, library_id, language, key, term)`.
- `vocab_pairs (id, library_id, language, heard, heard_key, meant, uses, last_edit_id)`: "Whisper
  heard X, the person meant Y". This is the misheard list.

### 2. What writes it

| Review action | Writes |
|---|---|
| `name` | `vocabulary` kind `name`, scope library (an admin may choose home) |
| `dictionary` | `vocabulary` kind `word` (or `term`), scope library |
| `correct` | `vocab_pairs (heard=original, meant=resolution)`, `uses + 1`. After 3 uses, the corrected spelling is added to `vocabulary` as kind `word` |
| Vocabulary page | Manual add, edit, delete, import (CSV: `term, kind, language, variants`) |

### 3. What reads it

- **The packs' `known()`:** `word_check` passes the library's and home's vocabulary to the pack
  (a frozen set of keys per run). A key in the vocabulary is `KNOWN`, and `near_vocabulary` fires
  for one edit away (Damerau-Levenshtein on keys, length ≥ 4).
- **Candidates:** a `vocab_pairs` hit on `heard_key` puts `meant` first, source `vocabulary`.
- **Re-scoring:** a new vocabulary entry or pair enqueues `word_check` only for transcripts with
  an open flag of the same `key` and `language` (index `(key, language, status)`). It never
  enqueues the whole library. Those flags then resolve themselves as `accepted` (source
  `vocabulary`) when the word is now known, or gain the candidate.
- **The second listen's speller** (`stt/refine.py` `Speller`): vocabulary spellings come before
  the file's own and the dictionary's. Only hamza, «ة» and «ى» forms change, as today.
- **The LLM judge** (52.7): the library's names and terms as a glossary, at most 150 entries by
  `uses`.
- **New transcriptions:** a *derived prompt*, made of the library's `stt.initial_prompt` (as the
  owner wrote it) plus the top vocabulary names and terms by `uses`. It is cut to Whisper's
  224-token prompt limit with the model's tokenizer. The owner's text always comes first and is
  never cut. On CTranslate2, the same terms go as `hotwords`.
- **Fingerprint rule:** `input_fingerprint` in `engine/stages/transcribe.py` keeps only the
  owner-written prompt. The derived part is left out on purpose, so learning a word never queues
  the library for transcription again. A CI test proves it.

### 4. Same word elsewhere

52.8's `apply_to_same_key` is the bulk fix. This story adds the count to the queue item ("37
other places"), and a preview of the first 5 sentences before the bulk action.

### 5. Transcribe again with the vocabulary

A per-video and per-library action (admins): "Transcribe again with the vocabulary". It shows the
audio hours and an estimate of the GPU time (from the home's measured realtime factor) before it
asks. It enqueues `transcribe` with a `force` reason. Edits carry over (52.9).

### 6. Export

`GET /api/vocabulary/export` (CSV). `GET /api/review/corrections/export?library=` gives a CSV
(video, track, start, end, heard, meant, language, user, time) plus a clip list (signed URLs that
expire in 24 h). It is for anyone who later wants to fine-tune a model. Nothing is trained here.

### 7. Management page

`web/src/pages/review/Vocabulary.tsx`: search, filter by kind, language and library, edit
variants, delete, import and export. Admins see every library. A `write` reviewer sees and edits
their libraries' entries only, and never home scope.

## Acceptance criteria

1. **Given** a flag on a lecturer's name, **when** a reviewer marks it `name`, **then** a
   `vocabulary` row (kind `name`, the library's scope) exists, and the next `word_check` of any
   transcript in that library does not flag that word as `not_in_dictionary`.
2. **Given** 12 open flags with the same key in other files, **when** the word is added to the
   dictionary, **then** `word_check` is enqueued for those transcripts only, and the 12 flags
   become `accepted` with source `vocabulary`.
3. **Given** the same correction made 3 times, **when** the third is saved, **then** the corrected
   spelling is in `vocabulary`, and new flags on the heard spelling list it as the first candidate.
4. **Given** a library with vocabulary, **when** its vocabulary changes, **then** the
   `transcribe` input fingerprint of every video in it is unchanged (CI test), and no transcribe
   job is enqueued.
5. **Given** a library prompt and 400 vocabulary names, **when** a new transcription starts,
   **then** the prompt Whisper gets starts with the owner's text, holds names by `uses`, and is at
   most 224 tokens.
6. **Given** a vocabulary spelling «ابن تيمية», **when** the second listen hears «ابن تيميه»,
   **then** the merged transcript spells it «ابن تيمية».
7. **Given** an admin, **when** they choose "Transcribe again with the vocabulary" on a library,
   **then** they first see the audio hours and the GPU-time estimate, and nothing is enqueued
   until they confirm.
8. **Given** a `write` reviewer of library A, **when** they add to, edit or delete vocabulary of
   library B or home scope, **then** 403.
9. **Given** an import CSV with a bad row, **when** it is uploaded, **then** the good rows are
   added and the answer lists the bad rows with reasons.
10. **Given** an export of corrections, **then** each row's clip URL plays the right span and
    expires after 24 h.

## Build plan

### Files

- `shared/db/migrations/0173+_vocabulary.sql` and `.sqlite.sql`.
- `api/internal/handlers/vocabulary/vocabulary.go`: CRUD, import and export. Mounted in
  `router/p6.go`, using `review.canReview` for library scope and `p.IsAdmin` for home scope.
- `api/internal/handlers/review/review.go`: the vocabulary writes on `name`, `dictionary` and
  `correct`, plus the re-score enqueue.
- `pipeline/src/maktaba_pipeline/review/vocabulary.py`: loads a library's vocabulary and pairs
  for a run, and the derived prompt (`derived_prompt(db, library_id, tokenizer)`).
- `pipeline/src/maktaba_pipeline/engine/stages/word_check.py`: passes the vocabulary to the pack,
  plus `near_vocabulary`.
- `pipeline/src/maktaba_pipeline/stt/refine.py`: `Speller` takes vocabulary spellings first.
- `pipeline/src/maktaba_pipeline/engine/stages/transcribe.py`: uses the derived prompt at decode
  time. `input_fingerprint` stays on the owner's prompt.
- `pipeline/src/maktaba_pipeline/stt/ct2.py`: `hotwords` from the same list.
- `web/src/pages/review/Vocabulary.tsx`, `web/src/lib/reviewApi.ts`, and strings in
  `web/src/i18n/en.json` + `ar.json` (`vocabulary.*`).

### Tests

- `test_transcribe.py::test_vocabulary_does_not_change_fingerprint`: add 50 terms and assert that
  the fingerprint is equal and no job is enqueued.
- `test_vocabulary.py`: derived prompt ordering, the 224-token cut with the real tokenizer
  fixture, owner text never cut, and the home + library merge.
- `test_refine.py`: vocabulary spelling wins over the file's own spelling, and only within the same
  folded letters.
- `test_word_check.py`: a vocabulary key is `KNOWN`, `near_vocabulary` fires at distance 1 and
  not at 2, and re-scoring touches only same-key transcripts.
- `vocabulary_test.go`: roles (nobody, viewer, other library's reviewer, home scope by a
  reviewer → 401/403), import with bad rows, and the export CSV's columns.
- Evaluation (52.2): the gold set is run twice, without and then with a vocabulary built from half
  its corrections. Flags on the other half's repeated names must drop by ≥ 50%, and precision must
  not fall.

### Rollout

Phase 3. The derived prompt starts with the next transcription after release. Nothing is
re-transcribed. The re-score of same-key flags runs at `word_check`'s low priority.

## Edge cases

- **A wrong term added by mistake:** deleting it re-queues `word_check` for transcripts with
  accepted-by-vocabulary flags of that key, so they open again.
- **The same term in two scripts or languages:** entries are per language, and the key is the
  pack's key for that language.
- **Prompt hallucination risk:** Whisper can echo prompt words over silence. The derived part
  holds names only (no sentences), at most 224 tokens. `word_check` lowers confidence on a
  prompt-echoed word in a no-speech stretch (`no_speech > 0.6`).
- **Very large vocabularies:** the `known()` set is loaded once per job. Beyond 100 k entries a
  library still loads in < 1 s (one indexed query).
