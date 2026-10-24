package pgbundle

import (
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

// PruneBackups keeps, in a backups folder, the newest keep pre-update dumps (pre-*.dump,
// keepPath always) and the newest keep dumps of failed updates (failed-*.dump), and
// removes the folders of rehearsals a crash left (.rehearse-*, older than an hour): each
// holds a whole database.
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
		if e.IsDir() {
			if strings.HasPrefix(name, ".rehearse-") && time.Since(info.ModTime()) > staleRehearsal {
				if err := os.RemoveAll(filepath.Join(dir, name)); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			continue
		}
		if !strings.HasSuffix(name, backupSuffix) {
			continue
		}
		for _, prefix := range []string{updateDumpPrefix, "failed-"} {
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
			if err := os.Remove(d.path); err != nil && !os.IsNotExist(err) && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
