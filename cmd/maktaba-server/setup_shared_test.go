package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/adopt"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
)

// TestStatusBesideAnInstallThatReadsTheAppsConfig: the server.toml in the
// app's place is an install's outside the app (on the default paths from
// before Epic 38, a newer command copied its config there, story 38.1). While
// that install is here the app is not set up as its own: the window's setup
// would otherwise register the app's server over it; the welcome screen
// offers its move instead.
func TestStatusBesideAnInstallThatReadsTheAppsConfig(t *testing.T) {
	e := newSetupEnv(t)
	if err := os.WriteFile(e.config, []byte("[server]\ndata_dir = '"+e.state+"'\n[database]\nurl = 'postgres://m@127.0.0.1/m'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, ev := runStep(t, "status", map[string]any{}); data(t, ev)["configured"] != true {
		t.Fatalf("the app's own: %v", ev)
	}
	// Maktaba's launcher task of a release install on the default paths: no
	// MAKTABA_CONFIG of its own, so the app's server.toml is the one it reads
	dir := filepath.Join(e.dir, "release")
	sys := &adoptFakeSys{tasks: []adopt.Task{{Name: `\Maktaba`, Command: filepath.Join(dir, "maktaba-launcher.exe"),
		Args: `--root "` + dir + `" serve`, Enabled: true}},
		// its environment: this test's folders, no node config
		env: map[string]string{"MAKTABA_NODE_CONFIG": filepath.Join(e.dir, "none.json"),
			"LOCALAPPDATA": filepath.Join(e.dir, "Local"), "APPDATA": filepath.Join(e.dir, "Roaming")}}
	prev := adoptSys
	adoptSys = func() adopt.Sys { return sys }
	t.Cleanup(func() { adoptSys = prev })
	if _, _, ev := runStep(t, "status", map[string]any{}); data(t, ev)["configured"] != false {
		t.Fatalf("beside an install that reads the app's server.toml: %v", ev)
	}
}

// TestALeftoverNodeConfigCopiedIsOne: on Windows the node config of an install
// from before Epic 38 is in %APPDATA%\Maktaba, and another may be in
// %LOCALAPPDATA%\Maktaba (a move undone left the app's): one node config nothing
// runs, said once, the one the roles read (config.LocalNodeConfigPath decides).
func TestALeftoverNodeConfigCopiedIsOne(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the roaming profile's original and its copy are Windows's")
	}
	e := newSetupEnv(t)
	local, roaming := filepath.Join(e.dir, "Local"), filepath.Join(e.dir, "Roaming")
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("APPDATA", roaming)
	t.Setenv("MAKTABA_NODE_CONFIG", "")
	node := `{"node_id":"n-1","private_key":"AQID","database_url":"postgres://node@home/maktaba","home":{"name":"Home","url":"http://home:8080"}}`
	for _, p := range []string{filepath.Join(roaming, "Maktaba", "node.json"), filepath.Join(local, "Maktaba", "node.json")} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(node), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	prevFacts, prevGPUs := hostFacts, hostGPUs
	t.Cleanup(func() { hostFacts, hostGPUs = prevFacts, prevGPUs })
	hostFacts = func(string) (uint64, uint64) { return 32 << 30, 500 << 30 }
	hostGPUs = func() []hostinfo.GPU { return nil }
	_, _, ev := runStep(t, "check", map[string]any{})
	var unused []string
	for _, w := range data(t, ev)["warnings"].([]any) {
		n := w.(map[string]any)
		if n["code"] == "node_config_unused" {
			unused = append(unused, n["params"].(map[string]any)["path"].(string))
		}
	}
	// whichever of the two the roles read (config.LocalNodeConfigPath), said once
	if want := config.LocalNodeConfigPath(); len(unused) != 1 || unused[0] != want {
		t.Fatalf("node configs nothing runs %q, want the one the roles read alone %q", unused, want)
	}
}
