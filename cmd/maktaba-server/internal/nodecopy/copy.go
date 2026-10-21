// Package nodecopy is a full node's live, read-only copy of its home's database
// (specs/epics/37-node-replica): a Postgres hot standby on this machine, made with
// pg_basebackup and following the home's database over streaming replication, so
// the node signs people in, plays and shows the library from it while the home is
// away. Nothing here is a manual step: the supervisor's Manager fetches the Postgres
// programs, asks the home for the copy (cluster_node_replica_grant, over the node's own
// database connection), copies, runs the copy as a supervised process, watches it,
// copies anew when it cannot follow any more, and wipes it when the machine is revoked.
package nodecopy

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbin"
)

// DefaultPort is the copy's loopback port (the bundled home's is 54329).
const DefaultPort = 54330

// Files of the copy: in the keys directory the password this machine chose for its
// replication role, the passfile the copy's replication reads it from and the home's
// database CA it verifies the home with; in the copy's directory its data and marker.
const (
	passwordFile = "copy-password"
	passFile     = "copy.pgpass"
	caFile       = "copy-ca.crt"
	markerFile   = "maktaba-copy.json"
	dataDir      = "data"
)

// Copy is this machine's copy of the home's database.
type Copy struct {
	NodeID string
	// HomeDSN is this machine's own role on the home's database (node.json).
	HomeDSN string
	// Dir holds the copy (Dir/data) while Keys holds its secrets.
	Dir, Keys string
	Port      int
	// Bin is the Postgres programs' directory (pgbin.Ensure's, plus bin).
	Bin string
}

func (c Copy) id() string { return strings.ReplaceAll(strings.ToLower(c.NodeID), "-", "") }

// Role is the copy's replication role on the home (and so on the copy).
func (c Copy) Role() string { return "maktaba_rep_" + c.id() }

// Slot is the home's slot for the copy.
func (c Copy) Slot() string { return "maktaba_copy_" + c.id() }

// Data is the copy's data directory.
func (c Copy) Data() string { return filepath.Join(c.Dir, dataDir) }

// Addr is where the copy listens (loopback only).
func (c Copy) Addr() string { return net.JoinHostPort("127.0.0.1", strconv.Itoa(c.port())) }

func (c Copy) port() int {
	if c.Port > 0 {
		return c.Port
	}
	return DefaultPort
}

func (c Copy) exe(name string) string { return filepath.Join(c.Bin, pgbin.Exe(name)) }

// Password is the password this machine chose for its replication role: made once
// (32 random bytes), so the programs of this machine are given the copy's address at
// their start, before the home has been asked.
func (c Copy) Password() (string, error) {
	path := filepath.Join(c.Keys, passwordFile)
	if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) >= 32 {
		return strings.TrimSpace(string(b)), nil
	}
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", err
	}
	pw := base64.RawURLEncoding.EncodeToString(raw)
	if err := writeSecret(path, []byte(pw+"\n")); err != nil {
		return "", err
	}
	return pw, nil
}

// Database is the home's database name (the node's DSN's).
func (c Copy) Database() string {
	if u, err := url.Parse(c.HomeDSN); err == nil && strings.Trim(u.Path, "/") != "" {
		return strings.Trim(u.Path, "/")
	}
	return "maktaba"
}

// DSN is the copy's address for this machine's programs (MAKTABA_COPY_DATABASE_URL).
func (c Copy) DSN() (string, error) {
	pw, err := c.Password()
	if err != nil {
		return "", err
	}
	u := url.URL{Scheme: "postgres", User: url.UserPassword(c.Role(), pw), Host: c.Addr(),
		Path: "/" + c.Database(), RawQuery: "sslmode=disable&connect_timeout=5"}
	return u.String(), nil
}

// Grant is the home's answer to the copy's request (slot 0142).
type Grant struct {
	Role     string  `json:"role"`
	Slot     string  `json:"slot"`
	SlotMade bool    `json:"slot_made"`
	SystemID string  `json:"system_identifier"`
	Version  int     `json:"server_version_num"`
	Database string  `json:"database"`
	CA       *string `json:"ca_pem"`
	// Settings are the home's values a hot standby needs at least (max_connections, ...).
	Settings map[string]string `json:"settings"`
}

// Major is the home's Postgres major version.
func (g Grant) Major() string { return strconv.Itoa(g.Version / 10000) }

// Ask asks the home for the copy: its replication role (with this machine's password)
// and its slot, made anew with fresh.
func (c Copy) Ask(ctx context.Context, fresh bool) (Grant, error) {
	pw, err := c.Password()
	if err != nil {
		return Grant{}, err
	}
	db, err := sql.Open("postgres", c.HomeDSN)
	if err != nil {
		return Grant{}, err
	}
	defer db.Close()
	actx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var raw []byte
	if err := db.QueryRowContext(actx, `SELECT cluster_node_replica_grant($1, $2, $3)`, c.NodeID, pw, fresh).Scan(&raw); err != nil {
		return Grant{}, err
	}
	var g Grant
	if err := json.Unmarshal(raw, &g); err != nil {
		return Grant{}, fmt.Errorf("nodecopy: the home's answer: %w", err)
	}
	if g.Role != c.Role() || g.Slot != c.Slot() {
		return Grant{}, fmt.Errorf("nodecopy: the home named %s/%s, not this machine's copy", g.Role, g.Slot)
	}
	return g, nil
}

// Report tells the home how the copy is (cluster_node_report_replica).
func (c Copy) Report(ctx context.Context, s Status) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	db, err := sql.Open("postgres", c.HomeDSN)
	if err != nil {
		return err
	}
	defer db.Close()
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err = db.ExecContext(rctx, `SELECT cluster_node_report_replica($1, $2::jsonb)`, c.NodeID, string(b))
	return err
}

// Marker records what a copy is a copy of, and the home's settings it was configured
// with.
type Marker struct {
	SystemID string            `json:"system_identifier"`
	Major    string            `json:"major"`
	Slot     string            `json:"slot"`
	Built    time.Time         `json:"built"`
	Settings map[string]string `json:"settings,omitempty"`
}

// Built is the marker of a usable copy (false: none, or one that never finished).
func (c Copy) Built() (Marker, bool) {
	var m Marker
	raw, err := os.ReadFile(filepath.Join(c.Data(), markerFile))
	if err != nil || json.Unmarshal(raw, &m) != nil || m.SystemID == "" {
		return Marker{}, false
	}
	if _, err := os.Stat(filepath.Join(c.Data(), "PG_VERSION")); err != nil {
		return Marker{}, false
	}
	return m, true
}

// homeAddr is the home database's host and port (the node's DSN's).
func (c Copy) homeAddr() (host, port string, err error) {
	u, err := url.Parse(c.HomeDSN)
	if err != nil || u.Hostname() == "" {
		return "", "", fmt.Errorf("nodecopy: the home's database address: %v", err)
	}
	port = u.Port()
	if port == "" {
		port = "5432"
	}
	return u.Hostname(), port, nil
}

// conninfo is the replication's connection to the home: the passfile for the
// password, the pinned CA when the home has published one (verify-ca), else TLS when
// the home offers it.
func (c Copy) conninfo() (string, error) {
	host, port, err := c.homeAddr()
	if err != nil {
		return "", err
	}
	kv := [][2]string{{"host", host}, {"port", port}, {"user", c.Role()},
		{"passfile", filepath.ToSlash(filepath.Join(c.Keys, passFile))}, {"application_name", c.Slot()},
		{"connect_timeout", "10"}}
	if ca := filepath.Join(c.Keys, caFile); fileExists(ca) {
		kv = append(kv, [2]string{"sslmode", "verify-ca"}, [2]string{"sslrootcert", filepath.ToSlash(ca)})
	} else {
		kv = append(kv, [2]string{"sslmode", "prefer"})
	}
	var s strings.Builder
	for i, p := range kv {
		if i > 0 {
			s.WriteByte(' ')
		}
		s.WriteString(p[0] + "='" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(p[1]) + "'")
	}
	return s.String(), nil
}

// writeAccess writes the passfile and pins the CA the grant carries.
func (c Copy) writeAccess(g Grant) error {
	host, port, err := c.homeAddr()
	if err != nil {
		return err
	}
	pw, err := c.Password()
	if err != nil {
		return err
	}
	esc := strings.NewReplacer(`\`, `\\`, `:`, `\:`)
	line := esc.Replace(host) + ":" + esc.Replace(port) + ":*:" + esc.Replace(c.Role()) + ":" + esc.Replace(pw) + "\n"
	if err := writeSecret(filepath.Join(c.Keys, passFile), []byte(line)); err != nil {
		return err
	}
	if g.CA != nil && strings.Contains(*g.CA, "BEGIN CERTIFICATE") {
		// pinned at the first grant that carries it; a later one does not replace it
		if p := filepath.Join(c.Keys, caFile); !fileExists(p) {
			if err := writeSecret(p, []byte(*g.CA)); err != nil {
				return err
			}
		}
	}
	return nil
}

// Build copies the home's database into a new data directory and makes it a standby
// of the home on the grant's slot; the copy in place, if any, is replaced only once
// the new one is complete. The copy must not run meanwhile (the slot is the new one's).
func (c Copy) Build(ctx context.Context, g Grant, log io.Writer) error {
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return err
	}
	restrict(c.Dir)
	if err := c.writeAccess(g); err != nil {
		return fmt.Errorf("nodecopy: the copy's access: %w", err)
	}
	conn, err := c.conninfo()
	if err != nil {
		return err
	}
	tmp := c.Data() + ".new"
	_ = os.RemoveAll(tmp)
	cmd := exec.CommandContext(ctx, c.exe("pg_basebackup"), "-d", conn, "-D", tmp, "-X", "stream", "-S", g.Slot,
		"-c", "fast", "-w")
	cmd.Env = append(os.Environ(), "PGPASSFILE="+filepath.Join(c.Keys, passFile), "PGCONNECT_TIMEOUT=10")
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("nodecopy: pg_basebackup: %w", err)
	}
	if err := c.standby(tmp, conn, g); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := c.compatible(ctx, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	old := c.Data() + ".old"
	_ = os.RemoveAll(old)
	if fileExists(c.Data()) {
		if err := os.Rename(c.Data(), old); err != nil {
			_ = os.RemoveAll(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, c.Data()); err != nil {
		return err
	}
	_ = os.RemoveAll(old)
	return nil
}

// ErrIncompatible: this machine's Postgres cannot run a copy of the home's database
// (its files are of another build: another architecture's layout, other build options).
var ErrIncompatible = errors.New("this machine's Postgres cannot run a copy of the home's database")

// compatible checks a copy against this machine's Postgres: reading a value of its
// control file makes postgres check the files are of its own build.
func (c Copy) compatible(ctx context.Context, dir string) error {
	cctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	out, err := exec.CommandContext(cctx, c.exe("postgres"), "-D", dir, "-C", "data_checksums").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", ErrIncompatible, strings.TrimSpace(string(out)))
	}
	return nil
}

// hbaConf: the copy listens on loopback only, and still asks for the password.
const hbaConf = `# Maktaba: this machine's copy of its home's database (loopback only)
local   all   all                  scram-sha-256
host    all   all   127.0.0.1/32   scram-sha-256
host    all   all   ::1/128        scram-sha-256
`

// standbySettings are the settings a hot standby needs at least the primary's values of.
var standbySettings = []string{"max_connections", "max_worker_processes", "max_wal_senders",
	"max_prepared_transactions", "max_locks_per_transaction"}

// writeConfig writes the copy's own postgresql.conf and postgresql.auto.conf: the
// home's would carry its system's settings (a Linux or Mac home's shared memory type,
// locales Windows does not know), which can stop the copy from starting at all.
func (c Copy) writeConfig(dir, conn string, g Grant) error {
	var s strings.Builder
	s.WriteString("# Maktaba: this machine's copy of its home's database (nodecopy). Its own\n" +
		"# configuration: the settings a hot standby needs at least the home's values of.\n")
	for _, k := range standbySettings {
		if v := strings.TrimSpace(g.Settings[k]); v != "" && strings.Trim(v, "0123456789") == "" {
			fmt.Fprintf(&s, "%s = %s\n", k, v)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "postgresql.conf"), []byte(s.String()), 0o600); err != nil {
		return err
	}
	q := func(v string) string { return "'" + strings.ReplaceAll(v, "'", "''") + "'" }
	auto := fmt.Sprintf("# Maktaba: this machine's copy of its home's database\nprimary_conninfo = %s\nprimary_slot_name = %s\n",
		q(conn), q(g.Slot))
	return os.WriteFile(filepath.Join(dir, "postgresql.auto.conf"), []byte(auto), 0o600)
}

// Tune writes the copy's configuration again when the home's settings changed since
// (a home that allows more connections now): true when the copy needs a restart.
func (c Copy) Tune(g Grant) (bool, error) {
	m, ok := c.Built()
	if !ok || len(g.Settings) == 0 || sameSettings(m.Settings, g.Settings) {
		return false, nil
	}
	conn, err := c.conninfo()
	if err != nil {
		return false, err
	}
	if err := c.writeConfig(c.Data(), conn, g); err != nil {
		return false, err
	}
	m.Settings = g.Settings
	b, _ := json.Marshal(m)
	return true, os.WriteFile(filepath.Join(c.Data(), markerFile), b, 0o600)
}

func sameSettings(a, b map[string]string) bool {
	for _, k := range standbySettings {
		if a[k] != b[k] {
			return false
		}
	}
	return true
}

// standby makes a fresh base backup this machine's copy: its own configuration and
// pg_hba, none of the home's TLS files, the standby signal and the marker.
func (c Copy) standby(dir, conn string, g Grant) error {
	for _, f := range []string{"server.key", "server.crt", "postgres-server.key", "postgres-server.crt", "root.crt",
		"standby.signal", "recovery.signal", "postmaster.opts"} {
		_ = os.Remove(filepath.Join(dir, f))
	}
	if err := os.WriteFile(filepath.Join(dir, "pg_hba.conf"), []byte(hbaConf), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "standby.signal"), nil, 0o600); err != nil {
		return err
	}
	if err := c.writeConfig(dir, conn, g); err != nil {
		return err
	}
	m, _ := json.Marshal(Marker{SystemID: g.SystemID, Major: g.Major(), Slot: g.Slot, Built: time.Now().UTC(),
		Settings: g.Settings})
	if err := os.WriteFile(filepath.Join(dir, markerFile), m, 0o600); err != nil {
		return err
	}
	restrict(dir)
	return nil
}

// ServerArgs are the copy's postgres arguments: a hot standby on its loopback port,
// without TLS or a unix socket (this machine's programs reach it over TCP).
func (c Copy) ServerArgs() []string {
	return []string{"-D", c.Data(), "-c", "port=" + strconv.Itoa(c.port()), "-c", "listen_addresses=127.0.0.1",
		"-c", "unix_socket_directories=", "-c", "ssl=off", "-c", "hot_standby=on",
		"-c", "cluster_name=maktaba-copy", "-c", "archive_mode=off"}
}

// Postgres is the copy's server program.
func (c Copy) Postgres() string { return c.exe("postgres") }

// StopCommand stops the copy fast (pg_ctl: Windows has no signal for it).
func (c Copy) StopCommand() *exec.Cmd {
	return exec.Command(c.exe("pg_ctl"), "stop", "-D", c.Data(), "-m", "fast", "-W")
}

// Status is how the copy is, as the machines page shows it.
type Status struct {
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
	// AppliedAt is the home's time of the last change the copy applied (its beat);
	// LagMS how long ago that was.
	AppliedAt *time.Time `json:"applied_at,omitempty"`
	LagMS     *int64     `json:"lag_ms,omitempty"`
	ReplayLSN string     `json:"replay_lsn,omitempty"`
	Built     *time.Time `json:"built,omitempty"`
	HomeMajor string     `json:"home_major,omitempty"`
	Checked   time.Time  `json:"checked"`
}

// The copy's states.
const (
	StatePreparing    = "preparing"    // fetching the Postgres programs
	StateWaiting      = "waiting"      // no copy yet, the home does not answer
	StateCopying      = "copying"      // the first copy
	StateRebuilding   = "rebuilding"   // copying anew: it could not follow any more
	StateFollowing    = "following"    // applying the home's changes
	StateBehind       = "behind"       // running, but not applying the home's changes now
	StateStarting     = "starting"     // running, not answering yet
	StateIncompatible = "incompatible" // the home's Postgres is of another major version
	StateUnavailable  = "unavailable"  // the home's database keeps no copies
	StateError        = "error"
	StateRevoked      = "revoked" // this machine is no longer the home's: the copy is wiped
)

// FollowingWithin: a copy whose last applied beat is this recent follows the home.
const FollowingWithin = 30 * time.Second

// Observe reads the copy's own state: whether it answers, the home's time of the last
// change it applied and its replay position.
func (c Copy) Observe(ctx context.Context, now time.Time) Status {
	s := Status{Checked: now.UTC()}
	if m, ok := c.Built(); ok {
		b := m.Built
		s.Built, s.HomeMajor = &b, m.Major
	}
	dsn, err := c.DSN()
	if err != nil {
		s.State, s.Detail = StateError, err.Error()
		return s
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		s.State, s.Detail = StateError, err.Error()
		return s
	}
	defer db.Close()
	octx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var (
		inRecovery bool
		lsn        sql.NullString
		at         sql.NullTime
	)
	err = db.QueryRowContext(octx, `SELECT pg_is_in_recovery(), pg_last_wal_replay_lsn()::text,
	       (SELECT at FROM cluster_replica_beat WHERE id = 1)`).Scan(&inRecovery, &lsn, &at)
	if err != nil {
		s.State, s.Detail = StateStarting, err.Error()
		if strings.Contains(err.Error(), "locale is incompatible") {
			// the home's database collates with a locale this system does not have (the
			// bundled Postgres's is C, which every system has)
			s.State = StateIncompatible
		}
		return s
	}
	s.ReplayLSN = lsn.String
	if at.Valid {
		t := at.Time.UTC()
		lag := now.Sub(t).Milliseconds()
		s.AppliedAt, s.LagMS = &t, &lag
	}
	switch {
	case !inRecovery:
		s.State, s.Detail = StateError, "the copy is not a standby"
	case at.Valid && now.Sub(at.Time) <= FollowingWithin:
		s.State = StateFollowing
	default:
		s.State = StateBehind
	}
	return s
}

// Wipe removes the copy and its secrets (the machine was revoked).
func (c Copy) Wipe() error {
	var first error
	for _, p := range []string{c.Data(), c.Data() + ".new", c.Data() + ".old",
		filepath.Join(c.Keys, passFile), filepath.Join(c.Keys, caFile), filepath.Join(c.Keys, passwordFile)} {
		if err := os.RemoveAll(p); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Kinds of a failed request to the home.
const (
	kindUnreachable = iota + 1 // the home's database does not answer
	kindRefused                // this machine may not sign in, or is not the home's any more
	kindUnsupported            // the home keeps no copies (an older home, or a database that cannot)
	kindBusy                   // the slot is in use (a copy still follows on it)
	kindOther
)

func kindOf(err error) int {
	var pe *pq.Error
	if errors.As(err, &pe) {
		switch pe.Code {
		case "28P01", "28000", "42501":
			return kindRefused
		case "42883", "0A000":
			return kindUnsupported
		case "55006":
			return kindBusy
		case "57P01", "57P02", "57P03", "53300":
			return kindUnreachable
		}
		return kindOther
	}
	var ne net.Error
	if errors.As(err, &ne) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "connection refused") {
		return kindUnreachable
	}
	return kindOther
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func writeSecret(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	restrict(path)
	return nil
}

// restrict lets only this account (and the system) read a path: on Unix the mode
// does it (0700 / 0600), on Windows the ACL (no inherited entries).
func restrict(path string) {
	if runtime.GOOS != "windows" {
		return
	}
	restrictACL(path)
}
