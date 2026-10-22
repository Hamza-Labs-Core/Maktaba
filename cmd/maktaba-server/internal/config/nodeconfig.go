package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// NodeConfig is the part of a joined machine's Epic 31 node config (node.json, written
// by the pipeline when the home approved the join) the node's roles need: the streaming
// role as a cluster node (Epic 32.1), and since Epic 35 (story 35.8) the whole of
// maktaba-server in node mode — the API with the node's database role and the home's
// address, the machine agent with the node's channel key.
//
// It holds credentials (the database password, the node's private key): never log or
// print it; String redacts.
type NodeConfig struct {
	NodeID      string  `json:"node_id"`
	Name        string  `json:"name"`
	PrivateKey  string  `json:"private_key"`
	Home        HomeRef `json:"home"`
	DatabaseURL string  `json:"database_url"`
	// MachineID is the PC the node config belongs to (Windows' MachineGuid), written by
	// the pipeline when it joins or first runs with a config that has none
	// (cluster/config.py); "" in a config no new Maktaba has run with yet.
	MachineID string `json:"machine_id"`
	// Err is why node.json, which exists, could not be read (bad JSON, a truncated
	// file): such a machine is not "not joined", which would start it as a home.
	Err error `json:"-"`
}

// HomeRef is node.json's home.
type HomeRef struct {
	URL        string `json:"url"`
	APIURL     string `json:"api_url"`
	InstanceID string `json:"instance_id"`
	Name       string `json:"name"`
	// ClusterPub is the home's long-term cluster public key (channel handshake v2);
	// empty in a config written before v2 (the machine must re-join, story 35.8 EC2).
	ClusterPub string `json:"cluster_pub"`
}

// HomeURL is the home's API URL (home.url, or the older home.api_url).
func (n NodeConfig) HomeURL() string {
	u := strings.TrimSpace(n.Home.URL)
	if u == "" {
		u = strings.TrimSpace(n.Home.APIURL)
	}
	return strings.TrimRight(u, "/")
}

// Node8 is the first 8 hex of the node id (its worker ids, its database role).
func (n NodeConfig) Node8() string {
	h := strings.ToLower(strings.ReplaceAll(n.NodeID, "-", ""))
	if len(h) > 8 {
		h = h[:8]
	}
	return h
}

// String never shows a credential.
func (n NodeConfig) String() string {
	return "node " + n.NodeID + " of " + n.HomeURL()
}

// Joined reports whether the machine joined a home (it holds a database credential).
func (n NodeConfig) Joined() bool { return strings.TrimSpace(n.DatabaseURL) != "" }

// Missing names what the node's roles need and the config lacks (its node id, its
// private key): a hand-edited or half-written node.json.
func (n NodeConfig) Missing() []string {
	var out []string
	if strings.TrimSpace(n.NodeID) == "" {
		out = append(out, "node_id")
	}
	if strings.TrimSpace(n.PrivateKey) == "" {
		out = append(out, "private_key")
	}
	return out
}

// NodeConfigPath is where the pipeline keeps the node config (cluster/config.py
// config_path): MAKTABA_NODE_CONFIG, else per OS. On Windows a machine has one node
// config and it stays where it is (it is never copied or moved, story 38.1):
// %APPDATA%\Maktaba\node.json of a machine that joined before Epic 38, while it is
// this PC's (its machine_id is this PC's MachineGuid) or no PC's yet (the pipeline
// claims it the first time it runs with it); else %LOCALAPPDATA%\Maktaba\node.json,
// where a new join writes. One in a roaming profile that another PC claimed is
// skipped (ForeignNodeConfig): this PC is not that machine, and its own join then
// writes the local file, never the other PC's identity. Streaming, the pipeline and
// the node installer's scripts resolve it the same way.
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
		if old := roamingNodeConfig(getenv, userHome); isFile(old) && !claimedElsewhere(old, thisMachine(getenv)) {
			return old
		}
		return filepath.Join(WindowsRoot(getenv, userHome), "node.json")
	}
	base := getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(userHome, ".config")
	}
	return filepath.Join(base, "maktaba", "node.json")
}

// machineIDEnv names this PC's id in place of Windows' MachineGuid: tests, and a PC
// whose registry cannot be read.
const machineIDEnv = "MAKTABA_MACHINE_ID"

// ThisMachineID is this PC's id: $MAKTABA_MACHINE_ID, else Windows' MachineGuid
// (HKLM\SOFTWARE\Microsoft\Cryptography); "" when it cannot be known (off Windows).
func ThisMachineID(getenv func(string) string) string {
	if v := strings.TrimSpace(getenv(machineIDEnv)); v != "" {
		return v
	}
	return machineGUID()
}

// roamingNodeConfig is %APPDATA%\Maktaba\node.json, a node config from before Epic 38.
func roamingNodeConfig(getenv func(string) string, userHome string) string {
	roaming := getenv("APPDATA")
	if roaming == "" {
		roaming = filepath.Join(userHome, "AppData", "Roaming")
	}
	return filepath.Join(roaming, "Maktaba", "node.json")
}

// nodeConfigOwner is the machine_id a node config records: "" when it has none or
// cannot be read (a broken file is never skipped: its reader says what is wrong).
func nodeConfigOwner(path string) string {
	b, err := os.ReadFile(path) //nolint:gosec // this machine's node config
	if err != nil {
		return ""
	}
	var v struct {
		MachineID string `json:"machine_id"`
	}
	if json.Unmarshal(b, &v) != nil {
		return ""
	}
	return strings.TrimSpace(v.MachineID)
}

// claimedElsewhere: the node config at path records another PC than this one. A
// config no PC has claimed, or a PC whose id cannot be known, is never skipped: a
// working node is never taken down on a guess.
func claimedElsewhere(path string, thisID func() string) bool {
	owner := nodeConfigOwner(path)
	if owner == "" {
		return false
	}
	this := thisID()
	return this != "" && !strings.EqualFold(owner, this)
}

// thisMachine is ThisMachineID, asked only when a claimed config needs it.
func thisMachine(getenv func(string) string) func() string {
	return func() string { return ThisMachineID(getenv) }
}

// ForeignNodeConfig is the roaming profile's node config when another PC claimed it
// (NodeConfigPath skips it), with that PC's id, while this PC runs unjoined because
// of it: until it joins as its own machine. "" when there is none (off Windows, with
// $MAKTABA_NODE_CONFIG set, a config that is this PC's or no PC's), and once this PC
// joined as its own (its local node config): the other PC's is then nothing to it.
// It is the one rule for every place that says so (serve, setup's check, the move's
// plan).
func ForeignNodeConfig(getenv func(string) string, goos, userHome string) (path, owner string) {
	if goos != "windows" || strings.TrimSpace(getenv("MAKTABA_NODE_CONFIG")) != "" {
		return "", ""
	}
	old := roamingNodeConfig(getenv, userHome)
	if !isFile(old) || !claimedElsewhere(old, thisMachine(getenv)) {
		return "", ""
	}
	if ReadNodeConfig(getenv, goos, userHome, os.ReadFile).Joined() {
		return "", "" // this PC's own join (NodeConfigPath reads the local file)
	}
	return old, nodeConfigOwner(old)
}

// LocalForeignNodeConfig is ForeignNodeConfig for this process.
func LocalForeignNodeConfig() (path, owner string) {
	home, _ := os.UserHomeDir()
	return ForeignNodeConfig(os.Getenv, runtime.GOOS, home)
}

// ForeignNodeLine says why this PC runs unjoined beside another PC's node config.
func ForeignNodeLine(path, owner string) string {
	return "The machine settings " + path + " in this PC's roaming profile belong to another PC (" + owner +
		"): this PC is not that machine, so it works for no home yet. To have it work for a home, join it as its own " +
		"machine (Help: Machines, Add a machine); its settings are then kept on this PC only."
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// ReadNodeConfig reads this machine's node config (zero value when there is none; Err
// set when the file exists but cannot be read).
func ReadNodeConfig(getenv func(string) string, goos, userHome string, readFile func(string) ([]byte, error)) NodeConfig {
	var n NodeConfig
	path := NodeConfigPath(getenv, goos, userHome)
	b, err := readFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return n
	}
	if err == nil {
		err = json.Unmarshal(b, &n)
	}
	if err != nil {
		return NodeConfig{Err: fmt.Errorf("node config %s: %w", path, err)}
	}
	n.NodeID, n.DatabaseURL = strings.TrimSpace(n.NodeID), strings.TrimSpace(n.DatabaseURL)
	n.PrivateKey = strings.TrimSpace(n.PrivateKey)
	return n
}

// LocalNodeConfig is ReadNodeConfig for this process.
func LocalNodeConfig() NodeConfig {
	home, _ := os.UserHomeDir()
	return ReadNodeConfig(os.Getenv, runtime.GOOS, home, os.ReadFile)
}

// LocalNodeConfigPath is NodeConfigPath for this process.
func LocalNodeConfigPath() string {
	home, _ := os.UserHomeDir()
	return NodeConfigPath(os.Getenv, runtime.GOOS, home)
}
