package pgbundle

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Epic 49 story 49.2: 16 and older keep <root>/postgres (every cluster made before), 18 and
// later have their own directory.
func TestDataDirFor(t *testing.T) {
	for major, want := range map[string]string{"16": "postgres", "": "postgres", "x": "postgres", "17": "postgres",
		"18": "postgres-18", "19": "postgres-19"} {
		if got := filepath.Base(DataDirFor("/r", major)); got != want {
			t.Errorf("DataDirFor(%q) = %s, want %s", major, got, want)
		}
	}
}

// A payload runs postgres-18/ when it has one, and keeps postgres/ as the previous major's
// programs for the upgrade; one with postgres/ alone runs it and has no previous major.
func TestPayloadHomePrefersTheNewerMajor(t *testing.T) {
	p := t.TempDir()
	server := func(dir string) {
		if err := os.MkdirAll(filepath.Join(p, dir, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, dir, "bin", exe("postgres")), nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := payloadHome(p); got != "" {
		t.Fatalf("no Postgres: %q", got)
	}
	server("postgres")
	if got := payloadHome(p); got != filepath.Join(p, "postgres") || PreviousHome(got) != "" {
		t.Fatalf("postgres/ alone: %q, previous %q", got, PreviousHome(got))
	}
	server("postgres-18")
	got := payloadHome(p)
	if got != filepath.Join(p, "postgres-18") || PreviousHome(got) != filepath.Join(p, "postgres") {
		t.Fatalf("with postgres-18/: %q, previous %q", got, PreviousHome(got))
	}
}

// The hold: recorded with its reason, read back, taken back (twice is fine).
func TestMajorHold(t *testing.T) {
	root := t.TempDir()
	if HeldMajor(root) != "" {
		t.Fatal("held before any hold")
	}
	if err := HoldMajor(root, "no room for the upgraded copy"); err != nil {
		t.Fatal(err)
	}
	if got := HeldMajor(root); got != "no room for the upgraded copy" {
		t.Fatalf("HeldMajor = %q", got)
	}
	if err := ReleaseMajor(root); err != nil || HeldMajor(root) != "" {
		t.Fatalf("ReleaseMajor: %v, still %q", err, HeldMajor(root))
	}
	if err := ReleaseMajor(root); err != nil {
		t.Fatal(err)
	}
}

// Needed: nothing to upgrade without an old cluster; a new cluster with no marker was made
// fresh and stays; a marker with no checkpoint is an upgrade that did not finish.
func TestMajorUpgradeNeeded(t *testing.T) {
	root := t.TempDir()
	u := MajorUpgrade{Old: Bundle{Data: filepath.Join(root, "postgres")}, New: Bundle{Data: filepath.Join(root, "postgres-18")}}
	ctx := context.Background()
	if need, _, err := u.Needed(ctx); err != nil || need {
		t.Fatalf("no old cluster: %v %v", need, err)
	}
	for _, d := range []string{u.Old.Data, u.New.Data} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "PG_VERSION"), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if need, why, err := u.Needed(ctx); err != nil || need || why != "made fresh" {
		t.Fatalf("a fresh new cluster: %v %q %v", need, why, err)
	}
	if err := os.WriteFile(filepath.Join(u.New.Data, UpgradeMarker), []byte(`{"major":"16"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if need, why, err := u.Needed(ctx); err != nil || !need || why != "an upgrade that did not finish" {
		t.Fatalf("an unfinished upgrade: %v %q %v", need, why, err)
	}
}

// PostgreSQL 18 runs with io_method=sync (no I/O worker processes: the idle memory
// budget); 16, which has no such setting, never gets it.
func TestServerArgsIOMethodOn18Only(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a shell script as postgres")
	}
	for major, want := range map[string]bool{"16": false, "18": true} {
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		script := "#!/bin/sh\necho 'postgres (PostgreSQL) " + major + ".7'\n"
		if err := os.WriteFile(filepath.Join(home, "bin", "postgres"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		data := t.TempDir()
		if err := os.WriteFile(filepath.Join(data, "PG_VERSION"), []byte(major+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		args := strings.Join(Bundle{Home: home, Data: data, Port: 5499}.serverArgs(false), " ")
		if got := strings.Contains(args, "io_method=sync"); got != want {
			t.Errorf("PostgreSQL %s: io_method=sync %v, want %v (%s)", major, got, want, args)
		}
	}
}

// A payload with a newer major on a home whose library is in the previous major's cluster:
// the home has a database (setup is done), and Prepare never makes an empty cluster of the
// newer major beside it (only the server's upgrade does).
func TestHasDatabaseAndNoEmptyNewerCluster(t *testing.T) {
	root, payload := t.TempDir(), t.TempDir()
	for _, d := range []string{"postgres", "postgres-18"} {
		if err := os.MkdirAll(filepath.Join(payload, d, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(payload, d, "bin", exe("postgres")), nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	b := Bundle{Home: filepath.Join(payload, "postgres-18"), Data: DataDirFor(root, "18"), Keys: t.TempDir()}
	if b.HasDatabase() {
		t.Fatal("no cluster at all, yet a database")
	}
	old := DataDirFor(root, "16")
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "PG_VERSION"), []byte("16\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !b.HasDatabase() {
		t.Fatal("the 16 cluster waiting for its upgrade is no database")
	}
	err := b.Prepare(context.Background(), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "not upgraded yet") {
		t.Fatalf("Prepare = %v", err)
	}
	if _, err := os.Stat(b.Data); !os.IsNotExist(err) {
		t.Fatalf("Prepare made %s: %v", b.Data, err)
	}
}

// Windows' psql answers with \r\n line ends: the rows are the same as elsewhere (the upgrade's
// last step named a database "postgres\r" on the Windows runner).
func TestRowsOfTakesNoCarriageReturn(t *testing.T) {
	for _, out := range []string{"postgres\nmaktaba\n", "postgres\r\nmaktaba\r\n", "postgres\r\n\r\nmaktaba"} {
		if got := rowsOf(out); len(got) != 2 || got[0] != "postgres" || got[1] != "maktaba" {
			t.Errorf("%q: %q", out, got)
		}
	}
	if got := rowsOf(""); len(got) != 0 {
		t.Errorf("no output: %q", got)
	}
}

// Stopping mid-upgrade (cancelled ctx) skips the deferred 30s oldSQL grant restoration
// immediately because clusterMarker is already gone and Prepare restores grants on the
// next start.
func TestMajorUpgradeDeferredOldSQLSkipsOnCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stand-in for pg_controldata/postgres")
	}
	root := t.TempDir()
	oldHome, newHome := filepath.Join(root, "pg16"), filepath.Join(root, "pg18")
	for _, h := range []string{oldHome, newHome} {
		if err := os.MkdirAll(filepath.Join(h, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	oldData, newData, keys := filepath.Join(root, "postgres"), filepath.Join(root, "postgres-18"), filepath.Join(root, "keys")
	if err := os.MkdirAll(oldData, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(keys, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldData, "PG_VERSION"), []byte("16\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldData, clusterMarker), []byte("rev 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keys, "postgres-"+SuperUser+".password"), []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctrlScript := "#!/bin/sh\necho 'Database cluster state: shut down'\necho 'Database system identifier: 123456789'\necho 'Latest checkpoint location: 0/1'\necho 'Data page checksum version: 1'\n"
	if err := os.WriteFile(filepath.Join(oldHome, "bin", "pg_controldata"), []byte(ctrlScript), 0o755); err != nil {
		t.Fatal(err)
	}
	// postgres stand-in sleeps if ever invoked a second time (deferred restore).
	calls := filepath.Join(root, "pg-calls")
	pgScript := "#!/bin/sh\necho call >> '" + calls + "'\ntrap 'kill $p 2>/dev/null; exit 0' INT TERM\nsleep 30 </dev/null >/dev/null 2>&1 &\np=$!\nwait $p\n"
	if err := os.WriteFile(filepath.Join(oldHome, "bin", "postgres"), []byte(pgScript), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	// psql succeeds on the first call (slotGrantsOffSQL); then Room cancels ctx during the
	// copy phase so Run exits into the deferred cleanup while ctx is already cancelled.
	psqlScript := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(filepath.Join(oldHome, "bin", "psql"), []byte(psqlScript), 0o755); err != nil {
		t.Fatal(err)
	}
	oldCheck := readyCheck
	t.Cleanup(func() { readyCheck = oldCheck })
	readyCheck = func(_ context.Context, _, _, _ string) error {
		if _, err := os.Stat(calls); err != nil {
			return err
		}
		return nil
	}
	u := MajorUpgrade{
		Old:     Bundle{Home: oldHome, Data: oldData, Keys: keys, Port: 54991},
		New:     Bundle{Home: newHome, Data: newData, Keys: keys, Port: 54991},
		NoClone: true,
		Room: func(string, uint64) error {
			cancel()
			return context.Canceled
		},
	}
	var log bytes.Buffer
	start := time.Now()
	_ = u.Run(ctx, &log)
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("cancelled upgrade hung for %s in deferred cleanup:\n%s", took, log.String())
	}
	if !strings.Contains(log.String(), "left for its next start") {
		t.Fatalf("deferred cleanup did not skip on cancellation:\n%s", log.String())
	}
	raw, _ := os.ReadFile(calls)
	if strings.Count(string(raw), "call") != 1 {
		t.Fatalf("old postgres started %d times, want 1 (deferred oldSQL must not run):\n%s", strings.Count(string(raw), "call"), string(raw))
	}
}

func TestMajorUpgradeKeepsExistingClusterAndSweeps(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stand-in for pg_controldata")
	}
	root := t.TempDir()
	u := MajorUpgrade{Old: Bundle{Data: filepath.Join(root, "postgres"), Home: filepath.Join(root, "pg16")},
		New: Bundle{Data: filepath.Join(root, "postgres-18")}}
	write := func(path, value string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(u.Old.Data, "PG_VERSION"), "16", 0o600)
	write(filepath.Join(u.New.Data, "PG_VERSION"), "18", 0o600)
	write(u.Old.Bin("pg_controldata"), "#!/bin/sh\nprintf '%s\\n' 'Database cluster state: shut down' 'Database system identifier: 2'\n", 0o700)
	write(filepath.Join(u.New.Data, UpgradeMarker), `{"system_identifier":"1","checkpoint":"0/1"}`, 0o600)
	leftovers := []string{u.New.Data + ".upgrading", u.New.Data + ".stale", filepath.Join(root, ".pg_upgrade-123")}
	for _, dir := range leftovers {
		write(filepath.Join(dir, "file"), "leftover", 0o600)
	}
	active := filepath.Join(root, ".pg_upgrade-456")
	save := lookProcess
	t.Cleanup(func() { lookProcess = save })
	lookProcess = func(pid int) (string, bool) {
		switch pid {
		case 101:
			return "postgres", true
		case 102:
			return "unrelated", true
		default:
			return "", false
		}
	}
	write(filepath.Join(active, "postmaster.pid"), "101\n", 0o600)
	write(filepath.Join(leftovers[0], "postmaster.pid"), "102\n", 0o600)
	unrelated := filepath.Join(root, ".pg_upgrade-notes")
	write(filepath.Join(unrelated, "file"), "keep", 0o600)
	link := filepath.Join(root, ".pg_upgrade-789")
	if err := os.Symlink(u.New.Data, link); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := u.Run(context.Background(), &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "level=warn "+u.Old.Data+" has a different system ID") ||
		!strings.Contains(log.String(), "keeping the existing postgres-18 cluster") {
		t.Fatal(log.String())
	}
	for _, dir := range leftovers {
		if _, err := os.Lstat(dir); !os.IsNotExist(err) {
			t.Errorf("%s: want removed, got %v", dir, err)
		}
	}
	for _, path := range []string{u.New.Data, u.Old.Data, active, unrelated, link} {
		if _, err := os.Lstat(path); err != nil {
			t.Errorf("%s should remain: %v", path, err)
		}
	}
}

func TestUpgradeSweepContinuesPastUnremovableStaging(t *testing.T) {
	root := t.TempDir()
	u := MajorUpgrade{New: Bundle{Data: filepath.Join(root, "postgres-18")}}
	staging := u.New.Data + ".upgrading"
	if err := os.WriteFile(staging, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	leftovers := []string{u.New.Data + ".stale", filepath.Join(root, ".pg_upgrade-123")}
	for _, dir := range leftovers {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var log bytes.Buffer
	u.sweepUpgradeLeftovers(&log)
	if !strings.Contains(log.String(), staging) {
		t.Fatal(log.String())
	}
	for _, dir := range leftovers {
		if _, err := os.Lstat(dir); !os.IsNotExist(err) {
			t.Errorf("%s should be removed: %v", dir, err)
		}
	}
}

func TestUpgradeSweepKeepsWorkWhileStagingRuns(t *testing.T) {
	root := t.TempDir()
	u := MajorUpgrade{New: Bundle{Data: filepath.Join(root, "postgres-18")}}
	staging := u.New.Data + ".upgrading"
	stale := u.New.Data + ".stale"
	work := filepath.Join(root, ".pg_upgrade-123")
	for _, dir := range []string{staging, stale, work} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(staging, "postmaster.pid"), []byte("101\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	save := lookProcess
	t.Cleanup(func() { lookProcess = save })
	lookProcess = func(pid int) (string, bool) { return "postgres", pid == 101 }
	u.sweepUpgradeLeftovers(&bytes.Buffer{})
	for _, dir := range []string{staging, work} {
		if _, err := os.Lstat(dir); err != nil {
			t.Errorf("%s should remain: %v", dir, err)
		}
	}
	if _, err := os.Lstat(stale); !os.IsNotExist(err) {
		t.Errorf("stale should be removed: %v", err)
	}
}
