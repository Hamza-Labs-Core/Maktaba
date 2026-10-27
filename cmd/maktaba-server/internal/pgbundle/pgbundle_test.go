//go:build !windows

package pgbundle

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// The fakes record their calls in $FAKE_LOG; the fake postgres runs until
// SIGINT/SIGTERM and marks itself up for the stubbed readiness check.
var fakes = map[string]string{
	"initdb": `#!/bin/sh
echo "$@" >> "$FAKE_LOG/initdb.calls"
while [ $# -gt 0 ]; do
  case "$1" in
    -D) D="$2"; shift ;;
    --pwfile=*) cp "${1#--pwfile=}" "$FAKE_LOG/initdb.pw" ;;
  esac
  shift
done
mkdir -p "$D" && echo 17 > "$D/PG_VERSION"
`,
	"postgres": `#!/bin/sh
echo "$@" >> "$FAKE_LOG/postgres.calls"
trap 'rm -f "$FAKE_LOG/up"; echo stopped >> "$FAKE_LOG/postgres.calls"; exit 0' INT TERM
touch "$FAKE_LOG/up"
while :; do sleep 0.05; done
`,
	"psql": `#!/bin/sh
echo "$@" >> "$FAKE_LOG/psql.calls"
echo "$PGPASSWORD" > "$FAKE_LOG/psql.pw"
cat > "$FAKE_LOG/psql.sql"
`,
	"pg_dump": `#!/bin/sh
echo "$@" >> "$FAKE_LOG/pg_dump.calls"
while [ $# -gt 0 ]; do
  case "$1" in
    -f) F="$2"; shift ;;
    --file=*) F="${1#--file=}" ;;
    --compress=*) C="${1#--compress=}" ;;
  esac
  shift
done
for r in $FAKE_REFUSE_COMPRESS; do
  if [ "$C" = "$r" ]; then echo "pg_dump: error: this build does not support compression with $C" >&2; exit 1; fi
done
if [ -n "$FAKE_LOCKED" ] && [ ! -e "$FAKE_LOG/locked.$FAKE_LOCKED" ]; then
  touch "$FAKE_LOG/locked.$FAKE_LOCKED"
  mkdir "$F" && echo half > "$F/toc.dat"
  echo 'pg_dump: error: could not obtain lock on relation "public.videos"' >&2; exit 1
fi
mkdir "$F" && echo toc > "$F/toc.dat" && echo rows > "$F/3456.dat.gz"
`,
}

// fakeBundle builds a Bundle whose bin/ holds the fakes. The tests that use it
// run processes and take longer than the unit tier's per-test cap, so they run in
// the integration tier (without -short), like the supervisor's.
func fakeBundle(t *testing.T) (Bundle, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("runs fake postgres programs; runs without -short")
	}
	root, err := os.MkdirTemp("", "pgb") // short: room for the unix socket path
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	logDir := filepath.Join(root, "log")
	for _, d := range []string{filepath.Join(root, "pg", "bin"), logDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range fakes {
		if err := os.WriteFile(filepath.Join(root, "pg", "bin", name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("FAKE_LOG", logDir)
	prev := readyCheck
	readyCheck = func(context.Context, string, string, string) error {
		if _, err := os.Stat(filepath.Join(logDir, "up")); err != nil {
			return err
		}
		return nil
	}
	t.Cleanup(func() { readyCheck = prev })
	return Bundle{
		Home:    filepath.Join(root, "pg"),
		Data:    filepath.Join(root, "state", "postgres"),
		Keys:    filepath.Join(root, "keys"),
		Backups: filepath.Join(root, "state", "backups"),
		Port:    DefaultPort,
	}, logDir
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func lines(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "\n")
}

func TestPrepareFirstStart(t *testing.T) {
	b, logDir := fakeBundle(t)
	if err := b.Prepare(context.Background(), io.Discard); err != nil {
		t.Fatal(err)
	}
	initdb := read(t, filepath.Join(logDir, "initdb.calls"))
	for _, want := range []string{"-D " + b.Data + ".initdb", "-U " + SuperUser, "--auth=scram-sha-256", "--encoding=UTF8"} {
		if !strings.Contains(initdb, want) {
			t.Errorf("initdb args %q lack %q", initdb, want)
		}
	}
	super := read(t, b.passwordFile(SuperUser))
	if strings.TrimSpace(read(t, filepath.Join(logDir, "initdb.pw"))) != strings.TrimSpace(super) {
		t.Error("initdb did not get the stored superuser password")
	}
	for _, f := range []string{b.passwordFile(SuperUser), b.passwordFile(AppRole)} {
		info, err := os.Stat(f)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("%s: mode %v, %v; want 0600", f, info.Mode().Perm(), err)
		}
	}
	// the bootstrap server: loopback only, the socket in the data dir; stopped after
	pg := read(t, filepath.Join(logDir, "postgres.calls"))
	if !strings.Contains(pg, "-c listen_addresses=127.0.0.1") || !strings.Contains(pg, "-c unix_socket_directories="+b.Data) ||
		!strings.Contains(pg, "-p 54329") || !strings.Contains(pg, "stopped") {
		t.Errorf("bootstrap postgres calls:\n%s", pg)
	}
	sql := read(t, filepath.Join(logDir, "psql.sql"))
	app := strings.TrimSpace(read(t, b.passwordFile(AppRole)))
	if !setsPassword(t, sql, app) {
		t.Errorf("bootstrap SQL does not set the app password as its SCRAM secret:\n%s", sql)
	}
	if strings.Contains(sql, app) {
		t.Error("the bootstrap SQL carries the app password itself: Postgres echoes a failed statement")
	}
	for _, want := range []string{"SET log_min_error_statement = panic;", "CREATE DATABASE maktaba OWNER maktaba", "CREATE EXTENSION IF NOT EXISTS pg_trgm", vectorSQL} {
		if !strings.Contains(sql, want) {
			t.Errorf("bootstrap SQL lacks %q:\n%s", want, sql)
		}
	}
	if strings.TrimSpace(read(t, filepath.Join(logDir, "psql.pw"))) != strings.TrimSpace(super) {
		t.Error("psql must run as the superuser")
	}
	url, err := b.URL()
	if err != nil || url != "postgres://maktaba:"+app+"@127.0.0.1:54329/maktaba?sslmode=disable" {
		t.Fatalf("URL = %q, %v", url, err)
	}
}

func TestPrepareAgainDoesNothing(t *testing.T) {
	b, logDir := fakeBundle(t)
	ctx := context.Background()
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	if n := lines(t, filepath.Join(logDir, "initdb.calls")); n != 1 {
		t.Errorf("initdb ran %d times", n)
	}
	if n := lines(t, filepath.Join(logDir, "psql.calls")); n != 1 {
		t.Errorf("the bootstrap ran %d times", n)
	}
}

func TestPrepareRekeysALostAppPassword(t *testing.T) {
	b, logDir := fakeBundle(t)
	ctx := context.Background()
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	old := read(t, b.passwordFile(AppRole))
	if err := os.Remove(b.passwordFile(AppRole)); err != nil {
		t.Fatal(err)
	}
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	now := strings.TrimSpace(read(t, b.passwordFile(AppRole)))
	if now == strings.TrimSpace(old) || !setsPassword(t, read(t, filepath.Join(logDir, "psql.sql")), now) {
		t.Fatal("a lost app password must be replaced and set on the role")
	}
	if n := lines(t, filepath.Join(logDir, "initdb.calls")); n != 1 {
		t.Errorf("initdb ran %d times", n)
	}
}

func TestPrepareRedoesAnUnfinishedInitdb(t *testing.T) {
	b, logDir := fakeBundle(t)
	// initdb killed half way: its directory beside, an empty data dir
	for _, d := range []string{b.Data + ".initdb", b.Data} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(b.Data+".initdb", "PG_VERSION"), []byte("17\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.Prepare(context.Background(), io.Discard); err != nil {
		t.Fatal(err)
	}
	if n := lines(t, filepath.Join(logDir, "initdb.calls")); n != 1 || !b.Initialized() {
		t.Fatalf("initdb ran %d times, initialized %v", n, b.Initialized())
	}
	if _, err := os.Stat(b.Data + ".initdb"); !os.IsNotExist(err) {
		t.Fatal("the initdb directory must be renamed into place")
	}
}

func TestPrepareRefusesALostSuperuserPassword(t *testing.T) {
	b, logDir := fakeBundle(t)
	ctx := context.Background()
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(b.passwordFile(SuperUser)); err != nil {
		t.Fatal(err)
	}
	err := b.Prepare(ctx, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "is missing but the cluster") {
		t.Fatalf("Prepare = %v, want the lost password reported", err)
	}
	if _, err := os.Stat(b.passwordFile(SuperUser)); !os.IsNotExist(err) {
		t.Fatal("no new superuser password may be invented for an existing cluster")
	}
	if n := lines(t, filepath.Join(logDir, "initdb.calls")); n != 1 {
		t.Errorf("initdb ran %d times", n)
	}
}

func TestPrepareFailsWhenPostgresExits(t *testing.T) {
	b, _ := fakeBundle(t)
	if err := os.WriteFile(b.Bin("postgres"), []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := b.Prepare(context.Background(), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "exited while starting") {
		t.Fatalf("Prepare = %v, want the exit reported", err)
	}
	if _, err := os.Stat(filepath.Join(b.Data, bootstrapMarker)); err == nil {
		t.Fatal("no bootstrap marker after a failed bootstrap")
	}
}

func TestBackupKeepsSeven(t *testing.T) {
	b, logDir := fakeBundle(t)
	if err := b.Prepare(context.Background(), io.Discard); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 1, 3, 30, 0, 0, time.Local)
	var last string
	for i := 0; i < 9; i++ {
		p, err := b.Backup(context.Background(), start.AddDate(0, 0, i))
		if err != nil {
			t.Fatal(err)
		}
		last = p
	}
	names, err := b.backups()
	if err != nil || len(names) != KeepBackups {
		t.Fatalf("backups = %v, %v; want %d", names, err, KeepBackups)
	}
	if names[0] != "maktaba-20261003-033000.dump" || filepath.Join(b.Backups, names[6]) != last {
		t.Fatalf("kept the wrong dumps: %v", names)
	}
	calls := read(t, filepath.Join(logDir, "pg_dump.calls"))
	if !strings.Contains(calls, "--format=directory --jobs="+strconv.Itoa(DumpJobs())+" --compress=zstd:1 ") ||
		!strings.Contains(calls, "-U "+SuperUser) || !strings.Contains(calls, "--dbname=maktaba ") ||
		strings.Contains(calls, "--exclude-table-data") {
		t.Errorf("pg_dump args (a nightly dump is whole, in parallel):\n%s", calls)
	}
	if info, err := os.Stat(last); err != nil || !info.IsDir() || DumpBytes(last) == 0 {
		t.Errorf("the dump %s is no directory-format dump: %v", last, err)
	}
	if got := b.lastBackup(); !got.Equal(start.AddDate(0, 0, 8)) {
		t.Errorf("lastBackup = %v", got)
	}
}

func TestBackupFailureLeavesNoPartial(t *testing.T) {
	b, _ := fakeBundle(t)
	if err := b.Prepare(context.Background(), io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b.Bin("pg_dump"), []byte("#!/bin/sh\necho 'connection refused' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Backup(context.Background(), time.Now()); err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("Backup = %v", err)
	}
	entries, _ := os.ReadDir(b.Backups)
	if len(entries) != 0 {
		t.Fatalf("left %d files behind", len(entries))
	}
}

func TestServerArgsDropTheSocketOnALongPath(t *testing.T) {
	t.Parallel()
	b := Bundle{Data: "/Users/a/Library/Application Support/Maktaba/postgres", Port: DefaultPort}
	if args := strings.Join(b.ServerArgs(), " "); !strings.Contains(args, "unix_socket_directories="+b.Data) {
		t.Fatalf("args = %s", args)
	}
	b.Data = "/" + strings.Repeat("x", 100) + "/postgres"
	if args := b.ServerArgs(); !slices.Contains(args, "unix_socket_directories=") {
		t.Fatalf("a socket path past 103 bytes must be left out: %s", strings.Join(args, " "))
	}
}

func TestDue(t *testing.T) {
	t.Parallel()
	at := func(d, h int) time.Time { return time.Date(2026, 10, d, h, 0, 0, 0, time.Local) }
	for _, c := range []struct {
		now, last time.Time
		want      bool
	}{
		{at(20, 14), time.Time{}, true},
		{at(20, 4), at(20, 3), false},  // today's is done
		{at(20, 4), at(19, 3), true},   // yesterday's only
		{at(20, 2), at(19, 3), false},  // before 03:00: yesterday's counts
		{at(20, 2), at(18, 23), true},  // a Mac asleep last night catches up
		{at(20, 23), at(20, 9), false}, // a catch-up after 03:00 counts for today
	} {
		if got := due(c.now, c.last); got != c.want {
			t.Errorf("due(%v, %v) = %v, want %v", c.now, c.last, got, c.want)
		}
	}
}

func TestResolve(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MAKTABA_CONFIG", "")
	t.Setenv("MAKTABA_POSTGRES_HOME", "")
	tmp, err := filepath.EvalSymlinks(t.TempDir()) // the payload is found through the real path
	if err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(tmp, "server")
	for _, f := range []string{filepath.Join("bin", exe("maktaba-server")), filepath.Join("postgres", "bin", exe("postgres"))} {
		p := filepath.Join(payload, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	exe := filepath.Join(payload, "bin", exe("maktaba-server"))
	cfg := config.Defaults()

	b, ok, err := Resolve(cfg, exe)
	if runtime.GOOS != "darwin" {
		if ok || err != nil {
			t.Fatalf("auto off a Mac: %v, %v", ok, err)
		}
		cfg.Postgres.Enabled = "on"
		b, ok, err = Resolve(cfg, exe)
	}
	if err != nil || !ok || b.Home != filepath.Join(payload, "postgres") || b.Port != DefaultPort ||
		b.Data != filepath.Join(cfg.StateDir(), "postgres") || b.Backups != filepath.Join(cfg.StateDir(), "backups") {
		t.Fatalf("payload postgres: %+v, %v, %v", b, ok, err)
	}

	set := cfg
	set.DatabaseURLSet = true
	if _, ok, _ := Resolve(set, exe); ok {
		t.Error("a [database] url in server.toml must win")
	}
	off := cfg
	off.Postgres.Enabled = "off"
	if _, ok, _ := Resolve(off, exe); ok {
		t.Error("enabled = off must not run it")
	}
	if _, ok, err := Resolve(cfg, filepath.Join(t.TempDir(), "maktaba-server")); ok || (err == nil) != (cfg.Postgres.Enabled != "on") {
		t.Errorf("no payload: ok=%v err=%v", ok, err)
	}
	t.Setenv("MAKTABA_POSTGRES_HOME", filepath.Join(payload, "postgres"))
	if b, ok, _ := Resolve(cfg, ""); !ok || b.Home != filepath.Join(payload, "postgres") {
		t.Errorf("MAKTABA_POSTGRES_HOME not used: %+v %v", b, ok)
	}
	t.Setenv("MAKTABA_POSTGRES_HOME", "")
	t.Setenv("MAKTABA_APP_BUNDLE", payload) // the app's launcher names the payload
	if b, ok, _ := Resolve(cfg, ""); !ok || b.Home != filepath.Join(payload, "postgres") {
		t.Errorf("MAKTABA_APP_BUNDLE not used: %+v %v", b, ok)
	}
	// inside an app's payload "auto" is on, on every system (the Linux app, Epic 39)
	if b, ok, err := Resolve(config.Defaults(), ""); !ok || err != nil || b.Home != filepath.Join(payload, "postgres") {
		t.Errorf("auto in an app payload: %+v %v %v", b, ok, err)
	}
	t.Setenv("MAKTABA_APP_BUNDLE", "")

	// an install already on SQLite keeps it (auto only)
	if cfg.Postgres.Enabled == "" {
		db := strings.TrimPrefix(config.Defaults().Database.URL, "sqlite://")
		if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(db, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, ok, _ := Resolve(cfg, exe); ok {
			t.Error("a SQLite database in use must keep the install on SQLite")
		}
	}
}

// A cluster made before the machines could be served gets the provisioning grants at
// its next start, once; and from then its pg_hba.conf and certificate are Maktaba's.
func TestPrepareAnOlderClusterLetsTheAppRoleProvision(t *testing.T) {
	b, logDir := fakeBundle(t)
	b.LAN, b.TLS = true, true
	ctx := context.Background()
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	if sql := read(t, filepath.Join(logDir, "psql.sql")); !strings.Contains(sql, "CREATEROLE REPLICATION") ||
		!strings.Contains(sql, "CREATE DATABASE maktaba") {
		t.Fatalf("a first start: one session with both:\n%s", sql)
	}
	if hba := read(t, filepath.Join(b.Data, "pg_hba.conf")); strings.Contains(hba, "maktaba_replicas") {
		t.Fatalf("no machine may connect yet, but pg_hba.conf lets them:\n%s", hba)
	}
	// a machine approved (WatchReach): the next start lets the machines in
	if err := b.KeepReach(Reach{Open: true}); err != nil {
		t.Fatal(err)
	}
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	if hba := read(t, filepath.Join(b.Data, "pg_hba.conf")); !strings.Contains(hba, "hostssl maktaba      +maktaba_replicas") {
		t.Fatalf("pg_hba.conf:\n%s", hba)
	}
	if !b.TLSReady() {
		t.Fatal("no server certificate")
	}

	// an older cluster: only the app's bootstrap had run
	if err := os.Remove(filepath.Join(b.Data, clusterMarker)); err != nil {
		t.Fatal(err)
	}
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	sql := read(t, filepath.Join(logDir, "psql.sql"))
	if !strings.Contains(sql, "CREATEROLE REPLICATION") || strings.Contains(sql, "CREATE DATABASE") {
		t.Fatalf("an older cluster: the provisioning grants only:\n%s", sql)
	}
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	if n := lines(t, filepath.Join(logDir, "psql.calls")); n != 2 {
		t.Fatalf("the bootstrap ran %d times, want 2", n)
	}

	// a cluster an earlier release provisioned (its marker names no revision): once more,
	// with the slot functions made the app role's alone
	if err := os.WriteFile(filepath.Join(b.Data, clusterMarker), []byte("2026-10-01T00:00:00Z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	sql = read(t, filepath.Join(logDir, "psql.sql"))
	if !strings.Contains(sql, "REVOKE EXECUTE ON FUNCTION") || !strings.Contains(sql, "pg_logical_slot_get_changes") ||
		strings.Contains(sql, "CREATE DATABASE") {
		t.Fatalf("a cluster provisioned before: the provisioning again:\n%s", sql)
	}
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	if n := lines(t, filepath.Join(logDir, "psql.calls")); n != 3 {
		t.Fatalf("the bootstrap ran %d times, want 3", n)
	}

	// a home provisioned before the bundle had pgvector (rev 3): once more, with the
	// extension made in the app's database when this Postgres has it
	if err := os.WriteFile(filepath.Join(b.Data, clusterMarker), []byte("2026-10-20T00:00:00Z\nrev 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	sql = read(t, filepath.Join(logDir, "psql.sql"))
	if !strings.Contains(sql, vectorSQL) || strings.Contains(sql, "CREATE DATABASE") {
		t.Fatalf("a home of rev 3: pgvector, without the app's bootstrap:\n%s", sql)
	}
	if n := lines(t, filepath.Join(logDir, "psql.calls")); n != 4 {
		t.Fatalf("the bootstrap ran %d times, want 4", n)
	}
}

// pgvector is made only when this Postgres has it (an older bundle, a machine's fetched
// Postgres, must still start), by the superuser: it is not a trusted extension.
func TestVectorOnlyWhenAvailable(t *testing.T) {
	want := "SELECT 'CREATE EXTENSION IF NOT EXISTS vector'\n" +
		"  WHERE EXISTS (SELECT FROM pg_available_extensions WHERE name = 'vector')\\gexec\n"
	if vectorSQL != want {
		t.Fatalf("vectorSQL = %q, want %q", vectorSQL, want)
	}
	for name, sql := range map[string]string{"bootstrap": bootstrapSQL("pw"), "restore": recreateSQL(RestoreOptions{}),
		"cluster": clusterSQL} {
		if !strings.Contains(sql, vectorSQL) {
			t.Errorf("%s SQL lacks pgvector:\n%s", name, sql)
		}
		if strings.Contains(sql, "\nCREATE EXTENSION IF NOT EXISTS vector") {
			t.Errorf("%s SQL makes pgvector unconditionally:\n%s", name, sql)
		}
	}
	if !slices.Contains(Extensions, "vector") {
		t.Errorf("Extensions = %v: a home's dump has vector", Extensions)
	}
}

// A pre-update dump is named after the update, replaces one of that name, and
// keeps the newest of its kind beside the nightly ones.
func TestDumpBeforeUpdate(t *testing.T) {
	b, logDir := fakeBundle(t)
	if err := b.Prepare(context.Background(), io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Backup(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, v := range [][2]string{{"v0.1.0", "v0.2.0"}, {"v0.2.0", "v0.3.0"}, {"", "v0.4.0"}, {"", "v0.4.0"}} {
		p, err := b.DumpBeforeUpdate(context.Background(), v[0], v[1], 2)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
		time.Sleep(20 * time.Millisecond) // a later modification time
	}
	if filepath.Base(paths[2]) != "pre-unknown-to-v0.4.0.dump" || paths[3] != paths[2] {
		t.Fatalf("paths = %v", paths)
	}
	entries, _ := os.ReadDir(b.Backups)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 3 || !strings.HasPrefix(names[0], "maktaba-") || names[1] != "pre-unknown-to-v0.4.0.dump" ||
		names[2] != "pre-v0.2.0-to-v0.3.0.dump" {
		t.Fatalf("backups = %v (the nightly one and the newest two pre-update dumps)", names)
	}
	if n := lines(t, filepath.Join(logDir, "pg_dump.calls")); n != 5 {
		t.Fatalf("pg_dump ran %d times, want 5", n)
	}
	calls := strings.Split(strings.TrimSpace(read(t, filepath.Join(logDir, "pg_dump.calls"))), "\n")
	for i, c := range calls {
		// the nightly one whole; the pre-update ones without the rows the pipeline makes again
		if skips := strings.Contains(c, "--exclude-table-data=public.segment_embeddings"); skips != (i > 0) {
			t.Errorf("call %d: %s", i, c)
		}
	}
}

// A worker that could not lock its table (a session asked for it whole while the server
// served) begins the dump again; a dump that keeps failing otherwise says why.
func TestPgDumpBeginsAgainWhenAWorkerWasLockedOut(t *testing.T) {
	b, logDir := fakeBundle(t)
	if err := b.Prepare(context.Background(), io.Discard); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_LOCKED", "once")
	p, err := b.DumpBeforeUpdate(context.Background(), "v0.1.0", "v0.2.0", 2)
	if err != nil {
		t.Fatal(err)
	}
	if n := lines(t, filepath.Join(logDir, "pg_dump.calls")); n != 2 {
		t.Fatalf("pg_dump ran %d times, want 2", n)
	}
	if got, _ := os.ReadFile(filepath.Join(p, "toc.dat")); string(got) != "toc\n" {
		t.Fatalf("the dump kept the cut one's files: %q", got)
	}
	if err := os.WriteFile(b.Bin("pg_dump"), []byte("#!/bin/sh\necho 'pg_dump: error: could not obtain lock on relation' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := b.DumpBeforeUpdate(context.Background(), "v0.2.0", "v0.3.0", 2); err == nil ||
		!strings.Contains(err.Error(), "could not obtain lock") {
		t.Fatalf("a dump locked out every time = %v", err)
	}
}

// A pg_dump built without zstd (the bundled one: zlib only) is asked for lz4, then gzip;
// what it took is asked first from then on.
func TestPgDumpFallsBackToACompressionItHas(t *testing.T) {
	b, logDir := fakeBundle(t)
	if err := b.Prepare(context.Background(), io.Discard); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_REFUSE_COMPRESS", "zstd:1 lz4")
	for i := 0; i < 2; i++ {
		if _, err := b.DumpBeforeUpdate(context.Background(), "v0.1.0", "v0.2.0", 2); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for _, c := range strings.Split(strings.TrimSpace(read(t, filepath.Join(logDir, "pg_dump.calls"))), "\n") {
		for _, f := range strings.Fields(c) {
			if v, ok := strings.CutPrefix(f, "--compress="); ok {
				got = append(got, v)
			}
		}
	}
	if strings.Join(got, " ") != "zstd:1 lz4 gzip:1 gzip:1" {
		t.Fatalf("compressions asked: %v", got)
	}
	entries, _ := os.ReadDir(b.Backups)
	if len(entries) != 1 || entries[0].Name() != "pre-v0.1.0-to-v0.2.0.dump" {
		t.Fatalf("backups = %v (one dump, no partial left)", entries)
	}
	compressionOf.Delete(b.Bin("pg_dump"))
}

// The bundled Postgres logs in UTC, as every other line of server.log.
func TestServerArgsLogInUTC(t *testing.T) {
	b := Bundle{Data: t.TempDir(), Port: 54329}
	if args := strings.Join(b.ServerArgs(), " "); !strings.Contains(args, "-c log_timezone=UTC") {
		t.Fatalf("args: %s", args)
	}
}
