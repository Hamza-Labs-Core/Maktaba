---
schema: 1
kind: pr
pr: 799
title: An inline CHECK made 0209 destructive, so the wave rehearsal restored the dump
date: 2026-11-07
session: b0ea7fd9-6e6d-4c4f-ab23-200b28d0b3bd
agent: claude-code
---

## What happened
- Once 0209 was renumbered, scenario 16.3 (wave 3's jobs across a rollback and an update) failed because the rollback restored the dump. 0209 added `plugin_installs.data_version` with an inline `CHECK (data_version IS NULL OR data_version >= 1)`.
- The updater's `RollbackSafe` (`cmd/maktaba-server/internal/updater/schemakeep.go`) treats any added constraint as destructive, an inline column CHECK included (its `ADD … CHECK` pattern matches it). A destructive slot means the rollback restores the dump, and the 16.x wave scenarios fail on any restore.
- The CHECK was dropped; the migration says why. The writers already keep the value at NULL or at least 1. `RollbackSafe(…, 208, 209)` is true again, and #799 merged at d75c5391f once 16.3 passed.

## What we learned
- An additive migration must not add a constraint, not even inline on a new column, unless a restore on rollback is acceptable. Run `RollbackSafe` on the new slot before pushing: it answers in a second, and the rehearsal takes 30 minutes to say the same.
- The REQUIRE for a migration fix names the job that runs it: migration-lint runs in `lint / lint`, not in a job of its own.
