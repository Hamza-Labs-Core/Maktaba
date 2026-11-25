package pgbundle

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// holdFile in a backups folder says a restore a person asked for is not done: every dump
// there may be the one it puts back, or the next one tried, so nothing removes any of them
// by itself (PruneBackups, MakeRoom, the scheduled dumps and their pruning), whichever process
// runs it; but the room for the copy of the database it replaces (MakeRoomKeeping). What a
// person deletes by name goes all the same.
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

// ReleaseBackups ends HoldBackups (and KeepForRestore: what an older release removed is back).
func ReleaseBackups(dir string) error {
	if err := ReturnKept(dir); err != nil {
		return err
	}
	if kept := filepath.Join(dir, keptDir); isFolder(kept) {
		if err := os.RemoveAll(kept); err != nil {
			return err
		}
	}
	if err := os.Remove(filepath.Join(dir, holdFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// keptDir in a backups folder holds the dumps a restore on an older release needs, linked (no
// room is taken): a release from before holdFile knows neither it nor the names kept, and
// removes the oldest *.dump of the folder for the room of its own copy, or its retention's.
// Every removal of every release is of a *.dump (or *.dump.partial, .rehearse-*) of the folder
// itself: what is in here is out of its reach.
const keptDir = "being-restored"

// KeptPath is where KeepForRestore keeps the dump name of dir.
func KeptPath(dir, name string) string { return filepath.Join(dir, keptDir, name) }

// OriginalPath is the path in its folder of a dump kept by KeepForRestore (any other: itself).
func OriginalPath(path string) string {
	if d := filepath.Dir(path); filepath.Base(d) == keptDir {
		return filepath.Join(filepath.Dir(d), filepath.Base(path))
	}
	return path
}

// KeepForRestore keeps the dumps names of dir where no release removes them, until
// ReleaseBackups. An error: the disk cannot (it has no hard links).
func KeepForRestore(dir string, names ...string) error {
	for _, n := range names {
		if err := linkDump(filepath.Join(dir, n), KeptPath(dir, n)); err != nil {
			return err
		}
	}
	return nil
}

// KeepOnly leaves nothing kept in dir but the dumps names: the others, which an earlier asked
// restore needed, are put back where they are gone, and hold no room any more.
func KeepOnly(dir string, names ...string) error {
	ents, err := os.ReadDir(filepath.Join(dir, keptDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := ReturnKept(dir); err != nil {
		return err
	}
	keep := keepNames(names)
	for _, e := range ents {
		if !keep(e.Name()) {
			if err := RemoveDump(KeptPath(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// MovedDump is where the dump path is: there, or, when the backups were moved since (MoveDumps),
// in dir under its name.
func MovedDump(dir, path string) string {
	if path == "" {
		return path
	}
	if _, err := os.Lstat(path); err == nil {
		return path
	}
	if info, err := os.Lstat(filepath.Join(dir, filepath.Base(path))); err == nil && IsDump(info) {
		return filepath.Join(dir, filepath.Base(path))
	}
	return path
}

// ReturnKept puts back in dir what KeepForRestore keeps and is there no more.
func ReturnKept(dir string) error {
	if !isFolder(filepath.Join(dir, keptDir)) {
		return nil
	}
	ents, err := os.ReadDir(filepath.Join(dir, keptDir))
	if err != nil {
		return err
	}
	for _, e := range ents {
		if !isDump(e) {
			continue // a link cut short
		}
		if err := linkDump(KeptPath(dir, e.Name()), filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func isFolder(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir()
}

// linkDump links the dump from as to, whole or not at all. There already, nothing is done, but
// for one that is a part of it (a release stopped in the middle of removing it left that): it
// gets the rest.
func linkDump(from, to string) error {
	have, herr := os.Lstat(to)
	info, err := os.Lstat(from)
	if herr == nil && (err != nil || !info.IsDir() || !have.IsDir() || !partOf(to, from)) {
		return nil
	}
	if err != nil {
		return err
	}
	if herr == nil {
		if err := linkFiles(from, to); err != nil {
			return err
		}
		return os.Chtimes(to, info.ModTime(), info.ModTime())
	}
	part := to + ".linking"
	if err := os.RemoveAll(part); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	if !info.IsDir() {
		err = os.Link(from, part)
	} else {
		if err = linkFiles(from, part); err == nil { // its date is the folder's
			err = os.Chtimes(part, info.ModTime(), info.ModTime())
		}
	}
	if err == nil {
		err = os.Rename(part, to)
	}
	if err != nil {
		_ = os.RemoveAll(part)
	}
	return err
}

// linkFiles links the files of the folder from in the folder to, those that are there apart.
func linkFiles(from, to string) error {
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(to, rel), 0o700)
		}
		if err := os.Link(p, filepath.Join(to, rel)); !errors.Is(err, fs.ErrExist) {
			return err
		}
		return nil
	})
}

// partOf reports whether the folder part holds nothing but files of the folder whole (the same
// files, linked).
func partOf(part, whole string) bool {
	files := func(dir string) (map[string]fs.FileInfo, error) {
		out := map[string]fs.FileInfo{}
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			out[rel], err = d.Info()
			return err
		})
		return out, err
	}
	some, err := files(part)
	if err != nil {
		return false
	}
	all, err := files(whole)
	if err != nil {
		return false
	}
	for rel, info := range some {
		if other, ok := all[rel]; !ok || !os.SameFile(info, other) {
			return false
		}
	}
	return true
}

// NewestOfKind reports whether dump name is the newest of its kind in dir.
func NewestOfKind(dir, name string) bool {
	info, err := os.Lstat(filepath.Join(dir, name))
	if err != nil {
		return false
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range ents {
		if e.Name() == name || !isDump(e) || dumpKind(e.Name()) != dumpKind(name) {
			continue
		}
		if i, err := e.Info(); err == nil && !i.ModTime().Before(info.ModTime()) {
			return false
		}
	}
	return true
}

// IsUpdateDump reports whether name is one an update takes first.
func IsUpdateDump(name string) bool { return dumpKind(name) == updateDumpPrefix }

// BackupsHeld reports whether dir's dumps are held.
func BackupsHeld(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, holdFile))
	return err == nil
}
