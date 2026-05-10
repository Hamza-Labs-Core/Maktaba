// Phase 6 / Epic 7 handler wiring. The router package is the central
// place that holds the chi mux; each handler package's Mount(r) adds
// its routes. Keeping this in its own file makes the wiring readable
// and the diff for Phase 6 isolated.
//
// MountP6 is opt-in — callers that want only the foundation routes
// (system/health, system/version, JWKS) skip it. Production wires it
// from main.go once a *sql.DB is available.
package router

import (
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/collections"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/devices"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/jobs"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/libraries"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/recommendations"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/search"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/settings"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/speakers"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/streaming"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/tags"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/videos"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/graphql"
	grpcpipeline "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/pipeline"
	grpcstreaming "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/streaming"
)

// P6Deps bundles the dependencies for Phase 6 handlers. DB is the only
// required field; gRPC clients are optional (a nil client is a no-op).
type P6Deps struct {
	DB              *sql.DB
	PipelineClient  grpcpipeline.Client
	StreamingClient grpcstreaming.Client
	SearchSemantic  search.SemanticClient
	URLSigner       streaming.URLSigner
	EventHub        *ws.Hub
}

// MountP6 attaches every Phase 6 handler onto r. Safe to call with a
// nil DB — handlers gracefully degrade (returning 503 or empty lists
// where appropriate), keeping the dev/test path runnable without a
// database.
func MountP6(r chi.Router, d P6Deps) {
	if d.DB == nil {
		return
	}
	hub := d.EventHub
	if hub == nil {
		hub = ws.NewHub()
	}

	libHandler := &libraries.Handler{DB: d.DB}
	libHandler.Mount(r)

	videoHandler := &videos.Handler{DB: d.DB}
	videoHandler.Mount(r)

	searchHandler := &search.Handler{DB: d.DB, Semantic: d.SearchSemantic}
	searchHandler.Mount(r)

	streamSvc := streamingServiceAdapter{client: d.StreamingClient}
	streamHandler := &streaming.Handler{
		DB:      d.DB,
		Service: streamSvc,
		Signer:  d.URLSigner,
	}
	streamHandler.Mount(r)

	jobsHandler := &jobs.Handler{DB: d.DB}
	jobsHandler.Mount(r)

	colHandler := &collections.Handler{DB: d.DB}
	colHandler.Mount(r)

	tagsHandler := &tags.Handler{DB: d.DB}
	tagsHandler.Mount(r)

	speakerHandler := &speakers.Handler{DB: d.DB}
	speakerHandler.Mount(r)

	settingsHandler := &settings.Handler{
		DB:         d.DB,
		Pipeline:   pipelineSettingsAdapter{client: d.PipelineClient},
		FileEnvCfg: map[string]any{},
	}
	settingsHandler.Mount(r)

	recHandler := &recommendations.Handler{DB: d.DB}
	recHandler.Mount(r)

	devHandler := &devices.Handler{DB: d.DB}
	devHandler.Mount(r)

	wsHandler := &ws.Handler{Hub: hub}
	wsHandler.Mount(r)

	// GraphQL — schema endpoint + SDL.
	r.Method(http.MethodPost, "/graphql", graphql.Handler{})
	r.Method(http.MethodGet, "/graphql/schema", graphql.Handler{})
}
