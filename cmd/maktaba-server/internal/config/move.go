package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// errKept: the new place already holds something else, so the old one stays where
// it is (a move or a copy never replaces a file and never merges two folders).
var errKept = errors.New("already exists")

// Beside a file being copied (to another volume, or as a copy that leaves the
// original), and beside a folder moving to another volume file by file.
const (
	partSuffix   = ".maktaba-part"
	movingSuffix = ".maktaba-moving"
)

// mover moves (MigrateMacDirs) or copies (MigrateWindowsDirs) an install's files
// into this platform's paths. rename is os.Rename; tests replace it to act out a
// move between two volumes.
type mover struct {
	rename func(from, to string) error
}

var osMover = mover{rename: os.Rename}

// crossDevice reports a rename that failed because from and to are on different
// volumes (a roaming profile on another drive or a server, a folder linked to
// another disk).
func crossDevice(err error) bool { return errors.Is(err, errCrossDevice) }

// file moves one file (or link) to to and reports whether it moved. A to that holds
// the same bytes is a move that stopped before it removed from: from goes now. A to
// that holds other bytes is errKept. Between volumes the file is copied beside to,
// flushed to disk, compared with from (size and bytes) and renamed into place
// before from is removed, so a move cut short never leaves a part of a file under
// either name, and the next start finishes it.
func (m mover) file(from, to string) (bool, error) {
	info, err := os.Lstat(from)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if t, err := os.Lstat(to); err == nil {
		if os.SameFile(info, t) { // one file under two names (%APPDATA% is %LOCALAPPDATA%)
			return false, nil
		}
		same, err := sameFile(from, to)
		if err != nil {
			return false, err
		}
		if !same {
			return false, errKept
		}
		return true, os.Remove(from)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return false, err
	}
	err = m.rename(from, to)
	if err == nil {
		return true, nil
	}
	if !crossDevice(err) {
		if !exists(from) && exists(to) { // another start moved it first
			return false, nil
		}
		return false, err
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(from)
		if err != nil {
			return false, err
		}
		if err := os.Symlink(target, to); err != nil {
			return false, err
		}
	case info.Mode().IsRegular():
		if err := m.copyVerified(from, to, info); err != nil {
			return false, err
		}
	default:
		return false, fmt.Errorf("%s is not a file or a folder", from)
	}
	return true, os.Remove(from)
}

// copy copies one file to to and leaves from where it is (MigrateWindowsDirs: an
// older version of the install, or another process, may still read it). It reports
// whether it copied. A to with the same bytes is a copy made before; one with other
// bytes is errKept (never replaced). A link is copied as the file it names. Like a
// move between volumes, the copy is written beside to, flushed, compared with from
// and renamed into place, so to is only ever whole and a copy cut short is made
// again by the next start.
func (m mover) copy(from, to string) (bool, error) {
	info, err := os.Stat(from)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("%s is not a file", from)
	}
	if _, err := os.Lstat(to); err == nil {
		same, err := sameBytes(from, to)
		if err != nil {
			return false, err
		}
		if !same {
			return false, errKept
		}
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return false, err
	}
	return true, m.copyVerified(from, to, info)
}

// copyVerified copies from into to: into to+partSuffix, flushed to disk, compared
// with from, then renamed to to (on to's own volume).
func (m mover) copyVerified(from, to string, info fs.FileInfo) error {
	part := to + partSuffix
	err := copyFlushed(from, part, info)
	if err == nil {
		var same bool
		if same, err = sameBytes(from, part); err == nil && !same {
			err = fmt.Errorf("the copy of %s differs from it", from)
		}
	}
	if err == nil {
		err = m.rename(part, to)
	}
	if err != nil {
		_ = os.Remove(part)
		return err
	}
	syncDir(filepath.Dir(to))
	return nil
}

// dir moves a folder to to and reports whether it moved. to must not exist yet (an
// empty folder there is taken); anything else there is errKept. Between volumes it
// moves file by file (file), with a note beside to while it does, so the next start
// finishes a move that stopped half way and only then removes from's folders.
func (m mover) dir(from, to string) (bool, error) {
	note := to + movingSuffix
	if !exists(note) {
		info, err := os.Lstat(from)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !info.IsDir() { // a link to a folder moves as the link
			return m.file(from, to)
		}
		if t, err := os.Lstat(to); err == nil {
			if os.SameFile(info, t) { // one folder under two names
				return false, nil
			}
			if !t.IsDir() || os.Remove(to) != nil { // only an empty folder is taken
				return false, errKept
			}
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
			return false, err
		}
		err = m.rename(from, to)
		if err == nil {
			return true, nil
		}
		if !crossDevice(err) {
			return false, err
		}
		if err := writeFlushed(note, []byte(from+"\n")); err != nil {
			return false, err
		}
	}
	if err := m.tree(from, to); err != nil {
		return false, err
	}
	return true, os.Remove(note)
}

// tree moves what is left in from into to, file by file, then removes from's
// emptied folders.
func (m mover) tree(from, to string) error {
	if _, err := os.Lstat(from); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	var dirs []string
	err := filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(to, rel)
		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			dirs = append(dirs, p)
			return os.MkdirAll(dst, info.Mode().Perm()|0o700)
		}
		if _, err := m.file(p, dst); err != nil {
			return fmt.Errorf("move %s to %s: %w", p, dst, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Remove(dirs[i]); err != nil {
			return err
		}
	}
	return nil
}

// copyFlushed copies a file's bytes, mode and time, and flushes the copy to disk.
func copyFlushed(from, to string, info fs.FileInfo) error {
	in, err := os.Open(from) //nolint:gosec // the install's own files
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.Remove(to); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm()) //nolint:gosec // beside the install's own file
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(to, info.ModTime(), info.ModTime())
}

// writeFlushed writes a small file and flushes it to disk.
func writeFlushed(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) //nolint:gosec // beside the install's own folder
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
	return f.Close()
}

// syncDir flushes a folder's entries (a rename into it) to disk where the OS can
// (Windows cannot open a folder for that; NTFS journals the rename).
func syncDir(dir string) {
	if f, err := os.Open(dir); err == nil { //nolint:gosec // the install's own folder
		_ = f.Sync()
		_ = f.Close()
	}
}

// sameFile: a and b are two links to the same target, or two files with the same
// bytes.
func sameFile(a, b string) (bool, error) {
	ia, err := os.Lstat(a)
	if err != nil {
		return false, err
	}
	ib, err := os.Lstat(b)
	if err != nil {
		return false, err
	}
	if ia.Mode()&fs.ModeSymlink != 0 || ib.Mode()&fs.ModeSymlink != 0 {
		ta, errA := os.Readlink(a)
		tb, errB := os.Readlink(b)
		return errA == nil && errB == nil && ta == tb, nil
	}
	if !ia.Mode().IsRegular() || !ib.Mode().IsRegular() {
		return false, nil
	}
	return sameBytes(a, b)
}

// sameBytes compares two files' size and bytes.
func sameBytes(a, b string) (bool, error) {
	fa, err := os.Open(a) //nolint:gosec // the install's own files
	if err != nil {
		return false, err
	}
	defer func() { _ = fa.Close() }()
	fb, err := os.Open(b) //nolint:gosec // the install's own files
	if err != nil {
		return false, err
	}
	defer func() { _ = fb.Close() }()
	sa, err := fa.Stat()
	if err != nil {
		return false, err
	}
	sb, err := fb.Stat()
	if err != nil {
		return false, err
	}
	if sa.Size() != sb.Size() {
		return false, nil
	}
	ba, bb := make([]byte, 64<<10), make([]byte, 64<<10)
	for {
		na, errA := io.ReadFull(fa, ba)
		nb, errB := io.ReadFull(fb, bb)
		if !bytes.Equal(ba[:na], bb[:nb]) {
			return false, nil
		}
		endA := errors.Is(errA, io.EOF) || errors.Is(errA, io.ErrUnexpectedEOF)
		endB := errors.Is(errB, io.EOF) || errors.Is(errB, io.ErrUnexpectedEOF)
		if errA != nil && !endA {
			return false, errA
		}
		if errB != nil && !endB {
			return false, errB
		}
		if endA || endB {
			return endA == endB, nil
		}
	}
}
