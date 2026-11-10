package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

func yes() *bool { b := true; return &b }
func no() *bool  { b := false; return &b }

// Epic 59 story 59.07 T01, T02: the level from the kernel's facts.
func TestLevelOfTheKernelsFacts(t *testing.T) {
	for _, c := range []struct {
		name    string
		goos    string
		k       hostinfo.Kernel
		plugind string
		missing []string
		level   string
		want    []string
	}{
		{"T01 no Landlock, no user namespaces", "linux", hostinfo.Kernel{Landlock: 0, UserNS: no()}, "", nil, LevelContainer, []string{"landlock", "namespaces"}},
		{"T02 every layer", "linux", hostinfo.Kernel{Landlock: 4, UserNS: yes()}, "", nil, LevelFull, []string{}},
		{"Landlock without user namespaces (Docker's default seccomp)", "linux", hostinfo.Kernel{Landlock: 3, UserNS: no()}, "", nil, LevelContainer, []string{"namespaces"}},
		{"user namespaces without Landlock (a custom profile on a NAS kernel)", "linux", hostinfo.Kernel{Landlock: 0, UserNS: yes()}, "", nil, LevelContainer, []string{"landlock"}},
		{"user namespaces not tried", "linux", hostinfo.Kernel{Landlock: 4}, "", nil, LevelContainer, []string{"namespaces"}},
		{"plugind reports partial on a full kernel", "linux", hostinfo.Kernel{Landlock: 4, UserNS: yes()}, "partial", []string{"namespaces"}, LevelContainer, []string{"namespaces"}},
		{"plugind partial with nothing named", "linux", hostinfo.Kernel{Landlock: 4, UserNS: yes()}, "partial", nil, LevelContainer, []string{"plugind"}},
		{"plugind full agrees", "linux", hostinfo.Kernel{Landlock: 4, UserNS: yes()}, "full", nil, LevelFull, []string{}},
		{"macOS has no optional layer to lack", "darwin", hostinfo.Kernel{}, "", nil, LevelFull, []string{}},
		{"Windows", "windows", hostinfo.Kernel{}, "full", nil, LevelFull, []string{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := LevelOf(c.goos, c.k, c.plugind, c.missing)
			if got.Level != c.level || !slices.Equal(got.Missing, c.want) {
				t.Fatalf("LevelOf = %s %v, want %s %v", got.Level, got.Missing, c.level, c.want)
			}
		})
	}
}

func isolationRow(t *testing.T, r *Runner) map[string]Isolation {
	t.Helper()
	var raw []byte
	if err := r.DB.QueryRow(`SELECT value FROM app_settings WHERE key = $1`, IsolationKey).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	out := map[string]Isolation{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// 59.07 (the supervisor's half of T06): on a Container-level machine an enabled
// third-party plugin waits isolation_held until the policy allows container-only
// isolation; each machine's level is written to plugins.isolation beside the others'.
func TestContainerLevelHoldsThirdPartyPluginsUntilAllowed(t *testing.T) {
	db := scratchDB(t)
	ctx := context.Background()
	f, _ := newFiles(t)
	st := store.New(db)
	r := &Runner{Files: f, DB: db, Machine: store.Home, Version: "0.3.83", Catalog: &sink{}, Status: st, Log: os.Stderr,
		Kernel: &hostinfo.Kernel{Landlock: 0, UserNS: no()}, GOOS: "linux"}
	cid := components.PluginPrefix + testID
	v1, err := f.Install(ctx, bytes.NewReader(pkg(t, testID, "1.0.0")))
	if err != nil {
		t.Fatal(err)
	}
	record(t, db, v1, nil)
	if _, err := db.Exec(`UPDATE plugin_installs SET review_level = 'reviewed'`); err != nil {
		t.Fatal(err)
	}
	// another machine's level is already there: it is kept
	if _, err := db.Exec(`INSERT INTO app_settings (key, value) VALUES ($1, '{"node-b": {"level": "full", "missing": []}}')`, IsolationKey); err != nil {
		t.Fatal(err)
	}
	r.Pass(ctx)
	if g := r.Gate(cid); g != components.ReasonIsolationHeld {
		t.Fatalf("gate %q, want isolation_held on a Container-level home", g)
	}
	rows := isolationRow(t, r)
	if rows[store.Home].Level != LevelContainer || rows["node-b"].Level != LevelFull {
		t.Fatalf("plugins.isolation %+v", rows)
	}
	if got := Current("linux", hostinfo.Kernel{Landlock: 4, UserNS: yes()}); got.Level != LevelContainer {
		t.Fatalf("Current %+v, want the runner's level", got)
	}
	if _, err := db.Exec(`INSERT INTO app_settings (key, value) VALUES ($1, '{"allow_unreviewed": false, "allow_container_isolation": true}')`, PolicyKey); err != nil {
		t.Fatal(err)
	}
	r.Pass(ctx)
	if g := r.Gate(cid); g != "" {
		t.Fatalf("gate %q after the admin's yes, want open", g)
	}

	// a full kernel whose plugin host reports partial: Container, and it stays so this start
	r2 := &Runner{Files: f, DB: db, Machine: "node-c", Version: "0.3.83", Catalog: &sink{}, Status: st, Log: os.Stderr,
		Kernel: &hostinfo.Kernel{Landlock: 4, UserNS: yes()}, GOOS: "linux", Host: &supervisor.PluginsHealth{}}
	r2.Pass(ctx)
	if got := isolationRow(t, r2)["node-c"]; got.Level != LevelFull {
		t.Fatalf("node-c %+v, want full before the host ran", got)
	}
}
