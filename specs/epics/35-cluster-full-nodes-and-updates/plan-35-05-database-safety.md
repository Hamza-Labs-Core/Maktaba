# Plan 35.5 — Database safety — implementation

> Implementation plan for [story-35-05-database-safety](story-35-05-database-safety.md). Design: [design.md](design.md) §6.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Forward-only schema; binaries roll back. | Additive migrations make the previous release run on the new schema; automatic downs risk data. |
| D2 | Restore is manual and confirmed. | It loses every write since the backup, including nodes' work. |
| D3 | Rehearse on a scratch database restored from the backup. | Catches a failing migration on real data without touching the real database; also tests the backup. |
| D4 | Idempotency of NO TRANSACTION files is a test, not a review note. | Power loss mid-file must be recoverable by re-running. |
| D5 | Contract steps marked in MANIFEST and checked by `compat-prev`. | Makes `breaks_previous_home` truthful; such releases are held (35.3). |

## 1. Files

**Create**
- `cmd/maktaba-server/internal/updater/steps_backup.go` — pg_dump/VACUUM INTO, verify, record
- `cmd/maktaba-server/internal/updater/steps_rehearse.go` — scratch DB lifecycle, timing
- `cmd/maktaba-server/internal/dbtools/pgtools.go` — locate matching `pg_dump`/`pg_restore`, version check
- `cmd/maktaba-server/cmd_restore.go` — `maktaba-server restore`
- `shared/db/migrations/migrations_rules_test.go` — NO TRANSACTION idempotency, MANIFEST markers
- `tools/compat-prev/run.sh` and a `compat-prev` Make target

**Modify**
- `api/selfcheck_cli.go` — `--deep` (open every repository, run the smoke read queries)
- `shared/db/migrations/MANIFEST.md`, `shared/db/migrations/README.md` — the "Breaking for the previous
  release" marker and the idempotency rule
- `.github/workflows/_integration.yml` — `compat-prev` job
- `deploy/packaging/upgrade.md` — forward-only policy, restore command, backups location
- `api/internal/updates/handlers.go` (35.3) — list backups for the Updates page

## 2. Steps

1. pg tool discovery and version check; backup step with verification; SQLite path.
2. Rehearsal step; cleanup of stale scratch databases at start.
3. Migration rule tests; fix any existing NO TRANSACTION file that fails them (0127/0128 checked first).
4. `compat-prev` tooling and CI job.
5. `restore` command; docs.

## 3. Tests

Story TCs; integration on Postgres 15 and 16 (format and compression choice).

## 4. Rollout

Phase B with 35.4. The rule tests land first (phase A) so releases built from then on are safe to
rehearse and roll back.
