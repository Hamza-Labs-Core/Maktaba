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
	// Open routes return no content (sign-in, the caller's own account, health),
	// or guard themselves (the parental routes): the gate lets them through.
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
	// Probe says how the bypass rehearsal (57.13) asks the route for a fixture:
	// the request path with {video}, {series}, {collection}, {library} or
	// {channel} for the fixture's id. Empty: the route takes no fixture.
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
	mPatch  = []string{http.MethodPatch}
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
	closed := func(methods []string, owner, reason string, patterns ...string) {
		for _, p := range patterns {
			t = append(t, Surface{Methods: methods, Pattern: p, Class: Closed, Owner: owner, Reason: reason})
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
		"/api/pairing/approve",
		"/api/pairing/device",
		"/api/pairing/device/token",
		"/api/pairing/device/{code}",
		"/api/pairing/exchange",
		"/api/pairing/pending",
		"/api/pairing/request",
		"/api/pairing/status",
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
	exact(Open, all, "Epic 57's own routes: each checks admin or guardian itself (57.01, 57.02)",
		"/api/me/parental",
		"/api/parental/children",
		"/api/parental/ratings",
		"/api/users/{id}/parental",
		"/api/users/{id}/parental/guardians",
		"/api/users/{id}/parental/guardians/{gid}",
		"/api/users/{id}/parental/rules",
		"/api/users/{id}/parental/rules/{rid}",
	)
	exact(Open, all, "the device's age signal only ever tightens the caller's own limit (57.04)",
		"/api/me/age-signal",
	)
	exact(Open, all, "Ask a parent and the PIN: the child asks, the guardian answers; each checks its caller itself and names no content the asker was not already refused (57.06)",
		"/api/me/parental/requests",
		"/api/me/parental/guardians",
		"/api/me/parental/unlock",
		"/api/me/parental-pin",
		"/api/parental/requests",
		"/api/parental/requests/{id}/approve",
		"/api/parental/requests/{id}/deny",
	)
	exact(Open, all, "the time rules: the account's own countdown, and routes that check admin or guardian themselves; no content (57.14)",
		"/api/me/parental/time",
		"/api/parental/users/{id}/time-rules",
		"/api/parental/users/{id}/time-usage",
		"/api/parental/users/{id}/time-grants",
	)
	exact(Refuse, all, "the review of detected tiers names videos by title and is for an admin or a guardian; the handler refuses a restricted account too (57.17)",
		"/api/parental/review",
		"/api/parental/review/{video_id}",
	)
	exact(Open, all, "what the home can do and which strings it has; no content",
		"/api/capabilities",
		"/api/entitlements",
		"/api/language-packs",
		"/api/discovery/home",
		"/api/stream/capabilities",
	)
	exact(Open, all, "the caller's own notices; none names a video",
		"/api/notifications",
		"/api/notifications/read-all",
		"/api/notifications/{id}/read",
	)
	exact(Open, mPost, "a client's own diagnostics, written not read", "/api/diagnostics/envelope")
	exact(Open, all, "the caller's own search words; no result is in them",
		"/api/search/recent",
		"/api/search/recent/{sid}",
		"/api/search/saved",
		"/api/search/saved/{id}",
		"/api/search/save",
	)
	exact(Open, all, "GraphQL answers through sub-requests to the REST routes, each of which passes this gate",
		"/graphql",
		"/graphql/schema",
	)
	exact(Open, all, "the caller's own stream session, by its unguessable id: progress, errors, close",
		"/api/stream/sessions/{id}/errors",
		"/api/stream/sessions/{id}/progress",
	)
	exact(Open, mDelete, "closing one's own stream session", "/api/stream/sessions/{id}")
	exact(Open, mPost, "ending or beating a watch the filter let start; a hidden video's beat is refused in the handler",
		"/api/watch/stop",
	)
	exact(Open, all, "the caller's own recommendation rows: refresh and dismiss a kind; no content returned",
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

	// ---- live TV: story 57.10 filters these ----
	prefix(Closed, "57.10", "channels, their guide, schedule, art and live streams",
		"/api/channels",
	)
	prefix(Closed, "57.10", "what is on air, the TV surface's info, remote TV and the TV's admin",
		"/api/tv/",
	)

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
	closed(mWrite, "", "curating collections and series is not a restricted account's",
		"/api/collections",
		"/api/collections/{id}",
		"/api/collections/{id}/items",
		"/api/collections/{id}/items/{video_id}",
		"/api/collections/{id}/convert",
		"/api/collections/{id}/duplicate",
		"/api/collections/{id}/age-tier",
		"/api/series/{id}",
		"/api/series/{id}/enrichment/accept-all",
		"/api/series/{id}/age-tier",
		"/api/series/{id}/style",
	)
	closed(mGet, "", "a series' missing episodes name titles the home does not hold: not filtered yet", "/api/series/{id}/missing")

	// ---- images by content hash ----
	filtered(mGet, "the thumbnail's video must pass the filter (handlers/reports thumbVisible)", "", "/api/thumbnails/{hash}/small.jpg")

	// ---- not filtered yet: closed to a restricted account ----
	closed(all, "", "search ranks, caches and suggests over every video; its queries do not carry the filter clause yet",
		"/api/search",
		"/api/search/passages",
		"/api/search/results/{sid}",
		"/api/search/suggest",
	)
	closed(mGet, "", "the live event streams of the home and of a library name videos without asking the filter yet",
		"/ws/home",
		"/ws/library/{id}",
	)
	return t
}
