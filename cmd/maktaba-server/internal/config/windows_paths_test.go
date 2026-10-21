package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// winLayout builds the Windows layout before Epic 38 (server.toml and the keys in
// %APPDATA%\Maktaba) and the new one (everything in %LOCALAPPDATA%\Maktaba) under
// one temp dir. env is the environment migrateWindows reads.
func winLayout(t *testing.T) (old, cur Dirs, env map[string]string) {
	t.Helper()
	base := t.TempDir()
	env = map[string]string{"APPDATA": filepath.Join(base, "Roaming"), "LOCALAPPDATA": filepath.Join(base, "Local")}
	getenv := func(k string) string { return env[k] }
	return legacyWindowsDirs(getenv, base), DirsFor(getenv, "windows", base), env
}

func migrateWin(t *testing.T, m mover, old, cur Dirs, env map[string]string) []string {
	t.Helper()
	lines, err := migrateWindows(m, old, cur, func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("migrate: %v (%q)", err, lines)
	}
	return lines
}

// Epic 38 story 38.1: a Windows install moves its config and keys out of the
// roaming profile; the data folder was local already and stays, with the models
// and caches moved beside it (the state root, as on a Mac); the node installer's
// own files stay where its scripts read them.
func TestMigrateWindowsInstall(t *testing.T) {
	t.Parallel()
	old, cur, env := winLayout(t)
	toml := "# my PC\n[server]\nweb_listen = \"0.0.0.0:8780\"\n"
	writeAt(t, filepath.Join(old.Config, "server.toml"), toml)
	writeAt(t, filepath.Join(old.Config, "secrets.key"), "S")
	writeAt(t, filepath.Join(old.Config, "stream-signing.key"), "K")
	writeAt(t, filepath.Join(old.Config, "postgres-maktaba.password"), "P")
	writeAt(t, filepath.Join(old.Config, "node.json"), `{"node_id":"n"}`)
	writeAt(t, filepath.Join(old.Config, "node.env"), "MAKTABA_X=1")
	writeAt(t, filepath.Join(old.Data, "maktaba.db"), "DB")
	writeAt(t, filepath.Join(old.Data, "models", "huggingface", "w.bin"), "W")
	writeAt(t, filepath.Join(old.Data, "cache", "streaming", "seg"), "C")

	if lines := migrateWin(t, osMover, old, cur, env); len(lines) != 7 {
		t.Fatalf("want a line for each of the 7 pieces moved:\n%s", strings.Join(lines, "\n"))
	}
	if got := readAt(t, filepath.Join(cur.Config, "server.toml")); got != toml {
		t.Fatalf("server.toml changed on the way:\n%s", got)
	}
	root := filepath.Dir(cur.Data)
	for path, body := range map[string]string{
		filepath.Join(cur.Keys, "secrets.key"):                "S",
		filepath.Join(cur.Keys, "stream-signing.key"):         "K",
		filepath.Join(cur.Keys, "postgres-maktaba.password"):  "P",
		filepath.Join(cur.Config, "node.json"):                `{"node_id":"n"}`,
		filepath.Join(cur.Data, "maktaba.db"):                 "DB",
		filepath.Join(root, "models", "huggingface", "w.bin"): "W",
		filepath.Join(root, "cache", "streaming", "seg"):      "C",
		filepath.Join(old.Config, "node.env"):                 "MAKTABA_X=1",
	} {
		if b := readAt(t, path); b != body {
			t.Errorf("%s = %q, want %q", path, b, body)
		}
	}
	if note := readAt(t, filepath.Join(old.Config, movedNote)); !strings.Contains(note, "settings and keys") {
		t.Errorf("the note says the data moved: %q", note)
	}
	for _, gone := range []string{"server.toml", "secrets.key", "stream-signing.key", "postgres-maktaba.password", "node.json"} {
		if exists(filepath.Join(old.Config, gone)) {
			t.Errorf("%s must be gone from the roaming profile", gone)
		}
	}
	if lines := migrateWin(t, osMover, old, cur, env); len(lines) != 0 {
		t.Fatalf("a second start moves nothing:\n%s", strings.Join(lines, "\n"))
	}
}

// serve runs without a server.toml: such an install still moves its keys, its
// node.json and its large state (a new key would leave every stored secret
// unreadable; models would download again, the semantic index would be lost).
func TestMigrateWindowsWithoutServerToml(t *testing.T) {
	t.Parallel()
	old, cur, env := winLayout(t)
	writeAt(t, filepath.Join(old.Config, "secrets.key"), "S")
	writeAt(t, filepath.Join(old.Config, "stream-signing.key"), "K")
	writeAt(t, filepath.Join(old.Config, "node.json"), "{}")
	writeAt(t, filepath.Join(old.Data, "models", "m"), "W")
	writeAt(t, filepath.Join(old.Data, "chroma", "chroma.sqlite3"), "I")
	writeAt(t, filepath.Join(old.Data, "cache", "seg"), "C")
	migrateWin(t, osMover, old, cur, env)
	root := filepath.Dir(cur.Data)
	for path, body := range map[string]string{
		filepath.Join(cur.Keys, "secrets.key"):          "S",
		filepath.Join(cur.Keys, "stream-signing.key"):   "K",
		filepath.Join(cur.Config, "node.json"):          "{}",
		filepath.Join(root, "models", "m"):              "W",
		filepath.Join(root, "chroma", "chroma.sqlite3"): "I",
		filepath.Join(root, "cache", "seg"):             "C",
	} {
		if b := readAt(t, path); b != body {
			t.Errorf("%s = %q, want %q", path, b, body)
		}
	}
	if exists(filepath.Join(cur.Config, "server.toml")) {
		t.Error("no server.toml is made by the move")
	}
}

// With $MAKTABA_CONFIG naming a server.toml with the default data_dir, the large
// state still moves beside data\; server.toml and the keys stay beside that file
// (KeysDir), and the roaming profile's keys are not that install's.
func TestMigrateWindowsOwnConfig(t *testing.T) {
	t.Parallel()
	old, cur, env := winLayout(t)
	own := filepath.Join(t.TempDir(), "server.toml")
	env["MAKTABA_CONFIG"] = own
	writeAt(t, own, "[server]\nweb_listen = \"0.0.0.0:8780\"\n")
	writeAt(t, filepath.Join(filepath.Dir(own), "secrets.key"), "MINE")
	writeAt(t, filepath.Join(old.Config, "secrets.key"), "ROAMING")
	writeAt(t, filepath.Join(old.Config, "node.json"), "{}")
	writeAt(t, filepath.Join(old.Data, "models", "m"), "W")
	migrateWin(t, osMover, old, cur, env)
	if readAt(t, filepath.Join(filepath.Dir(cur.Data), "models", "m")) != "W" {
		t.Error("the models must move beside data\\")
	}
	if readAt(t, filepath.Join(old.Config, "secrets.key")) != "ROAMING" || exists(filepath.Join(cur.Keys, "secrets.key")) ||
		readAt(t, filepath.Join(filepath.Dir(own), "secrets.key")) != "MINE" {
		t.Error("with $MAKTABA_CONFIG the keys stay where they are")
	}
	if !exists(filepath.Join(cur.Config, "node.json")) {
		t.Error("node.json follows $MAKTABA_NODE_CONFIG, not $MAKTABA_CONFIG: it moves")
	}

	// a data_dir of its own: the state stays in it
	old, cur, env = winLayout(t)
	chosen := filepath.Join(t.TempDir(), "state")
	env["MAKTABA_CONFIG"] = own
	writeAt(t, own, "[server]\ndata_dir = '"+chosen+"'\n")
	writeAt(t, filepath.Join(old.Data, "models", "m"), "W")
	migrateWin(t, osMover, old, cur, env)
	if exists(filepath.Join(filepath.Dir(cur.Data), "models")) {
		t.Error("the state of a chosen data_dir install must not move")
	}
}

// A Windows install whose data_dir was chosen (this PC's E:\tv\maktaba-local
// style) keeps it: only the config and keys move.
func TestMigrateWindowsKeepsChosenDataDir(t *testing.T) {
	t.Parallel()
	old, cur, env := winLayout(t)
	chosen := filepath.Join(t.TempDir(), "maktaba-local", "data")
	toml := "[server]\ndata_dir = '" + chosen + "'\n"
	writeAt(t, filepath.Join(old.Config, "server.toml"), toml)
	writeAt(t, filepath.Join(chosen, "models", "m"), "W")
	migrateWin(t, osMover, old, cur, env)
	if readAt(t, filepath.Join(cur.Config, "server.toml")) != toml || readAt(t, filepath.Join(chosen, "models", "m")) != "W" {
		t.Fatal("a chosen data_dir and its models must stay where they are")
	}
}

// Nothing is replaced and no two folders merge: a piece whose new place holds
// something else stays, and the line says which one Maktaba uses.
func TestMigrateWindowsNeverReplaces(t *testing.T) {
	t.Parallel()
	old, cur, env := winLayout(t)
	writeAt(t, filepath.Join(old.Config, "secrets.key"), "OLD")
	writeAt(t, filepath.Join(cur.Keys, "secrets.key"), "NEW")
	writeAt(t, filepath.Join(old.Data, "chroma", "a"), "OLD")
	writeAt(t, filepath.Join(cur.Config, "chroma", "b"), "NEW")
	lines := migrateWin(t, osMover, old, cur, env)
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "Left "+filepath.Join(old.Config, "secrets.key")) ||
		!strings.Contains(lines[1], "Maktaba uses that one") {
		t.Fatalf("lines:\n%s", strings.Join(lines, "\n"))
	}
	if readAt(t, filepath.Join(old.Config, "secrets.key")) != "OLD" || readAt(t, filepath.Join(cur.Keys, "secrets.key")) != "NEW" ||
		readAt(t, filepath.Join(old.Data, "chroma", "a")) != "OLD" || exists(filepath.Join(cur.Config, "chroma", "a")) {
		t.Fatal("both sides must stay as they were")
	}
	if exists(filepath.Join(old.Config, movedNote)) {
		t.Error("nothing left the roaming profile: no note")
	}
}

// This PC's case: %APPDATA% on E:, %LOCALAPPDATA% on C: (or a roaming profile on a
// server), and here the data folder on a third drive too. Every rename between
// them fails as between volumes; the keys, node.json and the state still move,
// whole, and a move a power cut stopped is finished by the next start.
func TestMigrateWindowsAcrossVolumes(t *testing.T) {
	t.Parallel()
	old, cur, env := winLayout(t)
	m := volumes(old.Config, cur.Config, cur.Data)
	writeAt(t, filepath.Join(old.Config, "server.toml"), "[server]\n")
	writeAt(t, filepath.Join(old.Config, "secrets.key"), "S")
	writeAt(t, filepath.Join(old.Config, "stream-signing.key"), "K")
	writeAt(t, filepath.Join(old.Config, "node.json"), "{}")
	models := map[string]string{"huggingface/hub/a.bin": "A", "huggingface/hub/b.bin": "B", "ollama/c": "C"}
	plant(t, filepath.Join(old.Data, "models"), models)

	// the power cut: the sixth copy (the second of the models) never lands
	if _, err := migrateWindows(cutAt(m, 6), old, cur, func(k string) string { return env[k] }); err == nil {
		t.Fatal("the cut move must fail")
	}
	if readAt(t, filepath.Join(cur.Keys, "secrets.key")) != "S" || exists(filepath.Join(old.Config, "secrets.key")) {
		t.Fatal("the key moved before the cut must be whole in the new place only")
	}
	migrateWin(t, m, old, cur, env)
	root := filepath.Dir(cur.Data)
	holds(t, filepath.Join(root, "models"), models)
	holds(t, cur.Keys, map[string]string{"secrets.key": "S", "stream-signing.key": "K"})
	if readAt(t, filepath.Join(cur.Config, "node.json")) != "{}" || readAt(t, filepath.Join(cur.Config, "server.toml")) != "[server]\n" {
		t.Fatal("server.toml and node.json must be in the new place")
	}
	if exists(filepath.Join(old.Data, "models")) || exists(filepath.Join(root, "models"+movingSuffix)) {
		t.Fatal("the old models folder and the note must be gone")
	}
	if lines := migrateWin(t, m, old, cur, env); len(lines) != 0 {
		t.Fatalf("a third start moves nothing:\n%s", strings.Join(lines, "\n"))
	}
}

// %APPDATA% and %LOCALAPPDATA% naming one folder: a file is never taken for a copy
// of itself (and removed).
func TestMigrateWindowsSameFolder(t *testing.T) {
	t.Parallel()
	_, _, env := winLayout(t)
	env["APPDATA"] = env["LOCALAPPDATA"]
	getenv := func(k string) string { return env[k] }
	old, cur := legacyWindowsDirs(getenv, t.TempDir()), DirsFor(getenv, "windows", t.TempDir())
	writeAt(t, filepath.Join(cur.Config, "server.toml"), "[server]\n")
	writeAt(t, filepath.Join(cur.Config, "node.json"), "{}")
	writeAt(t, filepath.Join(old.Config, "secrets.key"), "S")
	migrateWin(t, osMover, old, cur, env)
	if readAt(t, filepath.Join(cur.Config, "server.toml")) != "[server]\n" || readAt(t, filepath.Join(cur.Config, "node.json")) != "{}" ||
		readAt(t, filepath.Join(cur.Keys, "secrets.key")) != "S" {
		t.Fatal("server.toml and node.json must stay, the key move into keys\\")
	}
}

// A key copied whole to the new place by a start that stopped before it removed
// the old one: the next start removes the old one and keeps the key.
func TestMigrateWindowsAfterACrashBeforeTheRemove(t *testing.T) {
	t.Parallel()
	old, cur, env := winLayout(t)
	writeAt(t, filepath.Join(old.Config, "secrets.key"), "S")
	writeAt(t, filepath.Join(cur.Keys, "secrets.key"), "S")
	migrateWin(t, volumes(old.Config, cur.Config), old, cur, env)
	if exists(filepath.Join(old.Config, "secrets.key")) || readAt(t, filepath.Join(cur.Keys, "secrets.key")) != "S" {
		t.Fatal("the next start must finish the move")
	}
}

// The Mac move (Epic 36) between two volumes works the same way.
func TestMigrateMacAcrossVolumes(t *testing.T) {
	t.Parallel()
	old, cur := layout(t)
	m := volumes(filepath.Dir(old.Config), filepath.Dir(old.Data), cur.Config)
	writeAt(t, filepath.Join(old.Config, "server.toml"), "[server]\n")
	writeAt(t, filepath.Join(old.Config, "secrets.key"), "S")
	writeAt(t, filepath.Join(old.Data, "maktaba.db"), "DB")
	writeAt(t, filepath.Join(old.Data, "models", "m"), "W")
	if _, err := migrateDirs(m, old, cur); err != nil {
		t.Fatal(err)
	}
	holds(t, cur.Data, map[string]string{"maktaba.db": "DB"})
	if readAt(t, filepath.Join(cur.Keys, "secrets.key")) != "S" || readAt(t, filepath.Join(filepath.Dir(cur.Data), "models", "m")) != "W" {
		t.Fatal("the key and the models must be in the new place")
	}
	if exists(old.Data) || exists(filepath.Join(old.Config, "secrets.key")) {
		t.Fatal("the old data folder and key must be gone")
	}
}

// node.json: %LOCALAPPDATA%\Maktaba\node.json, or the roaming profile's of a
// machine that joined before Epic 38 while there is none in the new place.
func TestNodeConfigPathWindows(t *testing.T) {
	t.Parallel()
	_, cur, env := winLayout(t)
	getenv := func(k string) string { return env[k] }
	base := filepath.Dir(filepath.Dir(cur.Config)) // <base>\Local\Maktaba → <base>
	newPath := filepath.Join(cur.Config, "node.json")
	oldPath := filepath.Join(env["APPDATA"], "Maktaba", "node.json")
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
