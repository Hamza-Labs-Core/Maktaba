package componentseed

import (
	"context"
	"sort"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Part is the component_status part these rows are reported under: what the home's
// configuration says against the database (server.toml keys superseded, environment
// variables and boot flags that win for this boot). It is a part of its own so the
// supervisor's reconciler (51.04), whose report on part "supervisor" is that part's whole
// picture, neither prunes these rows nor is pruned by them.
const Part = "config"

// CondConfigured is the condition a superseded server.toml key sets to false.
const CondConfigured = "Configured"

// Note is one thing the configuration says of a component for this boot.
type Note struct {
	ID     string
	Reason string // env_override | boot_flag
	Params map[string]any
}

// envOverrides are the environment variables that win at runtime over desired state
// (story 51.03 §4): never imported (seeding them would freeze a test's setting into the
// database), reported while they are set.
var envOverrides = []struct {
	Name, Component string
	Set             func(v string) bool
}{
	{"MAKTABA_UPDATE_DISABLE", "updates.auto", func(v string) bool { return v == "1" || strings.EqualFold(v, "true") }},
	{"MAKTABA_CLUSTER_AGENT", "cluster", offValue},
	{"MAKTABA_DISCOVERY_MDNS", "discovery.mdns", offValue},
	{"MAKTABA_PIPELINE_SEMANTIC", "search.semantic", func(v string) bool { return strings.EqualFold(strings.TrimSpace(v), "off") }},
}

// offValue: 0, false, no or off (how those processes read their switch).
func offValue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "no", "off":
		return true
	}
	return false
}

// EnvNotes are the overrides set in getenv's environment.
func EnvNotes(getenv func(string) string) []Note {
	var out []Note
	for _, e := range envOverrides {
		if v := getenv(e.Name); v != "" && e.Set(v) {
			out = append(out, Note{ID: e.Component, Reason: "env_override", Params: map[string]any{"variable": e.Name}})
		}
	}
	return out
}

// BootNotes are the command line's overrides of this boot (serve --no-mdns).
func BootNotes(noMDNS bool) []Note {
	if noMDNS {
		return []Note{{ID: "discovery.mdns", Reason: "boot_flag", Params: map[string]any{"flag": "--no-mdns"}}}
	}
	return nil
}

// Statuses are the config part's rows: one per component a note or finding names. An
// override (env_override, boot_flag) of a component that should run makes it degraded;
// a superseded key only says so (the component runs as the database says).
func Statuses(desired map[string]store.Desired, found []Finding, notes []Note) []store.Status {
	byID := map[string]*store.Status{}
	get := func(id string) *store.Status {
		if s, ok := byID[id]; ok {
			return s
		}
		d := desired[id]
		s := &store.Status{ID: id, ObservedGeneration: d.Generation, Phase: phaseOf(d.Want)}
		byID[id] = s
		return s
	}
	for _, n := range notes {
		s := get(n.ID)
		s.Conditions = append(s.Conditions, store.Condition{Type: components.CondReady, Status: "false",
			Reason: n.Reason, Params: n.Params, Generation: s.ObservedGeneration})
		if desired[n.ID].Want == components.WantEnabled || desired[n.ID].Want == "" {
			s.Phase = components.PhaseDegraded
		}
	}
	for _, f := range found {
		s := get(f.Component)
		s.Conditions = append(s.Conditions, store.Condition{Type: CondConfigured, Status: "false",
			Reason: "toml_superseded", Generation: s.ObservedGeneration,
			Params: map[string]any{"key": f.Key, "value": f.Value, "line": f.Line, "file_says": f.Says}})
	}
	out := make([]store.Status, 0, len(byID))
	for _, s := range byID {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func phaseOf(want string) string {
	switch want {
	case components.WantInstalled:
		return components.PhaseOff
	case components.WantAbsent:
		return components.PhaseNotInstalled
	}
	return components.PhaseRunning
}

// Report writes the config part's rows for machine (its whole picture: rows no longer
// named are removed, so a key put right in server.toml clears at the next boot).
func Report(ctx context.Context, st *store.Store, machine string, desired map[string]store.Desired,
	found []Finding, notes []Note) error {
	return st.ReportStatus(ctx, machine, Part, Statuses(desired, found, notes))
}
