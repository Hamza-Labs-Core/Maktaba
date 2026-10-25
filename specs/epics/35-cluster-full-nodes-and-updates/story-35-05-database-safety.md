# Story 35.5 — Database safety

> **Status:** built. AC5 is migration-lint's rules 5 and 6 (`tools/migration-lint/rules35.go`);
> AC6 is `make compat-prev` (`tools/compat-prev/run.sh`, CI job `integration / compat-prev`),
> which also checks the nodes' schema guard both ways (an old node against a new home at the
> schema level: the old release's code and guard on the new database, not an old node process); AC8 is `maktaba-server restore` and the
> Updates page's *Backups* (`serve_restore.go`, `api/internal/updates/backups.go`). One change
> to AC8: the page restores itself, behind a dialog where the backup's name is typed, instead of
> showing the command (the owner's rule: a behaviour is a feature, never a manual step); the
> command stays for a home whose web does not answer.

## Description

*As the owner, I want every automatic update that touches the database to take a backup it has checked,
to try the migrations on a copy first, and never to roll the schema back by itself.*

The schema only moves forward: migrations are additive (expand/contract across releases), so the
previous release keeps running on the new schema and a rollback replaces binaries only. Before a release
with migrations, the home takes a verified backup and rehearses the migrations on a scratch database
restored from it. Restoring a backup is a manual, confirmed command.

Design: [design.md](design.md) §6; README decisions "Database rollback", "Contract releases"; R5.3.

## Acceptance criteria

- **AC1** `backup` step (only when `migrate status` lists pending migrations): Postgres `pg_dump
  --format=custom` (zstd:3 on Postgres 16+, else gzip 6) with the migration user into
  `<data_dir>/backups/pre-<version>-<utc>.dump`, mode 0600, never on the share; `pg_restore --list` must
  parse it and list every table; size and SHA-256 in `update_runs.backup`. SQLite: `VACUUM INTO`.
- **AC2** `pg_dump` must match the server's major version; a mismatch fails the step (`pg_dump_version`)
  before anything changed.
- **AC3** `rehearse` step (pending migrations and `rehearse_migrations`): `CREATE DATABASE
  maktaba_rehearse_<run8>`, `pg_restore` the backup, `bin/releases/<v>/maktaba-api migrate up` with a
  30-minute statement timeout, `maktaba-api selfcheck --deep`, `DROP DATABASE`; its duration is recorded
  and shown. Without `CREATEDB` or above `rehearse_max_db_gb` (20) it is skipped with a warning.
- **AC4** A rehearsal failure blocks the version (`blocked_reason = rehearsal`) and notifies; the real
  database is untouched.
- **AC5** Migration rules enforced by tests in `shared/db/migrations`: NO TRANSACTION files are
  idempotent (every statement `IF NOT EXISTS` / `IF EXISTS` / `CREATE OR REPLACE`); "Breaking for older
  nodes" rows define `maktaba_min_node_schema()`; the new marker "Breaking for the previous release" sets
  `breaks_previous_home` (35.1).
- **AC6** CI `make compat-prev`: the last tag's API repository tests run against a database migrated by
  HEAD; a failure without the "Breaking for the previous release" marker fails the build.
- **AC7** Down migrations are never run by the updater; `MAKTABA_DISABLE_DOWN` stays the production
  default (deploy/packaging/upgrade.md).
- **AC8** `maktaba-server restore --backup <file> --confirm` (home only): stops every role but the
  supervisor, disconnects nodes (`cmd disconnect`), `pg_restore --clean --if-exists`, sets `current` to
  the kept release whose `schema_rev` matches the backup's applied goose version (refuses if none is
  kept), restarts; audited. The Updates page lists backups and shows the command, not a button.
- **AC9** Backups pruned to `backups_keep` (3) after a commit, never the one of an active run.

## Test cases

- **TC1** `TestBackupPostgresVerified` (integration; corrupt file → step fails).
- **TC2** `TestBackupSkippedWithoutPendingMigrations`.
- **TC3** `TestRehearsalFailingMigrationBlocks` — a test migration that fails on restored data.
- **TC4** `TestRehearsalSkippedWithoutCreatedb`, `TestRehearsalSkippedAboveSize`.
- **TC5** `TestNoTransactionFilesIdempotent` — parses every NO TRANSACTION file.
- **TC6** `TestManifestMarkersMatchFunction` (extends the Epic 31 test).
- **TC7** `TestRestoreChoosesMatchingRelease`, `TestRestoreRefusesWithoutConfirm`.
- **TC8** CI job `compat-prev`.

## Edge cases

- **EC1** Disk space for the dump is estimated from `pg_database_size` in preflight (35.4); a dump that
  still runs out fails the step and deletes the partial file.
- **EC2** A crash during `rehearse` leaves `maktaba_rehearse_*`: dropped at the next start.
- **EC3** A crash mid NO TRANSACTION migration: goose marks it unapplied; re-run completes it (AC5).
- **EC4** A restored database older than every kept release: `restore` refuses and prints the version to
  install by hand.

## Dependencies

35.4 (steps), 35.1 (`breaks_previous_home`), Epic 22.4/22.6 (migrations, `migrate down` guard).
