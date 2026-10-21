package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// movedNote is left in the old config directory after a migration.
const movedNote = "MOVED.txt"

// In the new root (%LOCALAPPDATA%\Maktaba, ~/Library/Application Support/Maktaba):
// the lock a start holds while it moves an install into this platform's paths, and
// what the last start said about the pieces it left where they are.
const (
	moveLockFile = "paths-move.lock"
	moveReported = "paths-move.json"
)

// moveLockWait is how long a start waits for another one that is moving the same
// install (a serve and a setup step started together after an update). The moves
// are renames and small copies; a start that has waited this long stops and says
// why, and the next one finds the move done.
var moveLockWait = 30 * time.Second

// stateFolders are the large state's folders, beside the default data folder on
// macOS and Windows (Config.StateDir) and inside it before.
var stateFolders = []string{"models", "cache", "chroma", "postgres", "backups"}

// MigrateDirs moves an install made before this platform's paths into them
// (MigrateMacDirs, MigrateWindowsDirs) and says what it moved, a line each. exe is
// the running maktaba-server. Nothing moves inside a desktop app's payload
// (InAppPayload): the app's own files are in these paths from the start, and an
// install from before comes into the app only through the app's move (Epic 38
// story 38.13), which copies it and keeps the old one for going back. Moving it
// at the app's first setup step would take its files from under it while it still
// runs.
func MigrateDirs(exe string) ([]string, error) {
	if InAppPayload(exe) {
		return nil, nil
	}
	switch runtime.GOOS {
	case "darwin":
		from, err := MigrateMacDirs()
		if err != nil || from == "" {
			return nil, err
		}
		return []string{"Moved this install from " + from + " to " + LocalDirs().Config}, nil
	case "windows":
		return MigrateWindowsDirs()
	}
	return nil, nil
}

// MigrateMacDirs moves a Mac install made before Epic 36 (XDG paths:
// ~/.config/maktaba, ~/.local/share/maktaba) into the Mac paths (story 36.2), once:
// when there is no server.toml in the new place yet and there is one in the old.
// The old default data directory moves whole (models and caches into the state
// root beside it); a data_dir set to another place stays where it is. server.toml
// is rewritten as text (comments kept) with the old data paths replaced. Nothing
// happens with $MAKTABA_CONFIG set or off macOS. One start at a time moves
// (lockMove). It returns the old config directory when it moved one.
func MigrateMacDirs() (string, error) {
	if runtime.GOOS != "darwin" || strings.TrimSpace(os.Getenv("MAKTABA_CONFIG")) != "" {
		return "", nil
	}
	old, cur := xdgDirs(os.Getenv, home()), LocalDirs()
	if exists(filepath.Join(cur.Config, "server.toml")) || !exists(filepath.Join(old.Config, "server.toml")) {
		return "", nil // nothing to move: no lock taken, no folder made
	}
	release, err := lockMove(cur.Config, moveLockWait)
	if err != nil {
		return "", err
	}
	defer release()
	return migrateDirs(osMover, old, cur)
}

// migrateDirs moves a Mac install from old to cur (tests call it with temp dirs).
func migrateDirs(m mover, old, cur Dirs) (string, error) {
	newToml := filepath.Join(cur.Config, "server.toml")
	oldToml := filepath.Join(old.Config, "server.toml")
	if exists(newToml) || !exists(oldToml) {
		return "", nil
	}
	raw, err := os.ReadFile(oldToml)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", oldToml, err)
	}
	cfg := Defaults()
	meta, err := toml.Decode(string(raw), &cfg)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", oldToml, err)
	}
	if err := os.MkdirAll(cur.Config, 0o700); err != nil {
		return "", err
	}
	text := string(raw)

	// 1. The data: only the old default directory (a data_dir the owner chose stays).
	oldData := filepath.Clean(cfg.Server.DataDir)
	if !meta.IsDefined("server", "data_dir") {
		oldData = filepath.Clean(old.Data)
	}
	if oldData == filepath.Clean(old.Data) {
		// an empty data dir in the new place (made by an earlier command) is taken
		if _, err := m.dir(oldData, cur.Data); errors.Is(err, errKept) {
			return "", fmt.Errorf("cannot move %s to %s: it already exists", oldData, cur.Data)
		} else if err != nil {
			return "", fmt.Errorf("move %s to %s: %w", oldData, cur.Data, err)
		}
		// models and the caches sit beside data/ on a Mac (Config.StateDir); a run
		// that stopped half way finishes here
		if exists(cur.Data) {
			root := filepath.Dir(cur.Data)
			for _, d := range stateFolders {
				// one already beside data/ stays as it is
				from := filepath.Join(cur.Data, d)
				if _, err := m.dir(from, filepath.Join(root, d)); err != nil && !errors.Is(err, errKept) {
					return "", fmt.Errorf("move %s: %w", from, err)
				}
			}
			// the old path as a whole value or a prefix (never /x/maktaba-other)
			for _, end := range []string{`"`, `'`, "/"} {
				text = strings.ReplaceAll(text, oldData+end, cur.Data+end)
			}
		}
	}

	// 2. The key files into keys/, everything else beside server.toml.
	entries, err := os.ReadDir(old.Config)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		name := e.Name()
		if name == "server.toml" || name == movedNote {
			continue
		}
		dest := filepath.Join(cur.Config, name)
		if name == "secrets.key" || name == "stream-signing.key" {
			if err := os.MkdirAll(cur.Keys, 0o700); err != nil {
				return "", err
			}
			dest = filepath.Join(cur.Keys, name)
		}
		mv := m.file
		if e.IsDir() {
			mv = m.dir
		}
		// one already in the new place stays as it is
		if _, err := mv(filepath.Join(old.Config, name), dest); err != nil && !errors.Is(err, errKept) {
			return "", fmt.Errorf("move %s: %w", name, err)
		}
	}

	// 3. server.toml last: until it is in place the next start migrates again.
	tmp := newToml + ".migrating"
	if err := os.WriteFile(tmp, []byte(text), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, newToml); err != nil {
		return "", err
	}
	if err := os.Remove(oldToml); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	note := "Maktaba moved this install to " + cur.Config + " (and its data to " + cur.Data + ").\n"
	_ = os.WriteFile(filepath.Join(old.Config, movedNote), []byte(note), 0o600)
	return old.Config, nil
}

// MigrateWindowsDirs moves what a Windows install kept before Epic 38 into
// %LOCALAPPDATA%\Maktaba (story 38.1) and says what it moved, a line each (main.go
// runs it before every command outside the app's payload, MigrateDirs). Each piece
// moves on its own, whether or not there is a server.toml, and only while its new
// place is empty: nothing is replaced and no two folders are merged; a piece whose
// new place holds something else stays where it is, and a line says so once (and
// again only when that changes). A move that stopped half way (a crash, a full
// disk) is finished by the next start, and one start at a time moves (lockMove).
// The pieces (windowsPieces):
//
//   - server.toml, from %APPDATA%\Maktaba to %LOCALAPPDATA%\Maktaba;
//   - the keys (secrets.key, stream-signing.key and the bundled Postgres's
//     postgres-*.password), from %APPDATA%\Maktaba to %LOCALAPPDATA%\Maktaba\keys;
//   - node.json, from %APPDATA%\Maktaba to %LOCALAPPDATA%\Maktaba (until it moves
//     the old one is read, NodeConfigPath);
//   - models, cache, chroma, postgres and backups, from the default data folder
//     (%LOCALAPPDATA%\Maktaba\data, which stays) to beside it, the state root
//     (Config.StateDir), when data_dir is that default.
//
// With $MAKTABA_CONFIG set, server.toml and the keys stay beside that file (where
// they always were); with $MAKTABA_NODE_CONFIG set, node.json stays. The node
// installer's own files (node.env, node@<n>.env) stay in %APPDATA%\Maktaba, where
// its scripts read them.
func MigrateWindowsDirs() ([]string, error) {
	if runtime.GOOS != "windows" {
		return nil, nil
	}
	return migrateWindows(osMover, legacyWindowsDirs(os.Getenv, home()), LocalDirs(), os.Getenv, moveLockWait)
}

// piece is one file or folder of a Windows install that moves.
type piece struct {
	from, to string
	dir      bool
}

// windowsPieces are what a Windows install from before Epic 38 moves, in order:
// server.toml, the keys, node.json, then the large state (only when data_dir is
// the default: a server.toml that cannot be read moves none of it, and the command
// that loads it says why).
func windowsPieces(old, cur Dirs, getenv func(string) string) ([]piece, error) {
	var ps []piece
	cfgPath := strings.TrimSpace(getenv("MAKTABA_CONFIG"))
	if cfgPath == "" {
		oldToml := filepath.Join(old.Config, "server.toml")
		cfgPath = filepath.Join(cur.Config, "server.toml")
		ps = append(ps,
			piece{from: oldToml, to: cfgPath},
			piece{from: filepath.Join(old.Keys, "secrets.key"), to: filepath.Join(cur.Keys, "secrets.key")},
			piece{from: filepath.Join(old.Keys, "stream-signing.key"), to: filepath.Join(cur.Keys, "stream-signing.key")})
		passwords, err := filepath.Glob(filepath.Join(old.Keys, "postgres-*.password"))
		if err != nil {
			return nil, err
		}
		for _, p := range passwords {
			ps = append(ps, piece{from: p, to: filepath.Join(cur.Keys, filepath.Base(p))})
		}
		if !exists(cfgPath) && exists(oldToml) {
			cfgPath = oldToml // its data_dir decides, before it has moved
		}
	}
	if strings.TrimSpace(getenv("MAKTABA_NODE_CONFIG")) == "" {
		ps = append(ps, piece{from: filepath.Join(old.Config, "node.json"), to: filepath.Join(cur.Config, "node.json")})
	}
	if data, ok := dataDirOf(cfgPath, cur.Data); ok && filepath.Clean(data) == filepath.Clean(cur.Data) {
		root := filepath.Dir(filepath.Clean(cur.Data))
		for _, d := range stateFolders {
			ps = append(ps, piece{from: filepath.Join(cur.Data, d), to: filepath.Join(root, d), dir: true})
		}
	}
	return ps, nil
}

// migrateWindows is MigrateWindowsDirs from old to cur (tests pass temp dirs, their
// own environment and lock wait).
func migrateWindows(m mover, old, cur Dirs, getenv func(string) string, wait time.Duration) ([]string, error) {
	pieces, err := windowsPieces(old, cur, getenv)
	if err != nil {
		return nil, err
	}
	pending := false
	for _, p := range pieces {
		pending = pending || exists(p.from) || (p.dir && exists(p.to+movingSuffix))
	}
	if !pending {
		return nil, nil // the usual start: nothing to move, no lock taken
	}
	release, err := lockMove(cur.Config, wait)
	if err != nil {
		return nil, err
	}
	defer release()

	said := readReported(cur.Config)
	left := map[string]string{} // piece → its line, for the pieces left where they are
	looked := map[string]bool{}
	var lines []string
	movedOut := false // something left the roaming profile
	var moveErr error
	for _, p := range pieces {
		mv := m.file
		if p.dir {
			mv = m.dir
		}
		moved, err := mv(p.from, p.to)
		if err != nil && !errors.Is(err, errKept) {
			moveErr = fmt.Errorf("move %s to %s: %w", p.from, p.to, err)
			break
		}
		looked[p.from] = true
		switch {
		case err != nil:
			line := "Left " + p.from + " where it is: " + p.to + " already exists, and Maktaba uses that one"
			if said[p.from] != line {
				lines = append(lines, line)
			}
			left[p.from] = line
		case moved:
			lines = append(lines, "Moved "+p.from+" to "+p.to)
			movedOut = movedOut || !p.dir
		}
	}
	if movedOut {
		note := "Maktaba moved this install's settings and keys to " + cur.Config + ".\n"
		_ = os.WriteFile(filepath.Join(old.Config, movedNote), []byte(note), 0o600)
	}
	// what a start that stopped early did not look at stays as it was said
	for k, v := range said {
		if !looked[k] {
			left[k] = v
		}
	}
	if !maps.Equal(left, said) {
		_ = writeReported(cur.Config, left) // without it the next start says these again
	}
	return lines, moveErr
}

// lockMove takes the move lock in root (LockFileEx on Windows, flock elsewhere),
// waiting up to wait for another start that holds it; release gives it back.
func lockMove(root string, wait time.Duration) (release func(), err error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(root, moveLockFile)
	deadline := time.Now().Add(wait)
	for {
		f, err := layout.LockFile(path)
		if err == nil {
			return func() { layout.Unlock(f) }, nil
		}
		if !errors.Is(err, layout.ErrLocked) {
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("another maktaba-server has been moving this install for %s (it holds %s): "+
				"run this again when it is done", wait, path)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// readReported is what an earlier start said about the pieces it left where they
// are (piece → its line); none when there is no record or it cannot be read.
func readReported(root string) map[string]string {
	var r struct {
		Left map[string]string `json:"left"`
	}
	b, err := os.ReadFile(filepath.Join(root, moveReported)) //nolint:gosec // the install's own file
	if err != nil || json.Unmarshal(b, &r) != nil || r.Left == nil {
		return map[string]string{}
	}
	return r.Left
}

// writeReported records the pieces left where they are (no record when none is).
func writeReported(root string, left map[string]string) error {
	path := filepath.Join(root, moveReported)
	if len(left) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	b, err := json.MarshalIndent(struct {
		Left map[string]string `json:"left"`
	}{left}, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// dataDirOf is the data_dir of the server.toml at path, or def when it names none
// or there is no server.toml; ok is false when the file cannot be read or parsed.
func dataDirOf(path, def string) (dir string, ok bool) {
	raw, err := os.ReadFile(path) //nolint:gosec // the configured server.toml
	if errors.Is(err, fs.ErrNotExist) {
		return def, true
	}
	if err != nil {
		return "", false
	}
	var c struct {
		Server struct {
			DataDir string `toml:"data_dir"`
		} `toml:"server"`
	}
	c.Server.DataDir = def
	if _, err := toml.Decode(string(raw), &c); err != nil {
		return "", false
	}
	return c.Server.DataDir, true
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
