//go:build windows

package adopt

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"golang.org/x/sys/windows"
)

// fakeServeEnv is the port this test binary listens on when it runs as the
// old install's maktaba-server.exe serve.
const fakeServeEnv = "MAKTABA_ADOPT_FAKE_PORT"

// fakeServe is the old install's server for TestRealMove: it holds its port
// until it is killed.
func fakeServe() {
	l, err := net.Listen("tcp", "127.0.0.1:"+os.Getenv(fakeServeEnv))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for {
		c, err := l.Accept()
		if err == nil {
			_ = c.Close()
		}
	}
}

func init() {
	if strings.EqualFold(filepath.Base(os.Args[0]), "maktaba-server.exe") && len(os.Args) > 1 && os.Args[1] == "serve" {
		fakeServe()
	}
	// TestRealFirewallRule's elevated run, as maktaba-server's main makes it
	if len(os.Args) > 1 && os.Args[1] == FirewallHelperArg {
		os.Exit(FirewallHelper(os.Args[2:]))
	}
}

// TestAConsolesDirectoryIsRead: a running cmd.exe's current directory, from
// its process environment block: what its relative script is resolved against.
func TestAConsolesDirectoryIsRead(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a cmd.exe")
	}
	dir := t.TempDir()
	cmd := exec.Command(system32("cmd.exe"), "/c", "ping -n 30 127.0.0.1 >nul")
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	if got := processCwd(cmd.Process.Pid); !samePath(got, dir) {
		t.Errorf("cwd %q, want %q", got, dir)
	}
	if got := processCwd(0); got != "" {
		t.Errorf("no such process: %q", got)
	}
}

// TestTheFirewallIsRead: the firewall's rules and the network's profile are
// read without an administrator (read only: nothing is changed).
func TestTheFirewallIsRead(t *testing.T) {
	if testing.Short() {
		t.Skip("runs PowerShell")
	}
	sys := RealSys()
	rules, err := sys.FirewallRules(t.TempDir())
	if err != nil || len(rules) != 0 {
		t.Errorf("rules of an empty folder: %v %+v", err, rules)
	}
	profiles, err := sys.ActiveProfiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range profiles {
		if p != "Domain" && p != "Private" && p != "Public" {
			t.Errorf("profile %q", p)
		}
	}
}

// TestRealFirewallRule makes, reads and removes a throwaway inbound rule for a
// program in a temp folder through the elevated run, as a move and its Undo do.
// It changes the machine's firewall, so it runs only where
// MAKTABA_TEST_WINDOWS_TASK=1 (the windows-latest CI runner, an administrator).
func TestRealFirewallRule(t *testing.T) {
	if os.Getenv("MAKTABA_TEST_WINDOWS_TASK") != "1" {
		t.Skip("changes the firewall: MAKTABA_TEST_WINDOWS_TASK=1 (CI)")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	// the app's bin: the elevated run (this test binary as maktaba-server.exe)
	// and the program the rule is for
	// the temp folder's long path (a runner's TEMP is 8.3): the firewall keeps that
	bin := filepath.Join(longPath(t.TempDir()), "server", "bin")
	helper, prog := filepath.Join(bin, "maktaba-server.exe"), filepath.Join(bin, "maktaba-streaming.exe")
	for _, p := range []string{helper, prog} {
		write(t, p, "")
		if err := os.WriteFile(p, raw, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	prev := helperExe
	helperExe = func() (string, error) { return helper, nil }
	t.Cleanup(func() { helperExe = prev })

	rule := FirewallRule{Program: prog, Protocol: "TCP", Ports: []string{freePort(t)}, Profiles: "Any",
		Enabled: true, Allow: true, Inbound: true, Group: firewallGroup, Display: "Maktaba adopt test", Description: "a test's own"}
	rule.Name = firewallPrefix + ruleID(rule)
	sys := RealSys()
	t.Cleanup(func() { _ = sys.RemoveFirewallRules([]string{rule.Name}) })
	if err := sys.AddFirewallRules([]FirewallRule{rule}); err != nil {
		t.Fatal(err)
	}
	got, err := sys.FirewallRules(bin)
	if err != nil || len(got) != 1 || got[0].Name != rule.Name || !got[0].serves() || !samePath(got[0].Program, prog) ||
		got[0].Protocol != "TCP" || !reflect.DeepEqual(got[0].Ports, rule.Ports) {
		t.Fatalf("read back: %v %+v", err, got)
	}
	if err := checkFirewall(sys, []FirewallRule{rule}); err != nil {
		t.Errorf("check: %v", err)
	}
	if err := sys.RemoveFirewallRules([]string{rule.Name}); err != nil {
		t.Fatal(err)
	}
	if got, err := sys.FirewallRules(bin); err != nil || len(got) != 0 {
		t.Fatalf("after removing: %v %+v", err, got)
	}
	// the helper refuses what is not the app's
	foreign := rule
	foreign.Program = system32("cmd.exe")
	if err := sys.AddFirewallRules([]FirewallRule{foreign}); err == nil || !strings.Contains(err.Error(), "not one of the app's programs") {
		t.Errorf("a rule for another program: %v", err)
	}
}

// TestTheAppTaskIsThisUsers: the app's task is named by the SID of the user
// this process runs as (the shell's task_name), read from its token; nothing
// is registered.
func TestTheAppTaskIsThisUsers(t *testing.T) {
	u, err := RealSys().User()
	if err != nil {
		t.Fatal(err)
	}
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u.SID, "S-1-") || u.SID != tu.User.Sid.String() {
		t.Errorf("SID %q", u.SID)
	}
	if got, want := AppTask(u), `\Maktaba Server `+u.SID; got != want {
		t.Errorf("AppTask = %q, want %q", got, want)
	}
	if want := taskUser(os.Getenv); u.Name != want {
		t.Errorf("name %q, want %q", u.Name, want)
	}
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
}

// TestRealMove moves a real install out of a real scheduled task: a script
// with a restart loop like this PC's (serve, `timeout /t 3`, `goto run`),
// started by its task under conhost --headless. The task's definition is
// exported as UTF-16, the task disabled, the loop and the server found among
// the processes and ended, its port freed and still free once the loop's delay
// is over; the task removed, then registered again from the saved definition
// (undo), and run. It changes the machine's tasks, so it runs only where
// MAKTABA_TEST_WINDOWS_TASK=1 (the windows-latest CI runner).
func TestRealMove(t *testing.T) {
	if os.Getenv("MAKTABA_TEST_WINDOWS_TASK") != "1" {
		t.Skip("registers a scheduled task: MAKTABA_TEST_WINDOWS_TASK=1 (CI)")
	}
	grace := stopGrace
	stopGrace = 2 * time.Second
	t.Cleanup(func() { stopGrace = grace })

	root := filepath.Join(t.TempDir(), "old install")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "bin", "maktaba-server.exe"), "")
	if err := os.WriteFile(filepath.Join(root, "bin", "maktaba-server.exe"), raw, 0o755); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	write(t, filepath.Join(root, "server.toml"), fmt.Sprintf("[server]\nlisten = \"127.0.0.1:%s\"\nweb_listen = \"127.0.0.1:%s\"\nstream_addr = \"127.0.0.1:%s\"\n",
		port, freePort(t), freePort(t)))
	write(t, filepath.Join(root, "stream-signing.key"), "key")
	script := filepath.Join(root, "start.cmd")
	write(t, script, "@echo off\r\ncd /d \"%~dp0\"\r\nset MAKTABA_CONFIG=%~dp0server.toml\r\nset "+fakeServeEnv+"="+port+"\r\n"+
		":run\r\nbin\\maktaba-server.exe serve --no-mdns > maktaba.log 2>&1\r\ntimeout /t 3 /nobreak >nul\r\ngoto run\r\n")

	sys := RealSys()
	name := fmt.Sprintf(`\Maktaba adopt test %d`, os.Getpid())
	// what the task started (and the loop starts again) holds the folder's files
	endAll := func() {
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
			procs, _ := sys.Processes()
			left := false
			for _, p := range procs {
				if within(p.Exe, root) || (isConsole(p) && strings.Contains(strings.ToLower(p.Cmdline), strings.ToLower(script))) {
					left = true
					_ = sys.Kill(p.PID)
				}
			}
			if !left {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	t.Cleanup(func() {
		_ = sys.EndTask(name)
		_ = sys.DeleteTask(name)
		endAll()
	})
	// the task as this PC's: at the user's sign-in (InteractiveToken). Where
	// no sign-in runs it (a runner without a session), the same task as S4U.
	register := func(principal string) {
		def := filepath.Join(root, "task.xml")
		if err := os.WriteFile(def, utf16File(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><URI>`+name+`</URI></RegistrationInfo>
  <Principals><Principal id="Author">`+principal+`<RunLevel>LeastPrivilege</RunLevel></Principal></Principals>
  <Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><Enabled>true</Enabled><ExecutionTimeLimit>PT0S</ExecutionTimeLimit></Settings>
  <Actions Context="Author"><Exec>
    <Command>C:\Windows\System32\conhost.exe</Command>
    <Arguments>--headless cmd.exe /c "`+script+`"</Arguments>
    <WorkingDirectory>`+root+`</WorkingDirectory>
  </Exec></Actions>
</Task>`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := sys.CreateTask(name, def); err != nil {
			t.Fatal(err)
		}
		if err := sys.RunTask(name); err != nil {
			t.Fatal(err)
		}
	}
	listens := func(d time.Duration) bool {
		for deadline := time.Now().Add(d); !sys.Listening(port); time.Sleep(200 * time.Millisecond) {
			if time.Now().After(deadline) {
				return false
			}
		}
		return true
	}
	register("<LogonType>InteractiveToken</LogonType>")
	if !listens(20 * time.Second) {
		u, err := sys.User()
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("the sign-in task did not run here: the same task as S4U")
		_ = sys.EndTask(name)
		endAll()
		register("<UserId>" + u.SID + "</UserId><LogonType>S4U</LogonType>")
		if !listens(30 * time.Second) {
			t.Fatalf("the old install's server never listened on %s (%s)", port, read(t, filepath.Join(root, "maktaba.log")))
		}
	}

	srcs, err := Find(sys, "")
	if err != nil {
		t.Fatal(err)
	}
	var src Source
	for _, s := range srcs {
		if s.ID == name {
			src = s
		}
	}
	if src.Kind != KindScript || !samePath(src.Dir, root) || !src.ConfigFound || len(src.PIDs) != 1 || !src.TaskEnabled ||
		!src.Loops || src.RestartDelay != 3*time.Second {
		t.Fatalf("found %+v (all: %+v)", src, srcs)
	}

	app := filepath.Join(t.TempDir(), "Maktaba")
	tgt := Target{ConfigPath: filepath.Join(app, "server.toml"), StateDir: app, KeysDir: filepath.Join(app, "keys"),
		NodeConfig: filepath.Join(app, "node.json")}
	plan, err := MakePlan(sys, src, tgt)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := Move(context.Background(), sys, plan, tgt, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sys.Listening(port) {
		t.Error("the old server still holds its port")
	}
	// the loop's delay over, and more: nothing came back
	time.Sleep(src.RestartDelay + 2*time.Second)
	if sys.Listening(port) {
		t.Error("the old server came back after the stop: its restart loop still runs")
	}
	if procs, _ := sys.Processes(); len(procs) > 0 {
		for _, p := range procs {
			if within(p.Exe, root) || src.runsScript(p) {
				t.Errorf("still running: %+v", p)
			}
		}
	}
	if len(rec.Saved) != 1 {
		t.Fatalf("saved %+v", rec.Saved)
	}
	saved, err := os.ReadFile(rec.Saved[0].XML)
	if err != nil || len(saved) < 2 || saved[0] != 0xff || saved[1] != 0xfe {
		t.Fatalf("the saved definition is not UTF-16 with its byte order mark: %v %x", err, saved[:min(len(saved), 4)])
	}
	tasks, _ := sys.Tasks()
	for _, tk := range tasks {
		if tk.Name == name && tk.Enabled {
			t.Error("the old task is still enabled")
		}
	}

	procs := []supervisor.ChildStatus{{Role: supervisor.RoleAPI, Wanted: true, State: supervisor.StateRunning}}
	if err := Verify(context.Background(), sys, tgt, fakeServer(t, &procs), 0); err != nil {
		t.Fatal(err)
	}
	if ok, err := Finish(sys, tgt.StateDir); !ok || err != nil {
		t.Fatalf("finish: %v %v", ok, err)
	}
	if sys.HasTask(name) {
		t.Fatal("the old task is still there")
	}

	// undo: registered again from the saved definition, enabled and run
	if err := Undo(sys, tgt, nil); err != nil {
		t.Fatal(err)
	}
	if !sys.HasTask(name) {
		t.Fatal("the old task is not back")
	}
	tasks, _ = sys.Tasks()
	back := false
	for _, tk := range tasks {
		if tk.Name == name {
			back = tk.Enabled && strings.Contains(tk.Args, script)
		}
	}
	if !back {
		t.Errorf("the task that came back: %+v", tasks)
	}
	// and it ran: the old install's server holds its port again
	for deadline := time.Now().Add(60 * time.Second); !sys.Listening(port); {
		if time.Now().After(deadline) {
			t.Fatalf("the old install's server did not come back on %s after undo", port)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
