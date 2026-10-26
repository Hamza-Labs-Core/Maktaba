// Epic 35 phase C wiring (stories 35.8–35.11): sign-ins across the machines of a
// cluster and every machine's /api/cluster/self.
//
//	POST /api/cluster/delegate/introspect   home, node-signed
//	POST /api/cluster/delegate/signout      home, node-signed
//	GET  /api/cluster/self                  every machine, public
//	*    /api/cluster/sso/…                 every machine: 404 (the cluster sign-in, removed)
package router

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/authz"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/pat"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/sessions"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/clusterauth"
)

// ClusterAuthDeps bundles the home's delegation endpoints.
type ClusterAuthDeps struct {
	DB     *sql.DB
	Logger *slog.Logger
}

// MountClusterAuth mounts the home's half (nil DB: nothing).
func MountClusterAuth(r chi.Router, d ClusterAuthDeps) *clusterauth.Home {
	if d.DB == nil {
		return nil
	}
	h := &clusterauth.Home{DB: d.DB, Sessions: sessions.New(d.DB), Users: users.New(d.DB),
		ACL: &authz.ACLStore{DB: d.DB}, PAT: pat.New(d.DB), Logger: d.Logger}
	h.Mount(r)
	return h
}

// NodeLocalDeps bundles /api/cluster/self and a machine's other routes of its own.
type NodeLocalDeps struct {
	Self clusterauth.SelfFunc
	// Moved serves GET /api/cluster/moved/{node}: a switch's notice of the new home for
	// a machine that was off (Epic 37); nil: not mounted.
	Moved http.HandlerFunc
	// EmergencyStatus and Emergency serve GET and POST /api/cluster/emergency on a full
	// node: a standby home taking over when its home is gone (Epic 37); nil: not mounted.
	EmergencyStatus, Emergency http.HandlerFunc
	// TermClaim serves POST /api/cluster/term-claim: another standby home's claim on an
	// emergency's term (Epic 37); nil: not mounted.
	TermClaim http.HandlerFunc
}

// MountNodeLocal mounts GET /api/cluster/self, the removed cluster sign-in's 404s
// (clusterauth.RemovedRoutes) and a switch's notices of the new home.
func MountNodeLocal(r chi.Router, d NodeLocalDeps) {
	clusterauth.MountSelf(r, d.Self)
	clusterauth.MountRemoved(r)
	if d.Moved != nil {
		r.Get("/api/cluster/moved/{node}", d.Moved)
	}
	if d.EmergencyStatus != nil && d.Emergency != nil {
		r.Get("/api/cluster/emergency", d.EmergencyStatus)
		r.Post("/api/cluster/emergency", d.Emergency)
	}
	if d.TermClaim != nil {
		r.Post("/api/cluster/term-claim", d.TermClaim)
	}
}
