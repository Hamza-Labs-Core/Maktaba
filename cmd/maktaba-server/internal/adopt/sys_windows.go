//go:build windows

package adopt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// RealSys is the Windows machine: schtasks, a CIM query through PowerShell (a
// process's command line is not readable otherwise), taskkill. Every program is
// started without a window.
func RealSys() Sys { return winSys{} }

type winSys struct{}

func system32(name string) string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", name)
}

func run(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		return out.Bytes(), fmt.Errorf("%s %s: %w: %s", filepath.Base(name), strings.Join(args, " "), err, msg)
	}
	return out.Bytes(), nil
}

func schtasks(args ...string) ([]byte, error) { return run(system32("schtasks.exe"), args...) }

func (winSys) Tasks() ([]Task, error) {
	out, err := schtasks("/Query", "/XML", "ONE")
	if err != nil {
		return nil, err
	}
	return ParseTasks(out)
}

// powershell runs a script with UTF-8 output; values reach it through env
// (never spliced into the script).
func powershell(env []string, script string) ([]byte, error) {
	cmd := exec.Command(system32(`WindowsPowerShell\v1.0\powershell.exe`), "-NoProfile", "-NonInteractive", "-Command",
		"[Console]::OutputEncoding = [Text.Encoding]::UTF8; "+script)
	cmd.Env = append(os.Environ(), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("powershell: %w: %s", err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

// TaskXML is the task's definition as Task Scheduler keeps it, in UTF-16 with
// its byte order mark (what schtasks /Create /XML reads back; schtasks' own
// /Query /XML writes the console's code page under a UTF-16 declaration).
func (winSys) TaskXML(name string) ([]byte, error) {
	i := strings.LastIndexByte(name, '\\')
	out, err := powershell([]string{"MAKTABA_ADOPT_TASK_PATH=" + name[:i+1], "MAKTABA_ADOPT_TASK_NAME=" + name[i+1:]},
		`$t = Get-ScheduledTask -TaskPath $env:MAKTABA_ADOPT_TASK_PATH | Where-Object TaskName -eq $env:MAKTABA_ADOPT_TASK_NAME; `+
			`if (-not $t) { throw "no task $env:MAKTABA_ADOPT_TASK_PATH$env:MAKTABA_ADOPT_TASK_NAME" }; Export-ScheduledTask -InputObject $t`)
	if err != nil {
		return nil, err
	}
	return utf16File(string(bytes.TrimPrefix(out, []byte("\xef\xbb\xbf")))), nil
}

// utf16File is s as UTF-16 little-endian with a byte order mark.
func utf16File(s string) []byte {
	units := utf16.Encode([]rune(strings.TrimSpace(s)))
	b := make([]byte, 2, 2+2*len(units))
	b[0], b[1] = 0xff, 0xfe
	for _, u := range units {
		b = append(b, byte(u), byte(u>>8))
	}
	return b
}

func (winSys) HasTask(name string) bool {
	_, err := schtasks("/Query", "/TN", name)
	return err == nil
}

func (winSys) EndTask(name string) error {
	_, err := schtasks("/End", "/TN", name)
	return err
}

func (winSys) SetTaskEnabled(name string, on bool) error {
	flag := "/DISABLE"
	if on {
		flag = "/ENABLE"
	}
	_, err := schtasks("/Change", "/TN", name, flag)
	return err
}

func (winSys) DeleteTask(name string) error {
	_, err := schtasks("/Delete", "/TN", name, "/F")
	return err
}

func (winSys) CreateTask(name, xmlFile string) error {
	_, err := schtasks("/Create", "/TN", name, "/XML", xmlFile, "/F")
	return err
}

func (winSys) RunTask(name string) error {
	_, err := schtasks("/Run", "/TN", name)
	return err
}

func (winSys) Processes() ([]Proc, error) {
	// Maktaba's programs, and the pipeline's workers (WQL's _ is any one
	// character: maktaba_pipeline and maktaba-pipeline)
	const query = `Get-CimInstance Win32_Process -Filter "Name LIKE 'maktaba-%' OR CommandLine LIKE '%maktaba_pipeline%'" | ` +
		`Select-Object ProcessId,Name,ExecutablePath,CommandLine | ConvertTo-Json -Compress`
	out, err := run(system32(`WindowsPowerShell\v1.0\powershell.exe`), "-NoProfile", "-NonInteractive", "-Command", query)
	if err != nil {
		return nil, err
	}
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return nil, nil
	}
	var procs []Proc
	if out[0] == '{' { // one process: ConvertTo-Json gives an object, not an array
		var p Proc
		if err := json.Unmarshal(out, &p); err != nil {
			return nil, err
		}
		return []Proc{p}, nil
	}
	if err := json.Unmarshal(out, &procs); err != nil {
		return nil, err
	}
	return procs, nil
}

func (winSys) Kill(pid int) error {
	_, err := run(system32("taskkill.exe"), "/F", "/T", "/PID", strconv.Itoa(pid))
	return err
}

func (winSys) ReadFile(path string) ([]byte, error) { return readFile(path) }
func (winSys) Getenv(name string) string            { return os.Getenv(name) }

// User is the user of this process's token: its SID, and the name Task
// Scheduler writes for it.
func (winSys) User() (User, error) {
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return User{}, err
	}
	return User{SID: tu.User.Sid.String(), Name: taskUser(os.Getenv)}, nil
}

// The app's server agent's named objects (contract-agent).
const (
	stopEvent  = `Local\Maktaba.Server.Stop`
	agentMutex = `Local\Maktaba.Server.Agent`
	agentWait  = 90 * time.Second
)

func (winSys) StopAppServer() error {
	ev, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, windows.StringToUTF16Ptr(stopEvent))
	if err != nil {
		return nil // no agent runs in this session
	}
	err = windows.SetEvent(ev)
	_ = windows.CloseHandle(ev)
	if err != nil {
		return fmt.Errorf("ask the app's server to stop: %w", err)
	}
	for deadline := time.Now().Add(agentWait); ; {
		m, err := windows.OpenMutex(windows.SYNCHRONIZE, false, windows.StringToUTF16Ptr(agentMutex))
		if err != nil {
			return nil // the agent has exited
		}
		_ = windows.CloseHandle(m)
		if time.Now().After(deadline) {
			return fmt.Errorf("the app's server did not stop within %s", agentWait)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (winSys) Listening(port string) bool {
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}
