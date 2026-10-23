package main

import (
	"bytes"
	"encoding/json"
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

// mkWinInstall lays out the Windows app's marker files under install:
// Maktaba.exe, server/payload.json and an empty server/bin. It returns the
// payload directory.
func mkWinInstall(t *testing.T, install string) string {
	t.Helper()
	payload := filepath.Join(install, "server")
	if err := os.MkdirAll(filepath.Join(payload, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{filepath.Join(install, "Maktaba.exe"), filepath.Join(payload, "payload.json")} {
		if err := os.WriteFile(f, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return payload
}

// TestBundlePayloadWindowsLayout: <install>/server/bin is a payload only with
// payload.json beside bin and Maktaba.exe beside server (the rule is the
// files', so it runs on every OS).
func TestBundlePayloadWindowsLayout(t *testing.T) {
	t.Parallel()
	install := filepath.Join(t.TempDir(), "Maktaba")
	payload := mkWinInstall(t, install)
	bin := filepath.Join(payload, "bin")
	if got, ok := bundlePayload(bin); !ok || got != payload {
		t.Fatalf("bundlePayload(%q) = %q, %v; want %q, true", bin, got, ok, payload)
	}
	if got, ok := bundlePayload(bin + string(filepath.Separator)); !ok || got != payload {
		t.Fatalf("with a trailing separator: %q, %v", got, ok)
	}

	notDetected := func(what, dir string) {
		t.Helper()
		if got, ok := bundlePayload(dir); ok {
			t.Errorf("%s: bundlePayload(%q) = %q, true; want not a payload", what, dir, got)
		}
	}
	noPayloadJSON := mkWinInstall(t, filepath.Join(t.TempDir(), "Maktaba"))
	if err := os.Remove(filepath.Join(noPayloadJSON, "payload.json")); err != nil {
		t.Fatal(err)
	}
	notDetected("no payload.json", filepath.Join(noPayloadJSON, "bin"))

	noShell := mkWinInstall(t, filepath.Join(t.TempDir(), "Maktaba"))
	if err := os.Remove(filepath.Join(filepath.Dir(noShell), "Maktaba.exe")); err != nil {
		t.Fatal(err)
	}
	notDetected("no Maktaba.exe", filepath.Join(noShell, "bin"))

	shellIsDir := mkWinInstall(t, filepath.Join(t.TempDir(), "Maktaba"))
	exe := filepath.Join(filepath.Dir(shellIsDir), "Maktaba.exe")
	if err := os.Remove(exe); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(exe, 0o755); err != nil {
		t.Fatal(err)
	}
	notDetected("Maktaba.exe is a folder", filepath.Join(shellIsDir, "bin"))

	// the same files under other folder names
	other := filepath.Join(t.TempDir(), "Maktaba")
	for _, f := range []string{filepath.Join(other, "Maktaba.exe"), filepath.Join(other, "payload", "payload.json")} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	notDetected("payload folder not named server", filepath.Join(other, "payload", "bin"))
	notDetected("bin folder not named bin", filepath.Join(payload, "tools"))
	notDetected("the install folder itself", install)
	notDetected("the payload folder itself", payload)
}

func TestAppStateRoot(t *testing.T) {
	t.Parallel()
	env := func(kv ...string) func(string) string {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) string { return m[k] }
	}
	winHome, macHome := `C:\Users\amira`, "/Users/amira"
	local := `C:\Users\amira\AppData\Local`
	for _, tc := range []struct {
		name, flag string
		getenv     func(string) string
		goos, home string
		want       string
	}{
		{"windows: --root", `D:\state`, env("LOCALAPPDATA", local, layout.EnvInstallRoot, `E:\env`), "windows", winHome, `D:\state`},
		{"windows: $MAKTABA_INSTALL_ROOT", "", env("LOCALAPPDATA", local, layout.EnvInstallRoot, `E:\env`), "windows", winHome, `E:\env`},
		{"windows: %LOCALAPPDATA%", "", env("LOCALAPPDATA", local), "windows", winHome, filepath.Join(local, "Maktaba")},
		{"windows: no LOCALAPPDATA", "", env(), "windows", winHome, filepath.Join(winHome, "AppData", "Local", "Maktaba")},
		{"windows: nothing", "", env(), "windows", "", "."},
		{"darwin: --root", "/tmp/state", env(layout.EnvInstallRoot, "/env"), "darwin", macHome, "/tmp/state"},
		{"darwin: $MAKTABA_INSTALL_ROOT", "", env(layout.EnvInstallRoot, "/env"), "darwin", macHome, "/env"},
		{"darwin: Application Support", "", env("LOCALAPPDATA", local), "darwin", macHome,
			filepath.Join(macHome, "Library", "Application Support", "Maktaba")},
		{"darwin: no home", "", env(), "darwin", "", "."},
	} {
		if got := appStateRootFor(tc.flag, tc.getenv, tc.goos, tc.home); got != tc.want {
			t.Errorf("%s: appStateRootFor = %q, want %q", tc.name, got, tc.want)
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
		if line != "update_disable= runtime_owner= launcher= release_dir=" {
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
		!strings.Contains(out.String(), `"updates": "with the app (the cluster's update: staged here`) {
		t.Fatalf("--status: code %d, out %q", code, out.String())
	}
	var st BundleStatus
	if err := json.Unmarshal(out.Bytes(), &st); err != nil {
		t.Fatalf("--status --json: %v\n%s", err, out.String())
	}
	// the platform's paths: backslashes and maktaba-server.exe on Windows
	if want := filepath.Join(bundle, "bin", layout.Exe("maktaba-server")); st.AppBundle != bundle || st.Server != want ||
		st.Root != root || st.LauncherLog != layout.New(root).LauncherLog() || st.StopEvent != stopEventName {
		t.Fatalf("--status --json = %+v; want server %q", st, want)
	}
	out.Reset()
	if code := run([]string{"--app-bundle", bundle, "--root", root, "--status"}, &out, &errb); code != exitOK ||
		!strings.Contains(out.String(), "server:     "+st.Server+" (") || !strings.Contains(out.String(), "state:      "+root+"\n") {
		t.Fatalf("--status: code %d, out %q", code, out.String())
	}
	errb.Reset()
	if code := run([]string{"--app-bundle", t.TempDir(), "--root", root, "serve"}, &out, &errb); code != exitFailure ||
		!strings.Contains(errb.String(), "no "+filepath.Join("bin", layout.Exe("maktaba-server"))) {
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
	if code := printBundleStatus(&out, bundle, rt, root, true); code != exitOK || !strings.Contains(out.String(), "by itself in the window") {
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
		{"", "", ""}, // Maktaba.app: the server updates itself through the app (Epic 35)
		{"", rt, "MAKTABA_RUNTIME_OWNER=app"},
		{`{"owner": "app"}`, rt, "MAKTABA_RUNTIME_OWNER=app"},
		{`{"owner": "someone"}`, rt, "MAKTABA_RUNTIME_OWNER=app"},
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
