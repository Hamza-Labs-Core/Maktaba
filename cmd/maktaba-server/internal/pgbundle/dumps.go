package pgbundle

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Dump is one finished dump in a backups folder (the Backups page lists them).
type Dump struct {
	Name string `json:"name"`
	// Kind: nightly | update | failed | replaced | other.
	Kind  string    `json:"kind"`
	Bytes int64     `json:"bytes"`
	At    time.Time `json:"at"`
}

// kindOf names a dump's kind from its name.
func kindOf(name string) string {
	switch dumpKind(name) {
	case backupPrefix:
		return "nightly"
	case updateDumpPrefix:
		return "update"
	case "failed-":
		return "failed"
	case ReplacedDumpPrefix:
		return "replaced"
	}
	return "other"
}

// ListDumps is every finished dump in dir (*.dump: a file, or a directory-format dump's
// folder), newest first; none when dir is not there.
func ListDumps(dir string) ([]Dump, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []Dump{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Dump{}
	for _, e := range entries {
		if !isDump(e) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		p := filepath.Join(dir, e.Name())
		out = append(out, Dump{Name: e.Name(), Kind: kindOf(e.Name()), Bytes: DumpBytes(p), At: info.ModTime().UTC()})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out, nil
}

// ErrNotADump is a name that is not a finished dump of the folder.
var ErrNotADump = errors.New("not a dump of the backups folder")

// dumpPath is dir/name when name is a plain *.dump name (no folder, no "..") of a dump
// there (a file, or a directory-format dump's folder; never a link).
func dumpPath(dir, name string) (string, error) {
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") ||
		!strings.HasSuffix(name, backupSuffix) {
		return "", fmt.Errorf("%w: %q", ErrNotADump, name)
	}
	p := filepath.Join(dir, name)
	info, err := os.Lstat(p)
	if err != nil {
		return "", err
	}
	if !IsDump(info) {
		return "", fmt.Errorf("%w: %q", ErrNotADump, name)
	}
	return p, nil
}

// DeleteDump removes the dump name from dir.
func DeleteDump(dir, name string) error {
	p, err := dumpPath(dir, name)
	if err != nil {
		return err
	}
	return RemoveDump(p)
}

// MoveDumps moves every finished dump of from (and the record of the pre-update
// dumps to keep) into to: renamed on the same disk, else copied, compared by size
// and removed. A dump to already has stays as it is. It returns how many it moved.
func MoveDumps(from, to string) (int, error) {
	dumps, err := ListDumps(from)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(to, 0o700); err != nil {
		return 0, err
	}
	names := []string{}
	for _, d := range dumps {
		names = append(names, d.Name)
	}
	if _, err := os.Stat(filepath.Join(from, keepFile)); err == nil {
		names = append(names, keepFile)
	}
	moved := 0
	for _, n := range names {
		src, dst := filepath.Join(from, n), filepath.Join(to, n)
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		if err := os.Rename(src, dst); err != nil {
			if err := copyDump(src, dst); err != nil {
				return moved, fmt.Errorf("move %s to %s: %w", src, to, err)
			}
			if err := RemoveDump(src); err != nil {
				return moved, err
			}
		}
		if n != keepFile {
			moved++
		}
	}
	return moved, nil
}

// copyDump copies the dump src to dst (through dst.partial, then moved into place): a
// file, or a directory-format dump's folder file by file, each copy's size checked.
func copyDump(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return copySame(src, dst)
	}
	part := dst + ".partial"
	_ = os.RemoveAll(part)
	if err := os.MkdirAll(part, 0o700); err != nil {
		return err
	}
	ents, err := os.ReadDir(src)
	if err == nil {
		for _, e := range ents {
			if !e.Type().IsRegular() {
				continue // a directory-format dump is flat: toc.dat and one file per table
			}
			if err = copySame(filepath.Join(src, e.Name()), filepath.Join(part, e.Name())); err != nil {
				break
			}
		}
	}
	if err != nil {
		_ = os.RemoveAll(part)
		return err
	}
	_ = os.Chtimes(part, info.ModTime(), info.ModTime())
	return os.Rename(part, dst)
}

// copySame copies the file src to dst (through dst.partial) and checks the copy's size.
func copySame(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // the backups folder's own file
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	part := dst + ".partial"
	out, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) //nolint:gosec // the new backups folder
	if err != nil {
		return err
	}
	n, err := io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n != info.Size() {
		err = fmt.Errorf("copied %d of %d bytes", n, info.Size())
	}
	if err != nil {
		_ = os.Remove(part)
		return err
	}
	_ = os.Chtimes(part, info.ModTime(), info.ModTime())
	return os.Rename(part, dst)
}
