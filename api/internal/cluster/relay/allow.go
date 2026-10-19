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
	// Use names the client call that needs it (documentation).
	Use string
	// Verify, when set, is checked against the upstream before the call
	// is forwarded (DELETE only removes collections).
	Verify string
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
	Rules      []Rule
}

// HomeOnly lists the extensions whose calls are never relayed, with why.
var HomeOnly = map[string]string{
	// Its API token can create and delete every video of the account and
	// the calls are admin operations made by the home's API itself
	// (api/internal/tvremote): no stage needs them on a node.
	"cloudflare_stream": "the Cloudflare API token is used by the home's API only (admin operations)",
	// dizqueTV holds no secret of its own; its stages are home-only for
	// other reasons (they embed the Plex token in dizqueTV programmes).
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
		if r.Method != method || !r.Re.MatchString(path) {
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
