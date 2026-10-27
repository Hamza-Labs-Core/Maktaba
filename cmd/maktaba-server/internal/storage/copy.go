package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// partSuffix is a file being copied: the copy gets its name only once whole.
const partSuffix = ".maktaba-part"

// topExcluded: rel is (inside) one of the names left out at the top.
func topExcluded(rel string, exclude []string) bool {
	top, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
	for _, x := range exclude {
		if top == x {
			return true
		}
	}
	return false
}

// sizeOf is the files and bytes under dir (links count as files of no size),
// less the names left out at its top; 0 when it is not there.
func sizeOf(dir string, exclude []string) (files int, bytes uint64) {
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // what cannot be read is not counted
		}
		rel, _ := filepath.Rel(dir, p)
		if rel != "." && topExcluded(rel, exclude) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		files++
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				bytes += uint64(info.Size())
			}
		}
		return nil
	})
	return files, bytes
}

// Size is the files and bytes under dir.
func Size(dir string) (files int, bytes uint64) { return sizeOf(dir, nil) }

// mirror makes e.To a copy of e.From: folders made, links made again, each file
// copied unless the same one is there (size and time), into a part file that
// takes the file's name once flushed. Then what is in e.To and not in e.From goes
// (a run cut short, a file the server removed since), except the names at its
// top that are other entries' (the state root's folders, the marker). A Keep
// entry merges: a file there that is newer stays, and nothing else is removed.
func mirror(ctx context.Context, e Entry, report func(uint64)) error {
	if err := os.MkdirAll(e.To, 0o700); err != nil {
		return err
	}
	seen := map[string]bool{}
	err := filepath.WalkDir(e.From, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(e.From, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if topExcluded(rel, e.Exclude) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		seen[rel] = true
		dst := filepath.Join(e.To, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			if t, err := os.Lstat(dst); err == nil && !t.IsDir() {
				if err := os.RemoveAll(dst); err != nil {
					return err
				}
			}
			return os.MkdirAll(dst, info.Mode().Perm()|0o700)
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if cur, err := os.Readlink(dst); err == nil && cur == target {
				return nil
			}
			if err := os.RemoveAll(dst); err != nil {
				return err
			}
			return os.Symlink(target, dst)
		case d.Type().IsRegular():
			if t, err := os.Lstat(dst); err == nil && t.Mode().IsRegular() {
				same := t.Size() == info.Size() && t.ModTime().Unix() == info.ModTime().Unix()
				if same || (e.Keep && t.ModTime().After(info.ModTime())) {
					report(uint64(info.Size()))
					return nil
				}
			}
			if err := copyFile(p, dst, info); err != nil {
				return err
			}
			report(uint64(info.Size()))
			return nil
		}
		return nil // a socket, a device: nothing a copy carries
	})
	if err != nil {
		return err
	}
	return prune(e, seen)
}

// others are the names at the top of e.To that belong to other entries (the
// state root's folders, when they go into the same folder as the data) or to
// the move itself (the marker): not e's to prune or count.
func others(e Entry) []string {
	if e.Name != "data" {
		return nil
	}
	out := append([]string{}, e.Exclude...)
	for _, n := range append([]string{config.StorageMarkerFile}, config.StateRootFolders...) {
		if topExcluded(n, e.Exclude) || !exists(filepath.Join(e.From, n)) {
			out = append(out, n)
		}
	}
	return out
}

// prune removes from e.To what e.From does not have (part files only, for a Keep
// entry), less others(e).
func prune(e Entry, seen map[string]bool) error {
	keep := others(e)
	var gone []string
	err := filepath.WalkDir(e.To, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(e.To, p)
		if err != nil || rel == "." {
			return err
		}
		if strings.HasSuffix(p, partSuffix) {
			gone = append(gone, p)
			return nil
		}
		if e.Keep || seen[rel] {
			return nil
		}
		if !strings.Contains(filepath.ToSlash(rel), "/") && topExcluded(rel, keep) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		gone = append(gone, p)
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, g := range gone {
		if err := os.RemoveAll(g); err != nil {
			return err
		}
	}
	return nil
}

// copyFile copies from into to: into to+partSuffix, flushed, with from's mode and
// time, then renamed over to.
func copyFile(from, to string, info fs.FileInfo) error {
	part := to + partSuffix
	in, err := os.Open(from) //nolint:gosec // the install's own files
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	_ = os.Remove(part)
	out, err := os.OpenFile(part, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm()|0o600) //nolint:gosec // the copy's own folder
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(part)
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(part)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(part)
		return err
	}
	_ = os.Chmod(part, info.Mode().Perm())
	if err := os.Chtimes(part, info.ModTime(), info.ModTime()); err != nil {
		_ = os.Remove(part)
		return err
	}
	if err := os.Rename(part, to); err != nil {
		_ = os.Remove(part)
		return err
	}
	return nil
}

// errNotWhole is a copy that lacks a file or holds another size.
var errNotWhole = errors.New("the copy differs")

// verify checks e's copy: every file of e.From is in e.To with its size (a Keep
// entry's newer file there counts), every link a link; without Keep, e.To holds
// no file e.From does not (less the other entries' names at its top). It returns
// the files it checked.
func verify(e Entry) (int, error) {
	files := 0
	err := filepath.WalkDir(e.From, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(e.From, p)
		if err != nil || rel == "." {
			return err
		}
		if topExcluded(rel, e.Exclude) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		dst := filepath.Join(e.To, rel)
		t, err := os.Lstat(dst)
		if err != nil {
			return fmt.Errorf("%w: %s is missing", errNotWhole, dst)
		}
		switch {
		case d.IsDir():
			if !t.IsDir() {
				return fmt.Errorf("%w: %s is not a folder", errNotWhole, dst)
			}
		case d.Type()&fs.ModeSymlink != 0:
			files++
			if t.Mode()&fs.ModeSymlink == 0 {
				return fmt.Errorf("%w: %s is not a link", errNotWhole, dst)
			}
		case d.Type().IsRegular():
			files++
			info, err := d.Info()
			if err != nil {
				return err
			}
			if !t.Mode().IsRegular() {
				return fmt.Errorf("%w: %s is not a file", errNotWhole, dst)
			}
			if t.Size() != info.Size() && (!e.Keep || !t.ModTime().After(info.ModTime())) {
				return fmt.Errorf("%w: %s holds %d bytes, %s %d", errNotWhole, dst, t.Size(), p, info.Size())
			}
		}
		return nil
	})
	if err != nil || e.Keep {
		return files, err
	}
	srcFiles, _ := sizeOf(e.From, e.Exclude)
	dstFiles, _ := sizeOf(e.To, append(others(e), e.Exclude...))
	if srcFiles != dstFiles {
		return files, fmt.Errorf("%w: %s holds %d files, %s %d", errNotWhole, e.To, dstFiles, e.From, srcFiles)
	}
	return files, nil
}
