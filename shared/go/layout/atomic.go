package layout

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
)

// ReplaceFile renames from over to atomically (on Windows retried briefly while
// another process holds to a moment).
func ReplaceFile(from, to string) error { return replaceFile(from, to) }

// WriteFileAtomic writes data to path so a reader (or a power cut) sees
// either the old file or the new one: a temp file next to it, fsync, rename
// over path (Windows: MoveFileExW with REPLACE_EXISTING | WRITE_THROUGH),
// fsync the directory.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	_ = os.Chmod(tmpName, perm)
	if err := beforeRename(path); err != nil { // test seam: a crash before the rename
		return err
	}
	if err := replaceFile(tmpName, path); err != nil {
		return err
	}
	ok = true
	return syncDir(dir)
}

// beforeRename is a test hook (crash simulation); nil in production.
var beforeRename = func(string) error { return nil }

// SHA256File is the hex SHA-256 of a file.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// CopyFile copies src to dst atomically, keeping src's permission bits.
func CopyFile(src, dst string) error {
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if _, err := io.Copy(tmp, in); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		return fail(err)
	}
	_ = os.Chmod(tmpName, st.Mode().Perm()&^0o022) // never group/other-writable
	if err := replaceFile(tmpName, dst); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return syncDir(dir)
}
