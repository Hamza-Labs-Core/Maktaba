package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

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
		if held := NodeConfigFile(old, os.ReadFile); isFile(held) && !claimedElsewhere(held, getenv) {
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

// wholeNodeConfig: b is a node config with its identity (not a cut file).
func wholeNodeConfig(b []byte) bool {
	var v struct {
		NodeID     string `json:"node_id"`
		PrivateKey string `json:"private_key"`
	}
	return json.Unmarshal(b, &v) == nil && strings.TrimSpace(v.NodeID) != "" && strings.TrimSpace(v.PrivateKey) != ""
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
	if json.Unmarshal(b, &v) != nil || strings.TrimSpace(v.MachineID) == "" {
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
// node; anything else is the home. MAKTABA_CLUSTER_ROLE (home|node)
// overrides. Returns the role and, for a node, its node_id.
func DetectRole(getenv func(string) string, goos, userHome string, readFile func(string) ([]byte, error)) (string, string) {
	nodeID := ""
	joined := false
	if b, err := readFile(NodeConfigFile(NodeConfigPath(getenv, goos, userHome), readFile)); err == nil {
		var nc nodeConfig
		if json.Unmarshal(b, &nc) == nil {
			nodeID = strings.TrimSpace(nc.NodeID)
			joined = strings.TrimSpace(nc.DatabaseURL) != ""
		}
	}
	switch strings.ToLower(strings.TrimSpace(getenv("MAKTABA_CLUSTER_ROLE"))) {
	case RoleHome:
		return RoleHome, ""
	case RoleNode:
		return RoleNode, nodeID
	}
	if joined {
		return RoleNode, nodeID
	}
	return RoleHome, ""
}

// LocalIdentity builds this host's identity: role from DetectRole, id from
// the node config (node) or the instance_identity row (home; the host name
// when the table is absent).
func LocalIdentity(ctx context.Context, pool *pgxpool.Pool, hostname, streamBase string) HostIdentity {
	home, _ := os.UserHomeDir()
	role, nodeID := DetectRole(os.Getenv, runtime.GOOS, home, os.ReadFile)
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
	return HostIdentity{ID: id, Host: hostname, Role: role, StreamBaseURL: strings.TrimRight(strings.TrimSpace(streamBase), "/")}
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
