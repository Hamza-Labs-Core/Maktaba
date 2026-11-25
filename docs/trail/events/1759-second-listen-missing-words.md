---
schema: 1
kind: pr
pr: 1759
title: "One sentence without words left a whole transcript out of its second listen (#1756)"
date: 2026-11-25
session: c9608b0a-6764-4bd7-84eb-1bfe023a95c3
agent: claude-code
---

## What happened
- **Cause, from the code:** `second_listen` asked that every sentence with text has its words, and
  `transcribe` marks a transcript's words stored (`metadata.words`) when the words were asked for.
  One sentence without words skipped the whole transcript, and the relisten sweep, reading the
  mark, queued `second_listen` again, never `transcribe`, until the tries ran out (490 videos on
  one home by its log). The home's rows were read (read only) at the review.
- **A producer:** `stt/align.py` `place` keeps Whisper's bookkeeping, which counts each sentence's
  tokens from its first word. A "." decoded as a sentence of its own moves the later sentences'
  words along, and the window's last sentence is left with none.
- **#1759, first version,** let the second listen correct the sentences that had words. Its review
  found, on real data, that the old placement damaged their neighbours too: a word stored under the
  wrong sentence has no slot where it belongs, and rebuilding the sentence from its slots loses it.
- **#1759, as it is:** the videos left out are transcribed again, once a home, when no older worker
  serves the stages; `refine` leaves out a sentence whose words do not cover its text and no stretch
  runs over it; `place` splits a word over two sentences between them. A `second_listen` job queued
  before the update leaves its transcript to `transcribe`.
- **The home's own data agreed:** its segments carry `word_misses` (7,977 of 702,307 in the 890
  transcripts skipped for this reason, none in 82,344 of 300 others), which the old placement makes.

## What we learned
- **A mark that says what was asked is not what was stored.** The sweep chose its stage by the mark,
  so its retries could never change the outcome. Write such a mark from the stored rows.
- **A skip is not a stop of the later stages:** `_skipped` ends its job done, so by the code the
  subtitles, index and tags ran on Whisper's words; what those videos lacked is the correction.

- **Check that stored words cover their text before rebuilding the text from them.** That some
  sentence has words says nothing of which words they are.
- **The pipeline's `__version__` is stamped in the node wheel only,** so it cannot say which worker
  is older. A capability a worker reports can.

- **A step that changes what a sweep chooses must look at the jobs already queued:** the sweep leaves
  a video with an open job alone, and that job ran on the old choice.

## Open
- Two minors are left to an issue: a rollback and a second update can strand a few videos, and a
  word over two sentences under 0.02 s long leaves both out of the second listen.
- Transcripts corrected before the fix can have lost words the same way; none is looked for.
- On the home after the update: `transcribe.relisten_reset` (why "sentences without words"), then
  the skipped transcripts draining, 20 every 10 minutes, as `transcribe` jobs.
