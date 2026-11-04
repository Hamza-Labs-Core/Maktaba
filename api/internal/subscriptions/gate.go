package subscriptions

import (
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// TypeLicenseRequired is the problem type of a request the license
// refuses (story 16.2: the server enforces every gate). The body names
// the feature, the home's tier and the lowest tier that grants it, and
// says in plain words what to do; the web shows that detail as is.
const TypeLicenseRequired = "https://maktaba.tv/problems/license-required"

// refusalText is what the admin reads when a paid feature is refused.
var refusalText = map[Feature]string{
	FeatureCloudRelay: "Remote access through Maktaba Cloud needs the Home or Pro plan. " +
		"This home is on %s: use Maktaba on your home network, or apply a license in Settings → Subscription.",
	FeatureMultiUser: "More than one user needs the Home plan (4 users) or Pro (unlimited). " +
		"This home is on %s. Apply a license in Settings → Subscription.",
	FeatureCloudBackup: "Cloud backup needs the Home or Pro plan. This home is on %s. " +
		"Apply a license in Settings → Subscription.",
	FeatureAdvancedMetric: "Analytics older than 30 days, the per-user breakdown and export need the Home or Pro plan. " +
		"This home is on %s. Apply a license in Settings → Subscription.",
	FeatureFederation: "Connecting other Maktaba homes needs the Pro plan. This home is on %s. " +
		"Apply a license in Settings → Subscription.",
}

var tierLabel = map[Tier]string{TierFree: "Free", TierHome: "Home", TierPro: "Pro"}

// TierLabel is a tier's display name.
func TierLabel(t string) string {
	if l, ok := tierLabel[Tier(t)]; ok {
		return l
	}
	return "Free"
}

// RequiredTier is the lowest tier that grants f.
func RequiredTier(f Feature) Tier {
	for _, t := range []Tier{TierFree, TierHome, TierPro} {
		for _, g := range tierFeatures[t] {
			if g == f {
				return t
			}
		}
	}
	return TierPro
}

// Refusal is the 403 a gate answers when the license does not grant f.
func Refusal(f Feature, tier string) *httperror.Error {
	text, ok := refusalText[f]
	if !ok {
		text = "This needs a paid plan. This home is on %s. Apply a license in Settings → Subscription."
	}
	return (&httperror.Error{
		Type:   TypeLicenseRequired,
		Title:  "Not included in this home's plan",
		Status: http.StatusForbidden,
		Detail: strings.Replace(text, "%s", TierLabel(tier), 1),
	}).With("feature", string(f)).With("tier", tier).With("required_tier", string(RequiredTier(f)))
}

// StoreRef hands the entitlement Store to middleware built before the
// store (the router's chain is built first). Empty: the free tier.
type StoreRef struct {
	p     atomic.Pointer[Store]
	relay atomic.Pointer[func() (bool, bool)]
}

// SetRelayLicensed hands RelayGate the cloud.relay component's Licensed condition (Epic
// 51 story 51.19): f answers whether the licence its desired row publishes grants it now,
// and whether that is known (not with the components' kill switch, nor before the API
// has published).
func (r *StoreRef) SetRelayLicensed(f func() (licensed, known bool)) { r.relay.Store(&f) }

// relayRefused is the component's condition: refused when it is known and false.
func (r *StoreRef) relayRefused() bool {
	if r == nil {
		return false
	}
	f := r.relay.Load()
	if f == nil {
		return false
	}
	licensed, known := (*f)()
	return known && !licensed
}

// Set publishes the store.
func (r *StoreRef) Set(s *Store) { r.p.Store(s) }

// Store returns the published store, or a free-tier one.
func (r *StoreRef) Store() *Store {
	if r != nil {
		if s := r.p.Load(); s != nil {
			return s
		}
	}
	return freeStore
}

var freeStore = NewStore()

// RequireFeature refuses every request unless the license grants f.
func RequireFeature(ref *StoreRef, f Feature) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s := ref.Store()
			if !s.Allows(f) {
				httperror.Write(w, r, Refusal(f, s.TierName()))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// viaHeader is middleware.ViaHeader (the relay agent's mark, which
// middleware.StripVia keeps only from loopback); repeated here to keep
// this package free of the middleware package.
const viaHeader = "X-Maktaba-Via"

// relayOpen are the paths answered through the relay on any tier: the
// health probes the cloud uses to see the home is up.
var relayOpen = []string{"/healthz", "/readyz", "/api/health", "/api/system/health"}

// RelayGate refuses a request the cloud relay forwarded unless the
// license grants cloud_relay (story 16.1: free is LAN-only). It must run
// after middleware.StripVia. It asks the cloud.relay component's Licensed
// condition too (story 51.19, SetRelayLicensed): the same answer, from the
// row the runners read; its own store check stays, so neither can open
// remote access the other refuses, and the refusal is the same body.
func RelayGate(ref *StoreRef) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get(viaHeader) == "" || relayIsOpen(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			s := ref.Store()
			if !s.Allows(FeatureCloudRelay) || ref.relayRefused() {
				httperror.Write(w, r, Refusal(FeatureCloudRelay, s.TierName()))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ComponentRefusal is Refusal for turning on a component whose licence flag is f (Epic 51
// story 51.19): the same problem, so a client that reads license-required keeps working,
// with code licence_missing and the component.
func ComponentRefusal(component string, f Feature, tier string) *httperror.Error {
	return Refusal(f, tier).With("code", "licence_missing").With("component", component)
}

func relayIsOpen(path string) bool {
	for _, p := range relayOpen {
		if path == p {
			return true
		}
	}
	return false
}
