package pgbundle

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// keepFile in a backups folder holds rules.updates.backups_keep as the home's last
// committed update saw it: the dump pruning at the next start keeps as many (the server
// reads no rules before its database step).
const keepFile = "backups-keep"

// staleRehearsal is how old a rehearsal's folder must be before it counts as one a crash
// left (a rehearsal runs minutes, and never while the server starts).
const staleRehearsal = time.Hour

// SetBackupsKeep records rules.updates.backups_keep for the dump pruning (n < 1: none).
func SetBackupsKeep(dir string, n int) error {
	if n < 1 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, keepFile), []byte(strconv.Itoa(n)+"\n"), 0o600)
}

// BackupsKeep is how many dumps of each kind the folder keeps: rules.updates.backups_keep
// as the last committed update recorded it, else def.
func BackupsKeep(dir string, def int) int {
	b, err := os.ReadFile(filepath.Join(dir, keepFile))
	if err != nil {
		return def
	}
	if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && n >= 1 {
		return n
	}
	return def
}

// ReplacedDumpPrefix starts the name of a dump of the database a restore by hand replaced
// (replaced-<version>-<utc>.dump: story 35.5 AC8).
const ReplacedDumpPrefix = "replaced-"

// PruneBackups keeps, in a backups folder, the newest keep pre-update dumps (pre-*.dump,
// keepPath always), the newest keep dumps of failed updates (failed-*.dump) and as many of
// databases a restore replaced (replaced-*.dump), and removes the folders of rehearsals a
// crash left (.rehearse-*) and the dumps one cut short (*.partial), older than an hour:
// each holds up to a whole database.
func PruneBackups(dir, keepPath string, keep int) error {
	keep = max(keep, 1)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	type dump struct {
		path string
		mod  time.Time
	}
	kinds := map[string][]dump{}
	var firstErr error
	for _, e := range entries {
		name := e.Name()
		info, err := e.Info()
		if err != nil {
			continue // gone meanwhile
		}
		stale := strings.HasPrefix(name, ".rehearse-") && e.IsDir() && time.Since(info.ModTime()) > staleRehearsal ||
			strings.Contains(name, backupSuffix+".partial") && time.Since(lastWrite(filepath.Join(dir, name), info)) > staleRehearsal
		if stale {
			if err := os.RemoveAll(filepath.Join(dir, name)); err != nil && firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !isDump(e) {
			continue
		}
		for _, prefix := range []string{updateDumpPrefix, "failed-", ReplacedDumpPrefix} {
			if strings.HasPrefix(name, prefix) {
				kinds[prefix] = append(kinds[prefix], dump{filepath.Join(dir, name), info.ModTime()})
			}
		}
	}
	for _, dumps := range kinds {
		sort.SliceStable(dumps, func(i, j int) bool {
			if dumps[i].path == keepPath || dumps[j].path == keepPath {
				return dumps[i].path == keepPath
			}
			return dumps[i].mod.After(dumps[j].mod)
		})
		for _, d := range dumps[min(keep, len(dumps)):] {
			if err := RemoveDump(d.path); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// lastWrite is when path (info) or, for a folder, any file in it last changed: a dump
// being written changes one of its files, not its folder, for as long as a table takes.
func lastWrite(path string, info fs.FileInfo) time.Time {
	last := info.ModTime()
	if !info.IsDir() {
		return last
	}
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if fi, err := d.Info(); err == nil && fi.ModTime().After(last) {
			last = fi.ModTime()
		}
		return nil
	})
	return last
}
