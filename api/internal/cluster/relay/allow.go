// Package relay is the home's service door (story 31.R, design
// specs/epics/31-multi-instance/story-31-relay-service-door.md): a cluster
// node sends an extension's HTTP request over its sealed agent channel,
// and the home checks it against that extension's allow-list, adds the
// secret the node never sees (the Plex token, kept in home_secrets),
// forwards it to the configured server and streams the answer back with
// the secret scrubbed out of it.
//
// Nothing here forwards an arbitrary URL: the upstream address is the
// extension's own setting on the home, and a request is a method plus a
// path and query that must match one of the extension's rules exactly.
package relay

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// MaxTarget bounds a request's path and query.
const MaxTarget = 16 << 10

// Rule is one allowed call: a method, a path pattern (anchored, matched
// against the raw path) and the query parameters it may carry.
type Rule struct {
	Method string
	// Path is shown on the Machines page; Re matches it.
	Path string
	Re   *regexp.Regexp
	// Query: parameter name -> pattern its (decoded) value must match.
	// A parameter not listed is refused; each may appear once.
	Query map[string]*regexp.Regexp
	// Required: parameters that must be present (with Query's pattern).
	Required []string
	// QueryKeys: families of parameters whose names follow a pattern (Plex's
	// channelMapping[<n>]), each name once, its value matching Value.
	QueryKeys []KeyRule
	// HomeOnly: only a caller on the home itself may make this call (a first-party
	// plugin's stage there, Epic 54 story 54.35); a node never may.
	HomeOnly bool
	// Use names the client call that needs it (documentation).
	Use string
	// Verify, when set, is checked against the upstream before the call
	// is forwarded (DELETE only removes collections).
	Verify string
	// Op, when set, names an operation the door serves itself instead of forwarding
	// the call (dizqueTV's, issue #827: a projection read or a programme the door builds).
	Op string
}

// KeyRule is a family of query parameters: a name pattern and its value's.
type KeyRule struct {
	Name  *regexp.Regexp
	Value *regexp.Regexp
}

// VerifyCollection: the rating key in the path must name a collection.
const VerifyCollection = "collection"

// Extension is a relayable extension: where its secret is, how it is
// added, and its allow-list.
type Extension struct {
	Name string
	// SecretName is the home_secrets name of the secret.
	SecretName string
	// Header carries the secret on the upstream request.
	Header string
	// URLKey is the extensions.settings key of the upstream address;
	// URLEnv the environment variable that bootstraps it.
	URLKey string
	URLEnv string
	// QueryParam is the query parameter name that would carry the secret
	// (refused in requests, scrubbed from answers).
	QueryParam string
	// MaxBody bounds a request's body (0: MaxRequestBody).
	MaxBody int
	Rules   []Rule
}

// HomeOnly lists the extensions whose calls are never relayed, with why.
var HomeOnly = map[string]string{
	// Its API token can create and delete every video of the account and
	// the calls are admin operations made by the home's API itself
	// (api/internal/tvremote): no stage needs them on a node.
	"cloudflare_stream": "the Cloudflare API token is used by the home's API only (admin operations)",
	// dizqueTV holds no secret of its own, but its programmes carry the Plex token: the
	// home's own plugin reaches it through the door's operations (DizqueTV), which build
	// the programmes on the home; no node is ever relayed to it.
	"dizquetv": "dizqueTV stages run on the home (programmes carry the Plex token)",
}

var (
	digits   = regexp.MustCompile(`^[0-9]{1,12}$`)
	digitsOK = regexp.MustCompile(`^[0-9]{0,12}$`)
	flag01   = regexp.MustCompile(`^[01]$`)
	// a collection member list: server://<machine>/com.plexapp.plugins.library/library/metadata/<keys>
	memberURI = regexp.MustCompile(`^server://[A-Za-z0-9-]{1,64}/com\.plexapp\.plugins\.library/library/metadata/[0-9]{1,12}(,[0-9]{1,12})*$`)
	anyTitle  = regexp.MustCompile(`^[^\x00-\x1f]{1,512}$`)
	labelTag  = regexp.MustCompile(`^[^\x00-\x1f]{1,128}$`)
	only1     = regexp.MustCompile(`^1$`)
	only0     = regexp.MustCompile(`^0$`)
	only18    = regexp.MustCompile(`^18$`)
	only2     = regexp.MustCompile(`^2$`)
	itemType  = regexp.MustCompile(`^(1|4)$`)
	// Live TV: Maktaba's own HDHomeRun tuner and XMLTV guide
	tunerURL      = regexp.MustCompile(`^https?://[A-Za-z0-9.\-\[\]:]{1,255}(/[^\x00-\x20]{0,512})?$`)
	language      = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})?$`)
	hdhrDevice    = regexp.MustCompile(`^device://tv\.plex\.grabbers\.hdhomerun/[A-Za-z0-9-]{1,64}$`)
	xmltvLineup   = regexp.MustCompile(`^lineup://tv\.plex\.providers\.epg\.xmltv/[^\x00-\x1f]+$`)
	channelList   = regexp.MustCompile(`^[0-9]{1,6}(,[0-9]{1,6})*$`)
	channelMapKey = regexp.MustCompile(`^channelMapping(ByKey)?\[[0-9]{1,6}\]$`)
	channelNumber = regexp.MustCompile(`^[0-9]{1,6}$`)
)

func q(pairs ...any) map[string]*regexp.Regexp {
	m := map[string]*regexp.Regexp{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i].(string)] = pairs[i+1].(*regexp.Regexp)
	}
	return m
}

// Plex is the Plex extension: every call the node-placed stages
// (plex_index, plex_collection, plex_groups) and the reachability probe
// make through maktaba_pipeline.integrations.plex.PlexClient. Live TV
// (/livetv, /media/grabbers) is absent on purpose: plex_livetv stays on the
// home beside write_channels, so no node needs it.
var Plex = Extension{
	Name:       "plex",
	SecretName: "ext.plex.token",
	Header:     "X-Plex-Token",
	URLKey:     "url",
	URLEnv:     "MAKTABA_PLEX_URL",
	QueryParam: "X-Plex-Token",
	Rules: []Rule{
		{Method: "GET", Path: "/identity", Re: regexp.MustCompile(`^/identity$`),
			Use: "ping, machine_identifier"},
		{Method: "GET", Path: "/library/sections", Re: regexp.MustCompile(`^/library/sections$`),
			Use: "sections"},
		{Method: "GET", Path: "/library/sections/{key}/all", Re: regexp.MustCompile(`^/library/sections/[0-9]{1,12}/all$`),
			Query: q("type", itemType), Use: "section_listing"},
		{Method: "GET", Path: "/library/sections/{key}/collections", Re: regexp.MustCompile(`^/library/sections/[0-9]{1,12}/collections$`),
			Use: "collections"},
		{Method: "GET", Path: "/library/metadata/{keys}", Re: regexp.MustCompile(`^/library/metadata/[0-9]{1,12}(,[0-9]{1,12}){0,49}$`),
			Use: "metadata, item_section, collection_labels"},
		{Method: "GET", Path: "/library/collections/{key}/children", Re: regexp.MustCompile(`^/library/collections/[0-9]{1,12}/children$`),
			Use: "collection_children"},
		{Method: "POST", Path: "/library/collections", Re: regexp.MustCompile(`^/library/collections$`),
			Query:    q("type", only1, "title", anyTitle, "smart", only0, "sectionId", digits, "uri", memberURI),
			Required: []string{"type", "title", "smart", "sectionId", "uri"}, Use: "create_collection"},
		{Method: "PUT", Path: "/library/collections/{key}/items", Re: regexp.MustCompile(`^/library/collections/[0-9]{1,12}/items$`),
			Query: q("uri", memberURI), Required: []string{"uri"}, Use: "create_collection (add members)"},
		{Method: "PUT", Path: "/library/collections/{key}/items/{item}/move", Re: regexp.MustCompile(`^/library/collections/[0-9]{1,12}/items/[0-9]{1,12}/move$`),
			Query: q("after", digitsOK), Use: "create_collection (order)"},
		{Method: "PUT", Path: "/library/metadata/{key}/prefs", Re: regexp.MustCompile(`^/library/metadata/[0-9]{1,12}/prefs$`),
			Query: q("collectionSort", only2), Required: []string{"collectionSort"}, Use: "create_collection (custom order)"},
		{Method: "PUT", Path: "/library/sections/{key}/all (labels of a collection)", Re: regexp.MustCompile(`^/library/sections/[0-9]{1,12}/all$`),
			Query:    q("type", only18, "id", digits, "label[0].tag.tag", labelTag, "label.locked", flag01),
			Required: []string{"type", "id", "label[0].tag.tag"}, Use: "add_label"},
		{Method: "DELETE", Path: "/library/metadata/{key} (collections only)", Re: regexp.MustCompile(`^/library/metadata/[0-9]{1,12}$`),
			Verify: VerifyCollection, Use: "delete_metadata (a collection)"},
		// Live TV (story 54.35): what plex_livetv calls, from its plugin on the home only
		// (the stage is home-placed; no node makes these calls)
		{Method: "GET", Path: "/livetv/dvrs", Re: regexp.MustCompile(`^/livetv/dvrs$`),
			HomeOnly: true, Use: "dvrs"},
		{Method: "GET", Path: "/media/grabbers/devices", Re: regexp.MustCompile(`^/media/grabbers/devices$`),
			HomeOnly: true, Use: "grabber_devices"},
		{Method: "POST", Path: "/media/grabbers/devices", Re: regexp.MustCompile(`^/media/grabbers/devices$`),
			Query: q("uri", tunerURL), Required: []string{"uri"}, HomeOnly: true, Use: "add_grabber_device"},
		{Method: "POST", Path: "/livetv/dvrs", Re: regexp.MustCompile(`^/livetv/dvrs$`),
			Query:    q("language", language, "device", hdhrDevice, "lineup", xmltvLineup, "lineupTitle", anyTitle),
			Required: []string{"language", "device", "lineup", "lineupTitle"}, HomeOnly: true, Use: "create_dvr"},
		{Method: "PUT", Path: "/livetv/dvrs/{key}/lineups", Re: regexp.MustCompile(`^/livetv/dvrs/[0-9]{1,12}/lineups$`),
			Query: q("lineup", xmltvLineup), Required: []string{"lineup"}, HomeOnly: true, Use: "add_dvr_lineup"},
		{Method: "DELETE", Path: "/livetv/dvrs/{key}/lineups", Re: regexp.MustCompile(`^/livetv/dvrs/[0-9]{1,12}/lineups$`),
			Query: q("lineup", xmltvLineup), Required: []string{"lineup"}, HomeOnly: true, Use: "remove_dvr_lineup"},
		{Method: "PUT", Path: "/livetv/dvrs/{key}/devices/{device}", Re: regexp.MustCompile(`^/livetv/dvrs/[0-9]{1,12}/devices/[0-9]{1,12}$`),
			HomeOnly: true, Use: "add_dvr_device"},
		{Method: "DELETE", Path: "/livetv/dvrs/{key}/devices/{device}", Re: regexp.MustCompile(`^/livetv/dvrs/[0-9]{1,12}/devices/[0-9]{1,12}$`),
			HomeOnly: true, Use: "remove_dvr_device"},
		{Method: "DELETE", Path: "/livetv/dvrs/{key}", Re: regexp.MustCompile(`^/livetv/dvrs/[0-9]{1,12}$`),
			HomeOnly: true, Use: "delete_dvr"},
		{Method: "POST", Path: "/livetv/dvrs/{key}/reloadGuide", Re: regexp.MustCompile(`^/livetv/dvrs/[0-9]{1,12}/reloadGuide$`),
			HomeOnly: true, Use: "reload_guide"},
		{Method: "DELETE", Path: "/media/grabbers/devices/{key}", Re: regexp.MustCompile(`^/media/grabbers/devices/[0-9]{1,12}$`),
			HomeOnly: true, Use: "delete_grabber_device"},
		{Method: "PUT", Path: "/media/grabbers/devices/{key} (title)", Re: regexp.MustCompile(`^/media/grabbers/devices/[0-9]{1,12}$`),
			Query: q("title", anyTitle, "enabled", only1), Required: []string{"title", "enabled"}, HomeOnly: true,
			Use: "set_device_title"},
		{Method: "PUT", Path: "/media/grabbers/devices/{key}/channelmap", Re: regexp.MustCompile(`^/media/grabbers/devices/[0-9]{1,12}/channelmap$`),
			Query:     q("channelsEnabled", channelList),
			QueryKeys: []KeyRule{{Name: channelMapKey, Value: channelNumber}},
			Required:  []string{"channelsEnabled"}, HomeOnly: true, Use: "put_channel_map"},
	},
}

var (
	dzID      = `[A-Za-z0-9_-]{1,64}`
	dzNumber  = `[0-9]{1,6}`
	httpURL   = regexp.MustCompile(`^https?://[^\x00-\x20]{1,1000}$`)
	dzMaxBody = 48 << 20
)

// DizqueTV is dizqueTV as the home's own tv.maktaba.dizquetv reaches it (issue #827). Every
// rule is HomeOnly. A programme body is never read or written raw: the reads are the door's
// projections (Op), with no secret in them, and a show, filler list or channel is written
// through an operation that builds or fills its programmes on the home (dizquetv.go). The raw
// calls left answer with what the plugin sent (the time-slot tool), a link, or nothing.
var DizqueTV = Extension{
	Name:       "dizquetv",
	URLKey:     "url",
	URLEnv:     "MAKTABA_DIZQUETV_URL",
	QueryParam: "X-Plex-Token",
	// a channel's programmes (dizqueTV itself takes up to 50 MB)
	MaxBody: dzMaxBody,
	Rules: []Rule{
		{Method: "GET", Path: "/maktaba/channels", Re: regexp.MustCompile(`^/maktaba/channels$`),
			HomeOnly: true, Op: opChannels, Use: "read_state, read_dizquetv, ping (channel numbers)"},
		{Method: "GET", Path: "/maktaba/shows", Re: regexp.MustCompile(`^/maktaba/shows$`),
			HomeOnly: true, Op: opList, Use: "read_state, read_remote (id, name)"},
		{Method: "GET", Path: "/maktaba/fillers", Re: regexp.MustCompile(`^/maktaba/fillers$`),
			HomeOnly: true, Op: opList, Use: "read_state (id, name)"},
		{Method: "GET", Path: "/maktaba/plex-servers", Re: regexp.MustCompile(`^/maktaba/plex-servers$`),
			HomeOnly: true, Op: opServers, Use: "plex_server_name (names; never a token or an address)"},
		{Method: "GET", Path: "/maktaba/show/{id}", Re: regexp.MustCompile(`^/maktaba/show/` + dzID + `$`),
			HomeOnly: true, Op: opReadList, Use: "read_state, read_remote, read-back (programmes projected)"},
		{Method: "GET", Path: "/maktaba/filler/{id}", Re: regexp.MustCompile(`^/maktaba/filler/` + dzID + `$`),
			HomeOnly: true, Op: opReadList, Use: "read_state, read_dizquetv, read-back (programmes projected)"},
		{Method: "GET", Path: "/maktaba/channel/{n}", Re: regexp.MustCompile(`^/maktaba/channel/` + dzNumber + `$`),
			HomeOnly: true, Op: opReadChannel, Use: "write_channel, read_dizquetv (programmes projected)"},
		{Method: "GET", Path: "/maktaba/channel/programless/{n}", Re: regexp.MustCompile(`^/maktaba/channel/programless/` + dzNumber + `$`),
			HomeOnly: true, Op: opReadChannel, Use: "read_state, read-back (settings projected)"},
		{Method: "PUT", Path: "/maktaba/show", Re: regexp.MustCompile(`^/maktaba/show$`),
			HomeOnly: true, Op: opWriteList, Use: "apply_show, apply_actions (programmes built on the home)"},
		{Method: "POST", Path: "/maktaba/show/{id}", Re: regexp.MustCompile(`^/maktaba/show/` + dzID + `$`),
			HomeOnly: true, Op: opWriteList, Use: "apply_show, apply_actions (programmes built on the home)"},
		{Method: "PUT", Path: "/maktaba/filler", Re: regexp.MustCompile(`^/maktaba/filler$`),
			HomeOnly: true, Op: opWriteList, Use: "apply_actions (programmes built on the home)"},
		{Method: "POST", Path: "/maktaba/filler/{id}", Re: regexp.MustCompile(`^/maktaba/filler/` + dzID + `$`),
			HomeOnly: true, Op: opWriteList, Use: "apply_actions (programmes built on the home)"},
		{Method: "POST", Path: "/maktaba/channel", Re: regexp.MustCompile(`^/maktaba/channel$`),
			HomeOnly: true, Op: opWriteChannel, Use: "write_channel (a new channel; programmes filled on the home)"},
		{Method: "PUT", Path: "/maktaba/channel", Re: regexp.MustCompile(`^/maktaba/channel$`),
			HomeOnly: true, Op: opWriteChannel, Use: "write_channel (programmes filled on the home)"},
		{Method: "HEAD", Path: "/maktaba/art?url=", Re: regexp.MustCompile(`^/maktaba/art$`),
			Query: q("url", httpURL), Required: []string{"url"}, HomeOnly: true, Op: opArt,
			Use: "hosted_link (a logo under the home's dz_art_url)"},
		{Method: "POST", Path: "/api/channel-tools/time-slots", Re: regexp.MustCompile(`^/api/channel-tools/time-slots$`),
			HomeOnly: true, Op: opRaw, Use: "write_channel (the timeline, from the plugin's projections)"},
		{Method: "POST", Path: "/api/upload/image", Re: regexp.MustCompile(`^/api/upload/image$`),
			HomeOnly: true, Op: opRaw, Use: "upload_image (a channel logo)"},
		{Method: "DELETE", Path: "/api/show/{id}", Re: regexp.MustCompile(`^/api/show/` + dzID + `$`),
			HomeOnly: true, Op: opRaw, Use: "apply_show, removals"},
		{Method: "DELETE", Path: "/api/filler/{id}", Re: regexp.MustCompile(`^/api/filler/` + dzID + `$`),
			HomeOnly: true, Op: opRaw, Use: "removals"},
		{Method: "DELETE", Path: "/api/channel", Re: regexp.MustCompile(`^/api/channel$`),
			HomeOnly: true, Op: opRaw, Use: "removals (body: the channel's number)"},
	},
}

// Extensions are the relayable extensions by name.
var Extensions = map[string]*Extension{Plex.Name: &Plex, DizqueTV.Name: &DizqueTV}

// Refusal reasons of Match.
var (
	ErrNotAllowed = errors.New("not on the extension's allow-list")
	ErrBadTarget  = errors.New("malformed path or query")
)

// Match checks a request's method and target ("/path?query") against the
// extension's rules. It returns the rule and the path and raw query to
// forward unchanged.
func (e *Extension) Match(method, target string) (*Rule, string, string, error) {
	return e.MatchFor(false, method, target)
}

// MatchFor is Match for a caller on the home (home) or a node: a HomeOnly rule
// matches only a caller on the home.
func (e *Extension) MatchFor(home bool, method, target string) (*Rule, string, string, error) {
	if len(target) == 0 || len(target) > MaxTarget || target[0] != '/' {
		return nil, "", "", ErrBadTarget
	}
	for _, c := range target {
		if c < 0x21 || c == 0x7f || c == '\\' || c == '#' {
			return nil, "", "", ErrBadTarget
		}
	}
	path, rawQuery, _ := strings.Cut(target, "?")
	// the path is matched as sent: no escapes, no dot segments, no doubled
	// slashes (a pattern sees exactly what Plex will)
	if strings.Contains(path, "%") || strings.Contains(path, "//") || strings.Contains(path, "/.") {
		return nil, "", "", ErrBadTarget
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return nil, "", "", ErrBadTarget
	}
	for k := range values {
		if e.QueryParam != "" && strings.EqualFold(k, e.QueryParam) {
			return nil, "", "", ErrNotAllowed
		}
	}
	method = strings.ToUpper(method)
	for i := range e.Rules {
		r := &e.Rules[i]
		if r.Method != method || !r.Re.MatchString(path) || (r.HomeOnly && !home) {
			continue
		}
		if queryOK(r, values) {
			return r, path, rawQuery, nil
		}
	}
	return nil, "", "", ErrNotAllowed
}

func queryOK(r *Rule, values url.Values) bool {
	for k, vs := range values {
		re, ok := r.Query[k]
		if !ok {
			for _, kr := range r.QueryKeys {
				if kr.Name.MatchString(k) {
					re, ok = kr.Value, true
					break
				}
			}
		}
		if !ok || len(vs) != 1 || !re.MatchString(vs[0]) {
			return false
		}
	}
	for _, k := range r.Required {
		if _, ok := values[k]; !ok {
			return false
		}
	}
	return true
}

// AllowView is one rule as the Machines page lists it.
type AllowView struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Use    string `json:"use"`
}

// Allowed lists the extension's rules.
func (e *Extension) Allowed() []AllowView {
	out := make([]AllowView, 0, len(e.Rules))
	for _, r := range e.Rules {
		out = append(out, AllowView{Method: r.Method, Path: r.Path, Use: r.Use})
	}
	return out
}
