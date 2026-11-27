---
schema: 1
kind: pr
pr: 1791
title: Weekly PostgreSQL backups with tiered retention and archival compression
date: 2026-11-27
session: 01a21b8e-de09-7821-9616-58a412529342
agent: codex
---

## What happened

- [PR #1791](https://github.com/Hamza-Labs-Core/Maktaba/pull/1791) merged to main as `abc06e8be03c38ccafc988d6debb2cc97ec38e2b`. Scheduled PostgreSQL backups default to every seven days (`every_days: 7`) at 03:00 local time.
- Scheduled dumps use grandfather-father-son retention. The keep set combines the four newest dumps (`keep: 4`), the newest dump in each of the 12 most recent represented months (`keep_monthly: 12`), and the newest dump in each of the five most recent represented years (`keep_yearly: 5`). Explicit `0` disables the corresponding monthly or yearly tier. The recent-dump tier requires at least one.
- Scheduled directory dumps older than 60 days are automatically compressed in place with zstd (`compress_after_days: 60`). Verification and parallel restore stream archived chunks. External PostgreSQL restore expands compressed chunks into private temporary files before the restore transaction so psql can read them with its existing copy path.
- The backup CLI, supervisor settings API, and English and Arabic Storage settings expose the policy. The PR also updated bilingual Storage help and NAS documentation and added paired changelog fragments.

## What we learned

- Archiving changes the dump directory's mtime. The archiver restores it, and `dumpAt` and `scheduledAt` use the timestamp in the scheduled dump name so a crash before mtime restoration cannot make the dump appear newer for age and retention decisions.
- Use `BackupsHeld` guards throughout archival work to protect dumps in use. Recompression verifies the SHA-256 of the decompressed `.dat.zst` contents against the input before replacing the archive.
