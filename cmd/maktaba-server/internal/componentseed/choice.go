package componentseed

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// Setup's answer to "Choose what to run" (story 51.17): the bundle, kept in the state
// folder until the database's first seed reads it instead of today's switches. No file
// is Full (README H8): every install before this story, and every one set up without
// the question, seeds exactly as before.
//
// It is a file of its own, not the reconciler's components.json cache: the reconciler may
// write its cache from an empty, unseeded database before the seed runs, which would lose
// the answer.

// ChoiceFile is the answer's name in <state>/state.
const ChoiceFile = "bundle.json"

// ChoicePath is the answer's path under the state root.
func ChoicePath(stateDir string) string { return filepath.Join(stateDir, "state", ChoiceFile) }

// Choice is setup's answer.
type Choice struct {
	Bundle string `json:"bundle"` // full | core | worker | custom
	// Base and Enable are a custom bundle's: the bundle it starts from (core or full) and
	// the components the admin ticked.
	Base   string   `json:"base,omitempty"`
	Enable []string `json:"enable,omitempty"`
	// Enabled is the whole set as setup worked it out (requirements included), in
	// catalog order; the seed works it out again from the catalog it runs.
	Enabled       []string  `json:"enabled"`
	DownloadBytes int64     `json:"download_bytes"`
	ChosenAt      time.Time `json:"chosen_at"`
}

// Set is the choice's set on platform from cat (Enabled when the catalog cannot make it
// any more: an id a later release dropped).
func (c Choice) Set(cat *components.Catalog, platform string) []string {
	set, err := cat.BundleSet(c.Bundle, c.Base, c.Enable, platform)
	if err != nil {
		return c.Enabled
	}
	return set
}

// Has reports whether the choice's set holds id.
func (c Choice) Has(id string) bool { return slices.Contains(c.Enabled, id) }

// VenvExtras are the pipeline extras the venv is built with for the choice: Full and
// Worker keep the bootstrap's default (as today: extras "" and none false); a choice
// without the ML runtime or yt-dlp builds with none (none true: constraints-core.txt).
func (c Choice) VenvExtras() (extras string, none bool) {
	if c.Bundle == "full" || c.Bundle == "worker" || c.Bundle == "" {
		return "", false
	}
	var out []string
	if c.Has("runtime.ml") {
		out = append(out, "stt", "search")
	}
	if c.Has("tool.yt-dlp") {
		out = append(out, "mediaintel")
	}
	if len(out) == 0 {
		return "", true
	}
	return strings.Join(out, ","), false
}

// ReadChoice reads setup's answer under stateDir; ok is false when there is none.
func ReadChoice(stateDir string) (Choice, bool, error) {
	raw, err := os.ReadFile(ChoicePath(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return Choice{}, false, nil
	}
	if err != nil {
		return Choice{}, false, err
	}
	var c Choice
	if err := json.Unmarshal(raw, &c); err != nil {
		return Choice{}, false, fmt.Errorf("%s: %w", ChoicePath(stateDir), err)
	}
	if !slices.Contains(components.SetupBundles, c.Bundle) {
		return Choice{}, false, fmt.Errorf("%s: no bundle %q", ChoicePath(stateDir), c.Bundle)
	}
	return c, true, nil
}

// WriteChoice writes the answer atomically (0600, the state folder made when missing).
func WriteChoice(stateDir string, c Choice) error {
	path := ChoicePath(stateDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if c.Enabled == nil {
		c.Enabled = []string{}
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return layout.WriteFileAtomic(path, append(b, '\n'), 0o600)
}

// venvAssets are the assets today's disk check reads from the pipeline's venv: a Core
// venv has its Python too, so for a choice they follow the choice's set.
var venvAssets = map[string]bool{"runtime.ml": true, "model.refine": true, "tool.yt-dlp": true}

// PlanChoice is the first desired state of a home whose setup chose c (not Full): what the
// choice's set holds is on, the rest off. What the owner turned on outside the bundle
// (an extension row, telemetry's opt-in, the cloud link) keeps today's rule, and an asset
// on disk outside the set (the Full dmg's ollama) is installed, not absent.
func PlanChoice(cat *components.Catalog, f Facts, c Choice) map[string]string {
	platform := Platform()
	out := cat.SetupWants(c.Set(cat, platform))
	today := Plan(cat, Facts{Config: f.Config, Slots: f.Slots, LLMProcess: f.LLMProcess, Relay: f.Relay,
		Assets: f.Assets, OperatorOff: f.OperatorOff, Extensions: f.Extensions, HDHR: f.HDHR})
	for _, comp := range cat.Components() {
		switch {
		case comp.Kind == components.KindExtension, comp.ID == "telemetry", comp.ID == "cloud.link", comp.ID == "cloud.relay":
			if today[comp.ID] == components.WantEnabled {
				out[comp.ID] = components.WantEnabled
			}
		case comp.Kind == components.KindAsset && !venvAssets[comp.ID] && f.Assets[comp.ID] && out[comp.ID] == components.WantAbsent:
			out[comp.ID] = components.WantInstalled
		}
	}
	keepRequirementsOn(cat, out)
	return out
}
