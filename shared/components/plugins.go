package components

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Epic 54, story 54.37: third-party plugins as run-time catalog entries.
//
// A plugin is installed long after its server was built, so it cannot be in catalog.toml.
// Its entry (kind plugin, id plugin.<plugin id>) is derived from its manifest and merged
// with the static catalog by With, so every consumer (the API's effect walk, the runners'
// gates, the supervisor) answers the same about it.
const (
	// KindPlugin is a signed third-party package whose stages run in plugins.host.
	KindPlugin = "plugin"
	// PluginPrefix starts every plugin entry's id; a static entry may not use it.
	PluginPrefix = "plugin."
	// PluginsHost is the component of the plugin host's process (maktaba-plugind).
	PluginsHost = "plugins.host"
	// PluginsGroup is the UI section of plugins.
	PluginsGroup = "plugins"
)

// The reason codes Epic 54 adds (story 54.37 §3); each is in catalog.toml's reasons.
const (
	ReasonIncompatible        = "incompatible"
	ReasonGrantsNeeded        = "grants_needed"
	ReasonRevoked             = "revoked"
	ReasonUnreviewedDisallow  = "unreviewed_disallowed"
	ReasonEntitlementMissing  = "entitlement_missing"
	ReasonPluginCrashLoop     = "plugin_crash_loop"
	ReasonSandboxPartial      = "sandbox_partial"
	ReasonSandboxUnavailable  = "sandbox_unavailable"
	ReasonRunnerCrashLoop     = "runner_crash_loop"
	ReasonTrustedUnsigned     = "plugin_trusted_unsigned"
	ReasonTrustedVersion      = "plugin_trusted_version"
	ReasonKeptOff             = "kept_off"
	pluginDrainCapS           = 120
	allPlatforms              = "any"
	PluginRequiresComputeLLM  = "llm"
	PluginRequiresComputeEmbd = "search.semantic"
)

// PluginReasons are the twelve codes, in the order of the story.
var PluginReasons = []string{ReasonIncompatible, ReasonGrantsNeeded, ReasonRevoked, ReasonUnreviewedDisallow,
	ReasonEntitlementMissing, ReasonPluginCrashLoop, ReasonSandboxPartial, ReasonSandboxUnavailable,
	ReasonRunnerCrashLoop, ReasonTrustedUnsigned, ReasonTrustedVersion, ReasonKeptOff}

// PluginSpec is what an entry is derived from: an installed plugin's validated manifest.
type PluginSpec struct {
	ID        string
	Version   string
	Platforms []string // the manifest's; "any" means all four
	// Stages are the queue names (p_<hash>_<name>) of its [[stage]] entries.
	Stages []string
	// Capabilities: required and optional, to derive compute requirements (54.21).
	Required, Optional []string
	TimeoutS           int
	// Since is the server version that installed it.
	Since string
	// RunsOn is the manifest's placement (README §4.13): home (also ""), any or gpu.
	RunsOn string
}

// The manifest's placements (story 54.42 §1).
const (
	PlacementHome = "home"
	PlacementAny  = "any"
	PlacementGPU  = "gpu"
)

// The reasons a joined machine gives for a plugin it does not run (story 54.42).
const (
	// ReasonPluginHomeOnly: the plugin's placement is the home.
	ReasonPluginHomeOnly = "plugin_home_only"
	// ReasonPluginNoGPU: a gpu plugin on a machine that serves no gpu pool.
	ReasonPluginNoGPU = "plugin_no_gpu"
	// ReasonPluginUntrusted: the package the machine fetched failed its verification.
	ReasonPluginUntrusted = "plugin_package_untrusted"
)

// MachineReasons are the codes a joined machine adds for plugins.
var MachineReasons = []string{ReasonPluginHomeOnly, ReasonPluginNoGPU, ReasonPluginUntrusted}

// RunsOnMachine is why a joined machine does not run a plugin of this placement ("" when
// it does): gpu needs a machine that serves the gpu pool.
func RunsOnMachine(placement string, servesGPU bool) string {
	switch placement {
	case PlacementAny:
		return ""
	case PlacementGPU:
		if servesGPU {
			return ""
		}
		return ReasonPluginNoGPU
	}
	return ReasonPluginHomeOnly
}

// PluginEntry derives a plugin's catalog entry (story 54.37 §1 table).
func PluginEntry(p PluginSpec) *Component {
	platforms := p.Platforms
	if len(platforms) == 0 || slices.Contains(platforms, allPlatforms) {
		platforms = []string{"darwin-arm64", "windows-amd64", "linux-amd64", "linux-arm64"}
	}
	requires := []string{PluginsHost}
	wants := []string{}
	for _, c := range p.Required {
		switch c {
		case "compute.llm":
			requires = append(requires, PluginRequiresComputeLLM)
		case "compute.embed":
			requires = append(requires, PluginRequiresComputeEmbd)
		}
	}
	for _, c := range p.Optional {
		switch c {
		case "compute.llm":
			wants = append(wants, PluginRequiresComputeLLM)
		case "compute.embed":
			wants = append(wants, PluginRequiresComputeEmbd)
		}
	}
	grace := p.TimeoutS
	if grace <= 0 || grace > pluginDrainCapS {
		grace = pluginDrainCapS
	}
	return &Component{
		ID: PluginPrefix + p.ID, Kind: KindPlugin, Switchable: true, Group: PluginsGroup,
		TitleKey: "plugin." + p.ID + ".name", DescKey: "plugin." + p.ID + ".summary",
		Stages: nonNil(slices.Clone(p.Stages)), Processes: []string{}, Loops: []string{},
		Requires: requires, Wants: wants, Conflicts: []string{}, Settings: []string{"plugins." + p.ID},
		Platforms: platforms, Placement: catalogPlacement(p.RunsOn), Licence: "", Features: []string{}, Bundles: []string{},
		Since: p.Since, Sources: map[string]string{}, Files: []File{},
		Drain: Drain{GraceS: grace, Checkpoint: true},
	}
}

// catalogPlacement is a plugin entry's Epic 51 placement: home for a home plugin, any for
// one a joined machine may run (any, gpu).
func catalogPlacement(runsOn string) string {
	if runsOn == PlacementAny || runsOn == PlacementGPU {
		return "any"
	}
	return "home"
}

// IsPlugin reports whether an id is a plugin entry's.
func IsPlugin(id string) bool { return strings.HasPrefix(id, PluginPrefix) }

// With answers a catalog over static and the plugin entries: Get, ByStage, Order,
// Requires, Dependents and Components answer for both. An entry must be a plugin entry
// whose requirements exist and whose stages no other component owns.
func With(static *Catalog, entries []*Component) (*Catalog, error) {
	c := &Catalog{Schema: static.Schema, Phases: static.Phases, Reasons: static.Reasons, Features: static.Features,
		components: slices.Clone(static.components), byID: map[string]*Component{}, byStage: map[string]*Component{},
		byLoop: map[string]*Component{}}
	for k, v := range static.byID {
		c.byID[k] = v
	}
	for k, v := range static.byStage {
		c.byStage[k] = v
	}
	for k, v := range static.byLoop {
		c.byLoop[k] = v
	}
	var errs []string
	for _, e := range entries {
		switch {
		case e.Kind != KindPlugin || !IsPlugin(e.ID):
			errs = append(errs, fmt.Sprintf("%s: not a plugin entry", e.ID))
			continue
		case c.byID[e.ID] != nil:
			errs = append(errs, fmt.Sprintf("%s: listed twice", e.ID))
			continue
		}
		for _, r := range e.Requires {
			if c.byID[r] == nil {
				errs = append(errs, fmt.Sprintf("%s: requires unknown component %q", e.ID, r))
			}
		}
		for _, s := range e.Stages {
			if o := c.byStage[s]; o != nil {
				errs = append(errs, fmt.Sprintf("%s: stage %s is %s's", e.ID, s, o.ID))
			}
			c.byStage[s] = e
		}
		c.byID[e.ID] = e
		c.components = append(c.components, e)
	}
	if len(errs) > 0 {
		return nil, joinErrs(errs)
	}
	order, err := c.topoOrder()
	if err != nil {
		return nil, err
	}
	c.order = order
	return c, nil
}

// PluginQueueName is a plugin stage's queue name, p_<8 hex of sha256(id)>_<stage>
// (54.01 §4; pluginmanifest.QueueName, tested on the same vector).
func PluginQueueName(pluginID, stage string) string {
	sum := sha256.Sum256([]byte(pluginID))
	return "p_" + hex.EncodeToString(sum[:])[:8] + "_" + stage
}

// installedManifest is what an entry reads of a normalised manifest (plugin_installs).
type installedManifest struct {
	ID           string   `json:"id"`
	Version      string   `json:"version"`
	Platforms    []string `json:"platforms"`
	Placement    string   `json:"placement"`
	Capabilities struct {
		Required []string `json:"required"`
		Optional []string `json:"optional"`
	} `json:"capabilities"`
	Resources struct {
		TimeoutS int `json:"timeout_s"`
	} `json:"resources"`
	Stage []struct {
		Name string `json:"name"`
	} `json:"stage"`
}

// PluginSpecOf reads a spec from a normalised manifest (plugin_installs.manifest); since
// is the server version that installed it.
func PluginSpecOf(manifest []byte, since string) (PluginSpec, error) {
	var m installedManifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		return PluginSpec{}, fmt.Errorf("components: a plugin manifest: %w", err)
	}
	if m.ID == "" {
		return PluginSpec{}, errors.New("components: a plugin manifest with no id")
	}
	s := PluginSpec{ID: m.ID, Version: m.Version, Platforms: m.Platforms, Required: m.Capabilities.Required,
		Optional: m.Capabilities.Optional, TimeoutS: m.Resources.TimeoutS, Since: since, RunsOn: m.Placement}
	for _, st := range m.Stage {
		s.Stages = append(s.Stages, PluginQueueName(m.ID, st.Name))
	}
	return s, nil
}

// WithPlugins is With over the entries of installed manifests. A manifest that cannot be
// read, or an entry With refuses, is left out and named in skipped; the rest are merged.
func WithPlugins(static *Catalog, specs []PluginSpec) (cat *Catalog, skipped []string) {
	entries := make([]*Component, 0, len(specs))
	for _, s := range specs {
		e := PluginEntry(s)
		if _, err := With(static, append(slices.Clone(entries), e)); err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", e.ID, err))
			continue
		}
		entries = append(entries, e)
	}
	cat, err := With(static, entries)
	if err != nil { // not reached: each entry was checked with the ones before it
		return static, append(skipped, err.Error())
	}
	return cat, skipped
}
