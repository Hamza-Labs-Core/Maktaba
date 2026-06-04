// Phase 11 / web-pages-batch2 handler wiring. Mounts the admin
// Library-ACL matrix and the self-service personal-access-token surface.
// Kept in its own file so the batch-2 diff stays isolated, mirroring the
// p6/p9/p10 convention.
//
// The PAT *credential* middleware (which attaches a principal for
// `Authorization: Bearer pat_...`) is NOT installed here — it lives in
// the transport-security chain (main.go applySecurity) alongside the JWT
// and cookie middlewares. This file only wires the route handlers.
package router

import (
	"database/sql"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/pat"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/apitokens"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/libraryacl"
)

// P11Deps bundles the dependencies for the batch-2 handlers.
type P11Deps struct {
	DB *sql.DB
}

// MountP11 attaches the batch-2 route handlers onto r. Safe to skip when
// DB is nil (no-DB stub); routes simply aren't registered.
func MountP11(r chi.Router, d P11Deps) {
	if d.DB == nil {
		return
	}
	libraryacl.New(d.DB).Mount(r)
	apitokens.New(pat.New(d.DB)).Mount(r)
}
