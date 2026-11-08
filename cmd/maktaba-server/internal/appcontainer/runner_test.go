package appcontainer

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
	policy    string
	code      int
	granted   []GrantEntry
	revoked   []string
	profile   string
	started   bool
	opts      Options
	prog      string
	grantErr  error
	remote    string
	filesOnly []string
	brokered  BrokerRoots
	brokerErr error
	stopped   bool
}

func (f *fakeLaunch) install(t *testing.T) {
	t.Helper()
	ap, gf, rf, run, rem, gff, bf, sf, slf := askPolicy, grantFn, revokeFn, runFn, remoteFn, grantFilesFn, brokerFn, shareFn, shareLocalFn
	t.Cleanup(func() {
		askPolicy, grantFn, revokeFn, runFn, remoteFn, grantFilesFn, brokerFn, shareFn, shareLocalFn = ap, gf, rf, run, rem, gff, bf, sf, slf
	})
	shareFn = func(p string) bool { return f.remote != "" && p == f.remote }
	shareLocalFn = func(string) (string, bool, error) { return "", false, nil }
	brokerFn = func(_ string, roots BrokerRoots) ([]string, func(uint32, uintptr), func(), error) {
		if f.brokerErr != nil {
			return nil, nil, nil, f.brokerErr
		}
		f.brokered = roots
		return []string{EnvFilesPipe + `=\\.\pipe\x`}, func(uint32, uintptr) {}, func() { f.stopped = true }, nil
	}
	grantFilesFn = func(_, path string) error { f.filesOnly = append(f.filesOnly, path); return nil }
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
	runFn = func(profile, prog string, _ []string, o Options) (int, error) {
		f.started, f.opts, f.profile, f.prog = true, o, profile, prog
		return 0, nil
	}
}

// The launcher asks the runner for its policy, takes back the grants it no longer has,
// grants the group's own profile, keeps the grants and starts the runner with them.
func TestTheRunnerLauncherGrantsItsGroupsProfileAndStartsIt(t *testing.T) {
	dir := mustCanonical(t, t.TempDir())
	t.Setenv("MAKTABA_DATA_DIR", dir)
	run, media, old := filepath.Join(dir, "run"), filepath.Join(dir, "media"), filepath.Join(dir, "old")
	_ = os.MkdirAll(media, 0o700)
	_ = os.MkdirAll(old, 0o700)
	grants := filepath.Join(dir, "runner-light.grants")
	b, _ := json.Marshal([]GrantEntry{{Path: old, Write: true}})
	_ = os.WriteFile(grants, b, 0o600)
	// the policy's run folder is the runner's word; the supervisor's -run-dir wins
	pol, _ := json.Marshal(RunnerPolicy{Read: []string{media, filepath.Join(dir, "missing")}, RunDir: filepath.Join(dir, "elsewhere"), Processes: 2})
	f := &fakeLaunch{policy: string(pol)}
	f.install(t)
	var errOut strings.Builder
	areas := filepath.Join(dir, "areas")
	code := LaunchRunner([]string{"runner-sandbox-run", "-group", "light", "-run-dir", run, "-areas-dir", areas,
		"-grants", grants, "--", "python", "-m", "maktaba_plugin_runner"}, &errOut)
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
	if !reflect.DeepEqual(f.filesOnly, []string{areas}) {
		t.Fatalf("the areas folder: %v", f.filesOnly)
	}
	if f.opts.ProcessLimit != 2 || len(f.opts.Capabilities) != 0 {
		t.Fatalf("options %+v", f.opts)
	}
	// kept: what was granted, and the missing read area (granted when it is back)
	var kept []GrantEntry
	raw, _ := os.ReadFile(grants)
	_ = json.Unmarshal(raw, &kept)
	if len(kept) != 3 {
		t.Fatalf("grants kept %s", raw)
	}
}

// No runner runs unsandboxed: a policy that cannot be read or a grant that fails exits
// the runner's sandbox code before anything starts; the runner's own refusals at its
// policy step (untrusted packages, a secret in its environment) pass through.
func TestTheRunnerLauncherNeverStartsARunnerItCouldNotSandbox(t *testing.T) {
	t.Setenv("MAKTABA_DATA_DIR", t.TempDir())
	run := t.TempDir()
	args := []string{"-group", "light", "-run-dir", run, "--", "python"}
	for name, f := range map[string]*fakeLaunch{
		"bad policy":   {policy: "not json"},
		"policy fails": {policy: "{}", code: 1},
		"grant fails":  {policy: "{}", grantErr: errors.New("denied")},
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
	if got := LaunchRunner([]string{"-group", "../x", "-run-dir", run, "--", "python"}, io.Discard); got != 2 {
		t.Fatalf("a group name that is not one: %d", got)
	}
}

// Launch hands runner-sandbox-run to LaunchRunner (aclaunch and maktaba-server alike).
func TestLaunchHandsARunnerToLaunchRunner(t *testing.T) {
	var out strings.Builder
	if code := Launch([]string{"runner-sandbox-run", "-group", "light", "--", "x"}, &out); code != 2 ||
		!strings.Contains(out.String(), "-group") {
		t.Fatalf("%d %q", code, out.String())
	}
}

// A folder on another machine (a share, a mapped drive) is never granted nor revoked: its
// ACL is not the launcher's to change, and an AppContainer cannot reach a share. One the
// group reads or writes by name is brokered instead: the runner starts with its broker's
// pipe in its environment and the hook that learns its process, and the broker stops when
// it exits; a share the group writes in full is not brokered (its jobs run in the worker).
func TestTheRunnerLauncherLeavesSharesAlone(t *testing.T) {
	dir := mustCanonical(t, t.TempDir())
	t.Setenv("MAKTABA_DATA_DIR", dir)
	share := filepath.Join(dir, "share")
	_ = os.MkdirAll(share, 0o700)
	pol, _ := json.Marshal(RunnerPolicy{WriteNamed: []string{share}, RunDir: filepath.Join(dir, "run")})
	f := &fakeLaunch{policy: string(pol), remote: share}
	f.install(t)
	var errOut strings.Builder
	if code := LaunchRunner([]string{"-group", "light", "-run-dir", filepath.Join(dir, "run"), "--", "python"}, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	for _, g := range f.granted {
		if g.Path == share {
			t.Fatalf("a share was granted: %+v", f.granted)
		}
	}
	if !strings.Contains(errOut.String(), "plugin_runner_area_brokered") || strings.Contains(errOut.String(), "plugin_runner_area_remote") {
		t.Fatalf("not logged as brokered: %q", errOut.String())
	}
	if !reflect.DeepEqual(f.brokered, BrokerRoots{{Path: share, WriteNamed: true}}) || !f.stopped || f.opts.Started == nil {
		t.Fatalf("brokered %+v, stopped %v, hook %v", f.brokered, f.stopped, f.opts.Started != nil)
	}
	if !strings.Contains(strings.Join(f.opts.Env, "\n"), EnvFilesPipe+"=") {
		t.Fatal("the runner was not told its broker's pipe")
	}
	// a share written in full is not brokered; a broker that cannot start leaves the runner
	// to start without its shares (logged)
	full, _ := json.Marshal(RunnerPolicy{Write: []string{share}, RunDir: filepath.Join(dir, "run")})
	f2 := &fakeLaunch{policy: string(full), remote: share}
	f2.install(t)
	errOut.Reset()
	if code := LaunchRunner([]string{"-group", "light", "-run-dir", filepath.Join(dir, "run"), "--", "python"}, &errOut); code != 0 ||
		f2.brokered != nil || !strings.Contains(errOut.String(), "plugin_runner_area_remote") {
		t.Fatalf("a share written in full: exit %d, brokered %+v: %s", code, f2.brokered, errOut.String())
	}
	f3 := &fakeLaunch{policy: string(pol), remote: share, brokerErr: errors.New("no pipe")}
	f3.install(t)
	errOut.Reset()
	if code := LaunchRunner([]string{"-group", "light", "-run-dir", filepath.Join(dir, "run"), "--", "python"}, &errOut); code != 0 ||
		!f3.started || !strings.Contains(errOut.String(), "plugin_runner_broker_unavailable") {
		t.Fatalf("a broker that cannot start: exit %d, started %v: %s", code, f3.started, errOut.String())
	}
	for p, want := range map[string]bool{`\\nas\media`: true, "//nas/media": true, `\\?\C:\media`: false, `C:\media`: false} {
		if uncPath(p) != want {
			t.Fatalf("uncPath(%q) = %v", p, !want)
		}
	}
}

// The policy's areas come from areas.json, which the runner must not be able to steer to
// a folder it should never reach: a grant holding or inside a protected folder (the
// supervisor's token, the keys, the run folders) refuses the start, except the group's own
// run folder; so does a filesystem root.
func TestARunnersPolicyNeverReachesAProtectedFolder(t *testing.T) {
	data := filepath.Join(string(filepath.Separator)+"d", "maktaba")
	run := filepath.Join(data, "run")
	own := filepath.Join(run, "runners", "light")
	keys := filepath.Join(string(filepath.Separator)+"k", "keys")
	protect := []string{run, filepath.Join(data, "supervisor"), keys}
	ok := []GrantEntry{{Path: own, Write: true}, {Path: filepath.Join(data, "thumbnails"), Write: true}, {Path: "/media/a"}}
	if err := CheckGrants(ok, protect, own); err != nil {
		t.Fatalf("the group's own folders: %v", err)
	}
	for _, bad := range []string{data, filepath.Join(run, "runners"), filepath.Join(run, "runners", "net"),
		filepath.Join(data, "supervisor", "local.token"), keys, filepath.Dir(keys), string(filepath.Separator)} {
		if err := CheckGrants([]GrantEntry{{Path: bad}}, protect, own); err == nil {
			t.Errorf("%s was allowed", bad)
		}
	}

	dir := mustCanonical(t, t.TempDir())
	t.Setenv("MAKTABA_DATA_DIR", dir)
	secret := filepath.Join(dir, "supervisor")
	pol, _ := json.Marshal(RunnerPolicy{Read: []string{secret}})
	f := &fakeLaunch{policy: string(pol)}
	f.install(t)
	var errOut strings.Builder
	code := LaunchRunner([]string{"-group", "light", "-run-dir", filepath.Join(dir, "run", "runners", "light"),
		"-protect", secret, "--", "python"}, &errOut)
	if code != RunnerSandboxExit || f.started || len(f.granted) != 0 || !strings.Contains(errOut.String(), "policy_path") {
		t.Fatalf("exit %d, started %v, granted %v: %s", code, f.started, f.granted, errOut.String())
	}
}

// The grants file is the record of what the profile holds: written before any grant (so a
// failed start still knows what to take back), kept across a folder that is not mounted, and
// a file that cannot be read refuses the start rather than forgetting grants.
func TestTheGrantsRecordIsKeptBeforeGranting(t *testing.T) {
	dir := mustCanonical(t, t.TempDir())
	t.Setenv("MAKTABA_DATA_DIR", dir)
	media := filepath.Join(dir, "media")
	_ = os.MkdirAll(media, 0o700)
	grants := filepath.Join(dir, "g.json")
	gone := filepath.Join(dir, "unmounted")
	b, _ := json.Marshal([]GrantEntry{{Path: gone, Write: true}})
	_ = os.WriteFile(grants, b, 0o600)
	pol, _ := json.Marshal(RunnerPolicy{Read: []string{media}})
	f := &fakeLaunch{policy: string(pol), grantErr: errors.New("denied")}
	f.install(t)
	args := []string{"-group", "light", "-run-dir", filepath.Join(dir, "run"), "-grants", grants, "--", "python"}
	if code := LaunchRunner(args, io.Discard); code != RunnerSandboxExit {
		t.Fatalf("exit %d", code)
	}
	raw, _ := os.ReadFile(grants)
	for _, want := range []string{gone, media} {
		if !strings.Contains(string(raw), filepath.ToSlash(want)) && !strings.Contains(string(raw), strings.ReplaceAll(want, `\`, `\\`)) {
			t.Fatalf("%s is not in the record after a failed grant: %s", want, raw)
		}
	}
	_ = os.WriteFile(grants, []byte("not json"), 0o600)
	f2 := &fakeLaunch{policy: string(pol)}
	f2.install(t)
	var errOut strings.Builder
	if code := LaunchRunner(args, &errOut); code != RunnerSandboxExit || f2.started || !strings.Contains(errOut.String(), "grants_file") {
		t.Fatalf("an unreadable record: exit %d, started %v: %s", code, f2.started, errOut.String())
	}
}

// A policy path is compared as the filesystem names it: a link into a protected folder is
// refused like the folder itself, and a spelling Windows reads otherwise (a verbatim or
// device prefix, a trailing dot or space) is refused outright.
func TestAPolicyPathIsComparedAsTheFilesystemNamesIt(t *testing.T) {
	dir := mustCanonical(t, t.TempDir())
	t.Setenv("MAKTABA_DATA_DIR", dir)
	secret := filepath.Join(dir, "supervisor")
	_ = os.MkdirAll(secret, 0o700)
	link := filepath.Join(dir, "innocent")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	run := filepath.Join(dir, "run", "runners", "light")
	for _, bad := range []string{link, `\\?\C:\data`, `\\.\C:\data`, filepath.Join(dir, "media."), filepath.Join(dir, "media ")} {
		pol, _ := json.Marshal(RunnerPolicy{Read: []string{bad}})
		f := &fakeLaunch{policy: string(pol)}
		f.install(t)
		var errOut strings.Builder
		code := LaunchRunner([]string{"-group", "light", "-run-dir", run, "-protect", secret, "--", "python"}, &errOut)
		if code != RunnerSandboxExit || f.started || !strings.Contains(errOut.String(), "policy_path") {
			t.Errorf("%s: exit %d, started %v: %s", bad, code, f.started, errOut.String())
		}
	}
	if got, want := mustCanonical(t, filepath.Join(link, "not-yet", "x")), filepath.Join(mustCanonical(t, secret), "not-yet", "x"); got != want {
		t.Fatalf("canonical of a path not made yet: %s, want %s", got, want)
	}
	// a dangling link is not walked past
	dangling := filepath.Join(dir, "dangling")
	_ = os.Symlink(filepath.Join(dir, "nowhere"), dangling)
	if _, err := canonical(filepath.Join(dangling, "x")); err == nil {
		t.Fatal("a path through a dangling link resolved")
	}
	for p, want := range map[string]bool{`C:\media\show`: false, `C:\media\file.txt:secret`: true, `C:\media\NUL`: true,
		`C:\media\com1.txt`: true, `C:\media\console`: false, `C:\media\x.`: true, "/media/a": false} {
		if badSpelling(p) != want {
			t.Errorf("badSpelling(%q) = %v", p, !want)
		}
	}
}

// A grant's walk on Windows recomputes, for each child of a granted folder, the profile's
// inherited entries from its parent's as Windows' inheritance rules have it: a folder-wide
// grant (object and container inherit) reaches every folder and file below; the areas
// folder's grant (object inherit, no propagate) reaches its files and none of its folders;
// an inherit-only entry passes on without applying; a junction or symbolic link (a name
// surrogate) is never walked through, deduplicated or cloud files are; and the
// filesystem's final name of a handle reads as the path it was checked as.
func TestAGrantsWalkFollowsWindowsInheritanceAndNoLink(t *testing.T) {
	const oi, ci, np, io, inh = aceObjectInherit, aceContainerInherit, aceNoPropagate, aceInheritOnly, aceInherited
	for _, c := range []struct {
		flags  uint8
		dir    bool
		want   uint8
		passes bool
	}{
		{oi | ci, true, inh | oi | ci, true},
		{oi | ci, false, inh, true},
		{oi | np, true, 0, false},
		{oi | np, false, inh, true},
		{oi, true, inh | io | oi, true},
		{inh | io | oi, false, inh, true},
		{inh | io | oi, true, inh | io | oi, true},
		{ci, false, 0, false},
		{ci | np, true, inh, true},
		{0, true, 0, false},
		{0, false, 0, false},
	} {
		got, ok := inheritedFlags(c.flags, c.dir)
		if ok != c.passes || (ok && got != c.want) {
			t.Errorf("flags 0x%02x to a child (folder %v): 0x%02x %v, want 0x%02x %v", c.flags, c.dir, got, ok, c.want, c.passes)
		}
	}
	for tag, want := range map[uint32]bool{0xA0000003: true, 0xA000000C: true, 0x80000013: false, 0x9000001A: false, 0x8000001B: false} {
		if nameSurrogate(tag) != want {
			t.Errorf("nameSurrogate(0x%08x) = %v", tag, !want)
		}
	}
	for in, want := range map[string]string{`\\?\C:\media\a`: `C:\media\a`, `\\?\UNC\nas\media`: `\\nas\media`, `C:\x`: `C:\x`} {
		if got := finalPathName(in); got != want {
			t.Errorf("finalPathName(%q) = %q, want %q", in, got, want)
		}
	}
}

// A grant or revoke happens only after the path's last check, on the canonical path: the
// hook between the two (where the Windows tests swap a folder) sees each change's path
// once, after the policy was checked. A recorded grant whose path is now reached through a
// link is not taken back there: the start fails closed, the revoke never runs. The areas
// folder is granted as the filesystem names it.
func TestAGrantOrRevokeComesAfterItsLastCheck(t *testing.T) {
	dir := mustCanonical(t, t.TempDir())
	t.Setenv("MAKTABA_DATA_DIR", dir)
	media, old := filepath.Join(dir, "media"), filepath.Join(dir, "old")
	_ = os.MkdirAll(media, 0o700)
	_ = os.MkdirAll(old, 0o700)
	grants := filepath.Join(dir, "g.json")
	b, _ := json.Marshal([]GrantEntry{{Path: old}})
	_ = os.WriteFile(grants, b, 0o600)
	pol, _ := json.Marshal(RunnerPolicy{Read: []string{media}})
	f := &fakeLaunch{policy: string(pol)}
	f.install(t)
	ac := afterCheck
	t.Cleanup(func() { afterCheck = ac })
	var seen []string
	afterCheck = func(step, p string) {
		seen = append(seen, step+" "+p)
		for _, g := range f.granted {
			if g.Path == p {
				t.Errorf("the hook ran after the grant on %s", p)
			}
		}
	}
	run := filepath.Join(dir, "run")
	args := []string{"-group", "light", "-run-dir", run, "-grants", grants, "-areas-dir", filepath.Join(dir, ".", "areas"), "--", "python"}
	if code := LaunchRunner(args, io.Discard); code != 0 {
		t.Fatalf("exit %d", code)
	}
	want := []string{"revoke " + old, "grant " + media, "grant " + run}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("hook saw %v, want %v", seen, want)
	}
	if !reflect.DeepEqual(f.filesOnly, []string{filepath.Join(dir, "areas")}) {
		t.Fatalf("the areas folder granted as %v", f.filesOnly)
	}

	// the recorded folder is now a link to another one: refused before any revoke
	elsewhere := filepath.Join(dir, "elsewhere")
	_ = os.MkdirAll(elsewhere, 0o700)
	_ = os.RemoveAll(old)
	if err := os.Symlink(elsewhere, old); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	_ = os.WriteFile(grants, b, 0o600)
	f2 := &fakeLaunch{policy: string(pol)}
	f2.install(t)
	var errOut strings.Builder
	if code := LaunchRunner(args, &errOut); code != RunnerSandboxExit || f2.started || len(f2.revoked) != 0 ||
		!strings.Contains(errOut.String(), `"step":"revoke"`) {
		t.Fatalf("a recorded path now a link: exit %d, started %v, revoked %v: %s", code, f2.started, f2.revoked, errOut.String())
	}
}

// A runner's broker serves only its share folders, each path spelled one way: a share
// path at or under a brokered root (any case, as Windows compares), never another share,
// a traversal, a stream, a device, a trailing dot or an 8.3 alias; a write only a
// subtitle name in a folder the group writes by name, never the folder itself nor any
// other name; and only the policy's read and named-write shares are brokered.
func TestARunnersBrokerServesOnlyItsShareFolders(t *testing.T) {
	roots := BrokerRoots{{Path: `\\nas\media\shows`, WriteNamed: true}, {Path: `\\nas\media\films`}}
	for path, want := range map[string]bool{
		`\\nas\media\shows\a\ep1.mkv`:      true,
		`\\NAS\Media\Shows\a\EP1.mkv`:      true,
		"//nas/media/shows/a/ep1.mkv":      true,
		`\\nas\media\shows`:                true,
		`\\nas\media\films\f.mkv`:          true,
		`\\nas\media\showsx\a.mkv`:         false,
		`\\nas\media\extra\a.mkv`:          false,
		`\\nas\second\shows\a.mkv`:         false,
		`\\evil\media\shows\a.mkv`:         false,
		`\\nas\media\shows\..\secret.txt`:  false,
		`\\nas\media\shows\.\a.mkv`:        false,
		`\\nas\media\shows\\a.mkv`:         false,
		`\\nas\media\shows\a.mkv:stream`:   false,
		`\\nas\media\shows\NUL`:            false,
		`\\nas\media\shows\a.mkv.`:         false,
		`\\nas\media\shows\SHOWSA~1\a.mkv`: false,
		`\\?\UNC\nas\media\shows\a.mkv`:    false,
		`C:\media\shows\a.mkv`:             false,
	} {
		if _, _, err := roots.Confine(path, false); (err == nil) != want {
			t.Errorf("read %s: %v", path, err)
		}
	}
	for path, want := range map[string]bool{
		`\\nas\media\shows\a\ep1.en.srt`:             true,
		`\\nas\media\shows\a\ep1.ar.vtt.maktaba-tmp`: true,
		`\\nas\media\shows\a\ep1.mkv`:                false,
		`\\nas\media\shows\a\ep1.srt`:                false,
		`\\nas\media\shows\a\notes.txt`:              false,
		`\\nas\media\shows\a\EP1~1.SRT`:              false,
		`\\nas\media\shows`:                          false,
		`\\nas\media\films\f.en.srt`:                 false,
	} {
		if _, _, err := roots.Confine(path, true); (err == nil) != want {
			t.Errorf("write %s: %v", path, err)
		}
	}
	// Windows' upcase table is not Go's: a root compares by ASCII case only, so U+0130 or
	// the Kelvin sign never makes a request name a sibling folder as the root
	uni := BrokerRoots{{Path: "\\\\nas\\media\\\u0130stanbul", WriteNamed: true}, {Path: "\\\\nas\\media\\\u212aids"}}
	for _, path := range []string{`\\nas\media\istanbul\a.mkv`, `\\nas\media\kids\a.mkv`, "\\\\nas\\media\\\u0130stanbul.mkv"} {
		if _, _, err := uni.Confine(path, false); err == nil {
			t.Errorf("%s was confined to %+v", path, uni)
		}
	}
	if _, _, err := uni.Confine("\\\\nas\\media\\\u0130stanbul\\a.mkv", false); err != nil {
		t.Errorf("the root's own spelling: %v", err)
	}
	remote := func(p string) bool { return uncPath(p) }
	got := brokerRootsOf(RunnerPolicy{
		Read:       []string{`\\nas\a`, `\\nas\b`, "/local", `\\nas\c`},
		WriteNamed: []string{`\\nas\a`},
		Write:      []string{`\\nas\c`},
		Python:     []string{`\\nas\py`},
	}, remote)
	// a share written in full (\\nas\c) is served for reads only: never WriteNamed, so every
	// write, rename and removal there is refused (Confine)
	want := BrokerRoots{{Path: `\\nas\a`, WriteNamed: true}, {Path: `\\nas\b`}, {Path: `\\nas\c`}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("brokered %+v, want %+v", got, want)
	}
	for _, path := range []string{`\\nas\c\show\ep1.en.srt`, `\\nas\c\show\ep1.mp4`, `\\nas\c\show\ep1.en.srt.maktaba-tmp`} {
		if _, _, err := got.Confine(path, true); err == nil {
			t.Errorf("a write of %s in a folder the group writes in full was confined", path)
		}
		if _, _, err := got.Confine(path, false); err != nil {
			t.Errorf("a read of %s: %v", path, err)
		}
	}

	// an administrative share is never brokered, nor a share this machine serves from a
	// folder that holds or is inside a protected one
	none := func(string) (string, bool, error) { return "", false, nil }
	for _, r := range []string{`\\nas\C$\media`, `\\nas\admin$`, `\\localhost\IPC$`, `\\nas`} {
		if _, _, err := (BrokerRoots{{Path: r}}).check(none, nil, ""); err == nil {
			t.Errorf("%s was brokered", r)
		}
	}
	if _, _, err := (BrokerRoots{{Path: `\\nas\media$\shows`}}).check(none, nil, ""); err != nil {
		t.Errorf("a hidden share that is not administrative: %v", err)
	}
	dir := mustCanonical(t, t.TempDir())
	secret := filepath.Join(dir, "supervisor")
	_ = os.MkdirAll(filepath.Join(dir, "media"), 0o700)
	at := func(local string) func(string) (string, bool, error) {
		return func(string) (string, bool, error) { return local, true, nil }
	}
	if _, _, err := (BrokerRoots{{Path: `\\localhost\d`}}).check(at(dir), []string{secret}, ""); err == nil {
		t.Error("this machine's share of the folder holding the supervisor's was brokered")
	}
	if _, _, err := (BrokerRoots{{Path: `\\localhost\s`}}).check(at(filepath.Join(secret, "x")), []string{secret}, ""); err == nil {
		t.Error("this machine's share inside the supervisor's folder was brokered")
	}
	if kept, _, err := (BrokerRoots{{Path: `\\localhost\m`}}).check(at(filepath.Join(dir, "media")), []string{secret}, ""); err != nil || len(kept) != 1 {
		t.Errorf("this machine's share of a library: %v", err)
	}
	// a share of this machine that cannot be looked up is left out (its jobs: the worker)
	lookupFails := func(string) (string, bool, error) { return "", false, errors.New("access denied") }
	kept, dropped, err := (BrokerRoots{{Path: `\\localhost\m`}, {Path: `\\nas\media`}}).check(lookupFails2(lookupFails), nil, "")
	if err != nil || !reflect.DeepEqual(kept, BrokerRoots{{Path: `\\nas\media`}}) || !reflect.DeepEqual(dropped, BrokerRoots{{Path: `\\localhost\m`}}) {
		t.Errorf("a share that cannot be looked up: kept %+v, dropped %+v, %v", kept, dropped, err)
	}
}

// lookupFails2 fails the lookup for this machine's shares only.
func lookupFails2(fail func(string) (string, bool, error)) func(string) (string, bool, error) {
	return func(root string) (string, bool, error) {
		if strings.HasPrefix(strings.ToLower(root), `\\localhost\`) {
			return fail(root)
		}
		return "", false, nil
	}
}

func mustCanonical(t *testing.T, p string) string {
	t.Helper()
	c, err := canonical(p)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A venv's python on Windows is a launcher whose child is the runner: the launcher starts
// the base interpreter itself (the process the broker serves), told its venv; a python of
// its own is started as named; a venv whose base interpreter cannot be named refuses the
// start, before any grant, and never falls back to the launcher.
func TestARunnerInAVenvIsStartedAsItsBaseInterpreter(t *testing.T) {
	old := venvDirect
	venvDirect = true
	t.Cleanup(func() { venvDirect = old })
	dir := mustCanonical(t, t.TempDir())
	t.Setenv("MAKTABA_DATA_DIR", dir)
	// inherited hints are never passed on
	t.Setenv(pyvenvLauncherEnv, `C:\elsewhere\python.exe`)
	t.Setenv(EnvRunnerVenv, `C:\elsewhere`)
	home := filepath.Join(dir, "python")
	base := filepath.Join(home, "python.exe")
	venv := filepath.Join(dir, "venv")
	venvPy := filepath.Join(venv, "Scripts", "python.exe")
	for _, p := range []string{base, venvPy} {
		_ = os.MkdirAll(filepath.Dir(p), 0o700)
		_ = os.WriteFile(p, []byte("exe"), 0o700)
	}
	cfg := filepath.Join(venv, "pyvenv.cfg")
	_ = os.WriteFile(cfg, []byte("home = "+home+"\r\ninclude-system-site-packages = false\r\n"), 0o600)
	pol, _ := json.Marshal(RunnerPolicy{Processes: 1})
	launch := func(py string) (*fakeLaunch, int, string) {
		t.Helper()
		f := &fakeLaunch{policy: string(pol)}
		f.install(t)
		var errOut strings.Builder
		code := LaunchRunner([]string{"runner-sandbox-run", "-group", "light", "-run-dir", filepath.Join(dir, "run"),
			"--", py, "-m", "maktaba_plugin_runner"}, &errOut)
		return f, code, errOut.String()
	}
	envOf := func(f *fakeLaunch, key string) []string {
		var out []string
		for _, kv := range f.opts.Env {
			if k, v, _ := strings.Cut(kv, "="); strings.EqualFold(k, key) {
				out = append(out, v)
			}
		}
		return out
	}
	f, code, out := launch(venvPy)
	if code != 0 || !f.started || f.prog != base {
		t.Fatalf("exit %d, started %v %q: %s", code, f.started, f.prog, out)
	}
	if got := envOf(f, pyvenvLauncherEnv); !slices.Equal(got, []string{venvPy}) {
		t.Fatalf("%s %v, want only %s", pyvenvLauncherEnv, got, venvPy)
	}
	if got := envOf(f, EnvRunnerVenv); !slices.Equal(got, []string{venv}) {
		t.Fatalf("%s %v, want only %s", EnvRunnerVenv, got, venv)
	}
	if !strings.Contains(out, `"plugin_runner_venv_direct"`) {
		t.Fatalf("the direct start is not said: %s", out)
	}
	f, code, _ = launch(base)
	if code != 0 || f.prog != base || len(envOf(f, pyvenvLauncherEnv)) != 0 || len(envOf(f, EnvRunnerVenv)) != 0 {
		t.Fatalf("a python of its own: exit %d, started %q with %v", code, f.prog, f.opts.Env)
	}
	// "executable" wins over "home"
	exe := filepath.Join(dir, "other", "python.exe")
	_ = os.MkdirAll(filepath.Dir(exe), 0o700)
	_ = os.WriteFile(exe, []byte("exe"), 0o700)
	_ = os.WriteFile(cfg, []byte("home = "+home+"\nexecutable = "+exe+"\n"), 0o600)
	if f, code, out = launch(venvPy); code != 0 || f.prog != exe {
		t.Fatalf("executable: exit %d, started %q: %s", code, f.prog, out)
	}
	// every venv whose base cannot be named refuses the start, before any grant or start
	otherVenv := filepath.Join(dir, "venv2", "Scripts", "python.exe")
	_ = os.MkdirAll(filepath.Dir(otherVenv), 0o700)
	_ = os.WriteFile(otherVenv, []byte("exe"), 0o700)
	_ = os.WriteFile(filepath.Join(dir, "venv2", "pyvenv.cfg"), []byte("home = "+home+"\n"), 0o600)
	for name, body := range map[string]string{
		"a base that is gone":     "home = " + filepath.Join(dir, "gone") + "\n",
		"no base named":           "include-system-site-packages = false\n",
		"not a key = value file":  "\x00\x01garbage",
		"a relative base":         "executable = python\\python.exe\n",
		"the venv's own python":   "executable = " + venvPy + "\n",
		"a folder":                "executable = " + home + "\n",
		"another venv's launcher": "executable = " + otherVenv + "\n",
	} {
		_ = os.WriteFile(cfg, []byte(body), 0o600)
		f, code, out := launch(venvPy)
		if code != RunnerSandboxExit || f.started || len(f.granted) > 0 || !strings.Contains(out, `"step":"venv"`) {
			t.Errorf("%s: exit %d, started %v, granted %v: %s", name, code, f.started, f.granted, out)
		}
	}
	// a pyvenv.cfg that cannot be read (a folder in its place) refuses the start too
	_ = os.Remove(cfg)
	_ = os.Mkdir(cfg, 0o700)
	if f, code, out := launch(venvPy); code != RunnerSandboxExit || f.started {
		t.Errorf("an unreadable pyvenv.cfg: exit %d, started %v: %s", code, f.started, out)
	}
}

// venvBase on its own: where CPython finds pyvenv.cfg (beside the program, else one up),
// and no pyvenv.cfg at all is a python of its own.
func TestVenvBaseFindsTheConfigAsCPythonDoes(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "base")
	base := filepath.Join(home, "python.exe")
	flat := filepath.Join(dir, "flat", "python.exe")
	plain := filepath.Join(dir, "plain", "bin", "python.exe")
	for _, p := range []string{base, flat, plain} {
		_ = os.MkdirAll(filepath.Dir(p), 0o700)
		_ = os.WriteFile(p, []byte("exe"), 0o700)
	}
	_ = os.WriteFile(filepath.Join(dir, "flat", "pyvenv.cfg"), []byte("home="+home+"\n"), 0o600)
	if b, v, err := venvBase(flat); err != nil || b != base || v != filepath.Join(dir, "flat") {
		t.Fatalf("beside the program: %q %q %v", b, v, err)
	}
	if b, v, err := venvBase(plain); err != nil || b != plain || v != "" {
		t.Fatalf("no pyvenv.cfg: %q %q %v", b, v, err)
	}
}

func TestWithoutEnvDropsKeysInAnyCase(t *testing.T) {
	got := withoutEnv([]string{"A=1", "__pyvenv_launcher__=x", "MAKTABA_RUNNER_VENV=y", "B=2"}, pyvenvLauncherEnv, EnvRunnerVenv)
	if !slices.Equal(got, []string{"A=1", "B=2"}) {
		t.Fatalf("%v", got)
	}
}
