---
schema: 1
kind: pr
pr: 806
title: YouTube and the repair of broken downloads run as Maktaba's built-in plugins (Epic 54 wave 6, 54.33)
date: 2026-11-07
session: 8687f2a4-808d-47dd-93b7-2c1167e0f385
prs: [800]
---

## What happened
- `youtube_sync`, `youtube_fetch`, `youtube_backfill` and `exact_dates` moved into `tv.maktaba.youtube`, and `repair_broken` into `tv.maktaba.repair`, in the `net` runner. Roles: migration 0206.
- A relayed `enqueue` may name the stage that starts a new subject (`probe` for a video). A download's video row is in the plugin's uncommitted transaction, so the worker holds that request and queues it after the job succeeds.
- The manifests list the rules sections their stages read (`exactdates`, `repair`).
- The parity proofs for youtube (with exact_dates) and repair measure N16. So does wave 5's dedup proof, which had lacked it (added to #800).

## What we learned
- A row made by a column default (`gen_random_uuid()`) can't get the same id on both parity sides by patching Python. Give each test database the same default instead: a sequence-backed function set as the column default in the seed.
- The statement API refuses a `rules.*` read that the manifest's `settings` does not list (`plugin_scope`). A stage that is off by default passes parity without ever reading its rules, so seed the rules on for every stage that has them.
- A placed file's mtime is the time of the placement, not the downloader's. Mask it, and mask the library stages whose input hash reads it (`input_hash@clip_groups`).
- ffmpeg's error text carries pointers (`@ 0x…`) that differ between runs. The harness masks them in the job-log comparison.
- A parity test passes `group=` explicitly. After a plugin moves to another group, the test's runner serves nothing and everything falls back (`runner_not_serving_here`). The fallback check catches it.
- A check of each plugin test for `compare_n16` would have caught the missing N16 earlier.
- The sandbox's `exec` must name the program a stage actually starts. yt-dlp runs as `MAKTABA_YTDLP` (a command line) or `python -m yt_dlp`, but `tool_path("yt-dlp")` looked only for a `yt-dlp` on PATH. On an install without one, the `net` runner could not run yt-dlp, and real downloads would have been refused and run in the worker. The parity fakes replace the subprocess, so they never exec anything. CI caught it because neither job has yt-dlp on PATH. A developer machine with Homebrew's yt-dlp did not. `tool_path` now resolves yt-dlp the way `ytdlp_command()` does.

## Open
- The nightly check of the hosts yt-dlp contacts (T02): issue #805.
- The update-rehearsal scenarios of every wave (16.1–16.6): issue #804.
- The job-scoped path check (T03): issue #796.
