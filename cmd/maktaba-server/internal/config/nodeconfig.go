package config

import (
	"bytes"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
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
	// MachineID is the PC the node config belongs to (Windows' MachineGuid), written by
	// the pipeline when it joins or first runs with a config that has none
	// (cluster/config.py); "" in a config no new Maktaba has run with yet.
	MachineID string `json:"machine_id"`
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
	// the next); 0 before the first switch this machine followed. Node is the machine the
	// home was when it began it ("" for the first home): two standby homes cut off from
	// each other may each begin one term, and the one whose machine id is lower gives way.
	Term int64  `json:"term,omitempty"`
	Node string `json:"node,omitempty"`
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
// writes the local file, never the other PC's identity. What a write that could not
// finish left of the roaming node.json counts as it (nodeConfigFile): the node stays
// joined, and the pipeline's next load puts it back. Streaming, the pipeline and the
// node installer's scripts resolve it the same way.
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
		old := roamingNodeConfig(getenv, userHome)
		if held := heldNodeConfig(old); held != "" && !claimedElsewhere(held, thisMachine(getenv)) {
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

// legacyNodeLeftover matches node.json's leftovers as they were named before they
// carried the file's name (cluster/config.py _LEGACY_LEFTOVER).
var legacyNodeLeftover = regexp.MustCompile(`^\.node-(\d+-[0-9a-f]{8}\.bak|[a-z0-9_]{8}\.tmp)$`)

// nodeConfigFile is the file that holds the node config at path, as the pipeline, its
// one writer, reads it (cluster/config.py _source): path, unless it is missing or holds
// no whole node config (a node id and a private key; one that cannot be read is read
// as it is, and its reader says why). Then what the pipeline's next load puts in its
// place: a write that could not finish (ReplaceFileW failing half way) leaves the old
// file under a backup name and the new one as a temp file beside it
// (.<name>-<time>-<pid>-<hex>.bak and .tmp), and the newest whole backup is taken, else
// the newest whole temp file. Else path (its reader says what is wrong).
func nodeConfigFile(path string, readFile func(string) ([]byte, error)) string {
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

// withoutBOM is a node config's bytes as UTF-8, the way every reader reads them (the
// pipeline's _text, the installer's Get-Content): a node.json an editor saved with a byte
// order mark is read in that encoding (Notepad and PowerShell 5.1's Set-Content -Encoding
// UTF8 write UTF-8 with one, its Out-File and > UTF-16; UTF-32 too), and one without is
// UTF-8. Text that does not decode strictly gives no bytes (broken), the same for every
// reader: a UTF-16 lone surrogate, an ANSI file with a byte that is not UTF-8.
func withoutBOM(b []byte) []byte {
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
		return nil // not UTF-8 (an ANSI file with a non-ASCII byte): broken for every reader
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
	var n NodeConfig
	return json.Unmarshal(withoutBOM(b), &n) == nil && len(n.Missing()) == 0
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
	if json.Unmarshal(withoutBOM(b), &v) != nil {
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
// plan). The path is the file that holds it (nodeConfigFile: a leftover of a write
// that could not finish, while node.json is missing or broken).
func ForeignNodeConfig(getenv func(string) string, goos, userHome string) (path, owner string) {
	if goos != "windows" || strings.TrimSpace(getenv("MAKTABA_NODE_CONFIG")) != "" {
		return "", ""
	}
	held := heldNodeConfig(roamingNodeConfig(getenv, userHome))
	if held == "" || !claimedElsewhere(held, thisMachine(getenv)) {
		return "", ""
	}
	if ReadNodeConfig(getenv, goos, userHome, os.ReadFile).Joined() {
		return "", "" // this PC's own join (NodeConfigPath reads the local file)
	}
	return held, nodeConfigOwner(held)
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
// set when the file exists but cannot be read), from the file that holds it
// (nodeConfigFile: what a write that could not finish left, while node.json is missing
// or broken).
func ReadNodeConfig(getenv func(string) string, goos, userHome string, readFile func(string) ([]byte, error)) NodeConfig {
	var n NodeConfig
	path, b, err := readNodeConfigSteady(NodeConfigPath(getenv, goos, userHome), readFile)
	if errors.Is(err, fs.ErrNotExist) {
		return n
	}
	if err == nil {
		err = json.Unmarshal(withoutBOM(b), &n)
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
