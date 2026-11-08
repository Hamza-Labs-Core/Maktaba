---
schema: 1
kind: pr
pr: 921
title: Pulling a video's audio for transcription is a queue stage like the others (Epic 54 wave 9, step 5, 54.36 §2)
date: 2026-11-07
session: 848393e1-ea22-4a25-a90f-b930df232381
prs: [847, 919, 920]
---

## What happened
- `extract` became an engine stage. Its `transcribe` is a declared request carrying the payload transcribe reads. For a streaming backend the request is left out when the video's transcription is already handled, as before. `transcribe` and `extract` joined `MONOTONE_STAGES`.
- The stage's decoder is a module attribute (`decode = extract_to_file`), so tests can swap it and mypy sees it as exported. A re-export via `import ... as` was not.
- Slot 0213 seeds `extract` as an engine stage, SchemaRev 213. The full pipeline suite on a fresh Postgres passed (4802).

## What we learned
- That full run on a fresh cluster also showed a test that passed only because an earlier run had left a Postgres role behind (#902).

## Open
- The move into `tv.maktaba.core-media` (wave 9f), then the removal one release later, and only if no stage was flipped back.
- `categorize` has never had a handler. Remove it or keep it declared: an owner decision.
