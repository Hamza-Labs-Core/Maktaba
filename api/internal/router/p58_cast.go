package router

// Epic 58 story 58.07: the cast grant. POST /api/cast/grants mints one for a signed-in
// sender (handlers/streaming.CastGrants); the grant's gate (Deps.CastGate, installed by New)
// holds the receiver that uses it to its items, and gets the database here.

import (
	"database/sql"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/castgrant"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/streaming"
)

// CastDeps are the cast grant's dependencies.
type CastDeps struct {
	DB   *sql.DB
	Keys *keys.Set
	// Gate is the router's grant gate (Deps.CastGate): it gets DB. Nil: none to give.
	Gate *castgrant.Gate
}

// MountCast wires POST /api/cast/grants.
func MountCast(r chi.Router, d CastDeps) {
	d.Gate.SetDB(d.DB)
	(&streaming.CastGrants{DB: d.DB, Keys: d.Keys}).Mount(r)
}
