package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func noEnv(string) string { return "" }

func TestDirsForMac(t *testing.T) {
	t.Parallel()
	d := DirsFor(func(k string) string { return "/xdg/" + k }, "darwin", "/Users/a")
	root := "/Users/a/Library/Application Support/Maktaba"
	want := Dirs{Config: root, Data: root + "/data", Logs: "/Users/a/Library/Logs/Maktaba", Keys: root + "/keys"}
	if d != want {
		t.Fatalf("darwin dirs = %+v, want %+v (XDG is ignored on a Mac)", d, want)
	}
}

func TestDirsForLinuxKeepsXDG(t *testing.T) {
	t.Parallel()
	d := DirsFor(noEnv, "linux", "/home/a")
	want := Dirs{Config: "/home/a/.config/maktaba", Data: "/home/a/.local/share/maktaba",
		Logs: "/home/a/.local/state/maktaba/logs", Keys: "/home/a/.config/maktaba"}
	if d != want {
		t.Fatalf("linux dirs = %+v, want %+v", d, want)
	}
	env := map[string]string{"XDG_CONFIG_HOME": "/c", "XDG_DATA_HOME": "/d", "XDG_STATE_HOME": "/s"}
	d = DirsFor(func(k string) string { return env[k] }, "linux", "/home/a")
	if d.Config != "/c/maktaba" || d.Data != "/d/maktaba" || d.Logs != "/s/maktaba/logs" {
		t.Fatalf("XDG overrides not used: %+v", d)
	}
}

func TestDirsForWindowsUnchanged(t *testing.T) {
	t.Parallel()
	env := map[string]string{"APPDATA": `C:\R`, "LOCALAPPDATA": `C:\L`}
	d := DirsFor(func(k string) string { return env[k] }, "windows", `C:\U`)
	if d.Config != filepath.Join(`C:\R`, "Maktaba") || d.Data != filepath.Join(`C:\L`, "Maktaba", "data") || d.Keys != d.Config {
		t.Fatalf("windows dirs = %+v", d)
	}
}

func TestStateDir(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ goos, data, want string }{
		{"darwin", "/m/Maktaba/data", "/m/Maktaba"},
		{"darwin", "/Volumes/Big/maktaba", "/Volumes/Big/maktaba"},
		{"linux", "/home/a/.local/share/maktaba", "/home/a/.local/share/maktaba"},
		{"windows", `C:\L\Maktaba\data`, `C:\L\Maktaba\data`},
	} {
		if got := stateDir(c.goos, c.data); got != c.want {
			t.Errorf("stateDir(%s, %s) = %s, want %s", c.goos, c.data, got, c.want)
		}
	}
}

// layout builds an old XDG install and the new Mac dirs under one temp dir.
func layout(t *testing.T) (old, cur Dirs) {
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
	old, cur := layout(t)
	toml := "# my comment\n[server]\ndata_dir = \"" + old.Data + "\"\n[database]\nurl = \"sqlite://" + old.Data + "/maktaba.db\"\n"
	writeAt(t, filepath.Join(old.Config, "server.toml"), toml)
	writeAt(t, filepath.Join(old.Config, "secrets.key"), "S")
	writeAt(t, filepath.Join(old.Config, "stream-signing.key"), "K")
	writeAt(t, filepath.Join(old.Data, "maktaba.db"), "DB")
	writeAt(t, filepath.Join(old.Data, "models", "m", "w.bin"), "W")
	writeAt(t, filepath.Join(old.Data, "cache", "streaming", "seg"), "C")

	from, err := migrateDirs(old, cur)
	if err != nil || from != old.Config {
		t.Fatalf("migrate = %q, %v", from, err)
	}
	got := readAt(t, filepath.Join(cur.Config, "server.toml"))
	if !strings.Contains(got, "# my comment") || !strings.Contains(got, `data_dir = "`+cur.Data+`"`) ||
		!strings.Contains(got, `url = "sqlite://`+cur.Data+`/maktaba.db"`) || strings.Contains(got, old.Data) {
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
	if from, err := migrateDirs(old, cur); err != nil || from != "" {
		t.Fatalf("second migrate = %q, %v", from, err)
	}
}

func TestMigrateKeepsChosenDataDir(t *testing.T) {
	t.Parallel()
	old, cur := layout(t)
	chosen := filepath.Join(t.TempDir(), "media-state")
	toml := "[server]\ndata_dir = \"" + chosen + "\"\n"
	writeAt(t, filepath.Join(old.Config, "server.toml"), toml)
	writeAt(t, filepath.Join(chosen, "maktaba.db"), "DB")
	if _, err := migrateDirs(old, cur); err != nil {
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
	old, cur := layout(t)
	writeAt(t, filepath.Join(old.Config, "server.toml"), "[server]\n")
	writeAt(t, filepath.Join(cur.Config, "server.toml"), "[server]\nlisten = \"x\"\n")
	if from, err := migrateDirs(old, cur); err != nil || from != "" {
		t.Fatalf("migrate = %q, %v", from, err)
	}
	if !exists(filepath.Join(old.Config, "server.toml")) {
		t.Fatal("the old install must not be touched when the new one exists")
	}
}

func TestMigrateRefusesToOverwriteData(t *testing.T) {
	t.Parallel()
	old, cur := layout(t)
	writeAt(t, filepath.Join(old.Config, "server.toml"), "[server]\n")
	writeAt(t, filepath.Join(old.Data, "maktaba.db"), "OLD")
	writeAt(t, filepath.Join(cur.Data, "maktaba.db"), "NEW")
	if _, err := migrateDirs(old, cur); err == nil {
		t.Fatal("want an error when the new data dir already exists")
	}
	if readAt(t, filepath.Join(cur.Data, "maktaba.db")) != "NEW" || !exists(filepath.Join(old.Config, "server.toml")) {
		t.Fatal("nothing may move when the migration is refused")
	}
}
