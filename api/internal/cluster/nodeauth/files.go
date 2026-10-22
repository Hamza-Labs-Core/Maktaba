package nodeauth

import (
	"os"
	"path/filepath"
)

// writeFile replaces a node state file with b: written beside it, flushed to the disk,
// then renamed over it (and the rename flushed too, where the system can: the streaming
// ledger's way), so a crash or a power cut leaves the old file or the new one, never a
// torn one.
func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync() // the rename itself (not on every system)
		_ = d.Close()
	}
	return nil
}
