# Implementation Plan — Story 24.5 Backup and restore

> Companion to [story-24-05-backup-restore.md](story-24-05-backup-restore.md).
> Story states *what* and *why*; this plan states *how*.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Backup runner | `maktaba-api backup run` — scheduled by the **single shared cron daemon** (see §2.9). `tasks.backup_cron` config key; default daily at 03:00 in UTC (configurable). |
| Postgres | `pg_dump --format=custom`. Parallel jobs via `--jobs=$N`. CRC trailer appended to the dump for tail-truncation detection (see §2.5). |
| SQLite | `VACUUM INTO`. |
| Restore | `maktaba-api restore --from <file> [--then-migrate] [--allow-partial]` runs `pg_restore` with `--clean --if-exists`. |
| Retention | `tasks.backup_retention_days` (default 14); GC runs after each successful backup. |
| `.maktaba/` sidecar dir | **Not backed up.** Rebuilt from the DB by plan-24-02's `maktaba-pipeline reprocess --rebuild-sidecars` after a restore. Sidecars are a pure projection of `transcript_segments` + media metadata. |
| Chroma | Not backed up; rebuild path documented and tested via `reprocess --from-stage index`. |
| Caches | Not backed up. Documented. |
| Cron daemon | One process: `robfig/cron/v3` embedded in `maktaba-api serve`. Owned by this plan; plan-24-07 (integrity verification) **registers handlers** with this same daemon — it does not run a second daemon. |
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
// CRCTrailerSize is the fixed-size footer the runner appends to every
// pg_dump file: 8 bytes of magic ("MKTBCRC1") + 4 bytes BE CRC32 of all
// preceding bytes. `pg_restore --list` does NOT detect tail truncation
// (it reads the table-of-contents at the head of the file), so we add
// our own footer check.
const CRCTrailerSize = 12
const CRCTrailerMagic = "MKTBCRC1"

func appendCRCTrailer(file string) error {
    f, err := os.OpenFile(file, os.O_RDWR, 0)
    if err != nil { return err }
    defer f.Close()
    h := crc32.NewIEEE()
    if _, err := io.Copy(h, f); err != nil { return err }
    sum := h.Sum32()
    if _, err := f.Seek(0, io.SeekEnd); err != nil { return err }
    if _, err := f.Write([]byte(CRCTrailerMagic)); err != nil { return err }
    return binary.Write(f, binary.BigEndian, sum)
}

func verify(file string) error {
    if strings.HasSuffix(file, ".sqlite") {
        // sqlite_check via integrity pragma on the dump.
        return runSqliteIntegrityCheck(file)
    }
    // 1. CRC trailer check — catches tail truncation.
    if err := verifyCRCTrailer(file); err != nil {
        return fmt.Errorf("crc trailer: %w", err)
    }
    // 2. Strip the trailer in-place for `pg_restore --list` (the trailer
    //    is invisible to libpq's custom format reader, but be defensive).
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

The `pg_dump --format=custom` output does not have a tail-truncation
signature in the libpq custom format, so we rely on our 12-byte trailer
added immediately after the dump completes (before atomic rename). The
restore path strips the trailer (it is not part of `pg_dump`'s on-wire
format) before invoking `pg_restore`.

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
type RestoreOptions struct {
    File          string
    ThenMigrate   bool   // run migrate up after restore (default true)
    AllowPartial  bool   // proceed when pg_restore reports row failures (default false)
    Confirm       string // must be "RESTORE" to proceed
}

func (r *Restorer) Restore(ctx context.Context, opts RestoreOptions) error {
    if opts.Confirm != "RESTORE" {
        return fmt.Errorf("--confirm RESTORE required")
    }
    if r.dialect == "postgres" {
        if err := r.restorePg(ctx, opts); err != nil { return err }
    } else {
        if err := r.restoreSqlite(ctx, opts.File); err != nil { return err }
    }
    if opts.ThenMigrate {
        // Pinned restore order: restore, THEN migrate. Plan-24-06 EC2
        // depends on this — if the dump is older-schema, this brings
        // it forward to current.
        return r.migrateUp(ctx)
    }
    return nil
}

func (r *Restorer) restorePg(ctx context.Context, opts RestoreOptions) error {
    args := []string{
        "--dbname=" + r.dsn,
        "--clean", "--if-exists",
        "--no-owner", "--no-privileges",
        "--jobs=" + strconv.Itoa(r.parallelJobs),
    }
    if opts.AllowPartial {
        args = append(args, "--exit-on-error=false")
    } else {
        args = append(args, "--exit-on-error")
    }
    args = append(args, opts.File)
    cmd := exec.CommandContext(ctx, "pg_restore", args...)
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
**Restore order is pinned by `--then-migrate`** (default on): restore →
migrate up. Plan-24-06 (Disaster Recovery) calls into this with
`--then-migrate` so a restored older-schema dump is brought forward
automatically. **`--allow-partial`** lets DR runs proceed past
per-table errors (plan-24-06 EC1); off by default.

### 2.8 CLI surface

```
maktaba-api backup run                # one-shot; exits non-zero on failure
maktaba-api backup snapshot           # alias for "run"; documented for admins
maktaba-api backup list               # show backups + sizes + ages
maktaba-api restore --from <file>     # destructive; requires --confirm
```

The `restore` command refuses without `--confirm RESTORE` (a Story
23.6-style confirm).

### 2.9 Cron daemon (single, shared)

The runner is invoked by **one** internal cron scheduler
(`github.com/robfig/cron/v3`) that lives inside `maktaba-api serve`.
This is the same daemon that plan-24-07 (integrity verification)
registers handlers with — there is no second scheduler. The cron
binary owns the daemon; other plans pass it function references:

```go
// api/internal/cron/daemon.go — owned by plan-24-05.
//
// Timezone defaults to UTC. Operators override via [backup] timezone in
// config; the parser uses cron.ParseStandard for "5-field" expressions
// (no seconds; a deliberate v1 simplification).
func New(cfg *config.Config) (*cron.Cron, error) {
    loc, err := time.LoadLocation(cfg.Backup.Timezone) // default "UTC"
    if err != nil { return nil, err }
    return cron.New(cron.WithLocation(loc)), nil
}

// In maktaba-api serve startup:
sched, err := cron.New(cfg)
if err != nil { return err }
// 24-05 backup entry:
sched.AddFunc(cfg.Tasks.BackupCron, func() {
    res, err := backupRunner.Run(ctx)
    if err != nil {
        slog.Error("backup_failed", "err", err)
        // Alert via the audit log + a metric. Documented EC2.
        return
    }
    audit.Write(ctx, audit.Event{
        Category:  audit.CategoryAdmin,
        Event:     "backup.run",
        ActorUser: nil, // system actor
        Payload:   map[string]any{"file": res.File, "size": res.SizeBytes}})
    if removed, err := gc(cfg.Tasks.BackupRoot, cfg.Tasks.BackupRetentionDays); err == nil {
        slog.Info("backup_gc", "removed", removed)
    }
})
// 24-07 registers its weekly integrity handler against the SAME sched:
integrity.RegisterCron(sched, cfg)
sched.Start()
```

**Timezone.** Defaults to `UTC` so behavior is identical across
deployment regions. Operators override via `[backup] timezone = "America/New_York"`
in the TOML config; the value is parsed by Go's `time.LoadLocation`.

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
