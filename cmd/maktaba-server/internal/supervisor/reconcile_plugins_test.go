package supervisor

import (
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// pluginsCatalog is the embedded catalog with one plugin entry, dev.example.a.
func pluginsCatalog(t *testing.T) *components.Catalog {
	t.Helper()
	e := components.PluginEntry(components.PluginSpec{ID: "dev.example.a", Stages: []string{"p_00000000_x"}})
	cat, err := components.With(components.Default(), []*components.Component{e})
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

// Story 54.37 T05 and 54.07 T08: an on-demand host runs only while a plugin is enabled,
// not held and passing its gates; otherwise it reads running with idle, no process.
func TestOnDemandHostRunsOnlyForAnEnabledPlugin(t *testing.T) {
	tab := &fakeTable{}
	tab.procs = append(tab.procs, procView{Role: RolePlugins, Component: components.PluginsHost, Dormant: true,
		Wanted: true, State: StateStopped})
	st := &fakeStore{}
	r, _ := newTestReconciler(components.Default(), tab, st)
	gate := ""
	r.PluginGate = func(string) string { return gate }
	host := func() store.Status {
		row, ok := st.row(components.PluginsHost)
		if !ok {
			t.Fatal("no plugins.host row")
		}
		return row
	}

	// no plugin installed (the static catalog): idle, no process, running with idle
	passWith(r, map[string]store.Desired{})
	if calls := tab.takeCalls(); len(calls) != 0 {
		t.Fatalf("calls %v with no plugin", calls)
	}
	if row := host(); row.Phase != components.PhaseRunning || row.Detail["idle"] != true {
		t.Fatalf("plugins.host %s %v, want running idle", row.Phase, row.Detail)
	}

	// a plugin installed and off: still idle
	r.SetPluginCatalog(pluginsCatalog(t))
	off := map[string]store.Desired{"plugin.dev.example.a": want("plugin.dev.example.a", "installed", 1)}
	passWith(r, off)
	if calls := tab.takeCalls(); len(calls) != 0 {
		t.Fatalf("calls %v with the plugin off", calls)
	}

	// enabled: the host starts (plugins.host has no desired row: its catalog default)
	on := map[string]store.Desired{"plugin.dev.example.a": want("plugin.dev.example.a", "enabled", 2)}
	passWith(r, on)
	if calls := tab.takeCalls(); !slices.Equal(calls, []string{"=true"}) {
		t.Fatalf("calls %v, want the host started", calls)
	}
	if row := host(); row.Phase != components.PhaseRunning || row.Detail["idle"] != nil {
		t.Fatalf("plugins.host %s %v, want running", row.Phase, row.Detail)
	}

	// its gate closes (grants_needed): the host stops again
	gate = components.ReasonGrantsNeeded
	passWith(r, on)
	if calls := tab.takeCalls(); !slices.Equal(calls, []string{"=false"}) {
		t.Fatalf("calls %v, want the host stopped", calls)
	}
	gate = ""
	passWith(r, on)
	tab.takeCalls()

	// held: stopped
	held := map[string]store.Desired{"plugin.dev.example.a": {ID: "plugin.dev.example.a", Want: "enabled",
		Generation: 3, Hold: &store.Hold{By: "plugin-breaker"}}}
	passWith(r, held)
	if calls := tab.takeCalls(); !slices.Equal(calls, []string{"=false"}) {
		t.Fatalf("calls %v, want the host stopped for a held plugin", calls)
	}

	// the admin's master switch: plugins.host off keeps it off whatever the plugins say
	master := map[string]store.Desired{"plugin.dev.example.a": want("plugin.dev.example.a", "enabled", 4),
		components.PluginsHost: want(components.PluginsHost, "installed", 1)}
	passWith(r, master)
	if calls := tab.takeCalls(); len(calls) != 0 {
		t.Fatalf("calls %v with plugins.host off", calls)
	}
	if row := host(); row.Phase != components.PhaseOff {
		t.Fatalf("plugins.host %s, want off", row.Phase)
	}
}

// A host that exited with its sandbox unavailable reads failed with that reason.
func TestOnDemandHostFailedSandbox(t *testing.T) {
	tab := &fakeTable{}
	tab.procs = append(tab.procs, procView{Role: RolePlugins, Component: components.PluginsHost, Dormant: true,
		Wanted: true, Allowed: true, State: StateFailed, FailReason: ReasonSandboxUnavailable})
	st := &fakeStore{}
	r, _ := newTestReconciler(pluginsCatalog(t), tab, st)
	passWith(r, map[string]store.Desired{"plugin.dev.example.a": want("plugin.dev.example.a", "enabled", 1)})
	row, _ := st.row(components.PluginsHost)
	if row.Phase != components.PhaseFailed || row.Reason() != ReasonSandboxUnavailable {
		t.Fatalf("plugins.host %s %q, want failed sandbox_unavailable", row.Phase, row.Reason())
	}
}

// Story 54.03 T06: the host's exit 78 (its sandbox could not be applied) is no crash to
// restart from: it stays failed with sandbox_unavailable, started once, until a restart.
func TestFatalExitIsNotRestarted(t *testing.T) {
	p, _ := fakeProc(t, RolePlugins, "crash", "FAKE_CODE=78")
	p.FatalExits = map[int]string{PluginsSandboxExit: ReasonSandboxUnavailable}
	s := New([]Process{p}, testPolicy(), WithLog(&syncBuffer{}))
	running(t, s)
	snap := waitFor(t, s, "the host failed", func(s Snapshot) bool { return proc(s, RolePlugins).State == StateFailed })
	got := proc(snap, RolePlugins)
	if got.Restarts != 0 || got.NextRestartAt != nil || got.LastExit == nil || got.LastExit.Code != 78 {
		t.Fatalf("after exit 78: %+v", got)
	}
	var view procView
	for _, v := range s.procViews() {
		if v.Role == RolePlugins {
			view = v
		}
	}
	if view.FailReason != ReasonSandboxUnavailable {
		t.Fatalf("FailReason %q", view.FailReason)
	}
	if err := s.Restart(RolePlugins, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, s, "started again on a restart", func(s Snapshot) bool {
		e := proc(s, RolePlugins).LastExit
		return e != nil && !e.At.Equal(got.LastExit.At)
	})
}

// The sockets move to a short folder when <data>/run is too long for a unix socket.
func TestPluginsSocketsFitSunPath(t *testing.T) {
	short := config.Config{}
	short.Server.DataDir = "/var/lib/maktaba"
	p := PluginsPaths(short)
	if p.Sockets != "" || p.Dispatch != "/var/lib/maktaba/run/plugins/plugind.sock" {
		t.Fatalf("short: %+v", p)
	}
	long := config.Config{}
	long.Server.DataDir = "/" + strings.Repeat("deep/", 30) + "data"
	p = PluginsPaths(long)
	if p.Sockets == "" || len(p.Dispatch) > maxSocketPath || len(p.API) > maxSocketPath ||
		filepath.Dir(p.Dispatch) != p.Sockets || PluginsPaths(long).Sockets != p.Sockets {
		t.Fatalf("long: %+v", p)
	}
}

// The roles that serve plugins are told where: the API its socket, the pipeline the
// host's socket and the per-boot token its dispatcher mints with (a home on Postgres).
func TestPluginsEnvOfTheRoles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no plugins on Windows yet")
	}
	cfg := config.Config{}
	cfg.Server.DataDir = t.TempDir()
	cfg.Database.URL = "postgres://home/maktaba"
	p := PluginsPaths(cfg)
	api := envMap(childEnvOK(RoleAPI, cfg))
	pipe := envMap(childEnvOK(RolePipeline, cfg))
	if api[EnvPluginAPISocket] != p.API || pipe[EnvPluginDSocket] != p.Dispatch || pipe["MAKTABA_SUPERVISOR_TOKEN_FILE"] == "" {
		t.Fatalf("api %q, pipeline %q %q", api[EnvPluginAPISocket], pipe[EnvPluginDSocket], pipe["MAKTABA_SUPERVISOR_TOKEN_FILE"])
	}
	cfg.Database.URL = "sqlite:///x.db"
	if _, ok := envMap(childEnvOK(RolePipeline, cfg))[EnvPluginDSocket]; ok {
		t.Fatal("a SQLite home runs no plugins")
	}
}
