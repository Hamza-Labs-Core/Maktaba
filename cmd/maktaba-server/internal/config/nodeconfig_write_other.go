//go:build !windows

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// lockNodeConfig: no lock off Windows, as in the pipeline (cluster/config.py
// _one_at_a_time): a rename is atomic, the last write wins, and nothing is put back.
func lockNodeConfig(string, time.Duration) (func(), error) { return func() {}, nil }

// newNodeConfigTemp creates the temp file of one write beside path
// (nodeConfigLeftoverName) with mode 0600 and O_EXCL, the user's alone from the moment it
// exists (the pipeline's _new_temp).
func newNodeConfigTemp(path string) (*os.File, string, error) {
	for range 8 {
		tmp := nodeConfigLeftoverName(path, ".tmp")
		f, err := CreateUserOnly(tmp)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		return f, tmp, nil
	}
	return nil, "", fmt.Errorf("no free temp file name beside %s", path)
}

// CreateUserOnly creates the file at path for writing, which must not be there yet (an
// error matching fs.ErrExist when it is), the user's alone from the moment it exists:
// mode 0600 and O_EXCL. A secret the server keeps (the node config, a switch's records
// and identity) is written to one and renamed into place.
func CreateUserOnly(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // the server's own files
}

// MakeUserOnly makes the file at path the user's alone when it is not: mode 0600 for a
// secret an older version (or a hand) left readable by others.
func MakeUserOnly(path string) error {
	if ok, err := IsUserOnly(path); err != nil || ok {
		return err
	}
	return os.Chmod(path, 0o600)
}

// IsUserOnly: the file at path is this user's alone (no group or other bits in its
// mode).
func IsUserOnly(path string) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return fi.Mode().Perm()&0o077 == 0, nil
}

// replaceNodeConfig puts body in place of the node config at path: a temp file, flushed,
// renamed over it (atomic), the folder synced and limited to the user (0700, as the
// pipeline keeps it).
func replaceNodeConfig(path string, body []byte) error {
	dir := filepath.Dir(path)
	_ = os.Chmod(dir, 0o700)
	f, tmp, err := newNodeConfigTemp(path)
	if err != nil {
		return err
	}
	if err := writeSynced(f, body); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	syncDir(dir)
	return nil
}

// isBusy: never off Windows (a file another process has open can be read and renamed).
func isBusy(error) bool { return false }
