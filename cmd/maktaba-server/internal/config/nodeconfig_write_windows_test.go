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
// keeps that ACL. One already there keeps its own (ReplaceFileW), as the pipeline's claim
// keeps it.
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

	user := os.Getenv("USERNAME")
	for _, args := range [][]string{{"/inheritance:r", "/grant:r", user + ":F"}, {"/grant", "*S-1-5-32-545:(R)"}} {
		if out, err := exec.Command("icacls", append([]string{path}, args...)...).CombinedOutput(); err != nil { //nolint:gosec // the test's temp file
			t.Fatalf("icacls: %v %s", err, out)
		}
	}
	before := icaclsEntries(t, path)
	if err := WriteNodeConfig(path, addCA); err != nil {
		t.Fatal(err)
	}
	if after := icaclsEntries(t, path); strings.Join(after, "|") != strings.Join(before, "|") {
		t.Fatalf("the replaced node.json's ACL: %q, was %q", after, before)
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
