// Package components is the component catalog (Epic 51, story 51.01): every worker,
// stage group, background loop, extension and asset of Maktaba, once, with what it
// requires and where it runs.
//
// The catalog is catalog.toml beside this file, embedded at build time. Nothing in it
// changes at runtime: the desired state (what an admin wants) and the actual state (what
// each machine reports) are database rows, read and written through the store package.
//
//	c := components.Default()
//	comp, ok := c.ByStage("transcribe")          // transcription
//	comp, ok = c.ByProcess("pipeline", "stt-2")  // transcription
//	comp, ok = c.ByLoop("api", "tvchange_controller")
//	for _, id := range c.Order() { ... }         // requirements first
//
// The pipeline reads the same file (maktaba_pipeline.components.catalog) and validates
// it with the same rules; testdata/catalog.dump.json is what both loaders must answer.
package components

import (
	"bytes"
	_ "embed" // the catalog file
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

//go:embed catalog.toml
var catalogTOML []byte

// Kinds. A component's kind is one of these, or a "+" join of the first three in the
// order stages+process+loop.
const (
	KindStages    = "stages"
	KindProcess   = "process"
	KindLoop      = "loop"
	KindExtension = "extension"
	KindAsset     = "asset"
)

// Phases, as the catalog lists them (README §4.3). The store's Rollup ranks them.
const (
	PhaseNotInstalled = "not_installed"
	PhaseInstalling   = "installing"
	PhaseRemoving     = "removing"
	PhaseOff          = "off"
	PhaseStopped      = "stopped"
	PhaseWaiting      = "waiting"
	PhaseStarting     = "starting"
	PhaseRunning      = "running"
	PhaseDegraded     = "degraded"
	PhaseDraining     = "draining"
	PhaseFailed       = "failed"
)

// KnownPhases is every phase the code handles; the catalog's `phases` must be exactly
// these (a new phase needs code that ranks it).
var KnownPhases = []string{PhaseNotInstalled, PhaseInstalling, PhaseRemoving, PhaseOff,
	PhaseStopped, PhaseWaiting, PhaseStarting, PhaseRunning, PhaseDegraded, PhaseDraining,
	PhaseFailed}

// Want values of the desired state.
const (
	WantAbsent    = "absent"
	WantInstalled = "installed"
	WantEnabled   = "enabled"
)

// Condition types (README §4.3).
const (
	CondInstalled       = "Installed"
	CondRequirementsMet = "RequirementsMet"
	CondLicensed        = "Licensed"
	CondSupported       = "Supported"
	CondReady           = "Ready"
)

// The closed lists a component's fields are checked against.
var (
	Platforms = []string{"darwin-arm64", "windows-amd64", "linux-amd64", "linux-arm64"}
	Groups    = []string{"core", "media", "library", "processing", "livetv", "integrations",
		"cluster", "system", "runtimes", "models", "tools", "languages"}
	Bundles   = []string{"full", "core", "worker"}
	LoopHosts = []string{"supervisor", "api", "streaming", "pipeline"}
)

// Default stop and drain times.
const (
	DefaultDrainGraceS = 30
	DefaultStopGraceS  = 5
)

var (
	idRe      = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_*-]+)*$`)
	stageRe   = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	processRe = regexp.MustCompile(`^[a-z][a-z0-9-]*/(\*|[a-z0-9-]*\*?)$`)
	loopRe    = regexp.MustCompile(`^(supervisor|api|streaming|pipeline):[a-z][a-z0-9_-]*$`)
	settingRe = regexp.MustCompile(`^(rules\.[a-z][a-z0-9_]*|toml:[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*)$`)
	sinceRe   = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	licenceRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	sha256Re  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

var topKeys = []string{"schema", "phases", "reasons", "features", "component"}

var componentKeys = []string{"id", "kind", "switchable", "group", "title_key", "desc_key",
	"stages", "processes", "loops", "requires", "wants", "conflicts", "settings", "platforms",
	"placement", "resources", "drain", "stop_grace_s", "licence", "features", "bundles", "since",
	"source", "sources", "files", "in_payload", "gated"}

// Drain is how a component with stages stops (README §4.5).
type Drain struct {
	GraceS     int  `toml:"grace_s" json:"grace_s"`
	Checkpoint bool `toml:"checkpoint" json:"checkpoint"`
}

// Resources is what a component needs to run on a machine.
type Resources struct {
	RAMMB  int    `toml:"ram_mb" json:"ram_mb"`
	DiskMB int    `toml:"disk_mb" json:"disk_mb"`
	GPU    string `toml:"gpu" json:"gpu"` // "", none, preferred, required
}

// File is one file of an asset, as the signed index lists it (tools/release/cmd/pin-models
// fills them, story 51.15). Source is the source it comes from when the asset has more
// than one (a per-platform sources entry); empty, the asset's default source.
type File struct {
	Path   string `toml:"path" json:"path"`
	SHA256 string `toml:"sha256" json:"sha256"`
	Size   int64  `toml:"size" json:"size"`
	Source string `toml:"source,omitempty" json:"source,omitempty"`
}

// FilesOf are the asset's files that come from source.
func (c *Component) FilesOf(source string) []File {
	var out []File
	for _, f := range c.Files {
		if f.Source == source || (f.Source == "" && source == c.Source) {
			out = append(out, f)
		}
	}
	return out
}

// Component is one catalog entry. Lists are never nil.
type Component struct {
	ID         string
	Kind       string
	Switchable bool
	Group      string
	TitleKey   string
	DescKey    string
	Stages     []string
	Processes  []string // "<role>/<instance>" patterns
	Loops      []string // "<host>:<name>"
	Requires   []string // ids or "any:a|b"; Catalog.Requires resolves them
	Wants      []string
	Conflicts  []string
	Settings   []string
	Platforms  []string
	Placement  string // any | home
	Resources  Resources
	Drain      Drain
	StopGraceS int
	Licence    string
	Features   []string
	Bundles    []string
	Since      string
	Source     string
	Sources    map[string]string // per platform, over Source
	Files      []File
	InPayload  bool
	// Gated: a model whose download needs the owner's Hugging Face login (Cohere).
	Gated bool
}

// HasPart reports whether the kind includes part (stages, process or loop).
func (c *Component) HasPart(part string) bool {
	return slices.Contains(strings.Split(c.Kind, "+"), part)
}

// SourceFor is where the asset comes from on platform.
func (c *Component) SourceFor(platform string) string {
	if s, ok := c.Sources[platform]; ok {
		return s
	}
	return c.Source
}

// Requirement is one entry of `requires`: any one of AnyOf is enough (a plain id is a
// group of one).
type Requirement struct {
	AnyOf []string `json:"any_of"`
}

// Catalog is a loaded, validated catalog. It is read-only.
type Catalog struct {
	Schema     int
	Phases     []string
	Reasons    []string
	Features   []string
	components []*Component
	byID       map[string]*Component
	byStage    map[string]*Component
	byLoop     map[string]*Component
	order      []string
}

var (
	defaultOnce sync.Once
	defaultCat  *Catalog
	defaultErr  error
)

// Default is the embedded catalog, parsed once. It panics on a bad catalog: the tests
// load the same bytes, so a bad file never ships.
func Default() *Catalog {
	defaultOnce.Do(func() { defaultCat, defaultErr = Load(catalogTOML) })
	if defaultErr != nil {
		panic("components: the embedded catalog is invalid: " + defaultErr.Error())
	}
	return defaultCat
}

// Raw is the embedded catalog file as it is.
func Raw() []byte { return bytes.Clone(catalogTOML) }

type rawComponent struct {
	ID         string            `toml:"id"`
	Kind       string            `toml:"kind"`
	Switchable *bool             `toml:"switchable"`
	Group      string            `toml:"group"`
	TitleKey   string            `toml:"title_key"`
	DescKey    string            `toml:"desc_key"`
	Stages     []string          `toml:"stages"`
	Processes  []string          `toml:"processes"`
	Loops      []string          `toml:"loops"`
	Requires   []string          `toml:"requires"`
	Wants      []string          `toml:"wants"`
	Conflicts  []string          `toml:"conflicts"`
	Settings   []string          `toml:"settings"`
	Platforms  []string          `toml:"platforms"`
	Placement  string            `toml:"placement"`
	Resources  *Resources        `toml:"resources"`
	Drain      *Drain            `toml:"drain"`
	StopGraceS *int              `toml:"stop_grace_s"`
	Licence    string            `toml:"licence"`
	Features   []string          `toml:"features"`
	Bundles    []string          `toml:"bundles"`
	Since      string            `toml:"since"`
	Source     string            `toml:"source"`
	Sources    map[string]string `toml:"sources"`
	Files      []File            `toml:"files"`
	InPayload  *bool             `toml:"in_payload"`
	Gated      bool              `toml:"gated"`
}

type rawCatalog struct {
	Schema    int            `toml:"schema"`
	Phases    []string       `toml:"phases"`
	Reasons   []string       `toml:"reasons"`
	Features  []string       `toml:"features"`
	Component []rawComponent `toml:"component"`
}

// Load parses and validates a catalog. Every problem found is in the error, one per line.
func Load(data []byte) (*Catalog, error) {
	var loose map[string]any
	if _, err := toml.Decode(string(data), &loose); err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	var errs []string
	bad := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }
	for _, k := range sortedKeys(loose) {
		if !slices.Contains(topKeys, k) {
			bad("unknown top-level field %q", k)
		}
	}
	if list, ok := loose["component"].([]map[string]any); ok {
		for i, m := range list {
			name, _ := m["id"].(string)
			for _, k := range sortedKeys(m) {
				if !slices.Contains(componentKeys, k) {
					bad("component %d (%s): unknown field %q", i, name, k)
				}
			}
		}
	}
	if len(errs) > 0 {
		return nil, joinErrs(errs)
	}
	var raw rawCatalog
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	c := &Catalog{Schema: raw.Schema, Phases: nonNil(raw.Phases), Reasons: nonNil(raw.Reasons),
		Features: nonNil(raw.Features), byID: map[string]*Component{}, byStage: map[string]*Component{},
		byLoop: map[string]*Component{}}
	if raw.Schema != 1 {
		bad("schema = %d: this code reads schema 1", raw.Schema)
	}
	if !slices.Equal(c.Phases, KnownPhases) {
		bad("phases must be exactly %v (the code ranks them), got %v", KnownPhases, c.Phases)
	}
	checkList := func(what string, list []string, re *regexp.Regexp) {
		if len(list) == 0 {
			bad("%s is empty", what)
		}
		seen := map[string]bool{}
		for _, v := range list {
			if seen[v] {
				bad("%s lists %q twice", what, v)
			}
			seen[v] = true
			if !re.MatchString(v) {
				bad("%s: bad value %q", what, v)
			}
		}
	}
	checkList("reasons", c.Reasons, stageRe)
	checkList("features", c.Features, stageRe)

	procs := map[string]string{}
	for i := range raw.Component {
		comp := build(raw.Component[i])
		where := fmt.Sprintf("component %s", comp.ID)
		if !idRe.MatchString(comp.ID) {
			bad("component %d: bad id %q", i, comp.ID)
			where = fmt.Sprintf("component %d", i)
		}
		if _, dup := c.byID[comp.ID]; dup {
			bad("component id %q is listed twice", comp.ID)
			continue
		}
		c.byID[comp.ID] = comp
		c.components = append(c.components, comp)
		validate(comp, where, c.Features, bad)
		for _, s := range comp.Stages {
			if other, ok := c.byStage[s]; ok && other != comp {
				bad("stage %q is in both %s and %s", s, other.ID, comp.ID)
				continue
			}
			c.byStage[s] = comp
		}
		for _, p := range comp.Processes {
			if other, ok := procs[p]; ok {
				bad("process %q is in both %s and %s", p, other, comp.ID)
				continue
			}
			procs[p] = comp.ID
		}
		for _, l := range comp.Loops {
			if other, ok := c.byLoop[l]; ok && other != comp {
				bad("loop %q is in both %s and %s", l, other.ID, comp.ID)
				continue
			}
			c.byLoop[l] = comp
		}
	}
	// references between components
	for _, comp := range c.components {
		for _, r := range comp.Requires {
			for _, id := range splitRequirement(r) {
				if _, ok := c.byID[id]; !ok {
					bad("component %s: requires unknown component %q", comp.ID, id)
				} else if id == comp.ID {
					bad("component %s: requires itself", comp.ID)
				}
			}
		}
		for _, field := range []struct {
			name string
			ids  []string
		}{{"wants", comp.Wants}, {"conflicts", comp.Conflicts}} {
			for _, id := range field.ids {
				other, ok := c.byID[id]
				switch {
				case !ok:
					bad("component %s: %s unknown component %q", comp.ID, field.name, id)
				case id == comp.ID:
					bad("component %s: %s itself", comp.ID, field.name)
				case field.name == "conflicts" && !slices.Contains(other.Conflicts, comp.ID):
					bad("component %s conflicts with %s but %s does not list %s: conflicts must be listed on both sides",
						comp.ID, id, id, comp.ID)
				}
			}
		}
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

func build(r rawComponent) *Component {
	comp := &Component{ID: r.ID, Kind: r.Kind, Switchable: true, Group: r.Group,
		TitleKey: r.TitleKey, DescKey: r.DescKey, Stages: nonNil(r.Stages),
		Processes: nonNil(r.Processes), Loops: nonNil(r.Loops), Requires: nonNil(r.Requires),
		Wants: nonNil(r.Wants), Conflicts: nonNil(r.Conflicts), Settings: nonNil(r.Settings),
		Platforms: nonNil(r.Platforms), Placement: r.Placement, Licence: r.Licence,
		Features: nonNil(r.Features), Bundles: nonNil(r.Bundles), Since: r.Since, Source: r.Source,
		Sources: r.Sources, Files: r.Files, Gated: r.Gated, Drain: Drain{GraceS: DefaultDrainGraceS}}
	if r.Switchable != nil {
		comp.Switchable = *r.Switchable
	}
	if comp.Placement == "" {
		comp.Placement = "any"
	}
	if r.Resources != nil {
		comp.Resources = *r.Resources
	}
	if r.Drain != nil {
		comp.Drain = *r.Drain
	}
	if r.StopGraceS != nil {
		comp.StopGraceS = *r.StopGraceS
	} else if comp.HasPart(KindLoop) {
		comp.StopGraceS = DefaultStopGraceS
	}
	if r.InPayload != nil {
		comp.InPayload = *r.InPayload
	}
	if comp.Sources == nil {
		comp.Sources = map[string]string{}
	}
	if comp.Files == nil {
		comp.Files = []File{}
	}
	return comp
}

func validate(c *Component, where string, features []string, bad func(string, ...any)) {
	core := strings.HasPrefix(c.ID, "core.")
	if core && c.Switchable {
		bad("%s: core.* components must have switchable = false", where)
	}
	if !core && !c.Switchable {
		bad("%s: only core.* components may have switchable = false", where)
	}
	if !validKind(c.Kind) {
		bad("%s: bad kind %q", where, c.Kind)
	}
	parts := map[string][]string{KindStages: c.Stages, KindProcess: c.Processes, KindLoop: c.Loops}
	switch c.Kind {
	case KindAsset:
		for part, list := range parts {
			if len(list) > 0 {
				bad("%s: an asset has no %s", where, plural(part))
			}
		}
	case KindExtension:
		if len(c.Processes) > 0 {
			bad("%s: an extension has no processes", where)
		}
	default:
		if validKind(c.Kind) {
			for _, part := range []string{KindStages, KindProcess, KindLoop} {
				has := c.HasPart(part)
				if has && len(parts[part]) == 0 {
					bad("%s: kind %q but no %s", where, c.Kind, plural(part))
				}
				if !has && len(parts[part]) > 0 {
					bad("%s: lists %s but kind %q has no %s", where, plural(part), c.Kind, part)
				}
			}
		}
	}
	if c.Kind != KindAsset && (c.Source != "" || len(c.Sources) > 0 || len(c.Files) > 0 || c.InPayload || c.Gated) {
		bad("%s: source, sources, files, in_payload and gated are for assets only", where)
	}
	if !slices.Contains(Groups, c.Group) {
		bad("%s: bad group %q", where, c.Group)
	}
	if want := "components." + c.ID + ".title"; c.TitleKey != want {
		bad("%s: title_key must be %q", where, want)
	}
	if want := "components." + c.ID + ".desc"; c.DescKey != want {
		bad("%s: desc_key must be %q", where, want)
	}
	each := func(field string, list []string, ok func(string) bool) {
		seen := map[string]bool{}
		for _, v := range list {
			if seen[v] {
				bad("%s: %s lists %q twice", where, field, v)
			}
			seen[v] = true
			if !ok(v) {
				bad("%s: bad %s entry %q", where, field, v)
			}
		}
	}
	each("stages", c.Stages, stageRe.MatchString)
	each("processes", c.Processes, processRe.MatchString)
	each("loops", c.Loops, loopRe.MatchString)
	each("requires", c.Requires, func(v string) bool {
		ids := splitRequirement(v)
		if strings.HasPrefix(v, "any:") && len(ids) < 1 {
			return false
		}
		for _, id := range ids {
			if !idRe.MatchString(id) {
				return false
			}
		}
		return len(ids) > 0
	})
	each("wants", c.Wants, idRe.MatchString)
	each("conflicts", c.Conflicts, idRe.MatchString)
	each("settings", c.Settings, settingRe.MatchString)
	each("platforms", c.Platforms, func(v string) bool { return slices.Contains(Platforms, v) })
	each("bundles", c.Bundles, func(v string) bool { return slices.Contains(Bundles, v) })
	each("features", c.Features, func(v string) bool { return slices.Contains(features, v) })
	if len(c.Platforms) == 0 {
		bad("%s: platforms is empty", where)
	}
	for p := range c.Sources {
		if !slices.Contains(Platforms, p) {
			bad("%s: sources has unknown platform %q", where, p)
		}
	}
	for _, f := range c.Files {
		if f.Path == "" || !sha256Re.MatchString(f.SHA256) || f.Size <= 0 {
			bad("%s: a file needs path, sha256 (64 hex) and size", where)
		}
	}
	validateSources(c, where, bad)
	if c.Placement != "any" && c.Placement != "home" {
		bad("%s: bad placement %q", where, c.Placement)
	}
	switch c.Resources.GPU {
	case "", "none", "preferred", "required":
	default:
		bad("%s: bad resources.gpu %q", where, c.Resources.GPU)
	}
	if c.Resources.RAMMB < 0 || c.Resources.DiskMB < 0 {
		bad("%s: resources must not be negative", where)
	}
	if c.Drain.GraceS <= 0 {
		bad("%s: drain.grace_s must be positive", where)
	}
	if c.StopGraceS < 0 || (c.StopGraceS > 0 && !c.HasPart(KindLoop)) {
		bad("%s: stop_grace_s is for loops only", where)
	}
	if c.Licence != "" && !licenceRe.MatchString(c.Licence) {
		bad("%s: bad licence %q", where, c.Licence)
	}
	if !sinceRe.MatchString(c.Since) {
		bad("%s: since must be a release number like 0.3.66, got %q", where, c.Since)
	}
}

// A model's source is pinned (story 51.15): a Hugging Face repo at a commit sha (never a
// branch), an ollama model at its manifest digest. The signed index copies them.
var (
	hfSourceRe     = regexp.MustCompile(`^hf:[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*@[0-9a-f]{40}$`)
	ollamaSourceRe = regexp.MustCompile(`^ollama:[a-z0-9][a-z0-9._/-]*:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}$`)
)

// IsModelSource reports a source the index lists as a model (hf: or ollama:).
func IsModelSource(s string) bool {
	return strings.HasPrefix(s, "hf:") || strings.HasPrefix(s, "ollama:")
}

// validateSources: every hf: and ollama: source an asset uses is pinned and has its files;
// every file belongs to one of the asset's model sources, once per path.
func validateSources(c *Component, where string, bad func(string, ...any)) {
	used := map[string]bool{}
	if c.Source != "" {
		used[c.Source] = true
	}
	for _, s := range c.Sources {
		used[s] = true
	}
	for _, s := range sortedKeysOf(used) {
		switch {
		case strings.HasPrefix(s, "hf:") && !hfSourceRe.MatchString(s):
			bad("%s: source %q must be pinned as hf:<owner>/<repo>@<40-hex commit>, never a branch", where, s)
		case strings.HasPrefix(s, "ollama:") && !ollamaSourceRe.MatchString(s):
			bad("%s: source %q must be pinned as ollama:<name>:<tag>@sha256:<manifest digest>", where, s)
		case IsModelSource(s) && len(c.FilesOf(s)) == 0:
			bad("%s: source %q has no files (tools/release/cmd/pin-models fills them)", where, s)
		}
	}
	paths := map[string]bool{}
	for _, f := range c.Files {
		src := f.Source
		if src == "" {
			src = c.Source
		}
		if !used[src] || !IsModelSource(src) {
			bad("%s: file %q is from %q, not one of its model sources", where, f.Path, src)
		}
		if paths[src+" "+f.Path] {
			bad("%s: file %q listed twice for %q", where, f.Path, src)
		}
		paths[src+" "+f.Path] = true
	}
}

func sortedKeysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func validKind(kind string) bool {
	switch kind {
	case KindAsset, KindExtension:
		return true
	}
	parts := strings.Split(kind, "+")
	order := []string{KindStages, KindProcess, KindLoop}
	at := -1
	for _, p := range parts {
		i := slices.Index(order, p)
		if i <= at {
			return false
		}
		at = i
	}
	return len(parts) > 0
}

func plural(part string) string {
	switch part {
	case KindProcess:
		return "processes"
	case KindLoop:
		return "loops"
	}
	return part
}

func splitRequirement(r string) []string {
	if rest, ok := strings.CutPrefix(r, "any:"); ok {
		var out []string
		for _, id := range strings.Split(rest, "|") {
			if id = strings.TrimSpace(id); id != "" {
				out = append(out, id)
			}
		}
		return out
	}
	return []string{r}
}

// topoOrder is every id, requirements before what requires them, in file order
// otherwise (a depth-first walk of `requires`, both loaders the same way).
func (c *Catalog) topoOrder() ([]string, error) {
	const (
		visiting = 1
		done     = 2
	)
	state := map[string]int{}
	var out []string
	var visit func(id string, path []string) error
	visit = func(id string, path []string) error {
		switch state[id] {
		case done:
			return nil
		case visiting:
			start := slices.Index(path, id)
			return fmt.Errorf("requires cycle: %s", strings.Join(append(slices.Clone(path[start:]), id), " -> "))
		}
		state[id] = visiting
		path = append(path, id)
		for _, r := range c.byID[id].Requires {
			for _, dep := range splitRequirement(r) {
				if err := visit(dep, path); err != nil {
					return err
				}
			}
		}
		state[id] = done
		out = append(out, id)
		return nil
	}
	for _, comp := range c.components {
		if err := visit(comp.ID, nil); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Components are every component, in file order.
func (c *Catalog) Components() []*Component { return slices.Clone(c.components) }

// IDs are every component id, in file order.
func (c *Catalog) IDs() []string {
	out := make([]string, len(c.components))
	for i, comp := range c.components {
		out[i] = comp.ID
	}
	return out
}

// Get is the component with id.
func (c *Catalog) Get(id string) (*Component, bool) {
	comp, ok := c.byID[id]
	return comp, ok
}

// ByStage is the component a pipeline stage belongs to.
func (c *Catalog) ByStage(stage string) (*Component, bool) {
	comp, ok := c.byStage[stage]
	return comp, ok
}

// ByLoop is the component a loop belongs to (host: supervisor, api, streaming, pipeline).
func (c *Catalog) ByLoop(host, name string) (*Component, bool) {
	comp, ok := c.byLoop[host+":"+name]
	return comp, ok
}

// LoopsOf are the loops of host, each with its component, sorted by name.
func (c *Catalog) LoopsOf(host string) map[string]*Component {
	out := map[string]*Component{}
	for key, comp := range c.byLoop {
		if h, name, _ := strings.Cut(key, ":"); h == host {
			out[name] = comp
		}
	}
	return out
}

// ByProcess is the component of a supervised process (role, instance; "" is a role's
// main instance). Several patterns may match: an exact one wins over a prefix ("stt-*"),
// which wins over "*".
func (c *Catalog) ByProcess(role, instance string) (*Component, bool) {
	var best *Component
	bestRank := -1
	for _, comp := range c.components {
		for _, p := range comp.Processes {
			if rank := matchProcess(p, role, instance); rank > bestRank {
				best, bestRank = comp, rank
			}
		}
	}
	return best, best != nil
}

// MatchProcesses are every component with a pattern that matches (role, instance): the
// catalog test wants exactly one.
func (c *Catalog) MatchProcesses(role, instance string) []*Component {
	var out []*Component
	for _, comp := range c.components {
		for _, p := range comp.Processes {
			if matchProcess(p, role, instance) >= 0 {
				out = append(out, comp)
				break
			}
		}
	}
	return out
}

// matchProcess ranks how pattern matches: -1 not at all, 0 "*", 1 a prefix, 2 exactly.
func matchProcess(pattern, role, instance string) int {
	prole, pinst, _ := strings.Cut(pattern, "/")
	switch {
	case prole != role:
		return -1
	case pinst == instance:
		return 2
	case pinst == "*":
		return 0
	case strings.HasSuffix(pinst, "*") && instance != "" && strings.HasPrefix(instance, strings.TrimSuffix(pinst, "*")):
		return 1
	}
	return -1
}

// Requires are id's requirements, each a group of alternatives (an "any:" entry keeps
// its ids together).
func (c *Catalog) Requires(id string) []Requirement {
	comp, ok := c.byID[id]
	if !ok {
		return nil
	}
	out := make([]Requirement, 0, len(comp.Requires))
	for _, r := range comp.Requires {
		out = append(out, Requirement{AnyOf: splitRequirement(r)})
	}
	return out
}

// Dependents are the ids whose requires name id (directly), in file order.
func (c *Catalog) Dependents(id string) []string {
	var out []string
	for _, comp := range c.components {
		for _, r := range comp.Requires {
			if slices.Contains(splitRequirement(r), id) {
				out = append(out, comp.ID)
				break
			}
		}
	}
	return out
}

// Order is every id with requirements first: start in this order, stop in reverse.
func (c *Catalog) Order() []string { return slices.Clone(c.order) }

// IsReason reports whether code is one of the catalog's reason codes.
func (c *Catalog) IsReason(code string) bool { return slices.Contains(c.Reasons, code) }

// IsPhase reports whether p is one of the catalog's phases.
func (c *Catalog) IsPhase(p string) bool { return slices.Contains(c.Phases, p) }

// InBundle are the ids enabled by default in bundle, in Order.
func (c *Catalog) InBundle(bundle string) []string {
	var out []string
	for _, id := range c.order {
		if slices.Contains(c.byID[id].Bundles, bundle) {
			out = append(out, id)
		}
	}
	return out
}

// Dump is the catalog as canonical JSON (sorted keys, two-space indent, a final
// newline): the pipeline's loader answers the same bytes for the same file.
func (c *Catalog) Dump() ([]byte, error) {
	comps := make([]map[string]any, 0, len(c.components))
	for _, comp := range c.components {
		reqs := make([][]string, 0, len(comp.Requires))
		for _, r := range c.Requires(comp.ID) {
			reqs = append(reqs, r.AnyOf)
		}
		comps = append(comps, map[string]any{
			"id": comp.ID, "kind": comp.Kind, "switchable": comp.Switchable, "group": comp.Group,
			"title_key": comp.TitleKey, "desc_key": comp.DescKey, "stages": comp.Stages,
			"processes": comp.Processes, "loops": comp.Loops, "requires": reqs, "wants": comp.Wants,
			"conflicts": comp.Conflicts, "settings": comp.Settings, "platforms": comp.Platforms,
			"placement": comp.Placement, "bundles": comp.Bundles, "features": comp.Features,
			"licence": comp.Licence, "since": comp.Since, "stop_grace_s": comp.StopGraceS,
			"drain":     map[string]any{"grace_s": comp.Drain.GraceS, "checkpoint": comp.Drain.Checkpoint},
			"resources": map[string]any{"ram_mb": comp.Resources.RAMMB, "disk_mb": comp.Resources.DiskMB, "gpu": comp.Resources.GPU},
			"source":    comp.Source, "sources": comp.Sources, "in_payload": comp.InPayload, "gated": comp.Gated,
		})
	}
	doc := map[string]any{"schema": c.Schema, "phases": c.Phases, "reasons": c.Reasons,
		"features": c.Features, "components": comps, "order": c.order}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func joinErrs(errs []string) error {
	return errors.New("catalog: " + strings.Join(errs, "\ncatalog: "))
}
