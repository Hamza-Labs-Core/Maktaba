package channel

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Cluster roles (as the pipeline's cluster code names them).
const (
	RoleHome = "home"
	RoleNode = "node"
)

// HostIdentity is who this streaming host is in the cluster.
type HostIdentity struct {
	// ID is stable across restarts: a joined node's node_id, the home's
	// instance id.
	ID string
	// Host is the host name (channel_runtime.host).
	Host string
	// Role is home (the main instance: the front door for HDHomeRun/SSDP,
	// XMLTV, M3U and the web) or node (a machine that joined it).
	Role string
	// StreamBaseURL is where this host's streaming port is reached
	// (MAKTABA_PUBLIC_BASE_URL); the home sends a viewer there.
	StreamBaseURL string
}

// IsHome reports whether this host is the home.
func (h HostIdentity) IsHome() bool { return h.Role != RoleNode }

// nodeConfig is the part of the pipeline's node config (cluster/config.py)
// that says whether this machine joined a home.
type nodeConfig struct {
	NodeID      string `json:"node_id"`
	DatabaseURL string `json:"database_url"`
}

// NodeConfigPath is where the pipeline keeps this machine's node config
// (cluster/config.py config_path): MAKTABA_NODE_CONFIG, else per OS. On Windows the
// machine's one node config stays where it is: the roaming profile's
// %APPDATA%\Maktaba\node.json of a machine that joined before Epic 38, while it is
// this PC's (its machine_id is this PC's MachineGuid) or no PC's yet; else
// %LOCALAPPDATA%\Maktaba\node.json. One another PC claimed is skipped: this PC is not
// that machine (the same rule as maktaba-server and the pipeline). What a write that
// could not finish left of the roaming node.json counts as it (NodeConfigFile).
func NodeConfigPath(getenv func(string) string, goos, userHome string) string {
	if p := strings.TrimSpace(getenv("MAKTABA_NODE_CONFIG")); p != "" {
		if strings.HasPrefix(p, "~") {
			p = filepath.Join(userHome, strings.TrimPrefix(p, "~"))
		}
		return p
	}
	switch goos {
	case "darwin":
		return filepath.Join(userHome, "Library", "Application Support", "Maktaba", "node.json")
	case "windows":
		roaming := getenv("APPDATA")
		if roaming == "" {
			roaming = filepath.Join(userHome, "AppData", "Roaming")
		}
		old := filepath.Join(roaming, "Maktaba", "node.json")
		if held := heldNodeConfig(old); held != "" && !claimedElsewhere(held, getenv) {
			return old
		}
		local := getenv("LOCALAPPDATA")
		if local == "" {
			local = filepath.Join(userHome, "AppData", "Local")
		}
		return filepath.Join(local, "Maktaba", "node.json")
	}
	base := getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(userHome, ".config")
	}
	return filepath.Join(base, "maktaba", "node.json")
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// legacyNodeLeftover matches node.json's leftovers as they were named before they
// carried the file's name (cluster/config.py _LEGACY_LEFTOVER).
var legacyNodeLeftover = regexp.MustCompile(`^\.node-(\d+-[0-9a-f]{8}\.bak|[a-z0-9_]{8}\.tmp)$`)

// NodeConfigFile is the file that holds the node config at path, as the pipeline, its
// one writer, reads it (cluster/config.py _source; maktaba-server's nodeConfigFile):
// path, unless it is missing or holds no whole node config (a node id and a private
// key; one that cannot be read is read as it is). Then what the pipeline's next load
// puts in its place: a write that could not finish (ReplaceFileW failing half way)
// leaves the old file under a backup name and the new one as a temp file beside it
// (.<name>-<time>-<pid>-<hex>.bak and .tmp), and the newest whole backup is taken, else
// the newest whole temp file. Else path.
func NodeConfigFile(path string, readFile func(string) ([]byte, error)) string {
	if b, err := readFile(path); (err == nil && wholeNodeConfig(b)) || (err != nil && !errors.Is(err, fs.ErrNotExist)) {
		return path
	}
	dir, base := filepath.Dir(path), filepath.Base(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return path
	}
	for _, ext := range []string{".bak", ".tmp"} {
		best, newest := "", time.Time{}
		for _, e := range entries {
			if filepath.Ext(e.Name()) != ext || !isNodeConfigLeftover(base, e.Name()) {
				continue
			}
			p := filepath.Join(dir, e.Name())
			info, err := e.Info()
			if err != nil {
				continue
			}
			if b, err := readFile(p); err != nil || !wholeNodeConfig(b) {
				continue
			}
			if best == "" || info.ModTime().After(newest) {
				best, newest = p, info.ModTime()
			}
		}
		if best != "" {
			return best
		}
	}
	return path
}

// isNodeConfigLeftover: name is what a write of the node config base left beside it
// (cluster/config.py _leftovers).
func isNodeConfigLeftover(base, name string) bool {
	if strings.HasPrefix(name, "."+base+"-") && (strings.HasSuffix(name, ".bak") || strings.HasSuffix(name, ".tmp")) {
		return true
	}
	return base == "node.json" && legacyNodeLeftover.MatchString(name)
}

// WithoutBOM is a node config's bytes as UTF-8, the way every reader reads them (the
// pipeline's _text, maktaba-server's withoutBOM, the installer's Get-Content): a
// node.json an editor saved with a byte order mark is read in that encoding (Notepad and
// PowerShell 5.1's Set-Content -Encoding UTF8 write UTF-8 with one, its Out-File and >
// UTF-16; UTF-32 too), and one without is UTF-8. Text that does not decode strictly
// gives no bytes (broken), the same for every reader: a UTF-16 lone surrogate, an ANSI
// file with a byte that is not UTF-8.
func WithoutBOM(b []byte) []byte {
	switch {
	case bytes.HasPrefix(b, []byte("\xef\xbb\xbf")):
		b = b[3:]
	case bytes.HasPrefix(b, []byte("\xff\xfe\x00\x00")):
		return decodeUTF32(b[4:], binary.LittleEndian)
	case bytes.HasPrefix(b, []byte("\x00\x00\xfe\xff")):
		return decodeUTF32(b[4:], binary.BigEndian)
	case bytes.HasPrefix(b, []byte("\xff\xfe")):
		return decodeUTF16(b[2:], binary.LittleEndian)
	case bytes.HasPrefix(b, []byte("\xfe\xff")):
		return decodeUTF16(b[2:], binary.BigEndian)
	}
	if !utf8.Valid(b) {
		return nil // not UTF-8 (an ANSI file with a byte that is not): broken for every reader
	}
	return b
}

// decodeUTF16 is UTF-16 text (after its BOM) as UTF-8; nil when it is not whole.
func decodeUTF16(b []byte, order binary.ByteOrder) []byte {
	if len(b)%2 != 0 {
		return nil
	}
	units := make([]uint16, len(b)/2)
	for i := range units {
		units[i] = order.Uint16(b[2*i:])
	}
	var out []byte
	for i := 0; i < len(units); i++ {
		r := rune(units[i])
		switch {
		case utf16.IsSurrogate(r) && i+1 < len(units):
			r = utf16.DecodeRune(r, rune(units[i+1]))
			i++
			if r == utf8.RuneError {
				return nil
			}
		case utf16.IsSurrogate(r):
			return nil
		}
		out = utf8.AppendRune(out, r)
	}
	return out
}

// decodeUTF32 is UTF-32 text (after its BOM) as UTF-8; nil when it is not whole.
func decodeUTF32(b []byte, order binary.ByteOrder) []byte {
	if len(b)%4 != 0 {
		return nil
	}
	var out []byte
	for i := 0; i < len(b); i += 4 {
		r := rune(order.Uint32(b[i:]))
		if !utf8.ValidRune(r) {
			return nil
		}
		out = utf8.AppendRune(out, r)
	}
	return out
}

// wholeNodeConfig: b is a node config with its identity (not a cut file).
func wholeNodeConfig(b []byte) bool {
	var v struct {
		NodeID     string `json:"node_id"`
		PrivateKey string `json:"private_key"`
	}
	return json.Unmarshal(WithoutBOM(b), &v) == nil && strings.TrimSpace(v.NodeID) != "" && strings.TrimSpace(v.PrivateKey) != ""
}

// claimedElsewhere: the node config at path records another PC (its machine_id)
// than this one ($MAKTABA_MACHINE_ID, else Windows' MachineGuid). One with no
// machine_id, an unreadable one, or a PC whose id cannot be known is never skipped.
func claimedElsewhere(path string, getenv func(string) string) bool {
	b, err := os.ReadFile(path) //nolint:gosec // this machine's node config
	if err != nil {
		return false
	}
	var v struct {
		MachineID string `json:"machine_id"`
	}
	if json.Unmarshal(WithoutBOM(b), &v) != nil || strings.TrimSpace(v.MachineID) == "" {
		return false
	}
	this := strings.TrimSpace(getenv("MAKTABA_MACHINE_ID"))
	if this == "" {
		this = machineGUID()
	}
	return this != "" && !strings.EqualFold(strings.TrimSpace(v.MachineID), this)
}

// DetectRole decides home or node the way the pipeline does: a machine
// whose node config holds a database credential joined a home and is a
// node; one with none, or one that never joined, is the home.
// MAKTABA_CLUSTER_ROLE (home|node) overrides. Returns the role and, for a
// node, its node_id. A node config that is there but cannot be read (held by
// another process past a write's moment, ReadNodeConfigFile) or is broken is
// an error, never the home: a node taken for the home would start as a second
// home.
func DetectRole(getenv func(string) string, goos, userHome string, readFile func(string) ([]byte, error)) (string, string, error) {
	override := strings.ToLower(strings.TrimSpace(getenv("MAKTABA_CLUSTER_ROLE")))
	if override == RoleHome {
		return RoleHome, "", nil
	}
	path := NodeConfigPath(getenv, goos, userHome)
	b, err := ReadNodeConfigFile(path, readFile)
	var nc nodeConfig
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return "", "", fmt.Errorf("node config %s: %w", path, err)
	case json.Unmarshal(WithoutBOM(b), &nc) != nil:
		return "", "", fmt.Errorf("node config %s: not a node config", path)
	}
	nodeID := strings.TrimSpace(nc.NodeID)
	if override == RoleNode || strings.TrimSpace(nc.DatabaseURL) != "" {
		return RoleNode, nodeID, nil
	}
	return RoleHome, "", nil
}

// ReadNodeConfigFile reads the node config at path from the file that holds it
// (NodeConfigFile), waiting out a write under way (writeUnderWay) for up to readWait:
// a reader never takes a node.json that is being replaced for a missing one. A write
// still under way at the end is an error that is not fs.ErrNotExist, so the node is
// never taken for the home.
func ReadNodeConfigFile(path string, readFile func(string) ([]byte, error)) ([]byte, error) {
	deadline := time.Now().Add(readWait)
	for {
		held := NodeConfigFile(path, readFile)
		b, err := readFile(held)
		// a leftover it picked that is gone already, or going (denied while it is removed):
		// the write ended, look again
		vanished := held != path && (errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission))
		if !vanished && !writeUnderWay(path, err) {
			return b, err
		}
		if !time.Now().Before(deadline) {
			if errors.Is(err, fs.ErrNotExist) || isBusy(err) {
				return nil, fmt.Errorf("%s is being written (a write of it still under way after %s): %v", held, readWait, err)
			}
			return nil, err
		}
		time.Sleep(busyWait)
	}
}

// readWait and busyWait: how long a reader waits out a write of the node config
// (maktaba-server's and the pipeline's, which hold its lock for all of it: milliseconds,
// more on a busy machine; tests shorten it), and how often it looks again.
var readWait = 2 * time.Second

const busyWait = 5 * time.Millisecond

// writeUnderWay: reading the node config at path failed with err while a write
// of it may be under way: the file is held by another process (ReplaceFileW, a
// rename), or it is missing, or denied for a moment (a backup being removed),
// while its lock is held (made in the last minute) or a write's leftovers are beside
// it (ReplaceFileW renames node.json aside before the new one takes its name).
func writeUnderWay(path string, err error) bool {
	switch {
	case err == nil:
		return false
	case isBusy(err):
		return true
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, fs.ErrPermission):
		return lockedLately(filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".lock")) || hasLeftovers(path)
	}
	return false
}

// lockedLately: the lock file at lock is there and was made in the last minute (a
// write removes it when it is done; one a process that stopped mid-write left, long
// ago, is no write under way; the pipeline's _locked_lately).
func lockedLately(lock string) bool {
	fi, err := os.Stat(lock)
	if err != nil {
		return false
	}
	age := time.Since(fi.ModTime())
	return age > -time.Minute && age < time.Minute
}

// hasLeftovers: a write under way left something beside the node config at path:
// a leftover named in the last minute (the time in its name; the pipeline's
// _in_progress). A stale one, from a write that could not finish long ago, is no
// write under way.
func hasLeftovers(path string) bool {
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return false
	}
	prefix := "." + filepath.Base(path) + "-"
	for _, e := range entries {
		if e.IsDir() || !isNodeConfigLeftover(filepath.Base(path), e.Name()) || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		stamp, _, ok := strings.Cut(strings.TrimPrefix(e.Name(), prefix), "-")
		if started, err := strconv.ParseInt(stamp, 10, 64); ok && err == nil {
			if age := time.Since(time.Unix(started, 0)); age > -time.Minute && age < time.Minute {
				return true
			}
		}
	}
	return false
}

// heldNodeConfig is the file that holds the node config at path (NodeConfigFile)
// when there is one, waiting out a write under way (a leftover it picked can be
// gone a moment later) for up to readWait; "" when there is none. A write still
// under way at the end keeps path (its reader then says it is being written): a
// resolver never takes the local place for it.
func heldNodeConfig(path string) string {
	deadline := time.Now().Add(readWait)
	for {
		held := NodeConfigFile(path, os.ReadFile)
		if isFile(held) {
			return held
		}
		underWay := writeUnderWay(path, fs.ErrNotExist)
		if held == path && !underWay {
			return ""
		}
		if !time.Now().Before(deadline) {
			if underWay {
				return path
			}
			return ""
		}
		time.Sleep(busyWait)
	}
}

// LocalIdentity builds this host's identity: role from DetectRole, id from
// the node config (node) or the instance_identity row (home; the host name
// when the table is absent). A node config that cannot be read is an error
// (DetectRole): never the home's identity.
func LocalIdentity(ctx context.Context, pool *pgxpool.Pool, hostname, streamBase string) (HostIdentity, error) {
	home, _ := os.UserHomeDir()
	role, nodeID, err := DetectRole(os.Getenv, runtime.GOOS, home, os.ReadFile)
	if err != nil {
		return HostIdentity{}, err
	}
	id := nodeID
	if role == RoleHome || id == "" {
		id = "home-" + hostname
		if pool != nil {
			qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			var inst string
			if err := pool.QueryRow(qctx, `SELECT instance_id::text FROM instance_identity WHERE id = 1`).Scan(&inst); err == nil && inst != "" {
				id = inst
			}
			cancel()
		}
		if role == RoleNode {
			id = "node-" + hostname
		}
	}
	return HostIdentity{ID: id, Host: hostname, Role: role, StreamBaseURL: strings.TrimRight(strings.TrimSpace(streamBase), "/")}, nil
}

// ErrRunningElsewhere is returned when a channel already encodes on
// another live host (one encode per channel cluster-wide).
var ErrRunningElsewhere = errors.New("channel: on air on another host")

// RunningElsewhereError names the host that runs the channel.
type RunningElsewhereError struct {
	HostID, Host, StreamBaseURL string
}

func (e *RunningElsewhereError) Error() string {
	return fmt.Sprintf("channel: on air on host %s (%s)", e.Host, e.HostID)
}

func (e *RunningElsewhereError) Is(target error) bool { return target == ErrRunningElsewhere }

// RuntimeOwner is implemented by repos that can say which host runs a
// channel (its channel_runtime row and that row's last heartbeat).
type RuntimeOwner interface {
	RuntimeOf(ctx context.Context, channelID uuid.UUID) (rt Runtime, heartbeat time.Time, found bool, err error)
}

// HostStatus is a streaming host's capacity (streaming_hosts), written
// with each heartbeat so the home can pick a host for a channel.
type HostStatus struct {
	Identity       HostIdentity
	Encoder        string
	MaxActive      int
	ActiveChannels int
	NVENCFree      int // -1: no cap
	VTFree         int // -1: no cap
}

// HostStatusWriter is implemented by repos that record HostStatus.
type HostStatusWriter interface {
	SetHostStatus(ctx context.Context, st HostStatus) error
}

// DefaultRuntimeStaleAfter: a channel_runtime row whose heartbeat is older
// than this belongs to a dead host and no longer owns its channel.
const DefaultRuntimeStaleAfter = 45 * time.Second

// HeartbeatInterval is how often a host rewrites its rows.
const HeartbeatInterval = 10 * time.Second
