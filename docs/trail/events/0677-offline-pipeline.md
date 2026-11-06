---
schema: 1
kind: pr
pr: 677
title: serve --offline reaches the pipeline; the dictionary survives a failed pack switch
date: 2026-11-06
session: b705488f-6ddd-4a03-8d08-60545e9b7ffa
prs: [668, 665]
---

## What happened
- A review of #668 found that `MAKTABA_OFFLINE` stopped no Python call. Wikipedia (books, on by default), the reconciler's scheduled YouTube refresh and its downloads, enrichment's web search and CAMeL's dictionary download all ran offline. The pipeline now reads the switch (`maktaba_pipeline/offline.py`). The YouTube stages are left unclaimed, through the stage selector's `unavailable` reason. `books` answers from its cache, and its fingerprint says offline so the next run once online is not skipped. Enrichment picks no search backend.
- `TestOutboundHostsAreListed` scans `pipeline/src` and all of `cmd/`. On the first run it found `ar.wikipedia.org`.
- #665's dictionary reload replaced a working dictionary with None when the new pack's folder failed to load, and its download fallback wrote into the old version's folder, which the pack install may already have deleted. A failed reload now keeps the dictionary in use and retries after 5 minutes. A folder inside a pack is never created or downloaded into.

## What we learned
- `StageImpl.unavailable` and `cluster/stages._cannot_run` are the right place for "this worker must not run it now": jobs stay pending and the Machines page says why. Failing them would spend their attempts.
- `tests/conftest.py` replaces `classify.books.http_get` for every test, so a unit test of its own guard cannot call the real function.
- No local Postgres is installed on the Mac, but the installed Maktaba's update payload carries one (`~/Library/Application Support/Maktaba/updates/payloads/<v>/postgres/bin`, with pgvector). `initdb` it in the scratchpad with `unix_socket_directories=''` (the scratchpad path is over the socket length limit) to run the `[postgres]` tests.
