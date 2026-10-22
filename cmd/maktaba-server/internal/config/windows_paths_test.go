package config

import (
	"errors"
	"io/fs"
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
	env = map[string]string{"APPDATA": filepath.Join(base, "Roaming"), "LOCALAPPDATA": filepath.Join(base, "Local"),
		machineIDEnv: "this-pc"} // never this PC's real MachineGuid
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

// uninstall --purge's list (WindowsOriginals): the originals the copy keeps, and its
// lock and record; never node.json; none with $MAKTABA_CONFIG. Once they and their
// copies are gone, the next command copies nothing back.
func TestWindowsOriginals(t *testing.T) {
	t.Parallel()
	old, cur, env := winLayout(t)
	plant(t, old.Config, map[string]string{"server.toml": "[server]\n", "secrets.key": "S", "postgres-maktaba.password": "P", "node.json": "{}"})
	migrateWin(t, osMover, old, cur, env)
	getenv := func(k string) string { return env[k] }
	got := windowsOriginals(old, cur, getenv)
	want := []string{
		filepath.Join(old.Config, "server.toml"), filepath.Join(old.Keys, "secrets.key"),
		filepath.Join(old.Keys, "stream-signing.key"), filepath.Join(old.Keys, "postgres-maktaba.password"),
		filepath.Join(cur.Config, moveLockFile), filepath.Join(cur.Config, moveReported),
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("originals:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, p := range append(got, filepath.Join(cur.Config, "server.toml"), cur.Keys) {
		if err := os.RemoveAll(p); err != nil {
			t.Fatal(err)
		}
	}
	if lines := migrateWin(t, osMover, old, cur, env); len(lines) != 0 || exists(filepath.Join(cur.Keys, "secrets.key")) {
		t.Fatalf("after the purge nothing is copied back (%q)", lines)
	}
	env["MAKTABA_CONFIG"] = filepath.Join(t.TempDir(), "server.toml")
	if got := windowsOriginals(old, cur, getenv); got != nil {
		t.Fatalf("with $MAKTABA_CONFIG the roaming profile's are not this install's: %q", got)
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

// The roaming profile's node config is the PC's that claimed it (its machine_id):
// an unclaimed one and this PC's own are read and run; another PC's is skipped, so
// this PC is not that machine, its join writes the local file, the other PC's stays
// as it was, and serve and setup's check say why it runs unjoined.
func TestNodeConfigClaimedByAnotherPC(t *testing.T) {
	t.Parallel()
	old, cur, env := winLayout(t)
	getenv := func(k string) string { return env[k] }
	base := filepath.Dir(filepath.Dir(cur.Config))
	original, local := filepath.Join(old.Config, "node.json"), filepath.Join(cur.Config, "node.json")

	writeAt(t, original, `{"node_id":"n1","private_key":"k","database_url":"postgres://h"}`) // unclaimed
	if got := NodeConfigPath(getenv, "windows", base); got != original {
		t.Fatalf("unclaimed: %s, want the original (it runs, and the pipeline claims it)", got)
	}
	if p, _ := ForeignNodeConfig(getenv, "windows", base); p != "" {
		t.Fatalf("unclaimed is not another PC's: %s", p)
	}

	writeAt(t, original, `{"node_id":"n1","private_key":"k","database_url":"postgres://h","machine_id":"THIS-PC"}`)
	if got := NodeConfigPath(getenv, "windows", base); got != original {
		t.Fatalf("this PC's own (case aside): %s, want the original", got)
	}

	theirs := `{"node_id":"n1","private_key":"k","database_url":"postgres://h","machine_id":"other-pc"}`
	writeAt(t, original, theirs)
	if got := NodeConfigPath(getenv, "windows", base); got != local {
		t.Fatalf("another PC's: %s, want this PC's own place %s", got, local)
	}
	if n := ReadNodeConfig(getenv, "windows", base, os.ReadFile); n.Joined() || n.Err != nil {
		t.Fatalf("this PC is not that machine: %+v", n)
	}
	p, owner := ForeignNodeConfig(getenv, "windows", base)
	if p != original || owner != "other-pc" || !strings.Contains(ForeignNodeLine(p, owner), "join it as its own machine") {
		t.Fatalf("foreign: %q %q", p, owner)
	}
	// this PC's join writes where NodeConfigPath says: the local file
	writeAt(t, NodeConfigPath(getenv, "windows", base), `{"node_id":"n2","private_key":"k2","database_url":"postgres://h","machine_id":"this-pc"}`)
	if n := ReadNodeConfig(getenv, "windows", base, os.ReadFile); n.NodeID != "n2" {
		t.Fatalf("after this PC's join: %+v", n)
	}
	// joined as its own machine, the other PC's original is nothing to this PC:
	// nothing says it runs unjoined because of it (serve, check, the move's plan)
	if p, owner := ForeignNodeConfig(getenv, "windows", base); p != "" || owner != "" {
		t.Fatalf("joined locally, still another PC's: %q %q", p, owner)
	}
	if readAt(t, original) != theirs {
		t.Fatal("the other PC's node config must stay as it was")
	}

	// a PC whose id cannot be known never skips a config (no working node goes down)
	if claimedElsewhere(original, func() string { return "" }) {
		t.Fatal("an unknown machine id must not skip a node config")
	}
}

// A claim whose ReplaceFileW failed with 1177 and whose putting back failed too
// (cluster/config.py) leaves the roaming node.json under its backup name and the
// claimed copy as a temp file. That is still this PC's roaming config: the default path
// resolves it (no local one is taken), and it is read as the pipeline's next load puts
// it back (the backup, else the temp file; never a cut one), so the node stays joined.
// Another PC's leftovers stay that PC's, and another node config's are never
// node.json's.
func TestNodeConfigLeftByAFailedClaim(t *testing.T) {
	t.Parallel()
	_, cur, env := winLayout(t)
	getenv := func(k string) string { return env[k] }
	base := filepath.Dir(filepath.Dir(cur.Config))
	dir := filepath.Join(env["APPDATA"], "Maktaba")
	original, local := filepath.Join(dir, "node.json"), filepath.Join(cur.Config, "node.json")
	bak := filepath.Join(dir, ".node.json-1792600000-4242-0badf00d.bak")
	tmp := filepath.Join(dir, ".node.json-1792600000-4242-0badf00d.tmp")
	unclaimed := `{"node_id":"n1","private_key":"k","database_url":"postgres://h"}`
	writeAt(t, bak, unclaimed)
	writeAt(t, tmp, `{"node_id":"n1","private_key":"k","database_url":"postgres://h","machine_id":"this-pc"}`)
	hourAgo := time.Now().Add(-time.Hour) // ReplaceFileW's backup keeps the old file's mtime
	if err := os.Chtimes(bak, hourAgo, hourAgo); err != nil {
		t.Fatal(err)
	}
	read := func() NodeConfig { return ReadNodeConfig(getenv, "windows", base, os.ReadFile) }

	if got := NodeConfigPath(getenv, "windows", base); got != original {
		t.Fatalf("left by a failed claim: %s, want the roaming config %s", got, original)
	}
	if n := read(); n.Err != nil || !n.Joined() || n.NodeID != "n1" || n.MachineID != "" {
		t.Fatalf("the old file, as the pipeline puts it back: %+v (err %v)", n, n.Err)
	}
	if p, _ := ForeignNodeConfig(getenv, "windows", base); p != "" {
		t.Fatalf("this PC's own leftovers are not another PC's: %s", p)
	}

	if err := os.Remove(bak); err != nil {
		t.Fatal(err)
	}
	if n := read(); !n.Joined() || n.MachineID != "this-pc" {
		t.Fatalf("only the new copy: it is the config: %+v (err %v)", n, n.Err)
	}

	writeAt(t, tmp, `{"node_id":"n1","private_key":"k","data`)
	if got := NodeConfigPath(getenv, "windows", base); got != local {
		t.Fatalf("a cut temp file is no config: %s, want %s", got, local)
	}
	if n := read(); n.Joined() || n.Err != nil {
		t.Fatalf("a cut temp file is no config: %+v (err %v)", n, n.Err)
	}

	writeAt(t, original, "{not json")
	writeAt(t, bak, unclaimed)
	if n := read(); n.Err != nil || !n.Joined() || n.NodeID != "n1" {
		t.Fatalf("node.json broken, a whole backup: the backup: %+v (err %v)", n, n.Err)
	}
	held := func(p string) ([]byte, error) { // another program holds node.json
		if p == original {
			return nil, fs.ErrPermission
		}
		return os.ReadFile(p) //nolint:gosec // the test's temp files
	}
	if n := ReadNodeConfig(getenv, "windows", base, held); !errors.Is(n.Err, fs.ErrPermission) {
		t.Fatalf("a node.json that cannot be read is never read past: %+v (err %v)", n, n.Err)
	}

	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	writeAt(t, bak, `{"node_id":"n9","private_key":"k","database_url":"postgres://h","machine_id":"other-pc"}`)
	if got := NodeConfigPath(getenv, "windows", base); got != local {
		t.Fatalf("another PC's leftovers: %s, want this PC's own place %s", got, local)
	}
	if p, owner := ForeignNodeConfig(getenv, "windows", base); p != bak || owner != "other-pc" {
		t.Fatalf("another PC's leftovers are named: %q %q", p, owner)
	}

	other := filepath.Join(t.TempDir(), "node-b.json")
	writeAt(t, filepath.Join(filepath.Dir(other), ".node-a.json-1792600000-4242-0badf00d.bak"), unclaimed)
	if n := ReadNodeConfig(func(k string) string {
		if k == "MAKTABA_NODE_CONFIG" {
			return other
		}
		return ""
	}, "windows", base, os.ReadFile); n.Joined() || n.Err != nil {
		t.Fatalf("another node config's leftovers are not this one's: %+v (err %v)", n, n.Err)
	}
}

// A node.json an editor saved with a UTF-8 BOM (Notepad, PowerShell 5.1) is read past it,
// as the pipeline reads it (utf-8-sig): it is the node config, never taken for broken with
// a backup read instead, and its owner is read too. The broken node.json the pipeline
// keeps aside when a leftover takes its name (node.json.broken-*) is no leftover: it is
// never read as the node config, even when it parses.
func TestNodeConfigWithABOMAndAKeptBrokenOne(t *testing.T) {
	t.Parallel()
	_, cur, env := winLayout(t)
	getenv := func(k string) string { return env[k] }
	base := filepath.Dir(filepath.Dir(cur.Config))
	dir := filepath.Join(env["APPDATA"], "Maktaba")
	original, local := filepath.Join(dir, "node.json"), filepath.Join(cur.Config, "node.json")
	bak := filepath.Join(dir, ".node.json-1792600000-4242-0badf00d.bak")
	const bom = "\xef\xbb\xbf"
	writeAt(t, original, bom+`{"node_id":"n2","private_key":"k","database_url":"postgres://two"}`)
	writeAt(t, bak, `{"node_id":"n1","private_key":"k","database_url":"postgres://one"}`)
	if got := nodeConfigFile(original, os.ReadFile); got != original {
		t.Fatalf("a BOM'd node.json is whole: read from %s", got)
	}
	if n := ReadNodeConfig(getenv, "windows", base, os.ReadFile); n.Err != nil || !n.Joined() || n.NodeID != "n2" {
		t.Fatalf("a BOM'd node.json: %+v (err %v)", n, n.Err)
	}
	writeAt(t, original, bom+`{"node_id":"n2","private_key":"k","database_url":"postgres://two","machine_id":"other-pc"}`)
	if got := NodeConfigPath(getenv, "windows", base); got != local {
		t.Fatalf("another PC's, past its BOM: %s, want %s", got, local)
	}
	if p, owner := ForeignNodeConfig(getenv, "windows", base); p != original || owner != "other-pc" {
		t.Fatalf("another PC's, past its BOM: %q %q", p, owner)
	}

	for _, p := range []string{original, bak} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	writeAt(t, filepath.Join(dir, "node.json.broken-1792600000-3fa1"), `{"node_id":"n3","private_key":"k","database_url":"postgres://three"}`)
	if got := NodeConfigPath(getenv, "windows", base); got != local {
		t.Fatalf("a kept broken node.json is no node config: %s, want %s", got, local)
	}
	if n := ReadNodeConfig(getenv, "windows", base, os.ReadFile); n.Joined() || n.Err != nil {
		t.Fatalf("a kept broken node.json is no node config: %+v (err %v)", n, n.Err)
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
