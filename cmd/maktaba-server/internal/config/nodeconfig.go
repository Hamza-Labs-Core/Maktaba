package config

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
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
	// DatabaseCA is the home's database CA (PEM), sealed in the join's credentials or
	// handed over the machine channel (never learnt over the database connection it
	// verifies); DatabaseTLS is "off" when the home's database has no TLS (its own
	// [postgres] tls = "off"). HomeDSN reaches the home with them.
	DatabaseCA  string `json:"database_ca,omitempty"`
	DatabaseTLS string `json:"database_tls,omitempty"`
	// StandbyHomes are the web addresses of the machines that may take over as the home
	// in an emergency (the home tells every machine): with the home gone, this machine
	// asks them for its notice of a new home.
	StandbyHomes []string `json:"standby_homes,omitempty"`
	// Peers are the web addresses of the home's other machines (the home tells every
	// machine): one that was off during a switch, its old home gone, asks them for its
	// notice of the new home (every copy has the notices).
	Peers []string `json:"peers,omitempty"`
	// Err is why node.json, which exists, could not be read (bad JSON, a truncated
	// file): such a machine is not "not joined", which would start it as a home.
	Err error `json:"-"`
}

// HomeDatabaseCAFile is the home's database CA as this machine pins it, in the keys
// directory (HomeDSN writes it).
const HomeDatabaseCAFile = "home-database-ca.crt"

// DatabaseVerified reports whether this machine knows how to verify its home's
// database: its CA, or the home's word that it has no TLS.
func (n NodeConfig) DatabaseVerified() bool {
	return strings.TrimSpace(n.DatabaseCA) != "" || strings.EqualFold(n.DatabaseTLS, "off")
}

// HomeDSN is the home's database as this machine reaches it (DatabaseURL): verifying
// the home's CA (verify-ca, the CA written to keysDir), with no fallback to a plain
// connection; plain only when the home says its database has no TLS; as node.json has
// it while neither is known (a machine joined before its home sealed the CA, until the
// home hands it over the machine channel). A CA that cannot be pinned is an error, and
// the DSN still verifies (against what is pinned already): it never falls back to one
// that does not.
func (n NodeConfig) HomeDSN(keysDir string) (string, error) {
	u, err := url.Parse(n.DatabaseURL)
	if err != nil || u.Host == "" {
		return n.DatabaseURL, fmt.Errorf("node config: the home's database url: %v", err)
	}
	q := u.Query()
	var pinErr error
	switch {
	case strings.TrimSpace(n.DatabaseCA) != "":
		path := filepath.Join(keysDir, HomeDatabaseCAFile)
		pinErr = writeCA(path, n.DatabaseCA)
		q.Set("sslmode", "verify-ca")
		q.Set("sslrootcert", filepath.ToSlash(path))
	case strings.EqualFold(n.DatabaseTLS, "off"):
		q.Set("sslmode", "disable")
		q.Del("sslrootcert")
	default:
		return n.DatabaseURL, nil
	}
	u.RawQuery = q.Encode()
	return u.String(), pinErr
}

// writeCA writes a CA certificate (0600) unless the file holds it already; anything
// but a CA certificate is refused.
func writeCA(path, pemText string) error {
	if err := CheckCA(pemText); err != nil {
		return err
	}
	if b, err := os.ReadFile(path); err == nil && bytes.Equal(b, []byte(pemText)) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(pemText), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// CheckCA reports whether pemText is one CA certificate.
func CheckCA(pemText string) error {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil || block.Type != "CERTIFICATE" {
		return errors.New("node config: the home's database CA is not a certificate")
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("node config: the home's database CA: %w", err)
	}
	if !c.IsCA {
		return errors.New("node config: the home's database CA is not a CA certificate")
	}
	return nil
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
	// Term is the cluster's term this home began (Epic 37: a switch of the home starts
	// the next); 0 before the first switch this machine followed.
	Term int64 `json:"term,omitempty"`
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
// config_path): MAKTABA_NODE_CONFIG, else per OS. On Windows it is
// %LOCALAPPDATA%\Maktaba\node.json (Epic 38 story 38.1); a machine that joined
// before keeps the one in its roaming profile (%APPDATA%\Maktaba\node.json) while
// there is none in the new place.
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
		cur := filepath.Join(WindowsRoot(getenv, userHome), "node.json")
		if isFile(cur) {
			return cur
		}
		roaming := getenv("APPDATA")
		if roaming == "" {
			roaming = filepath.Join(userHome, "AppData", "Roaming")
		}
		if old := filepath.Join(roaming, "Maktaba", "node.json"); isFile(old) {
			return old
		}
		return cur
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
