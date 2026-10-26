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
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/graphql"
	grpcpipeline "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/pipeline"
	grpcstreaming "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/streaming"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/browse"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/collections"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/contextcard"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/devices"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/enrichment"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/filler"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/jobs"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/libraries"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/models"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/recommendations"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/reports"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/rules"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/search"
	seriesh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/series"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/settings"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/speakers"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/streaming"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/tags"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/transcription"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/videos"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
	perfpkg "github.com/Hamza-Labs-Core/Maktaba/api/internal/perf"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/runtimecfg"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange"
)

// P6Deps bundles the dependencies for Phase 6 handlers. DB is the only
// required field; gRPC clients are optional (a nil client is a no-op).
type P6Deps struct {
	DB *sql.DB
	// Settings is the runtime settings reader (PATCH /api/settings drops its
	// cache). Nil ⇒ one of its own; main.go shares one with the auth surface,
	// so a renamed home reads at once in GET /api/auth/me.
	Settings        *runtimecfg.Reader
	PipelineClient  grpcpipeline.Client
	StreamingClient grpcstreaming.Client
	// SearchSemantic is the semantic leg of POST /api/search. Nil →
	// derived from PipelineClient when it implements
	// grpcpipeline.SemanticSearcher (the real client does); still nil →
	// search is FTS-only.
	SearchSemantic search.SemanticClient
	// SearchPipelineClient is the pipeline worker that holds the search index (the
	// supervisor's index worker, MAKTABA_PIPELINE_SEARCH_ADDR): the semantic leg asks
	// it rather than PipelineClient. Nil → PipelineClient.
	SearchPipelineClient grpcpipeline.Client
	// SearchSemanticBudget overrides the per-request deadline of the
	// semantic leg (zero → search.DefaultSemanticBudget).
	SearchSemanticBudget time.Duration
	URLSigner            streaming.URLSigner
	// StreamOffline lets a full node play with the home's database down (PR #145;
	// nil on the home).
	StreamOffline streaming.Offline
	EventHub      *ws.Hub

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
	// BusResettle settles BusDSN's sslmode again (nil: never), for a listener that
	// cannot connect with it (eventbus.PostgresBackend.Resettle).
	BusResettle func(ctx context.Context) string
	// Secrets is the home's sealed store (slot 0120, story 32.6): the
	// extensions' secret settings live there. nil refuses secret writes.
	Secrets secrets.Store
	// EnrichAutoAcceptCtx, when set, runs the review queue's auto-accept
	// sweep (rules.enrich.auto_accept_threshold, story 26.6) until it
	// ends. Nil: no sweep (tests). The threshold defaults to 0, so the
	// sweep does nothing until an admin sets it.
	EnrichAutoAcceptCtx context.Context
	// OnQueues, when set, is called with the queue/extensions handler
	// before it is mounted (story 32.6 hangs the Cloudflare test on it).
	OnQueues func(*queues.Handler)
}

// modelPipelineClient adapts the gRPC pipeline.Client to the models
// handler's Pipeline interface. A nil client (pipeline address unset)
// yields a nil interface so the handler runs in offline mode rather than
// holding a typed-nil that would slip past its nil check.
func modelPipelineClient(c grpcpipeline.Client) models.Pipeline {
	if c == nil {
		return nil
	}
	return c
}

// searchSemantic is the search handler's semantic leg: the explicit
// SearchSemantic, else the SemanticSearch RPC of SearchPipelineClient (the
// index worker) or, without one, of the pipeline over the same gRPC
// connection, else nil (FTS-only). A nil interface is returned as nil, never
// as a typed nil the handler's nil check would miss.
func searchSemantic(d P6Deps) search.SemanticClient {
	if d.SearchSemantic != nil {
		return d.SearchSemantic
	}
	client := d.PipelineClient
	if d.SearchPipelineClient != nil {
		client = d.SearchPipelineClient
	}
	if ss, ok := client.(grpcpipeline.SemanticSearcher); ok && ss != nil {
		return search.NewGRPCSemantic(ss, d.DB)
	}
	return nil
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
	// the runtime settings PATCH /api/settings stores (search weights, TTLs,
	// pause grace, heartbeat): one reader, its cache dropped by the PATCH
	runtimeSettings := d.Settings
	if runtimeSettings == nil {
		runtimeSettings = runtimecfg.New(d.DB)
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
	// Epic 45 story 45.4: the browsing routes. /api/libraries/summary is a
	// static segment, which chi prefers to /api/libraries/{id}.
	(&browse.Handler{DB: d.DB}).Mount(r)

	// Video patch, delete, process and reprocess write audit rows.
	videoHandler := &videos.Handler{DB: d.DB, Audit: videos.DBAudit{DB: d.DB}}
	// Story 27.11: a display title set by hand reaches the channels' future
	// blocks through the library's display_title stage.
	videoHandler.DisplayTitleRefresh = func(ctx context.Context, libraryID string) error {
		_, err := queues.Enqueue(ctx, d.DB, queues.EnqueueParams{
			SubjectKind: "library", SubjectID: libraryID, Stage: "display_title", Force: true,
			Reason: map[string]any{"why": "display_title_edited", "by": "user"},
		})
		return err
	}
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
		DB:             d.DB,
		Semantic:       searchSemantic(d),
		EmbedCache:     embedCache,
		SemanticBudget: d.SearchSemanticBudget,
		Settings:       runtimeSettings,
		// Story 26.8: "From YouTube" runs yt-dlp's own search (no API key),
		// the binary the TV live slots use. Without yt-dlp the Search page
		// hides the section.
		YouTube: search.NewYtDlpSearcher(os.Getenv("MAKTABA_YTDLP")),
	}
	searchHandler.Mount(r)

	streamSvc := streamingServiceAdapter{client: d.StreamingClient}
	streamHandler := &streaming.Handler{
		DB:       d.DB,
		IPSalt:   analyticsSalt(d),
		Service:  streamSvc,
		Signer:   d.URLSigner,
		Settings: runtimeSettings,
		Offline:  d.StreamOffline,
	}
	streamHandler.Mount(r)

	jobsHandler := &jobs.Handler{DB: d.DB, Settings: runtimeSettings}
	jobsHandler.Mount(r)

	colHandler := &collections.Handler{DB: d.DB}
	colHandler.Mount(r)
	// Phase 8 / Story 9.14: smart-collection ?freeze convert endpoint.
	colHandler.MountSmart(r)

	tagsHandler := &tags.Handler{DB: d.DB}
	tagsHandler.Mount(r)

	// Story 27.10 — filler & bumper pools/items (Epic 27 receiver batch).
	fillerHandler := &filler.Handler{DB: d.DB, Drafts: tvchange.NewDrafts(d.DB)} // Epic 33: drafts while staging is on
	fillerHandler.Mount(r)

	speakerHandler := &speakers.Handler{DB: d.DB}
	speakerHandler.Mount(r)

	settingsHandler := &settings.Handler{
		DB:         d.DB,
		Pipeline:   pipelineSettingsAdapter{client: d.PipelineClient},
		FileEnvCfg: map[string]any{},
		Settings:   runtimeSettings,
	}
	settingsHandler.Mount(r)

	// Admin-editable pipeline rules (planner / grouping / detection),
	// stored as per-section overrides in app_settings (rules.<section>).
	rulesHandler := &rules.Handler{DB: d.DB}
	if d.DB != nil {
		// Epic 33: with TV staging on, rules.tv / rules.tv_output saves go into a TV change draft
		rulesHandler.Staging = tvchange.NewDrafts(d.DB)
	}
	rulesHandler.Mount(r)

	// Model Management surface for the Settings page (catalog + async
	// download/activate/test). Proxies to the pipeline's model service;
	// a nil client degrades to the static fallback catalog + "pipeline
	// offline" on mutations.
	modelsHandler := models.New(modelPipelineClient(d.PipelineClient))
	modelsHandler.Mount(r)

	recHandler := &recommendations.Handler{DB: d.DB, Settings: runtimeSettings}
	recHandler.Mount(r)

	// Epic 26 — Content Intelligence (batch 2). Enrichment review
	// (26.6), the web context card (26.9), and the cross-library series
	// browser (26.10). All degrade gracefully with a nil DB.
	enrichHandler := &enrichment.Handler{DB: d.DB}
	if d.DB != nil {
		// Find metadata / Re-enrich run as the engine's enrich stage (stub
		// audit #4), so they show in the control centre.
		enrichHandler.Enqueue = func(ctx context.Context, videoID string, reason map[string]any) error {
			_, err := queues.Enqueue(ctx, d.DB, queues.EnqueueParams{
				SubjectKind: "video", SubjectID: videoID, Stage: "enrich", Force: true,
				Reason: queues.ReasonFor(ctx, "find_metadata", reason),
			})
			return err
		}
	}
	enrichHandler.Mount(r)
	if d.EnrichAutoAcceptCtx != nil && d.DB != nil {
		go enrichHandler.RunAutoAccept(d.EnrichAutoAcceptCtx, enrichment.DefaultAutoAcceptInterval, nil)
	}

	contextHandler := &contextcard.Handler{DB: d.DB}
	contextHandler.Mount(r)

	seriesHandler := &seriesh.Handler{DB: d.DB}
	seriesHandler.Mount(r)

	devHandler := &devices.Handler{DB: d.DB}
	devHandler.Mount(r)
	// Story 12.11 downloaded-flag sync routes (slot 0063).
	devHandler.MountDownloads(r)

	wsHandler := &ws.Handler{Hub: hub, Settings: runtimeSettings}
	// Per-subscriber visibility: /ws/jobs and /ws/queues frames are kept to
	// the caller's libraries, /ws/playback/{video_id} needs the video's.
	wsHandler.Filter = queues.FilterEvent
	wsHandler.VideoLibrary = func(ctx context.Context, id string) (string, error) {
		var lib string
		err := d.DB.QueryRowContext(ctx, `SELECT library_id FROM videos WHERE id = $1`, id).Scan(&lib)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return lib, err
	}
	// Cross-replica WS event bus (Epic 19 Story 19.2 / HLB-353).
	// When a DSN + ctx are supplied, every replica appends events to
	// the durable slot-0061 `events` table; the slot-0061 trigger
	// fires pg_notify('ws.events',…); this replica's LISTEN loop fans
	// them out to `hub`, so a client whose socket is on *this* replica
	// receives an event published on *any* replica. The handler also
	// replays missed events on reconnect.
	bus := wireEventBus(d, hub, wsHandler)

	// Plan 06-11 queue control centre. The bus (when wired) carries the
	// durable queue.control / pool.updated events; snapshots go straight
	// to the hub every 2 s while someone is subscribed to /ws/queues.
	// Whole-library transcription report (/transcription page). It
	// rides the same /ws/queues + jobs streams for liveness, and gives
	// the Transcribe card its time left.
	transcriptionHandler := &transcription.Handler{DB: d.DB}
	queuesHandler := &queues.Handler{DB: d.DB, Hub: hub, StageETA: map[string]func(context.Context, time.Time) (*float64, error){
		"transcribe": transcriptionHandler.ETASeconds,
	}}
	if bus != nil {
		queuesHandler.Events = bus
	}
	queuesHandler.Secrets = d.Secrets
	if d.OnQueues != nil {
		d.OnQueues(queuesHandler)
	}
	queuesHandler.Mount(r)
	wsHandler.Initial = queuesHandler.InitialFrame
	if d.BusCtx != nil {
		go queuesHandler.PublishSnapshots(d.BusCtx, queues.DefaultSnapshotInterval)
	}
	wsHandler.Mount(r)

	transcriptionHandler.Mount(r)
	// Control-centre reports: the review pages, channel plan (and its
	// dry-run preview) and channel audit the pipeline writes to its
	// reports folder, plus the small thumbnails the review pages link.
	reportsHandler := &reports.Handler{DB: d.DB}
	if d.PipelineClient != nil {
		reportsHandler.Pipeline = d.PipelineClient
	}
	reportsHandler.Mount(r)

	// GraphQL (Story 7.17): one schema over these REST handlers. Each
	// root field runs its REST route in process through r, so access,
	// rate limits, Idempotency-Key replay and audit are the REST ones;
	// subscriptions read this hub. GET /graphql is the
	// graphql-transport-ws upgrade.
	gqlHandler := graphql.NewHandlerFromEnv(graphql.NewResolvers(r, d.DB, hub))
	// Persisted queries (AC-5): the slot-0140 store; allowlist mode is the
	// graphql.persisted_only runtime setting (default off).
	gqlHandler.Persisted = graphql.NewPersistedStore(d.DB)
	gqlHandler.PersistedOnly = func(ctx context.Context) bool {
		on, _ := runtimeSettings.Bool(ctx, runtimecfg.GraphQLPersistedOnly)
		return on
	}
	r.Method(http.MethodPost, "/graphql", gqlHandler)
	r.Method(http.MethodGet, "/graphql", gqlHandler)
	r.Method(http.MethodGet, "/graphql/schema", gqlHandler)
}

// analyticsSalt is the home's salt for the viewers' addresses (AnalyticsSalt), from the
// store P6 is given; without one, no address is hashed.
func analyticsSalt(d P6Deps) string {
	if d.Secrets == nil {
		return ""
	}
	return AnalyticsSalt(context.Background(), d.Secrets, nil)
}
