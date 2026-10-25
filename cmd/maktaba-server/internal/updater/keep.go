package updater

// The releases a machine keeps, started again by hand (story 35.5 AC8): a restore of one of
// the home's backups runs the kept release whose schema is the backup's, since every
// release refuses a database a newer one migrated (install-kinds.md "The database").

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/apppayload"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// KeptRelease is a release this machine keeps and can start again: its version, what
// names it to the installer (a runtime directory, a kept payload: "" for the plain
// layout's and the app's own payload), the schema its migrations make (the newest
// migration it ships; 0 when not known), and whether it is the one running.
type KeptRelease struct {
	Version string `json:"version"`
	Ref     string `json:"ref,omitempty"`
	Schema  int64  `json:"schema,omitempty"`
	Running bool   `json:"running,omitempty"`
}

// Keeper is an installer that names the releases it keeps and makes one of them the
// release the next start runs (a restore: no run, no guard).
type Keeper interface {
	Kept() []KeptRelease
	RunKept(ctx context.Context, r KeptRelease) error
}

// releaseSchema is the schema a release directory's migrations make: release.json's
// schema_rev (the signed word, beside a staged release's binaries), else the newest
// Postgres migration in its migrations directory (an app's payload, a first install's
// release); 0 when neither says.
func releaseSchema(dir string) int64 {
	if b, err := os.ReadFile(filepath.Join(dir, releaseverify.ManifestFile)); err == nil {
		var m struct {
			SchemaRev int64 `json:"schema_rev"`
		}
		if json.Unmarshal(b, &m) == nil && m.SchemaRev > 0 {
			return m.SchemaRev
		}
	}
	return newestMigration(filepath.Join(dir, "migrations"))
}

// newestMigration is the highest Postgres migration slot in dir (NNNN_topic.sql; the
// SQLite siblings say the same).
func newestMigration(dir string) int64 {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var top int64
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") || strings.HasSuffix(name, ".sqlite.sql") {
			continue
		}
		slot, _, ok := strings.Cut(name, "_")
		if !ok {
			continue
		}
		if v, err := strconv.ParseInt(slot, 10, 64); err == nil && v > top {
			top = v
		}
	}
	return top
}

// Kept implements Keeper: every valid release under bin/releases.
func (li *LayoutInstaller) Kept() []KeptRelease {
	vs, _ := li.Layout.Versions()
	running := li.Running()
	var out []KeptRelease
	for _, v := range vs {
		if li.Layout.ValidateRelease(v) != nil {
			continue
		}
		out = append(out, KeptRelease{Version: layout.NormalizeVersion(v), Schema: releaseSchema(li.Layout.ReleaseDir(v)),
			Running: layout.NormalizeVersion(v) == running})
	}
	return out
}

// RunKept implements Keeper: current = that release (no guard: it is started on purpose).
func (li *LayoutInstaller) RunKept(ctx context.Context, r KeptRelease) error {
	return li.Revert(ctx, &Journal{From: r.Version})
}

// Kept implements Keeper: every installed runtime directory of this platform.
func (ri *RuntimeInstaller) Kept() []KeptRelease {
	dirs, _ := ri.Runtime.List()
	cur, _ := ri.Runtime.Current()
	var out []KeptRelease
	for _, d := range dirs {
		out = append(out, KeptRelease{Version: layout.NormalizeVersion(d.Version), Ref: d.Name,
			Schema: releaseSchema(filepath.Join(ri.Runtime.Root, d.Name)), Running: d.Name == cur})
	}
	return out
}

// RunKept implements Keeper: `current` → that runtime directory.
func (ri *RuntimeInstaller) RunKept(ctx context.Context, r KeptRelease) error {
	return ri.Revert(ctx, &Journal{From: r.Version, FromRef: r.Ref})
}

// Kept implements Keeper: the app's own payload and the copies kept before its updates.
func (ai *AppInstaller) Kept() []KeptRelease {
	var out []KeptRelease
	running := ai.Running()
	if p, err := apppayload.Read(filepath.Join(ai.bundled(), "payload.json")); err == nil {
		v := layout.NormalizeVersion(p.Version)
		out = append(out, KeptRelease{Version: v, Schema: releaseSchema(ai.bundled()),
			Running: v == running && sameDir(ai.Payload, ai.bundled())})
	}
	ents, _ := os.ReadDir(filepath.Join(ai.updatesDir(), "payloads"))
	for _, e := range ents {
		dir := filepath.Join(ai.updatesDir(), "payloads", e.Name())
		b, err := os.ReadFile(filepath.Join(dir, keptMarker))
		if !e.IsDir() || err != nil {
			continue
		}
		v := layout.NormalizeVersion(strings.TrimSpace(string(b)))
		out = append(out, KeptRelease{Version: v, Ref: dir, Schema: releaseSchema(dir),
			Running: sameDir(ai.Payload, dir)})
	}
	return out
}

// RunKept implements Keeper: the launcher runs that payload (a kept copy through the
// override, the app's own without one).
func (ai *AppInstaller) RunKept(ctx context.Context, r KeptRelease) error {
	return ai.Revert(ctx, &Journal{From: r.Version, FromRef: r.Ref})
}
