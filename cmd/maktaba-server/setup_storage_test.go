package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/storage"
)

// fakeStorageDisks stands two disks in for this machine's: the system one and a
// bigger one; every folder is on a disk with free bytes free.
func fakeStorageDisks(t *testing.T, base string, free uint64, fsType string) {
	t.Helper()
	prevList, prevOf := storageDisks, storageDiskOf
	t.Cleanup(func() { storageDisks, storageDiskOf = prevList, prevOf })
	storageDisks = func(string, string) []storage.Disk {
		return []storage.Disk{
			{Mount: "/", Name: "Macintosh HD", FreeBytes: 40 << 30, Suitable: true, Internal: true, SuggestedDir: "/default"},
			{Mount: filepath.Join(base, "Big"), Name: "Big", FreeBytes: 900 << 30, Suitable: true,
				SuggestedDir: filepath.Join(base, "Big", "Maktaba")},
			{Mount: filepath.Join(base, "Stick"), Name: "Stick", FreeBytes: 2000 << 30, FSType: "exfat",
				SuggestedDir: filepath.Join(base, "Stick", "Maktaba")},
		}
	}
	storageDiskOf = func(string) (storage.Disk, error) {
		return storage.Disk{Mount: base, FreeBytes: free, FSType: fsType, Suitable: fsType != "exfat"}, nil
	}
}

func TestSetupStorageOffersTheRoomiestDisk(t *testing.T) {
	e := newSetupEnv(t)
	fakeStorageDisks(t, e.dir, 900<<30, "apfs")
	code, _, last := runStep(t, "storage", map[string]any{"check_only": true})
	if code != 0 {
		t.Fatalf("exit %d: %v", code, last)
	}
	d := data(t, last)
	if d["suggested"] != filepath.Join(e.dir, "Big", "Maktaba") {
		t.Errorf("suggested %v", d["suggested"])
	}
	if disks, _ := d["disks"].([]any); len(disks) != 3 || d["need_bytes"] == nil || d["comfortable_bytes"] == nil {
		t.Errorf("result %v", d)
	}
}

// The chosen folder is written to server.toml before anything is made in the
// state root, with its marker; what an earlier step saved goes with it.
func TestSetupStorageWritesTheDataDir(t *testing.T) {
	e := newSetupEnv(t)
	e.dir, _ = filepath.EvalSymlinks(e.dir)
	fakeStorageDisks(t, e.dir, 60<<30, "apfs")
	// the admin step ran first (the user came back to this screen)
	if err := os.MkdirAll(filepath.Join(e.state, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.state, "state", "setup.json"), []byte(`{"roots":["/m"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(e.dir, "Big", "Maktaba")
	code, _, last := runStep(t, "storage", map[string]any{"data_dir": dir})
	if code != 0 {
		t.Fatalf("exit %d: %v", code, last)
	}
	d := data(t, last)
	if d["data_dir"] != dir {
		t.Errorf("data_dir %v", d["data_dir"])
	}
	if w, _ := d["warnings"].([]any); len(w) != 1 || w[0].(map[string]any)["code"] != "storage_small" {
		t.Errorf("warnings %v", d["warnings"])
	}
	cfg, _, err := config.LoadFrom(e.config)
	if err != nil || cfg.Server.DataDir != dir {
		t.Fatalf("server.toml data_dir %q %v", cfg.Server.DataDir, err)
	}
	if _, missing := config.StorageMissing(filepath.Dir(e.config), dir); missing {
		t.Error("the chosen folder reads as missing")
	}
	if _, err := os.Stat(filepath.Join(dir, "state", "setup.json")); err != nil {
		t.Errorf("the pending setup did not move: %v", err)
	}
	// the next step finds it in the new state root
	code, _, last = runStep(t, "storage", map[string]any{"check_only": true})
	if code != 0 || data(t, last)["suggested"] != dir {
		t.Errorf("back on the screen: %v", last)
	}
}

func TestSetupStorageRefusals(t *testing.T) {
	for _, c := range []struct {
		name, fs string
		free     uint64
		dir      func(e *setupEnv) string
		code     string
	}{
		{"relative", "apfs", 900 << 30, func(*setupEnv) string { return "Big/Maktaba" }, "storage_relative"},
		{"exfat", "exfat", 900 << 30, func(e *setupEnv) string { return filepath.Join(e.dir, "Stick", "M") }, "storage_not_local"},
		{"too small", "apfs", 5 << 30, func(e *setupEnv) string { return filepath.Join(e.dir, "Small", "M") }, "storage_too_small"},
		{"not empty", "apfs", 900 << 30, func(e *setupEnv) string {
			p := filepath.Join(e.dir, "Full")
			_ = os.MkdirAll(p, 0o700)
			_ = os.WriteFile(filepath.Join(p, "x"), nil, 0o600)
			return p
		}, "storage_not_empty"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newSetupEnv(t)
			fakeStorageDisks(t, e.dir, c.free, c.fs)
			before, _ := os.ReadFile(e.config)
			code, _, last := runStep(t, "storage", map[string]any{"data_dir": c.dir(e)})
			if code != 1 || last["code"] != c.code || last["field"] != "data_dir" {
				t.Fatalf("exit %d: %v", code, last)
			}
			if after, _ := os.ReadFile(e.config); string(after) != string(before) {
				t.Error("server.toml changed")
			}
		})
	}
}

// The chosen disk not connected: status says the install is set up (the server
// waits for it), and finish makes no database in its place.
func TestSetupWithTheChosenDiskMissing(t *testing.T) {
	e := newSetupEnv(t)
	e.dir, _ = filepath.EvalSymlinks(e.dir)
	fakeStorageDisks(t, e.dir, 900<<30, "apfs")
	dir := filepath.Join(e.dir, "Big", "Maktaba")
	if code, _, last := runStep(t, "storage", map[string]any{"data_dir": dir}); code != 0 {
		t.Fatalf("%v", last)
	}
	if err := os.RemoveAll(filepath.Join(e.dir, "Big")); err != nil { // the disk is unplugged
		t.Fatal(err)
	}
	_, _, last := runStep(t, "status", map[string]any{})
	st := data(t, last)
	if st["configured"] != true || st["storage_missing"] != dir {
		t.Errorf("status %v", st)
	}
	code, _, last := runStep(t, "finish", map[string]any{})
	if code != 1 || last["code"] != "storage_missing_disk" {
		t.Fatalf("finish %d %v", code, last)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "Big")); err == nil {
		t.Error("finish made the missing disk's folder")
	}
	if !strings.Contains(last["message"].(string), dir) {
		t.Errorf("message %v", last["message"])
	}
}

// The storage screen sets the backups too: their folder, the schedule and its hour.
func TestSetupStorageSetsTheBackups(t *testing.T) {
	e := newSetupEnv(t)
	e.dir, _ = filepath.EvalSymlinks(e.dir)
	fakeStorageDisks(t, e.dir, 900<<30, "apfs")
	dir := filepath.Join(e.dir, "Big", "Maktaba")
	code, _, last := runStep(t, "storage", map[string]any{"data_dir": dir, "backups_dir": "rel/b"})
	if code != 1 || last["code"] != "storage_backups_unusable" || last["field"] != "backups_dir" {
		t.Fatalf("a relative backups folder: %d %v", code, last)
	}
	bdir := filepath.Join(e.dir, "Other", "backups")
	code, _, last = runStep(t, "storage", map[string]any{"data_dir": dir, "backups_dir": bdir, "nightly": false, "hour": 1})
	if code != 0 {
		t.Fatalf("%d %v", code, last)
	}
	if d := data(t, last); d["backups_dir"] != bdir {
		t.Errorf("result %v", d)
	}
	cfg, _, err := config.LoadFrom(e.config)
	if err != nil {
		t.Fatal(err)
	}
	if s := cfg.Backups.Settings(); s.Dir != bdir || s.Nightly || s.Hour != 1 || cfg.Server.DataDir != dir {
		t.Errorf("saved %+v data_dir %s", s, cfg.Server.DataDir)
	}
}
