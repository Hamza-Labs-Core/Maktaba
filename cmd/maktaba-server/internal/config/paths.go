package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Dirs is where an install keeps its files when nothing overrides them (Epic 36
// story 36.2, Epic 38 story 38.1):
//
//	macOS   : ~/Library/Application Support/Maktaba (server.toml, keys/, data/,
//	          models/, chroma/, postgres/, cache/, backups/), logs in ~/Library/Logs/Maktaba
//	Linux   : $XDG_CONFIG_HOME/maktaba, $XDG_DATA_HOME/maktaba, $XDG_STATE_HOME/maktaba/logs
//	Windows : %LOCALAPPDATA%\Maktaba (server.toml, keys\, data\, logs\, models\, chroma\,
//	          postgres\, cache\, backups\) for a new install: nothing in the roaming
//	          profile. An install from before keeps its originals in %APPDATA%\Maktaba
//	          (its config and keys copied by MigrateWindowsDirs, its node.json read
//	          there, NodeConfigPath) and its large state in data\ (StateDir)
type Dirs struct {
	Config string // holds server.toml
	Data   string // the default [server] data_dir
	Logs   string
	Keys   string // secrets.key, stream-signing.key
}

// DirsFor resolves Dirs for an OS (tests pass their own environment and home).
func DirsFor(getenv func(string) string, goos, userHome string) Dirs {
	switch goos {
	case "darwin":
		root := filepath.Join(userHome, "Library", "Application Support", "Maktaba")
		return Dirs{
			Config: root,
			Data:   filepath.Join(root, "data"),
			Logs:   filepath.Join(userHome, "Library", "Logs", "Maktaba"),
			Keys:   filepath.Join(root, "keys"),
		}
	case "windows":
		root := WindowsRoot(getenv, userHome)
		return Dirs{
			Config: root,
			Data:   filepath.Join(root, "data"),
			Logs:   filepath.Join(root, "logs"),
			Keys:   filepath.Join(root, "keys"),
		}
	}
	return xdgDirs(getenv, userHome)
}

// WindowsRoot is %LOCALAPPDATA%\Maktaba: the install's home on Windows (the app's
// program and every per-user file, Epic 38).
func WindowsRoot(getenv func(string) string, userHome string) string {
	local := getenv("LOCALAPPDATA")
	if local == "" {
		local = filepath.Join(userHome, "AppData", "Local")
	}
	return filepath.Join(local, "Maktaba")
}

// legacyWindowsDirs is the Windows layout before Epic 38: server.toml and the keys
// in the roaming profile (%APPDATA%\Maktaba), the data and logs already local.
func legacyWindowsDirs(getenv func(string) string, userHome string) Dirs {
	roaming := getenv("APPDATA")
	if roaming == "" {
		roaming = filepath.Join(userHome, "AppData", "Roaming")
	}
	cfg := filepath.Join(roaming, "Maktaba")
	root := WindowsRoot(getenv, userHome)
	return Dirs{
		Config: cfg,
		Data:   filepath.Join(root, "data"),
		Logs:   filepath.Join(root, "logs"),
		Keys:   cfg,
	}
}

// xdgDirs is the XDG layout (Linux, and an install a Mac made before Epic 36).
func xdgDirs(getenv func(string) string, userHome string) Dirs {
	base := func(env, def string) string {
		if v := getenv(env); v != "" {
			return v
		}
		return filepath.Join(userHome, def)
	}
	cfg := filepath.Join(base("XDG_CONFIG_HOME", ".config"), "maktaba")
	return Dirs{
		Config: cfg,
		Data:   filepath.Join(base("XDG_DATA_HOME", filepath.Join(".local", "share")), "maktaba"),
		Logs:   filepath.Join(base("XDG_STATE_HOME", filepath.Join(".local", "state")), "maktaba", "logs"),
		Keys:   cfg,
	}
}

// LocalDirs is DirsFor this process.
func LocalDirs() Dirs { return DirsFor(os.Getenv, runtime.GOOS, home()) }

// KeysDir is where the home's key files live: beside server.toml when
// $MAKTABA_CONFIG names it (tests, containers), else the platform's keys dir.
func KeysDir() string {
	if strings.TrimSpace(os.Getenv("MAKTABA_CONFIG")) != "" {
		return filepath.Dir(Path())
	}
	return LocalDirs().Keys
}

// LogsDir is where the server's logs go: $MAKTABA_LOG_DIR, else the platform's.
func LogsDir() string {
	if v := strings.TrimSpace(os.Getenv("MAKTABA_LOG_DIR")); v != "" {
		return v
	}
	return LocalDirs().Logs
}

// StateDir is the root of the install's large state (models/, chroma/, cache/,
// postgres/, backups/). On macOS and Windows the default data_dir sits inside that
// root (~/Library/Application Support/Maktaba/data, %LOCALAPPDATA%\Maktaba\data), so
// the root is its parent; on Linux, and for any data_dir the owner chose, it is
// data_dir itself (where these always lived on Linux). A Windows install from
// before Epic 38 keeps them in its default data_dir, where its older versions (and
// a Postgres of its own that may be running) use them: outside the app's payload
// (InAppPayload), that install's state root is its data_dir. It is decided once, at
// the first command (recordStateRoot), and read from the record after that
// (stateInData). Nothing moves or copies them (story 38.1).
func (c Config) StateDir() string {
	exe, _ := os.Executable()
	return stateDir(runtime.GOOS, c.Server.DataDir, LocalDirs().Data, InAppPayload(exe))
}

// StateDirOutsideApp is StateDir as a release install outside the app resolves
// it, wherever this runs: the same record (stateInData), or the folders while that
// install has none (it never ran a version that writes one). It is for the app's
// move (Epic 38 story 38.13), which runs inside the app and reads an install from
// before with that install's own rule. It never writes the record.
func (c Config) StateDirOutsideApp() string {
	return stateDir(runtime.GOOS, c.Server.DataDir, LocalDirs().Data, false)
}

func stateDir(goos, dataDir, defaultData string, inApp bool) string {
	if (goos == "darwin" || goos == "windows") && dataDir != "" && filepath.Clean(dataDir) == filepath.Clean(defaultData) {
		root := filepath.Dir(filepath.Clean(dataDir))
		if goos == "windows" && !inApp && stateInData(root, dataDir) {
			return dataDir
		}
		return root
	}
	return dataDir
}

// StateRecordPath is the record of a Windows install's state root (stateInData),
// which uninstall --purge removes with the data; "" off Windows and inside the
// app's payload (exe is the running maktaba-server; the record there is an older
// install's).
func StateRecordPath(exe string) string {
	if runtime.GOOS != "windows" || InAppPayload(exe) {
		return ""
	}
	return filepath.Join(filepath.Dir(LocalDirs().Data), stateRootRecord)
}

// stateRootRecord, beside the default data folder (%LOCALAPPDATA%\Maktaba), keeps
// where a Windows install outside the app keeps its large state: "data" (an install
// from before Epic 38) or "root" (beside data\).
const stateRootRecord = "state-root.json"

// stateInData reports whether a Windows install outside the app keeps its large
// state in its default data folder: as root\state-root.json records it
// (recordStateRoot, at the first command), else as the folders say (dataDir holds
// one of stateFolders: an install from before). Reading never writes: the record
// is made once, at a command's start, before anything resolves the state root.
func stateInData(root, dataDir string) bool {
	if state, ok := readStateRoot(root); ok {
		return state == "data"
	}
	return holdsState(dataDir)
}

// readStateRoot is the recorded state root ("data" or "root"), if any.
func readStateRoot(root string) (string, bool) {
	var rec struct {
		State string `json:"state"`
	}
	b, err := os.ReadFile(filepath.Join(root, stateRootRecord)) //nolint:gosec // the install's own file
	if err != nil || json.Unmarshal(b, &rec) != nil || (rec.State != "data" && rec.State != "root") {
		return "", false
	}
	return rec.State, true
}

// recordStateRoot decides once where a Windows install outside the app keeps its
// large state and records it in root\state-root.json, which every later
// resolution reads (stateInData): a folder that appears in data\ afterwards (an
// older version run once) never moves the state root, and the bundled Postgres
// never starts an empty cluster beside the real one. MigrateWindowsDirs runs it at
// every command's start; it decides only once data\ exists (until then nothing is
// there to decide on) and never replaces a record (the first start to decide wins).
func recordStateRoot(root, dataDir string) error {
	if _, ok := readStateRoot(root); ok {
		return nil
	}
	if info, err := os.Stat(dataDir); err != nil || !info.IsDir() {
		return nil
	}
	state := "root"
	if holdsState(dataDir) {
		state = "data"
	}
	err := writeNew(filepath.Join(root, stateRootRecord), struct {
		State string `json:"state"`
	}{state})
	if errors.Is(err, fs.ErrExist) {
		return nil // another start decided first
	}
	return err
}

// writeNew writes v as JSON at path unless a file is there (renameNoReplace).
func writeNew(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(append(b, '\n'))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = renameNoReplace(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// holdsState: dir holds one of the large state's folders (stateFolders).
func holdsState(dir string) bool {
	for _, d := range stateFolders {
		if exists(filepath.Join(dir, d)) {
			return true
		}
	}
	return false
}
