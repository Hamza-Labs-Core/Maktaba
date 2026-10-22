//go:build windows

package adopt

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestTheAppsWindowOwnsThePrompt: the desktop app's main window (its handle in
// MAKTABA_SETUP_HWND) is the owner of the consent prompt, so the prompt opens
// in front of the app; without one (the CLI), or with one that names no
// window, there is no owner, as before.
func TestTheAppsWindowOwnsThePrompt(t *testing.T) {
	desk := windows.GetDesktopWindow()
	t.Setenv(ownerEnv, strconv.FormatUint(uint64(desk), 10))
	if got := ownerWindow(); got != desk {
		t.Fatalf("owner %#x, want the window given (%#x)", got, desk)
	}
	info, err := elevateInfo(`C:\app\server\bin\maktaba-server.exe`, []string{FirewallHelperArg, `C:\t\change.json`}, ownerWindow())
	if err != nil {
		t.Fatal(err)
	}
	if info.hwnd != desk || info.nShow != windows.SW_HIDE || info.cbSize != uint32(unsafe.Sizeof(info)) {
		t.Errorf("hwnd %#x, show %d, size %d", info.hwnd, info.nShow, info.cbSize)
	}
	if verb, params := windows.UTF16PtrToString(info.lpVerb), windows.UTF16PtrToString(info.lpParameters); verb != "runas" || params != FirewallHelperArg+` C:\t\change.json` {
		t.Errorf("verb %q, params %q", verb, params)
	}
	for _, v := range []string{"", "junk", "-5", "0"} {
		t.Setenv(ownerEnv, v)
		if got := ownerWindow(); got != 0 {
			t.Errorf("%q: owner %#x", v, got)
		}
	}
	if os.Unsetenv(ownerEnv) == nil && ownerWindow() != 0 {
		t.Error("an owner without MAKTABA_SETUP_HWND")
	}
}

// TestAPromptNotAnsweredInTimeChangesNothing: Windows's prompt (a stand-in:
// Windows is never asked here) is not answered within the wait. The move
// withdraws the change and reports the timeout; the run a late Yes starts
// finds nothing to make. A Yes as the time runs out, whose run took the change
// first, is waited for.
func TestAPromptNotAnsweredInTimeChangesNothing(t *testing.T) {
	prev := shellExecute
	t.Cleanup(func() { shellExecute = prev })
	dir := t.TempDir()
	in, out := filepath.Join(dir, "change.json"), filepath.Join(dir, "result.txt")
	// a change the run would refuse anyway: nothing here can reach the firewall
	change := []byte(`{"remove":["not-the-apps"]}`)
	if err := os.WriteFile(in, change, 0o600); err != nil {
		t.Fatal(err)
	}

	answer, ran := make(chan struct{}), make(chan int, 1)
	shellExecute = func(*shellExecuteInfo) error {
		<-answer // the prompt is up
		ran <- FirewallHelper([]string{in, out})
		return windows.ERROR_CANCELLED
	}
	const wait = 20 * time.Millisecond
	_, err := runElevated(`C:\app\server\bin\maktaba-server.exe`, nil, wait, func() bool { return withdraw(in) })
	var late *PromptTimeoutError
	if !errors.As(err, &late) || late.Wait != wait {
		t.Fatalf("an unanswered prompt: %v", err)
	}
	close(answer)
	if code := <-ran; code != 1 {
		t.Errorf("the late run: exit %d", code)
	}
	if msg, _ := os.ReadFile(out); !strings.Contains(string(msg), "withdrawn") {
		t.Errorf("the late run: %q", msg)
	}

	if testing.Short() {
		return // the rest starts a cmd.exe
	}
	if err := os.WriteFile(in, change, 0o600); err != nil {
		t.Fatal(err)
	}
	shellExecute = func(info *shellExecuteInfo) error {
		if err := os.Rename(in, in+takenSuffix); err != nil { // the run took it
			return err
		}
		time.Sleep(3 * wait) // its start outlasts the wait
		cmd := exec.Command(system32("cmd.exe"), "/c", "exit 3")
		if err := cmd.Start(); err != nil {
			return err
		}
		t.Cleanup(func() { _ = cmd.Wait() })
		h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(cmd.Process.Pid))
		info.hProcess = h
		return err
	}
	code, err := runElevated(`C:\app\server\bin\maktaba-server.exe`, nil, wait, func() bool { return withdraw(in) })
	if err != nil || code != 3 {
		t.Fatalf("a run under way: exit %d, %v", code, err)
	}
}
