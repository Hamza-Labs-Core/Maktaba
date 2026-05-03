# Story 24.6 — Disaster recovery

Documented recovery scenarios with verified RTO/RPO targets.

## Acceptance criteria

- AC1. Recovery scenarios documented with steps and expected wall-
  clock:
  1. **DB lost, media intact** — restore from latest backup;
     reprocess any new media since the backup. RTO ≤ 30 minutes.
     RPO ≤ 24 h (last daily backup).
  2. **DB and derived caches lost, media intact** — same as #1
     plus full reindex. RTO ≤ proportional to library size,
     reported per-library.
  3. **Media partially corrupted** — content_hash on
     mismatch detects; the affected videos transition to
     `state = CORRUPTED` and the user is notified. The
     `CORRUPTED` state is part of the architecture §3 FSM.
  4. **Service binaries corrupted** — reinstall via the canonical
     install path
     ([Epic 22.7](../22-devops/story-22-07-multi-platform-packaging.md));
     state intact.
- AC2. A `make dr-drill` target runs scenario #1 against a seeded
  fixture in CI nightly; failures alert.
- AC3. The user-visible "Restore" UI (admin panel) walks the user
  through each scenario with a single Run button per step.

## Test cases

- TC1. Scenario #1: `dr-drill` brings up a fresh stack, restores a
  previous-day dump, runs the catalog smoke test, all green within
  RTO budget.
- TC2. Scenario #3: corrupt a file's middle bytes; the next
  integrity check ([Story 24.7](story-24-07-integrity-verification.md))
  finds and marks it `CORRUPTED`; the UI shows it.
- TC3. Documented commands: every step in the DR doc has a
  copy-pasteable command that is exercised by the drill.

## Edge cases

- EC1. Partial DB restore (one schema lost) — the restore script
  refuses partial restores by default; an `--allow-partial` flag
  with documented risk is required.
- EC2. Restore onto a host with a higher schema version —
  migrations run forward automatically; the doctor reports the
  delta.
- EC3. Restore onto a host with a *lower* schema version — refused
  with a clear error; the operator is told to upgrade first.
