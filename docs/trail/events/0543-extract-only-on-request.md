---
schema: 1
kind: pr
pr: 543
title: Extract only on request; WAVs released and swept
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge b1ad4dbc. The probe->extract edge is removed. The engine probe (`chain_extract=False`) queues no extract. Transcribe (`may_request=("extract",)`) asks for it only when `configured_backend_requires_file()` is true (openai-api): it ends `waiting_inputs`, extract writes the WAV and re-queues transcribe, which decodes it, records extract's track and releases it. A row whose file is on another machine is decoded from the share, never re-requested (no loop). The live mlx-whisper backend never gets an extract job.
- Found: `db.jobs.enqueue`'s done-row skip treated a `waiting_inputs` run as done work, so extract's re-enqueue of transcribe was dropped as `skipped_done_unchanged`. That outcome and `awaiting_review` no longer count as done.
- WAV lifetime: `release_audio_cache` runs after the transcribe that read the WAV. `run_audio_cache_sweep` runs hourly on every worker: rows older than 6 h with no live extract/transcribe job, and files older than 6 h of this database's videos with no row. Files of hashes this database does not know are kept (branch stacks, tests).
- `--no-engine`: with no `audio_cache` row a streaming backend reads the source on extract's track; a job with no payload uses the video's hash and the selection policy.
- No migration: edges and `may_request` belong to the graph sync. The live extract backlog that 0172 releases drains as #527 no-ops.
- Harness: the fake DB implements `_enqueue_sqlite`; extract and transcribe handler tests run on both dialects.
- Failed first: the affected-tests gate; fixed with tests in the mapped files.

## What we learned
- Scratch Postgres needs `initdb -E UTF8`.
- Use `uv sync --all-extras` (not `--extra stt`).
- Scratchpad paths are too long for a Postgres unix socket; start it with `-k ''`.

## Open
- The legacy probe and FSM advance SQL is Postgres-only (`now()`, `FOR UPDATE`).
- Gap found later: with `--no-engine` the chosen audio track was not decoded (fixed in #556).
