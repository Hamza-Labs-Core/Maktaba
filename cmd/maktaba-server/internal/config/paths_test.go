package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func noEnv(string) string { return "" }

func TestDirsForMac(t *testing.T) {
	t.Parallel()
	d := DirsFor(func(k string) string { return "/xdg/" + k }, "darwin", "/Users/a")
	root := filepath.Join("/Users/a", "Library", "Application Support", "Maktaba")
	want := Dirs{Config: root, Data: filepath.Join(root, "data"), Logs: filepath.Join("/Users/a", "Library", "Logs", "Maktaba"),
		Keys: filepath.Join(root, "keys")}
	if d != want {
		t.Fatalf("darwin dirs = %+v, want %+v (XDG is ignored on a Mac)", d, want)
	}
}

func TestDirsForLinuxKeepsXDG(t *testing.T) {
	t.Parallel()
	d := DirsFor(noEnv, "linux", "/home/a")
	p := filepath.FromSlash
	want := Dirs{Config: p("/home/a/.config/maktaba"), Data: p("/home/a/.local/share/maktaba"),
		Logs: p("/home/a/.local/state/maktaba/logs"), Keys: p("/home/a/.config/maktaba")}
	if d != want {
		t.Fatalf("linux dirs = %+v, want %+v", d, want)
	}
	env := map[string]string{"XDG_CONFIG_HOME": "/c", "XDG_DATA_HOME": "/d", "XDG_STATE_HOME": "/s"}
	d = DirsFor(func(k string) string { return env[k] }, "linux", "/home/a")
	if d.Config != p("/c/maktaba") || d.Data != p("/d/maktaba") || d.Logs != p("/s/maktaba/logs") {
		t.Fatalf("XDG overrides not used: %+v", d)
	}
}

func TestDirsForWindows(t *testing.T) {
	t.Parallel()
	env := map[string]string{"APPDATA": `C:\R`, "LOCALAPPDATA": `C:\L`}
	d := DirsFor(func(k string) string { return env[k] }, "windows", `C:\U`)
	root := filepath.Join(`C:\L`, "Maktaba")
	want := Dirs{Config: root, Data: filepath.Join(root, "data"), Logs: filepath.Join(root, "logs"), Keys: filepath.Join(root, "keys")}
	if d != want {
		t.Fatalf("windows dirs = %+v, want %+v (nothing in the roaming profile)", d, want)
	}
	// no LOCALAPPDATA: the usual place under the profile
	if d := DirsFor(noEnv, "windows", `C:\U`); d.Config != filepath.Join(`C:\U`, "AppData", "Local", "Maktaba") {
		t.Fatalf("windows dirs without LOCALAPPDATA = %+v", d)
	}
	old := legacyWindowsDirs(func(k string) string { return env[k] }, `C:\U`)
	if old.Config != filepath.Join(`C:\R`, "Maktaba") || old.Keys != old.Config || old.Data != want.Data {
		t.Fatalf("legacy windows dirs = %+v", old)
	}
}

func TestStateDir(t *testing.T) {
	t.Parallel()
	def := "/m/Maktaba/data"
	for _, c := range []struct{ goos, data, want string }{
		{"darwin", "/m/Maktaba/data", "/m/Maktaba"},
		{"darwin", "/m/Maktaba/data/", "/m/Maktaba"},
		{"darwin", "/Volumes/media/data", "/Volumes/media/data"}, // a chosen dir named data
		{"darwin", "/Volumes/Big/maktaba", "/Volumes/Big/maktaba"},
		{"linux", "/m/Maktaba/data", "/m/Maktaba/data"},
		{"windows", "/m/Maktaba/data", "/m/Maktaba"},    // %LOCALAPPDATA%\Maktaba\data
		{"windows", "/d/media/state", "/d/media/state"}, // a chosen dir
	} {
		if got := stateDir(c.goos, c.data, def, false); filepath.Clean(got) != filepath.Clean(c.want) {
			t.Errorf("stateDir(%s, %s) = %s, want %s", c.goos, c.data, got, c.want)
		}
	}
}

// Epic 38 story 38.1: a Windows install from before keeps its models, caches and
// Postgres in its default data folder, where its older versions (and its own
// Postgres, maybe running) use them: nothing moves them, and outside the app that
// folder stays the state root. The first command decides and records it
// (recordStateRoot, state-root.json); every later resolution reads the record, so
// the state root survives a restart whatever the folders do. Inside the app the
// state root is always %LOCALAPPDATA%\Maktaba.
func TestStateDirWindowsInstallFromBefore(t *testing.T) {
	t.Parallel()
	data := filepath.Join(t.TempDir(), "Maktaba", "data")
	root := filepath.Dir(data)
	writeAt(t, filepath.Join(data, "chroma", "chroma.sqlite3"), "I")
	if got := stateDir("windows", data, data, true); got != root {
		t.Fatalf("inside the app: %s, want %s", got, root)
	}
	if got := stateDir("windows", data, data, false); got != data || exists(filepath.Join(root, stateRootRecord)) {
		t.Fatalf("before the record the folders decide, and reading writes nothing: %s", got)
	}
	if err := recordStateRoot(root, data); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readAt(t, filepath.Join(root, stateRootRecord)), `"data"`) {
		t.Fatal("the decision must be recorded")
	}
	// a restart after the state folders went (or moved by hand): the record decides
	if err := os.RemoveAll(filepath.Join(data, "chroma")); err != nil {
		t.Fatal(err)
	}
	if err := recordStateRoot(root, data); err != nil {
		t.Fatal(err)
	}
	if got := stateDir("windows", data, data, false); got != data {
		t.Fatalf("after the restart: %s, want the recorded %s", got, data)
	}
	if got := stateDir("darwin", data, data, false); got != root {
		t.Fatalf("a Mac moves its own (Epic 36): %s, want %s", got, root)
	}
}

// A new install (no state in data\) decides on the root beside it, once: an older
// version run later that makes data\models or data\postgres never moves the state
// root (the bundled Postgres would start an empty cluster in data\postgres). With no
// data\ yet there is nothing to decide on and nothing is written.
func TestStateDirDecidedOnce(t *testing.T) {
	t.Parallel()
	data := filepath.Join(t.TempDir(), "Maktaba", "data")
	root := filepath.Dir(data)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := recordStateRoot(root, data); err != nil || exists(filepath.Join(root, stateRootRecord)) {
		t.Fatalf("no data folder yet: %v, record made: %v", err, exists(filepath.Join(root, stateRootRecord)))
	}
	writeAt(t, filepath.Join(data, "maktaba.db"), "DB")
	if err := recordStateRoot(root, data); err != nil {
		t.Fatal(err)
	}
	if got := stateDir("windows", data, data, false); got != root {
		t.Fatalf("no state in data: %s, want the new root %s", got, root)
	}
	writeAt(t, filepath.Join(data, "postgres", "PG_VERSION"), "16")
	writeAt(t, filepath.Join(data, "models", "m"), "W")
	if err := recordStateRoot(root, data); err != nil {
		t.Fatal(err)
	}
	if got := stateDir("windows", data, data, false); got != root {
		t.Fatalf("a folder that appeared later moved the state root to %s", got)
	}
	if !strings.Contains(readAt(t, filepath.Join(root, stateRootRecord)), `"root"`) {
		t.Fatal("the record is never replaced")
	}
}

// The app's move (38.13) runs inside the app and reads an install from before with
// that install's rule: StateDirOutsideApp is its data folder while the app's own
// state root is %LOCALAPPDATA%\Maktaba. A data_dir the owner chose is the state root
// either way. LOCALAPPDATA points at a temp dir.
func TestStateDirOutsideApp(t *testing.T) {
	chosen := Config{Server: ServerSection{DataDir: filepath.Join(t.TempDir(), "state")}}
	writeAt(t, filepath.Join(chosen.Server.DataDir, "chroma", "chroma.sqlite3"), "I")
	if got := chosen.StateDirOutsideApp(); got != chosen.Server.DataDir {
		t.Errorf("a chosen data_dir outside the app: %s, want %s", got, chosen.Server.DataDir)
	}
	if got := stateDir(runtime.GOOS, chosen.Server.DataDir, LocalDirs().Data, true); got != chosen.Server.DataDir {
		t.Errorf("a chosen data_dir inside the app: %s, want %s", got, chosen.Server.DataDir)
	}
	if runtime.GOOS != "windows" {
		return // the default data_dir's rule is Windows' (TestStateDirWindowsInstallFromBefore)
	}
	t.Setenv("LOCALAPPDATA", t.TempDir())
	def := Config{Server: ServerSection{DataDir: LocalDirs().Data}}
	writeAt(t, filepath.Join(def.Server.DataDir, "chroma", "chroma.sqlite3"), "I")
	if got := stateDir(runtime.GOOS, def.Server.DataDir, LocalDirs().Data, true); got != filepath.Dir(def.Server.DataDir) {
		t.Errorf("inside the app: %s, want the root %s", got, filepath.Dir(def.Server.DataDir))
	}
	if got := def.StateDirOutsideApp(); got != def.Server.DataDir {
		t.Errorf("the install from before, read from inside the app: %s, want its data folder %s", got, def.Server.DataDir)
	}
}

// macLayout builds an old XDG install and the new Mac dirs under one temp dir.
func macLayout(t *testing.T) (old, cur Dirs) {
	t.Helper()
	base := t.TempDir()
	old = xdgDirs(noEnv, base)
	cur = DirsFor(noEnv, "darwin", base)
	return old, cur
}

func writeAt(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readAt(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestMigrateMovesDefaultInstall(t *testing.T) {
	t.Parallel()
	old, cur := macLayout(t)
	// literal strings: a Windows temp path has backslashes
	toml := "# my comment\n[server]\ndata_dir = '" + old.Data + "'\n[database]\nurl = 'sqlite://" + old.Data + "/maktaba.db'\n"
	writeAt(t, filepath.Join(old.Config, "server.toml"), toml)
	writeAt(t, filepath.Join(old.Config, "secrets.key"), "S")
	writeAt(t, filepath.Join(old.Config, "stream-signing.key"), "K")
	writeAt(t, filepath.Join(old.Data, "maktaba.db"), "DB")
	writeAt(t, filepath.Join(old.Data, "models", "m", "w.bin"), "W")
	writeAt(t, filepath.Join(old.Data, "cache", "streaming", "seg"), "C")

	from, err := migrateDirs(osMover, old, cur)
	if err != nil || from != old.Config {
		t.Fatalf("migrate = %q, %v", from, err)
	}
	got := readAt(t, filepath.Join(cur.Config, "server.toml"))
	if !strings.Contains(got, "# my comment") || !strings.Contains(got, `data_dir = '`+cur.Data+`'`) ||
		!strings.Contains(got, `url = 'sqlite://`+cur.Data+`/maktaba.db'`) || strings.Contains(got, old.Data) {
		t.Fatalf("rewritten server.toml:\n%s", got)
	}
	root := filepath.Dir(cur.Data)
	for path, body := range map[string]string{
		filepath.Join(cur.Keys, "secrets.key"):           "S",
		filepath.Join(cur.Keys, "stream-signing.key"):    "K",
		filepath.Join(cur.Data, "maktaba.db"):            "DB",
		filepath.Join(root, "models", "m", "w.bin"):      "W",
		filepath.Join(root, "cache", "streaming", "seg"): "C",
		filepath.Join(old.Config, movedNote):             "",
	} {
		if b := readAt(t, path); body != "" && b != body {
			t.Errorf("%s = %q, want %q", path, b, body)
		}
	}
	if exists(filepath.Join(old.Config, "server.toml")) || exists(old.Data) {
		t.Error("the old server.toml and data dir should be gone")
	}
	// once: a second run does nothing
	if from, err := migrateDirs(osMover, old, cur); err != nil || from != "" {
		t.Fatalf("second migrate = %q, %v", from, err)
	}
}

func TestMigrateKeepsChosenDataDir(t *testing.T) {
	t.Parallel()
	old, cur := macLayout(t)
	chosen := filepath.Join(t.TempDir(), "media-state")
	toml := "[server]\ndata_dir = '" + chosen + "'\n"
	writeAt(t, filepath.Join(old.Config, "server.toml"), toml)
	writeAt(t, filepath.Join(chosen, "maktaba.db"), "DB")
	if _, err := migrateDirs(osMover, old, cur); err != nil {
		t.Fatal(err)
	}
	if got := readAt(t, filepath.Join(cur.Config, "server.toml")); got != toml {
		t.Fatalf("server.toml changed:\n%s", got)
	}
	if readAt(t, filepath.Join(chosen, "maktaba.db")) != "DB" || exists(cur.Data) {
		t.Fatal("a chosen data_dir must stay where it is")
	}
}

func TestMigrateLeavesNewInstallAlone(t *testing.T) {
	t.Parallel()
	old, cur := macLayout(t)
	writeAt(t, filepath.Join(old.Config, "server.toml"), "[server]\n")
	writeAt(t, filepath.Join(cur.Config, "server.toml"), "[server]\nlisten = \"x\"\n")
	if from, err := migrateDirs(osMover, old, cur); err != nil || from != "" {
		t.Fatalf("migrate = %q, %v", from, err)
	}
	if !exists(filepath.Join(old.Config, "server.toml")) {
		t.Fatal("the old install must not be touched when the new one exists")
	}
}

func TestMigrateFinishesAfterAPartialRun(t *testing.T) {
	t.Parallel()
	old, cur := macLayout(t)
	toml := "[server]\ndata_dir = '" + old.Data + "'\n"
	writeAt(t, filepath.Join(old.Config, "server.toml"), toml)
	// a run that moved the data and stopped before server.toml
	writeAt(t, filepath.Join(cur.Data, "maktaba.db"), "DB")
	writeAt(t, filepath.Join(cur.Data, "models", "m"), "W")
	if _, err := migrateDirs(osMover, old, cur); err != nil {
		t.Fatal(err)
	}
	if got := readAt(t, filepath.Join(cur.Config, "server.toml")); !strings.Contains(got, cur.Data) {
		t.Fatalf("server.toml still names the old data dir:\n%s", got)
	}
	if readAt(t, filepath.Join(filepath.Dir(cur.Data), "models", "m")) != "W" {
		t.Fatal("models not moved beside data/")
	}
}

func TestMigrateIntoEmptyDataDir(t *testing.T) {
	t.Parallel()
	old, cur := macLayout(t)
	writeAt(t, filepath.Join(old.Config, "server.toml"), "[server]\n")
	writeAt(t, filepath.Join(old.Data, "maktaba.db"), "DB")
	if err := os.MkdirAll(cur.Data, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := migrateDirs(osMover, old, cur); err != nil {
		t.Fatal(err)
	}
	if readAt(t, filepath.Join(cur.Data, "maktaba.db")) != "DB" {
		t.Fatal("data not moved into the empty new data dir")
	}
}

func TestMigrateRefusesToOverwriteData(t *testing.T) {
	t.Parallel()
	old, cur := macLayout(t)
	writeAt(t, filepath.Join(old.Config, "server.toml"), "[server]\n")
	writeAt(t, filepath.Join(old.Data, "maktaba.db"), "OLD")
	writeAt(t, filepath.Join(cur.Data, "maktaba.db"), "NEW")
	if _, err := migrateDirs(osMover, old, cur); err == nil {
		t.Fatal("want an error when the new data dir already exists")
	}
	if readAt(t, filepath.Join(cur.Data, "maktaba.db")) != "NEW" || !exists(filepath.Join(old.Config, "server.toml")) {
		t.Fatal("nothing may move when the migration is refused")
	}
}
