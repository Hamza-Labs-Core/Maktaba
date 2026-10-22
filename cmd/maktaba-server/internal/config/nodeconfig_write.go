package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// maktaba-server writes node.json too (a switch of the home, a standby taking over, the
// home's database CA): the same way as its other writer, the pipeline
// (cluster/config.py _write), so the two never undo each other and a node's private key
// never gets its folder's ACL. One write at a time (lockNodeConfig: the lock the pipeline
// takes), through a temp file that is the user's alone from the moment it exists, put in
// place keeping the file's ACL (ReplaceFileW on Windows), with the pipeline's names for
// what a write that could not finish leaves (every reader takes those as the node config
// while node.json is missing or broken, and the pipeline's next load puts them back).

// nodeConfigLockWait is how long a write waits for another process's write of the same
// node config (the pipeline's _LOCK_WAIT_SEC).
var nodeConfigLockWait = 10 * time.Second

// busyTries and busyWait: a file another process has open (a reader, Python, Go or
// PowerShell) cannot be replaced or opened for a moment; a write tries again this often
// (the pipeline's _BUSY_TRIES, _BUSY_SLEEP).
const (
	busyTries = 20
	busyWait  = 5 * time.Millisecond
)

// nodeConfigLeftoverName is a name beside path for one write's backup of the old file
// (".bak") or temp file with the new one (".tmp"), as the pipeline names them
// (_leftover_name): the config's name, the time the write started, the process and 8
// random hex.
func nodeConfigLeftoverName(path, ext string) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return filepath.Join(filepath.Dir(path),
		fmt.Sprintf(".%s-%d-%d-%s%s", filepath.Base(path), time.Now().Unix(), os.Getpid(), hex.EncodeToString(b[:]), ext))
}

// nodeConfigLeftovers are the leftovers beside the node config at path
// (isNodeConfigLeftover), whole or not.
func nodeConfigLeftovers(path string) []string {
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && isNodeConfigLeftover(filepath.Base(path), e.Name()) {
			out = append(out, filepath.Join(filepath.Dir(path), e.Name()))
		}
	}
	return out
}

// WriteNodeConfig changes the node config at path: with the write lock held
// (lockNodeConfig), the fields of the file that holds it (nodeConfigFile; any encoding a
// reader reads, withoutBOM) are read, edit changes them, and they are written through a
// temp file the user's alone from the moment it exists, flushed, then put in place
// (replaceNodeConfig). A node config that is there but cannot be read is never written
// over with less (the error is returned); a broken one is replaced by the edit's fields.
func WriteNodeConfig(path string, edit func(fields map[string]json.RawMessage) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	unlock, err := lockNodeConfig(path, nodeConfigLockWait)
	if err != nil {
		return err
	}
	defer unlock()
	fields := map[string]json.RawMessage{}
	held := nodeConfigFile(path, os.ReadFile)
	b, err := readWhenFree(held)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return fmt.Errorf("node config %s: %w", held, err)
	default:
		if json.Unmarshal(withoutBOM(b), &fields) != nil {
			fields = map[string]json.RawMessage{} // broken: nothing of it to keep
		}
	}
	if err := edit(fields); err != nil {
		return err
	}
	out, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return err
	}
	return replaceNodeConfig(path, append(out, '\n'))
}

// SetNodeConfigAside moves the node config at path (the file that holds it,
// nodeConfigFile) to aside with the write lock held, and removes what a write that could
// not finish left of it: a reader would take that as the node config again, and the
// pipeline's next load would put it back (the pipeline's set_aside, a re-join). A node
// config already moved there is no error.
func SetNodeConfigAside(path, aside string) error {
	unlock, err := lockNodeConfig(path, nodeConfigLockWait)
	if err != nil {
		return err
	}
	defer unlock()
	if err := os.Rename(nodeConfigFile(path, os.ReadFile), aside); err != nil &&
		(!errors.Is(err, fs.ErrNotExist) || !isFile(aside)) {
		return err
	}
	for _, p := range nodeConfigLeftovers(path) {
		_ = os.Remove(p)
	}
	return nil
}

// readWhenFree reads path, trying again for a moment while another process holds it
// open without sharing it (ReplaceFileW, a rename; on Windows only).
func readWhenFree(path string) ([]byte, error) {
	for left := busyTries - 1; ; left-- {
		b, err := os.ReadFile(path) //nolint:gosec // this machine's node config
		if err == nil || !isBusy(err) || left == 0 {
			return b, err
		}
		time.Sleep(busyWait)
	}
}

// writeSynced writes b to f, flushes it to disk and closes it.
func writeSynced(f *os.File, b []byte) error {
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
