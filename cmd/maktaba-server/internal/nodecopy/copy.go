// Package nodecopy is a full node's live copy of its home's database
// (specs/epics/37-node-replica): this machine's own Postgres, where a home keeps its
// bundled one, migrated by this machine's own Maktaba and subscribed, by logical
// replication, to the publication the home keeps for it (every table but the secrets
// and other machines' rows). The node signs people in, plays and shows the library from
// it while the home is away, and it is what makes this machine able to become the home.
// Nothing here is a manual step: the supervisor's Manager fetches the Postgres programs,
// makes the database, asks the home for the copy (cluster_node_replica_grant, over the
// node's own database connection), subscribes, watches the subscription, subscribes
// anew when it cannot follow any more, and wipes the copy when the machine is revoked.
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
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
)

// Files of the copy, in the keys directory: the password this machine chose for its
// replication role, the passfile its subscription reads it from, the home's database CA
// it verifies the home with, the password of the read-only role this machine's programs
// read the copy as. In the database's directory: the marker.
const (
	passwordFile       = "copy-password"
	readerPasswordFile = "copy-reader-password"
	passFile           = "copy.pgpass"
	caFile             = "copy-ca.crt"
	markerFile         = "maktaba-copy.json"
)

// Subscription and Reader are the copy's subscription and read-only role (local names).
const (
	Subscription = "maktaba_copy"
	Reader       = "maktaba_reader"
)

// Copy is this machine's copy of the home's database.
type Copy struct {
	NodeID string
	// HomeDSN is this machine's own role on the home's database (node.json).
	HomeDSN string
	// DB is this machine's own Postgres, which holds the copy.
	DB pgbundle.Bundle
	// Migrate runs this machine's migrations on its own database (maktaba-api migrate).
	Migrate func(ctx context.Context, dsn string) error
}

func (c Copy) id() string { return strings.ReplaceAll(strings.ToLower(c.NodeID), "-", "") }

// Role is this machine's replication role on the home.
func (c Copy) Role() string { return "maktaba_rep_" + c.id() }

// Slot is the home's slot for the copy (the subscription makes it).
func (c Copy) Slot() string { return "maktaba_copy_" + c.id() }

// Publication is what the home publishes to this machine.
func (c Copy) Publication() string { return "maktaba_copy_" + c.id() }

// Addr is where the copy listens (loopback only).
func (c Copy) Addr() string { return c.DB.Addr() }

// Password is the password this machine chose for its replication role: made once
// (32 random bytes), kept in the keys directory.
func (c Copy) Password() (string, error) { return secret(filepath.Join(c.DB.Keys, passwordFile)) }

// ReaderPassword is the read-only role's password (the same way).
func (c Copy) ReaderPassword() (string, error) {
	return secret(filepath.Join(c.DB.Keys, readerPasswordFile))
}

func secret(path string) (string, error) {
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

// DSN is the copy's address for this machine's programs (MAKTABA_COPY_DATABASE_URL):
// the read-only role, so nothing this machine runs writes to its copy.
func (c Copy) DSN() (string, error) {
	pw, err := c.ReaderPassword()
	if err != nil {
		return "", err
	}
	u := url.URL{Scheme: "postgres", User: url.UserPassword(Reader, pw), Host: c.Addr(),
		Path: "/" + pgbundle.AppDatabase, RawQuery: "sslmode=disable&connect_timeout=5"}
	return u.String(), nil
}

// Grant is the home's answer to the copy's request (slot 0144).
type Grant struct {
	Role          string   `json:"role"`
	Slot          string   `json:"slot"`
	SlotExists    bool     `json:"slot_exists"`
	Publication   string   `json:"publication"`
	Tables        []string `json:"tables"`
	SchemaVersion int64    `json:"schema_version"`
	SystemID      string   `json:"system_identifier"`
	Version       int      `json:"server_version_num"`
	Database      string   `json:"database"`
	CA            *string  `json:"ca_pem"`
}

// Ask asks the home for the copy: its replication role (with this machine's password)
// and its publication, up to date; with fresh, a slot left from before is dropped (the
// subscription is about to make it anew).
func (c Copy) Ask(ctx context.Context, fresh bool) (Grant, error) {
	pw, err := c.Password()
	if err != nil {
		return Grant{}, err
	}
	actx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	db, err := c.openHome(actx)
	if err != nil {
		return Grant{}, err
	}
	defer db.Close()
	var raw []byte
	if err := db.QueryRowContext(actx, `SELECT cluster_node_replica_grant($1, $2, $3)`, c.NodeID, pw, fresh).Scan(&raw); err != nil {
		return Grant{}, err
	}
	var g Grant
	if err := json.Unmarshal(raw, &g); err != nil {
		return Grant{}, fmt.Errorf("nodecopy: the home's answer: %w", err)
	}
	if g.Role != c.Role() || g.Slot != c.Slot() || g.Publication != c.Publication() {
		return Grant{}, fmt.Errorf("nodecopy: the home named %s/%s, not this machine's copy", g.Role, g.Publication)
	}
	return g, nil
}

// openHome opens the home's database as this machine's own role, for one call under
// ctx. The join hands a node sslmode=prefer, which lib/pq does not take: it is settled
// as libpq settles it, TLS unless the server answers that it has none (allow: plain).
// Its connections end with ctx's deadline (deadlineDialer): a home switched off
// answers nothing, and lib/pq alone would wait on it for as long as the socket lives.
func (c Copy) openHome(ctx context.Context) (*sql.DB, error) {
	switch sslMode(c.HomeDSN) {
	case "allow":
		return openBounded(withSSLMode(c.HomeDSN, "disable"))
	case "prefer":
		db, err := openBounded(withSSLMode(c.HomeDSN, "require"))
		if err != nil {
			return nil, err
		}
		if err := db.PingContext(ctx); errors.Is(err, pq.ErrSSLNotSupported) {
			_ = db.Close()
			return openBounded(withSSLMode(c.HomeDSN, "disable"))
		}
		return db, nil
	}
	return openBounded(c.HomeDSN)
}

// openBounded is sql.Open("postgres", dsn) whose connections live no longer than the
// context they were made under.
func openBounded(dsn string) (*sql.DB, error) {
	conn, err := pq.NewConnector(dsn)
	if err != nil {
		return nil, err
	}
	conn.Dialer(deadlineDialer{})
	return sql.OpenDB(conn), nil
}

// deadlineDialer gives each connection its dial context's deadline, for good.
type deadlineDialer struct{ d net.Dialer }

func (d deadlineDialer) Dial(network, address string) (net.Conn, error) {
	return d.d.Dial(network, address)
}

func (d deadlineDialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	dd := d.d
	dd.Timeout = timeout
	nc, err := dd.Dial(network, address)
	if err == nil {
		_ = nc.SetDeadline(time.Now().Add(timeout))
	}
	return nc, err
}

func (d deadlineDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	nc, err := d.d.DialContext(ctx, network, address)
	if dl, ok := ctx.Deadline(); ok && err == nil {
		_ = nc.SetDeadline(dl)
	}
	return nc, err
}

// sslMode is a URL dsn's sslmode ("" for none, or a dsn that is not a URL).
func sslMode(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return ""
	}
	return strings.ToLower(u.Query().Get("sslmode"))
}

func withSSLMode(dsn, mode string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	q := u.Query()
	q.Set("sslmode", mode)
	u.RawQuery = q.Encode()
	return u.String()
}

// Report tells the home how the copy is (cluster_node_report_replica).
func (c Copy) Report(ctx context.Context, s Status) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	db, err := c.openHome(rctx)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.ExecContext(rctx, `SELECT cluster_node_report_replica($1, $2::jsonb)`, c.NodeID, string(b))
	return err
}

// Marker records what the copy follows: the home's database (its system identifier),
// the schema version this machine's database had when it subscribed, and when.
type Marker struct {
	SystemID    string    `json:"system_identifier"`
	LocalSchema int64     `json:"local_schema"`
	Subscribed  time.Time `json:"subscribed"`
}

func (c Copy) markerPath() string { return filepath.Join(c.DB.Data, markerFile) }

// Mark reads the marker (false: this database is not a copy, or never subscribed).
func (c Copy) Mark() (Marker, bool) {
	var m Marker
	raw, err := os.ReadFile(c.markerPath())
	if err != nil || json.Unmarshal(raw, &m) != nil {
		return Marker{}, false
	}
	return m, true
}

func (c Copy) writeMark(m Marker) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return writeSecret(c.markerPath(), b)
}

// Claim makes the database directory the copy's: a database there that is not one (this
// machine's own before it joined) is moved aside, never deleted; true when it was.
func (c Copy) Claim(now time.Time) (string, error) {
	if !c.DB.Initialized() {
		return "", nil
	}
	if _, err := os.Stat(c.markerPath()); err == nil {
		return "", nil
	}
	aside := c.DB.Data + ".before-copy-" + now.UTC().Format("20060102-150405")
	if err := os.Rename(c.DB.Data, aside); err != nil {
		return "", fmt.Errorf("nodecopy: moving this machine's own database aside: %w", err)
	}
	return aside, nil
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

// conninfo is the subscription's connection to the home: the passfile for the
// password, the pinned CA when the home has published one (verify-ca), else TLS when
// the home offers it.
func (c Copy) conninfo(database string) (string, error) {
	host, port, err := c.homeAddr()
	if err != nil {
		return "", err
	}
	if database == "" {
		if u, err := url.Parse(c.HomeDSN); err == nil {
			database = strings.Trim(u.Path, "/")
		}
	}
	kv := [][2]string{{"host", host}, {"port", port}, {"dbname", database}, {"user", c.Role()},
		{"passfile", filepath.ToSlash(filepath.Join(c.DB.Keys, passFile))}, {"application_name", c.Slot()},
		{"connect_timeout", "10"}}
	if ca := filepath.Join(c.DB.Keys, caFile); fileExists(ca) {
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
	if err := writeSecret(filepath.Join(c.DB.Keys, passFile), []byte(line)); err != nil {
		return err
	}
	if g.CA != nil && strings.Contains(*g.CA, "BEGIN CERTIFICATE") {
		// pinned at the first grant that carries it; a later one does not replace it
		if p := filepath.Join(c.DB.Keys, caFile); !fileExists(p) {
			if err := writeSecret(p, []byte(*g.CA)); err != nil {
				return err
			}
		}
	}
	return nil
}

// super is this machine's database as its superuser.
func (c Copy) super() (*sql.DB, error) {
	dsn, err := c.DB.SuperURL()
	if err != nil {
		return nil, err
	}
	return sql.Open("postgres", dsn)
}

// EnsureReader makes the read-only role this machine's programs read the copy as.
func (c Copy) EnsureReader(ctx context.Context) error {
	pw, err := c.ReaderPassword()
	if err != nil {
		return err
	}
	db, err := c.super()
	if err != nil {
		return err
	}
	defer db.Close()
	r, p := pq.QuoteIdentifier(Reader), pq.QuoteLiteral(pw)
	for _, q := range []string{
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '` + Reader + `') THEN CREATE ROLE ` + r + ` LOGIN; END IF; END $$`,
		`ALTER ROLE ` + r + ` WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD ` + p,
		`ALTER ROLE ` + r + ` SET default_transaction_read_only = on`,
		`GRANT pg_read_all_data TO ` + r,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("nodecopy: the copy's read-only role: %w", err)
		}
	}
	return nil
}

// LocalSchema is this machine's database's schema version (its migrations).
func (c Copy) LocalSchema(ctx context.Context) (int64, error) {
	db, err := c.super()
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var v sql.NullInt64
	err = db.QueryRowContext(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&v)
	return v.Int64, err
}

// sub is the subscription as this machine's database has it.
type sub struct {
	exists, enabled bool
	conninfo, slot  string
	pubs            []string
	tables          map[string]bool // the tables it subscribes, by name
}

func (c Copy) subscription(ctx context.Context, db *sql.DB) (sub, error) {
	var s sub
	var pubs pq.StringArray
	var slot sql.NullString
	err := db.QueryRowContext(ctx, `SELECT subenabled, subconninfo, subslotname, subpublications
	  FROM pg_subscription WHERE subname = $1`, Subscription).Scan(&s.enabled, &s.conninfo, &slot, &pubs)
	if errors.Is(err, sql.ErrNoRows) {
		return sub{}, nil
	}
	if err != nil {
		return sub{}, err
	}
	s.exists, s.slot, s.pubs = true, slot.String, pubs
	s.tables = map[string]bool{}
	rows, err := db.QueryContext(ctx, `SELECT c.relname FROM pg_subscription_rel r JOIN pg_subscription s ON s.oid = r.srsubid
	  JOIN pg_class c ON c.oid = r.srrelid WHERE s.subname = $1`, Subscription)
	if err != nil {
		return sub{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			s.tables[n] = true
		}
	}
	return s, rows.Err()
}

// truncate empties the tables of this machine's database the home publishes (what
// this machine's own migrations put there, or an earlier copy): the subscription's
// first copy then starts from nothing.
func truncate(ctx context.Context, db *sql.DB, tables []string) error {
	var have []string
	for _, t := range tables {
		var ok bool
		if err := db.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+pq.QuoteIdentifier(t)).Scan(&ok); err != nil {
			return err
		}
		if ok {
			have = append(have, "public."+pq.QuoteIdentifier(t))
		}
	}
	if len(have) == 0 {
		return nil
	}
	sort.Strings(have)
	_, err := db.ExecContext(ctx, `TRUNCATE `+strings.Join(have, ", ")+` CASCADE`)
	return err
}

// Subscribe drops any subscription this machine's database has and subscribes anew
// to the grant's publication: its tables emptied first, every row then copied.
func (c Copy) Subscribe(ctx context.Context, g Grant) error {
	db, err := c.super()
	if err != nil {
		return err
	}
	defer db.Close()
	if err := c.unsubscribe(ctx, db); err != nil {
		return err
	}
	if err := c.writeAccess(g); err != nil {
		return fmt.Errorf("nodecopy: the copy's access: %w", err)
	}
	if err := truncate(ctx, db, g.Tables); err != nil {
		return fmt.Errorf("nodecopy: emptying this machine's tables: %w", err)
	}
	conn, err := c.conninfo(g.Database)
	if err != nil {
		return err
	}
	q := fmt.Sprintf(`CREATE SUBSCRIPTION %s CONNECTION %s PUBLICATION %s
	  WITH (slot_name = %s, create_slot = true, copy_data = true, streaming = on, disable_on_error = false)`,
		pq.QuoteIdentifier(Subscription), pq.QuoteLiteral(conn), pq.QuoteIdentifier(g.Publication), pq.QuoteLiteral(g.Slot))
	if _, err := db.ExecContext(ctx, q); err != nil {
		return fmt.Errorf("nodecopy: subscribing: %w", err)
	}
	return nil
}

// Handover moves the copy to a new home without copying it again (specs/epics/
// 37-node-replica, "Switching the home"): the copy holds the switchover's marker, so it
// has everything the old home wrote, which is what the new home's database was when it
// made this machine's slot (g.Slot, existing). The subscription to the old home is
// dropped, its slot left there (the old home drops its own); one to the new home's slot
// is made without a first copy; the marker says the copy follows the new home's
// database. c.HomeDSN is this machine's role on the new home's database; local is this
// machine's schema version.
func (c Copy) Handover(ctx context.Context, g Grant, local int64) error {
	db, err := c.super()
	if err != nil {
		return err
	}
	defer db.Close()
	s, err := c.subscription(ctx, db)
	if err != nil {
		return err
	}
	if s.exists {
		name := pq.QuoteIdentifier(Subscription)
		for _, q := range []string{`ALTER SUBSCRIPTION ` + name + ` DISABLE`,
			`ALTER SUBSCRIPTION ` + name + ` SET (slot_name = NONE)`, `DROP SUBSCRIPTION ` + name} {
			if _, err := db.ExecContext(ctx, q); err != nil {
				return fmt.Errorf("nodecopy: leaving the old home: %w", err)
			}
		}
	}
	if err := c.writeAccess(g); err != nil {
		return fmt.Errorf("nodecopy: the copy's access: %w", err)
	}
	conn, err := c.conninfo(g.Database)
	if err != nil {
		return err
	}
	q := fmt.Sprintf(`CREATE SUBSCRIPTION %s CONNECTION %s PUBLICATION %s
	  WITH (slot_name = %s, create_slot = false, copy_data = false, streaming = on, disable_on_error = false)`,
		pq.QuoteIdentifier(Subscription), pq.QuoteLiteral(conn), pq.QuoteIdentifier(g.Publication), pq.QuoteLiteral(g.Slot))
	if _, err := db.ExecContext(ctx, q); err != nil {
		return fmt.Errorf("nodecopy: following the new home: %w", err)
	}
	return c.writeMark(Marker{SystemID: g.SystemID, LocalSchema: local, Subscribed: time.Now().UTC()})
}

// unsubscribe drops the subscription: with the home's slot when the home answers, else
// leaving the slot to the home (its cap, or the next subscription, drops it).
func (c Copy) unsubscribe(ctx context.Context, db *sql.DB) error {
	s, err := c.subscription(ctx, db)
	if err != nil || !s.exists {
		return err
	}
	name := pq.QuoteIdentifier(Subscription)
	_, _ = db.ExecContext(ctx, `ALTER SUBSCRIPTION `+name+` DISABLE`)
	if _, err := db.ExecContext(ctx, `DROP SUBSCRIPTION `+name); err == nil {
		return nil
	}
	if _, err := db.ExecContext(ctx, `ALTER SUBSCRIPTION `+name+` SET (slot_name = NONE)`); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `DROP SUBSCRIPTION `+name)
	return err
}

// Follow keeps the subscription current with a grant: its connection (the home's
// address, a CA pinned since), its tables (a table the home publishes now: emptied
// here, then copied), enabled. False when it cannot be kept and must be made anew.
func (c Copy) Follow(ctx context.Context, g Grant) (bool, error) {
	db, err := c.super()
	if err != nil {
		return false, err
	}
	defer db.Close()
	s, err := c.subscription(ctx, db)
	if err != nil {
		return false, err
	}
	if !s.exists || !g.SlotExists || s.slot != g.Slot || len(s.pubs) != 1 || s.pubs[0] != g.Publication {
		return false, nil
	}
	if err := c.writeAccess(g); err != nil {
		return false, err
	}
	conn, err := c.conninfo(g.Database)
	if err != nil {
		return false, err
	}
	name := pq.QuoteIdentifier(Subscription)
	if s.conninfo != conn {
		if _, err := db.ExecContext(ctx, `ALTER SUBSCRIPTION `+name+` CONNECTION `+pq.QuoteLiteral(conn)); err != nil {
			return false, err
		}
	}
	var added []string
	for _, t := range g.Tables {
		if !s.tables[t] {
			added = append(added, t)
		}
	}
	if len(added) > 0 || len(s.tables) != len(g.Tables) {
		if err := truncate(ctx, db, added); err != nil {
			return false, err
		}
		if _, err := db.ExecContext(ctx, `ALTER SUBSCRIPTION `+name+` REFRESH PUBLICATION WITH (copy_data = true)`); err != nil {
			return false, err
		}
	}
	if !s.enabled {
		if _, err := db.ExecContext(ctx, `ALTER SUBSCRIPTION `+name+` ENABLE`); err != nil {
			return false, err
		}
	}
	return true, nil
}

// Pause disables the subscription (the home's schema is ahead of this machine's: its
// changes would not apply until this machine updates).
func (c Copy) Pause(ctx context.Context) error {
	db, err := c.super()
	if err != nil {
		return err
	}
	defer db.Close()
	s, err := c.subscription(ctx, db)
	if err != nil || !s.exists || !s.enabled {
		return err
	}
	_, err = db.ExecContext(ctx, `ALTER SUBSCRIPTION `+pq.QuoteIdentifier(Subscription)+` DISABLE`)
	return err
}

// Status is how the copy is, as the machines page shows it.
type Status struct {
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
	// AppliedAt is the home's time of the last change the copy applied (its beat);
	// LagMS how long ago that was.
	AppliedAt *time.Time `json:"applied_at,omitempty"`
	LagMS     *int64     `json:"lag_ms,omitempty"`
	// Tables and Ready: how many tables the copy follows, and how many have their first
	// copy done.
	Tables     int        `json:"tables,omitempty"`
	Ready      int        `json:"ready,omitempty"`
	Errors     int64      `json:"errors,omitempty"`
	Subscribed *time.Time `json:"subscribed,omitempty"`
	Checked    time.Time  `json:"checked"`
}

// The copy's states.
const (
	StatePreparing    = "preparing"    // fetching the Postgres programs, making this machine's database
	StateWaiting      = "waiting"      // no copy yet, the home does not answer
	StateCopying      = "copying"      // the first copy of the tables
	StateRebuilding   = "rebuilding"   // subscribing anew: it could not follow any more
	StateFollowing    = "following"    // applying the home's changes
	StateBehind       = "behind"       // not applying the home's changes now
	StateStarting     = "starting"     // this machine's database is not answering yet
	StateIncompatible = "incompatible" // this machine's Maktaba is older than the home's: it follows after its update
	StateUnavailable  = "unavailable"  // the home's database keeps no copies
	StateError        = "error"
	StateRevoked      = "revoked" // this machine is no longer the home's: the copy is wiped
)

// FollowingWithin: a copy whose last applied beat is this recent follows the home.
const FollowingWithin = 30 * time.Second

// Observe reads the copy's own state: whether it answers, its tables' first copy, the
// home's time of the last change it applied, the subscription's errors.
func (c Copy) Observe(ctx context.Context, now time.Time) Status {
	s := Status{Checked: now.UTC()}
	if m, ok := c.Mark(); ok && !m.Subscribed.IsZero() {
		t := m.Subscribed
		s.Subscribed = &t
	}
	db, err := c.super()
	if err != nil {
		s.State, s.Detail = StateError, err.Error()
		return s
	}
	defer db.Close()
	octx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var (
		found   bool
		enabled bool
		errs    sql.NullInt64
		at      sql.NullTime
	)
	err = db.QueryRowContext(octx, `SELECT s.subenabled,
	       (SELECT count(*) FROM pg_subscription_rel r WHERE r.srsubid = s.oid),
	       (SELECT count(*) FROM pg_subscription_rel r WHERE r.srsubid = s.oid AND r.srsubstate = 'r'),
	       (SELECT apply_error_count + sync_error_count FROM pg_stat_subscription_stats t WHERE t.subid = s.oid)
	  FROM pg_subscription s WHERE s.subname = $1`, Subscription).Scan(&enabled, &s.Tables, &s.Ready, &errs)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		s.State, s.Detail = StateStarting, err.Error()
		return s
	default:
		found = true
	}
	s.Errors = errs.Int64
	if err := db.QueryRowContext(octx, `SELECT at FROM cluster_replica_beat WHERE id = 1`).Scan(&at); err == nil && at.Valid &&
		at.Time.After(time.Unix(0, 0)) {
		t := at.Time.UTC()
		lag := now.Sub(t).Milliseconds()
		s.AppliedAt, s.LagMS = &t, &lag
	}
	switch {
	case !found:
		s.State = StateWaiting
	case s.Ready < s.Tables:
		s.State, s.Detail = StateCopying, fmt.Sprintf("%d of %d tables copied", s.Ready, s.Tables)
	case !enabled:
		s.State = StateBehind
	case at.Valid && now.Sub(at.Time) <= FollowingWithin:
		s.State = StateFollowing
	default:
		s.State = StateBehind
	}
	return s
}

// Wipe removes the copy and its secrets (the machine was revoked): this machine's
// database (it held only the copy) and its passwords.
func (c Copy) Wipe() error {
	var first error
	for _, p := range []string{c.DB.Data,
		filepath.Join(c.DB.Keys, passFile), filepath.Join(c.DB.Keys, caFile), filepath.Join(c.DB.Keys, passwordFile),
		filepath.Join(c.DB.Keys, readerPasswordFile),
		filepath.Join(c.DB.Keys, "postgres-"+pgbundle.SuperUser+".password"),
		filepath.Join(c.DB.Keys, "postgres-"+pgbundle.AppRole+".password")} {
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
		switch {
		case pe.Code == "28P01" || pe.Code == "28000" || pe.Code == "42501":
			return kindRefused
		case pe.Code == "42883" || pe.Code == "0A000":
			return kindUnsupported
		case pe.Code == "55006":
			return kindBusy
		case strings.HasPrefix(string(pe.Code), "08") || strings.HasPrefix(string(pe.Code), "53") ||
			strings.HasPrefix(string(pe.Code), "57P"):
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
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	// a power cut is this feature's main case: the file is on the disk before the rename
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
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
