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
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/graphql"
	grpcpipeline "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/pipeline"
	grpcstreaming "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/streaming"
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
	perfpkg "github.com/Hamza-Labs-Core/Maktaba/api/internal/perf"
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

	// PerfRegistry, when set, receives the named hot-path caches MountP6
	// instantiates (e.g. the search embedding cache, Story 18.2 AC2 /
	// HLB-333) so the admin flush endpoint can drop them by name
	// (Story 18.8 AC4 / HLB-346). Nil → caches still work, just aren't
	// admin-flushable.
	PerfRegistry *perfpkg.Registry

	// BusCtx / BusDSN enable the cross-replica WS event bus (Epic 19
	// Story 19.2). When both are set MountP6 stands up an
	// eventbus.Bus over the slot-0061 `events` table + LISTEN/NOTIFY:
	// a per-replica LISTEN loop fans events out to this replica's hub,
	// the WS handler replays missed events on reconnect, and a pruner
	// enforces 7-day retention. Both zero → single-process hub only
	// (dev/test path; behaviour unchanged). Lifetime is tied to
	// BusCtx so a graceful shutdown stops the loop.
	BusCtx context.Context
	BusDSN string
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

	// Story 9.6 AC-1: inject the concrete library-scoped scan
	// enqueuer so POST /api/libraries/{id}/scan actually creates a
	// pending SCAN job (slot-0058 contract, mirrors pipeline
	// enqueue_scan) instead of soft-failing with an empty job_id.
	libHandler := &libraries.Handler{
		DB:          d.DB,
		JobEnqueuer: &libraries.PostgresJobEnqueuer{DB: d.DB},
	}
	libHandler.Mount(r)
	// Phase 8 / Story 9.17: surfaced library audit feed.
	libHandler.MountAudit(r)

	videoHandler := &videos.Handler{DB: d.DB}
	videoHandler.Mount(r)

	// Story 18.2 AC2/AC4 (HLB-333): the search handler gets an
	// in-process embedding-result cache (reusing the orphaned generic
	// perf.Cache — HLB-346, no second cache impl) and the default hard
	// per-request semantic deadline. The cache is registered so
	// POST /admin/cache/search_embed/flush actually drops it (Story
	// 18.8 AC4) instead of 404-ing on an empty registry.
	embedCache := perfpkg.NewCache[[]search.Hit]("search_embed", 10000, 10*time.Minute)
	if d.PerfRegistry != nil {
		d.PerfRegistry.Register(embedCache)
	}
	searchHandler := &search.Handler{
		DB:         d.DB,
		Semantic:   d.SearchSemantic,
		EmbedCache: embedCache,
	}
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
	// Phase 8 / Story 9.14: smart-collection ?freeze convert endpoint.
	colHandler.MountSmart(r)

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
	// Cross-replica WS event bus (Epic 19 Story 19.2 / HLB-353).
	// When a DSN + ctx are supplied, every replica appends events to
	// the durable slot-0061 `events` table; the slot-0061 trigger
	// fires pg_notify('ws.events',…); this replica's LISTEN loop fans
	// them out to `hub`, so a client whose socket is on *this* replica
	// receives an event published on *any* replica. The handler also
	// replays missed events on reconnect.
	wireEventBus(d, hub, wsHandler)
	wsHandler.Mount(r)

	// GraphQL — schema endpoint + SDL.
	r.Method(http.MethodPost, "/graphql", graphql.Handler{})
	r.Method(http.MethodGet, "/graphql/schema", graphql.Handler{})
}
