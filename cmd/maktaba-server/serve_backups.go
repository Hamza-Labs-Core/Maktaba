package main

// The backups' settings (Epic 36 story 36.16): [backups] in server.toml, read before
// the database step. /config/backups on the supervisor's admin is what the web's
// Backups section reads and changes (the API proxies it for its admins);
// `maktaba-server backups` does the same through it, or on the files with no server
// running.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/apppayload"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/storage"
)

// updateKeep is how many pre-update dumps stay: [backups] update_keep, else
// [postgres] update_dumps (rules.updates.backups_keep, recorded in the backups
// folder, wins over both: pgbundle.BackupsKeep).
func updateKeep(cfg config.Config) int {
	if cfg.Backups.UpdateKeep > 0 {
		return cfg.Backups.UpdateKeep
	}
	return cfg.Postgres.KeepUpdateDumps()
}

// roomOf is the room a dump leaves on its disk ([backups]).
func roomOf(cfg config.Config) pgbundle.RoomPolicy {
	b := cfg.Backups
	return pgbundle.RoomPolicy{ReserveShare: b.ReserveShare(), ReserveMin: b.ReserveMinBytes(), MaxShare: b.MaxShare()}
}

// backupsDirOf is where cfg's dumps go: [backups] dir, else the bundled Postgres's
// <state>/backups, else (a Postgres of the owner's) <data>/backups.
func backupsDirOf(cfg config.Config) string {
	def := filepath.Join(cfg.StateDir(), "backups")
	exe, _ := os.Executable()
	if _, ok, err := pgbundle.Resolve(cfg, exe); err != nil || !ok {
		if cfg.DatabaseURLSet {
			def = filepath.Join(cfg.Server.DataDir, "backups")
		}
	}
	return cfg.BackupsDir(def)
}

// backupsDoc is GET /config/backups' answer.
type backupsDoc struct {
	File          string                `json:"file"`
	Saved         config.BackupSettings `json:"saved"`
	Running       config.BackupSettings `json:"running"`
	RestartNeeded bool                  `json:"restart_needed"`
	CanRestart    bool                  `json:"can_restart"`
	// Dir is where the dumps are (Saved.Dir, else the default); DefaultDir is the default.
	Dir        string `json:"dir"`
	DefaultDir string `json:"default_dir"`
	// Disk is Dir's disk with its free space (nil when it cannot be read).
	Disk  *storage.Disk   `json:"disk,omitempty"`
	Dumps []pgbundle.Dump `json:"dumps"`
	// DumpsBytes is what the dumps take together.
	DumpsBytes int64 `json:"dumps_bytes"`
	// UpdateKeep is how many pre-update dumps stay now (update_keep, the update
	// settings' backups_keep or [postgres] update_dumps).
	UpdateKeep int `json:"update_keep"`
}

// backupsDocAt is the document for the server.toml at path; running is what serve
// started with (nil: as saved).
func backupsDocAt(path string, running *config.BackupSettings, canRestart bool) (backupsDoc, error) {
	cfg, _, err := config.LoadFrom(path)
	if err != nil {
		return backupsDoc{}, err
	}
	saved := cfg.Backups.Settings()
	run := saved
	if running != nil {
		run = *running
	}
	dir := backupsDirOf(cfg)
	d := backupsDoc{File: path, Saved: saved, Running: run, RestartNeeded: !reflect.DeepEqual(saved, run),
		CanRestart: canRestart, Dir: dir, DefaultDir: backupsDirOf(withoutBackupsDir(cfg)),
		UpdateKeep: pgbundle.BackupsKeep(dir, updateKeep(cfg))}
	if disk, err := storage.DiskOf(dir); err == nil {
		d.Disk = &disk
	}
	if d.Dumps, err = pgbundle.ListDumps(dir); err != nil {
		d.Dumps = []pgbundle.Dump{}
	}
	for _, x := range d.Dumps {
		d.DumpsBytes += x.Bytes
	}
	return d, nil
}

func withoutBackupsDir(cfg config.Config) config.Config {
	cfg.Backups.Dir = ""
	return cfg
}

// backupsChange is PUT /config/backups' body: the settings to change, and what to do
// with the dumps in the old folder when the folder changes (nil: ask).
type backupsChange struct {
	config.BackupPatch
	MoveDumps *bool `json:"move_dumps"`
}

// backupsRefusal is a change refused: the fields and why, and for a folder that
// changes with dumps in the old one, how many there are (the choice to make).
type backupsRefusal struct {
	Errors []config.FieldError `json:"errors"`
	// Dumps and DumpsBytes are what the old folder holds when move_dumps is wanted.
	Dumps      int    `json:"dumps,omitempty"`
	DumpsBytes int64  `json:"dumps_bytes,omitempty"`
	From       string `json:"from,omitempty"`
}

func (r *backupsRefusal) Error() string {
	var msgs []string
	for _, e := range r.Errors {
		msgs = append(msgs, e.Field+": "+e.Message)
	}
	return strings.Join(msgs, "; ")
}

// backupsMinFree is the least free room a new backups folder's disk must have: a
// dump of a small library and the reserve.
const backupsMinFree = 4 << 30

// applyBackups changes [backups] in the server.toml at path; it returns the keys it
// changed, or a *backupsRefusal.
func applyBackups(path string, ch backupsChange) ([]string, error) {
	cfg, _, err := config.LoadFrom(path)
	if err != nil {
		return nil, err
	}
	saved := cfg.Backups.Settings()
	next := saved
	changed := ch.Apply(&next)
	if len(changed) == 0 {
		return nil, nil
	}
	// the default folder typed out is the default: it moves with the data (storage move)
	if next.Dir != "" && filepath.Clean(next.Dir) == filepath.Clean(backupsDirOf(withoutBackupsDir(cfg))) {
		next.Dir = ""
	}
	ref := &backupsRefusal{Errors: config.ValidateBackups(next)}
	oldDir := backupsDirOf(cfg)
	nextCfg := cfg
	nextCfg.Backups = next.Section()
	newDir := backupsDirOf(nextCfg)
	moving := filepath.Clean(oldDir) != filepath.Clean(newDir)
	if len(ref.Errors) == 0 && moving {
		if next.Dir != "" {
			checked, err := storage.CheckTarget(next.Dir, nil)
			if err != nil {
				ref.Errors = append(ref.Errors, config.FieldError{Field: "backups.dir", Message: err.Error()})
			} else {
				next.Dir, newDir = checked, checked
				nextCfg.Backups = next.Section()
			}
		}
		if len(ref.Errors) == 0 {
			if msg := backupsFolderProblem(newDir, cfg); msg != "" {
				ref.Errors = append(ref.Errors, config.FieldError{Field: "backups.dir", Message: msg})
			}
		}
		if dumps, _ := pgbundle.ListDumps(oldDir); len(ref.Errors) == 0 && len(dumps) > 0 && ch.MoveDumps == nil {
			ref.From, ref.Dumps = oldDir, len(dumps)
			for _, d := range dumps {
				ref.DumpsBytes += d.Bytes
			}
			ref.Errors = append(ref.Errors, config.FieldError{Field: "backups.move_dumps", Message: fmt.Sprintf(
				"%d dumps are in %s: move them to the new folder, or leave them there (they are then no longer "+
					"listed, pruned or restored from here)", len(dumps), oldDir)})
		}
	}
	if len(ref.Errors) > 0 {
		return nil, ref
	}
	cfg.Backups = next.Section()
	if err := cfg.SaveTo(path); err != nil {
		return nil, err
	}
	if moving && ch.MoveDumps != nil && *ch.MoveDumps {
		if _, err := pgbundle.MoveDumps(oldDir, newDir); err != nil {
			return changed, fmt.Errorf("the settings are saved, but the dumps did not all move to %s: %w", newDir, err)
		}
	}
	if next.UpdateKeep > 0 {
		// the database step's pruning reads it there (rules.updates.backups_keep's record)
		if err := pgbundle.SetBackupsKeep(newDir, next.UpdateKeep); err != nil {
			return changed, err
		}
	}
	return changed, nil
}

// backupsFolderProblem says why dir cannot hold the dumps ("": it can): not a local
// disk the database's dumps can live on, inside the database's own folder, too little
// room, or not writable.
func backupsFolderProblem(dir string, cfg config.Config) string {
	if config.Within(dir, filepath.Join(cfg.StateDir(), "postgres")) {
		return "the backups cannot go inside the database's own folder"
	}
	disk, err := storageDiskOf(dir)
	if err != nil {
		return "cannot read the disk of " + dir + ": " + err.Error()
	}
	if !disk.Suitable {
		return fmt.Sprintf("%s is on a %s disk (%s): choose a disk of this computer", dir, disk.FSType, disk.Mount)
	}
	if disk.FreeBytes > 0 && disk.FreeBytes < backupsMinFree {
		return fmt.Sprintf("only %s free on %s", humanBytes(disk.FreeBytes), disk.Mount)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "cannot make " + dir + ": " + err.Error()
	}
	f, err := os.CreateTemp(dir, ".maktaba-probe-*")
	if err != nil {
		return "cannot write in " + dir + ": " + err.Error()
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
	return ""
}

// deleteBackup removes one dump of the server.toml at path's backups folder, never the
// one the database step may still put back (an update's migrations not finished).
func deleteBackup(path, name string) error {
	cfg, _, err := config.LoadFrom(path)
	if err != nil {
		return err
	}
	if rec, ok, _ := apppayload.ReadDatabase(cfg.StateDir()); ok && filepath.Base(rec.Dump) == name &&
		(rec.State == apppayload.DatabaseMigrating || rec.State == apppayload.DatabaseRestoring) {
		return fmt.Errorf("%w: %s is the way back from the update under way", errDumpInUse, name)
	}
	return pgbundle.DeleteDump(backupsDirOf(cfg), name)
}

var errDumpInUse = errors.New("in use")

// backupsRoutes serves /config/backups (GET, PUT backupsChange) and DELETE
// /config/backups/dumps/<name>.
func (c *configAdmin) backupsRoutes(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.URL.Path == "/config/backups" && r.Method == http.MethodGet:
		c.backupsGet(w)
	case r.URL.Path == "/config/backups" && r.Method == http.MethodPut:
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<16))
		var ch backupsChange
		if err == nil {
			err = json.Unmarshal(body, &ch)
		}
		if err != nil {
			writeConfigJSON(w, http.StatusBadRequest, map[string]string{"error": "the settings: " + err.Error()})
			return true
		}
		c.mu.Lock()
		changed, err := applyBackups(c.path, ch)
		c.mu.Unlock()
		var ref *backupsRefusal
		switch {
		case errors.As(err, &ref):
			writeConfigJSON(w, http.StatusUnprocessableEntity, ref)
			return true
		case err != nil:
			writeConfigJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return true
		}
		if len(changed) > 0 {
			fmt.Fprintf(os.Stderr, "[serve] the backups' settings changed from the web: %v\n", changed)
		}
		c.backupsGet(w)
	case strings.HasPrefix(r.URL.Path, "/config/backups/dumps/") && r.Method == http.MethodDelete:
		name := strings.TrimPrefix(r.URL.Path, "/config/backups/dumps/")
		err := deleteBackup(c.path, name)
		switch {
		case errors.Is(err, errDumpInUse):
			writeConfigJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		case errors.Is(err, pgbundle.ErrNotADump) || errors.Is(err, os.ErrNotExist):
			writeConfigJSON(w, http.StatusNotFound, map[string]string{"error": "no dump " + name})
		case err != nil:
			writeConfigJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		default:
			fmt.Fprintf(os.Stderr, "[serve] the dump %s was deleted from the web\n", name)
			c.backupsGet(w)
		}
	default:
		return false
	}
	return true
}

func (c *configAdmin) backupsGet(w http.ResponseWriter) {
	running := c.runningBackups
	d, err := backupsDocAt(c.path, running, c.restart != nil || c.reload != nil)
	if err != nil {
		writeConfigJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeConfigJSON(w, http.StatusOK, d)
}
