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
  one home by its log). The home's rows were not read.
- **A producer:** `stt/align.py` `place` keeps Whisper's bookkeeping, which counts each sentence's
  tokens from its first word. A "." decoded as a sentence of its own moves the later sentences'
  words along, and the window's last sentence is left with none.
- **#1759:** the second listen corrects the sentences that have words; the videos it left out get
  their tries again once a home (`relisten_seen`); a transcript with no words at all is transcribed
  again, and one Whisper gives none for is recorded so; `place` gives a word to the sentence its
  first token is in.

## What we learned
- **A mark that says what was asked is not what was stored.** The sweep chose its stage by the mark,
  so its retries could never change the outcome. Write such a mark from the stored rows.
- **A skip is not a stop of the later stages:** `_skipped` ends its job done, so by the code the
  subtitles, index and tags ran on Whisper's words; what those videos lacked is the correction.

## Open
- On the home after the update: `transcribe.relisten_reset` (why "sentences without words"), then
  the skipped transcripts draining, 20 every 10 minutes.
