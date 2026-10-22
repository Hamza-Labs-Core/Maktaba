//go:build !windows

package config

import (
	"errors"
	"io/fs"
	"os"
)

// renameNoReplace renames from to to unless to exists: a hard link (which fails on
// an existing to) and then the old name goes. On a file system without hard links
// it renames when to is still free. The error for an existing to matches
// fs.ErrExist.
func renameNoReplace(from, to string) error {
	err := os.Link(from, to)
	if err == nil {
		return os.Remove(from)
	}
	if errors.Is(err, fs.ErrExist) {
		return err
	}
	if _, serr := os.Lstat(to); serr == nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: fs.ErrExist}
	}
	return os.Rename(from, to)
}

// restrictToUser: the copy keeps its mode (0600 for a key, copyFlushed), which is
// what limits it to its user here.
func restrictToUser(string) error { return nil }

// machineGUID: Windows' id of this PC (ThisMachineID); none here.
func machineGUID() string { return "" }
