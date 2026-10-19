// "Test connection" for the Plex and dizqueTV extensions (package
// extcheck); the Cloudflare Stream one is wired in WireP32Remote. Both run
// on the home: the Plex token is in home_secrets there, and
// /api/extensions is a home route (a full node proxies it).
package router

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/extcheck"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

// WireExtensionTesters adds the Plex and dizqueTV testers to q.
func WireExtensionTesters(q *queues.Handler, db *sql.DB, store secrets.Store, logger *slog.Logger) {
	if q.Testers == nil {
		q.Testers = map[string]queues.ExtensionTester{}
	}
	deps := extcheck.Deps{Ext: q, Secrets: store, DB: db, Logger: logger}
	plex := &extcheck.Plex{Deps: deps}
	dz := &extcheck.DizqueTV{Deps: deps}
	q.Testers[extcheck.PlexExtension] = func(ctx context.Context) (any, error) { return plex.Test(ctx) }
	q.Testers[extcheck.DizqueTVExtension] = func(ctx context.Context) (any, error) { return dz.Test(ctx) }
}
