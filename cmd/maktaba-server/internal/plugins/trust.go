package plugins

import (
	"os"
	"path/filepath"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginsig"
)

// Trust is what this home checks a package's signatures against (story 54.14 §3): the
// embedded plugin root, the newest store-keys.json it can verify (the one the catalog loop
// stored, 54.17, or the release's embedded copy) and the newest versions it has seen
// (<data>/state/plugins-trust.json; the API keeps the same state in Postgres).
type Trust struct {
	// Root is the plugin root (nil: pluginsig.EmbeddedRoot).
	Root *pluginsig.Root
	// StateDir is <data>/state.
	StateDir string
	// Now is the clock (nil: time.Now).
	Now func() time.Time
}

// ListsDir is where the home keeps the signed lists it fetched (54.17): store-keys.json,
// revoked.json, revoked-head.json and the catalogs. Not under <state>/plugins, whose
// entries are the installed plugins' folders.
func ListsDir(stateDir string) string { return filepath.Join(stateDir, "plugins-lists") }

func (t *Trust) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

func (t *Trust) root() (*pluginsig.Root, error) {
	if t.Root != nil {
		return t.Root, nil
	}
	return pluginsig.EmbeddedRoot()
}

// State is the newest-seen state of the file copy.
func (t *Trust) State() pluginsig.State {
	st, _ := pluginsig.LoadState(pluginsig.TrustFile(t.StateDir))
	return st
}

// StoreKeys answers the newest store-keys.json that verifies against the root and the
// state (nil when there is none: the root is a placeholder, or no list was ever fetched
// and the release carries none). A list that no longer verifies (expired) counts as none.
func (t *Trust) StoreKeys() *pluginsig.StoreKeys {
	root, err := t.root()
	if err != nil || root.Empty() {
		return nil
	}
	st := t.State()
	var best *pluginsig.StoreKeys
	candidates := [][]byte{pluginsig.EmbeddedStoreKeys()}
	if b, err := os.ReadFile(filepath.Join(ListsDir(t.StateDir), "store-keys.json")); err == nil {
		candidates = append(candidates, b)
	}
	for _, raw := range candidates {
		if len(raw) == 0 {
			continue
		}
		// an older copy than the newest seen is refused by VerifyStoreKeys (anti-rollback)
		k, err := root.VerifyStoreKeys(raw, st, t.now())
		if err == nil && (best == nil || k.Version > best.Version) {
			best = k
		}
	}
	if best != nil {
		st.AcceptStoreKeys(best)
		_ = pluginsig.SaveState(pluginsig.TrustFile(t.StateDir), st)
	}
	return best
}
