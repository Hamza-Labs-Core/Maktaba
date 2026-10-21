package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

func TestBundlePayload(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		dir  string
		want string
		ok   bool
	}{
		{"/Applications/Maktaba.app/Contents/Resources/server/bin", "/Applications/Maktaba.app/Contents/Resources/server", true},
		{"/Users/x/Applications/Maktaba Beta.app/Contents/Resources/server/bin/", "/Users/x/Applications/Maktaba Beta.app/Contents/Resources/server", true},
		{"/opt/maktaba", "", false},                                        // a self-managed install root
		{"/opt/maktaba/bin/releases/1.0.0", "", false},                     // a release directory
		{"/Applications/Maktaba/Contents/Resources/server/bin", "", false}, // not inside a .app
		{"/Applications/Maktaba.app/Contents/Resources/bin", "", false},
	} {
		got, ok := bundlePayload(filepath.FromSlash(tc.dir))
		if ok != tc.ok || got != filepath.FromSlash(tc.want) {
			t.Errorf("bundlePayload(%q) = %q, %v; want %q, %v", tc.dir, got, ok, tc.want, tc.ok)
		}
	}
}

// mkBundle lays out <tmp>/Maktaba.app/Contents/Resources/server/bin with the
// test binary as maktaba-server and a behavior script; it returns the payload
// directory and a separate state root.
func mkBundle(t *testing.T, behavior string) (bundle, root string) {
	t.Helper()
	bundle = filepath.Join(t.TempDir(), "Maktaba.app", "Contents", "Resources", "server")
	bin := filepath.Join(bundle, "bin")
	if err := layout.CopyFile(selfBinary(t), filepath.Join(bin, layout.Exe("maktaba-server"))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "behavior"), []byte(behavior), 0o644); err != nil {
		t.Fatal(err)
	}
	root = t.TempDir()
	if err := os.MkdirAll(layout.New(root).UpdatesDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	return bundle, root
}

func TestBundleRestartsOn75WithUpdatesOff(t *testing.T) {
	t.Parallel()
	processTest(t)
	bundle, root := mkBundle(t, "exit:75,run")
	cfg := testConfig(root)
	cfg.Bundle = bundle
	stop := startLauncher(t, cfg)
	l := layout.New(root)
	waitFor(t, "a restart after exit 75", func() bool { return countAct(readTrace(l), "bin", "run") == 1 })
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if n := len(events(l, "launcher_restart")); n != 1 {
		t.Fatalf("launcher_restart events = %d, want 1", n)
	}
	b, _ := os.ReadFile(filepath.Join(root, "env"))
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line != "update_disable=1 runtime_owner= launcher= release_dir=" {
			t.Fatalf("server env %q: want updates off and no release layout", line)
		}
	}
	// The read-only payload is never written to: lock and log are in root.
	if _, err := os.Stat(filepath.Join(bundle, "updates")); !os.IsNotExist(err) {
		t.Fatalf("launcher wrote into the payload: %v", err)
	}
}

func TestBundleCLI(t *testing.T) {
	t.Parallel()
	bundle, root := mkBundle(t, "run")
	var out, errb bytes.Buffer
	if code := run([]string{"--app-bundle", bundle, "--replace-self", "/x/maktaba-launcher"}, &out, &errb); code != exitFailure ||
		!strings.Contains(errb.String(), "updated with the app") {
		t.Fatalf("--replace-self in an app bundle: code %d, stderr %q", code, errb.String())
	}
	out.Reset()
	if code := run([]string{"--app-bundle", bundle, "--root", root, "--status", "--json"}, &out, &errb); code != exitOK ||
		!strings.Contains(out.String(), `"updates": "with the app"`) {
		t.Fatalf("--status: code %d, out %q", code, out.String())
	}
	errb.Reset()
	if code := run([]string{"--app-bundle", t.TempDir(), "--root", root, "serve"}, &out, &errb); code != exitFailure ||
		!strings.Contains(errb.String(), "no bin/maktaba-server") {
		t.Fatalf("serve without a payload: code %d, stderr %q", code, errb.String())
	}
}

// A Linux runtime's owner decides who updates it (Epic 39 story 39.9): the app
// (updates off, as in Maktaba.app), or with owner headless the machine itself.
func TestRuntimeOwnerSaysWhoUpdates(t *testing.T) {
	t.Parallel()
	processTest(t)
	bundle, root := mkBundle(t, "exit:75,run")
	rt := t.TempDir()
	if err := os.WriteFile(filepath.Join(rt, runtimeOwnerFile), []byte(`{"owner": "headless"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(root)
	cfg.Bundle, cfg.RuntimeRoot = bundle, rt
	stop := startLauncher(t, cfg)
	l := layout.New(root)
	waitFor(t, "a restart after exit 75", func() bool { return countAct(readTrace(l), "bin", "run") == 1 })
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, "env"))
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line != "update_disable= runtime_owner=headless launcher= release_dir=" {
			t.Fatalf("server env %q: want updates on for a headless runtime", line)
		}
	}
	var out bytes.Buffer
	if code := printBundleStatus(&out, bundle, rt, root, true); code != exitOK || !strings.Contains(out.String(), "by this machine") {
		t.Fatalf("--status of a headless runtime: %q", out.String())
	}
}

func TestUpdateEnv(t *testing.T) {
	t.Parallel()
	rt := t.TempDir()
	for _, tc := range []struct {
		owner, root string
		want        string
	}{
		{"", "", "MAKTABA_UPDATE_DISABLE=1"}, // Maktaba.app
		{"", rt, "MAKTABA_RUNTIME_OWNER=app MAKTABA_UPDATE_DISABLE=1"},
		{`{"owner": "app"}`, rt, "MAKTABA_RUNTIME_OWNER=app MAKTABA_UPDATE_DISABLE=1"},
		{`{"owner": "someone"}`, rt, "MAKTABA_RUNTIME_OWNER=app MAKTABA_UPDATE_DISABLE=1"},
		{`{"owner": "headless"}`, rt, "MAKTABA_RUNTIME_OWNER=headless"},
	} {
		_ = os.Remove(filepath.Join(rt, runtimeOwnerFile))
		if tc.owner != "" {
			if err := os.WriteFile(filepath.Join(rt, runtimeOwnerFile), []byte(tc.owner), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if got := strings.Join(updateEnv(tc.root), " "); got != tc.want {
			t.Errorf("owner %q root %q: %q, want %q", tc.owner, tc.root, got, tc.want)
		}
	}
}
