# Database migrations

Schema for Maktaba is owned by [`goose`][goose] migrations in this
directory. The Go binary `maktaba-api` ships the runner; `make migrate`
applies pending migrations against `$DATABASE_URL`.

[goose]: https://github.com/pressly/goose

> Story 22.4 owns the *meta*-discipline (runner, lints, conventions).
> Real schema migrations are introduced by their owning epics — see
> [`MANIFEST.md`](MANIFEST.md) for the slot reservations.

## Quickstart

```bash
# Apply all pending migrations
DATABASE_URL=postgres://maktaba:maktaba@localhost:5432/maktaba?sslmode=disable \
  make migrate

# Inspect what's applied vs. pending
DATABASE_URL=... make migrate-status
```

## Conventions (enforced by `make lint-migrations`)

1. **Append-only.** Once merged to `main`, the SQL bytes inside a
   migration file are immutable. Add a follow-up migration to fix
   mistakes; never edit history. CI compares each PR against
   `origin/main` and fails on any modification.
2. **Idempotent.** Every `CREATE`/`DROP` uses `IF NOT EXISTS` /
   `IF EXISTS` so re-running the same migration is a no-op. The lint
   refuses unguarded DDL.
3. **SQLite parity.** Every `NNNN_<topic>.sql` ships a sibling
   `NNNN_<topic>.sqlite.sql` with an equivalent schema, so the SQLite
   build (architecture §3.2) stays usable. The lint requires both
   files; equivalence is asserted by tests in P1.
4. **No long-running DDL on Postgres.** `CREATE INDEX` uses
   `CONCURRENTLY`; table rewrites (`ALTER COLUMN ... TYPE`,
   `SET NOT NULL` on populated columns) require a documented
   ship-DDL → backfill-job → flip-read-path pattern.
5. **Backfills are separate jobs.** DDL ships in one migration;
   idempotent backfill work is queued via `processing_jobs`
   (architecture §7).
6. **Down migrations are dev-only.** `+goose Down` blocks may exist for
   local convenience but are never run in production paths. Rollback
   in production is handled at the artifact layer (Story 22.6).
7. **Numbering is contiguous.** Slots are claimed in
   [`MANIFEST.md`](MANIFEST.md) with no gaps; if two PRs race for the
   next slot, the later one rebases.

## File naming

```
NNNN_<topic>.sql              ← Postgres (canonical)
NNNN_<topic>.sqlite.sql       ← SQLite parity sibling
NNNN_<topic>.down.sql         ← optional dev-only down (reuses slot)
NNNN_<topic>.post.sql         ← optional non-transactional follow-up
```

`NNNN` is a zero-padded 4-digit integer matching the row in
`MANIFEST.md`. `<topic>` is `lower_snake_case`.

## Running the migrations

`make migrate` runs `cd api && go run . migrate up` with a sensible
default migrations path. The CLI also supports `status`, `version`,
and `validate`:

```bash
cd api
go run . migrate up        # apply pending
go run . migrate status    # tabular applied/pending
go run . migrate version   # current schema version
go run . migrate validate  # smoke-check the directory shape
```

The runner stages Postgres-only files (`*.sql` minus `*.sqlite.sql`)
into a temporary directory before invoking goose, so the SQLite parity
siblings are never applied to a Postgres connection.

## Adding a migration

1. Claim the next slot in [`MANIFEST.md`](MANIFEST.md) in the same PR.
2. Write `NNNN_<topic>.sql` with `+goose Up` (and optionally
   `+goose Down`) blocks.
3. Mirror the schema in `NNNN_<topic>.sqlite.sql`.
4. Wrap each statement in `+goose StatementBegin` / `+goose StatementEnd`.
5. Use `IF [NOT] EXISTS` everywhere; use `CREATE INDEX CONCURRENTLY`
   when the index is non-trivial.
6. Run `make lint-migrations` locally before pushing.

## Pitfall: never write the literal `+goose` in prose comments

The `pressly/goose/v3` parser flags a line as an annotation candidate
with this predicate (see `internal/sqlparser/parser.go`):

```go
strings.HasPrefix(strings.TrimSpace(line), "--") && strings.Contains(line, "+goose")
```

The `Contains` half over-matches: any `--` line whose text contains
the four characters `+goose` — including a prose comment that quotes
the directive in passing — is parsed as an annotation. A line like

```sql
-- The directive on line 1 uses '+goose NO TRANSACTION' because…
```

aborts the migration with `not supported: invalid annotation`. The
predicate is identical in the version we pin (`v3.22.1` in
`api/go.mod`) and the latest release at time of writing (`v3.27.1`),
so bumping goose does not help — this section can be deleted once
upstream tightens the predicate to require `-- +goose` at the start
of the trimmed line.

### How to write prose that references a directive

When you need to explain a goose directive in prose, **don't quote
the literal token**. Refer to it indirectly:

- ✅ `-- The directive on line 1 disables goose's per-migration transaction wrapper.`
- ✅ `-- This file uses goose's StatementBegin/StatementEnd markers around DDL.`
- ❌ `-- We use '+goose NO TRANSACTION' here because…`

Block comments (`/* … */`) are a safe escape hatch — the predicate
requires the trimmed line to start with `--`, so block-comment text
is never scanned. Prefer the indirect-prose style above; reach for
block comments only when you genuinely need to quote the literal
token.

For real-world examples of the indirect style, see the header
comments in `0002_processing_jobs.sql` and `0008_scan_control.sql`.
