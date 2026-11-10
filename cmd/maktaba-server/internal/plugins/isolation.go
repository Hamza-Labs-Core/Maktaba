package plugins

// Plugin isolation levels (Epic 59 story 59.07, README §6.6). Each machine computes its
// level at start from what hostinfo read of its kernel (59.04) and from what the plugin
// host reports once it runs:
//
//	full       Landlock and user namespaces: plugind and the runners apply every layer
//	container  no Landlock (a NAS kernel before 5.13) or no user namespaces (Docker's
//	           default seccomp profile): plugind still requires no-new-privs, rlimits,
//	           the Wasm sandbox and its own AF_UNIX-only seccomp filter, but its optional
//	           layers are missing (it reports "partial"); third-party plugins wait
//	           (isolation_held) until an admin allows plugins with container-only isolation
//	none       a privileged container or one with the Docker socket: never reached here,
//	           the container's preflight refuses to start (59.01, container_preflight.go)
//
// macOS (Seatbelt) and Windows (AppContainer) have no optional layer that a kernel can
// lack, so they are full unless plugind itself reports partial.
//
// Each machine writes its level to app_settings plugins.isolation, a map machine → Isolation,
// for the API (the Plugins page) and the pipeline's twin of the gate.

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"sync/atomic"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
)

// The levels.
const (
	LevelFull      = "full"
	LevelContainer = "container"
	LevelNone      = "none"
)

// IsolationKey is the app_settings key of every machine's level (a map machine →
// Isolation). A plugin's own settings are plugins.<id>, and an id has at least three
// labels, so the two never meet.
const IsolationKey = "plugins.isolation"

// Isolation is one machine's level and what it was computed from.
type Isolation struct {
	Level string `json:"level"`
	// Missing are the optional layers this machine cannot apply: landlock, namespaces
	// (from the kernel's facts), and whatever plugind reported missing.
	Missing []string `json:"missing"`
	// Landlock is the kernel's Landlock ABI (0: none); UserNS whether a user namespace
	// could be made (nil: not tried).
	Landlock int   `json:"landlock"`
	UserNS   *bool `json:"userns,omitempty"`
	// Plugind is plugind's own report ("full", "partial"; "" while it has not run).
	Plugind string `json:"plugind,omitempty"`
}

// LevelOf is the level of a machine running goos with kernel k, and plugind's last report
// ("" when it has not run this start).
func LevelOf(goos string, k hostinfo.Kernel, plugind string, plugindMissing []string) Isolation {
	iso := Isolation{Level: LevelFull, Missing: []string{}, Landlock: k.Landlock, UserNS: k.UserNS, Plugind: plugind}
	if goos == "linux" {
		if k.Landlock <= 0 {
			iso.Missing = append(iso.Missing, "landlock")
		}
		// not tried (no `true` to start) counts as missing: plugind would not have them either
		if k.UserNS == nil || !*k.UserNS {
			iso.Missing = append(iso.Missing, "namespaces")
		}
	}
	if plugind == "partial" {
		for _, m := range plugindMissing {
			if m != "" && !slices.Contains(iso.Missing, m) {
				iso.Missing = append(iso.Missing, m)
			}
		}
		if len(iso.Missing) == 0 {
			iso.Missing = append(iso.Missing, "plugind")
		}
	}
	if len(iso.Missing) > 0 {
		iso.Level = LevelContainer
	}
	return iso
}

// current is this process's last level (Current).
var current atomic.Pointer[Isolation]

// Current is this machine's level as the plugins runner last computed it; before its first
// pass, the kernel's facts alone.
func Current(goos string, k hostinfo.Kernel) Isolation {
	if c := current.Load(); c != nil {
		return *c
	}
	return LevelOf(goos, k, "", nil)
}

// publishIsolation writes machine's level into plugins.isolation, merged with the other
// machines' (one row, updated in one statement, so two machines never lose each other's).
func publishIsolation(ctx context.Context, db *sql.DB, machine string, iso Isolation) error {
	b, err := json.Marshal(map[string]Isolation{machine: iso})
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO app_settings (key, value) VALUES ($1, $2::jsonb)
		ON CONFLICT (key) DO UPDATE SET value = app_settings.value || excluded.value, updated_at = now()`,
		IsolationKey, string(b))
	return err
}
