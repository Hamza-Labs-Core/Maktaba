# Story 24.5 — Backup and restore

Every state-bearing surface has a documented, tested backup and
restore procedure.

## Acceptance criteria

- AC1. **Postgres:** daily `pg_dump --format=custom` to a configured
  backup root; retention configurable (default 14 days); a
  `maktaba-api restore --from <file>` command runs `pg_restore` with
  conflict-safe options.
- AC2. **SQLite:** daily `VACUUM INTO` snapshot; restore is a file
  copy.
- AC3. **ChromaDB:** documented as recoverable from the source media
  + transcripts (rebuild via `reprocess --from-stage index`); not
  separately backed up unless explicitly opted in.
- AC4. **Caches:** never backed up (HLS, sprites, embedding cache);
  documented.
- AC5. **Media volume:** out of scope for Maktaba's backup; pointer
  to "use your existing media backup story (rsync, ZFS snapshots,
  Time Machine, etc.)" with linked recommendations.

## Test cases

- TC1. Restore drill: run the daily backup, simulate disaster
  (drop the DB), restore, assert the catalog smoke test passes.
- TC2. Cross-version restore: a v1.0 dump can be restored into a
  v1.1 server; migrations run forward; data intact.
- TC3. Chroma rebuild: delete the Chroma store; `reprocess --from-
  stage index` restores it; semantic search returns equivalent
  results within tolerance.

## Edge cases

- EC1. Backup-during-burst — `pg_dump` runs in a low-traffic window
  (configurable cron); a documented "snapshot now" command exists
  for one-off snapshots.
- EC2. Backup file corruption — the backup runner verifies the
  dump immediately by streaming through `pg_restore --list` after
  writing; corrupted backups are alerted on.
- EC3. Backup target full — the runner deletes the oldest backup
  beyond retention before writing the new one; if still full,
  fails the new backup with an alert (does not overwrite a good
  recent backup).
