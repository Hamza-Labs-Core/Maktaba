package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/componentseed"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// The setup step `bundle` (Epic 51 story 51.17): "Choose what to run". With check_only it
// answers the four bundles, what each turns on, what it downloads and whether this
// machine runs it well; with a bundle it writes setup's answer (componentseed.Choice),
// which the database's first seed reads instead of today's switches, and which the venv,
// models and llm steps follow. Errors: bad_input, unknown_component, platform_unsupported.

// bundleFullRAM is the memory Full runs well with (transcription and the language model
// beside the rest): below it Full is allowed with a warning.
const bundleFullRAM = 16 << 30

// bundleBaseDisk is the disk any bundle needs besides its downloads: the database to
// start with and the caches.
const bundleBaseDisk int64 = 2 << 30

type bundleItem struct {
	ID       string `json:"id"`
	TitleKey string `json:"title_key"`
	Kind     string `json:"kind"`
}

type bundleOption struct {
	ID            string       `json:"id"`
	Enables       []bundleItem `json:"enables"`
	DownloadBytes int64        `json:"download_bytes"`
	DiskBytes     int64        `json:"disk_bytes"`
	Runs          bool         `json:"runs"`
	Warnings      []checkNote  `json:"warnings"`
	NeedsModels   bool         `json:"needs_models"`
	NeedsLLM      bool         `json:"needs_llm"`
}

type bundleCheck struct {
	Bundles []bundleOption `json:"bundles"`
	// Preselected is the payload's flavour: a Core dmg preselects Core, every other
	// payload Full.
	Preselected string `json:"preselected"`
	Flavour     string `json:"flavour"`
	RAMBytes    uint64 `json:"ram_bytes"`
	// Chosen is the answer saved before (a screen gone back to), or "".
	Chosen *componentseed.Choice `json:"chosen,omitempty"`
}

type bundleResult struct {
	Bundle        string   `json:"bundle"`
	Enabled       []string `json:"enabled"`
	DownloadBytes int64    `json:"download_bytes"`
	NeedsModels   bool     `json:"needs_models"`
	NeedsLLM      bool     `json:"needs_llm"`
	VenvExtras    string   `json:"venv_extras"`
	VenvCore      bool     `json:"venv_core"`
}

// bundlePlatform is the catalog platform setup plans for (tests replace it).
var bundlePlatform = components.ThisPlatform

// payloadFlavour is the payload's flavour ("full" or "core"; a payload from before story
// 51.17, or none, is full).
func payloadFlavour() string {
	dir, err := setupPayload()
	if err != nil {
		return "full"
	}
	return flavourOf(dir)
}

func flavourOf(payload string) string {
	var p struct {
		Flavour string `json:"flavour"`
	}
	if raw, err := os.ReadFile(filepath.Join(payload, "payload.json")); err == nil && json.Unmarshal(raw, &p) == nil &&
		strings.TrimSpace(p.Flavour) == "core" {
		return "core"
	}
	return "full"
}

// bundleDownload is what a set downloads at setup on platform: the venv's base wheels,
// and the assets the payload does not carry.
func bundleDownload(cat *components.Catalog, set []string, platform string) int64 {
	n := components.CoreVenvBytes
	for _, id := range set {
		if c, ok := cat.Get(id); ok {
			n += c.AssetBytes(platform)
		}
	}
	return n
}

func needsModels(cat *components.Catalog, set []string) bool {
	return slices.ContainsFunc(set, func(id string) bool {
		c, _ := cat.Get(id)
		return c.Kind == components.KindAsset && strings.HasPrefix(id, "model.") && !strings.HasPrefix(id, "model.llm.")
	})
}

func stepBundle(s *setupRun, in json.RawMessage) (any, error) {
	var req struct {
		CheckOnly bool     `json:"check_only"`
		Bundle    string   `json:"bundle"`
		Base      string   `json:"base"`
		Enable    []string `json:"enable"`
	}
	if err := decodeInput(in, &req); err != nil {
		return nil, err
	}
	cat := components.Default()
	platform := bundlePlatform()
	if req.CheckOnly {
		ram, _ := hostFacts(s.cfg.StateDir())
		flavour := payloadFlavour()
		res := bundleCheck{Preselected: flavour, Flavour: flavour, RAMBytes: ram}
		if ch, ok, err := componentseed.ReadChoice(s.cfg.StateDir()); err == nil && ok {
			res.Chosen = &ch
		}
		for _, b := range components.SetupBundles {
			base := ""
			if b == components.BundleCustom {
				base = "core"
			}
			set, err := cat.BundleSet(b, base, nil, platform)
			if err != nil {
				return nil, err
			}
			opt := bundleOption{ID: b, Enables: []bundleItem{}, Runs: true, Warnings: []checkNote{},
				NeedsModels: needsModels(cat, set), NeedsLLM: slices.Contains(set, "llm")}
			for _, id := range set {
				c, _ := cat.Get(id)
				if c.Switchable {
					opt.Enables = append(opt.Enables, bundleItem{ID: id, TitleKey: c.TitleKey, Kind: c.Kind})
				}
			}
			opt.DownloadBytes = bundleDownload(cat, set, platform)
			opt.DiskBytes = opt.DownloadBytes + bundleBaseDisk
			if ram > 0 && ram < bundleFullRAM && (slices.Contains(set, "transcription") || slices.Contains(set, "llm")) {
				opt.Warnings = append(opt.Warnings, newNote("low_memory",
					"This computer has "+humanBytes(ram)+". Full works best with 16 GB or more.",
					"ram_bytes", ram, "need_bytes", uint64(bundleFullRAM)))
			}
			res.Bundles = append(res.Bundles, opt)
		}
		return res, nil
	}
	if strings.TrimSpace(req.Bundle) == "" {
		return nil, &stepError{code: "bad_input", message: "give check_only, or the bundle to run", field: "bundle", usage: true}
	}
	set, err := cat.BundleSet(req.Bundle, req.Base, req.Enable, platform)
	var be *components.BundleError
	switch {
	case errors.As(err, &be) && be.Code == "unknown_component":
		return nil, failStep("unknown_component", "enable", "%s", be.Msg).with("id", be.ID)
	case errors.As(err, &be) && be.Code == "platform_unsupported":
		return nil, failStep("platform_unsupported", "enable", "%s", be.Msg).with("id", be.ID, "platform", platform)
	case errors.As(err, &be):
		return nil, &stepError{code: "bad_input", message: be.Msg, field: "bundle", usage: true}
	case err != nil:
		return nil, err
	}
	ch := componentseed.Choice{Bundle: req.Bundle, Enabled: set, DownloadBytes: bundleDownload(cat, set, platform),
		ChosenAt: time.Now().UTC()}
	if req.Bundle == components.BundleCustom {
		ch.Base, ch.Enable = req.Base, req.Enable
		if ch.Base == "" {
			ch.Base = "core"
		}
	}
	if err := componentseed.WriteChoice(s.cfg.StateDir(), ch); err != nil {
		return nil, err
	}
	extras, none := ch.VenvExtras()
	return bundleResult{Bundle: ch.Bundle, Enabled: set, DownloadBytes: ch.DownloadBytes,
		NeedsModels: needsModels(cat, set), NeedsLLM: slices.Contains(set, "llm"), VenvExtras: extras, VenvCore: none}, nil
}

// choiceVenvSpec are the extras setup's venv step builds with: setup's answer's (no
// answer: today's default).
func choiceVenvSpec(stateDir string) (extras string, none bool) {
	ch, ok, err := componentseed.ReadChoice(stateDir)
	if err != nil || !ok {
		return "", false
	}
	return ch.VenvExtras()
}

// setupBundleFlag is `setup --bundle <name>`: the bundle step's answer, from the command
// line (custom takes no ticks here: it starts from Core; Settings > Components adds them).
func setupBundleFlag(bundle string, out io.Writer) error {
	cfg, found, err := config.Load()
	if err != nil {
		return err
	}
	s := &setupRun{ctx: context.Background(), cfg: cfg, found: found, out: json.NewEncoder(io.Discard), log: io.Discard}
	raw, _ := json.Marshal(map[string]any{"bundle": bundle})
	res, err := stepBundle(s, raw)
	if err != nil {
		return err
	}
	r := res.(bundleResult)
	fmt.Fprintf(out, "Bundle: %s (%d components, downloads about %s)\n", r.Bundle, len(r.Enabled), humanBytes(uint64(r.DownloadBytes)))
	return nil
}
