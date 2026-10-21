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
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
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
	"sync"
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

// A standby home's kit (the home's secrets and keys, sealed to this machine for an
// emergency), in the keys directory: the sealed chunks, what they are (StandbyKitMeta),
// and a kit being received. A revoked machine's copy is wiped with them.
const (
	StandbyKitFile = "standby-kit.chunks"
	StandbyKitMeta = "standby-kit.json"
	StandbyKitPart = "standby-kit.part"
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
	// the home stores the password's verifier, which is all it is sent
	verifier, err := scramVerifier(pw)
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
	if err := db.QueryRowContext(actx, `SELECT cluster_node_replica_grant($1, $2, $3)`, c.NodeID, verifier, fresh).Scan(&raw); err != nil {
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
// ctx: as HomeDSN says (verify-ca with the home's pinned CA, or plain on the home's word
// that it has no TLS). A node.json from before its home sealed the CA says prefer, which
// lib/pq does not take: TLS, with no fallback to a plain connection (and the copy waits
// for the CA before it asks for anything: ErrNotVerified). Its connections end with
// ctx's deadline (deadlineDialer): a home switched off answers nothing, and lib/pq
// alone would wait on it for as long as the socket lives.
func (c Copy) openHome(_ context.Context) (*sql.DB, error) {
	switch sslMode(c.HomeDSN) {
	case "allow", "prefer":
		return openBounded(withSSLMode(c.HomeDSN, "require"))
	}
	return openBounded(c.HomeDSN)
}

// ErrNotVerified: this machine cannot verify its home's database yet (a node.json from
// before its home sealed the CA, until the home hands it over the machine channel).
var ErrNotVerified = errors.New("nodecopy: this machine cannot verify its home's database yet: " +
	"its CA comes over the machine channel")

// Verified reports whether this machine can verify its home's database (homeTLS).
func (c Copy) Verified() bool {
	_, err := c.homeTLS()
	return err == nil
}

// homeTLS is the subscription's TLS to the home, as HomeDSN verifies the home: its
// pinned CA (verify-ca), or plain on the home's word that it has no TLS. A home this
// machine cannot verify gets no subscription (ErrNotVerified).
func (c Copy) homeTLS() ([][2]string, error) {
	u, err := url.Parse(c.HomeDSN)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	switch mode := strings.ToLower(q.Get("sslmode")); {
	case (mode == "verify-ca" || mode == "verify-full") && q.Get("sslrootcert") != "":
		return [][2]string{{"sslmode", mode}, {"sslrootcert", q.Get("sslrootcert")}}, nil
	case mode == "disable":
		return [][2]string{{"sslmode", "disable"}}, nil
	}
	return nil, ErrNotVerified
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
//
// It answers the home's clock too, as an offset from this machine's (the home's now()
// against the middle of the call): the copy's beat is stamped on the home's clock.
func (c Copy) Report(ctx context.Context, s Status) (Seen, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return Seen{}, err
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	db, err := c.openHome(rctx)
	if err != nil {
		return Seen{}, err
	}
	defer db.Close()
	if _, err := db.ExecContext(rctx, `SELECT cluster_node_report_replica($1, $2::jsonb)`, c.NodeID, string(b)); err != nil {
		return Seen{}, err
	}
	t0 := time.Now()
	var home time.Time
	if err := db.QueryRowContext(rctx, `SELECT now()`).Scan(&home); err != nil {
		return Seen{}, err
	}
	t1 := time.Now()
	seen := Seen{Offset: home.Sub(t0.Add(t1.Sub(t0) / 2)), SlotLag: -1}
	// pg_replication_slots and the WAL position are anyone's to read
	if err := db.QueryRowContext(rctx, `SELECT COALESCE((SELECT (pg_current_wal_lsn() - confirmed_flush_lsn)::bigint
	    FROM pg_replication_slots WHERE slot_name = $1 AND confirmed_flush_lsn IS NOT NULL), -1)`, c.Slot()).Scan(&seen.SlotLag); err != nil {
		seen.SlotLag = -1
	}
	return seen, nil
}

// Seen is what a report learns of the home: its clock's offset from this machine's, and
// how far this machine's slot is behind the home's WAL in bytes (-1: unknown).
type Seen struct {
	Offset  time.Duration
	SlotLag int64
}

// CaughtUp: a copy whose slot is within this of the home's WAL has applied every change
// the home's database made, however late its beat (the home's API, which writes the
// beat, not running): a WAL segment.
const CaughtUp = 16 << 20

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
// password, and TLS as this machine verifies the home (homeTLS).
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
		{"connect_timeout", "10"},
		// a home switched off mid-copy is noticed (its socket would otherwise live on)
		{"keepalives", "1"}, {"keepalives_idle", "30"}, {"keepalives_interval", "10"}, {"keepalives_count", "3"}}
	tls, err := c.homeTLS()
	if err != nil {
		return "", err
	}
	kv = append(kv, tls...)
	var s strings.Builder
	for i, p := range kv {
		if i > 0 {
			s.WriteByte(' ')
		}
		s.WriteString(p[0] + "='" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(p[1]) + "'")
	}
	return s.String(), nil
}

// writeAccess writes the passfile (the home's CA is node.json's: never learnt from the
// grant, over the connection it is to verify).
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
	return writeSecret(filepath.Join(c.DB.Keys, passFile), []byte(line))
}

// super is this machine's database as its superuser.
func (c Copy) super() (*sql.DB, error) {
	dsn, err := c.DB.SuperURL()
	if err != nil {
		return nil, err
	}
	return sql.Open("postgres", dsn)
}

// EnsureReader makes the read-only role this machine's programs read the copy as, and
// ends a switch's freeze: a machine that was the home kept its database frozen until it
// started as a machine (its copy is written by the subscription and its migrations).
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
		`ALTER DATABASE ` + pq.QuoteIdentifier(pgbundle.AppDatabase) + ` RESET default_transaction_read_only`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("nodecopy: the copy's read-only role: %w", err)
		}
	}
	return nil
}

// ForgetSessions empties the copy's web_sessions: a copy holds no session id (a live
// cookie; their hashes come in web_session_keys), and one made before they were left out
// had them. Triggers stay off (the hashes the copy holds are the home's).
func (c Copy) ForgetSessions(ctx context.Context) error {
	db, err := c.super()
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		return err
	}
	var has bool
	if err := tx.QueryRowContext(ctx, `SELECT to_regclass('public.web_sessions') IS NOT NULL`).Scan(&has); err != nil || !has {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM web_sessions`); err != nil {
		return err
	}
	return tx.Commit()
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
// subscriptionTimeout bounds a statement that makes or changes the subscription (it
// connects to the home from this machine's database).
const subscriptionTimeout = 2 * time.Minute

func (c Copy) Subscribe(ctx context.Context, g Grant) error {
	ctx, cancel := context.WithTimeout(ctx, subscriptionTimeout)
	defer cancel()
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
	ctx, cancel := context.WithTimeout(ctx, subscriptionTimeout)
	defer cancel()
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
	ctx, cancel := context.WithTimeout(ctx, subscriptionTimeout)
	defer cancel()
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

// Quiesce pauses the subscription and waits (up to wait) until its workers have
// stopped: the home's slot is free then (a fresh copy drops it, which the home refuses
// while a subscription streams from it).
func (c Copy) Quiesce(ctx context.Context, wait time.Duration) error {
	if err := c.Pause(ctx); err != nil {
		return err
	}
	db, err := c.super()
	if err != nil {
		return err
	}
	defer db.Close()
	end := time.Now().Add(wait)
	for {
		var live int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_subscription
		    WHERE subname = $1 AND pid IS NOT NULL`, Subscription).Scan(&live); err != nil {
			return err
		}
		if live == 0 {
			return nil
		}
		if time.Now().After(end) {
			return fmt.Errorf("nodecopy: the subscription's workers did not stop within %s", wait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
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
	// LocalSchema is this machine's database's schema version (a switch of the home
	// takes only a machine at the home's)
	LocalSchema int64 `json:"local_schema,omitempty"`
	// late: behind only because its beat is late (the subscription enabled, its tables
	// copied): its slot's position may still show it caught up (Manager.withSlot)
	late bool
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
		s.State, s.late = StateBehind, true
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
		filepath.Join(c.DB.Keys, StandbyKitFile), filepath.Join(c.DB.Keys, StandbyKitMeta),
		filepath.Join(c.DB.Keys, StandbyKitPart),
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

// scramVerifier is Postgres's SCRAM-SHA-256 verifier of password (RFC 5802 / 7677: a
// random salt, 4096 iterations), as the home stores it for the role: the home checks the
// password at sign-in without ever being sent it.
func scramVerifier(password string) (string, error) {
	const iterations = 4096
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	salted, err := pbkdf2.Key(sha256.New, password, salt, iterations, sha256.Size)
	if err != nil {
		return "", err
	}
	mac := func(key []byte, msg string) []byte {
		h := hmac.New(sha256.New, key)
		h.Write([]byte(msg))
		return h.Sum(nil)
	}
	stored := sha256.Sum256(mac(salted, "Client Key"))
	b64 := base64.StdEncoding.EncodeToString
	return fmt.Sprintf("SCRAM-SHA-256$%d:%s$%s:%s", iterations, b64(salt), b64(stored[:]),
		b64(mac(salted, "Server Key"))), nil
}

// kindOf sorts a call's error. Refused is the home saying this machine may have no copy
// (MK403: not approved, or not a full machine) or its role may no longer sign in (a
// revocation): not a permission error elsewhere, a password or a pg_hba rule.
func kindOf(err error) int {
	var pe *pq.Error
	if errors.As(err, &pe) {
		switch {
		case pe.Code == "MK403" || (pe.Code == "28000" && strings.Contains(pe.Message, "not permitted to log in")):
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

// restricted are the directories whose ACL this process has set.
var restricted sync.Map

// restrict lets only this account (and the system) read a path: on Unix the mode does
// it (0700 / 0600), on Windows its directory's ACL (no inherited entries; the files in
// it inherit it), set once per directory by this process, its failure said.
func restrict(path string) {
	if runtime.GOOS != "windows" {
		return
	}
	dir := filepath.Dir(path)
	if _, done := restricted.LoadOrStore(dir, true); done {
		return
	}
	if err := restrictACL(dir); err != nil {
		restricted.Delete(dir)
		fmt.Fprintf(os.Stderr, "[copy] %s: its ACL could not be kept to this account: %v\n", dir, err)
	}
}
