//go:build !windows

package layout

import "os"

func replaceFile(from, to string) error { return os.Rename(from, to) }

// syncDir makes a rename durable: fsync the directory entry.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	_ = d.Sync() // some filesystems refuse fsync on a directory; the rename stands
	return nil
}
