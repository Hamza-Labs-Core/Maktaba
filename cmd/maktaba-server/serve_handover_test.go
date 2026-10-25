package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// Story 35.7: the TV handover runs only on a layout install under a supervisor that can
// make it (and on Linux a launcher that says it keeps the TV); an armed exit 75 hands
// over, any other exit does not.
func TestTVHandoverWiring(t *testing.T) {
	t.Setenv(layout.EnvLauncherHandover, "1")
	u := &updates{}
	if ok, why := u.tvHandover(nil, "1.5.0", false); ok || why == "" {
		t.Fatalf("no layout: %v %q", ok, why)
	}
	l := layout.New(t.TempDir())
	u.lay = &l
	if ok, _ := u.tvHandover(nil, "1.5.0", true); ok || u.armed.Load() {
		t.Fatal("armed without a supervisor")
	}
	sup := supervisor.New(nil, supervisor.DefaultPolicy(), supervisor.WithHandover(l.UpdatesDir(), 0))
	u.setSupervisor(sup)
	if ok, why := u.tvHandover(nil, "1.5.0", false); !ok || u.armed.Load() {
		t.Fatalf("a check armed it, or refused: %v %q", ok, why)
	}
	u.beforeExit(75)
	if sup.HandedOver() {
		t.Fatal("an exit nobody armed")
	}
	if ok, _ := u.tvHandover(nil, "1.5.0", true); !ok || !u.armed.Load() {
		t.Fatal("not armed")
	}
	u.beforeExit(0) // another exit: the arm stays for its exit 75
	if !u.armed.Load() {
		t.Fatal("an exit 0 took the arm")
	}
	u.beforeExit(75)
	if u.armed.Load() {
		t.Fatal("the arm outlived its exit")
	}
}

// The edge protocol of a release is its release.json's (0 when it cannot be read).
func TestEdgeProtocolOfARelease(t *testing.T) {
	dir := t.TempDir()
	if edgeProtocol(dir) != 0 {
		t.Fatal("no release.json")
	}
	if err := os.WriteFile(filepath.Join(dir, releaseverify.ManifestFile), []byte(`{"v":1,"edge_protocol":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := edgeProtocol(dir); got != 2 {
		t.Fatalf("edge protocol %d", got)
	}
}
