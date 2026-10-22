package channel

import (
	"os"
	"path/filepath"
	"testing"
)

// Epic 38 story 38.1: on Windows a machine has one node config and it stays where it
// is: the roaming profile's for a machine that joined before (even with a stray file
// in the new place), else %LOCALAPPDATA%\Maktaba\node.json (the same rule as
// maktaba-server and the pipeline).
func TestNodeConfigPathWindows(t *testing.T) {
	base := t.TempDir()
	env := map[string]string{"APPDATA": filepath.Join(base, "Roaming"), "LOCALAPPDATA": filepath.Join(base, "Local"),
		"MAKTABA_MACHINE_ID": "this-pc"} // never this PC's real MachineGuid
	getenv := func(k string) string { return env[k] }
	newPath := filepath.Join(base, "Local", "Maktaba", "node.json")
	oldPath := filepath.Join(base, "Roaming", "Maktaba", "node.json")
	write := func(p string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := NodeConfigPath(getenv, "windows", base); got != newPath {
		t.Fatalf("not joined: %s, want %s", got, newPath)
	}
	write(oldPath)
	if got := NodeConfigPath(getenv, "windows", base); got != oldPath {
		t.Fatalf("joined before Epic 38: %s, want %s", got, oldPath)
	}
	write(newPath)
	if got := NodeConfigPath(getenv, "windows", base); got != oldPath {
		t.Fatalf("both: %s, want the original %s", got, oldPath)
	}
	delete(env, "LOCALAPPDATA")
	delete(env, "APPDATA")
	if got, want := NodeConfigPath(getenv, "windows", base), filepath.Join(base, "AppData", "Local", "Maktaba", "node.json"); got != want {
		t.Fatalf("no LOCALAPPDATA: %s, want %s", got, want)
	}
}

// The roaming profile's node config is the PC's that claimed it (machine_id): an
// unclaimed one and this PC's own are read; another PC's is skipped, so this PC is not
// that node (not joined), and its own join writes the local file (the same rule as
// maktaba-server and the pipeline).
func TestNodeConfigClaimedByAnotherPC(t *testing.T) {
	base := t.TempDir()
	env := map[string]string{"APPDATA": filepath.Join(base, "Roaming"), "LOCALAPPDATA": filepath.Join(base, "Local"),
		"MAKTABA_MACHINE_ID": "this-pc"}
	getenv := func(k string) string { return env[k] }
	oldPath := filepath.Join(base, "Roaming", "Maktaba", "node.json")
	newPath := filepath.Join(base, "Local", "Maktaba", "node.json")
	write := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ body, want string }{
		{`{"node_id":"n","database_url":"postgres://h"}`, oldPath},                         // unclaimed
		{`{"node_id":"n","database_url":"postgres://h","machine_id":"This-PC"}`, oldPath},  // this PC's
		{`{"node_id":"n","database_url":"postgres://h","machine_id":"other-pc"}`, newPath}, // another PC's
	} {
		write(oldPath, c.body)
		if got := NodeConfigPath(getenv, "windows", base); got != c.want {
			t.Errorf("%s: %s, want %s", c.body, got, c.want)
		}
	}
	// another PC's: this PC is not that node
	if role, id := DetectRole(getenv, "windows", base, os.ReadFile); role != RoleHome || id != "" {
		t.Fatalf("another PC's node config: %s %s", role, id)
	}
}
