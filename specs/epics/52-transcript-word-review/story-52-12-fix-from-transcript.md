# Story 52.12 — Fix a word from the transcript

> Epic 52 · Phase 2 · Depends on: 52.9 · Needed by: —

## Why

Some mistakes are never flagged. Whisper can be confident and wrong, and a word can be in the
dictionary and still be the wrong word. A home admin who sees one while reading the transcript
tab should fix it right there (only home admins review, README §8 decision 1). That fix also teaches the vocabulary (52.10) and adds a known miss to
the evaluation data (52.2).

## Description

- The transcript tab in the video page (`TranscriptTab`, `web/src/pages/VideoDetail.tsx:385`) gains
  an **edit mode** for home admins. It is a pencil toggle, and the API decides who gets it:
  `GET /api/videos/{id}/segments` returns `can_review: true|false` (true only for a home admin,
  never from a library `write` or `admin` role).
- In edit mode, words are selectable. The words of each sentence come from
  `Intl.Segmenter(lang, {granularity: "word"})` (UAX #29) with `isWordLike`. A click selects one
  word; Shift+click or a drag extends to a span within the sentence.
- The selected span's UTF-16 offsets are converted to **Unicode code points** before sending (the
  edit contract of 52.9).
- A small popover over the word shows:
  - the word, an edit field, Name, Add to dictionary, Cancel;
  - a play button for the clip, from the word's time when the sentence has `word_data`, else the
    sentence;
  - open flags on that word, if there are any, which link to the queue item.
- Saving calls `POST /api/videos/{id}/transcript/words` `{segment_seq, char_start, char_len,
  before, text, action}`. The API:
  1. checks `canReview` (52.8);
  2. creates a `word_flags` row with reason `manual`, status set by the action, and priority 0;
  3. applies the edit through `ApplyEdit` (52.9);
  4. returns the updated sentence.
- `before` is sent so a concurrent change is caught (409 `stale`), and the tab reloads that
  sentence.
- Undo uses the same toast and route as the queue.
- Viewers and users with `write` on the library see the tab exactly as today: no pencil, no
  selectable words, no extra requests.
- The big-screen view (`web/src/bigscreen/screens/Video.tsx`) is not changed.

## Acceptance criteria

1. **Given** a home admin, **when** they open the transcript tab, **then** a pencil toggle is
   shown. **Given** a viewer, or a user with `write` on the video's library, **then** it is not,
   and the segments response says `can_review: false`.
2. **Given** edit mode, **when** the reviewer clicks a word and saves a new spelling, **then** the
   sentence shows the new word, a `manual` flag (status `corrected`) and an edit exist, and the
   subtitles follow within 60 s.
3. **Given** an Arabic sentence with harakat and a Latin word in it, **when** the reviewer selects
   the Latin word, **then** exactly that word is replaced (code-point offsets).
4. **Given** a selected span of 3 words, **when** it is saved as one word, **then** the edit covers
   all three, and `word_data` holds one word over their time.
5. **Given** the sentence was changed by someone else after the tab loaded, **when** the reviewer
   saves, **then** the answer is 409 `stale`, the sentence reloads, and nothing is overwritten.
6. **Given** a viewer, or a user with `write` or the library `admin` role who is not a home admin,
   calls `POST /api/videos/{id}/transcript/words` directly, **then** 403. With no session, 401.
7. **Given** a fix made here, **when** the reviewer presses Undo in the toast, **then** the edit
   is undone (52.9).
8. **Given** a word that has an open flag, **when** it is selected, **then** the popover shows that
   flag's reasons and candidates, and saving resolves that flag instead of making a new one.
9. **Given** the Arabic UI, **then** the popover, its strings and its layout are right to left,
   and the strings exist in English and Arabic.

## Build plan

### Files

- `web/src/pages/VideoDetail.tsx`: edit mode in `TranscriptTab`.
- `web/src/components/transcript/WordPicker.tsx` (segmentation and offsets) and
  `WordPopover.tsx`.
- `web/src/lib/codepoints.ts`: UTF-16 ↔ code point offsets.
- `web/src/i18n/en.json`, `web/src/i18n/ar.json`: `transcript.edit.*`.
- `api/internal/handlers/videos/segments.go`: `can_review` in the response.
- `api/internal/handlers/videos/words.go`: the POST route, mounted in `videos.go`, calling
  `review.canReview` and `transcript.ApplyEdit`.

### Tests

- `codepoints.test.ts`: offsets around Arabic combining marks, emoji (surrogate pairs), ZWJ, and
  tatweel.
- `WordPicker.test.tsx`: segmentation of Arabic, English and mixed sentences; single and span
  selection; no edit affordance when `can_review` is false.
- `words_test.go`: roles in the `TestAdminOnly` style, 409 on a stale `before`, the manual flag
  row, and an existing open flag resolved instead of duplicated.
- Playwright (branch stack): fix a word in the tab, see it in search and in the regenerated
  `.vtt`.

### Rollout

Ships with 52.11 in the phase 2 release.

## Edge cases

- **Segmenter differences:** `Intl.Segmenter` and the pack's tokenizer may split differently
  (clitics in Arabic). The edit is by characters, so it is still exact, and `word_check` re-tokenizes
  on its next run.
- **A browser without `Intl.Segmenter`:** fall back to splitting on whitespace and punctuation.
  All supported browsers have it today.
- **A sentence with no `word_data`:** the clip plays the sentence, and the edit still works.
- **Selecting across two sentences:** not allowed. The selection stops at the sentence end.
