package components

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
)

// Bundles as setup offers them (story 51.17): the catalog's three, plus custom (a base
// bundle with ticks added).

// BundleCustom is the fourth answer of setup's "Choose what to run": a base bundle (core
// or full) with the components the admin ticked.
const BundleCustom = "custom"

// SetupBundles are setup's four answers, in the order the screen shows them.
var SetupBundles = []string{"full", "core", "worker", BundleCustom}

// ThisPlatform is the catalog platform of the running binary ("darwin-arm64").
func ThisPlatform() string { return runtime.GOOS + "-" + runtime.GOARCH }

// CoreVenvBytes is the download of a venv with no extras (the base wheels of
// constraints-core.txt): what a Core home downloads at setup.
const CoreVenvBytes int64 = 150 << 20

// packEstimates are the download sizes of the packs the index does not size yet: the
// release index carries the real ones (51.15); these are what setup shows before it.
var packEstimates = map[string]int64{
	"runtime.ml":     2000 << 20, // torch / MLX / faster-whisper / transformers wheels
	"runtime.ollama": 170 << 20,
	"tool.yt-dlp":    15 << 20,
}

// AssetBytes is what installing asset c downloads on platform: the files of its source
// there, else its resources' disk_mb, else the pack's estimate. An asset the payload
// carries on platform ("payload:...") downloads nothing.
func (c *Component) AssetBytes(platform string) int64 {
	if c.Kind != KindAsset {
		return 0
	}
	src := c.SourceFor(platform)
	if strings.HasPrefix(src, "payload:") {
		return 0
	}
	var n int64
	for _, f := range c.FilesOf(src) {
		n += f.Size
	}
	if n > 0 {
		return n
	}
	if c.Resources.DiskMB > 0 {
		return int64(c.Resources.DiskMB) << 20
	}
	return packEstimates[c.ID]
}

// RunsOn reports whether c can run on platform (its platforms list it).
func (c *Component) RunsOn(platform string) bool {
	return len(c.Platforms) == 0 || slices.Contains(c.Platforms, platform)
}

// BundleError is a bundle setup or the API cannot make: Code is the step's error code
// (bad_input, unknown_component, platform_unsupported) and ID the component it names.
type BundleError struct {
	Code string
	ID   string
	Msg  string
}

func (e *BundleError) Error() string { return e.Code + ": " + e.Msg }

// BundleSet are the ids a bundle turns on, in Order: the catalog's bundle (for custom,
// base: core or full, "" meaning core) with the extra ids and everything they require
// (an "any:" group not met takes its first member this platform runs, preferring one in
// the base bundle). A component the platform cannot run is left out of a catalog bundle
// and refused as an extra.
func (c *Catalog) BundleSet(bundle, base string, extra []string, platform string) ([]string, error) {
	switch {
	case bundle == BundleCustom:
		if base == "" {
			base = "core"
		}
		if base != "core" && base != "full" {
			return nil, &BundleError{Code: "bad_input", Msg: fmt.Sprintf("a custom bundle starts from core or full, not %q", base)}
		}
	case slices.Contains(Bundles, bundle):
		if len(extra) > 0 {
			return nil, &BundleError{Code: "bad_input", Msg: "only a custom bundle takes components of its own"}
		}
		base = bundle
	default:
		return nil, &BundleError{Code: "bad_input", Msg: fmt.Sprintf("no bundle %q (full, core, worker or custom)", bundle)}
	}
	on := map[string]bool{}
	for _, id := range c.InBundle(base) {
		if comp := c.byID[id]; comp.RunsOn(platform) {
			on[id] = true
		}
	}
	for _, id := range extra {
		comp, ok := c.byID[id]
		if !ok {
			return nil, &BundleError{Code: "unknown_component", ID: id, Msg: "no component " + id}
		}
		if !comp.RunsOn(platform) {
			return nil, &BundleError{Code: "platform_unsupported", ID: id, Msg: id + " does not run on " + platform}
		}
		on[id] = true
	}
	inBase := func(id string) bool { return slices.Contains(c.byID[id].Bundles, base) }
	for changed := true; changed; {
		changed = false
		for _, id := range c.order {
			if !on[id] {
				continue
			}
			for _, r := range c.Requires(id) {
				if slices.ContainsFunc(r.AnyOf, func(x string) bool { return on[x] }) {
					continue
				}
				pick := ""
				for _, x := range r.AnyOf {
					if dep, ok := c.byID[x]; ok && dep.RunsOn(platform) && (pick == "" || inBase(x) && !inBase(pick)) {
						pick = x
					}
				}
				if pick == "" {
					return nil, &BundleError{Code: "platform_unsupported", ID: id,
						Msg: fmt.Sprintf("%s needs one of %s, which do not run on %s", id, strings.Join(r.AnyOf, ", "), platform)}
				}
				on[pick] = true
				changed = true
			}
		}
	}
	var out []string
	for _, id := range c.order {
		if on[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

// SetupWants are the wants a home's first desired state takes from a bundle's set:
// what the set holds is enabled (an asset installed: an asset has no run), every other
// switchable component installed, an asset absent.
func (c *Catalog) SetupWants(set []string) map[string]string {
	in := map[string]bool{}
	for _, id := range set {
		in[id] = true
	}
	out := map[string]string{}
	for _, comp := range c.components {
		switch {
		case !comp.Switchable:
			out[comp.ID] = WantEnabled
		case comp.Kind == KindAsset && in[comp.ID]:
			out[comp.ID] = WantInstalled
		case comp.Kind == KindAsset:
			out[comp.ID] = WantAbsent
		case in[comp.ID]:
			out[comp.ID] = WantEnabled
		default:
			out[comp.ID] = WantInstalled
		}
	}
	return out
}
