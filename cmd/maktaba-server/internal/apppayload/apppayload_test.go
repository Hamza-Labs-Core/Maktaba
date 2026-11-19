package apppayload

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b  string
		want  int
		known bool
	}{
		{"v0.3.0", "v0.2.0", 1, true},
		{"0.2.0", "v0.2.0", 0, true},
		{"v0.3.0-rc.1", "v0.3.0", -1, true},
		{"v0.3.0-rc.2", "v0.3.0-rc.1", 1, true},
		{"v0.3.0-beta.9", "v0.3.0-rc.1", -1, true},
		{"v1.10.0", "v1.9.9", 1, true},
		{"v0.2.0-12-gabc1234", "v0.2.0", 0, false}, // git describe: a development build
		{"dev", "v0.2.0", 0, false},
	} {
		got, known := CompareVersions(c.a, c.b)
		if got != c.want || known != c.known {
			t.Errorf("CompareVersions(%q, %q) = %d %v, want %d %v", c.a, c.b, got, known, c.want, c.known)
		}
	}
}

func TestOlder(t *testing.T) {
	if !Older("v0.2.0", "v0.3.0") || Older("v0.3.0", "v0.3.0") || Older("v0.3.0", "v0.2.0") {
		t.Error("releases")
	}
	// a development build's order is unknown: never older
	if Older("v0.2.0-5-gdeadbee", "v0.3.0") || Older("v0.2.0", "dev") {
		t.Error("a development build")
	}
}

func TestDatabaseRecord(t *testing.T) {
	state := t.TempDir()
	if _, ok, err := ReadDatabase(state); ok || err != nil {
		t.Fatalf("no record yet: %v %v", ok, err)
	}
	in := Database{Version: "v0.3.0", State: DatabaseMigrating, From: "v0.2.0", Dump: "/b/pre-v0.2.0-to-v0.3.0.dump"}
	if err := WriteDatabase(state, in); err != nil {
		t.Fatal(err)
	}
	got, ok, err := ReadDatabase(state)
	if err != nil || !ok || got.Version != in.Version || got.State != in.State || got.From != in.From ||
		got.Dump != in.Dump || got.At.IsZero() {
		t.Fatalf("record = %+v %v %v", got, ok, err)
	}
	if err := os.WriteFile(DatabasePath(state), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := ReadDatabase(state); ok || err == nil {
		t.Fatalf("a broken record: %v %v", ok, err)
	}
}

func TestConfirmAndRead(t *testing.T) {
	payload, state := t.TempDir(), t.TempDir()
	body := `{"schema":1,"version":"v0.3.0","platform":"windows-x86_64","postgres":{"version":"16.15.0"},` +
		`"python":{"version":"3.12.15"},"pipeline":{"wheel":"w.whl","sha256":"aa","constraints_sha256":"bb"}}`
	if err := os.WriteFile(filepath.Join(payload, "payload.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := Confirmed(state); ok || err != nil {
		t.Fatalf("nothing confirmed yet: %v %v", ok, err)
	}
	if err := Confirm(state, payload); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Confirmed(state)
	if err != nil || !ok || got.Version != "v0.3.0" || got.PostgresMajor() != "16" ||
		got.Pipeline.ConstraintsSHA256 != "bb" || got.Python.Version != "3.12.15" {
		t.Fatalf("confirmed = %+v %v %v", got, ok, err)
	}
}

// Epic 49 story 49.2: the record is the cluster's the install runs on, from its directories:
// database.json for 16 (and every release before), database-pg18.json once postgres-18 holds
// a cluster, database.json again while the upgrade is held.
func TestClusterRecord(t *testing.T) {
	root := t.TempDir()
	if got := filepath.Base(DatabasePath(root)); got != "database.json" {
		t.Fatalf("no cluster: %s", got)
	}
	if err := os.MkdirAll(filepath.Join(root, "postgres-18"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "postgres-18", "PG_VERSION"), []byte("18\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(DatabasePath(root)); got != "database-pg18.json" {
		t.Fatalf("an 18 cluster: %s", got)
	}
	if err := os.MkdirAll(filepath.Join(root, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "state", "postgres-upgrade-held"), []byte("no room\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(DatabasePath(root)); got != "database.json" {
		t.Fatalf("held: %s", got)
	}
}

// Epic 49 story 49.2 (review of #1445): on a home rolled back to 16 after its upgrade, the
// 16 cluster has moved on, so it is the one in use and database.json the record (a restore
// marks the record the release that runs reads). Read from pg_control as it is on disk.
func TestClusterInUseAfterARollback(t *testing.T) {
	root := t.TempDir()
	put := func(rel string, b []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	control := func(sysid, lsn uint64) []byte {
		b := make([]byte, 64)
		binary.LittleEndian.PutUint64(b[0:8], sysid)
		binary.LittleEndian.PutUint64(b[32:40], lsn)
		return b
	}
	put(filepath.Join("postgres-18", "PG_VERSION"), []byte("18\n"))
	put(filepath.Join("postgres", "PG_VERSION"), []byte("16\n"))
	put(filepath.Join("postgres-18", "maktaba-upgraded-from.json"),
		[]byte(`{"major":"16","system_identifier":"7709798569366362372","checkpoint":"0/14C8640"}`))
	put(filepath.Join("postgres", "global", "pg_control"), control(7709798569366362372, 0x14C8640))
	if got := ClusterMajorIn(root); got != "18" {
		t.Fatalf("the 16 cluster as the upgrade left it: %q, want 18", got)
	}
	put(filepath.Join("postgres", "global", "pg_control"), control(7709798569366362372, 0x1_0000_2A40))
	if got := ClusterMajorIn(root); got != "" || filepath.Base(DatabasePath(root)) != "database.json" {
		t.Fatalf("the 16 cluster moved on (a rollback): %q, %s", got, DatabasePath(root))
	}
	put(filepath.Join("postgres", "global", "pg_control"), control(42, 0x1_0000_2A40))
	if got := ClusterMajorIn(root); got != "18" {
		t.Fatalf("another cluster in the old directory: %q, want the upgraded one", got)
	}
	put(filepath.Join("postgres-18", "maktaba-upgraded-from.json"), []byte(`{"major":"16"}`))
	if got := ClusterMajorIn(root); got != "" {
		t.Fatalf("an upgrade that did not finish: %q, want 16's", got)
	}
}
