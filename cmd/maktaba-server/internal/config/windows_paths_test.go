package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// winLayout builds the Windows layout before Epic 38 (server.toml and the keys in
// %APPDATA%\Maktaba) and the new one (everything in %LOCALAPPDATA%\Maktaba) under
// one temp dir. env is the environment migrateWindows reads.
func winLayout(t *testing.T) (old, cur Dirs, env map[string]string) {
	t.Helper()
	return winLayoutAt(t.TempDir())
}

// winLayoutAt is winLayout under base (a second process builds the same).
func winLayoutAt(base string) (old, cur Dirs, env map[string]string) {
	env = map[string]string{"APPDATA": filepath.Join(base, "Roaming"), "LOCALAPPDATA": filepath.Join(base, "Local")}
	getenv := func(k string) string { return env[k] }
	return legacyWindowsDirs(getenv, base), DirsFor(getenv, "windows", base), env
}

func migrateWin(t *testing.T, m mover, old, cur Dirs, env map[string]string) []string {
	t.Helper()
	lines, err := migrateWindows(m, old, cur, func(k string) string { return env[k] }, time.Second)
	if err != nil {
		t.Fatalf("migrate: %v (%q)", err, lines)
	}
	return lines
}

// Epic 38 story 38.1: a Windows install's config and keys are copied out of the
// roaming profile, and the originals stay (an older version of the same install, a
// node's pipeline run from a checkout or the node installer still read them); the
// copies win. node.json is never copied: the machine's one node config stays where
// it is. The large state stays in data\, which stays the state root; nothing of the
// node installer's is touched.
func TestMigrateWindowsCopiesAndKeepsTheOriginals(t *testing.T) {
	t.Parallel()
	old, cur, env := winLayout(t)
	originals := map[string]string{
		"server.toml":               "# my PC\n[server]\nweb_listen = \"0.0.0.0:8780\"\n",
		"secrets.key":               "S",
		"stream-signing.key":        "K",
		"postgres-maktaba.password": "P",
		"node.json":                 `{"node_id":"n"}`,
		"node.env":                  "MAKTABA_X=1",
	}
	plant(t, old.Config, originals)
	state := map[string]string{"maktaba.db": "DB", "models/huggingface/w.bin": "W", "cache/streaming/seg": "C"}
	plant(t, old.Data, state)

	if lines := migrateWin(t, osMover, old, cur, env); len(lines) != 4 {
		t.Fatalf("want a line for each of the 4 files copied:\n%s", strings.Join(lines, "\n"))
	}
	for path, body := range map[string]string{
		filepath.Join(cur.Config, "server.toml"):             originals["server.toml"],
		filepath.Join(cur.Keys, "secrets.key"):               "S",
		filepath.Join(cur.Keys, "stream-signing.key"):        "K",
		filepath.Join(cur.Keys, "postgres-maktaba.password"): "P",
	} {
		if b := readAt(t, path); b != body {
			t.Errorf("%s = %q, want %q", path, b, body)
		}
	}
	if exists(filepath.Join(cur.Config, "node.json")) {
		t.Error("node.json must not be copied: one node config per machine")
	}
	holds(t, old.Config, originals) // every original as it was, and nothing added
	holds(t, old.Data, state)       // the state where it was
	if got := stateDir("windows", cur.Data, cur.Data, false); got != cur.Data {
		t.Errorf("the state root of an install from before: %s, want its data folder %s", got, cur.Data)
	}
	// an older reader (Maktaba before Epic 38) and this version read the same file
	base := filepath.Dir(filepath.Dir(cur.Config))
	if got := NodeConfigPath(func(k string) string { return env[k] }, "windows", base); got != filepath.Join(old.Config, "node.json") {
		t.Errorf("NodeConfigPath = %s, want the original", got)
	}
	if lines := migrateWin(t, osMover, old, cur, env); len(lines) != 0 {
		t.Fatalf("a second start finds the copies and does nothing:\n%s", strings.Join(lines, "\n"))
	}
	holds(t, old.Config, originals)
}

// serve runs without a server.toml: such an install still gets its keys copied (a
// new key would leave every stored secret unreadable).
func TestMigrateWindowsWithoutServerToml(t *testing.T) {
	t.Parallel()
	old, cur, env := winLayout(t)
	originals := map[string]string{"secrets.key": "S", "stream-signing.key": "K", "node.json": "{}"}
	plant(t, old.Config, originals)
	migrateWin(t, osMover, old, cur, env)
	holds(t, cur.Keys, map[string]string{"secrets.key": "S", "stream-signing.key": "K"})
	if exists(filepath.Join(cur.Config, "node.json")) || exists(filepath.Join(cur.Config, "server.toml")) {
		t.Fatal("no node.json and no server.toml are made")
	}
	holds(t, old.Config, originals)
}

// With $MAKTABA_CONFIG, server.toml and the keys stay beside that file (KeysDir) and
// the roaming profile's are not that install's: nothing is copied, no folder made.
func TestMigrateWindowsOwnConfig(t *testing.T) {
	t.Parallel()
	old, cur, env := winLayout(t)
	own := filepath.Join(t.TempDir(), "server.toml")
	env["MAKTABA_CONFIG"] = own
	writeAt(t, own, "[server]\n")
	plant(t, old.Config, map[string]string{"server.toml": "[server]\n", "secrets.key": "ROAMING", "node.json": "{}"})
	if lines := migrateWin(t, osMover, old, cur, env); len(lines) != 0 || exists(cur.Config) {
		t.Fatalf("with $MAKTABA_CONFIG nothing is copied and no folder made (%q)", lines)
	}
}

// One node config per machine: after the copy, a re-join (the pipeline's save()
// writes the file it read, cluster/config.py) and a hand edit both land in the
// roaming profile's node.json, the file maktaba-server, streaming and the pipeline
// all read; nothing stale is left beside it to take its place later.
func TestMigrateWindowsNodeConfigStaysOne(t *testing.T) {
	t.Parallel()
	old, cur, env := winLayout(t)
	original := filepath.Join(old.Config, "node.json")
	writeAt(t, original, `{"node_id":"first","private_key":"k1","database_url":"postgres://a"}`)
	migrateWin(t, osMover, old, cur, env)
	getenv := func(k string) string { return env[k] }
	base := filepath.Dir(filepath.Dir(cur.Config))
	read := func() NodeConfig { return ReadNodeConfig(getenv, "windows", base, os.ReadFile) }
	if p := NodeConfigPath(getenv, "windows", base); p != original {
		t.Fatalf("NodeConfigPath = %s, want the one node config %s", p, original)
	}
	// a re-join writes the file NodeConfigPath names
	writeAt(t, NodeConfigPath(getenv, "windows", base), `{"node_id":"rejoined","private_key":"k2","database_url":"postgres://b"}`)
	if n := read(); n.NodeID != "rejoined" {
		t.Fatalf("after a re-join: %+v", n)
	}
	migrateWin(t, osMover, old, cur, env)
	writeAt(t, original, `{"node_id":"edited","private_key":"k2","database_url":"postgres://b"}`) // by hand
	if n := read(); n.NodeID != "edited" {
		t.Fatalf("after a hand edit: %+v", n)
	}
	if exists(filepath.Join(cur.Config, "node.json")) {
		t.Fatal("no second node config may appear")
	}
}

// A copy is never replaced: a new place that holds something else stays, and so
// does the original, with a line that says which one Maktaba uses.
func TestMigrateWindowsNeverReplaces(t *testing.T) {
	t.Parallel()
	old, cur, env := winLayout(t)
	writeAt(t, filepath.Join(old.Config, "secrets.key"), "OLD")
	writeAt(t, filepath.Join(cur.Keys, "secrets.key"), "NEW")
	lines := migrateWin(t, osMover, old, cur, env)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "Did not copy "+filepath.Join(old.Config, "secrets.key")) ||
		!strings.HasSuffix(lines[0], "Maktaba uses "+filepath.Join(cur.Keys, "secrets.key")) {
		t.Fatalf("lines:\n%s", strings.Join(lines, "\n"))
	}
	if readAt(t, filepath.Join(old.Config, "secrets.key")) != "OLD" || readAt(t, filepath.Join(cur.Keys, "secrets.key")) != "NEW" {
		t.Fatal("both must stay as they were")
	}
}

// This PC's case: %APPDATA% on E:, %LOCALAPPDATA% on C: (or a roaming profile on a
// server): every rename between them fails as between volumes. The copies are made
// whole beside their new names; one a power cut stopped is made by the next start,
// and the originals stay throughout.
func TestMigrateWindowsAcrossVolumes(t *testing.T) {
	t.Parallel()
	old, cur, env := winLayout(t)
	m := volumes(old.Config, cur.Config)
	originals := map[string]string{"server.toml": "[server]\n", "secrets.key": "S", "stream-signing.key": "K", "node.json": "{}"}
	plant(t, old.Config, originals)

	// the power cut: the third copy (stream-signing.key) never lands
	if _, err := migrateWindows(cutAt(m, 3), old, cur, func(k string) string { return env[k] }, time.Second); err == nil {
		t.Fatal("the cut copy must fail")
	}
	if readAt(t, filepath.Join(cur.Keys, "secrets.key")) != "S" || exists(filepath.Join(cur.Keys, "stream-signing.key")) {
		t.Fatal("the copy made before the cut is whole; the cut one is not there under its name")
	}
	writeAt(t, filepath.Join(cur.Keys, "stream-signing.key"+partSuffix), "") // a crash mid-copy too
	migrateWin(t, m, old, cur, env)
	holds(t, cur.Keys, map[string]string{"secrets.key": "S", "stream-signing.key": "K"}) // and no part file
	if readAt(t, filepath.Join(cur.Config, "server.toml")) != "[server]\n" {
		t.Fatal("server.toml must be whole in the new place")
	}
	holds(t, old.Config, originals)
	if lines := migrateWin(t, m, old, cur, env); len(lines) != 0 {
		t.Fatalf("a third start copies nothing:\n%s", strings.Join(lines, "\n"))
	}
}

// %APPDATA% and %LOCALAPPDATA% naming one folder: a file is its own copy (nothing
// said, nothing removed); the key is copied into keys\.
func TestMigrateWindowsSameFolder(t *testing.T) {
	t.Parallel()
	_, _, env := winLayout(t)
	env["APPDATA"] = env["LOCALAPPDATA"]
	getenv := func(k string) string { return env[k] }
	old, cur := legacyWindowsDirs(getenv, t.TempDir()), DirsFor(getenv, "windows", t.TempDir())
	writeAt(t, filepath.Join(cur.Config, "server.toml"), "[server]\n")
	writeAt(t, filepath.Join(cur.Config, "node.json"), "{}")
	writeAt(t, filepath.Join(old.Config, "secrets.key"), "S")
	if lines := migrateWin(t, osMover, old, cur, env); len(lines) != 1 {
		t.Fatalf("only the key is copied:\n%s", strings.Join(lines, "\n"))
	}
	if readAt(t, filepath.Join(cur.Config, "server.toml")) != "[server]\n" || readAt(t, filepath.Join(cur.Config, "node.json")) != "{}" ||
		readAt(t, filepath.Join(cur.Keys, "secrets.key")) != "S" || readAt(t, filepath.Join(old.Config, "secrets.key")) != "S" {
		t.Fatal("server.toml and node.json stay, the key is copied into keys\\")
	}
}

// A piece not copied is said once, and again only when that changes (an older
// version rewrote its original after the copy); the record goes when there is
// nothing to say.
func TestMigrateWindowsSaysKeptOnce(t *testing.T) {
	t.Parallel()
	old, cur, env := winLayout(t)
	oldKey, newKey := filepath.Join(old.Config, "secrets.key"), filepath.Join(cur.Keys, "secrets.key")
	writeAt(t, oldKey, "OLD")
	writeAt(t, newKey, "NEW")
	if lines := migrateWin(t, osMover, old, cur, env); len(lines) != 1 || !strings.HasPrefix(lines[0], "Did not copy "+oldKey) {
		t.Fatalf("first start:\n%s", strings.Join(lines, "\n"))
	}
	if lines := migrateWin(t, osMover, old, cur, env); len(lines) != 0 {
		t.Fatalf("said again:\n%s", strings.Join(lines, "\n"))
	}
	if err := os.Remove(newKey); err != nil {
		t.Fatal(err)
	}
	if lines := migrateWin(t, osMover, old, cur, env); len(lines) != 1 || !strings.HasPrefix(lines[0], "Copied "+oldKey) {
		t.Fatalf("once the new place is empty the key is copied:\n%s", strings.Join(lines, "\n"))
	}
	if exists(filepath.Join(cur.Config, moveReported)) {
		t.Error("nothing to say: no record")
	}
	writeAt(t, oldKey, "REWRITTEN")
	if lines := migrateWin(t, osMover, old, cur, env); len(lines) != 1 || !strings.HasPrefix(lines[0], "Did not copy "+oldKey) {
		t.Fatalf("the original changed after the copy: said again:\n%s", strings.Join(lines, "\n"))
	}
	if readAt(t, newKey) != "OLD" {
		t.Fatal("the copy is never replaced")
	}
}

// The move lock: a second start waits for the first, a bounded time, and goes on
// when it is released.
func TestMoveLockWaits(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	release, err := lockMove(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := lockMove(root, 100*time.Millisecond); err == nil || !strings.Contains(err.Error(), "another maktaba-server") {
		t.Fatalf("held: %v", err)
	}
	if time.Since(start) < 100*time.Millisecond {
		t.Fatal("a second start waits before it gives up")
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		release()
	}()
	again, err := lockMove(root, 5*time.Second)
	if err != nil {
		t.Fatalf("released while waiting: %v", err)
	}
	again()
}

// Inside the desktop app's payload nothing is moved or copied (the app's move
// copies an older install and keeps it for going back, Epic 38 story 38.13); for a
// maktaba-server outside it the same install's key is copied on Windows (the
// original stays) and moved on a Mac (Epic 36). The environment points at temp dirs.
func TestMigrateDirsNotInAppPayload(t *testing.T) {
	base := t.TempDir()
	var oldKey, newKey string
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", filepath.Join(base, "Roaming"))
		t.Setenv("LOCALAPPDATA", filepath.Join(base, "Local"))
		oldKey = filepath.Join(base, "Roaming", "Maktaba", "secrets.key")
		newKey = filepath.Join(base, "Local", "Maktaba", "keys", "secrets.key")
	case "darwin":
		t.Setenv("HOME", base)
		for _, k := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
			t.Setenv(k, "")
		}
		writeAt(t, filepath.Join(base, ".config", "maktaba", "server.toml"), "[server]\n")
		oldKey = filepath.Join(base, ".config", "maktaba", "secrets.key")
		newKey = filepath.Join(base, "Library", "Application Support", "Maktaba", "keys", "secrets.key")
	default:
		t.Skip("nothing moves on " + runtime.GOOS)
	}
	t.Setenv("MAKTABA_CONFIG", "")
	t.Setenv("MAKTABA_APP_BUNDLE", "")
	writeAt(t, oldKey, "S")
	payload := filepath.Join(base, "app", "server")
	writeAt(t, filepath.Join(payload, "bin", "maktaba-server.exe"), "exe")
	writeAt(t, filepath.Join(payload, "payload.json"), "{}")

	if lines, err := MigrateDirs(filepath.Join(payload, "bin", "maktaba-server.exe")); err != nil || len(lines) != 0 {
		t.Fatalf("in the app: %q, %v", lines, err)
	}
	if readAt(t, oldKey) != "S" || exists(newKey) {
		t.Fatal("inside the app the older install's key must stay where it is")
	}
	if lines, err := MigrateDirs(filepath.Join(base, "release", "bin", "maktaba-server.exe")); err != nil || len(lines) == 0 {
		t.Fatalf("outside the app: %q, %v", lines, err)
	}
	if readAt(t, newKey) != "S" || exists(oldKey) != (runtime.GOOS == "windows") {
		t.Fatal("outside the app the install's key is copied (Windows, the original kept) or moved (Mac)")
	}
}

// The Mac move (Epic 36) between two volumes works the same way.
func TestMigrateMacAcrossVolumes(t *testing.T) {
	t.Parallel()
	old, cur := macLayout(t)
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

// node.json: one per machine, where it is: the roaming profile's of a machine that
// joined before Epic 38 (a stray file in the new place does not take its place),
// else %LOCALAPPDATA%\Maktaba\node.json, where a new join writes.
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
	if got := NodeConfigPath(getenv, "windows", base); got != oldPath {
		t.Fatalf("both: %s, want the original %s", got, oldPath)
	}
}
