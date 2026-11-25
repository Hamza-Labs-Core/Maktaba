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

// linkDump links the dump from as to (there already: nothing is done), whole or not at all.
func linkDump(from, to string) error {
	if _, err := os.Lstat(to); err == nil {
		return nil
	}
	info, err := os.Lstat(from)
	if err != nil {
		return err
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
		err = filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(from, p)
			if err != nil {
				return err
			}
			if d.IsDir() {
				return os.MkdirAll(filepath.Join(part, rel), 0o700)
			}
			return os.Link(p, filepath.Join(part, rel))
		})
		if err == nil { // its date is the folder's
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

// NewestOfKind reports whether dump name is the newest of its kind in dir: no release removes
// that one for room.
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

// IsNightly reports whether name is one of the scheduled dumps (their own retention goes by
// name, newest first).
func IsNightly(name string) bool { return dumpKind(name) == backupPrefix }

// BackupsHeld reports whether dir's dumps are held.
func BackupsHeld(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, holdFile))
	return err == nil
}
