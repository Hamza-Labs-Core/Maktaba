package router

// Where every mounted route is served when the API runs on a full node (Epic 35 story
// 35.10, design §11.1, §11.4). The home serves every class itself. Rules for routes
// that a newer mount adds go here; TestEveryRouteHasPlacementClass fails until they do,
// and a route no rule names is `home` meanwhile (the safe default).
//
// What `any` may be: reads the node's database role can do (slot 0103 revokes users,
// sessions, tokens, devices, viewing data, audit reads and the cluster tables from it)
// and that need no home-only process (the vector index, the pipeline's gRPC, the image
// caches, the TV controller, secrets). Writes are `home` in v1: one writer path keeps the
// audit, NOTIFY and cache invalidation on the home.

import (
	"net/http"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/placement"
)

// Placement is the route table.
var Placement = buildPlacement()

func buildPlacement() placement.Table {
	var t placement.Table
	add := func(rs []placement.Rule) { t = append(t, rs...) }
	get := placement.Get
	all := []string(nil)

	// ---- local: this machine ----
	add(placement.Exact(placement.Local, all,
		"/healthz",
		"/.well-known/security.txt",
		"/api/system/version",
		"/api/system/health",
		"/api/system/processes",
		"/api/system/sbom",
		// this machine's identity, its home, its web address (story 35.11)
		"/api/cluster/self",
		// a switch's notice of the new home, from this machine's copy (Epic 37)
		"/api/cluster/moved/{node}",
		// a standby home taking over when its home is gone, and another's claim on the
		// term it would take (Epic 37)
		"/api/cluster/emergency",
		"/api/cluster/term-claim",
		// the machine's own processes on its cluster link (story 35.16)
		"/api/cluster/link/local/agent",
		"/api/cluster/link/local/fetch",
		// the cluster sign-in's routes, removed: this machine answers them 404 itself
		// (clusterauth.RemovedRoutes), the home away or not
		"/api/cluster/sso/start",
		"/api/cluster/sso/callback",
		"/api/cluster/sso/authorize",
		"/api/cluster/sso/exchange",
		// this machine's update moves its TV to the new streaming binary (story 35.6):
		// its own supervisor asks its own API
		"/internal/updates/tv-change",
		"/internal/updates/tv-change/{run}",
		"/internal/updates/tv-change/{run}/withdraw",
		// a restore the command line asked for, recorded by this machine's API (story 35.5)
		"/internal/updates/restored",
		// VOD is served by the node itself: its streaming role reads the NAS (R13)
		"/api/stream/capabilities",
		"/api/stream/sessions",
		"/api/stream/sessions/{id}",
		// a player's playback failure is logged where its stream is served
		"/api/stream/sessions/{id}/errors",
	))

	// ---- any: database reads through the node's role ----
	add(placement.Exact(placement.Any, get,
		"/api/videos",
		"/api/videos/{id}",
		"/api/videos/{id}/chapters",
		"/api/videos/{id}/subtitles",
		"/api/videos/{id}/segments",
		"/api/libraries",
		"/api/libraries/{id}",
		"/api/libraries/{id}/stats",
		"/api/collections",
		"/api/collections/{id}",
		"/api/collections/{id}/items",
		"/api/items/library/{id}/collections",
		"/api/series",
		"/api/series/{id}",
		"/api/series/{id}/episodes",
		"/api/series/{id}/missing",
		"/api/seasons/styles",
		"/api/tags",
		"/api/speakers",
		"/api/filler/pools",
		"/api/channels",
		"/api/channels/{id}",
		"/api/channels/guide",
		"/api/channels/now",
		"/api/channels/sources",
		"/api/channels/{id}/guide",
		"/api/channels/{id}/schedule",
		"/api/jobs",
		"/api/jobs/{id}",
		"/api/jobs/{id}/events",
		"/api/queues/graph",
		"/api/queues/health",
		"/api/queues/insights",
		"/api/queues/libraries",
		"/api/queues/pulse",
		"/api/queues/running",
		"/api/queues/{stage}/failures",
		"/api/queues/{stage}/jobs",
		"/api/queues/{stage}/rate",
		"/api/queue/stats",
		"/api/pools",
		"/api/transcription/summary",
		"/api/rules",
		"/api/settings",
	))

	// ---- home: exact routes that sit next to local/any ones ----
	add(placement.Exact(placement.Home, all,
		// watch progress is viewing data (revoked from the node role)
		"/api/stream/sessions/{id}/progress",
		// the home publishes its keys; a node verifies against them
		"/api/.well-known/jwks.json",
		// the queue snapshot and the workers list read the machines' registry
		// (cluster_nodes is revoked from the node role)
		"/api/queues",
		"/api/queues/{stage}",
		// Epic 40: the failures page reads the ignores and the checks, its actions write
		"/api/queues/failures",
		"/api/queues/failures/actions",
		"/api/workers",
		// story 35.9: the home's half of delegation
		"/api/cluster/delegate/introspect",
		"/api/cluster/delegate/signout",
		// Epic 45: the home rows read the caller's playback and pins (viewing data;
		// /api/libraries/summary and /hub are home by their prefix)
		"/api/home",
	))
	// every write of an `any` family, and the reads that need the home
	add([]placement.Rule{
		{Methods: []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}, Prefix: "/api/", Class: placement.Home},
	})
	add(placement.Prefixes(placement.Home,
		"/api/auth/", "/api/me", "/api/users", "/api/security/",
		"/api/admin/", "/admin/", "/api/diagnostics/",
		"/api/cluster", "/api/tv/", "/api/system/updates",
		"/api/search", "/api/thumbnails/", "/api/videos/", "/api/libraries/", "/api/channels/",
		"/api/collections/", "/api/series/", "/api/items/", "/api/enrichment/",
		"/api/extensions", "/api/models", "/api/settings/", "/api/reports", "/api/explain",
		"/api/issues", "/api/recommendations", "/api/notifications", "/api/devices",
		"/api/watch/", "/api/activity", "/api/entitlements", "/api/pairing/",
		"/api/stream/", "/graphql", "/ws/",
		// the home's server.toml and its restart (Epic 45 story 45.3)
		"/api/server/",
	))
	return t
}
