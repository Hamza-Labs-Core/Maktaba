---
schema: 1
kind: decision
pr: 585
title: Epic 52 spec: word-level transcript review
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge c7b503cc9. The second review in the transcript path is the Cohere second listen, not an LLM. Whisper's word probabilities are computed and then dropped, and word times are stored only when `MAKTABA_STT_STORE_WORDS=1` (off by default).
- So `transcribe` will capture word evidence, and a new CPU `word_check` stage will flag suspect words using language packs (Epic 51 components).
- The LLM judge flags but never rewrites and is off by default (an LLM judge was wrong in the hearing test). Learned vocabulary stays out of the transcribe fingerprint, so it never re-queues a library.

## Open
- Four owner questions in README section 8 (answered in #588).
