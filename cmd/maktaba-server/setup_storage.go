package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/storage"
)

// setupComfortDisk is the free room under which the storage step warns that a
// disk is small for a library database and its backups.
const setupComfortDisk = 100 << 30

// storageDisks are the disks the storage step offers (tests replace it).
var storageDisks = storage.ListDisks

// storageDiskOf is the disk of a folder (tests replace it).
var storageDiskOf = storage.DiskOf

// storageChoice is the storage step's answer to check_only.
type storageChoice struct {
	Current          string         `json:"current"`
	Default          string         `json:"default"`
	NeedBytes        uint64         `json:"need_bytes"`
	ComfortableBytes uint64         `json:"comfortable_bytes"`
	Disks            []storage.Disk `json:"disks"`
	Suggested        string         `json:"suggested"`
}

// stepStorage: the `storage` step (Epic 36 story 36.15), where this install keeps
// its data, chosen before anything is made there (the admin step's pending state,
// the models, the venv, the database). Input {"check_only": true} (or no
// data_dir): the disks with their free space and the folder suggested on the one
// with the most room. Input {"data_dir": "<dir>"}: that folder is made (with its
// marker) and [server] data_dir written to server.toml; result {data_dir,
// state_dir, warnings}. Errors storage_relative, storage_not_local,
// storage_too_small, storage_not_empty, storage_unwritable,
// storage_database_exists (field data_dir).
func stepStorage(s *setupRun, in json.RawMessage) (any, error) {
	var req struct {
		CheckOnly bool   `json:"check_only"`
		DataDir   string `json:"data_dir"`
	}
	if err := decodeInput(in, &req); err != nil {
		return nil, err
	}
	def := config.DefaultDataDir()
	cur := filepath.Clean(s.cfg.Server.DataDir)
	if req.CheckOnly || strings.TrimSpace(req.DataDir) == "" {
		disks := storageDisks(cur, def)
		if disks == nil {
			disks = []storage.Disk{}
		}
		suggested := storage.Suggest(disks, def)
		if rec, ok := config.ReadStorageRecord(config.TOMLDir()); ok && filepath.Clean(rec.DataDir) == cur {
			suggested = cur // chosen before (the user came back to this screen)
		}
		return storageChoice{Current: cur, Default: def, NeedBytes: setupNeedDisk, ComfortableBytes: setupComfortDisk,
			Disks: disks, Suggested: suggested}, nil
	}
	dir, err := storage.CheckTarget(req.DataDir, nil)
	if err != nil {
		code := "storage_relative"
		var te *storage.TargetError
		if errors.As(err, &te) && te.Note.Code != "target_relative" {
			code = "storage_unwritable"
		}
		return nil, failStep(code, "data_dir", "%v", err)
	}
	same := dir == cur
	if !same && databaseExists(s.cfg) {
		return nil, failStep("storage_database_exists", "data_dir", "This install has a library database already: "+
			"setup does not move it (maktaba-server storage move does, or Settings → Storage)")
	}
	disk, err := storageDiskOf(dir)
	if err != nil {
		return nil, failStep("storage_unwritable", "data_dir", "Cannot read the disk of %s: %v", dir, err).
			with("error", err.Error())
	}
	if !disk.Suitable {
		return nil, failStep("storage_not_local", "data_dir", "%s is on a %s disk (%s): the library database needs a "+
			"disk of this computer", dir, disk.FSType, disk.Mount).with("fs_type", disk.FSType)
	}
	if disk.FreeBytes > 0 && disk.FreeBytes < setupNeedDisk {
		return nil, failStep("storage_too_small", "data_dir", "Only %s free on %s: Maktaba needs %s to start.",
			humanBytes(disk.FreeBytes), disk.Mount, humanBytes(setupNeedDisk)).
			with("free_bytes", disk.FreeBytes, "need_bytes", uint64(setupNeedDisk))
	}
	if !same && dir != filepath.Clean(def) && !emptyFolder(dir) {
		return nil, failStep("storage_not_empty", "data_dir", "%s is not empty: choose an empty folder or a new one.", dir).
			with("path", dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, failStep("storage_unwritable", "data_dir", "Cannot make %s: %v", dir, err).with("error", err.Error())
	}
	probe, err := os.CreateTemp(dir, ".maktaba-probe-*")
	if err != nil {
		return nil, failStep("storage_unwritable", "data_dir", "Cannot write in %s: %v", dir, err).with("error", err.Error())
	}
	_ = probe.Close()
	_ = os.Remove(probe.Name())

	// what the earlier steps saved goes with the state root (the admin's pending
	// account, when the user came back here)
	oldPending := s.pendingPath()
	next := s.cfg
	next.Server.DataDir = dir
	if err := config.SetDataDir(config.Path(), dir); err != nil {
		return nil, err
	}
	if dir == filepath.Clean(def) {
		err = os.Remove(filepath.Join(config.TOMLDir(), config.StorageRecordFile))
		if errors.Is(err, fs.ErrNotExist) {
			err = nil
		}
	} else {
		err = config.MarkStorage(config.TOMLDir(), dir, "setup")
	}
	if err != nil {
		return nil, err
	}
	s.cfg = next
	if newPending := s.pendingPath(); newPending != oldPending {
		if _, err := os.Stat(oldPending); err == nil {
			if err := os.MkdirAll(filepath.Dir(newPending), 0o700); err != nil {
				return nil, err
			}
			if err := os.Rename(oldPending, newPending); err != nil {
				return nil, fmt.Errorf("carry %s to %s: %w", oldPending, newPending, err)
			}
		}
	}
	warnings := []storage.Note{}
	if disk.FreeBytes > 0 && disk.FreeBytes < setupComfortDisk {
		warnings = append(warnings, storage.Note{Code: "storage_small", Message: fmt.Sprintf("%s free on %s is small "+
			"for a library database plus its backups", humanBytes(disk.FreeBytes), disk.Mount),
			Params: map[string]any{"free_bytes": disk.FreeBytes, "comfortable_bytes": uint64(setupComfortDisk)}})
	}
	return map[string]any{"data_dir": dir, "state_dir": next.StateDir(), "warnings": warnings}, nil
}

// emptyFolder: dir is not there, or holds nothing but a storage marker.
func emptyFolder(dir string) bool {
	kids, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	for _, k := range kids {
		if k.Name() != config.StorageMarkerFile && k.Name() != ".DS_Store" {
			return false
		}
	}
	return true
}
