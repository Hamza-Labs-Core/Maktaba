# Story 35.4 — Home update orchestrator

## Description

*As the owner, I want the home to update itself step by step, checking as it goes, and to either finish
or end up exactly where it started, even if the power goes out in the middle.*

The supervisor (`maktaba-server`) owns the run: it is the one process that outlives every role restart,
and the launcher (35.2) covers its own. It executes the steps of design §5.3 from a journal on disk,
mirrors them into `update_runs` / `update_run_steps`, and commits or rolls back the binaries.

Design: [design.md](design.md) §5; README R5, state machine "Update run (home)".

## Acceptance criteria

- **AC1** `POST /update/start {run_id}` on the supervisor loopback (127.0.0.1:9103, local token) starts a
  run; `GET /update/status`, `POST /update/abort` (before `migrating` only), `POST /update/rollback`.
  One run at a time (`updates/lock` plus a partial unique index on active `update_runs` states).
- **AC2** Steps in order: `preflight` → `download` → `verify` → `stage` → `smoke` → `backup` →
  `rehearse` → `drain` → `migrate` → `tv` → `restart_roles` → `restart_supervisor` → `watch` → `commit`
  → `rollout` (design §5.3), each with the described idempotency and failure state.
- **AC3** `updates/journal.json` is rewritten atomically before and after every step's side effects; on
  supervisor start an unfinished journal resumes per design §5.5; the journal decides, the database rows
  mirror.
- **AC4** `stage` extracts into `bin/releases/<v>.tmp/`, re-verifies extracted digests, builds the venv
  (`cmd/maktaba-server/internal/pyenv`, hash-checked), writes `.verified`, renames to `<v>`.
- **AC5** `smoke` runs `bin/releases/<v>/maktaba-api selfcheck` (config, DB read-only, `migrate status`,
  applied ≤ release `schema_rev`), `maktaba-streaming --version`, the venv's `python -m maktaba_pipeline
  --version`.
- **AC6** `drain` sends `cmd drain` to the home's worker sessions and waits up to
  `drain_timeout_minutes`; `migrate` runs `bin/releases/<v>/maktaba-api migrate up` while the old API
  serves.
- **AC7** `restart_roles`: `api` → `pipeline` → front door (only when `edge_protocol` changed), one at a
  time; a role is healthy (design §5.4) when its probe passes, `/readyz` is 200, it reports the new
  version and (pipeline) its agent session reconnects with the new version, all within 2 minutes.
- **AC8** `watch` lasts 10 minutes; a crash loop, a red `/readyz` or an unstable TV routing epoch rolls
  back. `commit` removes `pending`, sets the local and database watermarks, undrains, prunes releases and
  backups, sets `home_committed`, notifies.
- **AC9** Failure before `migrate` → `failed`, nothing changed, retried next window, at most 3 attempts
  per version, then `blocked`. Failure at `migrate` → `failed_migrate`, old binaries keep running, version
  blocked. Failure after → `rolling_back` → `rolled_back` (design §5.6), schema stays, version blocked.
- **AC10** Rollback order: front door (if restarted), `pipeline`, `api` back to `bin/releases/<from>/`;
  TV via Epic 33.9; supervisor `current = previous`, exit 75; refused (and reported) when `from`'s
  `schema_rev < maktaba_min_node_schema()`.
- **AC11** Every step start/finish/failure is an `update_run_steps` row, a log line in
  `updates/logs/<run_id>.log`, an event (`update_step_*`) and an `audit_log` row (category `update`).

## Test cases

- **TC1** `TestRunHappyPath` — fake children, fake fixture release, SQLite; reaches `home_committed`.
- **TC2** `TestResumeTable` — for every step, crash before and after its side effects; resume reaches the
  same end state.
- **TC3** `TestFailBeforeMigrateLeavesNothing`, `TestFailMigrateKeepsOldBinaries`,
  `TestReadyzFailRollsBack`, `TestWatchCrashLoopRollsBack`.
- **TC4** `TestThreeAttemptsThenBlocked`.
- **TC5** `TestRollbackRefusedBelowMinNodeSchema`.
- **TC6** `TestSingleRunLock`.
- **TC7** Integration on Postgres: v0.0.1-test → v0.0.2-test with two migrations (one NO TRANSACTION).

## Edge cases

- **EC1** The API restarts during the run (it is one of the roles): the run is unaffected; the page
  reconnects and reads the rows.
- **EC2** The run passes `window_overrun_minutes` before `migrate`: abandoned as `failed (interrupted)`;
  after `migrate` it always finishes or rolls back.
- **EC3** Disk fills during `stage`: `<v>.tmp` removed, `failed`.
- **EC4** The drain timeout expires with a transcription running: it checkpoints and is re-queued (Epic 31
  R4.4); not a failure.
- **EC5** Abort requested after `migrate`: refused (409 `past_migrate`); the run finishes or rolls back.

## Dependencies

35.1, 35.2, 35.3 (start), 35.5 (backup, rehearse), 35.6 (`tv` step), 35.7 (`restart_supervisor`),
Epic 22.9 supervisor.
