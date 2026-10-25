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

// one is the single note of a list, or the test fails.
func one(t *testing.T, what string, notes []Note) Note {
	t.Helper()
	if len(notes) != 1 {
		t.Fatalf("%s: %+v", what, notes)
	}
	return notes[0]
}

func param(n Note, key string) any {
	for i := 0; i+1 < len(n.Params); i += 2 {
		if n.Params[i] == key {
			return n.Params[i+1]
		}
	}
	return nil
}

func TestLinuxCheckOnASupportedSystem(t *testing.T) {
	l := Linux{Glibc: glibc("2.39"), Manager: manager(t, true, true), Unit: unit(t)}
	if r := l.Check(context.Background()); len(r.Problems) != 0 || len(r.Warnings) != 0 {
		t.Errorf("%+v", r)
	}
}

func TestLinuxCheckGlibc(t *testing.T) {
	l := Linux{Glibc: glibc("2.31"), Manager: manager(t, true, true), Unit: unit(t)}
	n := one(t, "old glibc", l.Check(context.Background()).Problems)
	if n.Code != "glibc_old" || param(n, "have") != "2.31" || param(n, "need") != MinGlibc || !strings.Contains(n.Message, "2.35") {
		t.Errorf("old glibc: %+v", n)
	}
	l.Glibc = glibc("")
	if n := one(t, "musl", l.Check(context.Background()).Problems); n.Code != "glibc_missing" {
		t.Errorf("musl: %+v", n)
	}
}

func TestLinuxCheckWithoutAUserManager(t *testing.T) {
	// no systemd as PID 1 (a container, WSL): no login would help, so no
	// remedy about logging in
	l := Linux{Glibc: glibc("2.35"), Manager: manager(t, false, true), Unit: unit(t), User: "media"}
	if n := one(t, "no systemd", l.Check(context.Background()).Problems); n.Code != "systemd_missing" ||
		!strings.Contains(n.Message, "starts with systemd") || strings.Contains(n.Message, "loginctl") {
		t.Errorf("no systemd: %+v", n)
	}
	// no user manager for this user
	m := manager(t, true, true)
	m.Run = func(context.Context, []string) (string, error) { return "", errors.New("Failed to connect to bus") }
	l = Linux{Glibc: glibc("2.35"), Manager: m, Unit: unit(t), User: "media"}
	n := one(t, "no user manager", l.Check(context.Background()).Problems)
	if n.Code != "user_manager_missing" || !strings.Contains(param(n, "reason").(string), "Failed to connect to bus") ||
		param(n, "user") != "media" || !strings.Contains(n.Message, "loginctl enable-linger media") {
		t.Errorf("no user manager: %+v", n)
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
	l := Linux{Glibc: glibc("2.36"), Manager: manager(t, true, true), Unit: u}
	n := one(t, "taken", l.Check(context.Background()).Problems)
	if n.Code != "service_taken" || !strings.Contains(param(n, "runs").(string), "--root /opt/maktaba") {
		t.Errorf("%+v", n)
	}
}

func TestLinuxCheckSeesASystemWideUnit(t *testing.T) {
	m := manager(t, true, true)
	sys := t.TempDir()
	if err := os.WriteFile(filepath.Join(sys, service.AppUnitName), []byte("[Service]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.SystemUnitDirs = []string{sys}
	l := Linux{Glibc: glibc("2.39"), Manager: m, Unit: unit(t)}
	n := one(t, "system-wide", l.Check(context.Background()).Problems)
	if n.Code != "service_system_wide" || param(n, "unit") != filepath.Join(sys, service.AppUnitName) {
		t.Errorf("%+v", n)
	}
}

func TestLinuxCheckWarnsWithoutLinger(t *testing.T) {
	l := Linux{Glibc: glibc("2.35"), Manager: manager(t, true, false), Unit: unit(t)}
	r := l.Check(context.Background())
	if len(r.Problems) != 0 || one(t, "linger", r.Warnings).Code != "linger_off" {
		t.Errorf("%+v", r)
	}
}

func TestOlderThan(t *testing.T) {
	for _, c := range []struct {
		v, least string
		want     bool
	}{{"2.31", "2.35", true}, {"2.35", "2.35", false}, {"2.39", "2.35", false}, {"2.100", "2.35", false}, {"3", "2.35", false}} {
		if got := olderThan(c.v, c.least); got != c.want {
			t.Errorf("olderThan(%s, %s) = %v", c.v, c.least, got)
		}
	}
}

// In the Docker image the entrypoint runs the server: no systemd, no user manager and no
// linger is no problem there; glibc still is.
func TestLinuxCheckInAContainer(t *testing.T) {
	l := Linux{Glibc: glibc("2.36"), Manager: manager(t, false, false), Unit: unit(t), User: "maktaba", Container: "docker"}
	if r := l.Check(context.Background()); len(r.Problems) != 0 || len(r.Warnings) != 0 {
		t.Errorf("a container: %+v", r)
	}
	l.Glibc = glibc("")
	if n := one(t, "musl in a container", l.Check(context.Background()).Problems); n.Code != "glibc_missing" {
		t.Errorf("musl in a container: %+v", n)
	}
}
