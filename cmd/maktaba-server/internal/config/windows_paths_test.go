package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// winLayout builds the Windows layout before Epic 38 (server.toml and the keys in
// %APPDATA%\Maktaba) and the new one (everything in %LOCALAPPDATA%\Maktaba) under
// one temp dir.
func winLayout(t *testing.T) (old, cur Dirs, getenv func(string) string) {
	t.Helper()
	base := t.TempDir()
	env := map[string]string{"APPDATA": filepath.Join(base, "Roaming"), "LOCALAPPDATA": filepath.Join(base, "Local")}
	getenv = func(k string) string { return env[k] }
	return legacyWindowsDirs(getenv, base), DirsFor(getenv, "windows", base), getenv
}

// Epic 38 story 38.1: a Windows install moves its config and keys out of the
// roaming profile, once; the data folder was local already and stays, with the
// models and caches moved beside it (the state root, as on a Mac); the node
// installer's own files stay where its scripts read them.
func TestMigrateWindowsInstall(t *testing.T) {
	t.Parallel()
	old, cur, _ := winLayout(t)
	toml := "# my PC\n[server]\nweb_listen = \"0.0.0.0:8780\"\n"
	writeAt(t, filepath.Join(old.Config, "server.toml"), toml)
	writeAt(t, filepath.Join(old.Config, "secrets.key"), "S")
	writeAt(t, filepath.Join(old.Config, "stream-signing.key"), "K")
	writeAt(t, filepath.Join(old.Config, "node.json"), `{"node_id":"n"}`)
	writeAt(t, filepath.Join(old.Config, "node.env"), "MAKTABA_X=1")
	writeAt(t, filepath.Join(old.Data, "maktaba.db"), "DB")
	writeAt(t, filepath.Join(old.Data, "models", "huggingface", "w.bin"), "W")
	writeAt(t, filepath.Join(old.Data, "cache", "streaming", "seg"), "C")

	from, err := migrateDirs(old, cur, windowsMoved)
	if err != nil || from != old.Config {
		t.Fatalf("migrate = %q, %v", from, err)
	}
	if got := readAt(t, filepath.Join(cur.Config, "server.toml")); got != toml {
		t.Fatalf("server.toml changed on the way:\n%s", got)
	}
	root := filepath.Dir(cur.Data)
	for path, body := range map[string]string{
		filepath.Join(cur.Keys, "secrets.key"):                "S",
		filepath.Join(cur.Keys, "stream-signing.key"):         "K",
		filepath.Join(cur.Config, "node.json"):                `{"node_id":"n"}`,
		filepath.Join(cur.Data, "maktaba.db"):                 "DB",
		filepath.Join(root, "models", "huggingface", "w.bin"): "W",
		filepath.Join(root, "cache", "streaming", "seg"):      "C",
		filepath.Join(old.Config, "node.env"):                 "MAKTABA_X=1",
		filepath.Join(old.Config, movedNote):                  "",
	} {
		if b := readAt(t, path); body != "" && b != body {
			t.Errorf("%s = %q, want %q", path, b, body)
		}
	}
	if note := readAt(t, filepath.Join(old.Config, movedNote)); !strings.Contains(note, "settings and keys") {
		t.Errorf("the note says the data moved: %q", note)
	}
	if exists(filepath.Join(old.Config, "server.toml")) || exists(filepath.Join(old.Config, "secrets.key")) ||
		exists(filepath.Join(old.Config, "node.json")) {
		t.Error("the moved files must be gone from the roaming profile")
	}
	if from, err := migrateDirs(old, cur, windowsMoved); err != nil || from != "" {
		t.Fatalf("second migrate = %q, %v", from, err)
	}
}

// A Windows install whose data_dir was chosen (this PC's E:\tv\maktaba-local
// style) keeps it: only the config and keys move.
func TestMigrateWindowsKeepsChosenDataDir(t *testing.T) {
	t.Parallel()
	old, cur, _ := winLayout(t)
	chosen := filepath.Join(t.TempDir(), "maktaba-local", "data")
	toml := "[server]\ndata_dir = '" + chosen + "'\n"
	writeAt(t, filepath.Join(old.Config, "server.toml"), toml)
	writeAt(t, filepath.Join(chosen, "models", "m"), "W")
	if _, err := migrateDirs(old, cur, windowsMoved); err != nil {
		t.Fatal(err)
	}
	if readAt(t, filepath.Join(cur.Config, "server.toml")) != toml || readAt(t, filepath.Join(chosen, "models", "m")) != "W" {
		t.Fatal("a chosen data_dir and its models must stay where they are")
	}
}

// node.json: %LOCALAPPDATA%\Maktaba\node.json, or the roaming profile's of a
// machine that joined before Epic 38 while there is none in the new place.
func TestNodeConfigPathWindows(t *testing.T) {
	t.Parallel()
	_, cur, getenv := winLayout(t)
	base := filepath.Dir(filepath.Dir(cur.Config)) // <base>\Local\Maktaba → <base>
	newPath := filepath.Join(cur.Config, "node.json")
	oldPath := filepath.Join(getenv("APPDATA"), "Maktaba", "node.json")
	if got := NodeConfigPath(getenv, "windows", base); got != newPath {
		t.Fatalf("not joined: %s, want %s (a new join writes the new place)", got, newPath)
	}
	writeAt(t, oldPath, "{}")
	if got := NodeConfigPath(getenv, "windows", base); got != oldPath {
		t.Fatalf("joined before Epic 38: %s, want %s", got, oldPath)
	}
	writeAt(t, newPath, "{}")
	if got := NodeConfigPath(getenv, "windows", base); got != newPath {
		t.Fatalf("both: %s, want the new %s", got, newPath)
	}
}
