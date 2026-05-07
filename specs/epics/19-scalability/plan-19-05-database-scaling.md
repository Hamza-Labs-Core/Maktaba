# Implementation Plan — Story 19.5 Database Scaling & Failover

> Companion to [story-19-05-database-scaling.md](story-19-05-database-scaling.md).
> Streaming replica setup, daily backup, restore drill, migration safety lint.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Replication | Postgres 16 streaming replication, async, single replica in v1. |
| Routing | API uses primary by default; `search` reads route to replica with ≤ 5 s lag tolerance. |
| Backup | `pg_dump --jobs=4 --compress=zstd:6` daily at 03:00 local; retain 14 days. |
| Restore drill | CI nightly: dump fresh, restore, run smoke catalogue check. |
| Migration safety | Pre-merge lint blocking long ops on hot tables. |

## 1. Project layout

```
ops/
├── postgres/
│   ├── primary.conf
│   ├── replica.conf
│   ├── pg_hba.conf
│   └── setup-replica.sh         # `pg_basebackup` + recovery
├── backup/
│   ├── pg_dump.sh
│   ├── restore.sh               # one-line restore for drill
│   └── retention.sh             # prune > 14 days
├── launchd/
│   └── com.maktaba.pgdump.plist
├── systemd/
│   └── maktaba-pgdump.timer
│   └── maktaba-pgdump.service
api/internal/dbroute/
├── router.go                    # primary vs replica routing
├── lag_monitor.go               # alert when lag > 60s
└── router_test.go
shared/db/migrations/
└── lint_long_running.go         # `make migrate:lint`
docs/runbooks/
├── postgres-replica.md
├── postgres-backup-restore.md
└── postgres-failover.md
```

## 2. Replica setup script

```bash
#!/usr/bin/env bash
# ops/postgres/setup-replica.sh
set -euo pipefail

PRIMARY_HOST=${1?primary host}
DATA_DIR=${2:-/var/lib/postgresql/16/replica}
REPL_USER=${REPL_USER:-replicator}

systemctl stop postgresql@16-replica || true
rm -rf "$DATA_DIR"
sudo -u postgres pg_basebackup \
    -h "$PRIMARY_HOST" -U "$REPL_USER" -D "$DATA_DIR" \
    -P -R --wal-method=stream

# postgresql.auto.conf added by -R contains primary_conninfo + standby.signal
systemctl start postgresql@16-replica
echo "Replica started — verify with: SELECT pg_is_in_recovery();"
```

`primary.conf` excerpt:

```ini
wal_level = replica
max_wal_senders = 5
wal_keep_size = 1GB
hot_standby_feedback = on
```

## 3. API routing

```go
// api/internal/dbroute/router.go
type Router struct {
    primary *sql.DB
    replica *sql.DB
    lagOK   atomic.Bool
}

func (r *Router) Read(query string) *sql.DB {
    if r.replica == nil || !r.lagOK.Load() { return r.primary }
    return r.replica
}

func (r *Router) Write() *sql.DB { return r.primary }
```

```go
// api/internal/dbroute/lag_monitor.go
func (m *LagMonitor) Run(ctx context.Context) {
    t := time.NewTicker(5 * time.Second); defer t.Stop()
    for {
        select {
        case <-ctx.Done(): return
        case <-t.C:
            var lag float64
            err := m.replica.QueryRowContext(ctx,
                `SELECT EXTRACT(EPOCH FROM (now() - pg_last_xact_replay_timestamp()))`).Scan(&lag)
            if err != nil || lag > 60 {
                if m.lagOK.Swap(false) {
                    metrics.ReplicaLagAlert.Inc()
                    slog.Warn("replica lag > 60s; routing search to primary", "lag_s", lag)
                }
                continue
            }
            if lag <= 5 {
                m.lagOK.Store(true)
            }
        }
    }
}
```

Search handler:

```go
db := router.Read("search")
```

## 4. Daily backup

```bash
#!/usr/bin/env bash
# ops/backup/pg_dump.sh
set -euo pipefail

OUT_DIR=/var/backups/maktaba
DATE=$(date +%Y%m%d)
DEST="$OUT_DIR/pg-$DATE"
mkdir -p "$DEST"

pg_dump \
  --host=localhost --port=5432 \
  --username="$PG_USER" --dbname="$PG_DB" \
  --jobs=4 --format=directory --compress=zstd:6 \
  --no-owner --no-acl \
  --file="$DEST"

# checksum
( cd "$DEST" && find . -type f -print0 | sort -z | xargs -0 sha256sum > SHA256SUMS )

# retention
"$(dirname "$0")/retention.sh" --keep=14
```

systemd timer:

```ini
# ops/systemd/maktaba-pgdump.timer
[Unit]
Description=Daily pg_dump for Maktaba
[Timer]
OnCalendar=*-*-* 03:00:00
RandomizedDelaySec=15min
Persistent=true
[Install]
WantedBy=timers.target
```

launchd equivalent for macOS production deploys.

## 5. Restore script

```bash
#!/usr/bin/env bash
# ops/backup/restore.sh — one-liner used in CI drill
set -euo pipefail
DEST_DB=${1?dest db name}
SRC_DIR=${2?dump directory}

createdb "$DEST_DB"
pg_restore --jobs=4 --no-owner --no-acl --dbname="$DEST_DB" "$SRC_DIR"
```

## 6. Migration safety lint

```go
// shared/db/migrations/lint_long_running.go
//go:build migratelint

func main() {
    files := mustGlob("shared/db/migrations/*.sql")
    for _, f := range files {
        body, _ := os.ReadFile(f)
        for _, rule := range rules {
            if hits := rule.match(body); len(hits) > 0 {
                fmt.Fprintf(os.Stderr, "%s: %s\n  hint: %s\n", f, rule.name, rule.hint)
                os.Exit(1)
            }
        }
    }
}

var rules = []rule{
    {
        name: "CREATE INDEX without CONCURRENTLY on hot table",
        match: regexp.MustCompile(`(?im)^\s*CREATE\s+(UNIQUE\s+)?INDEX\s+(?!CONCURRENTLY)`).FindAllIndex,
        hint:  "use CREATE INDEX CONCURRENTLY for tables in: videos, segments, processing_jobs, events",
        scope: hotTables,
    },
    {
        name: "ALTER TABLE … ADD COLUMN NOT NULL without DEFAULT",
        match: regexp.MustCompile(`(?im)ALTER TABLE.*ADD COLUMN.*NOT NULL[^D]*$`).FindAllIndex,
        hint:  "Add column nullable; backfill in a separate migration; then SET NOT NULL.",
    },
    {
        name: "DROP COLUMN on hot table",
        match: regexp.MustCompile(`(?im)ALTER TABLE.*DROP COLUMN`).FindAllIndex,
        hint:  "Two-phase: app stops writing → drop in next release.",
        scope: hotTables,
    },
}
```

`make migrate:lint` runs in PR CI; failure blocks merge.

## 7. Test cases

### TC1 — Restore drill
Nightly CI:
1. Pull last night's dump from backup volume.
2. `restore.sh maktaba_drill /backups/pg-YYYYMMDD`.
3. Run `tests/smoke/catalog_smoke_test.go` against the restored DB:
   - 50 random videos exist.
   - 1,000 random segments exist.
   - Sums of `count(*)` match the source DB's same-day count.

### TC2 — Read-replica search
Compose stack with primary+replica. Configure router with `lag_tolerance=5s`. Run search test from Story 18.2; assert search hits replica (`pg_stat_activity` `query` originates from replica connection).

Inject 30 s lag (`pg_wal_replay_pause`); assert router falls back to primary. Resume; replica lag drops; router resumes routing within 10 s.

### TC3 — Migration size lint
Test fixture migration `9999_create_index.sql` with `CREATE INDEX foo_idx ON videos(title)` (no CONCURRENTLY). Assert `make migrate:lint` exits non-zero with the hint message naming `videos`.

## 8. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 pg_dump slows transcribe writes | story | Cron pinned to 03:00; `--jobs=4` parallelism. Throttle via `nice -n 19` and `ionice -c 3`. |
| EC2 replica lag > 60 s | story | Lag monitor flips routing to primary; alert metric `replica_lag_seconds`. |
| EC3 SQLite path | story | No replica path; backup is `VACUUM INTO '/backup/db-YYYYMMDD.sqlite'`; lag monitor disabled. |
| Restore script run on existing DB | impl | `createdb` fails fast — no destructive clobber. |
| Wal segment retention insufficient | impl | `wal_keep_size=1GB`; catastrophic gap → operator runs `setup-replica.sh` from scratch. |

## 9. Configuration

```yaml
api:
  db:
    primary_dsn: ${PG_PRIMARY_DSN}
    replica_dsn: ${PG_REPLICA_DSN:-}      # empty disables read routing
    replica_lag_tolerance_s: 5
    replica_lag_alert_s: 60
backup:
  out_dir: /var/backups/maktaba
  retention_days: 14
```

## 10. Runbooks

- `docs/runbooks/postgres-replica.md` — setup, validate, promote.
- `docs/runbooks/postgres-backup-restore.md` — daily ops + drill.
- `docs/runbooks/postgres-failover.md` — promoting the replica when primary is down (manual; automated promotion deferred).

## 11. Dependencies

- Story 18.7 (query budgets the replica must also meet).
- Story 21.5 (alerting on `replica_lag_seconds`).
- Epic 22 devops (systemd/launchd packaging).
