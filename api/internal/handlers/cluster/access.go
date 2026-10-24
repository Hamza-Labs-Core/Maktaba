package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
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

// machineAccess is a machine's re-made access, as its home.access command takes it.
type machineAccess struct {
	Role     string `json:"role"`
	Password string `json:"password"`
	DBHost   string `json:"db_host,omitempty"`
	DBPort   int    `json:"db_port"`
	DBName   string `json:"db_name"`
	CA       string `json:"database_ca,omitempty"`
	TLS      string `json:"database_tls,omitempty"`
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
	password, err := cluster.NewPassword()
	if err != nil {
		return nil, err
	}
	role := roleOf(n)
	if err := h.Roles.Create(ctx, role, password); err != nil {
		return nil, err
	}
	ca, tls := h.pgTLS()
	return json.Marshal(machineAccess{Role: role, Password: password, DBHost: h.accessHost(), DBPort: h.accessPort(ctx),
		DBName: dbName, CA: ca, TLS: tls})
}

// accessPort is the database's port as the machines reach it: the bundled Postgres's,
// else the port the database itself says it listens on (an operator's Postgres on a port
// other than 5432), else PGPort.
func (h *Handler) accessPort(ctx context.Context) int {
	if h.PGBundled {
		return h.pgPort()
	}
	if r, ok := h.Roles.(accessRoles); ok {
		if p, err := r.ServerPort(ctx); err == nil && p > 0 {
			return p
		}
	}
	return h.pgPort()
}

// accessHost is the database's host when it is not on the home's own machine (an
// operator's Postgres elsewhere); "": the machines reach it at the home's address.
func (h *Handler) accessHost() string {
	if h.PGBundled {
		return ""
	}
	for _, k := range []string{"MAKTABA_DATABASE_URL", "DATABASE_URL"} {
		u, err := url.Parse(strings.TrimSpace(os.Getenv(k)))
		if err != nil || u.Hostname() == "" {
			continue
		}
		host := u.Hostname()
		if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
			return ""
		}
		return host
	}
	return ""
}
