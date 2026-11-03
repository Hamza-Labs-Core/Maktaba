package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// The last desired state the reconciler saw, kept in <state root>/state/components.json (story 51.04
// §4): at boot, before the database answers, the gates are set from it, so a held
// component does not start for a minute after an update and stop again. The database's
// answer always replaces it (a cache from a newer revision than a restored database
// loses too).

// CompCacheFile is the cache's name in the state directory.
const CompCacheFile = "components.json"

// CompCachePath is the cache's path under the state root (StateDir): beside setup.json in
// its state folder.
func CompCachePath(stateDir string) string { return filepath.Join(stateDir, "state", CompCacheFile) }

// compCache is the file's content.
type compCache struct {
	Revision  int64                    `json:"revision"`
	WrittenAt time.Time                `json:"written_at"`
	Machine   string                   `json:"machine"`
	Bundle    string                   `json:"bundle,omitempty"`
	Desired   map[string]cachedDesired `json:"desired"`
}

type cachedDesired struct {
	Want       string      `json:"want"`
	Hold       *store.Hold `json:"hold,omitempty"`
	Generation int64       `json:"generation"`
}

// readCompCache reads the cache at path. ok is false when there is none, or it is another
// machine's (this machine joined or left a home since), or it cannot be read (said in err).
func readCompCache(path, machine string) (map[string]store.Desired, int64, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	var c compCache
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, 0, false, fmt.Errorf("%s: %w", path, err)
	}
	if c.Machine != machine {
		return nil, 0, false, nil
	}
	out := make(map[string]store.Desired, len(c.Desired))
	for id, d := range c.Desired {
		out[id] = store.Desired{ID: id, Want: d.Want, Hold: d.Hold, Generation: d.Generation}
	}
	return out, c.Revision, true, nil
}

// writeCompCache writes the cache at path atomically.
func writeCompCache(path, machine, bundle string, desired map[string]store.Desired, rev int64, now time.Time) error {
	c := compCache{Revision: rev, WrittenAt: now.UTC(), Machine: machine, Bundle: bundle,
		Desired: make(map[string]cachedDesired, len(desired))}
	for id, d := range desired {
		c.Desired[id] = cachedDesired{Want: d.Want, Hold: d.Hold, Generation: d.Generation}
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return layout.WriteFileAtomic(path, append(b, '\n'), 0o600)
}

// BootConfig is cfg with the keys that decide which processes are built (story 51.04)
// taken from the last desired state the reconciler saw (the cache: the database is not up
// yet), where it disagrees with server.toml: the database wins (story 51.03). Each key
// changed is named in changed ("tv.slots=on"). With no cache for machine, or with
// [components] reconcile = "off", cfg is returned as it is.
//
//	livetv.slots enabled (held or not) -> [tv] slots = "on"
//	livetv.slots off, livetv.inprocess enabled -> [tv] slots = "off" (the in-process engine)
//	llm enabled                        -> [llm] enabled = "on" (the bundled ollama is built)
//	cloud.link enabled                 -> [cloud] enabled = true (the relay agent is built)
//
// A component turned off whose process server.toml still builds is closed by its gate
// (Allow), not here.
func BootConfig(cfg config.Config, machine string) (config.Config, []string) {
	if !cfg.Components.ReconcileOn() {
		return cfg, nil
	}
	desired, _, ok, err := readCompCache(CompCachePath(cfg.StateDir()), machine)
	if err != nil || !ok {
		return cfg, nil
	}
	var changed []string
	if d, has := desired["livetv.slots"]; has {
		in := desired["livetv.inprocess"]
		switch {
		case d.Want == components.WantEnabled && !cfg.TV.SlotsOn():
			cfg.TV.Slots = "on"
			changed = append(changed, "tv.slots=on")
		case d.Want != components.WantEnabled && in.Want == components.WantEnabled && cfg.TV.SlotsOn():
			cfg.TV.Slots = "off"
			changed = append(changed, "tv.slots=off")
		}
	}
	if d, has := desired["llm"]; has && d.Want == components.WantEnabled &&
		strings.EqualFold(strings.TrimSpace(cfg.LLM.Enabled), "off") {
		cfg.LLM.Enabled = "on"
		changed = append(changed, "llm.enabled=on")
	}
	if d, has := desired["cloud.link"]; has && d.Want == components.WantEnabled && !cfg.Cloud.Enabled {
		cfg.Cloud.Enabled = true
		changed = append(changed, "cloud.enabled=true")
	}
	return cfg, changed
}

// FileKeys puts back into cfg the keys BootConfig may change, as file has them (the
// seed's comparison with server.toml reads the file's own values).
func FileKeys(cfg, file config.Config) config.Config {
	cfg.TV.Slots, cfg.LLM.Enabled, cfg.Cloud.Enabled = file.TV.Slots, file.LLM.Enabled, file.Cloud.Enabled
	return cfg
}
