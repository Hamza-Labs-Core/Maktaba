# Implementation Plan — Story 24.5 Backup and restore

> Companion to [story-24-05-backup-restore.md](story-24-05-backup-restore.md).
> Story states *what* and *why*; this plan states *how*.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Backup runner | `maktaba-api backup run` — scheduled by a `tasks.backup_cron` config key; default daily at 03:00 local. |
| Postgres | `pg_dump --format=custom`. Parallel jobs via `--jobs=$N`. |
| SQLite | `VACUUM INTO`. |
| Restore | `maktaba-api restore --from <file>` runs `pg_restore` with `--clean --if-exists`. |
| Retention | `tasks.backup_retention_days` (default 14); GC runs after each successful backup. |
| Chroma | Not backed up; rebuild path documented and tested. |
| Caches | Not backed up. Documented. |
| Out of scope | DR scenarios (Story 24.6); media volume backups (out of scope by AC5). |

## 1. Architecture diagram

```
   cron (tasks.scheduler) ──► backup runner
                                  │
                  ┌───────────────┼───────────────┐
                  ▼               ▼               ▼
              pg_dump         VACUUM INTO     verify (pg_restore --list)
                  │               │               │
                  ▼               ▼               ▼
              <root>/maktaba-YYYY-MM-DDTHH-MM-SS.{dump,sqlite}
                                  │
                                  ▼
                           gc retention sweep

   restore CLI ──► pg_restore (or file-copy for SQLite)
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `api/internal/backup/backup.go` | Postgres + SQLite backup orchestration. |
| `api/internal/backup/verify.go` | Stream a freshly-written backup through `pg_restore --list`. |
| `api/internal/backup/retention.go` | Retention GC (delete oldest beyond N). |
| `api/internal/backup/restore.go` | Restore flow. |
| `api/cmd/api/backup.go` | `backup run`, `backup snapshot`, `backup list`. |
| `api/cmd/api/restore.go` | `restore --from <file>` CLI. |
| `pipeline/src/maktaba_pipeline/cli/reprocess_index.py` | Helper for "rebuild Chroma" scenario (just calls reprocess --from-stage index). |
| Tests — `tests/integration/backup_*.py`. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/config/config.go` | Adds `tasks.backup_root`, `tasks.backup_retention_days`, `tasks.backup_cron`. |
| `api/cmd/api/main.go` | Registers backup subcommands; embeds the cron daemon under `serve`. |
| `api/internal/http/admin.go` | `POST /api/admin/backup/snapshot` — admin-only, audit category `admin`. |

### 2.3 Postgres backup

`backup.go`:

```go
type Result struct {
    File      string
    SizeBytes int64
    DurationMS int64
    DialecT    string  // "postgres" | "sqlite"
}

func (r *Runner) Run(ctx context.Context) (Result, error) {
    if r.dialect == "postgres" {
        return r.runPg(ctx)
    }
    return r.runSqlite(ctx)
}

func (r *Runner) runPg(ctx context.Context) (Result, error) {
    ts := time.Now().UTC().Format("2006-01-02T15-04-05")
    out := filepath.Join(r.root, fmt.Sprintf("maktaba-%s.dump", ts))

    // pg_dump runs out-of-process; we never embed the password in argv.
    cmd := exec.CommandContext(ctx, "pg_dump",
        "--format=custom", "--compress=6",
        "--jobs=" + strconv.Itoa(r.parallelJobs),
        "--file=" + out,
        r.dsn)
    cmd.Env = append(os.Environ(), "PGPASSFILE=" + r.passfile)
    if err := cmd.Run(); err != nil { return Result{}, err }

    if err := verify(out); err != nil {
        // Don't keep a corrupt backup.
        _ = os.Remove(out)
        return Result{}, fmt.Errorf("verify failed: %w", err)
    }
    info, _ := os.Stat(out)
    return Result{File: out, SizeBytes: info.Size(), DialecT: "postgres"}, nil
}
```

The runner writes to `<backup_root>/.tmp/<file>` and atomic-renames
into place after `verify` succeeds. Atomic rename via Story 24.1's
helper.

### 2.4 SQLite backup

```go
func (r *Runner) runSqlite(ctx context.Context) (Result, error) {
    ts := time.Now().UTC().Format("2006-01-02T15-04-05")
    out := filepath.Join(r.root, fmt.Sprintf("maktaba-%s.sqlite", ts))
    _, err := r.db.ExecContext(ctx, "VACUUM INTO ?", out)
    if err != nil { return Result{}, err }
    info, _ := os.Stat(out)
    return Result{File: out, SizeBytes: info.Size(), DialecT: "sqlite"}, nil
}
```

`VACUUM INTO` produces a defragmented copy on the same filesystem; the
resulting file is consistent without a separate snapshot.

### 2.5 Verify

`verify.go`:

```go
func verify(file string) error {
    if strings.HasSuffix(file, ".sqlite") {
        // sqlite_check via integrity pragma on the dump.
        return runSqliteIntegrityCheck(file)
    }
    cmd := exec.Command("pg_restore", "--list", file)
    var stderr bytes.Buffer
    cmd.Stderr = &stderr
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("pg_restore --list: %w (%s)", err, stderr.String())
    }
    // Sanity check: the toc must contain `videos` and `users` tables.
    out, _ := cmd.Output()
    for _, want := range []string{"TABLE DATA public videos", "TABLE DATA public users"} {
        if !bytes.Contains(out, []byte(want)) {
            return fmt.Errorf("toc missing %q", want)
        }
    }
    return nil
}
```

### 2.6 Retention GC

`retention.go`:

```go
func gc(root string, keepDays int) ([]string, error) {
    cutoff := time.Now().AddDate(0, 0, -keepDays)
    entries, _ := os.ReadDir(root)
    sort.Slice(entries, func(i, j int) bool {
        return entries[i].Name() < entries[j].Name()
    })

    var removed []string
    for _, e := range entries {
        if !strings.HasPrefix(e.Name(), "maktaba-") { continue }
        info, _ := e.Info()
        if info.ModTime().Before(cutoff) {
            // Refuse to delete the most recent backup, even if it's old.
            if isOnlyRecent(entries, e.Name()) { continue }
            _ = os.Remove(filepath.Join(root, e.Name()))
            removed = append(removed, e.Name())
        }
    }
    return removed, nil
}
```

GC also handles the EC3 "target full" case: when free space is below
`gen.SizeBytes`, GC deletes oldest first. If still insufficient, the
new backup is *not* written (refuses to overwrite a recent good
backup).

### 2.7 Restore

`restore.go`:

```go
func (r *Restorer) Restore(ctx context.Context, file string) error {
    if r.dialect == "postgres" {
        return r.restorePg(ctx, file)
    }
    return r.restoreSqlite(ctx, file)
}

func (r *Restorer) restorePg(ctx context.Context, file string) error {
    cmd := exec.CommandContext(ctx, "pg_restore",
        "--dbname=" + r.dsn,
        "--clean", "--if-exists",
        "--no-owner", "--no-privileges",
        "--jobs=" + strconv.Itoa(r.parallelJobs),
        file)
    cmd.Env = append(os.Environ(), "PGPASSFILE=" + r.passfile)
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    return cmd.Run()
}

func (r *Restorer) restoreSqlite(ctx context.Context, file string) error {
    // For SQLite, restore is a file copy + reopen.
    if err := os.Rename(r.dbPath, r.dbPath + ".prev"); err != nil { return err }
    if err := copyFile(file, r.dbPath); err != nil { return err }
    return nil
}
```

The Postgres path runs `--clean --if-exists` so existing tables are
dropped first; combined with `--no-owner --no-privileges`, the dump's
ACL bits don't conflict with the live DB's role names.

### 2.8 CLI surface

```
maktaba-api backup run                # one-shot; exits non-zero on failure
maktaba-api backup snapshot           # alias for "run"; documented for admins
maktaba-api backup list               # show backups + sizes + ages
maktaba-api restore --from <file>     # destructive; requires --confirm
```

The `restore` command refuses without `--confirm RESTORE` (a Story
23.6-style confirm).

### 2.9 Cron daemon

The runner is invoked by an internal cron scheduler (`robfig/cron/v3`):

```go
sched := cron.New()
sched.AddFunc(cfg.Tasks.BackupCron, func() {
    res, err := backupRunner.Run(ctx)
    if err != nil {
        slog.Error("backup_failed", "err", err)
        // Alert via the audit log + a metric. Documented EC2.
        return
    }
    audit.Write(ctx, audit.Event{Category: audit.CategoryAdmin,
        Action: "backup.run", Detail: map[string]any{"file": res.File, "size": res.SizeBytes}})
    if removed, err := gc(cfg.Tasks.BackupRoot, cfg.Tasks.BackupRetentionDays); err == nil {
        slog.Info("backup_gc", "removed", removed)
    }
})
sched.Start()
```

## 3. Test plan

### 3.1 Restore drill (TC1)

| Test | What it pins |
|---|---|
| `TestPostgresBackupRestoreDrill` | Seed a fixture; run `backup run`; drop the DB; run `restore --from <file>`; the catalog smoke test passes. |
| `TestSqliteBackupRestoreDrill` | Same with `VACUUM INTO`. |

### 3.2 Cross-version restore (TC2)

| Test | What it pins |
|---|---|
| `TestRestoreV10IntoV11Server` | A v1.0 `pg_dump` is restored into a fresh v1.1 schema; migrations run forward via `migrate up`; smoke passes. |
| `TestRestoreV11IntoV10Refused` | A v1.1 dump into a v1.0 server (downgrade) refuses with a documented error from the migrate doctor (Story 22.6). |

### 3.3 Chroma rebuild (TC3)

| Test | What it pins |
|---|---|
| `TestChromaRebuildFromTranscripts` | Delete `chroma_dir`; run `maktaba-pipeline reprocess --from-stage index`; semantic search returns results within tolerance of a clean-build baseline. |

### 3.4 Retention + verify

| Test | What it pins |
|---|---|
| `TestRetentionGcKeepsRecent` | 20 backups present, retention=7 — 13 deleted, 7 remain. |
| `TestVerifyDetectsCorrupt` (EC2) | A corrupted `pg_dump` (truncate last 1 KB) → `verify` errors; the file is removed; the backup run logs FAILED. |
| `TestRetentionRefusesToDeleteOnlyRecent` | A single backup file older than retention is *not* deleted (we always keep at least one). |

## 4. Edge cases

| Case | Behaviour | Where pinned |
|---|---|---|
| Backup during burst (EC1) | Configurable cron picks low-traffic window; `backup snapshot` is a one-off ad-hoc command for ops. The runner uses `--jobs=1` by default to keep load predictable; raise via config. | `TestSnapshotOneOff` |
| Backup verify fails (EC2) | The new file is removed; the run is marked failed; the previous (good) backup remains intact; an alert audit row is written. | `TestVerifyDetectsCorrupt` |
| Backup target full (EC3) | GC runs first (oldest first); if still no space, new backup fails *without* deleting the most-recent good backup. | `TestTargetFullPreservesGood` |
| Active write during pg_dump | `pg_dump --format=custom` is a consistent point-in-time snapshot; in-flight writes don't corrupt the dump. | n/a |
| Restore replaces in-flight DB | `--clean --if-exists` drops first; advisory: take the API offline before restore. The CLI requires `--confirm` and prints a warning. | `TestRestoreRequiresConfirm` |
| Schema-mismatch dump (older minor) | `pg_restore --clean` drops; subsequent `migrate up` brings the schema forward. | `TestRestoreV10IntoV11Server` |
| Restore onto host with lower schema (EC3 of 24.6) | Refused by the migrate doctor at boot; documented. | n/a (Story 24.6) |
| Encrypted backup target | Not in scope for v1. The `backup_root` is assumed to be a trusted local directory; ops can mount an encrypted volume there. | n/a |
| Concurrent backup runs | The cron daemon serializes; ad-hoc `backup snapshot` takes an advisory lock to prevent overlap with the scheduled run. | `TestNoConcurrentBackup` |
| Network filesystem as `backup_root` | Supported; the atomic-rename helper from Story 24.1 falls back if rename isn't atomic. | `TestBackupOnSmbShare` |

## 5. Dependencies

| Dep | Version | Why |
|---|---|---|
| `pg_dump`, `pg_restore` | matches Postgres major | Backup/restore. |
| `sqlite3` | bundled | `VACUUM INTO` is a single SQL. |
| `github.com/robfig/cron/v3` | latest | Cron scheduler. |

## 6. Acceptance checklist

**Postgres**
- [ ] `pg_dump --format=custom` daily.
- [ ] Verify via `pg_restore --list` post-write.
- [ ] Atomic rename into final path.

**SQLite**
- [ ] `VACUUM INTO` daily.
- [ ] Restore is a file copy.

**Retention**
- [ ] Default 14 days; configurable.
- [ ] GC oldest-first; never delete the only remaining backup.

**Restore**
- [ ] `restore --from <file>` flag.
- [ ] `--confirm RESTORE` required.
- [ ] Postgres uses `--clean --if-exists`.

**Chroma & caches**
- [ ] Documented as not-backed-up.
- [ ] Rebuild path tested via reprocess.
