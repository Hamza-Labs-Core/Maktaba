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
}

// KeyRule is a family of query parameters: a name pattern and its value's.
type KeyRule struct {
	Name  *regexp.Regexp
	Value *regexp.Regexp
}

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
}

var (
	anyTitle = regexp.MustCompile(`^[^\x00-\x1f]{1,512}$`)
	only1    = regexp.MustCompile(`^1$`)
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

// Plex is the Plex extension (Plex Live TV is the only Plex integration Maktaba keeps): a
// node may make the reachability probe only; the Live TV calls (/livetv, /media/grabbers)
// are plex_livetv's, made by its plugin on the home beside write_channels, so they are
// HomeOnly.
var Plex = Extension{
	Name:       "plex",
	SecretName: "ext.plex.token",
	Header:     "X-Plex-Token",
	URLKey:     "url",
	URLEnv:     "MAKTABA_PLEX_URL",
	QueryParam: "X-Plex-Token",
	Rules: []Rule{
		{Method: "GET", Path: "/identity", Re: regexp.MustCompile(`^/identity$`),
			Use: "ping"},
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

// Extensions are the relayable extensions by name.
var Extensions = map[string]*Extension{Plex.Name: &Plex}

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
