package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/lib/pq"

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
	Term(ctx context.Context) (int64, error)
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
// with a new password, and returns its access (agent.AccessGranter). What stops it is a
// *cluster.AccessError with its code; a home that may not give roles mends that first
// (MendRoles).
func (h *Handler) GrantAccess(ctx context.Context, nodeID string) (json.RawMessage, error) {
	n, err := h.Store.Node(ctx, nodeID)
	if errors.Is(err, cluster.ErrNotFound) {
		return nil, &cluster.AccessError{Code: cluster.AccessUnknown, Reason: "the home does not know this machine " +
			"(removed, or it never joined)"}
	}
	if err != nil {
		return nil, err
	}
	if n.State != cluster.StateApproved {
		return nil, &cluster.AccessError{Code: cluster.AccessNotApproved,
			Reason: fmt.Sprintf("the machine is %s on the home, not approved", n.State)}
	}
	if ok, why := h.Roles.Supported(ctx); !ok {
		if ok, why = h.MendRoles(ctx, why); !ok {
			code := cluster.AccessCannotCreate
			if why == cluster.ReasonNoGroup {
				code = cluster.AccessNoGroup
			}
			return nil, &cluster.AccessError{Code: code, Reason: "this home cannot give machines roles: " + why}
		}
	}
	dbName, err := h.Roles.DatabaseName(ctx)
	if err != nil {
		return nil, err
	}
	if h.PGBundled && h.PGCA != "" {
		// the machines verify the bundled database with its CA: an access without it
		// would leave them a connection they cannot verify
		if _, err := os.ReadFile(h.PGCA); err != nil {
			return nil, &cluster.AccessError{Code: cluster.AccessCA,
				Reason: fmt.Sprintf("this home's database CA cannot be read: %v", err)}
		}
	}
	// the term first: an access a machine would refuse (no term) changes no password
	var term int64
	if r, ok := h.Roles.(termRoles); ok {
		if term, err = r.Term(ctx); err != nil {
			return nil, &cluster.AccessError{Code: cluster.AccessTerm, Reason: err.Error()}
		}
	}
	password, err := cluster.NewPassword()
	if err != nil {
		return nil, err
	}
	role := roleOf(n)
	err = h.Roles.Create(ctx, role, password)
	if notAdministered(err) && h.mendBundled(ctx) {
		// a role (or the group) the bundled Postgres's superuser made — a restore's, the
		// move from the developer stack's — which the app role may not alter on Postgres
		// 16 until it administers it: the cluster grants made again give it that
		err = h.Roles.Create(ctx, role, password)
	}
	if notAdministered(err) {
		return nil, &cluster.AccessError{Code: cluster.AccessNoAdmin, Reason: "this home's database user may not " +
			"change this machine's role (a role it does not administer: " + err.Error() + ")"}
	}
	if err != nil {
		return nil, err
	}
	ca, tls := h.pgTLS()
	return json.Marshal(machineAccess{Role: role, Password: password, DBHost: h.machineHost(), DBPort: h.machinePort(ctx),
		DBName: dbName, CA: ca, TLS: tls, Term: term})
}

// MendRoles mends why this home's database user may not give machines roles (why:
// Roles.Supported's reason) and says whether it may now, and why not: a bundled Postgres
// (whose superuser is the home's own) gets its cluster grants again from the supervisor
// (CREATEROLE, REPLICATION: mendBundled), and a missing group role is made again with the
// migrations' privileges (cluster.EnsureWorkerGroup) by a user that may create roles. Run
// at the start (MendAtStart) and when a grant meets the same.
func (h *Handler) MendRoles(ctx context.Context, why string) (bool, string) {
	logger := h.log()
	if why == cluster.ReasonCannotCreate && h.mendBundled(ctx) {
		var ok bool
		if ok, why = h.Roles.Supported(ctx); ok {
			return true, ""
		}
	}
	if why == cluster.ReasonNoGroup && h.RolesDB != nil {
		if err := cluster.EnsureWorkerGroup(ctx, h.RolesDB); err != nil {
			logger.Warn("cluster: the machines' group role could not be made again", "err", err,
				"event", "cluster_group_mend_failed")
			return false, why
		}
		logger.Info("cluster: the machines' group role was made again, with the migrations' privileges",
			"event", "cluster_group_mended")
		return h.Roles.Supported(ctx)
	}
	return false, why
}

// mendBundled has the supervisor make the bundled Postgres's cluster grants again (the app
// role's CREATEROLE and REPLICATION, and ADMIN on the machines' roles and their groups) and
// says whether it did; false on a home whose database is not its own.
func (h *Handler) mendBundled(ctx context.Context) bool {
	if !h.PGBundled || h.MendBundled == nil {
		return false
	}
	if err := h.MendBundled(ctx); err != nil {
		h.log().Warn("cluster: this home's database grants could not be made again", "err", err,
			"event", "cluster_roles_mend_failed")
		return false
	}
	h.log().Info("cluster: this home's database grants were made again (its user gives and changes the machines' roles)",
		"event", "cluster_roles_mended")
	return true
}

// notAdministered reports whether err is the database refusing the home's user a role it
// may not change (insufficient_privilege).
func notAdministered(err error) bool {
	var pe *pq.Error
	return errors.As(err, &pe) && pe.Code == "42501"
}

// MendAtStart mends, once at the start, why this home may not give machines roles (a
// bundled Postgres moved or restored without its grants): what MendRoles does when a
// grant meets it, before any machine asks.
func (h *Handler) MendAtStart(ctx context.Context) {
	if ok, why := h.Roles.Supported(ctx); !ok && why != cluster.ReasonSQLite {
		_, _ = h.MendRoles(ctx, why)
	}
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
