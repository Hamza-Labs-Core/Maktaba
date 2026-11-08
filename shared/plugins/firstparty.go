package plugins

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// FirstPartyGroups answers the runner groups the embedded first-party plugins need
// (story 54.23 §4): each manifest's [trusted] runner (or a stage's own), "own" named
// after the plugin's folder, and "fp" whenever there is any plugin. None when the
// bundle has no plugin: no runner runs.
func FirstPartyGroups() []string {
	var doc struct {
		Plugins []struct {
			Folder   string `json:"folder"`
			Manifest string `json:"manifest"`
		} `json:"plugins"`
	}
	if json.Unmarshal(FirstParty, &doc) != nil || len(doc.Plugins) == 0 {
		return nil
	}
	seen := map[string]bool{"fp": true}
	for _, p := range doc.Plugins {
		var m struct {
			Trusted struct {
				Runner string `toml:"runner"`
			} `toml:"trusted"`
			Stage []struct {
				Runner string `toml:"runner"`
			} `toml:"stage"`
		}
		if _, err := toml.Decode(p.Manifest, &m); err != nil {
			continue
		}
		group := func(g string) string {
			switch g {
			case "":
				return "light"
			case "own":
				return p.Folder
			}
			return g
		}
		seen[group(m.Trusted.Runner)] = true
		for _, s := range m.Stage {
			if s.Runner != "" {
				seen[group(s.Runner)] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for g := range seen {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

// FirstPartyGroupComponents answers, per runner group, the catalog components whose
// first-party plugin has stages in it (each manifest's [trusted] component, for its
// [trusted] runner and each stage's own). The supervisor keeps a shared group's runner held
// while none of them is on (Epic 54: an idle home runs no runner a component of its does
// not need). The fingerprint group ("fp") serves every first-party component.
func FirstPartyGroupComponents() map[string][]string {
	var doc struct {
		Plugins []struct {
			Folder   string `json:"folder"`
			Manifest string `json:"manifest"`
		} `json:"plugins"`
	}
	if json.Unmarshal(FirstParty, &doc) != nil {
		return nil
	}
	seen := map[string]map[string]bool{}
	add := func(group, comp string) {
		if comp == "" {
			return
		}
		if seen[group] == nil {
			seen[group] = map[string]bool{}
		}
		seen[group][comp] = true
	}
	for _, p := range doc.Plugins {
		var m struct {
			Trusted struct {
				Runner    string `toml:"runner"`
				Component string `toml:"component"`
			} `toml:"trusted"`
			Stage []struct {
				Runner string `toml:"runner"`
			} `toml:"stage"`
		}
		if _, err := toml.Decode(p.Manifest, &m); err != nil {
			continue
		}
		group := func(g string) string {
			switch g {
			case "":
				return "light"
			case "own":
				return p.Folder
			}
			return g
		}
		comp := m.Trusted.Component
		add(group(m.Trusted.Runner), comp)
		for _, s := range m.Stage {
			if s.Runner != "" {
				add(group(s.Runner), comp)
			}
		}
		add("fp", comp)
	}
	out := make(map[string][]string, len(seen))
	for g, cs := range seen {
		for c := range cs {
			out[g] = append(out[g], c)
		}
		sort.Strings(out[g])
	}
	return out
}

// FirstPartyStageGroups answers each first-party stage's runner group: the stage's own
// runner, else its plugin's [trusted] runner ("" is light, "own" the plugin's folder), as
// the pipeline's firstparty.runner_group.
func FirstPartyStageGroups() map[string]string {
	var doc struct {
		Plugins []struct {
			Folder   string `json:"folder"`
			Manifest string `json:"manifest"`
		} `json:"plugins"`
	}
	if json.Unmarshal(FirstParty, &doc) != nil {
		return nil
	}
	out := map[string]string{}
	for _, p := range doc.Plugins {
		var m struct {
			Trusted struct {
				Runner string `toml:"runner"`
			} `toml:"trusted"`
			Stage []struct {
				Name   string `toml:"name"`
				Runner string `toml:"runner"`
			} `toml:"stage"`
		}
		if _, err := toml.Decode(p.Manifest, &m); err != nil {
			continue
		}
		for _, s := range m.Stage {
			g := s.Runner
			if g == "" {
				g = m.Trusted.Runner
			}
			switch g {
			case "":
				g = "light"
			case "own":
				g = p.Folder
			}
			out[s.Name] = g
		}
	}
	return out
}

// RunnerGroupOf answers the group of a runner instance: a group with one runner per
// transcription slot names its instances <group>-<n> (ml-3); every other instance is its
// group.
func RunnerGroupOf(instance string) string {
	if g, n, ok := strings.Cut(instance, "-"); ok && n != "" && strings.Trim(n, "0123456789") == "" {
		return g
	}
	return instance
}
