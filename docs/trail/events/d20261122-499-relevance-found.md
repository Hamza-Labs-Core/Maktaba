---
schema: 1
kind: incident
title: "Story 49.9's relevance check found two ranking gaps before any home ran it"
date: 2026-11-22
session: 7998f2a8-8a3e-41cf-99b3-4cf9a473ddc3
prs: [1505]
---

## What happened
- #1505 (open) builds story 49.9's relevance check: searches drawn with a fixed seed from
  the library's own documents (known-item sentences and their spelling and word-form
  variants, chapter titles, topic words, common words), and `maktaba-api search-relevance`
  to run it on a home, read only.
- On a synthetic 3,000-video library it failed 23 of 601 known-item and 12 of 50 chapter
  searches. It found two gaps in story 49.7's ranking:
  - **Spelling variants lost the phrase boost.** The boost read the lines' `search_tsv`,
    which 49.4 left unnormalised (23 M lines), so اسلام never matched إسلام as a phrase.
    The boost now also looks in the first 30 videos' normalised documents.
  - **A word prefixed by و** that Snowball keeps whole matched neither index. A word no
    document holds is now searched without its و or ف when the rest is a dictionary word.
- The 12 chapter failures were the generator's own: titles of common words name no one
  video. Chapters now need a rare word, as sentences do.
- A first version of the document phrase pattern never matched a phrase at the start of a
  line. The edge class excluded the line break as well as the gaps did.
  `TestPhrasePatternOnPostgres` holds it.
- After the fixes all 693 searches pass, and a sentence's p95 on 45,000 documents is 16.5 ms.

## What we learned
- A relevance check on synthetic data finds real ranking bugs. Its vocabulary must be large
  (most words rare), or nothing counts as distinctive. 4,000 words gave no known-item
  sentence at all; 35,152 gave 216.
- A normalisation left out of one index shows up wherever that index is used (here the
  phrase boost), not where the normalisation was added.

## Open
- The run on the home's own transcripts, and the tuning of the field weights, the
  exact/stem balance and the phrase boost it calls for.
