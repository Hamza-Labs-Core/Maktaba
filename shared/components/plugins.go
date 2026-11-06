package components

import (
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
		Platforms: platforms, Placement: "home", Licence: "", Features: []string{}, Bundles: []string{},
		Since: p.Since, Sources: map[string]string{}, Files: []File{},
		Drain: Drain{GraceS: grace, Checkpoint: true},
	}
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
