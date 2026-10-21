//go:build windows

package pgbundle

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbin"
	"golang.org/x/sys/windows"
)

// adminNow is postgres.exe's own test (pgwin32_is_admin): an enabled
// membership of Administrators or Power Users.
func adminNow() (bool, error) {
	for _, s := range []windows.WELL_KNOWN_SID_TYPE{windows.WinBuiltinAdministratorsSid, windows.WinBuiltinPowerUsersSid} {
		sid, err := windows.CreateWellKnownSid(s)
		if err != nil {
			return false, err
		}
		if in, err := windows.Token(0).IsMember(sid); err != nil || in {
			return in, err
		}
	}
	return false, nil
}

func isAdmin(t *testing.T) bool {
	t.Helper()
	in, err := adminNow()
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// TestMain: with MAKTABA_TEST_ADMIN_CHECK the test binary only reports whether
// it runs as an administrator (TestPostgresTokenIsNoAdministrators' child).
func TestMain(m *testing.M) {
	if os.Getenv("MAKTABA_TEST_HOLD") == "1" {
		// a running server, until it is ended (at most a minute)
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	if os.Getenv("MAKTABA_TEST_ADMIN_CHECK") == "1" {
		in, err := adminNow()
		switch {
		case err != nil:
			fmt.Println(err)
			os.Exit(1)
		case in:
			fmt.Println("admin")
		default:
			fmt.Println("user")
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestPostgresTokenIsNoAdministrators: what restrict starts runs without the
// administrators' rights postgres.exe refuses, whatever this process has (an
// administrator on CI's windows-latest, a split token on a PC).
func TestPostgresTokenIsNoAdministrators(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.run=^$")
	cmd.Env = append(os.Environ(), "MAKTABA_TEST_ADMIN_CHECK=1")
	if err := restrict(cmd); err != nil {
		t.Fatal(err)
	}
	tok, _ := postgresToken()
	if (tok != 0) != isAdmin(t) {
		t.Fatalf("a restricted token (%v) only for an administrator (%v)", tok != 0, isAdmin(t))
	}
	if (cmd.SysProcAttr != nil && cmd.SysProcAttr.Token != 0) != (tok != 0) {
		t.Fatalf("restrict set the token %v, want %v", cmd.SysProcAttr, tok != 0)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "user" {
		t.Fatalf("the child runs as %q", got)
	}
	// once made, the token serves every start (the supervisor restarts postgres)
	again := exec.Command(self, "-test.run=^$")
	again.Env = cmd.Env
	if err := restrict(again); err != nil {
		t.Fatal(err)
	}
	if out, err := again.Output(); err != nil || strings.TrimSpace(string(out)) != "user" {
		t.Fatalf("second start: %q %v", out, err)
	}
	// the restricted token itself, whatever this session is: made, its user
	// given its objects, and a process started with it
	forced := exec.Command(self, "-test.run=^$")
	forced.Env = cmd.Env
	forced.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(forcedToken(t))}
	if out, err := forced.Output(); err != nil || strings.TrimSpace(string(out)) != "user" {
		t.Fatalf("with a restricted token: %q %v", out, err)
	}
}

// forcedToken is a restricted token as an administrator's postgres gets it,
// made from this process's token whatever it is.
func forcedToken(t *testing.T) windows.Token {
	t.Helper()
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	power, err := windows.CreateWellKnownSid(windows.WinBuiltinPowerUsersSid)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := restrictedToken([]*windows.SID{admins, power})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tok.Close() })
	return tok
}

// TestTheSupervisorHoldsARestrictedPostgres: postgres.exe under the restricted
// token is a child the supervisor can still hold, as its process tree does
// for every child (supervisor/proctree_windows.go): created suspended in a
// group of its own, opened with SET_QUOTA, TERMINATE, SUSPEND_RESUME and
// SYNCHRONIZE (what the process's DACL, from the token's default DACL that
// grantUser completes, must allow), put in a kill-on-close job, resumed,
// waited on, and ended with the job.
func TestTheSupervisorHoldsARestrictedPostgres(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.run=^$")
	cmd.Env = append(os.Environ(), "MAKTABA_TEST_HOLD=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Token:         syscall.Token(forcedToken(t)),
		CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP,
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME|
		windows.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
	if err != nil {
		t.Fatalf("the supervisor cannot open postgres under the restricted token: %v", err)
	}
	defer func() { _ = windows.CloseHandle(ph) }()
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(job) }()
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		t.Fatal(err)
	}
	if err := windows.AssignProcessToJobObject(job, ph); err != nil {
		t.Fatalf("put postgres under the restricted token in the supervisor's job: %v", err)
	}
	resume := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")
	if r, _, _ := resume.Call(uintptr(ph)); r != 0 {
		t.Fatalf("resume postgres under the restricted token: NTSTATUS 0x%x", r)
	}
	if ev, err := windows.WaitForSingleObject(ph, 100); err != nil || ev != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("postgres under the restricted token is not running: %d %v", ev, err)
	}
	if err := windows.TerminateJobObject(job, 1); err != nil {
		t.Fatalf("end the job: %v", err)
	}
	if ev, err := windows.WaitForSingleObject(ph, 10_000); err != nil || ev != windows.WAIT_OBJECT_0 {
		t.Fatalf("postgres under the restricted token did not end with its job: %d %v", ev, err)
	}
}

// TestRealPostgresOnWindows runs the whole first start against the pinned
// Postgres for Windows (pgbin): initdb, the bootstrap, the foreground server as
// the supervisor starts it (ServerCommand: a restricted token on CI's
// administrator runner), the readiness check as the app role, a dump, and
// pg_ctl's fast stop. It downloads Postgres, so it runs only where
// MAKTABA_TEST_PG_DOWNLOAD names a folder to keep it in (CI caches it).
func TestRealPostgresOnWindows(t *testing.T) {
	root := os.Getenv("MAKTABA_TEST_PG_DOWNLOAD")
	if root == "" {
		t.Skip("downloads Postgres: MAKTABA_TEST_PG_DOWNLOAD=<folder> (CI)")
	}
	pin, ok := pgbin.Here()
	if !ok {
		t.Skip("no pinned Postgres for this platform")
	}
	ctx := context.Background()
	home, err := pgbin.Ensure(ctx, root, pin, pgbin.HTTPFetch(nil), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	dir := t.TempDir()
	b := Bundle{Home: home, Data: filepath.Join(dir, "pg"), Keys: filepath.Join(dir, "keys"),
		Backups: filepath.Join(dir, "backups"), Port: port}
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	srv, err := b.ServerCommand()
	if err != nil {
		t.Fatal(err)
	}
	// a session that is no administrator's gets no restricted token; run postgres
	// with one anyway, so its user's rights on what postgres creates (grantUser)
	// are checked on every machine, not only on CI's administrator runner
	if srv.SysProcAttr == nil || srv.SysProcAttr.Token == 0 {
		tok := forcedToken(t)
		srv.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(tok)}
	}
	logf, err := os.Create(filepath.Join(dir, "postgres.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logf.Close()
	srv.Stdout, srv.Stderr = logf, logf
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- srv.Wait() }()
	stopped := false
	defer func() {
		if !stopped {
			_ = srv.Process.Kill()
			<-exited
		}
	}()
	deadline := time.Now().Add(60 * time.Second)
	for {
		err := Ready(ctx, b.Addr(), AppRole, AppDatabase)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			raw, _ := os.ReadFile(logf.Name())
			t.Fatalf("not ready: %v\n%s", err, raw)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, err := b.Backup(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	// the supervisor's Stop on Windows: pg_ctl's fast shutdown, a clean exit
	if out, err := b.StopCommand().CombinedOutput(); err != nil {
		t.Fatalf("pg_ctl stop: %v\n%s", err, out)
	}
	select {
	case err := <-exited:
		stopped = true
		if err != nil {
			raw, _ := os.ReadFile(logf.Name())
			t.Fatalf("postgres exited with %v\n%s", err, raw)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("postgres did not stop")
	}
	// the next start finds the cluster made and touches nothing
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
}
