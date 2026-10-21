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
// config and it stays where it is: %APPDATA%\Maktaba\node.json for a machine that
// joined before Epic 38 (it is never copied or moved, story 38.1), else
// %LOCALAPPDATA%\Maktaba\node.json, where a new join writes. Streaming, the
// pipeline and the node installer's scripts resolve it the same way.
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
		if old := filepath.Join(roaming, "Maktaba", "node.json"); isFile(old) {
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
