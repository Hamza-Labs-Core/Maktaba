// Epic 35 phase C wiring (stories 35.8–35.11): sign-ins across the machines of a
// cluster and every machine's /api/cluster/self.
//
//	POST /api/cluster/delegate/introspect   home, node-signed
//	GET  /api/cluster/sso/authorize         home, the browser (signed in on the home)
//	POST /api/cluster/sso/exchange          home, node-signed
//	GET  /api/cluster/self                  every machine, public
//	GET  /api/cluster/sso/start|callback    a node, the browser
package router

import (
	"database/sql"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/authz"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/pat"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/sessions"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/clusterauth"
)

// ClusterAuthDeps bundles the home's delegation and sign-in endpoints.
type ClusterAuthDeps struct {
	DB *sql.DB
	// Nodes is the machine registry (nil: a Postgres store on DB).
	Nodes cluster.Store
	// WebURL is the home's own web address (its sign-in page).
	WebURL string
	Logger *slog.Logger
}

// MountClusterAuth mounts the home's half (nil DB: nothing).
func MountClusterAuth(r chi.Router, d ClusterAuthDeps) *clusterauth.Home {
	if d.DB == nil {
		return nil
	}
	nodes := d.Nodes
	if nodes == nil {
		nodes = &cluster.PGStore{DB: d.DB}
	}
	h := &clusterauth.Home{DB: d.DB, Sessions: sessions.New(d.DB), Users: users.New(d.DB),
		ACL: &authz.ACLStore{DB: d.DB}, PAT: pat.New(d.DB), Nodes: nodes, WebURL: d.WebURL, Logger: d.Logger}
	h.Mount(r)
	return h
}

// NodeLocalDeps bundles /api/cluster/self and, on a node, the sign-in's node half.
type NodeLocalDeps struct {
	Self clusterauth.SelfFunc
	// Node is set on a full node; nil on the home (start / callback answer 404 there).
	Node *clusterauth.Node
}

// MountNodeLocal mounts GET /api/cluster/self and the sign-in's start and callback.
func MountNodeLocal(r chi.Router, d NodeLocalDeps) {
	clusterauth.MountSelf(r, d.Self)
	d.Node.Mount(r)
}
