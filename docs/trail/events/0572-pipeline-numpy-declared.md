---
schema: 1
kind: pr
pr: 572
title: Pipeline declares numpy; test_paths_per_os off the real /home
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge bdd079fcf. The pipeline declares numpy (`dev`, `stt`, `search` extras). The tests and the stt/search code import it, but no extra declared it, so `uv run --extra dev pytest` could not collect tests/stt and tests/stages.
- The default-worker runtime test now asks every stage that can be unavailable (transcribe, index, chapter_infer, topic_recluster).
- pipeline/README.md gives the full-suite command: `--all-extras` with a UTF8 scratch Postgres.
- `test_paths_per_os` used home=/home/u for Windows cases, and /home is an automatic network mount on macOS (0.13 s per lookup, over the 0.3 s hard cap under load); it now uses tmp_path.

## What we learned
- A SQL_ASCII Postgres server fails write_channels staging on Arabic text; scratch Postgres must be UTF8.
- On macOS /home is an automounter path; touching it in a test costs a network lookup.
