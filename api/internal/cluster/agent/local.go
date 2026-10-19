package agent

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
)

// LANAddress is the home's own folder name and the address its own
// processes are filed under: the first non-loopback private IPv4 of an
// interface that is up, else the host name.
func LANAddress() string {
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, ifc := range ifaces {
			if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, err := ifc.Addrs()
			if err != nil {
				continue
			}
			for _, a := range addrs {
				var ip net.IP
				switch v := a.(type) {
				case *net.IPNet:
					ip = v.IP
				case *net.IPAddr:
					ip = v.IP
				}
				if ip4 := ip.To4(); ip4 != nil && ip4.IsPrivate() && !ip4.IsLoopback() {
					return ip4.String()
				}
			}
		}
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "home"
	}
	host = strings.Split(host, ".")[0]
	if !ValidIPDir(host) {
		return "home"
	}
	return host
}

// HomeKeyPath is the home agent key's file (design-agent.md §4.3): the
// given override (MAKTABA_HOME_AGENT_KEY), else
// ~/.maktaba/cluster/home-agent.key: always local, never on the share.
func HomeKeyPath(override string) string { return localKeyPath(override, "home-agent.key") }

// ClusterKeyPath is the home's long-term cluster key's file (§4.2):
// MAKTABA_HOME_CLUSTER_KEY, else ~/.maktaba/cluster/home-cluster.key.
func ClusterKeyPath(override string) string { return localKeyPath(override, "home-cluster.key") }

func localKeyPath(override, name string) string {
	if override != "" {
		return override
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".maktaba", "cluster", name)
	}
	return filepath.Join(home, ".maktaba", "cluster", name)
}

// LoadOrCreateClusterKey returns the cluster key pair (X25519 private and
// public), creating the private half like the home agent key; it is
// always mode 0600.
func LoadOrCreateClusterKey(path string) (priv, pub []byte, err error) {
	priv, err = LoadOrCreateKey(path, 0o600)
	if err != nil {
		return nil, nil, err
	}
	pub, err = cluster.PublicKey(priv)
	if err != nil {
		return nil, nil, err
	}
	return priv, pub, nil
}

// Key file modes MAKTABA_HOME_AGENT_KEY_MODE may choose: 0600 (the default)
// or 0640, for a home whose workers read the key as another user of the
// API's group (Docker: a shared volume).
const (
	KeyMode      os.FileMode = 0o600
	GroupKeyMode os.FileMode = 0o640
)

// ParseKeyMode reads MAKTABA_HOME_AGENT_KEY_MODE: "0600"/"600" or
// "0640"/"640"; anything else is refused.
func ParseKeyMode(v string) (os.FileMode, bool) {
	switch strings.TrimSpace(v) {
	case "0600", "600", "0o600":
		return KeyMode, true
	case "0640", "640", "0o640":
		return GroupKeyMode, true
	}
	return 0, false
}

// LoadOrCreateHomeKey is LoadOrCreateKey with mode 0600.
func LoadOrCreateHomeKey(path string) ([]byte, error) { return LoadOrCreateKey(path, KeyMode) }

// LoadOrCreateKey returns the 32-byte key at path, creating it (32 random
// bytes, written atomically, with the given mode: 0600 or 0640) when
// absent or not 32 bytes. An existing file with bits beyond mode is
// tightened to it; an existing 0600 file is opened to 0640 only when mode
// asks for 0640. Nothing else is ever loosened.
func LoadOrCreateKey(path string, mode os.FileMode) ([]byte, error) {
	if mode != GroupKeyMode {
		mode = KeyMode
	}
	if b, err := os.ReadFile(path); err == nil { //nolint:gosec // the configured key path
		if len(b) == KeySize {
			if st, err := os.Stat(path); err == nil {
				perm := st.Mode().Perm()
				if perm&^mode != 0 || (mode == GroupKeyMode && perm == KeyMode) {
					_ = os.Chmod(path, mode)
				}
			}
			return b, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("agent: read key: %w", err)
	}
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("agent: random home key: %w", err)
	}
	dir := filepath.Dir(path)
	dirMode := os.FileMode(0o700)
	if mode == GroupKeyMode {
		dirMode = 0o750 // the group must reach the file
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("agent: home key dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".home-agent.key-*")
	if err != nil {
		return nil, fmt.Errorf("agent: home key: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("agent: home key mode: %w", err)
	}
	if _, err := tmp.Write(key); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("agent: write home key: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("agent: sync home key: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("agent: close home key: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return nil, fmt.Errorf("agent: install home key: %w", err)
	}
	return key, nil
}
