package components

import (
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
)

// Epic 54, story 54.37 §3: the gates a plugin entry adds to Epic 51's (want, hold,
// requirements, licence). Every runner asks the same function (Go here, Python in
// maktaba_pipeline.components.plugingate), and both are tested on one table,
// testdata/plugin_gates.json.

// PluginAPIMajor and PluginAPIMinor are the plugin API this release serves (the host's
// audit.rs PLUGIN_API_MAJOR / PLUGIN_API_MINOR).
const (
	PluginAPIMajor = 1
	PluginAPIMinor = 1
)

// PluginGateInput is what a plugin's gates read: its installed manifest, its grants and
// the home's policy.
type PluginGateInput struct {
	// ServerVersion is this release's ("0.3.83"; "" or "dev": every range passes).
	ServerVersion string `json:"server_version"`
	// Range is the manifest's maktaba range, normalised (">=0.6.0, <0.9.0").
	Range string `json:"range"`
	// PluginAPI is the manifest's plugin_api ("1.0").
	PluginAPI string `json:"plugin_api"`
	// Required are the manifest's required capabilities; Granted the admin's grants.
	Required []string `json:"required"`
	Granted  []string `json:"granted"`
	// ReviewLevel is plugin_installs.review_level; AllowUnreviewed the home's policy
	// (54.10: Allow unreviewed plugins).
	ReviewLevel     string `json:"review_level"`
	AllowUnreviewed bool   `json:"allow_unreviewed"`
	// Revoked and EntitlementMissing are Phase 2 and 3's (54.17, 54.19): false in Phase 1.
	Revoked            bool `json:"revoked"`
	EntitlementMissing bool `json:"entitlement_missing"`
}

// Unreviewed reports whether a review level needs the home's opt-in (54.10): anything the
// store did not countersign (story 54.14 §2: automated or reviewed; "store" is the
// Phase 1 name of the same, never written since).
func Unreviewed(level string) bool {
	return level != "store" && level != "automated" && level != "reviewed"
}

// PluginGate is the first gate that keeps the plugin waiting ("" when none does), with
// the params its words take. The order is the story's: a revocation first, then what no
// admin can change here (the version), then what an admin can.
func PluginGate(in PluginGateInput) (string, map[string]any) {
	switch {
	case in.Revoked:
		return ReasonRevoked, nil
	case !VersionInRange(in.ServerVersion, in.Range):
		return ReasonIncompatible, map[string]any{"range": in.Range, "server": in.ServerVersion}
	case !PluginAPIServed(in.PluginAPI):
		return ReasonIncompatible, map[string]any{"plugin_api": in.PluginAPI,
			"served": strconv.Itoa(PluginAPIMajor) + "." + strconv.Itoa(PluginAPIMinor)}
	case in.EntitlementMissing:
		return ReasonEntitlementMissing, nil
	case Unreviewed(in.ReviewLevel) && !in.AllowUnreviewed:
		return ReasonUnreviewedDisallow, nil
	}
	var missing []string
	for _, c := range in.Required {
		if !slices.Contains(in.Granted, c) {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		return ReasonGrantsNeeded, map[string]any{"capabilities": strings.Join(missing, ",")}
	}
	return "", nil
}

// PluginAPIServed reports whether this release serves a manifest's plugin_api: the same
// major, a minor no newer.
func PluginAPIServed(v string) bool {
	maj, min, ok := strings.Cut(strings.TrimSpace(v), ".")
	if !ok {
		return false
	}
	a, err1 := strconv.Atoi(maj)
	b, err2 := strconv.Atoi(min)
	return err1 == nil && err2 == nil && a == PluginAPIMajor && b <= PluginAPIMinor
}

// VersionInRange reports whether version meets every clause of a manifest range
// (">=0.6.0, <0.9.0"). A development build ("", "dev", or not x.y.z) meets every range.
func VersionInRange(version, rng string) bool {
	v, ok := semver(strings.TrimPrefix(strings.TrimSpace(version), "v"))
	if !ok {
		return true
	}
	for _, clause := range strings.Split(rng, ",") {
		clause = strings.ReplaceAll(clause, " ", "")
		if clause == "" {
			continue
		}
		op := clause[:len(clause)-len(strings.TrimLeft(clause, "<>="))]
		want, ok := semver(clause[len(op):])
		if !ok {
			return false
		}
		c := cmpSemver(v, want)
		pass := false
		switch op {
		case ">=":
			pass = c >= 0
		case ">":
			pass = c > 0
		case "<=":
			pass = c <= 0
		case "<":
			pass = c < 0
		case "=":
			pass = c == 0
		}
		if !pass {
			return false
		}
	}
	return true
}

func semver(s string) ([3]int, bool) {
	// a pre-release or build suffix is ignored: 0.3.83-rc1 is 0.3.83
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	var out [3]int
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func cmpSemver(a, b [3]int) int {
	for i := range 3 {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// live is the catalog with this process's plugin entries (nil: the static one).
var live atomic.Pointer[Catalog]

// Live is the catalog every consumer of a process reads (story 54.37 §2): the static
// catalog merged with the installed plugins' entries, swapped whole by SetLive so a
// reader never sees half a set. Before the first swap it is Default().
func Live() *Catalog {
	if c := live.Load(); c != nil {
		return c
	}
	return Default()
}

// SetLive swaps the live catalog (nil: back to the static one).
func SetLive(c *Catalog) { live.Store(c) }
