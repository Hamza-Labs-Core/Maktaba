//go:build !windows

// The app's user unit is the Linux app's: its paths are POSIX paths.

package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppUnitPathsAndPinnedXDG(t *testing.T) {
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	u := AppUnitFor(getenv, "/home/me", "/home/me/.local/share/maktaba", "/home/me/.local/state/maktaba/logs")
	if u.Path() != filepath.Join("/home/me", ".config", "systemd", "user", "maktaba-server.service") {
		t.Errorf("path %s", u.Path())
	}
	if u.Launcher != filepath.Join("/home/me/.local/share/maktaba", "runtime", "current", "bin", "maktaba-launcher") {
		t.Errorf("launcher %s", u.Launcher)
	}
	if u.LogFile != filepath.Join("/home/me/.local/state/maktaba/logs", "server.log") {
		t.Errorf("log %s", u.LogFile)
	}
	want := []string{"XDG_CONFIG_HOME=" + filepath.Join("/home/me", ".config"), "XDG_DATA_HOME=" + filepath.Join("/home/me", ".local", "share"),
		"XDG_STATE_HOME=" + filepath.Join("/home/me", ".local", "state"), "XDG_CACHE_HOME=" + filepath.Join("/home/me", ".cache")}
	if strings.Join(u.Env, "|") != strings.Join(want, "|") {
		t.Errorf("env %q, want %q", u.Env, want)
	}
	// a shell's own XDG dirs are pinned into the unit (the server at boot has no shell);
	// the unit's directory stays the manager's (UnitDir), not the shell's
	env["XDG_CONFIG_HOME"], env["XDG_CACHE_HOME"] = "/cfg", "relative/ignored"
	u = AppUnitFor(getenv, "/home/me", "/d", "/l")
	if u.Dir != filepath.Join("/home/me", ".config", "systemd", "user") {
		t.Errorf("dir %s", u.Dir)
	}
	if u.Env[0] != "XDG_CONFIG_HOME=/cfg" || u.Env[3] != "XDG_CACHE_HOME="+filepath.Join("/home/me", ".cache") {
		t.Errorf("env %q", u.Env)
	}
}

func TestAppUnitRender(t *testing.T) {
	u := AppUnit{Launcher: "/home/me/.local/share/maktaba/runtime/current/bin/maktaba-launcher",
		LogFile: "/home/me/.local/state/maktaba/logs/server.log",
		Env:     []string{"XDG_DATA_HOME=/home/me/.local/share", `XDG_CACHE_HOME=/home/me/100% "x"`}}
	got := u.Render()
	for _, want := range []string{
		appMarker + "\n[Unit]\n",
		"Environment=\"XDG_DATA_HOME=/home/me/.local/share\"\n",
		`Environment="XDG_CACHE_HOME=/home/me/100%% \"x\""` + "\n",
		"ExecStart=/home/me/.local/share/maktaba/runtime/current/bin/maktaba-launcher --log-file /home/me/.local/state/maktaba/logs/server.log serve\n",
		"Restart=always\n", "RestartForceExitStatus=75\n", "KillMode=mixed\n", "TimeoutStopSec=90\n",
		"WantedBy=default.target\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("unit lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "--root") {
		t.Error("the app's unit runs the runtime's launcher, never a layout root")
	}
}

func TestUnitDirIsTheManagers(t *testing.T) {
	f := &fakeManager{answers: map[string]string{"systemctl --user show-environment": "HOME=/home/me\nXDG_CONFIG_HOME=/srv/cfg\n"}}
	if got := f.manager(t).UnitDir(context.Background(), "/def"); got != filepath.Join("/srv/cfg", "systemd", "user") {
		t.Errorf("with the manager's XDG_CONFIG_HOME: %s", got)
	}
	f = &fakeManager{answers: map[string]string{"systemctl --user show-environment": "HOME=/home/me\n"}}
	if got := f.manager(t).UnitDir(context.Background(), "/def"); got != "/def" {
		t.Errorf("without: %s", got)
	}
}

func TestInstallRefusesASystemWideUnit(t *testing.T) {
	f := &fakeManager{}
	m := f.manager(t)
	sys := t.TempDir()
	m.SystemUnitDirs = []string{filepath.Join(t.TempDir(), "none"), sys}
	if err := os.WriteFile(filepath.Join(sys, AppUnitName), []byte("[Service]\nUser=maktaba\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	u := testUnit(t)
	err := m.Install(context.Background(), u, true, true) // not even with --replace
	if !errors.Is(err, ErrServiceTaken) || !strings.Contains(err.Error(), filepath.Join(sys, AppUnitName)) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(u.Path()); err == nil {
		t.Error("a unit was written beside a system-wide one")
	}
	if st := m.Status(context.Background(), u); st.SystemUnit != filepath.Join(sys, AppUnitName) {
		t.Errorf("status %+v", st)
	}
}

func TestSystemdArgsQuoteAndEscape(t *testing.T) {
	for in, want := range map[string]string{
		"/plain/path":            "/plain/path",
		"/home/my user/x":        `"/home/my user/x"`,
		"/home/100%/x":           "/home/100%%/x",
		"/home/$HOME/x":          "/home/$$HOME/x",
		`/a "quoted" b`:          `"/a \"quoted\" b"`,
		"":                       `""`,
		`/back\slash`:            `"/back\\slash"`,
		"/semi;colon":            `"/semi;colon"`,
		"/home/it's/maktaba-run": `"/home/it's/maktaba-run"`,
	} {
		if got := systemdArgs(in); got != want {
			t.Errorf("systemdArgs(%q) = %s, want %s", in, got, want)
		}
	}
}

// fakeManager records the commands and answers them from a table.
type fakeManager struct {
	ran     [][]string
	answers map[string]string
	fail    map[string]error
}

func (f *fakeManager) run(_ context.Context, argv []string) (string, error) {
	f.ran = append(f.ran, argv)
	key := strings.Join(argv, " ")
	for prefix, err := range f.fail {
		if strings.HasPrefix(key, prefix) {
			return "", err
		}
	}
	for prefix, out := range f.answers {
		if strings.HasPrefix(key, prefix) {
			return out, nil
		}
	}
	return "", nil
}

func (f *fakeManager) manager(t *testing.T) UserManager {
	t.Helper()
	return UserManager{Run: f.run, GOOS: "linux", Booted: func() bool { return true },
		LingerFile: filepath.Join(t.TempDir(), "linger", "me")}
}

func (f *fakeManager) commands() []string {
	var out []string
	for _, a := range f.ran {
		out = append(out, strings.Join(a, " "))
	}
	return out
}

func testUnit(t *testing.T) AppUnit {
	dir := t.TempDir()
	return AppUnit{Launcher: "/rt/current/bin/maktaba-launcher", LogFile: "/logs/server.log",
		Dir: filepath.Join(dir, "systemd", "user")}
}

func TestInstallWritesEnablesAndStarts(t *testing.T) {
	f := &fakeManager{}
	u := testUnit(t)
	if err := f.manager(t).Install(context.Background(), u, true, false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(u.Path())
	if err != nil || string(b) != u.Render() {
		t.Fatalf("unit file: %v\n%s", err, b)
	}
	want := []string{
		"systemctl --user show-environment",
		"systemctl --user daemon-reload",
		"systemctl --user enable --now maktaba-server.service",
	}
	if got := f.commands(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("ran %q, want %q", got, want)
	}
	// installing again over its own unit is fine (no --now: not started)
	f.ran = nil
	if err := f.manager(t).Install(context.Background(), u, false, false); err != nil {
		t.Fatal(err)
	}
	if got := f.commands(); got[len(got)-1] != "systemctl --user enable maktaba-server.service" {
		t.Errorf("without start: %q", got)
	}
}

func TestInstallRefusesAnotherInstallsUnit(t *testing.T) {
	f := &fakeManager{}
	u := testUnit(t)
	other := "[Service]\nExecStart=/usr/local/maktaba/maktaba-launcher --root /usr/local/maktaba serve\n"
	if err := os.MkdirAll(u.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(u.Path(), []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}
	err := f.manager(t).Install(context.Background(), u, true, false)
	if !errors.Is(err, ErrServiceTaken) || !strings.Contains(err.Error(), "--root /usr/local/maktaba") {
		t.Fatalf("err = %v, want service_taken naming the other unit", err)
	}
	if b, _ := os.ReadFile(u.Path()); string(b) != other {
		t.Error("the other install's unit was changed")
	}
	st := f.manager(t).Status(context.Background(), u)
	if st.Installed || !strings.Contains(st.Taken, "--root /usr/local/maktaba") {
		t.Errorf("status %+v", st)
	}
	// replace: it is overwritten
	if err := f.manager(t).Install(context.Background(), u, false, true); err != nil {
		t.Fatal(err)
	}
	if st := f.manager(t).Status(context.Background(), u); !st.Installed || st.Taken != "" {
		t.Errorf("after replace: %+v", st)
	}
	// uninstall never removes another install's unit
	if err := os.WriteFile(u.Path(), []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.manager(t).Uninstall(context.Background(), u); !errors.Is(err, ErrServiceTaken) {
		t.Errorf("uninstall of another unit: %v", err)
	}
	if _, err := os.Stat(u.Path()); err != nil {
		t.Error("another install's unit was removed")
	}
}

func TestStatusParsesSystemd(t *testing.T) {
	f := &fakeManager{answers: map[string]string{
		"systemctl --user show maktaba-server.service": "LoadState=loaded\nUnitFileState=enabled\nActiveState=active\nSubState=running\nMainPID=4242\n",
	}}
	u := testUnit(t)
	m := f.manager(t)
	if err := m.Install(context.Background(), u, true, false); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(m.LingerFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.LingerFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	st := m.Status(context.Background(), u)
	want := AppStatus{Unit: u.Path(), Installed: true, Enabled: true, Active: "active", SubState: "running",
		MainPID: 4242, Linger: true, Manager: "ok"}
	if st != want {
		t.Errorf("status %+v, want %+v", st, want)
	}
}

func TestNoUserManager(t *testing.T) {
	u := testUnit(t)
	for name, m := range map[string]UserManager{
		"not Linux":         {GOOS: "darwin"},
		"systemd not PID 1": {GOOS: "linux", Booted: func() bool { return false }},
		"no user manager": {GOOS: "linux", Booted: func() bool { return true },
			Run: (&fakeManager{fail: map[string]error{"systemctl --user show-environment": errors.New("Failed to connect to bus")}}).run},
	} {
		st := m.Status(context.Background(), u)
		if st.Manager != "unsupported" || st.Reason == "" {
			t.Errorf("%s: %+v", name, st)
		}
		if err := m.Install(context.Background(), u, true, false); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: install %v", name, err)
		}
		if _, err := os.Stat(u.Path()); err == nil {
			t.Errorf("%s: a unit was written", name)
		}
	}
}

func TestUninstallControlAndLinger(t *testing.T) {
	f := &fakeManager{}
	u := testUnit(t)
	m := f.manager(t)
	if err := m.Uninstall(context.Background(), u); err != nil {
		t.Fatalf("nothing installed: %v", err)
	}
	if err := m.Install(context.Background(), u, true, false); err != nil {
		t.Fatal(err)
	}
	f.ran = nil
	for _, a := range []string{"stop", "start", "restart"} {
		if err := m.Control(context.Background(), a); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Control(context.Background(), "kill"); err == nil {
		t.Error("an unknown action was run")
	}
	var asked []string
	m.Ask = func(_ context.Context, argv []string) (string, error) {
		asked = append(asked, strings.Join(argv, " "))
		return "", nil
	}
	if err := m.Linger(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if err := m.Linger(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if err := m.Uninstall(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(u.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Error("the unit is still there")
	}
	got := strings.Join(f.commands(), "|")
	for _, want := range []string{
		"systemctl --user stop maktaba-server.service", "systemctl --user start maktaba-server.service",
		"systemctl --user restart maktaba-server.service", "systemctl --user disable --now maktaba-server.service",
		"systemctl --user daemon-reload",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("did not run %q: %s", want, got)
		}
	}
	if strings.Join(asked, "|") != "loginctl enable-linger|loginctl disable-linger" {
		t.Errorf("linger ran %q (through Ask, which may prompt)", asked)
	}
}

func TestUserManagerEnvFindsTheRuntimeDir(t *testing.T) {
	env := []string{"XDG_RUNTIME_DIR=/run/user/1000", "HOME=/h"}
	if got := userManagerEnv(env); len(got) != 2 {
		t.Errorf("an XDG_RUNTIME_DIR set was changed: %q", got)
	}
}
