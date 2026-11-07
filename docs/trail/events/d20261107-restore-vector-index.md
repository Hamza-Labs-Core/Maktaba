---
schema: 1
kind: decision
title: A restore builds the vector index alone, with memory of its own
date: 2026-11-07
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
---

## What happened
- The rollback of 0.3.90 restored all 46 GB of rows in under two hours. Then it spent hours on one index: segment_embeddings_hnsw, the HNSW graph over 28 M halfvec(384) rows, built with the server's 64 MB of maintenance_work_mem. It was 9% done after two hours, and the home was down the whole time.

## What we decided
- The restore's lists set the vector index apart. It is built after the other indexes, alone, with PGOPTIONS: a third of RAM (at most 32 GB) and up to 7 parallel workers. If that build fails, the same memory with no workers is tried, then the server's own settings, so a rollback never fails on a speed setting.
- Building it in the background after the swap would shorten the outage more. It was not chosen: a rollback switches back to the older release, which would not know to build it.

## Keep doing
- Watch pg_stat_progress_create_index during a long restore: blocks_done against blocks_total gives the estimate.
