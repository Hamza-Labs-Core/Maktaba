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
// the lock a start holds while it moves or copies an install into this platform's
// paths, and what the last start said about the pieces it left as they are.
const (
	moveLockFile = "paths-move.lock"
	moveReported = "paths-move.json"
)

// moveLockWait is how long a start waits for another one that is moving or copying
// the same install (a serve and a setup step started together after an update).
// These are renames and small copies; a start that has waited this long stops and
// says why, and the next one finds the work done.
var moveLockWait = 30 * time.Second

// stateFolders are the large state's folders, beside the default data folder on
// macOS and Windows (Config.StateDir) and inside it before (a Windows install from
// before Epic 38 keeps them there).
var stateFolders = []string{"models", "cache", "chroma", "postgres", "backups"}

// MigrateDirs brings an install made before this platform's paths into them
// (MigrateMacDirs moves it, MigrateWindowsDirs copies it) and says what it did, a
// line each. exe is the running maktaba-server. Nothing happens inside a desktop
// app's payload (InAppPayload): the app's own files are in these paths from the
// start, and an install from before comes into the app only through the app's move
// (Epic 38 story 38.13), which copies it and keeps the old one for going back.
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

// MigrateWindowsDirs copies what a Windows install kept before Epic 38 in the
// roaming profile into %LOCALAPPDATA%\Maktaba (story 38.1) and says what it copied,
// a line each (main.go runs it before every command outside the app's payload,
// MigrateDirs). The originals are never removed: an older version of the same
// install, a node's pipeline run from a checkout or the node installer's scripts
// may still read them, as 38.13's move into the app keeps them. The copies win:
// this server reads the new place, and the api and the pipeline read it before the
// old one. The pieces (windowsPieces):
//
//   - server.toml, from %APPDATA%\Maktaba to %LOCALAPPDATA%\Maktaba;
//   - the keys (secrets.key, stream-signing.key and the bundled Postgres's
//     postgres-*.password), from %APPDATA%\Maktaba to %LOCALAPPDATA%\Maktaba\keys.
//
// node.json is never copied: a machine has one node config. Every reader
// (NodeConfigPath, streaming, the pipeline, the node installer) takes
// %LOCALAPPDATA%\Maktaba\node.json only when there is no %APPDATA%\Maktaba\node.json,
// and the pipeline writes (a re-join) to the one it read, so a copy would only
// split it in two: a re-join or a hand edit on one side unseen by the other.
//
// Each is copied on its own, whether or not there is a server.toml, and only while
// its new place is empty: a copy is never replaced, and one that differs from its
// original (the older version changed it since) stays as it is, with a line that
// says so once (and again only when that changes). A copy cut short is made again
// by the next start, and one start at a time copies (lockMove). The large state
// (models, cache, chroma, postgres, backups) is not copied: an install from before
// keeps it in its data folder, which stays its state root (Config.StateDir); a
// running Postgres's folder is never touched. With $MAKTABA_CONFIG set, server.toml
// and the keys stay beside that file (where they always were). The node
// installer's own files (node.env, node@<n>.env) stay in %APPDATA%\Maktaba, where
// its scripts read them.
func MigrateWindowsDirs() ([]string, error) {
	if runtime.GOOS != "windows" {
		return nil, nil
	}
	return migrateWindows(osMover, legacyWindowsDirs(os.Getenv, home()), LocalDirs(), os.Getenv, moveLockWait)
}

// piece is one file of a Windows install that is copied into the new place.
type piece struct{ from, to string }

// windowsPieces are what a Windows install from before Epic 38 copies, in order:
// server.toml, the keys (never node.json: MigrateWindowsDirs).
func windowsPieces(old, cur Dirs, getenv func(string) string) ([]piece, error) {
	var ps []piece
	if strings.TrimSpace(getenv("MAKTABA_CONFIG")) == "" {
		ps = append(ps,
			piece{filepath.Join(old.Config, "server.toml"), filepath.Join(cur.Config, "server.toml")},
			piece{filepath.Join(old.Keys, "secrets.key"), filepath.Join(cur.Keys, "secrets.key")},
			piece{filepath.Join(old.Keys, "stream-signing.key"), filepath.Join(cur.Keys, "stream-signing.key")})
		passwords, err := filepath.Glob(filepath.Join(old.Keys, "postgres-*.password"))
		if err != nil {
			return nil, err
		}
		for _, p := range passwords {
			ps = append(ps, piece{p, filepath.Join(cur.Keys, filepath.Base(p))})
		}
	}
	return ps, nil
}

// keptLine says that a piece was not copied: its new place holds something else.
func keptLine(p piece) string {
	return "Did not copy " + p.from + ": " + p.to + " already exists and differs from it, and Maktaba uses " + p.to
}

// migrateWindows is MigrateWindowsDirs from old to cur (tests pass temp dirs, their
// own environment and lock wait).
func migrateWindows(m mover, old, cur Dirs, getenv func(string) string, wait time.Duration) ([]string, error) {
	pieces, err := windowsPieces(old, cur, getenv)
	if err != nil {
		return nil, err
	}
	// The usual start finds every copy made (or kept, and said): it changes nothing
	// and takes no lock. Looking only reads.
	said := readReported(cur.Config)
	pending := false
	for _, p := range pieces {
		if !exists(p.from) {
			pending = pending || said[p.from] != ""
			continue
		}
		if !exists(p.to) {
			pending = true
			continue
		}
		same, err := sameBytes(p.from, p.to)
		pending = pending || err != nil || (same && said[p.from] != "") || (!same && said[p.from] != keptLine(p))
	}
	if !pending {
		return nil, nil
	}
	release, err := lockMove(cur.Config, wait)
	if err != nil {
		return nil, err
	}
	defer release()

	said = readReported(cur.Config) // another start may have just copied
	kept := map[string]string{}     // piece → its line, for the pieces not copied
	looked := map[string]bool{}
	var lines []string
	var copyErr error
	for _, p := range pieces {
		copied, err := m.copy(p.from, p.to)
		if err != nil && !errors.Is(err, errKept) {
			copyErr = fmt.Errorf("copy %s to %s: %w", p.from, p.to, err)
			break
		}
		looked[p.from] = true
		switch {
		case err != nil:
			line := keptLine(p)
			if said[p.from] != line {
				lines = append(lines, line)
			}
			kept[p.from] = line
		case copied:
			lines = append(lines, "Copied "+p.from+" to "+p.to+" (the original stays for older versions)")
		}
	}
	// what a start that stopped early did not look at stays as it was said
	for k, v := range said {
		if !looked[k] {
			kept[k] = v
		}
	}
	if !maps.Equal(kept, said) {
		_ = writeReported(cur.Config, kept) // without it the next start says these again
	}
	return lines, copyErr
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

// readReported is what an earlier start said about the pieces it did not copy
// (piece → its line); none when there is no record or it cannot be read.
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

// writeReported records the pieces not copied (no record when there is none).
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

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
