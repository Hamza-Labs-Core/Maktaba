package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
)

// movedNote is left in the old config directory after a migration.
const movedNote = "MOVED.txt"

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
	return migrateDirs(xdgDirs(os.Getenv, home()), LocalDirs())
}

// migrateDirs moves an install from old to cur (tests call it with temp dirs).
func migrateDirs(old, cur Dirs) (string, error) {
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
		if exists(oldData) {
			// an empty data dir in the new place (made by an earlier command) is fine
			if exists(cur.Data) {
				if err := os.Remove(cur.Data); err != nil {
					return "", fmt.Errorf("cannot move %s to %s: it already exists", oldData, cur.Data)
				}
			}
			if err := os.MkdirAll(filepath.Dir(cur.Data), 0o700); err != nil {
				return "", err
			}
			if err := os.Rename(oldData, cur.Data); err != nil {
				return "", fmt.Errorf("move %s to %s: %w", oldData, cur.Data, err)
			}
		}
		// models and the caches sit beside data/ on a Mac (Config.StateDir); a run
		// that stopped half way finishes here
		if exists(cur.Data) {
			root := filepath.Dir(cur.Data)
			for _, d := range []string{"models", "cache", "chroma", "postgres", "backups"} {
				from := filepath.Join(cur.Data, d)
				if exists(from) && !exists(filepath.Join(root, d)) {
					if err := os.Rename(from, filepath.Join(root, d)); err != nil {
						return "", fmt.Errorf("move %s: %w", from, err)
					}
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
		if exists(dest) {
			continue
		}
		if err := os.Rename(filepath.Join(old.Config, name), dest); err != nil {
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

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
