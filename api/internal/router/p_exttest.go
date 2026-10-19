// "Test connection" for the Plex, dizqueTV and Web search extensions
// (package extcheck); the Cloudflare Stream one is wired in WireP32Remote.
// They run on the home: the Plex token and the search keys are in
// home_secrets there, and /api/extensions is a home route (a full node
// proxies it).
package router

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/extcheck"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

// WireExtensionTesters adds the Plex, dizqueTV and Web search testers to q.
func WireExtensionTesters(q *queues.Handler, db *sql.DB, store secrets.Store, logger *slog.Logger) {
	if q.Testers == nil {
		q.Testers = map[string]queues.ExtensionTester{}
	}
	deps := extcheck.Deps{Ext: q, Secrets: store, DB: db, Logger: logger}
	plex := &extcheck.Plex{Deps: deps}
	dz := &extcheck.DizqueTV{Deps: deps}
	ws := &extcheck.WebSearch{Deps: deps}
	q.Testers[extcheck.PlexExtension] = func(ctx context.Context) (any, error) { return plex.Test(ctx) }
	q.Testers[extcheck.DizqueTVExtension] = func(ctx context.Context) (any, error) { return dz.Test(ctx) }
	q.Testers[extcheck.WebSearchExtension] = func(ctx context.Context) (any, error) { return ws.Test(ctx) }
}
