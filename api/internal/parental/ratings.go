// Package parental holds the age tiers of the home's content (Epic 57, story 57.01):
// the rating tables, the rule that gives a video its tier, and the recompute that writes
// it. Restricted accounts and their policies are story 57.02's.
package parental

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// The scale. A video's tier is one of Tiers, or unrated (no tier).
const (
	TierAll = 0
	Tier9   = 9
	Tier13  = 13
	Tier16  = 16
	Tier18  = 18
	// NothingAllowed is a limit under which only allow-listed items show (Apple's 0,
	// Don't Allow). It is never a video's tier.
	NothingAllowed = -1
	// NoLimit is a limit that hides nothing (Apple's 1000). It is never a video's tier.
	NoLimit = 1000
)

// Tiers is the scale, mildest first.
var Tiers = []int{TierAll, Tier9, Tier13, Tier16, Tier18}

// ValidTier reports whether n is a tier a video or a container may have.
func ValidTier(n int) bool {
	for _, t := range Tiers {
		if t == n {
			return true
		}
	}
	return false
}

// ContentTier is the tier of content of nominal age: the smallest tier at or above it.
func ContentTier(age int) int {
	for _, t := range Tiers {
		if t >= age {
			return t
		}
	}
	return Tier18
}

// LimitTier is the tier a limit of nominal age allows up to: the largest tier at or
// below it.
func LimitTier(age int) int {
	out := TierAll
	for _, t := range Tiers {
		if t <= age {
			out = t
		}
	}
	return out
}

// ratingsJSON is a copy of shared/parental/ratings.json (TestEmbeddedRatingsAreTheSharedFile
// keeps the two equal).
//
//go:embed ratings.json
var ratingsJSON []byte

// RatingsJSON is the scale and the tables as GET /api/parental/ratings serves them.
func RatingsJSON() []byte { return ratingsJSON }

type systemDef struct {
	Ages       map[string]int    `json:"ages"`
	Plain      bool              `json:"plain"`
	LimitsOnly bool              `json:"limits_only"`
	Scale      map[string]string `json:"scale"`
}

type tablesDef struct {
	Bare          []string             `json:"bare"`
	Prefixes      map[string][]string  `json:"prefixes"`
	UnratedLabels []string             `json:"unrated_labels"`
	Systems       map[string]systemDef `json:"systems"`
}

// Table maps a rating string to a tier.
type Table struct {
	bare     []string
	prefixes map[string][]string
	unrated  map[string]bool
	systems  map[string]systemDef // label keys in upper case

	// Logger gets one parental.rating_unknown line per distinct unknown string (nil:
	// slog.Default()).
	Logger *slog.Logger

	mu      sync.Mutex
	unknown map[string]bool
}

// maxUnknown bounds the strings remembered as logged.
const maxUnknown = 2000

// NewTable reads tables in the form of shared/parental/ratings.json.
func NewTable(data []byte) (*Table, error) {
	var d tablesDef
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("parental: ratings: %w", err)
	}
	t := &Table{bare: d.Bare, prefixes: d.Prefixes, unrated: map[string]bool{},
		systems: map[string]systemDef{}, unknown: map[string]bool{}}
	for _, l := range d.UnratedLabels {
		t.unrated[strings.ToUpper(l)] = true
	}
	for key, s := range d.Systems {
		ages := make(map[string]int, len(s.Ages))
		for l, a := range s.Ages {
			ages[strings.ToUpper(l)] = a
		}
		s.Ages = ages
		t.systems[key] = s
	}
	for _, list := range append([][]string{d.Bare}, values(d.Prefixes)...) {
		for _, key := range list {
			if _, ok := t.systems[key]; !ok {
				return nil, fmt.Errorf("parental: ratings: system %q is not defined", key)
			}
		}
	}
	return t, nil
}

func values(m map[string][]string) [][]string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([][]string, 0, len(m))
	for _, k := range keys {
		out = append(out, m[k])
	}
	return out
}

var (
	defaultOnce  sync.Once
	defaultTable *Table
)

// Default is the table of the embedded ratings file.
func Default() *Table {
	defaultOnce.Do(func() {
		t, err := NewTable(ratingsJSON)
		if err != nil {
			panic(err) // the embedded file is checked by the package's tests
		}
		defaultTable = t
	})
	return defaultTable
}

// meaning is what a rating string says.
type meaning struct {
	known      bool // the string is in a table
	age        int  // the label's nominal age
	limitsOnly bool // it is a limit, never a video's rating
	special    int  // NothingAllowed or NoLimit for a limit that is no age; else 0
}

// Content is the tier rating gives a video; ok is false when the rating is unrated or
// unknown. An unknown string is logged once.
func (t *Table) Content(rating string) (tier int, ok bool) {
	m := t.lookup(rating)
	if !m.known || m.limitsOnly {
		return 0, false
	}
	return ContentTier(m.age), true
}

// Limit is the tier rating allows up to when it is a limit: a tier, NothingAllowed or
// NoLimit. ok is false when the rating is unrated or unknown.
func (t *Table) Limit(rating string) (limit int, ok bool) {
	m := t.lookup(rating)
	if !m.known {
		return 0, false
	}
	if m.special != 0 {
		return m.special, true
	}
	return LimitTier(m.age), true
}

func (t *Table) lookup(rating string) meaning {
	s := strings.TrimSpace(rating)
	if s == "" || t.unrated[strings.ToUpper(s)] {
		return meaning{}
	}
	systems, label := t.bare, s
	if i := strings.Index(s, "/"); i >= 0 {
		prefix := strings.ToLower(strings.TrimSpace(s[:i]))
		label = strings.TrimSpace(s[i+1:])
		if _, ok := t.systems[prefix]; ok {
			systems = []string{prefix}
		} else {
			systems = t.prefixes[prefix]
		}
		if t.unrated[strings.ToUpper(label)] {
			return meaning{}
		}
	}
	for _, key := range systems {
		if m := t.inSystem(key, label, 0); m.known {
			return m
		}
	}
	t.logUnknown(s)
	return meaning{}
}

func (t *Table) inSystem(key, label string, depth int) meaning {
	sys, ok := t.systems[key]
	if !ok {
		return meaning{}
	}
	switch {
	case sys.Plain:
		if age, ok := plainAge(label); ok {
			return meaning{known: true, age: age}
		}
	case sys.Scale != nil:
		switch ref := sys.Scale[label]; {
		case ref == "":
		case ref == "nothing":
			return meaning{known: true, limitsOnly: true, special: NothingAllowed}
		case ref == "no_limit":
			return meaning{known: true, limitsOnly: true, special: NoLimit}
		case depth == 0:
			if i := strings.Index(ref, "/"); i > 0 {
				if m := t.inSystem(ref[:i], ref[i+1:], 1); m.known {
					m.limitsOnly = true
					return m
				}
			}
		}
	default:
		if age, ok := sys.Ages[strings.ToUpper(label)]; ok {
			return meaning{known: true, age: age, limitsOnly: sys.LimitsOnly}
		}
	}
	return meaning{}
}

// plainAge reads "NN" or "NN+".
func plainAge(label string) (int, bool) {
	d := strings.TrimSuffix(label, "+")
	if d == "" || len(d) > 3 {
		return 0, false
	}
	for _, c := range d {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(d)
	return n, err == nil
}

func (t *Table) logUnknown(s string) {
	key := strings.ToUpper(s)
	if len(key) > 80 {
		key = key[:80]
	}
	t.mu.Lock()
	if t.unknown[key] || len(t.unknown) >= maxUnknown {
		t.mu.Unlock()
		return
	}
	t.unknown[key] = true
	t.mu.Unlock()
	l := t.Logger
	if l == nil {
		l = slog.Default()
	}
	l.Warn("parental: a rating is in no table, so it counts as unrated", "rating", key,
		"event", "parental.rating_unknown")
}
