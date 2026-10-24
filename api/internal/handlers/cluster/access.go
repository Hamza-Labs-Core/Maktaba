package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
)

// The home owns its machines' database access: a machine that cannot log in to the
// home's database (its role is missing on it, refused, or the database moved: an app's
// move from an operator Postgres to its own, a restore, a new port) gets its role made
// again on the home's current database, with a new password (none is kept to recover),
// and the access pushed to it over the machine channel (agent.Hub.EnsureAccess).

// accessRoles are the Roles that can say whether a role logs in, and the database's port.
type accessRoles interface {
	Usable(ctx context.Context, role string) (bool, error)
	ServerPort(ctx context.Context) (int, error)
}

// termRoles are the Roles that can say the cluster's term.
type termRoles interface {
	Term(ctx context.Context) int64
}

// machineAccess is a machine's re-made access, as its home.access command takes it.
type machineAccess struct {
	Role     string `json:"role"`
	Password string `json:"password"`
	DBHost   string `json:"db_host,omitempty"`
	DBPort   int    `json:"db_port"`
	DBName   string `json:"db_name"`
	CA       string `json:"database_ca,omitempty"`
	TLS      string `json:"database_tls,omitempty"`
	// Term is this home's: a machine that follows a home of a later term refuses it
	Term int64 `json:"term,omitempty"`
}

// roleOf is a machine's database role (the one its join recorded, else its name).
func roleOf(n cluster.Node) string {
	if n.DBRole != nil && *n.DBRole != "" {
		return *n.DBRole
	}
	return cluster.RoleName(n.ID)
}

// AccessUsable reports whether an approved machine's role logs in on the home's database
// now (agent.AccessGranter). A home whose roles cannot say is not asked again.
func (h *Handler) AccessUsable(ctx context.Context, nodeID string) (bool, error) {
	n, err := h.Store.Node(ctx, nodeID)
	if err != nil {
		return false, err
	}
	if n.State != cluster.StateApproved {
		return true, nil // not this home's to give (revoked, removed): left as it is
	}
	r, ok := h.Roles.(accessRoles)
	if !ok {
		return true, nil
	}
	return r.Usable(ctx, roleOf(n))
}

// GrantAccess makes an approved machine's role again on the home's current database,
// with a new password, and returns its access (agent.AccessGranter).
func (h *Handler) GrantAccess(ctx context.Context, nodeID string) (json.RawMessage, error) {
	n, err := h.Store.Node(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	if n.State != cluster.StateApproved {
		return nil, fmt.Errorf("the machine is %s, not approved", n.State)
	}
	if ok, why := h.Roles.Supported(ctx); !ok {
		return nil, fmt.Errorf("this home cannot give machines roles: %s", why)
	}
	dbName, err := h.Roles.DatabaseName(ctx)
	if err != nil {
		return nil, err
	}
	if h.PGBundled && h.PGCA != "" {
		// the machines verify the bundled database with its CA: an access without it
		// would leave them a connection they cannot verify
		if _, err := os.ReadFile(h.PGCA); err != nil {
			return nil, fmt.Errorf("this home's database CA cannot be read: %w", err)
		}
	}
	password, err := cluster.NewPassword()
	if err != nil {
		return nil, err
	}
	role := roleOf(n)
	if err := h.Roles.Create(ctx, role, password); err != nil {
		return nil, err
	}
	ca, tls := h.pgTLS()
	var term int64
	if r, ok := h.Roles.(termRoles); ok {
		term = r.Term(ctx)
	}
	return json.Marshal(machineAccess{Role: role, Password: password, DBHost: h.machineHost(), DBPort: h.machinePort(ctx),
		DBName: dbName, CA: ca, TLS: tls, Term: term})
}

// AccessTarget is where a re-made access points (agent.AccessGranter): the host the
// machines are handed ("": the one they reach the home at), the port and the database.
func (h *Handler) AccessTarget(ctx context.Context, _ string) (string, int, string, error) {
	dbName, err := h.Roles.DatabaseName(ctx)
	if err != nil {
		return "", 0, "", err
	}
	return h.machineHost(), h.machinePort(ctx), dbName, nil
}

// machinePort is the database's port as the machines reach it, the join's and a re-made
// access's alike: the bundled Postgres's (MAKTABA_CLUSTER_PG_PORT); else
// MAKTABA_CLUSTER_DB_PORT, when the port the machines reach differs from the database's
// own (a container's published port: the compose files pass MAKTABA_PG_PORT through it);
// else the port the database says it listens on (an operator's Postgres on another port
// than 5432); else PGPort.
func (h *Handler) machinePort(ctx context.Context) int {
	if h.PGBundled {
		return h.pgPort()
	}
	if p, err := strconv.Atoi(strings.TrimSpace(os.Getenv("MAKTABA_CLUSTER_DB_PORT"))); err == nil && p > 0 && p < 65536 {
		return p
	}
	if r, ok := h.Roles.(accessRoles); ok {
		if p, err := r.ServerPort(ctx); err == nil && p > 0 {
			return p
		}
	}
	return h.pgPort()
}

// hostRe is a host name or address as a setting may give it.
var hostRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,253}$`)

// machineHost is the database's host as the machines are handed it: "" (they reach it at
// the host they reach the home at, as at their join) unless MAKTABA_CLUSTER_DB_HOST names
// another (a database on another machine than the home). The home's own address for its
// database (compose's "postgres", a loopback) is never one the machines can reach.
func (h *Handler) machineHost() string {
	if h.PGBundled {
		return ""
	}
	if v := strings.TrimSpace(os.Getenv("MAKTABA_CLUSTER_DB_HOST")); hostRe.MatchString(v) {
		return v
	}
	return ""
}
