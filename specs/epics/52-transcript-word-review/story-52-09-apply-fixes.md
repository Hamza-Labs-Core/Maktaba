# Story 52.09 — Applying fixes: edits, undo, subtitles, search, carry-over

> Epic 52 · Phase 2 · Depends on: 52.1, 52.8 · Needed by: 52.10, 52.11, 52.12, 52.13

## Why

A reviewer's fix has to land everywhere the word is shown: the transcript tab, search, and the
`.srt`/`.vtt` files next to the video that Plex, VLC and the TVs read. It has to do that without
running speech recognition again. It also has to survive the day the file is transcribed again,
and be undone when someone was wrong.

## Description

### 1. The edit (Go, one transaction)

`transcript.ApplyEdit(tx, transcriptID, segSeq, charStart, charLen, after, flagID, userID,
batchID)`:

1. Lock the sentence row (`SELECT … FOR UPDATE`). Check that the span still reads the flag's
   `original`. If not, mark the flag `stale` and return 409.
2. Replace the span in `transcript_segments.text`. Offsets are **Unicode code points**: Go
   indexes runes, never bytes. The web converts from UTF-16.
3. Shift `word_data` (52.1's packed format, decoded by `api/internal/transcript/worddata`):
   - The edited word(s) become one word with the new length.
   - The words after it move by `len(after) − charLen`.
   - Their times and probabilities stay. The edited word keeps the first word's start and the
     last word's end. Its probability is set to 255 ("checked by a person").
4. Insert `transcript_edits (id, transcript_id, segment_seq, char_start, char_len, before, after,
   flag_id, user_id, batch_id, created_at, undone_at, carried_from)`.
5. `UPDATE transcripts SET revision = revision + 1`.
6. Set the flag's `status`, `resolution`, `resolved_by` and `resolved_at`. Other open flags on the
   same sentence whose spans overlap the edit become `stale`. Spans after it get their
   `char_start` shifted.

### 2. What follows the edit

- **Search:** `search_tsv` is a generated column (slot 0016), so it updates with the row.
  Embeddings: the edit enqueues `index` for the video with the sentence's seq in the job's
  payload. The stage re-embeds that one sentence only.
- **Subtitles:** `subtitle_gen`'s input fingerprint gains `transcripts.revision`
  (`SUBTITLE_VERSION` bumps). The fix's transaction enqueues `subtitle_gen` and `index` for the
  video through `queues.Enqueue` (`api/internal/handlers/queues/enqueue.go`, the same
  `processing_jobs_enqueue` SQL function Python uses, so a burst of fixes coalesces into one
  job). The "only overwrite a file we wrote" rule still holds.
  Many edits in a minute cost one rewrite.
- **Live VTT** (`streaming/internal/transcripts`) reads segments from the DB, so it follows at
  once.

### 3. Undo

- `undo(editID, userID)` reverses one edit: it applies `before` over the span the edit wrote.
  This is refused (409 `changed_since`) if that span no longer reads `after`, i.e. a later edit
  touched it.
- It sets `undone_at`, bumps `revision`, and puts the flag back to `open`. Flags it made stale
  are re-checked by the next `word_check` run.
- Batch undo reverses a batch's edits newest first, in one transaction, all or nothing.
- Who: an admin may undo anyone's edit. A reviewer may undo only their own (52.8 checks this).

### 4. Carry-over when a transcript is replaced

When `transcribe` stores a new transcript for a video (`_store` in
`engine/stages/transcribe.py`), it loads the old transcript's live (not undone) edits. For each
edit:

- It looks in the new transcript, within ±2 s of the old sentence's start, for a word span whose
  pack `key` (folded) equals the folded `before`.
- One match: the edit is applied there (Python writer, same semantics as Go, same fixtures). The
  new edit row's `carried_from` holds the old edit id.
- No match, or more than one: a flag with reason `previous_fix` is created, with the old fix as
  its first candidate.

This runs inside `_store`'s transaction, so the new transcript is never active without its fixes.

### 5. History

`GET /api/videos/{id}/transcript/edits` (admins and the library's reviewers) lists edits with
who, when, before and after, and undone. 52.11 shows it.

## Acceptance criteria

1. **Given** an open flag on «الصلوة», **when** a reviewer corrects it to «الصلاة», **then** the
   sentence text reads «الصلاة», `transcripts.revision` went up by 1, and one `transcript_edits` row
   records before and after.
2. **Given** that edit, **when** search is asked for «الصلاة», **then** the video's sentence is
   found within 1 s of the fix.
3. **Given** that edit, **when** 60 s pass, **then** `Name.ar.srt` and `Name.ar.vtt` contain the
   new word and `subtitle_files.sha256` matches the new files.
4. **Given** a sentence whose words after the edit had times, **when** the edit changes the
   word's length, **then** every later word's `char_start` moved by the difference, and its times
   are unchanged (decoded from `word_data`).
5. **Given** an edit, **when** its author undoes it, **then** the text, `word_data` and subtitles
   return to what they were, the flag is `open` again, and `undone_at` is set.
6. **Given** two edits on the same span, **when** someone undoes the first, **then** the answer is
   409 `changed_since` and nothing changes.
7. **Given** a reviewer, **when** they try to undo another reviewer's edit, **then** 403. An admin
   doing the same gets 200.
8. **Given** a batch of 38 edits, **when** it is undone, **then** all 38 are reversed in one
   transaction. If one of them cannot be, none are, and the answer names it.
9. **Given** a video with 5 live edits, **when** a relisten replaces its transcript and 4 of the
   words are found within ±2 s, **then** the new transcript has those 4 fixes (`carried_from` set)
   and one `previous_fix` flag with the old fix as a candidate.
10. **Given** an Arabic sentence with combining marks or an emoji, **when** the web sends offsets
    for a word after them, **then** the right word is replaced (code points, not bytes or UTF-16
    units).

## Build plan

### Files

- `api/internal/transcript/edit.go`: `ApplyEdit`, `Undo`, `UndoBatch`.
- `api/internal/transcript/worddata/worddata.go`: decode, encode and shift for 52.1's format.
- `api/internal/handlers/review/review.go`: calls `ApplyEdit` from `resolve`.
- `api/internal/handlers/videos/edits.go`: the history route, mounted in `videos.go`.
- `pipeline/src/maktaba_pipeline/transcript/edits.py`: the Python writer for carry-over (the same
  semantics), and `carry_over(db, old_tid, new_tid)`.
- `pipeline/src/maktaba_pipeline/engine/stages/transcribe.py`: `_store` calls `carry_over`.
- `pipeline/src/maktaba_pipeline/engine/stages/subtitle_gen.py` and `engine/graph.py`: revision
  in the fingerprint, `SUBTITLE_VERSION` bump.
- `pipeline/src/maktaba_pipeline/engine/stages/index.py`: re-embed one sentence when the payload
  names it.
- `shared/db/migrations/0173+_transcript_edits.sql` and `.sqlite.sql`: `transcripts.revision INT
  NOT NULL DEFAULT 0`, the `transcript_edits` table, and an index on `(transcript_id,
  segment_seq)`.
- `shared/testdata/word_data/edits/*.json`: golden before/after cases shared by Go and Python
  (insert, delete, longer, shorter, multi-word span, marks, emoji, first and last word).

### Tests

- Go `edit_test.go` and Python `test_edits.py` both run every golden case and must give
  byte-identical `word_data` and text.
- `edit_pg_test.go`: the transaction rolls back whole on a failure injected after the text update.
  It also covers stale detection, undo rules and batch all-or-nothing.
- `test_subtitle_gen.py`: a revision bump changes the fingerprint, and a file not written by
  Maktaba is still never overwritten.
- `test_transcribe.py`: carry-over with 4 placeable edits and 1 unplaceable one, and that the
  new transcript is active only with its fixes (a failure mid-carry-over leaves the old one
  active).
- `test_index.py`: a one-sentence re-embed touches only that sentence's vector row.
- A timing test on the scratch DB: `ApplyEdit` p95 under 300 ms with a 2,000-sentence transcript.

### Rollout

Phase 2 release. The `SUBTITLE_VERSION` bump re-runs `subtitle_gen` once for every video. That is
a cheap file write, but it touches every subtitle file on the share. It runs at the stage's
normal priority and the release notes say so.

## Edge cases

- **An edit to nothing** (a deleted word): `after` is empty. The word goes from `word_data`, and
  a sentence left empty stays with empty text (its time is kept for subtitles to skip).
- **An edit that adds words** («بسم الله» for «بسملة»): one span becomes several words. They share
  the old span's time evenly, the way `refine._spread` does.
- **A transcript that is not active** (superseded): edits are refused (409 `not_active`).
- **SQLite home:** the same transaction. `FOR UPDATE` is dropped (SQLite locks the database).
- **Two reviewers editing two words of one sentence at once:** the row lock orders them. The
  second gets fresh offsets, because spans are re-checked inside the lock.
