# Implementation Plan — Story 22.4 Database migrations

> Companion to [story-22-04-database-migrations.md](story-22-04-database-migrations.md).
> Story states *what* and *why*; this plan states *how*.
> Migration-driven schema is the contract surface in
> [architecture.md §8](../../architecture.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Migration tool | `goose` (already chosen in architecture §12). Migrations live in `shared/db/migrations/`. |
| Filename convention | `NNNN_<topic>.sql` for Postgres; `NNNN_<topic>.sqlite.sql` for SQLite parity. |
| Driver | Goose runs from `api/cmd/api migrate` (Go binary); Pipeline reads schema-only via asyncpg/aiosqlite (architecture §12). The Go binary is the canonical migrator. |
| Migrate-only flag | `maktaba-api migrate up` (no serve). Boot-time auto-migrate is gated by `MAKTABA_AUTO_MIGRATE=true` (default in compose; off in production-grade deployments). |
| Linter | `tools/migration-lint.go` runs in the lint gate (Story 22.1). Append-only, idempotency, long-running DDL checks. |
| Out of scope | Specific schemas (Epics 1–10 own their own DDL); this story owns the *meta*-discipline only. |

## 1. Architecture diagram

```
shared/db/migrations/
├── 0001_init.sql              ← initial schema (Epic 1)
├── 0001_init.sqlite.sql       ← parity
├── 0002_jobs.sql              ← Epic 6 jobs table
├── 0002_jobs.sqlite.sql
├── ...
└── meta/
    ├── 0000_goose_version.sql ← managed by goose itself
    └── lints.json             ← lint exemptions w/ rationale + expiry

         ┌──────────────────────┐
         │ tools/migration-lint │ ◄── runs in `make lint`
         │  - append-only check │
         │  - idempotency guard │
         │  - long-DDL guard    │
         │  - sqlite parity     │
         └──────────────────────┘
                  │ pass
                  ▼
         ┌──────────────────────┐
         │ maktaba-api migrate  │
         │  up | doctor | status│
         └──────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `tools/migration-lint.go` | Static checks; runs in CI lint. |
| `tools/migration-lint/append_only.go` | Compares migration file content against `origin/main`. |
| `tools/migration-lint/idempotent.go` | AST-walks SQL for unguarded `CREATE TABLE`, `CREATE INDEX`, `ALTER COLUMN`. |
| `tools/migration-lint/long_running.go` | Flags `CREATE INDEX` without `CONCURRENTLY` on Postgres-targeted files; flags table rewrites. |
| `tools/migration-lint/parity.go` | Asserts every Postgres migration has a `.sqlite.sql` sibling whose statement count is within tolerance. |
| `api/cmd/api/migrate.go` | Cobra subcommand: `up`, `down` (dev-only), `status`, `doctor`. |
| `shared/db/migrations/meta/lints.json` | Recorded exemptions: `{ "0042_users_index": { "rule": "long-running", "reason": "small table", "expires": "2026-12-31" } }`. |
| `shared/db/migrations/README.md` | Author guide with the conventions documented below. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/cmd/api/main.go` | Register `migrate` subcommand. |
| `api/internal/db/dialect.go` | `IsSQLite()`, `IsPostgres()` helpers used by lint and code. |
| `Makefile` | `make migrate-up`, `make migrate-doctor`. |
| `.github/workflows/_lint.yml` | Invoke `tools/migration-lint`. |

### 2.3 Migration file conventions

`shared/db/migrations/README.md` documents the rules; the linter
enforces them:

```
1. Append-only. Once merged to main, the SQL bytes inside a migration
   file are immutable. Add a follow-up migration to fix mistakes.
2. Idempotent. Every CREATE/DROP uses IF NOT EXISTS / IF EXISTS so that
   a second invocation is a no-op.
3. SQLite parity. Every <NNNN>_<topic>.sql ships a <NNNN>_<topic>.sqlite.sql
   sibling that produces an equivalent schema (within documented
   dialectical limitations).
4. No long-running DDL on Postgres. CREATE INDEX uses CONCURRENTLY;
   table rewrites (ALTER TABLE … TYPE) require a documented batched plan.
5. Backfills are separate. DDL ships in one migration; idempotent backfill
   jobs are queued via processing_jobs (architecture §7).
6. Down migrations exist for local dev only (EC1). Production rollback
   is handled at the artifact layer (Story 22.6).
7. Numbering is contiguous; gaps are forbidden.
```

### 2.4 Append-only check

`tools/migration-lint/append_only.go`:

```go
package main

import (
    "bytes"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
)

func appendOnlyCheck(baseRef string) error {
    out, err := exec.Command("git", "diff", "--name-status", baseRef, "HEAD",
        "--", "shared/db/migrations/").Output()
    if err != nil {
        return err
    }
    for _, line := range bytes.Split(out, []byte{'\n'}) {
        if len(line) == 0 || bytes.HasPrefix(line, []byte("A\t")) {
            continue // Adds are fine.
        }
        // Modified or deleted; allow only the README and lints.json.
        path := string(bytes.SplitN(line, []byte{'\t'}, 2)[1])
        if filepath.Base(path) == "README.md" || filepath.Base(path) == "lints.json" {
            continue
        }
        return fmt.Errorf("migration %s is not append-only: status=%s", path, line[0:1])
    }
    return nil
}
```

The CI runs with `baseRef=origin/main`; locally, `make lint` uses
`origin/main` as well so behaviour is identical (Story 22.8 AC-3).

### 2.5 Idempotency check

`tools/migration-lint/idempotent.go`:

```go
package main

import (
    "regexp"
)

// Patterns that need a guard.
var unguarded = []*regexp.Regexp{
    regexp.MustCompile(`(?i)\bCREATE\s+TABLE\s+(?!IF\s+NOT\s+EXISTS)`),
    regexp.MustCompile(`(?i)\bCREATE\s+(UNIQUE\s+)?INDEX\s+(?!IF\s+NOT\s+EXISTS|CONCURRENTLY\s+IF\s+NOT\s+EXISTS)`),
    regexp.MustCompile(`(?i)\bDROP\s+TABLE\s+(?!IF\s+EXISTS)`),
    regexp.MustCompile(`(?i)\bDROP\s+INDEX\s+(?!IF\s+EXISTS)`),
    regexp.MustCompile(`(?i)\bALTER\s+TABLE\s+\S+\s+ADD\s+COLUMN\s+(?!IF\s+NOT\s+EXISTS)`),
}

func idempotencyCheck(file string, sql []byte) error {
    for _, re := range unguarded {
        if loc := re.FindIndex(sql); loc != nil {
            return fmt.Errorf("%s: unguarded DDL near byte %d: %s",
                file, loc[0], string(sql[loc[0]:loc[1]]))
        }
    }
    return nil
}
```

Goose's `+goose StatementBegin/StatementEnd` markers wrap each
statement; the linter strips them before regex matching.

### 2.6 Long-running DDL guard

`tools/migration-lint/long_running.go`:

```go
var longRunning = []*regexp.Regexp{
    // CREATE INDEX without CONCURRENTLY on Postgres files.
    regexp.MustCompile(`(?i)\bCREATE\s+(UNIQUE\s+)?INDEX\b(?!\s+CONCURRENTLY)`),
    // Table rewrites: ALTER COLUMN TYPE, SET NOT NULL on a populated col.
    regexp.MustCompile(`(?i)\bALTER\s+TABLE\s+\S+\s+ALTER\s+COLUMN\s+\S+\s+TYPE\b`),
    regexp.MustCompile(`(?i)\bALTER\s+TABLE\s+\S+\s+ALTER\s+COLUMN\s+\S+\s+SET\s+NOT\s+NULL\b`),
}

func longRunningCheck(file string, sql []byte, dialect string) error {
    if dialect == "sqlite" { return nil }
    for _, re := range longRunning {
        if loc := re.FindIndex(sql); loc != nil {
            if isExempt(file, "long-running") {
                return nil
            }
            return fmt.Errorf("%s: long-running DDL — wrap in CONCURRENTLY or "+
                "split into ship-DDL + backfill-job (see migrations/README.md): %s",
                file, string(sql[loc[0]:loc[1]]))
        }
    }
    return nil
}
```

`lints.json` exemptions look like:

```json
{
  "0042_speakers_index": {
    "rule": "long-running",
    "reason": "Empty table; the index materializes immediately.",
    "expires": "2026-12-31"
  }
}
```

Expired exemptions fail CI (parallel to the SECURITY suppressions
pattern).

### 2.7 SQLite parity

`tools/migration-lint/parity.go`:

```go
func parityCheck() error {
    pg := glob("shared/db/migrations/[0-9]*.sql")
    for _, p := range pg {
        if strings.HasSuffix(p, ".sqlite.sql") {
            continue
        }
        sibling := strings.TrimSuffix(p, ".sql") + ".sqlite.sql"
        if _, err := os.Stat(sibling); err != nil {
            return fmt.Errorf("%s missing SQLite sibling %s", p, sibling)
        }
    }
    return nil
}
```

Equivalent-schema parity (column count + name matching, not byte
identity) is asserted by an integration test that builds both schemas
and compares their `information_schema` snapshots through a small
abstraction.

### 2.8 The migrate subcommand

`api/cmd/api/migrate.go`:

```go
package main

import (
    "context"
    "embed"
    "errors"
    "fmt"

    "github.com/pressly/goose/v3"
    "github.com/spf13/cobra"
)

//go:embed shared/db/migrations/*.sql
var migrationsFS embed.FS

func newMigrateCmd() *cobra.Command {
    var (
        only   bool
        accept bool
    )
    cmd := &cobra.Command{
        Use:   "migrate",
        Short: "Run schema migrations",
        RunE: func(cmd *cobra.Command, args []string) error {
            db, dialect, err := openDB(cmd.Context())
            if err != nil { return err }
            goose.SetBaseFS(migrationsFS)
            if err := goose.SetDialect(dialect); err != nil { return err }
            // Pre-flight: ask the doctor first.
            est, err := planAndEstimate(cmd.Context(), db, dialect)
            if err != nil { return err }
            if est.LongestStatement > 30*time.Second && !accept {
                return fmt.Errorf("estimated %s on largest statement; "+
                    "rerun with --accept-long-migration", est.LongestStatement)
            }
            if err := goose.UpContext(cmd.Context(), db, "shared/db/migrations"); err != nil {
                return err
            }
            return nil
        },
    }
    cmd.Flags().BoolVar(&only, "migrate-only", false, "exit after running migrations")
    cmd.Flags().BoolVar(&accept, "accept-long-migration", false, "allow >30 s statements")
    cmd.AddCommand(newMigrateDoctorCmd())
    cmd.AddCommand(newMigrateStatusCmd())
    return cmd
}
```

`migrate-only` is read by `main.go`'s root: when set, the binary runs
migrations and exits 0 instead of starting the HTTP server.

### 2.9 The doctor subcommand

`migrate doctor` runs the pending migrations against a temp DB seeded
from `pg_dump`:

```go
func runDoctor(ctx context.Context, prodDSN string) (*Estimate, error) {
    tmpDSN, cleanup, err := createTempDB(ctx, prodDSN)
    if err != nil { return nil, err }
    defer cleanup()

    if err := loadDump(ctx, prodDSN, tmpDSN); err != nil { return nil, err }

    est := &Estimate{}
    err = applyMigrationsTimed(ctx, tmpDSN, func(name string, d time.Duration) {
        est.PerStatement = append(est.PerStatement, StatementTime{name, d})
        if d > est.LongestStatement {
            est.LongestStatement = d
        }
    })
    return est, err
}
```

The doctor prints a per-statement estimate to stderr; ops then decide
whether to pass `--accept-long-migration`.

## 3. Test plan

### 3.1 Linter

| Test | What it pins |
|---|---|
| `TestAppendOnlyEditFails` (TC1) | A fixture branch that edits `0001_init.sql` (modifies a single byte) fails the lint with the file path in the error. |
| `TestIdempotencyCheckCatchesUnguarded` | A migration with `CREATE TABLE foo (...)` (no `IF NOT EXISTS`) fails with the byte offset in the error. |
| `TestLongRunningGuardCatchesIndex` (TC3) | A migration with `CREATE INDEX idx ON videos(name)` on a > 10 k row table fails; `CREATE INDEX CONCURRENTLY` passes. |
| `TestLongRunningExemption` | An expired exemption in `lints.json` fails; a valid one passes. |
| `TestParityCheckRequiresSibling` (AC5) | A new `0099_widgets.sql` without `0099_widgets.sqlite.sql` fails. |

### 3.2 Migrate command

| Test | What it pins |
|---|---|
| `TestMigrateUpRunsTwiceIsNoop` (TC2) | First `migrate up` applies pending; second is no-op (zero DDL emitted, zero errors). |
| `TestMigrateOnlyExits` | `--migrate-only` returns exit 0 without launching the HTTP server. |
| `TestMigrateAutoOnBoot` | With `MAKTABA_AUTO_MIGRATE=true`, `serve` runs migrations before listening. |
| `TestMigrateRefuseLong` | A synthetic 60 s-estimated migration without `--accept-long-migration` fails fast with the estimate; with the flag, runs. |

### 3.3 SQLite parity

| Test | What it pins |
|---|---|
| `TestSchemaParityPostBootBuildsBothFresh` | Apply all migrations to a fresh Postgres and a fresh SQLite; the column lists for each table match (modulo type aliases documented in `dialect.go`). |
| `TestSqliteFKEnabled` | `PRAGMA foreign_keys` returns 1 on every connection. (Asserted in 24.3 too; here we verify migrations don't drop it.) |

### 3.4 Backfill pattern (EC2)

| Test | What it pins |
|---|---|
| `TestAddColumnNullableThenBackfill` | Migration A adds nullable; an idempotent `processing_jobs` task fills; migration B sets NOT NULL only after the job reports done. The pattern is exercised via a synthetic story. |

## 4. Edge cases

| Case | Behaviour | Where pinned |
|---|---|---|
| Down migrations (EC1) | Goose `+goose Down` blocks are present for dev convenience but flagged in production via `MAKTABA_DISABLE_DOWN=true`. The doctor never runs Down. | `TestDownDisabledInProd` |
| Backfill needed (EC2) | Author ships DDL in migration N, an idempotent `processing_jobs` task in migration N+1, and the read-path flip in N+2. The pattern is documented; tests exercise the three-step. | `TestAddColumnNullableThenBackfill` |
| SQLite-missing feature (EC3) | E.g., partial unique index — SQLite variant uses full index + filter. Parity test compares query results, not raw schemas. | `TestPartialIndexSqliteFallback` |
| Goose version mismatch | `goose` is vendored under api/vendor/; bumping the binary requires a vendor refresh. CI pins goose by version. | n/a |
| Migration order race in dev | Two devs add `0099_*` simultaneously. The append-only check fires when both PRs land — only one can win; the loser bumps to `0100_*` and rebases. | Documented in `migrations/README.md` |
| `gen_random_uuid()` requires `pgcrypto` | The init migration enables `pgcrypto`; SQLite uses `lower(hex(randomblob(16)))` via a Go helper that mints UUIDs application-side for the SQLite path. | `TestUuidInsertSqlitePath` |
| `IF NOT EXISTS` blocks index re-creation after rename | Documented: when renaming an index, drop+create-with-new-name in two statements both guarded; not a single `ALTER`. | n/a |
| Migration timeout under load | Goose runs each statement under `statement_timeout = '5min'`; the doctor estimate must come in under that. The `accept-long-migration` flag also bumps `statement_timeout`. | `TestStatementTimeoutEnforced` |
| Goose meta table | `goose_db_version` is owned by goose; never touched by app code; CI lint forbids any migration that writes to it. | `TestNoMetaTableWrites` |

## 5. Dependencies

| Dep | Version | Why |
|---|---|---|
| `github.com/pressly/goose/v3` | latest minor | Migration runner. |
| `github.com/spf13/cobra` | already | CLI. |
| `embed` | stdlib | Embed migrations into the api binary. |
| `pg_dump`/`pg_restore` | matches Postgres major | Doctor. |

## 6. Acceptance checklist

**Linter**
- [ ] `tools/migration-lint` runs in the lint gate.
- [ ] Append-only check compares against `origin/main`.
- [ ] Idempotency, long-running DDL, and parity checks fire as documented.

**Migrator**
- [ ] `maktaba-api migrate up` runs at boot under `MAKTABA_AUTO_MIGRATE`.
- [ ] `--migrate-only` exits without serving.
- [ ] `--accept-long-migration` is required for > 30 s statements.

**Conventions**
- [ ] `shared/db/migrations/README.md` documents the rules.
- [ ] Every `.sql` ships a `.sqlite.sql` sibling.
- [ ] `lints.json` exemptions carry rationale + expiry.

**Tests**
- [ ] All §3 tests pass on Postgres and SQLite via the dialect-parametrized fixture.
