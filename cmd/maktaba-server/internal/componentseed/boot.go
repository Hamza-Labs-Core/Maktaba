package componentseed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Home is the home's boot step: the seed (once), then this boot's comparison of
// server.toml with the database and the config part's status rows.
type Home struct {
	Store    *store.Store
	Catalog  *components.Catalog // nil: components.Default()
	Facts    Facts               // the machine's facts (the database's are read in the seed)
	Release  string
	TomlPath string // the server.toml this boot read ("" or missing: nothing compared)
	Getenv   func(string) string
	NoMDNS   bool
	Log      io.Writer
}

// Run seeds when the home has not been seeded, then compares and reports. Call it once
// per boot: every finding is logged each time it runs.
func (h Home) Run(ctx context.Context) error {
	cat := h.Catalog
	if cat == nil {
		cat = components.Default()
	}
	log := h.Log
	if log == nil {
		log = io.Discard
	}
	getenv := h.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	seeded, err := Seed(ctx, h.Store, cat, h.Facts, h.Release)
	if err != nil {
		return fmt.Errorf("seed: %w", err)
	}
	desired, _, err := h.Store.Desired(ctx, store.Home)
	if err != nil {
		return fmt.Errorf("desired state: %w", err)
	}
	if seeded {
		fmt.Fprintf(log, "[serve] components: desired state seeded from what this home runs today "+
			"(bundle %s, %d components, release %s)\n", Bundle, len(desired), h.Release)
	}
	var found []Finding
	if h.TomlPath != "" {
		raw, err := os.ReadFile(h.TomlPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		found = Superseded(raw, h.Facts.Config, desired)
		LogSuperseded(log, h.TomlPath, found)
	}
	notes := append(EnvNotes(getenv), BootNotes(h.NoMDNS)...)
	return Report(ctx, h.Store, store.Home, desired, found, notes)
}

// NodeOverrides are a joined machine's first overrides: the components its own
// server.toml decides differently from its home's desired state ([tv] slots, [server]
// mdns, its model server). A component the home has no row for is left to follow the
// home.
func NodeOverrides(cfg config.Config, llm bool, home map[string]store.Desired) map[string]string {
	mine := map[string]string{
		"livetv.slots":     want(cfg.TV.SlotsOn()),
		"livetv.inprocess": want(!cfg.TV.SlotsOn()),
		"discovery.mdns":   want(cfg.Server.MDNSOn()),
		"llm":              want(llm),
	}
	out := map[string]string{}
	for id, w := range mine {
		if d, ok := home[id]; ok && d.Want != w {
			out[id] = w
		}
	}
	return out
}

// ErrHomeNotSeeded is a machine that boots before its home seeded: it tries again later.
var ErrHomeNotSeeded = errors.New("the home has not seeded its components yet")

// Node is a joined machine's boot step: its first overrides, once (Marker records it).
type Node struct {
	Store  *store.Store
	NodeID string
	Config config.Config
	LLM    bool   // this machine runs or names a model server
	Marker string // the file that says this machine seeded (in its state folder)
	Log    io.Writer
}

// MarkerPath is the node's marker in a state folder.
func MarkerPath(stateDir string) string { return filepath.Join(stateDir, "components-seeded.json") }

// Run writes the machine's first overrides unless its marker is there.
func (n Node) Run(ctx context.Context) error {
	if _, err := os.Stat(n.Marker); err == nil {
		return nil
	}
	meta, err := n.Store.Meta(ctx)
	if err != nil {
		return err
	}
	if !meta.Seeded() {
		return ErrHomeNotSeeded
	}
	home, _, err := n.Store.Desired(ctx, store.Home)
	if err != nil {
		return err
	}
	rows := NodeOverrides(n.Config, n.LLM, home)
	wrote, err := n.Store.SeedOverrides(ctx, n.NodeID, rows)
	if err != nil {
		return err
	}
	if n.Log != nil && wrote > 0 {
		fmt.Fprintf(n.Log, "[serve] components: %d overrides of this machine's own server.toml seeded: %v\n", wrote, rows)
	}
	raw, _ := json.Marshal(map[string]any{"seeded_at": time.Now().UTC(), "node": n.NodeID, "overrides": rows})
	if err := os.MkdirAll(filepath.Dir(n.Marker), 0o700); err != nil {
		return err
	}
	return os.WriteFile(n.Marker, raw, 0o600)
}
