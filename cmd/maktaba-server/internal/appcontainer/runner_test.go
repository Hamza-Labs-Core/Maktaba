package appcontainer

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Story 54.23 on Windows: a runner's policy becomes its profile's grants. Read on the
// interpreter, the read areas and the tools; full control on the run folder and the write
// areas (write wins for a path named twice); the library's subtitle folders are written
// (the runner's guard keeps them to subtitle names) and never made; only the run folder and
// write areas under the data folder are made when missing.
func TestARunnersPolicyBecomesItsGrants(t *testing.T) {
	data := filepath.Join(string(filepath.Separator)+"data", "maktaba")
	p := RunnerPolicy{
		Python:     []string{"/py", "/py/lib"},
		Read:       []string{"/media/a", filepath.Join(data, "thumbnails")},
		Write:      []string{filepath.Join(data, "thumbnails"), "/media/b/.maktaba/trash"},
		WriteNamed: []string{"/media/a"},
		Exec:       []string{"/ffmpeg/bin/ffmpeg.exe"},
		ToolDirs:   []string{"/ffmpeg"},
		RunDir:     filepath.Join(data, "run", "runners"),
	}
	got := map[string]GrantEntry{}
	for _, g := range p.Grants(data) {
		got[filepath.ToSlash(g.Path)] = g
	}
	want := map[string]GrantEntry{
		"/py":                     {Path: filepath.Clean("/py")},
		"/py/lib":                 {Path: filepath.Clean("/py/lib")},
		"/ffmpeg":                 {Path: filepath.Clean("/ffmpeg")},
		"/ffmpeg/bin/ffmpeg.exe":  {Path: filepath.Clean("/ffmpeg/bin/ffmpeg.exe")},
		"/media/a":                {Path: filepath.Clean("/media/a"), Write: true},
		"/media/b/.maktaba/trash": {Path: filepath.Clean("/media/b/.maktaba/trash"), Write: true},
		filepath.ToSlash(filepath.Join(data, "thumbnails")):     {Path: filepath.Join(data, "thumbnails"), Write: true, Create: true},
		filepath.ToSlash(filepath.Join(data, "run", "runners")): {Path: filepath.Join(data, "run", "runners"), Write: true, Create: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("grants\n got %+v\nwant %+v", got, want)
	}
}

// A grant an earlier start made that the policy no longer names the same way is taken
// back (a library removed, a write area become read).
func TestStaleGrantsAreTakenBack(t *testing.T) {
	before := []GrantEntry{{Path: "/media/old", Write: true}, {Path: "/media/a", Write: true}, {Path: "/py"}}
	now := []GrantEntry{{Path: "/media/a"}, {Path: "/py"}}
	got := Stale(before, now)
	want := []GrantEntry{{Path: "/media/old", Write: true}, {Path: "/media/a", Write: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stale %+v, want %+v", got, want)
	}
}

// Only a group with net holds a capability (internetClient); a group with no tools may
// start no child process (the Job Object's limit), one with tools may.
func TestOnlyANetGroupHoldsInternetClient(t *testing.T) {
	if o := (RunnerPolicy{}).RunnerOptions(nil); len(o.Capabilities) != 0 || o.ProcessLimit != 1 {
		t.Fatalf("a group without net: %+v", o)
	}
	o := RunnerPolicy{Net: true, Processes: 34}.RunnerOptions(nil)
	if !reflect.DeepEqual(o.Capabilities, []string{CapInternetClient}) || o.ProcessLimit != 34 {
		t.Fatalf("a net group with tools: %+v", o)
	}
}

type fakeLaunch struct {
	policy   string
	code     int
	granted  []GrantEntry
	revoked  []string
	profile  string
	started  bool
	opts     Options
	grantErr error
	remote   string
}

func (f *fakeLaunch) install(t *testing.T) {
	t.Helper()
	ap, gf, rf, run, rem := askPolicy, grantFn, revokeFn, runFn, remoteFn
	t.Cleanup(func() { askPolicy, grantFn, revokeFn, runFn, remoteFn = ap, gf, rf, run, rem })
	remoteFn = func(p string) bool { return f.remote != "" && p == f.remote }
	askPolicy = func(_ string, args []string, _ io.Writer) ([]byte, int, error) {
		if args[len(args)-1] != "--policy" {
			t.Fatalf("the policy run's arguments: %v", args)
		}
		return []byte(`{"event":"a log line first"}` + "\n" + f.policy + "\n"), f.code, nil
	}
	grantFn = func(profile, path string, write bool) error {
		f.profile = profile
		if f.grantErr != nil {
			return f.grantErr
		}
		f.granted = append(f.granted, GrantEntry{Path: path, Write: write})
		return nil
	}
	revokeFn = func(_, path string) error { f.revoked = append(f.revoked, path); return nil }
	runFn = func(profile, _ string, _ []string, o Options) (int, error) {
		f.started, f.opts, f.profile = true, o, profile
		return 0, nil
	}
}

// The launcher asks the runner for its policy, takes back the grants it no longer has,
// grants the group's own profile, keeps the grants and starts the runner with them.
func TestTheRunnerLauncherGrantsItsGroupsProfileAndStartsIt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MAKTABA_DATA_DIR", dir)
	run, media, old := filepath.Join(dir, "run"), filepath.Join(dir, "media"), filepath.Join(dir, "old")
	_ = os.MkdirAll(media, 0o700)
	_ = os.MkdirAll(old, 0o700)
	grants := filepath.Join(dir, "runner-light.grants")
	b, _ := json.Marshal([]GrantEntry{{Path: old, Write: true}})
	_ = os.WriteFile(grants, b, 0o600)
	pol, _ := json.Marshal(RunnerPolicy{Read: []string{media, filepath.Join(dir, "missing")}, RunDir: run, Processes: 2})
	f := &fakeLaunch{policy: string(pol)}
	f.install(t)
	var errOut strings.Builder
	code := LaunchRunner([]string{"runner-sandbox-run", "-group", "light", "-grants", grants, "--", "python", "-m", "maktaba_plugin_runner"}, &errOut)
	if code != 0 || !f.started {
		t.Fatalf("exit %d, started %v: %s", code, f.started, errOut.String())
	}
	if f.profile != "Maktaba.Runner.light" {
		t.Fatalf("profile %q", f.profile)
	}
	if !reflect.DeepEqual(f.revoked, []string{old}) {
		t.Fatalf("revoked %v", f.revoked)
	}
	wantGranted := []GrantEntry{{Path: media}, {Path: run, Write: true}}
	if !reflect.DeepEqual(f.granted, wantGranted) {
		t.Fatalf("granted %+v, want %+v (a missing area is skipped)", f.granted, wantGranted)
	}
	if info, err := os.Stat(run); err != nil || !info.IsDir() {
		t.Fatalf("the run folder was not made: %v", err)
	}
	if f.opts.ProcessLimit != 2 || len(f.opts.Capabilities) != 0 {
		t.Fatalf("options %+v", f.opts)
	}
	var kept []GrantEntry
	raw, _ := os.ReadFile(grants)
	_ = json.Unmarshal(raw, &kept)
	if len(kept) != 2 {
		t.Fatalf("grants kept %s", raw)
	}
}

// No runner runs unsandboxed: a policy that cannot be read or a grant that fails exits
// the runner's sandbox code before anything starts; the runner's own refusals at its
// policy step (untrusted packages, a secret in its environment) pass through.
func TestTheRunnerLauncherNeverStartsARunnerItCouldNotSandbox(t *testing.T) {
	t.Setenv("MAKTABA_DATA_DIR", t.TempDir())
	args := []string{"-group", "light", "--", "python"}
	for name, f := range map[string]*fakeLaunch{
		"bad policy":   {policy: "not json"},
		"policy fails": {policy: "{}", code: 1},
		"grant fails":  {policy: `{"run_dir":"` + filepath.ToSlash(t.TempDir()) + `"}`, grantErr: errors.New("denied")},
	} {
		f.install(t)
		var errOut strings.Builder
		if code := LaunchRunner(args, &errOut); code != RunnerSandboxExit || f.started {
			t.Fatalf("%s: exit %d, started %v", name, code, f.started)
		}
		if !strings.Contains(errOut.String(), "plugin_runner_sandbox_unavailable") {
			t.Fatalf("%s: %q", name, errOut.String())
		}
	}
	for _, code := range []int{RunnerUntrustedExit, RunnerEnvExit} {
		f := &fakeLaunch{policy: "", code: code}
		f.install(t)
		if got := LaunchRunner(args, io.Discard); got != code || f.started {
			t.Fatalf("the runner's own refusal %d became %d (started %v)", code, got, f.started)
		}
	}
	if got := LaunchRunner([]string{"-group", "../x", "--", "python"}, io.Discard); got != 2 {
		t.Fatalf("a group name that is not one: %d", got)
	}
}

// Launch hands runner-sandbox-run to LaunchRunner (aclaunch and maktaba-server alike).
func TestLaunchHandsARunnerToLaunchRunner(t *testing.T) {
	var out strings.Builder
	if code := Launch([]string{"runner-sandbox-run", "--", "x"}, &out); code != 2 ||
		!strings.Contains(out.String(), "-group") {
		t.Fatalf("%d %q", code, out.String())
	}
}

// A folder on another machine (a share, a mapped drive) is never granted nor revoked: its
// ACL is not the launcher's to change, and an AppContainer cannot reach a share anyway.
func TestTheRunnerLauncherLeavesSharesAlone(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MAKTABA_DATA_DIR", dir)
	share := filepath.Join(dir, "share")
	_ = os.MkdirAll(share, 0o700)
	pol, _ := json.Marshal(RunnerPolicy{WriteNamed: []string{share}, RunDir: filepath.Join(dir, "run")})
	f := &fakeLaunch{policy: string(pol), remote: share}
	f.install(t)
	var errOut strings.Builder
	if code := LaunchRunner([]string{"-group", "light", "--", "python"}, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	for _, g := range f.granted {
		if g.Path == share {
			t.Fatalf("a share was granted: %+v", f.granted)
		}
	}
	if !strings.Contains(errOut.String(), "plugin_runner_area_remote") {
		t.Fatalf("not logged: %q", errOut.String())
	}
	for p, want := range map[string]bool{`\\nas\media`: true, "//nas/media": true, `\\?\C:\media`: false, `C:\media`: false} {
		if uncPath(p) != want {
			t.Fatalf("uncPath(%q) = %v", p, !want)
		}
	}
}
