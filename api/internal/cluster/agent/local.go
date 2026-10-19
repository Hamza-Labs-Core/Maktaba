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

// isLocalAddress reports whether ip is a loopback address or one of this
// machine's own interface addresses.
func isLocalAddress(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
			return true
		}
	}
	return false
}

// HomeKeyPath is MAKTABA_CLUSTER_KEY_PATH, else
// ~/.maktaba/cluster/home-agent.key: always local, never on the share.
func HomeKeyPath(override string) string {
	if override != "" {
		return override
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".maktaba", "cluster", "home-agent.key")
	}
	return filepath.Join(home, ".maktaba", "cluster", "home-agent.key")
}

// LoadOrCreateHomeKey returns the 32-byte home agent key at path, creating
// it (32 random bytes, mode 0600, written atomically) when absent or not
// 32 bytes. It tightens a looser mode to 0600.
func LoadOrCreateHomeKey(path string) ([]byte, error) {
	if b, err := os.ReadFile(path); err == nil { //nolint:gosec // the configured key path
		if len(b) == KeySize {
			if st, err := os.Stat(path); err == nil && st.Mode().Perm()&0o077 != 0 {
				_ = os.Chmod(path, 0o600)
			}
			return b, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("agent: read home key: %w", err)
	}
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("agent: random home key: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("agent: home key dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".home-agent.key-*")
	if err != nil {
		return nil, fmt.Errorf("agent: home key: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
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
