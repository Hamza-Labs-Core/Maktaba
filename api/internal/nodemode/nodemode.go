// Package nodemode is what a full node's API (Epic 35 story 35.8, design §9.1) knows
// about itself: that it runs in node mode (MAKTABA_NODE_MODE=1, set by the node's
// supervisor from node.json), the home it belongs to, and the rule that it never
// changes the schema: it refuses to migrate, and refuses to start when its database
// role could run DDL (a copied superuser URL must never let a node migrate by mistake).
package nodemode

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
)

// Config is the node-mode environment of the API.
type Config struct {
	// Enabled: MAKTABA_NODE_MODE=1.
	Enabled bool
	// NodeID is this machine's node id (MAKTABA_NODE_ID).
	NodeID string
	// HomeAPIURL is the home's API (MAKTABA_HOME_API_URL), e.g. http://home:8080.
	HomeAPIURL string
	// HomeStreamURL is the home's streaming port, the TV front door
	// (MAKTABA_HOME_STREAM_URL), e.g. http://home:8081.
	HomeStreamURL string
	// WebURL is this machine's own web address (MAKTABA_NODE_WEB_URL).
	WebURL string
	// AgentURL is the node supervisor's loopback admin (MAKTABA_AGENT_URL) where the
	// machine agent serves the delegation key and the revocations; AgentTokenFile is
	// its per-boot token (MAKTABA_AGENT_TOKEN_FILE, <data_dir>/supervisor/local.token).
	AgentURL       string
	AgentTokenFile string
	// Version and Platform are reported to the home (cluster_node_report_full).
	Platform string
	// Name is this machine's name (node.json's, MAKTABA_NODE_NAME).
	Name string
}

// FromEnv reads the node-mode environment.
func FromEnv(getenv func(string) string) Config {
	on := strings.TrimSpace(strings.ToLower(getenv("MAKTABA_NODE_MODE")))
	c := Config{
		Enabled:        on == "1" || on == "true" || on == "yes",
		NodeID:         strings.TrimSpace(getenv("MAKTABA_NODE_ID")),
		HomeAPIURL:     strings.TrimRight(strings.TrimSpace(getenv("MAKTABA_HOME_API_URL")), "/"),
		HomeStreamURL:  strings.TrimRight(strings.TrimSpace(getenv("MAKTABA_HOME_STREAM_URL")), "/"),
		WebURL:         strings.TrimRight(strings.TrimSpace(getenv("MAKTABA_NODE_WEB_URL")), "/"),
		AgentURL:       strings.TrimRight(strings.TrimSpace(getenv("MAKTABA_AGENT_URL")), "/"),
		AgentTokenFile: strings.TrimSpace(getenv("MAKTABA_AGENT_TOKEN_FILE")),
		Platform:       strings.TrimSpace(getenv("MAKTABA_PLATFORM")),
		Name:           strings.TrimSpace(getenv("MAKTABA_NODE_NAME")),
	}
	return c
}

// Validate reports what a node-mode API cannot run without.
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	var missing []string
	if c.NodeID == "" {
		missing = append(missing, "MAKTABA_NODE_ID")
	}
	if c.HomeAPIURL == "" {
		missing = append(missing, "MAKTABA_HOME_API_URL")
	} else if u, err := url.Parse(c.HomeAPIURL); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("node mode: MAKTABA_HOME_API_URL %q is not an http(s) URL", c.HomeAPIURL)
	}
	if len(missing) > 0 {
		return fmt.Errorf("node mode: %s not set (the node's supervisor sets them from node.json)", strings.Join(missing, ", "))
	}
	return nil
}

// ErrMigrate is returned when node mode is asked to migrate.
var ErrMigrate = errors.New("node mode: this machine never migrates the home's database (MAKTABA_AUTO_MIGRATE is ignored on a node; migrations run only on the home)")

// ErrDDLRole is wrapped by CheckRole when the role could change the schema.
var ErrDDLRole = errors.New("node mode: refusing to start with a database role that can run DDL")

// Privileges is what CheckRole reads of the node's database role.
type Privileges struct {
	Role         string
	Superuser    bool
	CreateRole   bool
	CreateDB     bool
	SchemaCreate bool // CREATE on schema public
	DBCreate     bool // CREATE on the database (new schemas)
	OwnsTables   int  // tables of schema public it owns (it could ALTER or DROP them)
}

// DDL lists why the role could change the schema (empty: it cannot).
func (p Privileges) DDL() []string {
	var why []string
	if p.Superuser {
		why = append(why, "superuser")
	}
	if p.CreateRole {
		why = append(why, "CREATEROLE")
	}
	if p.CreateDB {
		why = append(why, "CREATEDB")
	}
	if p.SchemaCreate {
		why = append(why, "CREATE on schema public")
	}
	if p.DBCreate {
		why = append(why, "CREATE on the database")
	}
	if p.OwnsTables > 0 {
		why = append(why, fmt.Sprintf("owns %d table(s)", p.OwnsTables))
	}
	return why
}

// ReadPrivileges reads the current role's privileges.
func ReadPrivileges(ctx context.Context, db *sql.DB) (Privileges, error) {
	var p Privileges
	err := db.QueryRowContext(ctx, `
SELECT current_user,
       COALESCE(r.rolsuper, false), COALESCE(r.rolcreaterole, false), COALESCE(r.rolcreatedb, false),
       has_schema_privilege(current_user, 'public', 'CREATE'),
       has_database_privilege(current_user, current_database(), 'CREATE'),
       (SELECT count(*) FROM pg_tables t WHERE t.schemaname = 'public' AND t.tableowner = current_user)
  FROM pg_roles r WHERE r.rolname = current_user`).Scan(
		&p.Role, &p.Superuser, &p.CreateRole, &p.CreateDB, &p.SchemaCreate, &p.DBCreate, &p.OwnsTables)
	return p, err
}

// ErrRoleCanDDL: the role's privileges were read and it can change the schema; the
// node's API stops for it (it is ErrDDLRole too).
var ErrRoleCanDDL = fmt.Errorf("%w: it can change the schema", ErrDDLRole)

// CheckRole refuses a role that can run DDL (design §9.1): ErrRoleCanDDL once its
// privileges were read and say so. A role whose privileges cannot be read is an
// ErrDDLRole too, with the cause: the node cannot use that database yet (it does not
// answer, starts up, has no connection to spare, refuses the password), so it runs on
// its copy and checks again; only ErrRoleCanDDL stops it.
func CheckRole(ctx context.Context, db *sql.DB) error {
	p, err := ReadPrivileges(ctx, db)
	if err != nil {
		return fmt.Errorf("%w: cannot read the role's privileges: %w", ErrDDLRole, err)
	}
	if why := p.DDL(); len(why) > 0 {
		return fmt.Errorf("%w: role %q has %s — use the node's own database role from node.json, never the home's",
			ErrRoleCanDDL, p.Role, strings.Join(why, ", "))
	}
	return nil
}

// Unreachable reports whether err is the database not answering, or not able to take a
// connection now — a dial or network failure, a timeout, a dropped connection, or the
// server saying so: SQLSTATE class 08 (connection exception), class 53 (insufficient
// resources: too many clients, out of memory or disk), 57P01..57P03 (shutting down,
// crashed, starting up) and 57P05 (an idle session ended) — rather than it answering
// with a refusal (a bad password, a missing grant).
func Unreachable(err error) bool {
	if err == nil {
		return false
	}
	var pe *pq.Error
	if errors.As(err, &pe) {
		c := string(pe.Code)
		return strings.HasPrefix(c, "08") || strings.HasPrefix(c, "53") ||
			c == "57P01" || c == "57P02" || c == "57P03" || c == "57P05"
	}
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, driver.ErrBadConn) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &ne)
}

// Rules is rules.cluster (app_settings; the pipeline's ClusterMachinesConfig declares
// the section): cluster sign-in on or off, how long a node waits for an unreachable
// home before it stops asking it per request, how current its copy of the home's
// database must be to answer meanwhile (and to be read from while the home answers),
// how long a sign-in made on it then lasts, how much the home keeps for a copy behind,
// whether the machines keep copies of a home's database that is not Maktaba's own
// Postgres, its picture cache.
type Rules struct {
	SSO                        bool `json:"sso"`
	GraceSeconds               int  `json:"delegate_unreachable_grace_seconds"`
	OfflineCopyMaxLagMinutes   int  `json:"offline_copy_max_lag_minutes"`
	CopyReadsMaxLagSeconds     int  `json:"copy_reads_max_lag_seconds"`
	OfflineSessionHours        int  `json:"offline_session_hours"`
	OfflineSessionGraceMinutes int  `json:"offline_session_grace_minutes"`
	ReplicaWALKeepGB           int  `json:"replica_wal_keep_gb"`
	CopiesOnOperatorPostgres   bool `json:"copies_on_operator_postgres"`
	ProxyCacheMB               int  `json:"proxy_cache_mb"`
}

// DefaultRules are the section's defaults.
func DefaultRules() Rules {
	return Rules{SSO: true, GraceSeconds: 30, OfflineCopyMaxLagMinutes: 10, CopyReadsMaxLagSeconds: 30, OfflineSessionHours: 12,
		OfflineSessionGraceMinutes: 30, ReplicaWALKeepGB: 20, ProxyCacheMB: 2048}
}

// ReadRules reads rules.cluster; any error or bad value gives the default for it.
func ReadRules(ctx context.Context, db *sql.DB) Rules {
	out, _ := LoadRules(ctx, db)
	return out
}

// CachedRules reads rules.cluster at most once per `every` (each read bounded to 3 s);
// a failed read — the home's database down too — keeps the last good section (the
// defaults until one was read), so an outage never swaps the admin's values for them.
// Only the first call waits for the read; later ones answer at once with the section
// they have while it is read again in the background (a caller is never held up by a
// database that does not answer).
func CachedRules(db *sql.DB, every time.Duration) func() Rules {
	var (
		mu      sync.Mutex
		rules   = DefaultRules()
		at      time.Time
		reading bool
	)
	read := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		got, err := LoadRules(ctx, db)
		cancel()
		mu.Lock()
		if err == nil {
			rules = got
		}
		at, reading = time.Now(), false
		mu.Unlock()
	}
	return func() Rules {
		mu.Lock()
		if at.IsZero() && !reading {
			reading = true
			mu.Unlock()
			read()
			mu.Lock()
		} else if !at.IsZero() && time.Since(at) >= every && !reading {
			reading = true
			go read()
		}
		defer mu.Unlock()
		return rules
	}
}

// LoadRules is ReadRules that reports a failed read (the defaults come with it), so a
// caller that already has the section can keep it; no row, or a bad value, is no error.
func LoadRules(ctx context.Context, db *sql.DB) (Rules, error) {
	out := DefaultRules()
	if db == nil {
		return out, nil
	}
	var raw []byte
	if err := db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = 'rules.cluster'`).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, nil
		}
		return out, err
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return out, nil
	}
	var b bool
	if v, ok := m["sso"]; ok && json.Unmarshal(v, &b) == nil {
		out.SSO = b
	}
	var on *bool
	if v, ok := m["copies_on_operator_postgres"]; ok && json.Unmarshal(v, &on) == nil && on != nil {
		out.CopiesOnOperatorPostgres = *on
	}
	var n int
	if v, ok := m["delegate_unreachable_grace_seconds"]; ok && json.Unmarshal(v, &n) == nil && n >= 5 && n <= 600 {
		out.GraceSeconds = n
	}
	for _, r := range []struct {
		key      string
		min, max int
		to       *int
	}{
		{"offline_copy_max_lag_minutes", 1, 1440, &out.OfflineCopyMaxLagMinutes},
		{"copy_reads_max_lag_seconds", 5, 3600, &out.CopyReadsMaxLagSeconds},
		{"offline_session_hours", 1, 720, &out.OfflineSessionHours},
		{"offline_session_grace_minutes", 0, 1440, &out.OfflineSessionGraceMinutes},
		{"replica_wal_keep_gb", 1, 1024, &out.ReplicaWALKeepGB},
	} {
		if v, ok := m[r.key]; ok && json.Unmarshal(v, &n) == nil && n >= r.min && n <= r.max {
			*r.to = n
		}
	}
	if v, ok := m["proxy_cache_mb"]; ok && json.Unmarshal(v, &n) == nil && n >= 0 {
		out.ProxyCacheMB = n
	}
	return out, nil
}
