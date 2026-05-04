# Implementation Plan — Story 22.4 Database migrations

> Companion to [story-22-04-database-migrations.md](story-22-04-database-migrations.md).
> Story states *what* and *why*; this plan states *how*.
> Migration-driven schema is the contract surface in
> [architecture.md §8](../../architecture.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Migration tool | `goose` (already chosen in architecture §12). Migrations live in `shared/db/migrations/`. |
| Filename convention | Per-dialect, fully suffixed: `NNNN_<topic>.up.pg.sql` and `NNNN_<topic>.up.sqlite.sql` (with `.down.<dialect>.sql` siblings for dev). The earlier `NNNN_<topic>.sql` (Postgres) plus `NNNN_<topic>.sqlite.sql` shape collapsed in cases where Postgres-only features (`IF NOT EXISTS` on `ADD COLUMN`, partial unique indexes) had no clean sibling; the per-dialect form lets each file be syntactically valid for its target. |
| Embed | The `//go:embed` directive cannot escape its own package directory, so the SQL files cannot be embedded directly from `api/cmd/api/`. A dedicated `shared/go/migrations/` Go module (owned by Story 22.2 §0) sits next to `shared/db/migrations/` and re-exports the SQL files via `embed.FS`. `api/cmd/api/migrate.go` imports this module instead of using a local `//go:embed`. |
| Driver | Goose runs from `api/cmd/api migrate` (Go binary); Pipeline reads schema-only via asyncpg/aiosqlite (architecture §12). The Go binary is the canonical migrator. |
| Migrate-only flag | `maktaba-api migrate up` (no serve). Boot-time auto-migrate is gated by `MAKTABA_AUTO_MIGRATE=true` (default in compose; off in production-grade deployments). |
| Linter | `tools/migration-lint.go` runs in the lint gate (Story 22.1). Append-only, idempotency, long-running DDL checks. |
| Schema canonicalization (CI gate) | `make migrate-up-fresh` boots the full migration set against an empty Postgres + SQLite under CI and asserts every plan-touched table reaches its declared shape (cross-cutting §1.1; complement to plan-24-03's CHECK lint). Catches both ordering bugs and "two epics edit the same column" merge collisions. |
| Out of scope | Specific schemas (Epics 1–10 own their own DDL); this story owns the *meta*-discipline only. |

## 1. Architecture diagram

```
shared/db/migrations/
├── 0001_extensions.up.pg.sql       ← CREATE EXTENSION pgcrypto (this story)
├── 0001_extensions.up.sqlite.sql   ← no-op
├── 0002_init.up.pg.sql             ← initial schema (Epic 1)
├── 0002_init.up.sqlite.sql         ← parity
├── 0003_jobs.up.pg.sql             ← Epic 6 jobs table
├── 0003_jobs.up.sqlite.sql
├── ...
└── meta/
    ├── 0000_goose_version.sql      ← managed by goose itself
    └── lints.json                  ← lint exemptions w/ rationale + expiry

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
| `tools/migration-lint/parity.go` | Asserts every `<NNNN>_<topic>.up.pg.sql` has a `<NNNN>_<topic>.up.sqlite.sql` sibling whose statement count is within tolerance (and same for `.down.pg.sql`/`.down.sqlite.sql`). |
| `tools/migrate-up-fresh.go` | CI helper: boots an empty Postgres + an empty SQLite, runs the full migration set in order, then introspects `information_schema` (Postgres) and `pragma_table_info` (SQLite) to assert every plan-touched table reaches its declared shape. Owned here; consumed by the lint gate via `make migrate-up-fresh`. |
| `api/cmd/api/migrate.go` | Cobra subcommand: `up`, `down` (dev-only), `status`, `doctor`. Imports `github.com/maktaba/shared/go/migrations` for the embedded SQL files. |
| `shared/db/migrations/meta/lints.json` | Recorded exemptions keyed by the FULL migration filename (e.g. `0042_users_index.up.pg.sql`), not the basename — two epics could otherwise both add `0042_*` and have their exemptions collide. Schema: `{ "0042_users_index.up.pg.sql": { "rule": "long-running", "reason": "small table", "expires": "2026-12-31" } }`. |
| `shared/db/migrations/0001_extensions.up.pg.sql` | Earliest migration (sequence-zero / 0001 if Epic 1 hasn't claimed it). Contents: `CREATE EXTENSION IF NOT EXISTS pgcrypto;` so `gen_random_uuid()` resolves regardless of Epic 1 ordering. SQLite sibling is a no-op (UUIDs are minted application-side via `lower(hex(randomblob(16)))`). |
| `shared/db/migrations/README.md` | Author guide with the conventions documented below. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/cmd/api/main.go` | Register `migrate` subcommand. |
| `api/internal/db/dialect.go` | `IsSQLite()`, `IsPostgres()` helpers used by lint and code. |
| `Makefile` | `make migrate-up`, `make migrate-doctor`, `make migrate-up-fresh` (boots empty Postgres + SQLite, applies the full migration set, asserts the resulting shape). |
| `.github/workflows/_lint.yml` | Invoke `tools/migration-lint`. |
| `.github/workflows/_integration.yml` | Adds a `migrate-up-fresh` step that runs against the integration-gate Postgres service and a freshly-created SQLite file; failure blocks merge. |

### 2.3 Migration file conventions

`shared/db/migrations/README.md` documents the rules; the linter
enforces them:

```
1. Append-only. Once merged to main, the SQL bytes inside a migration
   file are immutable. Add a follow-up migration to fix mistakes.
2. Idempotent. Every CREATE/DROP uses IF NOT EXISTS / IF EXISTS so that
   a second invocation is a no-op. Note: SQLite added IF NOT EXISTS
   to ALTER TABLE ... ADD COLUMN only in 3.35; we target older SQLite
   builds, so ADD COLUMN goes through a per-dialect migration file or
   the Go helper described in §2.7a.
3. Per-dialect filenames. Each migration ships two files:
      <NNNN>_<topic>.up.pg.sql      (Postgres)
      <NNNN>_<topic>.up.sqlite.sql  (SQLite)
   Both files are mandatory; the linter's parity check fails the build
   if either is missing. Dev-only down migrations follow the same
   pattern with `.down.pg.sql` / `.down.sqlite.sql` suffixes.
4. No long-running DDL on Postgres. CREATE INDEX uses CONCURRENTLY;
   table rewrites (ALTER TABLE … TYPE) require a documented batched plan.
5. Backfills are separate. DDL ships in one migration; idempotent backfill
   jobs are queued via processing_jobs (architecture §7).
6. Down migrations exist for local dev only (EC1). Production rollback
   is handled at the artifact layer (Story 22.6).
7. Numbering is contiguous; gaps are forbidden. The first numbered
   migration creates required Postgres extensions (`pgcrypto` for
   `gen_random_uuid()`); the SQLite sibling is a no-op because UUIDs
   are minted application-side on that dialect (`lower(hex(randomblob(16)))`).
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

// Patterns that need a guard. The ADD COLUMN guard is dialect-specific:
// `IF NOT EXISTS` was added to SQLite's ALTER TABLE only in 3.35.0
// (Mar 2021) and we target older builds, so SQLite migrations either
// (a) probe `pragma_table_info` first via the helper in §2.7a, or
// (b) split into a dedicated SQLite-only migration file. Postgres files
// can use IF NOT EXISTS freely.
var unguardedPostgres = []*regexp.Regexp{
    regexp.MustCompile(`(?i)\bCREATE\s+TABLE\s+(?!IF\s+NOT\s+EXISTS)`),
    regexp.MustCompile(`(?i)\bCREATE\s+(UNIQUE\s+)?INDEX\s+(?!IF\s+NOT\s+EXISTS|CONCURRENTLY\s+IF\s+NOT\s+EXISTS)`),
    regexp.MustCompile(`(?i)\bDROP\s+TABLE\s+(?!IF\s+EXISTS)`),
    regexp.MustCompile(`(?i)\bDROP\s+INDEX\s+(?!IF\s+EXISTS)`),
    regexp.MustCompile(`(?i)\bALTER\s+TABLE\s+\S+\s+ADD\s+COLUMN\s+(?!IF\s+NOT\s+EXISTS)`),
}

// SQLite drops the ADD COLUMN guard; the linter routes to per-dialect
// patterns based on the file's suffix (`.up.pg.sql` vs `.up.sqlite.sql`).
var unguardedSqlite = []*regexp.Regexp{
    regexp.MustCompile(`(?i)\bCREATE\s+TABLE\s+(?!IF\s+NOT\s+EXISTS)`),
    regexp.MustCompile(`(?i)\bCREATE\s+(UNIQUE\s+)?INDEX\s+(?!IF\s+NOT\s+EXISTS)`),
    regexp.MustCompile(`(?i)\bDROP\s+TABLE\s+(?!IF\s+EXISTS)`),
    regexp.MustCompile(`(?i)\bDROP\s+INDEX\s+(?!IF\s+EXISTS)`),
}

func idempotencyCheck(file string, sql []byte) error {
    patterns := unguardedPostgres
    if strings.HasSuffix(file, ".sqlite.sql") {
        patterns = unguardedSqlite
    }
    for _, re := range patterns {
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

`lints.json` exemptions key by the FULL filename (per-dialect),
including the `.up.<dialect>.sql` suffix, so two epics that both add
`0042_*` cannot accidentally share an exemption record:

```json
{
  "0042_speakers_index.up.pg.sql": {
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
    pg := glob("shared/db/migrations/[0-9]*.up.pg.sql")
    for _, p := range pg {
        sibling := strings.TrimSuffix(p, ".up.pg.sql") + ".up.sqlite.sql"
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

### 2.7a SQLite ADD COLUMN helper

SQLite < 3.35 does not support `ALTER TABLE … ADD COLUMN IF NOT
EXISTS`. Rather than gate on the SQLite version (the project ships
SQLite 3.34 inside some embedded builds), the migrator probes
`pragma_table_info` for the column and skips the ALTER when present:

```go
// shared/go/migrations/sqlite_helpers.go
package migrations

import (
    "context"
    "database/sql"
    "fmt"
)

// AddColumnIfMissing executes `ALTER TABLE … ADD COLUMN <colDef>` only
// when the named column is absent. Idempotent on every SQLite version
// we ship.
func AddColumnIfMissing(ctx context.Context, db *sql.DB, table, column, colDef string) error {
    var exists int
    row := db.QueryRowContext(ctx,
        `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column)
    if err := row.Scan(&exists); err != nil {
        return err
    }
    if exists > 0 {
        return nil
    }
    _, err := db.ExecContext(ctx,
        fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s`, table, colDef))
    return err
}
```

Goose's `+goose Go` block invokes the helper from a SQLite migration
file when the migration adds a column. The Postgres sibling uses
`IF NOT EXISTS` directly. Either way, both files reach the same shape
when the `migrate-up-fresh` CI gate (§2.10) introspects them.

### 2.10 Schema-canonicalization gate (`migrate-up-fresh`)

Two failure modes that the append-only check cannot catch:

1. **Ordering drift** — Plan A and Plan B both add a column to
   `videos`; A merges first as `0042_*`, B merges second as `0043_*`,
   but B's SQL was authored against the pre-A shape. The CI append-only
   check is fine, but a clean DB built from the full chain fails.
2. **Cross-dialect divergence** — A Postgres file references a CHECK
   that the SQLite sibling forgot.

`tools/migrate-up-fresh.go` boots a fresh Postgres (the integration
gate's service container) and a fresh SQLite file, applies every
migration in lexical order, and asserts the resulting shape:

```go
func runFreshUp(ctx context.Context, dialect string) error {
    db, err := openTempDB(ctx, dialect)
    if err != nil { return err }
    defer cleanup(db)

    if err := goose.UpContext(ctx, db, migrations.SubdirFor(dialect)); err != nil {
        return fmt.Errorf("goose up failed on fresh %s: %w", dialect, err)
    }

    // For every plan-touched table, assert the declared shape (column
    // names + types). The expected-shape registry is populated by each
    // epic's plan (cross-cutting §1.1 — schema canonicalization). When
    // a plan declares "videos has columns (id, library_id, size_bytes,
    // …)", that registry entry is checked here.
    for table, want := range plansRegistry {
        got, err := introspect(ctx, db, dialect, table)
        if err != nil {
            return fmt.Errorf("introspect %s: %w", table, err)
        }
        if diff := schemaDiff(want, got); diff != "" {
            return fmt.Errorf("table %s does not match declared shape:\n%s", table, diff)
        }
    }
    return nil
}
```

Wired into `_integration.yml` via `make migrate-up-fresh`. A failing
gate is a hard-block on merge — the plan author is responsible for
either bumping the migration order or updating their declared shape.

### 2.8 The migrate subcommand

`api/cmd/api/migrate.go`:

```go
package main

import (
    "context"
    "errors"
    "fmt"

    "github.com/pressly/goose/v3"
    "github.com/spf13/cobra"

    // The embed lives in its own module because //go:embed cannot
    // escape the package directory. The module ships alongside
    // shared/db/migrations/*.sql and re-exports them as `migrations.FS`.
    "github.com/maktaba/shared/go/migrations"
)

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
            goose.SetBaseFS(migrations.FS)
            if err := goose.SetDialect(dialect); err != nil { return err }
            // Pre-flight: ask the doctor first.
            est, err := planAndEstimate(cmd.Context(), db, dialect)
            if err != nil { return err }
            if est.LongestStatement > 30*time.Second && !accept {
                return fmt.Errorf("estimated %s on largest statement; "+
                    "rerun with --accept-long-migration", est.LongestStatement)
            }
            // Per-dialect filename suffix (".up.pg.sql" / ".up.sqlite.sql")
            // is filtered by goose via the dialect-specific subdirectory
            // selection — see migrations.SubdirFor(dialect).
            if err := goose.UpContext(cmd.Context(), db, migrations.SubdirFor(dialect)); err != nil {
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
| `TestAppendOnlyEditFails` (TC1) | A fixture branch that edits `0002_init.up.pg.sql` (modifies a single byte) fails the lint with the file path in the error. |
| `TestIdempotencyCheckCatchesUnguarded` | A migration with `CREATE TABLE foo (...)` (no `IF NOT EXISTS`) fails with the byte offset in the error. |
| `TestLongRunningGuardCatchesIndex` (TC3) | A migration with `CREATE INDEX idx ON videos(name)` on a > 10 k row table fails; `CREATE INDEX CONCURRENTLY` passes. |
| `TestLongRunningExemption` | An expired exemption in `lints.json` fails; a valid one passes. |
| `TestParityCheckRequiresSibling` (AC5) | A new `0099_widgets.up.pg.sql` without `0099_widgets.up.sqlite.sql` fails. |

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
| `TestSchemaCanonicalization` | `make migrate-up-fresh` boots empty Postgres + SQLite and asserts every plan-touched table reaches its declared shape. Failure cases: an epic that re-orders columns from another epic; a sibling migration that diverges from the Postgres canonical shape. |
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
| `gen_random_uuid()` requires `pgcrypto` | The first migration in `shared/db/migrations/` enables `pgcrypto` (`CREATE EXTENSION IF NOT EXISTS pgcrypto;`); SQLite uses `lower(hex(randomblob(16)))` via a Go helper that mints UUIDs application-side for the SQLite path. This story owns the extension migration directly — Epic 1 is not assumed to author it. | `TestUuidInsertSqlitePath` |
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
- [ ] Idempotency (per-dialect), long-running DDL, and parity checks fire as documented.

**Migrator**
- [ ] `maktaba-api migrate up` runs at boot under `MAKTABA_AUTO_MIGRATE`.
- [ ] `--migrate-only` exits without serving.
- [ ] `--accept-long-migration` is required for > 30 s statements.
- [ ] Embed comes from `github.com/maktaba/shared/go/migrations` (no `//go:embed shared/db/migrations/*.sql` from `api/cmd/api/`).

**Schema canonicalization**
- [ ] `make migrate-up-fresh` boots empty Postgres + empty SQLite, applies the full migration set, and asserts every plan-declared table reaches its declared shape. CI-blocking.
- [ ] `pgcrypto` extension is enabled in the first numbered migration (does not depend on Epic 1).

**Conventions**
- [ ] `shared/db/migrations/README.md` documents the rules.
- [ ] Every `<NNNN>_<topic>.up.pg.sql` ships a `<NNNN>_<topic>.up.sqlite.sql` sibling.
- [ ] `lints.json` exemptions carry rationale + expiry and key by full filename (including dialect suffix).
- [ ] SQLite ADD COLUMN routes through the `AddColumnIfMissing` Go helper or a dedicated SQLite migration file (per-dialect filenames make this trivial).

**Tests**
- [ ] All §3 tests pass on Postgres and SQLite via the dialect-parametrized fixture.
