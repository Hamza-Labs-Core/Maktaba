//go:build windows

package config

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// icaclsEntries are the ACEs icacls lists for path, one per line.
func icaclsEntries(t *testing.T, path string) []string {
	t.Helper()
	out, err := exec.Command("icacls", path).Output() //nolint:gosec // the test's temp file
	if err != nil {
		t.Fatalf("icacls %s: %v", path, err)
	}
	var aces []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(strings.Replace(line, path, "", 1))
		if strings.HasPrefix(line, "Successfully processed") {
			break
		}
		if line != "" {
			aces = append(aces, line)
		}
	}
	return aces
}

// usersOnly: the ACL grants this user, explicitly, and no one else.
func usersOnly(aces []string) bool {
	return len(aces) == 1 && strings.Contains(strings.ToLower(aces[0]), strings.ToLower(os.Getenv("USERNAME"))) &&
		!strings.Contains(aces[0], "(I)")
}

func grantAuthenticatedUsers(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("icacls", dir, "/grant", "*S-1-5-11:(OI)(CI)(M)").CombinedOutput(); err != nil { //nolint:gosec // the test's temp folder
		t.Fatalf("icacls: %v %s", err, out)
	}
}

// The node config's temp file is the user's alone from the moment it exists, before a
// byte of the secret is in it, even in a folder that grants Authenticated Users (a second
// drive), and no other handle can be opened on it while it is written; a new node.json
// keeps that ACL. One already there is the user's alone after the write too, whatever it
// had: the folder's inherited ACL (what #178's writer left), or a wider one set by hand.
func TestNodeConfigWriteIsTheUsersAlone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Maktaba")
	grantAuthenticatedUsers(t, dir)
	path := filepath.Join(dir, "node.json")

	f, tmp, err := newNodeConfigTemp(path)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(tmp); err != nil || info.Size() != 0 {
		t.Fatalf("the temp file: %v %v", info, err)
	}
	if aces := icaclsEntries(t, tmp); !usersOnly(aces) {
		t.Fatalf("the temp file before its first byte: %q", aces)
	}
	if g, err := os.Open(tmp); err == nil { //nolint:gosec // the test's temp file
		_ = g.Close()
		t.Fatal("another handle opened the temp file while it is written")
	} else if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("another handle: %v, want a sharing violation", err)
	}
	_ = f.Close()
	_ = os.Remove(tmp)

	if err := WriteNodeConfig(path, addCA); err != nil {
		t.Fatal(err)
	}
	if aces := icaclsEntries(t, path); !usersOnly(aces) {
		t.Fatalf("a new node.json: %q", aces)
	}

	// what #178's writer left: node.json renamed into place with the folder's ACL
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	writeAt(t, path, `{"node_id":"n1","private_key":"k","database_url":"postgres://h","joined_at":"then"}`)
	if aces := icaclsEntries(t, path); usersOnly(aces) || !strings.Contains(strings.Join(aces, "|"), "Authenticated Users:(I)(M)") {
		t.Fatalf("the test's node.json has the folder's ACL: %q", aces)
	}
	if err := WriteNodeConfig(path, addCA); err != nil {
		t.Fatal(err)
	}
	if aces := icaclsEntries(t, path); !usersOnly(aces) {
		t.Fatalf("a node.json with the folder's ACL after a write: %q", aces)
	}
	if got := readFields(t, path); got["joined_at"] != "then" || got["database_ca"] != "PEM" {
		t.Fatalf("its fields: %v", got)
	}

	user := os.Getenv("USERNAME")
	for _, args := range [][]string{{"/inheritance:r", "/grant:r", user + ":F"}, {"/grant", "*S-1-5-32-545:(R)"}} {
		if out, err := exec.Command("icacls", append([]string{path}, args...)...).CombinedOutput(); err != nil { //nolint:gosec // the test's temp file
			t.Fatalf("icacls: %v %s", err, out)
		}
	}
	if err := WriteNodeConfig(path, addCA); err != nil {
		t.Fatal(err)
	}
	if aces := icaclsEntries(t, path); !usersOnly(aces) {
		t.Fatalf("a node.json with a wider ACL set by hand, after a write: %q", aces)
	}
	if names := dirNames(t, dir); strings.Join(names, ",") != "node.json" {
		t.Fatalf("left beside it: %v", names)
	}
}

// Writes of one node config run one at a time with the pipeline's (its lock on
// .node.json.lock): while another process holds it, a write waits and then fails, and
// once it is free the write goes through and no lock file is left.
func TestNodeConfigWritesWaitForTheLock(t *testing.T) {
	wait := nodeConfigLockWait
	nodeConfigLockWait = 50 * time.Millisecond
	t.Cleanup(func() { nodeConfigLockWait = wait })
	dir := filepath.Join(t.TempDir(), "Maktaba")
	path := filepath.Join(dir, "node.json")
	writeAt(t, path, `{"node_id":"n1","private_key":"k"}`)
	held, err := layout.LockFile(filepath.Join(dir, ".node.json.lock")) // the pipeline's write
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteNodeConfig(path, addCA); err == nil || !strings.Contains(err.Error(), "another write") {
		layout.Unlock(held)
		t.Fatalf("while the lock is held: %v", err)
	}
	layout.Unlock(held)
	if err := WriteNodeConfig(path, addCA); err != nil {
		t.Fatal(err)
	}
	if names := dirNames(t, dir); strings.Join(names, ",") != "node.json" {
		t.Fatalf("after (the lock file goes when nobody holds it): %v", names)
	}
}

// A ReplaceFileW that fails is settled as the pipeline settles it: half way (1177, the
// old file under the backup name) the old file is put back and the write fails; with the
// old file gone (1176) the new one takes the name; a sharing violation is tried again.
func TestNodeConfigReplaceFailuresAreSettled(t *testing.T) {
	realReplace := replaceFile
	t.Cleanup(func() { replaceFile = realReplace })
	const old = `{"node_id":"n1","private_key":"k","joined_at":"then"}`
	for _, tc := range []struct {
		name    string
		fake    func(calls *int) func(path, tmp, backup string) error
		wantErr bool
		wantCA  bool
	}{
		{"1177 half way", func(*int) func(string, string, string) error {
			return func(path, _, backup string) error {
				if err := os.Rename(path, backup); err != nil {
					return err
				}
				return windows.Errno(1177)
			}
		}, true, false},
		{"1176 with the old file gone", func(*int) func(string, string, string) error {
			return func(path, _, _ string) error {
				if err := os.Remove(path); err != nil {
					return err
				}
				return windows.Errno(1176)
			}
		}, false, true},
		{"a sharing violation, twice", func(calls *int) func(string, string, string) error {
			return func(path, tmp, backup string) error {
				if *calls++; *calls <= 2 {
					return &os.LinkError{Op: "ReplaceFileW", Old: tmp, New: path, Err: windows.ERROR_SHARING_VIOLATION}
				}
				return realReplace(path, tmp, backup)
			}
		}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "Maktaba")
			path := filepath.Join(dir, "node.json")
			writeAt(t, path, old)
			calls := 0
			replaceFile = tc.fake(&calls)
			err := WriteNodeConfig(path, addCA)
			if (err != nil) != tc.wantErr {
				t.Fatalf("write: %v", err)
			}
			got := readFields(t, path)
			if got["joined_at"] != "then" || (got["database_ca"] == "PEM") != tc.wantCA {
				t.Fatalf("node.json after: %v", got)
			}
			if names := dirNames(t, dir); strings.Join(names, ",") != "node.json" {
				t.Fatalf("left beside it: %v", names)
			}
		})
	}
}

// A home's switch sets node.json aside only once no other whole node config is left to
// read: a stale backup a reader holds open is tried again for a moment, and while it
// stays the set-aside fails before anything moves (every reader would take that backup
// as the node config, and the pipeline would put it back). Once it can go, the switch
// sets node.json aside and nothing is read as a node config after it.
func TestSetNodeConfigAsideFailsBeforeLeavingAWholeOne(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Maktaba")
	path, aside := filepath.Join(dir, "node.json"), filepath.Join(dir, "node.json.was-node-1")
	writeAt(t, path, `{"node_id":"n1","private_key":"k","database_url":"postgres://h"}`)
	stale := filepath.Join(dir, ".node.json-1792600000-4242-0badf00d.bak")
	writeAt(t, stale, `{"node_id":"n0","private_key":"k","database_url":"postgres://h"}`)
	held, err := os.Open(stale) //nolint:gosec // the test's temp file: a reader holding it
	if err != nil {
		t.Fatal(err)
	}
	if err := SetNodeConfigAside(path, aside); err == nil {
		_ = held.Close()
		t.Fatal("set aside with a whole backup left to read")
	}
	if got := readFields(t, path); got["node_id"] != "n1" || exists(aside) {
		_ = held.Close()
		t.Fatalf("something moved: node.json %v, aside there %v", got, exists(aside))
	}
	_ = held.Close()
	if err := SetNodeConfigAside(path, aside); err != nil {
		t.Fatal(err)
	}
	if names := dirNames(t, dir); strings.Join(names, ",") != "node.json.was-node-1" {
		t.Fatalf("after: %v", names)
	}
}

// holdUnshared creates the file at path with body and keeps it open without sharing (as
// ReplaceFileW and an antivirus do): no one else can open it until release.
func holdUnshared(t *testing.T, path, body string) (release func()) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	// the handle that makes it, as a write's ReplaceFileW and the pipeline's temp file
	// hold theirs: no sharing from the start
	f, err := CreateUserOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(body); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(path); !isBusy(err) { //nolint:gosec // the test's temp file
		_ = f.Close()
		t.Fatalf("%s can be read while it is held: %v", path, err)
	}
	return func() { _ = f.Close() }
}

// The review's case: a write's backup and temp file held open without sharing, its lock
// taken a moment ago, node.json not there. Past readWait the roaming path is still the
// node config's and reading it is an error that is not "missing": never "not joined".
func TestHeldLeftoversOfAWriteAreNeverNotJoined(t *testing.T) {
	prev := readWait
	readWait = 20 * time.Millisecond
	t.Cleanup(func() { readWait = prev })
	root := t.TempDir()
	env := map[string]string{
		"APPDATA":            filepath.Join(root, "Roaming"),
		"LOCALAPPDATA":       filepath.Join(root, "Local"),
		"MAKTABA_MACHINE_ID": "pc-a",
	}
	getenv := func(k string) string { return env[k] }
	path := filepath.Join(root, "Roaming", "Maktaba", "node.json")
	body := `{"node_id":"n1","private_key":"k","database_url":"postgres://h"}`
	defer holdUnshared(t, nodeConfigLeftoverName(path, ".bak"), body)()
	defer holdUnshared(t, nodeConfigLeftoverName(path, ".tmp"), body)()
	writeAt(t, nodeConfigLockPath(path), "")
	if got := NodeConfigPath(getenv, "windows", root); got != path {
		t.Fatalf("NodeConfigPath = %s, want the roaming node config (%s)", got, path)
	}
	n := ReadNodeConfig(getenv, "windows", root, os.ReadFile)
	if n.Err == nil || errors.Is(n.Err, os.ErrNotExist) || n.Joined() {
		t.Fatalf("read: %+v (err %v), want an error that is not \"missing\"", n, n.Err)
	}
}
