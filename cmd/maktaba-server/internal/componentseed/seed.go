// Package componentseed writes the components' desired state once, from what a home runs
// today (Epic 51, story 51.03): an update to the first release with the component model
// must leave every home running exactly what it ran before.
//
// The home's supervisor seeds at boot, after the database is migrated and before any role
// starts: it is the one process that sees server.toml, the payload and the database at
// once. The seed runs once (component_meta.seeded_at, store.Seed). From then on the
// database decides; the server.toml keys it imported are only compared at each boot, and
// a disagreement is logged (toml_key_superseded) and reported, never acted on.
//
// A joined machine seeds nothing at home level: it writes overrides for itself only
// where its own server.toml differs from the home (NodeOverrides).
package componentseed

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Bundle is what every Phase 1 seed is: today's Maktaba (README H8). Setup's choice of
// bundle comes with story 51.17.
const Bundle = "full"

// Facts are what the home runs today, as the seed reads them.
type Facts struct {
	Config config.Config

	// the machine's (the supervisor decides them at boot)
	Slots      bool            // the TV slots run (supervisor.SlotsEnabled)
	LLMProcess bool            // serveLLM built the ollama process
	Relay      bool            // supervisor.RelayWanted
	Assets     map[string]bool // asset id -> on disk now (Assets)

	// the database's (ReadDB)
	OperatorOff map[string]bool // stage -> turned off on the Queues page (disabled_reason = operator)
	Extensions  map[string]bool // extensions.name -> enabled
	HDHR        bool            // hdhr_device.enabled

	// Choice is setup's answer to "Choose what to run" (story 51.17, ReadChoice); nil is
	// Full, today's switches.
	Choice *Choice
}

// BundleOf is the bundle a seed with f writes: setup's choice, else Full.
func BundleOf(f Facts) string {
	if f.Choice != nil && f.Choice.Bundle != "" {
		return f.Choice.Bundle
	}
	return Bundle
}

// alwaysOn are the switchable components whose loops always run today.
var alwaysOn = map[string]bool{"cluster": true, "queue_health": true, "analytics": true, "updates.auto": true}

func want(on bool) string {
	if on {
		return components.WantEnabled
	}
	return components.WantInstalled
}

// Plan is the want of every component of cat from what runs today (story 51.03 §2). A
// component the rules here do not name (one a later release adds) is enabled when the
// Full bundle has it, else installed.
func Plan(cat *components.Catalog, f Facts) map[string]string {
	cfg := f.Config
	out := map[string]string{}
	for _, c := range cat.Components() {
		var w string
		switch {
		case !c.Switchable, alwaysOn[c.ID]:
			w = components.WantEnabled
		case c.Kind == components.KindAsset:
			// on disk: installed; assets have no run, so never enabled
			if f.Assets[c.ID] {
				w = components.WantInstalled
			} else {
				w = components.WantAbsent
			}
		case c.Kind == components.KindExtension:
			w = want(f.Extensions[strings.TrimPrefix(c.ID, "ext.")])
		case c.ID == "livetv.slots":
			w = want(f.Slots)
		case c.ID == "livetv.inprocess":
			w = want(!f.Slots)
		case c.ID == "livetv.hdhomerun":
			w = want(f.HDHR)
		case c.ID == "llm":
			w = want(f.LLMProcess || strings.TrimSpace(cfg.LLM.URL) != "")
		case c.ID == "cloud.link", c.ID == "cloud.relay":
			w = want(f.Relay)
		case c.ID == "discovery.mdns":
			w = want(cfg.Server.MDNSOn())
		case c.ID == "backups":
			w = want(cfg.Backups.NightlyOn())
		case c.ID == "telemetry":
			w = want(cfg.Telemetry.On())
		case c.HasPart(components.KindStages) && len(c.Stages) > 0:
			// a stage group: off only when an operator turned every one of its stages off
			on := false
			for _, s := range c.Stages {
				if !f.OperatorOff[s] {
					on = true
					break
				}
			}
			w = want(on)
		default:
			full := false
			for _, b := range c.Bundles {
				full = full || b == Bundle
			}
			w = want(full)
		}
		out[c.ID] = w
	}
	keepRequirementsOn(cat, out)
	keepAssetsWanted(cat, out)
	return out
}

// keepAssetsWanted seeds installed (wanted on this home) the asset an enabled component
// needs when none of its requirement is on disk yet: today the stage runs and fetches its
// runtime or model on first use (a fresh home, one that never transcribed), so seeding it
// absent would turn the stage off. The first of an "any:" group that runs here is taken.
func keepAssetsWanted(cat *components.Catalog, out map[string]string) {
	platform := Platform()
	for _, c := range cat.Components() {
		if out[c.ID] != components.WantEnabled {
			continue
		}
		for _, r := range cat.Requires(c.ID) {
			pick := ""
			met := false
			for _, id := range r.AnyOf {
				dep, ok := cat.Get(id)
				if !ok || dep.Kind != components.KindAsset {
					met = true // not an asset: keepRequirementsOn's rule
					break
				}
				if out[id] != components.WantAbsent {
					met = true
					break
				}
				if pick == "" && dep.RunsOn(platform) {
					pick = id
				}
			}
			if !met && pick != "" {
				out[pick] = components.WantInstalled
			}
		}
	}
}

// keepRequirementsOn enables what an enabled component requires, so nothing that runs
// today waits on a requirement the rules above turned off (every livetv stage turned off
// on the Queues page while the slots run: livetv's loops run today too). Assets and
// "any:" groups are left as they are: an asset is never enabled.
func keepRequirementsOn(cat *components.Catalog, out map[string]string) {
	for changed := true; changed; {
		changed = false
		for _, c := range cat.Components() {
			if out[c.ID] != components.WantEnabled {
				continue
			}
			for _, r := range cat.Requires(c.ID) {
				if len(r.AnyOf) != 1 {
					continue
				}
				dep, ok := cat.Get(r.AnyOf[0])
				if !ok || dep.Kind == components.KindAsset || out[dep.ID] != components.WantInstalled {
					continue
				}
				out[dep.ID] = components.WantEnabled
				changed = true
			}
		}
	}
}

// ReadDB reads the database's facts: the stages an operator turned off, the extensions
// and the HDHomeRun device. A table an older schema has not got reads as empty.
func ReadDB(ctx context.Context, q store.Queryer, f *Facts) error {
	f.OperatorOff, f.Extensions = map[string]bool{}, map[string]bool{}
	rows, err := q.QueryContext(ctx, `SELECT stage FROM job_stages WHERE disabled_reason = 'operator'`)
	if err != nil {
		return fmt.Errorf("job_stages: %w", err)
	}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			_ = rows.Close()
			return err
		}
		f.OperatorOff[s] = true
	}
	if err := closeRows(rows); err != nil {
		return err
	}
	rows, err = q.QueryContext(ctx, `SELECT name, enabled FROM extensions`)
	if err != nil {
		return fmt.Errorf("extensions: %w", err)
	}
	for rows.Next() {
		var name string
		var on bool
		if err := rows.Scan(&name, &on); err != nil {
			_ = rows.Close()
			return err
		}
		f.Extensions[name] = on
	}
	if err := closeRows(rows); err != nil {
		return err
	}
	err = q.QueryRowContext(ctx, `SELECT enabled FROM hdhr_device WHERE id = 1`).Scan(&f.HDHR)
	if errors.Is(err, sql.ErrNoRows) {
		f.HDHR, err = false, nil
	}
	if err != nil {
		return fmt.Errorf("hdhr_device: %w", err)
	}
	return nil
}

func closeRows(rows *sql.Rows) error {
	err := rows.Err()
	if cerr := rows.Close(); err == nil {
		err = cerr
	}
	return err
}

// Seed seeds the home once: the database's facts read in the seed's own transaction, the
// machine's in f. It answers false when the home was seeded already.
func Seed(ctx context.Context, st *store.Store, cat *components.Catalog, f Facts, release string) (bool, error) {
	return st.Seed(ctx, func(ctx context.Context, q store.Queryer) (store.SeedPlan, error) {
		facts := f
		if err := ReadDB(ctx, q, &facts); err != nil {
			return store.SeedPlan{}, err
		}
		wants := Plan(cat, facts)
		if f.Choice != nil && f.Choice.Bundle != Bundle {
			wants = PlanChoice(cat, facts, *f.Choice)
		}
		return store.SeedPlan{Wants: wants, Bundle: BundleOf(f), Release: release,
			TomlImported: Imported(f.Config)}, nil
	})
}
