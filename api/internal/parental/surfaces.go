package parental

// The surface registry (Epic 57 stories 57.03 and 57.13): what every route of the
// API is to a restricted account. The gate (gate.go) acts on it for each request,
// and the router walk test (api/internal/router/parental_walk_test.go) fails on a
// mounted route no entry names, so a new route cannot reach a restricted account
// before someone decided what it shows.
//
// Only the classes that refuse (Refuse, Closed) may be given by prefix: a new route
// under such a prefix is refused until it gets an entry of its own. A route that
// answers a restricted account is always named exactly.

import (
	"net/http"
	"sort"
	"strings"
)

// Class is what a route is to a restricted account.
type Class int

const (
	// Open routes return no library content and do not depend on the caller's
	// limit or on the role the gate corrects: health, sign-in, the caller's own
	// account, and the routes of a machine and of the cluster, which check an
	// admin or a machine's identity themselves. The gate lets them through
	// before it reads anything, so they answer when the policies cannot be read
	// (the home's database down: DatabaseDown lists the ones that must).
	Open Class = iota + 1
	// Filtered routes return content and apply the filter themselves (SQL on
	// their lists, Check on an id their body carries).
	Filtered
	// ByID routes name one object in their path. The gate checks it before the
	// handler runs and answers a hidden one exactly as a missing one; the handler
	// filters whatever it lists under it.
	ByID
	// Refuse routes are not for a restricted account at all: 403 `restricted`.
	Refuse
	// Closed routes return content the filter does not cover yet: a restricted
	// account gets the answer of a route that does not exist.
	Closed
	// Guarded routes return no library content of their own, but their handler
	// reads the request's limit or trusts the caller's role (the parental routes,
	// the notices, a stream's progress): the gate reads the policy first, puts
	// the limit in the context and, for an account with a policy of its own,
	// takes the admin role off the principal. When the policy cannot be read the
	// handler runs with a limit that shows nothing and with no admin role.
	Guarded
)

func (c Class) String() string {
	switch c {
	case Open:
		return "open"
	case Filtered:
		return "filtered"
	case ByID:
		return "by_id"
	case Refuse:
		return "refuse"
	case Closed:
		return "closed"
	case Guarded:
		return "guarded"
	}
	return "unknown"
}

// Surface is one entry: the route (or every route under Pattern when Prefix), its
// class and why.
type Surface struct {
	// Methods the entry covers (nil: all).
	Methods []string
	// Pattern is the chi route pattern ("/api/videos/{id}"), or its beginning.
	Pattern string
	Prefix  bool
	Class   Class
	// Kind and Param name a ByID route's object: the subject kind and the path
	// parameter holding its id.
	Kind, Param string
	// Owner is the story that will filter a Closed route ("57.10"), when one will.
	Owner string
	// Reason says why the class is right.
	Reason string
	// Probe says how the bypass rehearsal (57.13) asks a read for a fixture: the
	// request path with {video}, {series}, {collection}, {library} or {channel}
	// for the fixture's id. It is a hint only: the rehearsal walks every mounted
	// Filtered and ByID route itself (api/internal/router/p57_bypass_pg_test.go)
	// and fails on one it cannot reach with a real fixture, whatever Probe says.
	Probe string
}

func (s Surface) covers(method string) bool {
	if len(s.Methods) == 0 {
		return true
	}
	for _, m := range s.Methods {
		if m == method {
			return true
		}
	}
	return false
}

// SurfaceTable is a registry.
type SurfaceTable []Surface

// Lookup is the entry for a route: the exact one, else the longest prefix.
func (t SurfaceTable) Lookup(method, pattern string) (Surface, bool) {
	if method == http.MethodHead {
		method = http.MethodGet
	}
	if len(pattern) > 1 {
		pattern = strings.TrimSuffix(pattern, "/")
	}
	best := -1
	for i := range t {
		s := &t[i]
		if !s.covers(method) {
			continue
		}
		if !s.Prefix {
			if s.Pattern == pattern {
				return *s, true
			}
			continue
		}
		if strings.HasPrefix(pattern, s.Pattern) && (best < 0 || len(s.Pattern) > len(t[best].Pattern)) {
			best = i
		}
	}
	if best < 0 {
		return Surface{}, false
	}
	return t[best], true
}

// Owned lists the entries a later story owns, by "METHODS pattern", sorted.
func (t SurfaceTable) Owned(owner string) []string {
	var out []string
	for _, s := range t {
		if s.Owner == owner {
			m := "*"
			if len(s.Methods) > 0 {
				m = strings.Join(s.Methods, ",")
			}
			p := s.Pattern
			if s.Prefix {
				p += "*"
			}
			out = append(out, m+" "+p)
		}
	}
	sort.Strings(out)
	return out
}

// Surfaces is the API's registry.
var Surfaces = buildSurfaces()

var (
	mGet    = []string{http.MethodGet}
	mPost   = []string{http.MethodPost}
	mPut    = []string{http.MethodPut}
	mDelete = []string{http.MethodDelete}
	mWrite  = []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
)

func buildSurfaces() SurfaceTable {
	var t SurfaceTable
	exact := func(c Class, methods []string, reason string, patterns ...string) {
		for _, p := range patterns {
			t = append(t, Surface{Methods: methods, Pattern: p, Class: c, Reason: reason})
		}
	}
	prefix := func(c Class, owner, reason string, patterns ...string) {
		for _, p := range patterns {
			t = append(t, Surface{Pattern: p, Prefix: true, Class: c, Owner: owner, Reason: reason})
		}
	}
	byID := func(methods []string, kind, param, reason string, patterns ...string) {
		for _, p := range patterns {
			probe := ""
			if len(methods) == 1 && methods[0] == http.MethodGet {
				probe = strings.Replace(p, "{"+param+"}", "{"+kind+"}", 1)
			}
			t = append(t, Surface{Methods: methods, Pattern: p, Class: ByID, Kind: kind, Param: param, Reason: reason, Probe: probe})
		}
	}
	filtered := func(methods []string, reason, probe string, patterns ...string) {
		for _, p := range patterns {
			pr := probe
			if pr == "" && len(methods) == 1 && methods[0] == http.MethodGet && !strings.Contains(p, "{") {
				pr = p
			}
			t = append(t, Surface{Methods: methods, Pattern: p, Class: Filtered, Reason: reason, Probe: pr})
		}
	}
	all := []string(nil)

	// ---- open: no content, or the route guards itself ----
	exact(Open, all, "this machine's health and identity; no content",
		"/healthz",
		"/.well-known/security.txt",
		"/api/system/health",
		"/api/system/version",
		"/api/cluster/self",
	)
	exact(Open, all, "the removed cluster sign-in: 404 for everyone",
		"/api/cluster/sso/start",
		"/api/cluster/sso/callback",
		"/api/cluster/sso/authorize",
		"/api/cluster/sso/exchange",
	)
	exact(Open, all, "signing in and out; no content",
		"/api/auth/cloud",
		"/api/auth/login",
		"/api/auth/logout",
		"/api/auth/logout-all",
		"/api/auth/me",
		"/api/auth/recovery-pass",
		"/api/auth/refresh",
		"/api/auth/register",
	)
	exact(Open, all, "pairing a device to the caller's own account (57.02 guards as_user); no content",
		"/api/pairing/device",
		"/api/pairing/device/token",
		"/api/pairing/device/{code}",
		"/api/pairing/exchange",
		"/api/pairing/pending",
		"/api/pairing/request",
		"/api/pairing/status",
	)
	exact(Guarded, all, "approving a device as another user is an admin's or a guardian's (57.02): the handler reads the caller's role",
		"/api/pairing/approve",
	)
	exact(Open, all, "the caller's own account, sign-ins and devices; no content",
		"/api/me",
		"/api/me/change-password",
		"/api/me/cloud-link",
		"/api/me/cloud-link/start",
		"/api/me/devices",
		"/api/me/sessions",
		"/api/me/sessions/{id}",
		"/api/me/activity/settings",
		"/api/devices",
		"/api/devices/register",
		"/api/devices/{id}",
	)
	exact(Open, mGet, "the caller's own tokens, by name; no content", "/api/me/tokens")
	exact(Open, mDelete, "revoking one's own token only tightens", "/api/me/tokens/{id}")
	exact(Refuse, mPost, "a restricted account mints no personal access token (README 6.6)", "/api/me/tokens")
	exact(Guarded, all, "Epic 57's own routes: each checks admin or guardian itself (57.01, 57.02)",
		"/api/me/parental",
		"/api/parental/children",
		"/api/parental/ratings",
		"/api/users/{id}/parental",
		"/api/users/{id}/parental/guardians",
		"/api/users/{id}/parental/guardians/{gid}",
		"/api/users/{id}/parental/rules",
		"/api/users/{id}/parental/rules/{rid}",
	)
	exact(Guarded, all, "the device's age signal only ever tightens the caller's own limit (57.04)",
		"/api/me/age-signal",
	)
	exact(Guarded, mPost, "Ask a parent: the child asks; the handler lets only a restricted account through and names no content the asker was not already refused (57.06)",
		"/api/me/parental/requests",
	)
	exact(Guarded, mGet, "what became of the caller's own requests: the handler lets only a restricted account through and reads only its rows, by the ids it sent itself, with no title and no guardian (57.06)",
		"/api/me/parental/requests",
	)
	exact(Guarded, all, "Ask a parent and the PIN: the child asks, the guardian answers; each checks its caller itself and names no content the asker was not already refused (57.06)",
		"/api/me/parental/guardians",
		"/api/me/parental/unlock",
		"/api/me/parental-pin",
		"/api/parental/requests",
		"/api/parental/requests/{id}/approve",
		"/api/parental/requests/{id}/deny",
	)
	exact(Guarded, all, "the time rules: the account's own countdown, and routes that check admin or guardian themselves; no content (57.14)",
		"/api/me/parental/time",
		"/api/parental/users/{id}/time-rules",
		"/api/parental/users/{id}/time-usage",
		"/api/parental/users/{id}/time-grants",
	)
	exact(Refuse, all, "the review of detected tiers names videos by title and is for an admin or a guardian; the handler refuses a restricted account too (57.17)",
		"/api/parental/review",
		"/api/parental/review/{video_id}",
	)
	exact(Refuse, all, "turning parental controls on or off in the home, the notice that a child's device signed in, and the tiers Rate libraries starts with are an admin's; each handler refuses every other account too (57.12)",
		"/api/parental/settings",
		"/api/parental/settings/notice/dismiss",
		"/api/parental/library-suggestions",
	)
	exact(Open, all, "what the home can do and which strings it has; no content",
		"/api/capabilities",
		"/api/entitlements",
		"/api/language-packs",
		"/api/discovery/home",
		"/api/stream/capabilities",
	)
	exact(Guarded, all, "the caller's own notices; none names a video",
		"/api/notifications",
		"/api/notifications/read-all",
		"/api/notifications/{id}/read",
	)
	exact(Open, mPost, "a client's own diagnostics, written not read", "/api/diagnostics/envelope")
	exact(Guarded, all, "the caller's own search words; no result is in them",
		"/api/search/recent/{sid}",
		"/api/search/saved",
		"/api/search/saved/{id}",
		"/api/search/save",
	)
	exact(Guarded, mDelete, "clearing the caller's own recent searches", "/api/search/recent")
	exact(Open, all, "GraphQL answers through sub-requests to the REST routes, each of which passes this gate",
		"/graphql",
		"/graphql/schema",
	)
	exact(Guarded, all, "the caller's own stream session, by its unguessable id: progress, errors, close",
		"/api/stream/sessions/{id}/errors",
		"/api/stream/sessions/{id}/progress",
	)
	exact(Guarded, mDelete, "closing one's own stream session", "/api/stream/sessions/{id}")
	exact(Guarded, mPost, "ending or beating a watch the filter let start; a hidden video's beat is refused in the handler",
		"/api/watch/stop",
	)
	exact(Guarded, all, "the caller's own recommendation rows: refresh and dismiss a kind; no content returned",
		"/api/recommendations/refresh",
		"/api/recommendations/rows/{reason_kind}",
	)

	// ---- videos ----
	filtered(mGet, "the list and its counts carry the filter clause", "",
		"/api/videos",
		"/api/videos/facets",
		"/api/me/videos",
	)
	byID(mGet, SubjectVideo, "id", "one video's description, images and text",
		"/api/videos/{id}",
		"/api/videos/{id}/chapters",
		"/api/videos/{id}/context",
		"/api/videos/{id}/enrichment",
		"/api/videos/{id}/poster",
		"/api/videos/{id}/segments",
		"/api/videos/{id}/stats",
		"/api/videos/{id}/subtitles",
		"/api/videos/{id}/transcript/edits",
		"/api/videos/{id}/trickplay",
		"/api/videos/{id}/trickplay/sprite",
		"/api/me/videos/{id}",
	)
	byID(mWrite, SubjectVideo, "id", "a change to one video: a hidden one is missing before the role check answers",
		"/api/videos/{id}",
		"/api/videos/{id}/age-tier",
		"/api/videos/{id}/cancel",
		"/api/videos/{id}/enrichment/accept",
		"/api/videos/{id}/enrichment/dismiss",
		"/api/videos/{id}/enrichment/reenrich",
		"/api/videos/{id}/enrichment/revert",
		"/api/videos/{id}/enrichment/search",
		"/api/videos/{id}/pause",
		"/api/videos/{id}/process",
		"/api/videos/{id}/reprocess",
		"/api/videos/{id}/resume",
		"/api/videos/{id}/tags",
		"/api/videos/{id}/transcript/words",
	)
	exact(Refuse, mPost, "a restricted account imports nothing from YouTube (README 6.6)", "/api/videos/{id}/import-youtube")
	byID(all, SubjectVideo, "video_id", "a device's download flag for one video",
		"/api/videos/{video_id}/downloaded",
	)
	byID(all, SubjectVideo, "video_id", "the caller's own history of one video",
		"/api/me/history/{video_id}",
	)
	byID(mDelete, SubjectVideo, "video_id", "dismissing one recommended video",
		"/api/recommendations/items/{video_id}",
	)
	byID(mGet, SubjectVideo, "video_id", "one video's playback events",
		"/ws/playback/{video_id}",
	)

	// ---- streaming and watching ----
	filtered(mPost, "the video is checked before a session opens (handlers/streaming OpenSession)", "",
		"/api/stream/sessions",
	)
	filtered(mPost, "a cast grant's video is checked, a queue must be the caller's own, and the grant carries the caller's limit (handlers/streaming CastGrants, Epic 58 story 58.07)", "",
		"/api/cast/grants",
	)
	filtered(mGet, "a session's URLs are minted again only while its video is still allowed", "",
		"/api/stream/sessions/{id}",
	)
	filtered(mPost, "the video a watch names is checked (handlers/watch)", "",
		"/api/watch/start",
		"/api/watch/heartbeat",
	)

	// ---- search ----
	exact(Refuse, all, "a restricted account does not search YouTube (README 6.6)",
		"/api/search/youtube",
		"/api/search/youtube/status",
	)

	// ---- exports: the whole lineup in one file ----
	exact(Refuse, all, "the XMLTV and M3U exports are not for a restricted account (README 6.6)",
		"/api/channels/xmltv",
		"/api/channels/playlist.m3u",
	)

	// ---- live TV (story 57.10): the handlers filter with parental.LiveTV ----
	filtered(mGet, "the channel list hides a channel above the limit or under a block rule", "",
		"/api/channels",
	)
	filtered(mGet, "the guide and what is on now hide such a channel and mask a programme above the limit to its time", "",
		"/api/channels/guide",
		"/api/channels/now",
	)
	for _, tail := range []string{"", "/guide", "/schedule"} {
		filtered(mGet, "one channel, its guide and its schedule: a hidden channel answers as a missing one, a programme above the limit is masked",
			"/api/channels/{channel}"+tail, "/api/channels/{id}"+tail)
	}
	filtered(mGet, "whether the caller may watch the channel now (404 for a hidden channel, 403 `restricted` for the programme)",
		"/api/channels/{channel}/access", "/api/channels/{id}/access")
	filtered(mPost, "the channel's signed URL: refused as /access refuses; the streaming service asks again on every request", "",
		"/api/channels/{id}/play",
	)
	filtered(all, "remote viewing asks the same check for a tune and at every heartbeat; ending a session shows nothing", "",
		"/api/tv/remote/tune",
		"/api/tv/remote/sessions/{id}/heartbeat",
		"/api/tv/remote/sessions/{id}",
	)
	exact(Open, mGet, "a channel's logo and art are images with no programme in them; players and tuners load them with no session",
		"/api/channels/{id}/logo",
		"/api/channels/{id}/art/{kind}",
	)
	exact(Open, mGet, "where the TV streams from (addresses and switches, no channel or programme)",
		"/api/tv/info",
	)
	exact(Open, all, "the streaming service's question, answered to the local token only; every user gets 403",
		"/api/tv/parental/verdict",
	)
	exact(Open, all, "the stream provider's webhook, checked by its own signature; no content",
		"/api/tv/remote/webhook",
	)
	// every other live TV route is the admin's (building channels, the tuner, releases,
	// simulcast, the cluster): closed, and a new one stays closed until it is named
	prefix(Closed, "", "the TV's admin routes and any live TV route not named above",
		"/api/channels",
		"/api/tv/",
	)

	// ---- a machine's and the cluster's own routes: they answer with the home's database down ----
	for _, d := range DatabaseDown {
		if s, ok := t.Lookup(http.MethodGet, d.Pattern); ok && !s.Prefix {
			continue // named above already (health, signing in)
		}
		exact(Open, all, d.Reason, d.Pattern)
	}

	// ---- the operator's surfaces: nothing here is for a restricted account ----
	prefix(Refuse, "", "admin and operator routes; a restricted account is never an admin (57.02)",
		"/admin/",
		"/api/admin/",
		"/api/cluster",
		"/api/components",
		"/api/extensions",
		"/api/filler/",
		"/api/jobs",
		"/api/models",
		"/api/pools",
		"/api/queue/",
		"/api/queues",
		"/api/review/",
		"/api/rules",
		"/api/server/",
		"/api/settings",
		"/api/speakers",
		"/api/vocabulary",
		"/api/workers",
		"/api/users",
		"/api/security/",
		"/api/system/",
		"/api/transcription/",
		"/api/enrichment/",
		"/api/issues",
		"/api/explain",
		"/api/activity",
		"/api/diagnostics/",
		"/api/seasons/",
		"/api/items/",
		"/api/reports",
		"/api/tags",
		"/internal/",
		"/ws/jobs",
		"/ws/queues",
	)
	prefix(Refuse, "", "plugin admin is not for a restricted account (README 6.6)",
		"/api/plugins",
	)
	prefix(Refuse, "", "managing libraries: scans, sources, access, merges",
		"/api/libraries",
	)

	// ---- tags and a library's numbers ----
	filtered(mGet, "the tags carried by at least one video the account may see (handlers/tags)", "",
		"/api/tags",
	)
	byID(mGet, SubjectLibrary, "id", "a library's numbers, counted over the videos the account may see; no jobs, no sweep (handlers/libraries StatsCached)",
		"/api/libraries/{id}/stats",
	)

	// ---- libraries as a viewer reads them ----
	filtered(mGet, "the caller's libraries: the gate takes a blocked library out of the account's access, and counts carry the filter clause", "",
		"/api/libraries",
		"/api/libraries/summary",
		"/api/me/pinned-libraries",
	)
	filtered(mPut, "pins are kept to libraries the narrowed account may read", "", "/api/me/pinned-libraries")
	byID(mGet, SubjectLibrary, "id", "one library's page: its rows, counts and posters carry the filter clause (handlers/browse)",
		"/api/libraries/{id}",
		"/api/libraries/{id}/hub",
		"/api/libraries/{id}/categories",
		"/api/libraries/{id}/collections",
		"/api/libraries/{id}/collections/{kind}/{gid}",
		"/api/libraries/{id}/series",
	)

	// ---- home, rows and the caller's own lists ----
	filtered(mGet, "every row, count and poster reads videos through the filter clause", "",
		"/api/home",
		"/api/home/featured",
		"/api/recommendations",
		"/api/me/activity",
		"/api/me/history",
		"/api/me/playlists",
		"/api/me/playlists/{id}",
		"/api/me/playlists/{id}/items",
		"/api/me/play-queues/current",
		"/api/me/play-queues/{id}",
	)
	exact(Refuse, mPut, "choosing the home's featured video is an admin's", "/api/home/featured")
	filtered(mWrite, "the videos a mark, a playlist or a queue names are checked (checkVisible), and its source is read through filtered lists", "",
		"/api/me/favorites",
		"/api/me/watched",
		"/api/me/playlists",
		"/api/me/playlists/{id}",
		"/api/me/playlists/{id}/items",
		"/api/me/playlists/{id}/items/{video_id}",
		"/api/me/playlists/{id}/order",
		"/api/me/play-queues",
		"/api/me/play-queues/{id}",
		"/api/me/play-queues/{id}/items",
		"/api/me/play-queues/{id}/reshuffle",
		"/api/me/play-queues/{id}/current",
	)

	// ---- collections and series ----
	filtered(mGet, "blocked and empty-for-this-account entries are left out; counts and posters carry the filter clause", "",
		"/api/collections",
		"/api/series",
	)
	byID(mGet, SubjectCollection, "id", "one collection and its items",
		"/api/collections/{id}",
		"/api/collections/{id}/items",
	)
	byID(mGet, SubjectSeries, "id", "one series and its episodes",
		"/api/series/{id}",
		"/api/series/{id}/episodes",
	)
	// Curating is an admin's (each handler refuses every other account, and a
	// restricted account is never an admin, 57.02). The gate still checks the
	// object first, so a hidden collection or series answers as a missing one
	// before the role check says anything about it.
	exact(Guarded, mPost, "making a collection is an admin's: the handler refuses every other account before it reads anything",
		"/api/collections",
	)
	byID(mWrite, SubjectCollection, "id", "a change to one collection: a hidden one is missing before the role check answers",
		"/api/collections/{id}",
		"/api/collections/{id}/items",
		"/api/collections/{id}/items/{video_id}",
		"/api/collections/{id}/convert",
		"/api/collections/{id}/duplicate",
		"/api/collections/{id}/age-tier",
	)
	byID(mWrite, SubjectSeries, "id", "a change to one series: a hidden one is missing before the role check answers",
		"/api/series/{id}",
		"/api/series/{id}/enrichment/accept-all",
		"/api/series/{id}/age-tier",
		"/api/series/{id}/style",
	)
	byID(mGet, SubjectSeries, "id", "the gaps in a series' episode numbers, counted from the episodes the account may see: a hidden episode is a gap like a missing one (season and episode numbers only, no titles)",
		"/api/series/{id}/missing",
	)

	// ---- images by content hash ----
	filtered(mGet, "the thumbnail's video must pass the filter (handlers/reports thumbVisible)", "", "/api/thumbnails/{hash}/small.jpg")

	// ---- search (handlers/search/parental.go) ----
	filtered(mPost, "the title and transcript queries carry the filter clause and the meaning search's passages are kept for allowed videos only, so ranks, totals and snippets are of what the account may see; its YouTube block is never asked", "",
		"/api/search",
	)
	filtered(mGet, "a kept search is filtered again, under the limit as it is now, on every read: pages, totals and one video's passages (a hidden video answers as one not in the search)", "",
		"/api/search/results/{sid}",
		"/api/search/passages",
	)
	filtered(mGet, "the caller's recent searches, each counted again over what it may see now", "",
		"/api/search/recent",
	)
	filtered(mGet, "typeahead: only the caller's own earlier search words (search_history) are read, never a title or a result, so there is nothing of a hidden video to leave out; refused like the rest of search when the policy cannot be read", "",
		"/api/search/suggest",
	)

	// ---- live feeds (parental/live.go): each connection has its own filter ----
	filtered(mGet, "the home's live feed: every event is asked of the connection's filter, with the limit as it is at that moment", "",
		"/ws/home",
	)
	byID(mGet, SubjectLibrary, "id", "a library's live feed: the library is checked at the start and again for every event, and an event is sent only when every item it names is allowed",
		"/ws/library/{id}",
	)

	// ---- plugin panels (plugins/panels.go) ----
	filtered(mGet, "a plugin's panel on a video, series, season or library: the subject must be allowed and the plugin allowed by a rule for this account (a panel may name other titles)", "",
		"/api/panels",
		"/api/panels/image",
	)
	return t
}

// DownRoute is one route that must answer when the policies cannot be read.
type DownRoute struct {
	Pattern string
	// Reason says why it must answer then, and what checks its caller instead of
	// the gate.
	Reason string
}

// DatabaseDown are the routes that must answer when the home's database cannot be
// read: a machine's own routes, the cluster's (a switch of the home, an emergency,
// a machine joining or reporting), updates and restores, and signing in. None
// returns library content, and each checks an admin or a machine's identity
// itself, so each is Open: the gate lets it through before it reads a policy. A
// route here that is not Open fails TestDatabaseDownRoutesAreOpen, and a
// machine-local route (router.Placement) or one that needs no sign-in
// (middleware.DefaultPublicAllowlist) that is missing here fails its sibling.
var DatabaseDown = []DownRoute{
	{"/healthz", "this machine's health; no content, no caller to check"},
	{"/.well-known/security.txt", "a static file"},
	{"/api/.well-known/jwks.json", "the public keys access tokens are checked with; no content"},
	{"/api/system/health", "this machine's health; no content"},
	{"/api/system/version", "this machine's version; no content"},
	{"/api/system/processes", "this machine's processes, for an admin while the home is away: the route requires an admin itself (RequireAdmin)"},
	{"/api/system/sbom", "this machine's bill of materials: the handler refuses every caller but an admin"},
	{"/api/cluster/self", "this machine's identity, its home and its address; no user and no library"},
	{"/api/cluster/emergency", "a standby home taking over when its home, and so the database, is gone: the handler refuses every caller but an admin of this machine"},
	{"/api/cluster/term-claim", "another standby home's claim on an emergency's term: sealed from its key, and this machine's supervisor refuses any other"},
	{"/api/cluster/moved/{node}", "a switch's notice of the new home: sealed to the one machine that may open it"},
	{"/api/cluster/kept-aside/report", "an old home's report after an emergency: sealed by its key, the handler refuses any other"},
	{"/api/cluster/switchover", "the state and the start of a switch of the home, read while the database is fenced and moved: every handler refuses every caller but an admin"},
	{"/api/cluster/switchover/standby", "as /api/cluster/switchover"},
	{"/api/cluster/switchover/{id}/cancel", "as /api/cluster/switchover"},
	{"/api/cluster/switchover/{id}/checklist", "as /api/cluster/switchover"},
	{"/api/cluster/join", "a machine's request to join, before it holds any credential: rate-limited, and an admin approves it"},
	{"/api/cluster/join/{node_id}", "the joining machine's poll, answered to its public key only"},
	{"/api/cluster/agent", "every process's channel to the home: it authenticates in its protocol (pairing key or the home's agent key)"},
	{"/api/cluster/link", "the cluster link: mutual TLS and tickets"},
	{"/api/cluster/link/stream", "as /api/cluster/link"},
	{"/api/cluster/link/pull", "the front door's pulls over loopback: the local token"},
	{"/api/cluster/link/local/agent", "this machine's own processes on its cluster link, over loopback: the local token"},
	{"/api/cluster/link/local/fetch", "as /api/cluster/link/local/agent"},
	{"/api/cluster/ca.crt", "the cluster CA's public certificate"},
	{"/api/cluster/delegate/introspect", "a node's question to the home about a sign-in: the handler refuses any request without a verified node signature"},
	{"/api/cluster/delegate/signout", "as /api/cluster/delegate/introspect"},
	{"/api/cluster/sso/start", "the removed cluster sign-in: 404 for everyone"},
	{"/api/cluster/sso/callback", "as /api/cluster/sso/start"},
	{"/api/cluster/sso/authorize", "as /api/cluster/sso/start"},
	{"/api/cluster/sso/exchange", "as /api/cluster/sso/start"},
	{"/internal/updates/tv-change", "this machine's update moving its TV to the new streaming binary: its own supervisor's token only"},
	{"/internal/updates/tv-change/{run}", "as /internal/updates/tv-change"},
	{"/internal/updates/tv-change/{run}/withdraw", "as /internal/updates/tv-change"},
	{"/internal/updates/restored", "a restore the command line asked for, recorded while the database is being put back: the per-boot local token only"},
	{"/internal/plugins/invocations", "this machine's own pipeline minting a plugin job's token: the per-boot local token only"},
	{"/internal/plugins/invocations/{id}", "as /internal/plugins/invocations"},
	{"/internal/plugins/breaker", "as /internal/plugins/invocations"},
	{"/internal/plugins/outcome", "as /internal/plugins/invocations"},
	{"/api/admin/system/update", "starting an update of this machine: the handler refuses every caller but an admin"},
	{"/api/server/restart", "restarting this machine's services, which an admin does when the database is down: the handler refuses every caller but an admin"},
	{"/api/server/backups", "the backups a restore reads: every handler refuses every caller but an admin"},
	{"/api/server/backups/dumps/{name}", "as /api/server/backups"},
	{"/api/auth/login", "signing in; no content"},
	{"/api/auth/refresh", "renewing a sign-in; no content"},
	{"/api/auth/logout", "signing out; no content"},
	{"/api/auth/cloud", "signing in with a Maktaba Cloud assertion; no content"},
	{"/api/auth/register", "registering, before any credential; no content"},
	{"/api/pairing/device", "a TV asking for a sign-in code, before any credential; no content"},
	{"/api/pairing/device/token", "the TV's poll for its approval; no content"},
	{"/api/discovery/home", "the home's identity for the apps' home picker; no user and no library"},
	{"/api/stream/capabilities", "what this machine's streaming can do; no content"},
	{"/api/tv/parental/verdict", "the streaming service's question, answered to the local token only"},
	{"/api/tv/remote/webhook", "the stream provider's webhook, checked by its own signature"},
}
