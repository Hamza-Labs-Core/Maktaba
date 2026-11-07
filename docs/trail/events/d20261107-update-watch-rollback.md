---
schema: 1
kind: incident
title: 0.3.90 rolled back — the update watch timed out on a busy pipeline
date: 2026-11-07
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
---

## What happened
- A WindowServer crash at 04:33 ended the login session. The NFS share was unmounted, and jobs that needed it parked.
- The 0.3.90 update's watch asked every pipeline process for its version on each round, with a 3 s timeout. Under a load of about 90, the answers came late now and then, so the watch never saw ten healthy minutes in a row.
- Stopping the pipeline by hand during the watch (SIGTERM) left it `stopped`. The watch then ran out its 33-minute bound and rolled back (`watch_timeout`).
- A word_check transaction stayed open for 13+ minutes, blocking writers.
- An old Docker container (`maktaba-chroma-1`) was running again under its `unless-stopped` policy.

## What we learned
- Liveness is the supervisor's job. An update watch only needs to confirm each process's version once per pid.
- Never stop processes on the live home while an update watch is running.

## Keep doing
- Read the server log's watch lines before blaming the release itself.
