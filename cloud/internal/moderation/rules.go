// Package moderation is Maktaba Cloud's takedown, reports and repeat-infringer policy for home
// broadcasting (Epic 50 story 50.24). Maktaba Cloud hosts the channels its users publish from
// their homes; this package keeps what a host needs for that: the notice and takedown queue,
// the viewer reports, the strikes and the records.
//
// A takedown never touches a library: the cloud sends the home a signed instruction over the
// cloud link (Takedowns), and the home removes the item's publishable mark (or the source's). An
// item airing now is slated at the edge at once through a kill-switch entry of kind programme
// (story 50.25); a channel or a publisher is taken off air the same way.
//
// This file is the rules with no database: what a notice must carry (T01), what a watch URL
// names (T02), the strike levels (T05) and the counter-notice window (T06).
package moderation

import (
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The bases an action records (§2, acting on actual knowledge).
const (
	BasisNotice         = "notice"
	BasisReport         = "report"
	BasisAuthority      = "authority"
	BasisAdminKnowledge = "admin_knowledge"
)

// The scopes of a takedown: an item and a source are the home's marks; a channel and a
// publisher are the kill switch's (flagrant cases).
const (
	ScopeItem      = "item"
	ScopeSource    = "source"
	ScopeChannel   = "channel"
	ScopePublisher = "publisher"
)

// The report categories (§4); illegal content is urgent and pages the on-call admin.
const (
	CategoryCopyright = "copyright"
	CategoryIllegal   = "illegal"
	CategoryAbuse     = "abuse"
	CategoryOther     = "other"
)

var (
	bases      = []string{BasisNotice, BasisReport, BasisAuthority, BasisAdminKnowledge}
	scopes     = []string{ScopeItem, ScopeSource, ScopeChannel, ScopePublisher}
	categories = []string{CategoryCopyright, CategoryIllegal, CategoryAbuse, CategoryOther}
	// the publishable source kinds a home's permission names (story 50.23)
	sourceKinds = []string{"channel", "playlist", "speaker", "label"}
)

// ValidBasis, ValidScope, ValidCategory and ValidSourceKind check a request's enum values.
func ValidBasis(b string) bool      { return slices.Contains(bases, b) }
func ValidScope(s string) bool      { return slices.Contains(scopes, s) }
func ValidCategory(c string) bool   { return slices.Contains(categories, c) }
func ValidSourceKind(k string) bool { return slices.Contains(sourceKinds, k) }

// Urgent reports whether a report's category pages the on-call admin (SEV2, ≤ 1 h to a decision).
func Urgent(category string) bool { return category == CategoryIllegal }

// WatchHost is the host viewers watch on; a notice's URLs must be on it.
const WatchHost = "watch.maktaba.tv"

// Notice is a copyright notice as the form (or a service admin, from an email) sends it.
type Notice struct {
	ClaimantName    string     `json:"claimant_name"`
	ClaimantEmail   string     `json:"claimant_email"`
	ClaimantAddress string     `json:"claimant_address"`
	ClaimantPhone   string     `json:"claimant_phone"`
	Work            string     `json:"work"`
	URLs            []string   `json:"urls"`
	Programme       string     `json:"programme"`
	At              *time.Time `json:"at"`
	Explanation     string     `json:"explanation"`
	GoodFaith       bool       `json:"good_faith"`
	Accuracy        bool       `json:"accuracy"`
	Signature       string     `json:"signature"`
	// Privacy asks that the publisher not be told the claimant's name (counsel's template decides
	// whether it is honoured).
	Privacy bool `json:"privacy"`
	Urgent  bool `json:"urgent"`
}

// The elements a notice can miss, as the reply names them (copyright.missing.* on the page).
const (
	MissingName        = "claimant_name"
	MissingEmail       = "claimant_email"
	MissingAddress     = "claimant_address"
	MissingWork        = "work"
	MissingURL         = "urls"
	MissingExplanation = "explanation"
	MissingGoodFaith   = "good_faith"
	MissingAccuracy    = "accuracy"
	MissingSignature   = "signature"
)

const maxField = 4000

func blank(s string) bool { return strings.TrimSpace(s) == "" }

// Missing is what a notice lacks of § 512(c)(3) and DSA Art. 16: the claimant and how to reach
// them, the work, at least one URL, the explanation, both statements and the typed signature.
// An empty answer is a complete notice (its URLs may still name nothing on Maktaba: OnMaktaba).
func (n Notice) Missing() []string {
	var out []string
	if blank(n.ClaimantName) || len(n.ClaimantName) > 200 {
		out = append(out, MissingName)
	}
	if _, err := mail.ParseAddress(strings.TrimSpace(n.ClaimantEmail)); err != nil || len(n.ClaimantEmail) > 320 {
		out = append(out, MissingEmail)
	}
	if blank(n.ClaimantAddress) || len(n.ClaimantAddress) > 1000 {
		out = append(out, MissingAddress)
	}
	if blank(n.Work) || len(n.Work) > maxField {
		out = append(out, MissingWork)
	}
	urls := 0
	for _, u := range n.URLs {
		if !blank(u) {
			urls++
		}
	}
	if urls == 0 || len(n.URLs) > 20 {
		out = append(out, MissingURL)
	}
	if blank(n.Explanation) || len(n.Explanation) > maxField {
		out = append(out, MissingExplanation)
	}
	if !n.GoodFaith {
		out = append(out, MissingGoodFaith)
	}
	if !n.Accuracy {
		out = append(out, MissingAccuracy)
	}
	if blank(n.Signature) || len(n.Signature) > 200 {
		out = append(out, MissingSignature)
	}
	return out
}

// ChannelRef is what a watch URL names: the publisher's home slug and the channel's slug.
type ChannelRef struct {
	Publisher string `json:"publisher"`
	Channel   string `json:"channel"`
}

var slugRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// ParseWatchURL reads watch.maktaba.tv/<publisher>/<channel> (a scheme is optional; a query, a
// fragment or a trailing path such as /live is ignored). false: not a channel on Maktaba.
func ParseWatchURL(raw string) (ChannelRef, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ChannelRef{}, false
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || !strings.EqualFold(u.Hostname(), WatchHost) {
		return ChannelRef{}, false
	}
	parts := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })
	if len(parts) < 2 {
		return ChannelRef{}, false
	}
	pub, ch := strings.ToLower(parts[0]), strings.ToLower(parts[1])
	// the watch site's own pages are no channel (invites, pairing, its API)
	if pub == "i" || pub == "pair" || pub == "api" || !slugRE.MatchString(pub) || !slugRE.MatchString(ch) {
		return ChannelRef{}, false
	}
	return ChannelRef{Publisher: pub, Channel: ch}, true
}

// OnMaktaba is the first URL of a notice that names a channel on Maktaba (false: the notice is
// about another site and is answered with that explanation).
func OnMaktaba(urls []string) (ChannelRef, bool) {
	for _, u := range urls {
		if ref, ok := ParseWatchURL(u); ok {
			return ref, true
		}
	}
	return ChannelRef{}, false
}

// GuideEntry is one programme of the guide a home published for a channel (story 34.2's format:
// the fields the queue reads). ItemRef is the home's opaque reference to the item.
type GuideEntry struct {
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Title   string    `json:"title"`
	ItemRef string    `json:"item_ref"`
}

// ProgrammeAt is the guide's programme airing at t; false: "unknown" (the guide has no slot
// then). The home then resolves the time itself from its schedule when it applies the takedown.
func ProgrammeAt(guide []GuideEntry, t time.Time) (GuideEntry, bool) {
	for _, g := range guide {
		if !t.Before(g.Start) && t.Before(g.End) {
			return g, true
		}
	}
	return GuideEntry{}, false
}

// Policy is the repeat-infringer policy's thresholds (broadcast.strikes.limit and
// broadcast.strikes.window_months: changed only by a pull request).
type Policy struct {
	Limit        int
	WindowMonths int
}

// DefaultPolicy is 3 strikes in 12 months.
var DefaultPolicy = Policy{Limit: 3, WindowMonths: 12}

// Expiry is when a strike given at t stops counting.
func (p Policy) Expiry(t time.Time) time.Time { return t.AddDate(0, p.WindowMonths, 0) }

// Strike is one strike as the policy counts it.
type Strike struct {
	At        time.Time
	ExpiresAt time.Time
	Withdrawn bool
}

// Active counts the strikes that hold at now: not withdrawn and not expired.
func Active(strikes []Strike, now time.Time) int {
	n := 0
	for _, s := range strikes {
		if !s.Withdrawn && now.Before(s.ExpiresAt) {
			n++
		}
	}
	return n
}

// The levels the policy gives a count of active strikes.
const (
	LevelNone      = ""
	LevelNotice    = "notice"    // 1: the publisher is told
	LevelWarning   = "warning"   // limit-1: the next ends broadcasting
	LevelTerminate = "terminate" // limit: broadcasting ends
)

// Level is what a count of active strikes means.
func (p Policy) Level(active int) string {
	switch {
	case active <= 0:
		return LevelNone
	case active >= p.Limit:
		return LevelTerminate
	case active == p.Limit-1:
		return LevelWarning
	}
	return LevelNotice
}

// RestoreAfterDays is the counter-notice window's start: 10 business days (§ 512(g) allows 10–14).
const RestoreAfterDays = 10

// AddBusinessDays is t plus n working days (Monday to Friday; public holidays are not known here,
// so the window opens at the earliest the law allows and a service admin restores by hand).
func AddBusinessDays(t time.Time, n int) time.Time {
	d := t
	for n > 0 {
		d = d.AddDate(0, 0, 1)
		if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday {
			n--
		}
	}
	return d
}

// RestoreOpen reports whether a counter-notice filed at filed lets the item be restored at now:
// the window has passed and the claimant has not said they went to court.
func RestoreOpen(filed time.Time, lawsuit bool, now time.Time) bool {
	return !lawsuit && !now.Before(AddBusinessDays(filed, RestoreAfterDays))
}

// StrikeDedupe is the window in which a second notice about the same item gives no second strike.
const StrikeDedupe = 30 * 24 * time.Hour
