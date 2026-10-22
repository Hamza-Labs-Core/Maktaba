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
	if role, id, _ := DetectRole(getenv, "windows", base, os.ReadFile); role != RoleHome || id != "" {
		t.Fatalf("another PC's node config: %s %s", role, id)
	}
}

// A claim whose ReplaceFileW failed with 1177 and whose putting back failed too leaves
// the roaming node.json under its backup name and the claimed copy as a temp file: it
// is still this PC's roaming config, read as the pipeline's next load puts it back, and
// this host stays a node (the same rule as maktaba-server and the pipeline). Another
// PC's leftovers are that PC's.
func TestNodeConfigLeftByAFailedClaim(t *testing.T) {
	base := t.TempDir()
	env := map[string]string{"APPDATA": filepath.Join(base, "Roaming"), "LOCALAPPDATA": filepath.Join(base, "Local"),
		"MAKTABA_MACHINE_ID": "this-pc"} // never this PC's real MachineGuid
	getenv := func(k string) string { return env[k] }
	dir := filepath.Join(base, "Roaming", "Maktaba")
	oldPath, newPath := filepath.Join(dir, "node.json"), filepath.Join(base, "Local", "Maktaba", "node.json")
	bak := filepath.Join(dir, ".node.json-1792600000-4242-0badf00d.bak")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(p, body string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(bak, `{"node_id":"n1","private_key":"k","database_url":"postgres://h"}`)
	write(filepath.Join(dir, ".node.json-1792600000-4242-0badf00d.tmp"),
		`{"node_id":"n1","private_key":"k","database_url":"postgres://h","machine_id":"this-pc"}`)
	if got := NodeConfigPath(getenv, "windows", base); got != oldPath {
		t.Fatalf("left by a failed claim: %s, want the roaming config %s", got, oldPath)
	}
	if got := NodeConfigFile(oldPath, os.ReadFile); got != bak {
		t.Fatalf("read from %s, want the backup %s (the old file first)", got, bak)
	}
	if role, id, _ := DetectRole(getenv, "windows", base, os.ReadFile); role != RoleNode || id != "n1" {
		t.Fatalf("still a node: %s %s", role, id)
	}
	if err := os.Remove(filepath.Join(dir, ".node.json-1792600000-4242-0badf00d.tmp")); err != nil {
		t.Fatal(err)
	}
	write(bak, `{"node_id":"n9","private_key":"k","database_url":"postgres://h","machine_id":"other-pc"}`)
	if got := NodeConfigPath(getenv, "windows", base); got != newPath {
		t.Fatalf("another PC's leftovers: %s, want %s", got, newPath)
	}
	if role, _, _ := DetectRole(getenv, "windows", base, os.ReadFile); role != RoleHome {
		t.Fatalf("another PC's leftovers: %s", role)
	}
}

// A node.json saved with a UTF-8 BOM (Notepad, PowerShell 5.1) is read past it, as the
// pipeline reads it: it is the node config (never a backup beside it), and its owner is
// read too. The broken node.json the pipeline keeps aside (node.json.broken-*) is never
// read as the node config (the same rule as maktaba-server and the pipeline).
func TestNodeConfigWithABOMAndAKeptBrokenOne(t *testing.T) {
	base := t.TempDir()
	env := map[string]string{"APPDATA": filepath.Join(base, "Roaming"), "LOCALAPPDATA": filepath.Join(base, "Local"),
		"MAKTABA_MACHINE_ID": "this-pc"} // never this PC's real MachineGuid
	getenv := func(k string) string { return env[k] }
	dir := filepath.Join(base, "Roaming", "Maktaba")
	oldPath, newPath := filepath.Join(dir, "node.json"), filepath.Join(base, "Local", "Maktaba", "node.json")
	bak := filepath.Join(dir, ".node.json-1792600000-4242-0badf00d.bak")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(p, body string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const bom = "\xef\xbb\xbf"
	write(oldPath, bom+`{"node_id":"n2","private_key":"k","database_url":"postgres://two"}`)
	write(bak, `{"node_id":"n1","private_key":"k","database_url":"postgres://one"}`)
	if got := NodeConfigFile(oldPath, os.ReadFile); got != oldPath {
		t.Fatalf("a BOM'd node.json is whole: read from %s", got)
	}
	if role, id, _ := DetectRole(getenv, "windows", base, os.ReadFile); role != RoleNode || id != "n2" {
		t.Fatalf("a BOM'd node.json: %s %s", role, id)
	}
	// PowerShell 5.1's Out-File and > save UTF-16 (little-endian, with its BOM)
	utf16le := "\xff\xfe"
	for _, c := range `{"node_id":"n3","private_key":"k","database_url":"postgres://three"}` {
		utf16le += string([]byte{byte(c), 0})
	}
	write(oldPath, utf16le)
	if role, id, _ := DetectRole(getenv, "windows", base, os.ReadFile); role != RoleNode || id != "n3" {
		t.Fatalf("a UTF-16 node.json: %s %s", role, id)
	}
	write(oldPath, bom+`{"node_id":"n2","private_key":"k","database_url":"postgres://two","machine_id":"other-pc"}`)
	if got := NodeConfigPath(getenv, "windows", base); got != newPath {
		t.Fatalf("another PC's, past its BOM: %s, want %s", got, newPath)
	}
	for _, p := range []string{oldPath, bak} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "node.json.broken-1792600000-3fa1"), `{"node_id":"n3","private_key":"k","database_url":"postgres://three"}`)
	if got := NodeConfigPath(getenv, "windows", base); got != newPath {
		t.Fatalf("a kept broken node.json is no node config: %s, want %s", got, newPath)
	}
	if role, _, _ := DetectRole(getenv, "windows", base, os.ReadFile); role != RoleHome {
		t.Fatalf("a kept broken node.json is no node config: %s", role)
	}
}
