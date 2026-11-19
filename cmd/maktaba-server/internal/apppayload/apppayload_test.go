package apppayload

import (
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
