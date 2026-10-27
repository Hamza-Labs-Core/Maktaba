package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetDataDirText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"replaced, comments kept",
			"# mine\n[server]\nlisten = \":8080\"\ndata_dir = \"/old\" # the data\n[media]\nroots = []\n",
			"# mine\n[server]\nlisten = \":8080\"\ndata_dir = \"/Volumes/Big/Maktaba\" # the data\n[media]\nroots = []\n"},
		{"added to [server]",
			"[media]\nroots = []\n\n[server]\nlisten = \":8080\"\n",
			"[media]\nroots = []\n\n[server]\ndata_dir = \"/Volumes/Big/Maktaba\"\nlisten = \":8080\"\n"},
		{"a data_dir of another table stays",
			"[pipeline]\ndata_dir = '/x'\n",
			"[pipeline]\ndata_dir = '/x'\n\n[server]\ndata_dir = \"/Volumes/Big/Maktaba\"\n"},
		{"empty file", "", "[server]\ndata_dir = \"/Volumes/Big/Maktaba\"\n"},
		{"windows line ends",
			"[server]\r\ndata_dir = 'C:\\old'\r\n",
			"[server]\r\ndata_dir = \"/Volumes/Big/Maktaba\"\r\n"},
	}
	for _, c := range cases {
		if got := setDataDirText(c.in, "/Volumes/Big/Maktaba"); got != c.want {
			t.Errorf("%s:\n%q\nwant\n%q", c.name, got, c.want)
		}
	}
}

func TestSetDataDirReadsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.toml")
	if err := os.WriteFile(path, []byte("[server]\nlisten = \":8080\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := `D:\Maktaba "data"`
	if err := SetDataDir(path, dir); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadFrom(path)
	if err != nil || cfg.Server.DataDir != dir || cfg.Server.Listen != ":8080" {
		t.Fatalf("%q %q %v", cfg.Server.DataDir, cfg.Server.Listen, err)
	}
}

// A data folder the owner chose is missing while the record names it and the
// marker is not in it (a disk not mounted, a bare mount point): a start waits.
func TestStorageMissing(t *testing.T) {
	base := t.TempDir()
	cfgDir, data := filepath.Join(base, "cfg"), filepath.Join(base, "Big", "Maktaba")
	if _, missing := StorageMissing(cfgDir, data); missing {
		t.Fatal("no record: never missing")
	}
	if err := MarkStorage(cfgDir, data, "setup"); err != nil {
		t.Fatal(err)
	}
	if _, missing := StorageMissing(cfgDir, data); missing {
		t.Fatal("marked: missing")
	}
	// the disk goes: its mount point stays, empty
	if err := os.RemoveAll(data); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if p, missing := StorageMissing(cfgDir, data); !missing || p != data {
		t.Fatalf("an empty mount point: %q %v", p, missing)
	}
	// a data_dir the owner set by hand elsewhere is not the record's
	if _, missing := StorageMissing(cfgDir, filepath.Join(base, "elsewhere")); missing {
		t.Fatal("another data_dir read as missing")
	}
	// marking again keeps the id of a marker already there
	if err := MarkStorage(cfgDir, filepath.Join(base, "Two"), "move"); err != nil {
		t.Fatal(err)
	}
	rec, _ := ReadStorageRecord(cfgDir)
	if err := MarkStorage(cfgDir, filepath.Join(base, "Two"), "move"); err != nil {
		t.Fatal(err)
	}
	if again, _ := ReadStorageRecord(cfgDir); again.ID != rec.ID {
		t.Fatal("the marker's id changed")
	}
}

func TestWithin(t *testing.T) {
	for _, c := range []struct {
		p, root string
		want    bool
	}{{"/a/b", "/a", true}, {"/a", "/a", true}, {"/ab", "/a", false}, {"/", "/a", false}, {"/a/../b", "/a", false}} {
		if got := Within(c.p, c.root); got != c.want {
			t.Errorf("Within(%q, %q) = %v", c.p, c.root, got)
		}
	}
}
