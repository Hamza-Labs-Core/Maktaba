//go:build windows

package adopt

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
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

// TestRealMove moves a real install out of a real scheduled task: the task's
// definition exported as UTF-16, the task disabled, the server found among the
// processes and ended with its tree, its port freed, the task removed, then
// registered again from the saved definition (undo). It changes the machine's
// tasks, so it runs only where MAKTABA_TEST_WINDOWS_TASK=1 (the windows-latest
// CI runner).
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
		"bin\\maktaba-server.exe serve --no-mdns > maktaba.log 2>&1\r\n")

	sys := RealSys()
	name := fmt.Sprintf(`\Maktaba adopt test %d`, os.Getpid())
	def := filepath.Join(root, "task.xml")
	if err := os.WriteFile(def, utf16File(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><URI>`+name+`</URI></RegistrationInfo>
  <Principals><Principal id="Author"><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>
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
	t.Cleanup(func() {
		_ = sys.EndTask(name)
		_ = sys.DeleteTask(name)
		// what the task started again (Undo runs it) holds the folder's files
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
			procs, _ := sys.Processes()
			left := false
			for _, p := range procs {
				if within(p.Exe, root) {
					left = true
					_ = sys.Kill(p.PID)
				}
			}
			if !left {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	})

	// the old install runs (started here: a sign-in task may not run on a CI
	// runner), as its task would run it
	cmd := exec.Command(`C:\Windows\System32\cmd.exe`, "/c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	for deadline := time.Now().Add(30 * time.Second); !sys.Listening(port); {
		if time.Now().After(deadline) {
			t.Fatalf("the old install's server never listened on %s (%s)", port, read(t, filepath.Join(root, "maktaba.log")))
		}
		time.Sleep(200 * time.Millisecond)
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
	if src.Kind != KindScript || !samePath(src.Dir, root) || !src.ConfigFound || len(src.PIDs) != 1 || !src.TaskEnabled {
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
	if procs, _ := sys.Processes(); len(procs) > 0 {
		for _, p := range procs {
			if within(p.Exe, root) {
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
	if err := Verify(context.Background(), tgt, fakeServer(t, &procs), 0); err != nil {
		t.Fatal(err)
	}
	if ok, err := Finish(sys, tgt.StateDir); !ok || err != nil {
		t.Fatalf("finish: %v %v", ok, err)
	}
	if sys.HasTask(name) {
		t.Fatal("the old task is still there")
	}

	// undo: registered again from the saved definition, enabled and run
	if err := Undo(sys, tgt); err != nil {
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
