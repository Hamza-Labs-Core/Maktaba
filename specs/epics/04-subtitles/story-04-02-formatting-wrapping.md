# Story 4.2 — SRT/VTT formatting & line wrapping

## Description

Subtitles must be readable on a phone, on a 4K TV, and through external
players. Line length and break rules matter as much as the text content.

## Acceptance criteria

- Each cue is at most `max_line_chars = 42` characters wide and at most
  `max_lines = 2` lines (configurable per library).
- Line breaks favor:
  1. Sentence-end punctuation (`.`, `?`, `!`, `؟`).
  2. Clause-end punctuation (`,`, `;`, `،`, `؛`, `:`).
  3. Word boundaries (never break mid-word).
- Cues never overlap; if two adjacent segments are within
  `merge_gap_sec = 0.05`, they merge; if a single segment is longer than
  `max_cue_sec = 6.0`, it is split at sentence/clause boundaries with the
  text proportionally divided by character count along word-timestamp
  positions where available.
- For Arabic source language: prefer Arabic punctuation glyphs over
  Latin equivalents in the rendered cue text (the input transcript
  already contains them where the STT model produced them; the wrapper
  must not "normalize" them away).
- VTT cues include speaker tags (`<v Speaker 1>...`) only when
  diarization ran and `speaker IS NOT NULL`. Speaker labels themselves
  are HTML-escaped before being placed inside the `<v ...>` tag, so a
  speaker name containing `>` or `&` does not break VTT parsing
  (consistent with the cue-text escape rule in
  [Story 4.1](story-04-01-generate-from-segments.md)).

## Test cases

- `test_wrap_respects_max_line_chars` — segment text 200 chars → no
  output line > 42 chars.
- `test_wrap_breaks_at_sentence` — fixture sentence with mid-segment
  period → line break exactly there, not at the next word boundary.
- `test_no_overlap_after_merge_or_split` — generated cues' time spans
  do not overlap; `cue[i].end <= cue[i+1].start`.
- `test_long_segment_split_proportionally` — 12 s single segment with
  word timestamps → split into 2–3 cues each ≤ `max_cue_sec`, each
  cue's time range matches its text's word-timestamp range.
- `test_arabic_punctuation_preserved` — input contains `؟` → output
  contains `؟`, not `?`.
- `test_speaker_tag_only_when_diarized` — `speaker = NULL` → VTT cue
  has no `<v>` tag.
- `test_speaker_label_escaped` — speaker name `"Sheikh <A>"` → VTT
  emits `<v Sheikh &lt;A&gt;>...`, not `<v Sheikh <A>>...`.

## Edge cases

- **Word timestamps absent** but segment too long for one cue. We split
  by character count along the segment duration linearly — imperfect
  but defensible; record `metadata.split_method = "linear"`.
- **Tokens that themselves exceed `max_line_chars`** (URLs, hashtags).
  Such tokens are placed on their own line; the line is allowed to
  exceed the limit (one violation logged per file at DEBUG).
- **Bidi text mixing Arabic and English.** Wrap by grapheme cluster, not
  byte; verified against a fixture containing surrogate pairs and
  combining marks.
