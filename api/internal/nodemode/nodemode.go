// Package nodemode is what a full node's API (Epic 35 story 35.8, design §9.1) knows
// about itself: that it runs in node mode (MAKTABA_NODE_MODE=1, set by the node's
// supervisor from node.json), the home it belongs to, and the rule that it never
// changes the schema: it refuses to migrate, and refuses to start when its database
// role could run DDL (a copied superuser URL must never let a node migrate by mistake).
package nodemode

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
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

// CheckRole refuses a role that can run DDL (design §9.1). A role whose privileges
// cannot be read is refused too: the node fails closed.
func CheckRole(ctx context.Context, db *sql.DB) error {
	p, err := ReadPrivileges(ctx, db)
	if err != nil {
		return fmt.Errorf("%w: cannot read the role's privileges: %v", ErrDDLRole, err)
	}
	if why := p.DDL(); len(why) > 0 {
		return fmt.Errorf("%w: role %q has %s — use the node's own database role from node.json, never the home's",
			ErrDDLRole, p.Role, strings.Join(why, ", "))
	}
	return nil
}

// Rules is rules.cluster (app_settings; the pipeline's ClusterMachinesConfig declares
// the section): cluster sign-in on or off, how long a node waits for an unreachable
// home before it refuses signed-in requests, how long it stays readable meanwhile, its
// picture cache.
type Rules struct {
	SSO                bool `json:"sso"`
	GraceSeconds       int  `json:"delegate_unreachable_grace_seconds"`
	OfflineReadMinutes int  `json:"offline_read_minutes"`
	ProxyCacheMB       int  `json:"proxy_cache_mb"`
}

// DefaultRules are the section's defaults.
func DefaultRules() Rules {
	return Rules{SSO: true, GraceSeconds: 30, OfflineReadMinutes: 10, ProxyCacheMB: 2048}
}

// ReadRules reads rules.cluster; any error or bad value gives the default for it.
func ReadRules(ctx context.Context, db *sql.DB) Rules {
	out := DefaultRules()
	if db == nil {
		return out
	}
	var raw []byte
	if err := db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = 'rules.cluster'`).Scan(&raw); err != nil {
		return out
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return out
	}
	var b bool
	if v, ok := m["sso"]; ok && json.Unmarshal(v, &b) == nil {
		out.SSO = b
	}
	var n int
	if v, ok := m["delegate_unreachable_grace_seconds"]; ok && json.Unmarshal(v, &n) == nil && n >= 5 && n <= 600 {
		out.GraceSeconds = n
	}
	if v, ok := m["offline_read_minutes"]; ok && json.Unmarshal(v, &n) == nil && n >= 0 && n <= 1440 {
		out.OfflineReadMinutes = n
	}
	if v, ok := m["proxy_cache_mb"]; ok && json.Unmarshal(v, &n) == nil && n >= 0 {
		out.ProxyCacheMB = n
	}
	return out
}
