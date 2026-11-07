package supervisor

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// #710: on Windows the host starts inside its AppContainer through the launcher, granted
// read on the plugins folder and read-write on its cache, run folder and short socket
// folder, never <data>\run itself (the supervisor's own files are there); elsewhere it is
// the host binary, unchanged.
func TestTheHostsCommandOnWindows(t *testing.T) {
	cfg := config.Config{}
	cfg.Server.DataDir = filepath.Join("C:", "data")
	p := pluginsPaths(cfg, "windows")
	args := []string{"--data", cfg.Server.DataDir, "--pool", "1"}

	name, argv := sandboxed("linux", "launcher", p, "maktaba-plugind", args)
	if name != "maktaba-plugind" || !slices.Equal(argv, args) {
		t.Fatalf("not Windows: %s %v", name, argv)
	}

	name, argv = sandboxed("windows", "launcher", p, "maktaba-plugind", args)
	if name != "launcher" {
		t.Fatalf("the launcher: %s", name)
	}
	sep := slices.Index(argv, "--")
	if argv[0] != "plugins-sandbox-run" || sep < 0 || argv[sep+1] != "maktaba-plugind" ||
		!slices.Equal(argv[sep+2:], args) {
		t.Fatalf("command line %v", argv)
	}
	grants := map[string]string{}
	for i := 1; i+1 < sep; i += 2 {
		grants[argv[i+1]] = argv[i]
	}
	want := map[string]string{p.Root: "-read", p.Cache: "-write", p.Run: "-write"}
	for dir, flag := range want {
		if grants[dir] != flag {
			t.Errorf("%s: %q, want %s (grants %v)", dir, grants[dir], flag, grants)
		}
	}
	runDir := filepath.Join(cfg.Server.DataDir, "run")
	if _, ok := grants[runDir]; ok {
		t.Errorf("the container is granted %s, which holds the supervisor's own files", runDir)
	}
}

// #710: on Windows both sockets are in the host's run folder (the one its AppContainer is
// granted); elsewhere the API's stays in <data>/run.
func TestThePluginSocketsOnWindows(t *testing.T) {
	cfg := config.Config{}
	cfg.Server.DataDir = filepath.Join("C:", "data")
	w := pluginsPaths(cfg, "windows")
	if filepath.Dir(w.API) != w.Run || filepath.Dir(w.Dispatch) != w.Run {
		t.Errorf("Windows sockets %s, %s: not in %s", w.API, w.Dispatch, w.Run)
	}
	o := pluginsPaths(cfg, "linux")
	if filepath.Dir(o.API) != filepath.Join(cfg.Server.DataDir, "run") {
		t.Errorf("Linux API socket %s moved", o.API)
	}
	// a data folder too long for a socket: the short folder, granted too
	cfg.Server.DataDir = filepath.Join("C:", strings.Repeat("x", 120))
	w = pluginsPaths(cfg, "windows")
	if w.Sockets == "" || filepath.Dir(w.API) != w.Sockets {
		t.Fatalf("long path: %+v", w)
	}
	if _, argv := sandboxed("windows", "l", w, "b", nil); !slices.Contains(argv, w.Sockets) {
		t.Errorf("the short socket folder is not granted: %v", argv)
	}
}
