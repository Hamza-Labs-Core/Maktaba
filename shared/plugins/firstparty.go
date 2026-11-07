package plugins

import (
	"encoding/json"
	"sort"

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
