package pgbundle

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// holdFile in a backups folder says a restore a person asked for is not done: every dump
// there may be the one it puts back, or the next one tried, so nothing removes any of them
// by itself (PruneBackups, MakeRoom, the scheduled dumps and their pruning), whichever
// process runs it. What a person deletes by name goes all the same.
const holdFile = "restore-hold"

// HoldBackups keeps every dump in dir until ReleaseBackups.
func HoldBackups(dir string) error {
	if BackupsHeld(dir) {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, holdFile), []byte("a restore is not done: no backup here is removed until it is\n"), 0o600)
}

// ReleaseBackups ends HoldBackups.
func ReleaseBackups(dir string) error {
	if err := os.Remove(filepath.Join(dir, holdFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// BackupsHeld reports whether dir's dumps are held.
func BackupsHeld(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, holdFile))
	return err == nil
}
