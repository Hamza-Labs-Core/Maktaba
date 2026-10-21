package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestLoadMissingReturnsDefaults(t *testing.T) {
	t.Parallel()
	cfg, found, err := LoadFrom(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Error("found should be false for a missing file")
	}
	if cfg.Server.Listen != "0.0.0.0:8080" {
		t.Errorf("default listen = %q", cfg.Server.Listen)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "server.toml")

	in := Defaults()
	in.Media.Roots = []string{"/mnt/videos", "/mnt/movies"}
	in.Database.URL, in.DatabaseURLSet = "postgres://localhost/maktaba", true
	in.Transcription.Backend = "faster-whisper"
	if err := in.SaveTo(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	out, found, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !found {
		t.Fatal("found should be true after save")
	}
	if out.Database.URL != in.Database.URL {
		t.Errorf("url = %q, want %q", out.Database.URL, in.Database.URL)
	}
	if len(out.Media.Roots) != 2 || out.Media.Roots[1] != "/mnt/movies" {
		t.Errorf("roots = %v", out.Media.Roots)
	}
	if !out.DatabaseURLSet {
		t.Error("a url chosen to keep is set after a load")
	}
}

// A setting the file does not set stays unset through a save: the default
// SQLite url (or the bundled Postgres's, which serve puts in the config it
// runs with) would keep the bundled Postgres from running, and instances = 0
// would mean no transcription workers.
func TestSaveLeavesOutWhatTheFileDoesNotSet(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "server.toml")
	if err := writeFile(path, "[server]\nweb_listen = \"0.0.0.0:8090\"\n"); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Media.Roots = []string{"/mnt/videos"}
	running := cfg
	running.Database.URL = "postgres://maktaba@127.0.0.1:54329/maktaba"
	for name, c := range map[string]Config{"loaded": cfg, "running": running} {
		if err := c.SaveTo(path); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(path)
		var file map[string]map[string]any
		if _, err := toml.Decode(string(raw), &file); err != nil {
			t.Fatal(err)
		}
		if _, ok := file["database"]["url"]; ok || file["pipeline"]["instances"] != nil {
			t.Fatalf("%s: server.toml sets what it did not:\n%s", name, raw)
		}
		out, _, err := LoadFrom(path)
		if err != nil || out.DatabaseURLSet || out.PipelineInstancesSet {
			t.Fatalf("%s: set after a save: url %v, instances %v (%v)", name, out.DatabaseURLSet, out.PipelineInstancesSet, err)
		}
		if out.Server.WebListen != "0.0.0.0:8090" || len(out.Media.Roots) != 1 || out.Database.URL != Defaults().Database.URL {
			t.Fatalf("%s: %+v %+v %q", name, out.Server, out.Media, out.Database.URL)
		}
	}

	// set ones are kept, a 0 too
	if err := writeFile(path, "[database]\nurl = \"postgres://m@h/m\"\n[pipeline]\ninstances = 0\n"); err != nil {
		t.Fatal(err)
	}
	cfg, _, _ = LoadFrom(path)
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	out, _, err := LoadFrom(path)
	if err != nil || !out.DatabaseURLSet || out.Database.URL != "postgres://m@h/m" ||
		!out.PipelineInstancesSet || out.Pipeline.Instances != 0 {
		t.Fatalf("set ones: %+v %+v %v", out.Database, out.Pipeline, err)
	}
	// a count other than 0 is kept without the flag (setting it is choosing it)
	cfg = Defaults()
	cfg.Pipeline.Instances = 2
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	if out, _, _ := LoadFrom(path); !out.PipelineInstancesSet || out.Pipeline.Instances != 2 {
		t.Fatalf("instances = 2: %+v", out.Pipeline)
	}
}

func TestPartialFileLayersOverDefaults(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "server.toml")
	// Only the database URL is set; everything else should default.
	if err := writeFile(path, "[database]\nurl = \"sqlite:///tmp/x.db\"\n"); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Database.URL != "sqlite:///tmp/x.db" {
		t.Errorf("url = %q", cfg.Database.URL)
	}
	if cfg.Server.Listen != "0.0.0.0:8080" {
		t.Errorf("listen should keep default, got %q", cfg.Server.Listen)
	}
}

// [postgres] update_dumps: how many pre-update dumps stay; unset (or not above
// 0) keeps the default three.
func TestKeepUpdateDumps(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "server.toml")
	if err := writeFile(path, "[postgres]\nupdate_dumps = 5\n"); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := cfg.Postgres.KeepUpdateDumps(); n != 5 {
		t.Errorf("update_dumps = 5 keeps %d", n)
	}
	for _, n := range []int{0, -2} {
		if got := (PostgresSection{UpdateDumps: n}).KeepUpdateDumps(); got != DefaultUpdateDumps {
			t.Errorf("update_dumps = %d keeps %d, want %d", n, got, DefaultUpdateDumps)
		}
	}
}
