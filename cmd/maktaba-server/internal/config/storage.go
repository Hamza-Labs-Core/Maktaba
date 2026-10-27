package config

// Where the install keeps its large state, chosen by the owner (Epic 36 story
// 36.15): first-time setup writes [server] data_dir before the database is made,
// and `maktaba-server storage move` (or Settings → Storage) copies an install's
// state to another disk and points data_dir there. Once a location was chosen
// this way it is recorded twice: storage.json beside server.toml names the data
// folder, and a marker file inside that folder carries the same id. A start that
// finds the record but not the marker (an external disk not mounted yet, an
// empty mount point on Linux) waits for the disk and never makes a new empty
// database in its place (StorageMissing).

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

const (
	// StorageRecordFile is the record beside server.toml of the data folder the owner chose.
	StorageRecordFile = "storage.json"
	// StorageMarkerFile is the marker inside that data folder.
	StorageMarkerFile = ".maktaba-storage"
)

// StateRootFolders are the folders the server keeps at its state root
// (Config.StateDir): the large state (stateFolders) and the server's own records
// beside it. A move carries every one of them; on macOS and Windows with the
// default data_dir they sit beside data/, for any other data_dir inside it.
var StateRootFolders = append(append([]string{}, stateFolders...),
	"venv", "state", "switchover", "postgres-programs")

// StateDirFor is the state root this install would have with data_dir set to
// dataDir (StateDir's rule).
func (c Config) StateDirFor(dataDir string) string {
	exe, _ := os.Executable()
	return stateDir(runtime.GOOS, dataDir, LocalDirs().Data, InAppPayload(exe))
}

// DefaultDataDir is this platform's default [server] data_dir.
func DefaultDataDir() string { return defaultDataDir() }

// TOMLDir is the folder of the server.toml this process reads (Path).
func TOMLDir() string { return filepath.Dir(Path()) }

// LockMove takes the lock the install's moves hold (the paths migration's, the
// storage move's) in root, waiting up to wait for another holder.
func LockMove(root string, wait time.Duration) (release func(), err error) {
	return lockMove(root, wait)
}

// StorageRecord is storage.json.
type StorageRecord struct {
	DataDir string    `json:"data_dir"`
	ID      string    `json:"id"`
	At      time.Time `json:"at"`
	By      string    `json:"by"` // setup | move
}

// ReadStorageRecord reads the record in configDir; ok false when there is none.
func ReadStorageRecord(configDir string) (StorageRecord, bool) {
	var r StorageRecord
	b, err := os.ReadFile(filepath.Join(configDir, StorageRecordFile)) //nolint:gosec // the install's own file
	if err != nil || json.Unmarshal(b, &r) != nil || r.DataDir == "" || r.ID == "" {
		return StorageRecord{}, false
	}
	return r, true
}

// MarkStorage makes dataDir the install's chosen data folder: the marker inside
// it (made with the folder when missing), then the record in configDir. by says
// who chose it (setup, move).
func MarkStorage(configDir, dataDir, by string) error {
	dataDir = filepath.Clean(dataDir)
	id, err := markerID(dataDir)
	if err != nil {
		return err
	}
	if id == "" {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		id = hex.EncodeToString(b)
		if err := os.MkdirAll(dataDir, 0o700); err != nil {
			return err
		}
		if err := layout.WriteFileAtomic(filepath.Join(dataDir, StorageMarkerFile), []byte(id+"\n"), 0o600); err != nil {
			return err
		}
	}
	raw, err := json.MarshalIndent(StorageRecord{DataDir: dataDir, ID: id, At: time.Now().UTC(), By: by}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return err
	}
	return layout.WriteFileAtomic(filepath.Join(configDir, StorageRecordFile), append(raw, '\n'), 0o600)
}

// markerID is the id of dataDir's marker ("" when it has none).
func markerID(dataDir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dataDir, StorageMarkerFile)) //nolint:gosec // the install's own file
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// StorageMissing reports the data folder the owner chose when it is not there:
// the record in configDir names dataDir and dataDir holds no marker with its id
// (the disk is not mounted, or a bare mount point stands in its place). An
// install with no record (the default place, or a data_dir set by hand) is
// never missing.
func StorageMissing(configDir, dataDir string) (string, bool) {
	rec, ok := ReadStorageRecord(configDir)
	if !ok || filepath.Clean(rec.DataDir) != filepath.Clean(dataDir) {
		return "", false
	}
	id, err := markerID(rec.DataDir)
	if err == nil && id == rec.ID {
		return "", false
	}
	return rec.DataDir, true
}

// StorageMissing is StorageMissing for this config and the server.toml it came from.
func (c Config) StorageMissing() (string, bool) {
	return StorageMissing(TOMLDir(), c.Server.DataDir)
}

// dataDirLine is a data_dir key line of server.toml.
var dataDirLine = regexp.MustCompile(`^(\s*data_dir\s*=\s*)("(?:[^"\\]|\\.)*"|'[^']*')(.*)$`)

// tableLine is a TOML table header ([server], [[x]]).
var tableLine = regexp.MustCompile(`^\s*\[`)

// SetDataDir rewrites [server] data_dir in the server.toml at path as text: the
// value replaced where the key is, or the key added at the top of [server] (the
// table added when the file has none). Every other line, comments too, stays as
// it is. The file is replaced atomically.
func SetDataDir(path, dataDir string) error {
	raw, err := os.ReadFile(path) //nolint:gosec // the install's own file
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	text := setDataDirText(string(raw), dataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return layout.WriteFileAtomic(path, []byte(text), 0o600)
}

func setDataDirText(text, dataDir string) string {
	value := "data_dir = " + strconv.Quote(dataDir)
	nl := "\n"
	if strings.Contains(text, "\r\n") {
		nl = "\r\n"
	}
	lines := strings.Split(text, nl)
	section, serverAt := "", -1
	for i, l := range lines {
		trim := strings.TrimSpace(l)
		if tableLine.MatchString(l) {
			section = strings.TrimSpace(strings.Trim(trim, "[]"))
			if section == "server" && serverAt < 0 {
				serverAt = i
			}
			continue
		}
		if section == "server" {
			if m := dataDirLine.FindStringSubmatch(l); m != nil {
				lines[i] = m[1] + strconv.Quote(dataDir) + m[3]
				return strings.Join(lines, nl)
			}
		}
	}
	if serverAt >= 0 {
		out := append(append(append([]string{}, lines[:serverAt+1]...), value), lines[serverAt+1:]...)
		return strings.Join(out, nl)
	}
	if text != "" && !strings.HasSuffix(text, nl) {
		text += nl
	}
	if text != "" {
		text += nl
	}
	return text + "[server]" + nl + value + nl
}

// Within reports whether p is root or inside it (both cleaned and absolute).
func Within(p, root string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
