package layout

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The TV handover across a supervisor restart (Epic 35 story 35.7, design §8.1).
//
// The supervisor keeps <updates>/handover.json as the record of the processes that may
// outlive it (the TV front door and the slots): each one's pid, the OS's start time of
// that pid (so a reused pid is never taken for it), its process group (POSIX) or named
// job (Windows), and its probe. It rewrites the record whenever one of them starts or
// ends. When it restarts for an update it stops every other process, writes the record
// with Handover set and exits 75 without signalling them; the launcher leaves them
// running, and the next supervisor adopts each one that is still the same process and
// answers its probe. On any other exit the launcher stops what the record lists.

// HandoverFile is the record's name in the updates directory.
const HandoverFile = "handover.json"

// EnvLauncherHandover is "1" in the environment of a server started by a launcher that
// leaves the processes a handover lists running when the server exits 75 (and stops
// them on every other exit).
const EnvLauncherHandover = "MAKTABA_LAUNCHER_HANDOVER"

// HandoverChild is one process that may outlive the supervisor.
type HandoverChild struct {
	Role     string `json:"role"`
	Instance string `json:"instance,omitempty"`
	PID      int    `json:"pid"`
	// Start is the OS's start time of PID (ProcessStart): the pid's identity.
	Start uint64 `json:"start"`
	// PGID is its process group (POSIX: it leads one); Job its named job (Windows).
	PGID int    `json:"pgid,omitempty"`
	Job  string `json:"job,omitempty"`
	// Probe is its liveness URL; Admin its admin address.
	Probe string `json:"probe,omitempty"`
	Admin string `json:"admin,omitempty"`
	// Binary is the program it runs and Release the release that program is from (a
	// release a running process uses is never pruned).
	Binary  string `json:"binary,omitempty"`
	Release string `json:"release,omitempty"`
	// Binding is the TV release a slot runs (the supervisor's SlotBinding).
	Binding json.RawMessage `json:"binding,omitempty"`
	// Wanted: an on-demand process that was asked for (the staging slot).
	Wanted bool `json:"wanted,omitempty"`
}

// Handover is the record.
type Handover struct {
	// BootID is the machine's boot the record is of (BootID): one from an earlier boot
	// is ignored and removed (story 35.7 EC3).
	BootID string `json:"boot_id"`
	// Writer is the pid of the supervisor that wrote it.
	Writer int `json:"writer"`
	// Handover: written by a supervisor exiting 75 for its successor to adopt the
	// children; false while a supervisor merely keeps the record.
	Handover  bool      `json:"handover,omitempty"`
	WrittenAt time.Time `json:"written_at"`
	// Job is the boot's named job every child is in (Windows: TVJobName).
	Job      string          `json:"job,omitempty"`
	Children []HandoverChild `json:"children"`
}

// HandoverPath is the record's path in an updates directory.
func HandoverPath(updatesDir string) string { return filepath.Join(updatesDir, HandoverFile) }

// ReadHandover reads the record (nil, nil: there is none).
func ReadHandover(updatesDir string) (*Handover, error) {
	b, err := os.ReadFile(HandoverPath(updatesDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var h Handover
	if err := json.Unmarshal(b, &h); err != nil {
		return nil, err
	}
	return &h, nil
}

// WriteHandover writes the record atomically.
func WriteHandover(updatesDir string, h Handover) error {
	if err := os.MkdirAll(updatesDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(HandoverPath(updatesDir), append(b, '\n'), 0o644)
}

// RemoveHandover removes the record.
func RemoveHandover(updatesDir string) error {
	if err := os.Remove(HandoverPath(updatesDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// HandedOverBy reports whether h is a handover the supervisor with pid wrote on this
// boot (the launcher then leaves its children running).
func (h *Handover) HandedOverBy(pid int) bool {
	return h != nil && h.Handover && h.Writer == pid && h.BootID != "" && h.BootID == BootID()
}

// CurrentBoot reports whether the record is of this boot.
func (h *Handover) CurrentBoot() bool { return h != nil && h.BootID != "" && h.BootID == BootID() }

// TVJobName is the named job the processes that may outlive the supervisor run in on
// Windows (story 35.7 AC4: Local\Maktaba-tv-<boot id>), of the install whose updates
// directory is updatesDir: the install's own, so another install on the machine (or a
// test's) never meets it.
func TVJobName(updatesDir string) string {
	abs, err := filepath.Abs(updatesDir)
	if err != nil {
		abs = updatesDir
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(abs))))
	return `Local\Maktaba-tv-` + sanitizeName(BootID()) + "-" + hex.EncodeToString(sum[:4])
}

// TVChildJobName is the named job of one such process, nested in the boot's job: the
// next supervisor opens it by name to end that process with its descendants.
func TVChildJobName(updatesDir string, pid int) string {
	return TVJobName(updatesDir) + "-" + itoa(pid)
}

func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// ErrNoProcess: no running process has that pid (or it has exited and waits to be
// reaped).
var ErrNoProcess = errors.New("no such process")

// ProcessIs reports whether pid is running and is the process that started at start
// (ProcessStart): not one that reuses its pid.
func ProcessIs(pid int, start uint64) bool {
	if pid <= 0 || start == 0 {
		return false
	}
	got, err := ProcessStart(pid)
	return err == nil && got == start
}
