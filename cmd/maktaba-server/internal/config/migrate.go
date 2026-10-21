package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
)

// movedNote is left in the old config directory after a migration.
const movedNote = "MOVED.txt"

// stateFolders are the large state's folders, beside the default data folder on
// macOS and Windows (Config.StateDir) and inside it before.
var stateFolders = []string{"models", "cache", "chroma", "postgres", "backups"}

// MigrateDirs moves an install made before this platform's paths into them
// (MigrateMacDirs, MigrateWindowsDirs) and says what it moved, a line each.
func MigrateDirs() ([]string, error) {
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
// happens with $MAKTABA_CONFIG set or off macOS. It returns the old config
// directory when it moved one.
func MigrateMacDirs() (string, error) {
	if runtime.GOOS != "darwin" || strings.TrimSpace(os.Getenv("MAKTABA_CONFIG")) != "" {
		return "", nil
	}
	return migrateDirs(osMover, xdgDirs(os.Getenv, home()), LocalDirs())
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
// runs it before every command). Each piece moves on its own, whether or not there
// is a server.toml, and only while its new place is empty: nothing is replaced and
// no two folders are merged; a piece whose new place holds something else stays
// where it is, and the line says so. A move that stopped half way (a crash, a full
// disk) is finished by the next start. The pieces:
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
	return migrateWindows(osMover, legacyWindowsDirs(os.Getenv, home()), LocalDirs(), os.Getenv)
}

// migrateWindows is MigrateWindowsDirs from old to cur (tests pass temp dirs and
// their own environment).
func migrateWindows(m mover, old, cur Dirs, getenv func(string) string) ([]string, error) {
	var lines []string
	move := func(mv func(string, string) (bool, error), from, to string) (bool, error) {
		moved, err := mv(from, to)
		switch {
		case errors.Is(err, errKept):
			lines = append(lines, "Left "+from+" where it is: "+to+" already exists, and Maktaba uses that one")
			return false, nil
		case err != nil:
			return false, fmt.Errorf("move %s to %s: %w", from, to, err)
		case moved:
			lines = append(lines, "Moved "+from+" to "+to)
		}
		return moved, nil
	}

	var files [][2]string // from, to
	cfgPath := strings.TrimSpace(getenv("MAKTABA_CONFIG"))
	if cfgPath == "" {
		cfgPath = filepath.Join(cur.Config, "server.toml")
		files = append(files,
			[2]string{filepath.Join(old.Config, "server.toml"), cfgPath},
			[2]string{filepath.Join(old.Keys, "secrets.key"), filepath.Join(cur.Keys, "secrets.key")},
			[2]string{filepath.Join(old.Keys, "stream-signing.key"), filepath.Join(cur.Keys, "stream-signing.key")})
		passwords, err := filepath.Glob(filepath.Join(old.Keys, "postgres-*.password"))
		if err != nil {
			return nil, err
		}
		for _, p := range passwords {
			files = append(files, [2]string{p, filepath.Join(cur.Keys, filepath.Base(p))})
		}
	}
	if strings.TrimSpace(getenv("MAKTABA_NODE_CONFIG")) == "" {
		files = append(files, [2]string{filepath.Join(old.Config, "node.json"), filepath.Join(cur.Config, "node.json")})
	}
	movedOut := false // something left the roaming profile
	for _, f := range files {
		moved, err := move(m.file, f[0], f[1])
		if err != nil {
			return lines, err
		}
		movedOut = movedOut || moved
	}
	if movedOut {
		note := "Maktaba moved this install's settings and keys to " + cur.Config + ".\n"
		_ = os.WriteFile(filepath.Join(old.Config, movedNote), []byte(note), 0o600)
	}

	// The large state, when data_dir is the default (a server.toml that cannot be
	// read moves nothing: the command that loads it says why).
	if data, ok := dataDirOf(cfgPath, cur.Data); ok && filepath.Clean(data) == filepath.Clean(cur.Data) {
		root := filepath.Dir(filepath.Clean(cur.Data))
		for _, d := range stateFolders {
			if _, err := move(m.dir, filepath.Join(cur.Data, d), filepath.Join(root, d)); err != nil {
				return lines, err
			}
		}
	}
	return lines, nil
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
