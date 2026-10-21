package hostcheck

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/service"
)

func manager(t *testing.T, ok, linger bool) service.UserManager {
	t.Helper()
	m := service.UserManager{GOOS: "linux", Booted: func() bool { return ok },
		Run:        func(context.Context, []string) (string, error) { return "", nil },
		LingerFile: filepath.Join(t.TempDir(), "linger")}
	if linger {
		if err := os.WriteFile(m.LingerFile, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

func unit(t *testing.T) service.AppUnit {
	return service.AppUnit{Launcher: "/rt/current/bin/maktaba-launcher", LogFile: "/l/server.log",
		Dir: filepath.Join(t.TempDir(), "systemd", "user")}
}

func glibc(v string) func(context.Context) string { return func(context.Context) string { return v } }

func TestLinuxCheckOnASupportedSystem(t *testing.T) {
	l := Linux{GOARCH: "amd64", Glibc: glibc("2.39"), Manager: manager(t, true, true), Unit: unit(t)}
	if r := l.Check(context.Background()); len(r.Problems) != 0 || len(r.Warnings) != 0 {
		t.Errorf("%+v", r)
	}
}

func TestLinuxCheckProblems(t *testing.T) {
	for name, tc := range map[string]struct {
		l    Linux
		want string
	}{
		"old glibc": {Linux{GOARCH: "amd64", Glibc: glibc("2.31")}, "glibc 2.31; Maktaba needs 2.35"},
		"musl":      {Linux{GOARCH: "arm64", Glibc: glibc("")}, "no GNU C library"},
		"riscv":     {Linux{GOARCH: "riscv64", Glibc: glibc("2.39")}, "not riscv64"},
	} {
		tc.l.Manager, tc.l.Unit = manager(t, true, true), unit(t)
		r := tc.l.Check(context.Background())
		if len(r.Problems) != 1 || !strings.Contains(r.Problems[0], tc.want) {
			t.Errorf("%s: %+v", name, r)
		}
	}
	// no systemd as PID 1 (a container, WSL)
	l := Linux{GOARCH: "amd64", Glibc: glibc("2.35"), Manager: manager(t, false, true), Unit: unit(t)}
	if r := l.Check(context.Background()); len(r.Problems) != 1 || !strings.Contains(r.Problems[0], "systemd user service") {
		t.Errorf("no systemd: %+v", r)
	}
	// no user manager for this user
	m := manager(t, true, true)
	m.Run = func(context.Context, []string) (string, error) { return "", errors.New("Failed to connect to bus") }
	l = Linux{GOARCH: "amd64", Glibc: glibc("2.35"), Manager: m, Unit: unit(t)}
	if r := l.Check(context.Background()); len(r.Problems) != 1 || !strings.Contains(r.Problems[0], "Failed to connect to bus") {
		t.Errorf("no user manager: %+v", r)
	}
}

func TestLinuxCheckSeesAnotherInstallsUnit(t *testing.T) {
	u := unit(t)
	if err := os.MkdirAll(u.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(u.Path(), []byte("[Service]\nExecStart=/opt/maktaba/maktaba-launcher --root /opt/maktaba serve\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := Linux{GOARCH: "amd64", Glibc: glibc("2.36"), Manager: manager(t, true, true), Unit: u}
	r := l.Check(context.Background())
	if len(r.Problems) != 1 || !strings.Contains(r.Problems[0], "--root /opt/maktaba") {
		t.Errorf("%+v", r)
	}
}

func TestLinuxCheckSeesASystemWideUnit(t *testing.T) {
	m := manager(t, true, true)
	sys := t.TempDir()
	if err := os.WriteFile(filepath.Join(sys, service.AppUnitName), []byte("[Service]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.SystemUnitDirs = []string{sys}
	l := Linux{GOARCH: "amd64", Glibc: glibc("2.39"), Manager: m, Unit: unit(t)}
	r := l.Check(context.Background())
	if len(r.Problems) != 1 || !strings.Contains(r.Problems[0], "system-wide") || !strings.Contains(r.Problems[0], sys) {
		t.Errorf("%+v", r)
	}
}

func TestLinuxCheckWarnsWithoutLinger(t *testing.T) {
	l := Linux{GOARCH: "arm64", Glibc: glibc("2.35"), Manager: manager(t, true, false), Unit: unit(t)}
	r := l.Check(context.Background())
	if len(r.Problems) != 0 || len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "linger") {
		t.Errorf("%+v", r)
	}
}

func TestOlderThan(t *testing.T) {
	for _, c := range []struct {
		v, min string
		want   bool
	}{{"2.31", "2.35", true}, {"2.35", "2.35", false}, {"2.39", "2.35", false}, {"2.100", "2.35", false}, {"3", "2.35", false}} {
		if got := olderThan(c.v, c.min); got != c.want {
			t.Errorf("olderThan(%s, %s) = %v", c.v, c.min, got)
		}
	}
}
