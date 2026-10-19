package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// NodeConfig is the part of a joined machine's Epic 31 node config (node.json, written
// by the pipeline when the home approved the join) the streaming role needs to run as a
// cluster node (Epic 32.1).
type NodeConfig struct {
	NodeID      string `json:"node_id"`
	DatabaseURL string `json:"database_url"`
}

// Joined reports whether the machine joined a home (it holds a database credential).
func (n NodeConfig) Joined() bool { return strings.TrimSpace(n.DatabaseURL) != "" }

// NodeConfigPath is where the pipeline keeps the node config (cluster/config.py
// config_path): MAKTABA_NODE_CONFIG, else per OS.
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
		base := getenv("APPDATA")
		if base == "" {
			base = filepath.Join(userHome, "AppData", "Roaming")
		}
		return filepath.Join(base, "Maktaba", "node.json")
	}
	base := getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(userHome, ".config")
	}
	return filepath.Join(base, "maktaba", "node.json")
}

// ReadNodeConfig reads this machine's node config (zero value when there is none).
func ReadNodeConfig(getenv func(string) string, goos, userHome string, readFile func(string) ([]byte, error)) NodeConfig {
	var n NodeConfig
	if b, err := readFile(NodeConfigPath(getenv, goos, userHome)); err == nil {
		_ = json.Unmarshal(b, &n)
	}
	n.NodeID, n.DatabaseURL = strings.TrimSpace(n.NodeID), strings.TrimSpace(n.DatabaseURL)
	return n
}

// LocalNodeConfig is ReadNodeConfig for this process.
func LocalNodeConfig() NodeConfig {
	home, _ := os.UserHomeDir()
	return ReadNodeConfig(os.Getenv, runtime.GOOS, home, os.ReadFile)
}
