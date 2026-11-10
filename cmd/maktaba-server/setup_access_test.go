package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/mediaaccess"
)

// Story 59.03: setup shows each library folder's access, with the fix in the NAS's words and
// who Maktaba runs as. A read-only folder is accepted; one Maktaba cannot list is not.
func TestSetupLibrariesShowsAccess(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file modes do not bind this user")
	}
	e := newSetupEnv(t)
	cfg := config.Defaults()
	cfg.Server.DataDir = e.state
	s := &setupRun{ctx: context.Background(), cfg: cfg, out: json.NewEncoder(io.Discard), log: io.Discard}
	// the platform comes from story 59.04's detection
	old := hostMachine
	hostMachine = func() hostinfo.Machine { return hostinfo.Machine{Platform: hostinfo.PlatformTrueNAS} }
	t.Cleanup(func() { hostMachine = old })

	rw, ro := filepath.Join(e.dir, "rw"), filepath.Join(e.dir, "ro")
	for _, d := range []string{rw, ro} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })
	out, err := stepLibraries(s, json.RawMessage(`{"roots":["`+rw+`","`+ro+`"]}`))
	if err != nil {
		t.Fatal(err)
	}
	res := out.(map[string]any)
	checks := res["roots"].([]rootCheck)
	if len(checks) != 2 || checks[0].Access != mediaaccess.ReadWrite || checks[0].Fix != nil {
		t.Fatalf("rw root: %+v", checks)
	}
	if checks[1].Access != mediaaccess.ReadOnly || checks[1].Fix == nil || checks[1].Fix.Key != "library.fix.truenas" {
		t.Fatalf("ro root: %+v", checks[1])
	}
	if res["platform"] != "truenas" || res["runs_as"].(mediaaccess.RunsAs).UID != os.Getuid() {
		t.Fatalf("step answer %+v", res)
	}
	if entries, _ := os.ReadDir(ro); len(entries) != 0 {
		t.Fatalf("the check wrote into the read-only folder: %v", entries)
	}

	none := filepath.Join(e.dir, "none")
	if err := os.Mkdir(none, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(none, 0o755) })
	_, err = stepLibraries(s, json.RawMessage(`{"roots":["`+none+`"]}`))
	if se, ok := err.(*stepError); !ok || se.code != "root_unreadable" {
		t.Fatalf("an unlistable folder: %v", err)
	}
}
