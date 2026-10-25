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

// HandoverProtocol is the handover this build's supervisor speaks: it keeps the record and
// adopts what a record of this protocol lists. A release says it in its signed
// release.json (handover_protocol: tools/release/genrelease reads this line); a handover
// is made only onto a release that says it (one from before adopts nothing, and would
// start its own TV processes on the ports the handed-over ones hold).
const HandoverProtocol = 1

// ReleaseHandoverProtocol is the handover_protocol of the release in dir (its
// release.json), 0 when it says none or cannot be read.
func ReleaseHandoverProtocol(dir string) int {
	b, err := os.ReadFile(filepath.Join(dir, "release.json"))
	if err != nil {
		return 0
	}
	var m struct {
		HandoverProtocol int `json:"handover_protocol"`
	}
	if json.Unmarshal(b, &m) != nil {
		return 0
	}
	return m.HandoverProtocol
}

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
	// PGID is its process group (POSIX: it leads one); Job its named job (Windows), and
	// JobHeld the launcher's handle to that job (it keeps the job, and its name, alive
	// after the supervisor that made it exits; whoever ends the process closes it).
	PGID      int    `json:"pgid,omitempty"`
	Job       string `json:"job,omitempty"`
	JobHeld   uint64 `json:"job_held,omitempty"`
	JobHolder int    `json:"job_holder,omitempty"` // the launcher holding JobHeld
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
	return h != nil && h.Handover && h.Writer == pid && h.CurrentBoot()
}

// CurrentBoot reports whether the record is of this boot.
func (h *Handover) CurrentBoot() bool { return h != nil && SameBoot(h.BootID, BootID()) }

// SameBoot reports whether two boot ids name one boot: equal, or both the boot time a
// machine without a boot counter reads (Windows' fallback, "t<unix seconds>") within a
// few minutes of each other (it moves with the clock's corrections). A reboot that close
// is still told apart by the processes' start times, which adoption checks.
func SameBoot(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	ta, oka := bootTime(a)
	tb, okb := bootTime(b)
	if !oka || !okb {
		return false
	}
	d := ta - tb
	if d < 0 {
		d = -d
	}
	return d <= bootTimeSlack
}

// bootTimeSlack is how far apart two readings of one boot's time may be.
const bootTimeSlack = 5 * 60

func bootTime(id string) (int64, bool) {
	if !strings.HasPrefix(id, "t") {
		return 0, false
	}
	var n int64
	for _, r := range id[1:] {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int64(r-'0')
	}
	return n, len(id) > 1
}

// TVJobName is the named job the processes that may outlive the supervisor run in on
// Windows (story 35.7 AC4), of the install whose updates directory is updatesDir: the
// install's own (its hash), so another install on the machine (or a test's) never meets
// it. Named kernel objects end with their last handle, so no boot's name outlives it.
func TVJobName(updatesDir string) string {
	abs, err := filepath.Abs(updatesDir)
	if err != nil {
		abs = updatesDir
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(abs))))
	return `Local\Maktaba-tv-` + hex.EncodeToString(sum[:4])
}

// TVChildJobName is the named job of one such process, nested in the boot's job: the
// next supervisor opens it by name to end that process with its descendants.
func TVChildJobName(updatesDir string, pid int) string {
	return TVJobName(updatesDir) + "-" + itoa(pid)
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
