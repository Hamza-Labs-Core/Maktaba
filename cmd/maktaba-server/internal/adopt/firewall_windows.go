//go:build windows

package adopt

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The firewall is read through its PowerShell module (NetSecurity): every
// value is an enum's invariant name or a path, never localized text. Values
// reach the scripts through the environment or stdin, never spliced in.
const firewallQuery = `$dir = [IO.Path]::GetFullPath($env:MAKTABA_ADOPT_FW_DIR).TrimEnd('\') + '\'
$out = @()
foreach ($f in @(Get-NetFirewallApplicationFilter -PolicyStore PersistentStore -ErrorAction Stop)) {
  $prog = [Environment]::ExpandEnvironmentVariables([string]$f.Program)
  if (-not $prog -or -not [IO.Path]::IsPathRooted($prog)) { continue }
  if (-not $prog.StartsWith($dir, [StringComparison]::OrdinalIgnoreCase)) { continue }
  foreach ($r in @($f | Get-NetFirewallRule -ErrorAction SilentlyContinue)) {
    $p = $r | Get-NetFirewallPortFilter
    $out += [pscustomobject]@{
      name = [string]$r.Name; display = [string]$r.DisplayName; program = $prog
      protocol = [string]$p.Protocol; ports = @($p.LocalPort | ForEach-Object { [string]$_ })
      profiles = $r.Profile.ToString(); enabled = ($r.Enabled.ToString() -eq 'True')
      allow = ($r.Action.ToString() -eq 'Allow'); inbound = ($r.Direction.ToString() -eq 'Inbound')
    }
  }
}
ConvertTo-Json -InputObject @($out) -Compress -Depth 4`

// FirewallRules are the inbound and outbound rules bound to a program inside
// dir (read only; the user needs no rights for it).
func (winSys) FirewallRules(dir string) ([]FirewallRule, error) {
	out, err := powershell([]string{"MAKTABA_ADOPT_FW_DIR=" + dir}, firewallQuery)
	if err != nil {
		return nil, err
	}
	var rules []FirewallRule
	if err := jsonList(out, &rules); err != nil {
		return nil, fmt.Errorf("the firewall's rules: %w", err)
	}
	for i := range rules {
		rules[i].Program = filepath.Clean(rules[i].Program)
	}
	return rules, nil
}

// ActiveProfiles are the firewall profiles of the networks this PC is on now
// (Domain, Private, Public).
func (winSys) ActiveProfiles() ([]string, error) {
	out, err := powershell(nil, `ConvertTo-Json -Compress -InputObject @(Get-NetConnectionProfile -ErrorAction Stop | ForEach-Object { $_.NetworkCategory.ToString() })`)
	if err != nil {
		return nil, err
	}
	var cats []string
	if err := jsonList(out, &cats); err != nil {
		return nil, err
	}
	for i, c := range cats {
		if strings.EqualFold(c, "DomainAuthenticated") {
			cats[i] = "Domain"
		}
	}
	return cats, nil
}

// CanElevate: this user may run a program as an administrator after one
// consent prompt (an administrator under UAC, or one already elevated). A
// standard user would have to type another account's password.
func (winSys) CanElevate() bool {
	tok := windows.GetCurrentProcessToken()
	if tok.IsElevated() {
		return true
	}
	var typ uint32
	var n uint32
	if err := windows.GetTokenInformation(tok, windows.TokenElevationType, (*byte)(unsafe.Pointer(&typ)), uint32(unsafe.Sizeof(typ)), &n); err == nil && typ == 3 {
		return true // TokenElevationTypeLimited: an administrator's filtered token
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return false
	}
	member, err := tok.IsMember(admins)
	return err == nil && member
}

func (winSys) AddFirewallRules(rules []FirewallRule) error {
	return runFirewallHelper(firewallChange{Add: rules})
}

func (winSys) RemoveFirewallRules(names []string) error {
	return runFirewallHelper(firewallChange{Remove: names})
}

// helperExe is the maktaba-server the elevated run starts: this one (the
// app's payload's; tests point it at a copy of the test binary).
var helperExe = os.Executable

// ErrDeclined: the administrator's prompt was refused.
var ErrDeclined = errors.New("the change was not allowed (Windows asked, and No was chosen)")

// runFirewallHelper runs this maktaba-server once as an administrator
// (ShellExecuteEx "runas": one consent prompt, hidden: no console window) to
// make the change, its input in a file of this user's, and waits for it.
func runFirewallHelper(c firewallChange) error {
	exe, err := helperExe()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "maktaba-firewall-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	in, out := filepath.Join(dir, "change.json"), filepath.Join(dir, "result.txt")
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.WriteFile(in, raw, 0o600); err != nil {
		return err
	}
	code, err := runElevated(exe, []string{FirewallHelperArg, in, out})
	if err != nil {
		return err
	}
	if code != 0 {
		msg, _ := os.ReadFile(out)
		return fmt.Errorf("the firewall change failed (exit %d): %s", code, strings.TrimSpace(string(msg)))
	}
	return nil
}

// longPath is p with every 8.3 name written out (p as it is when that fails).
func longPath(p string) string {
	in, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return p
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetLongPathName(in, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) > len(buf) {
		return p
	}
	return windows.UTF16ToString(buf[:n])
}

// shellExecuteInfo is SHELLEXECUTEINFOW.
type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         windows.Handle
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     windows.Handle
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    windows.Handle
	dwHotKey     uint32
	hIcon        windows.Handle
	hProcess     windows.Handle
}

const (
	seeMaskNoCloseProcess = 0x00000040
	seeMaskNoAsync        = 0x00000100
	seeMaskFlagNoUI       = 0x00000400
)

var procShellExecuteExW = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

// runElevated starts exe as an administrator, hidden, and waits for its exit
// code. A refused prompt is ErrDeclined.
func runElevated(exe string, args []string) (uint32, error) {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = syscall.EscapeArg(a)
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return 0, err
	}
	params, err := windows.UTF16PtrFromString(strings.Join(quoted, " "))
	if err != nil {
		return 0, err
	}
	info := shellExecuteInfo{fMask: seeMaskNoCloseProcess | seeMaskNoAsync | seeMaskFlagNoUI, lpVerb: verb,
		lpFile: file, lpParameters: params, nShow: windows.SW_HIDE}
	info.cbSize = uint32(unsafe.Sizeof(info))
	if r, _, e := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		if errors.Is(e, windows.ERROR_CANCELLED) {
			return 0, ErrDeclined
		}
		return 0, fmt.Errorf("run %s as an administrator: %w", filepath.Base(exe), e)
	}
	if info.hProcess == 0 {
		return 0, fmt.Errorf("run %s as an administrator: no process", filepath.Base(exe))
	}
	defer func() { _ = windows.CloseHandle(info.hProcess) }()
	if _, err := windows.WaitForSingleObject(info.hProcess, windows.INFINITE); err != nil {
		return 0, err
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.hProcess, &code); err != nil {
		return 0, err
	}
	return code, nil
}

// firewallApply makes the change it reads on stdin (already checked): the
// app's rules added (one of the same id first removed), the app's own removed.
const firewallApply = `$c = [Console]::In.ReadToEnd() | ConvertFrom-Json
foreach ($n in @($c.remove)) { if ($n) { Remove-NetFirewallRule -Name $n -PolicyStore PersistentStore -ErrorAction SilentlyContinue } }
foreach ($r in @($c.add)) {
  if (-not $r) { continue }
  Remove-NetFirewallRule -Name $r.name -PolicyStore PersistentStore -ErrorAction SilentlyContinue
  $a = @{ Name = $r.name; DisplayName = $r.display; Description = $r.description; Group = $r.group
          Direction = 'Inbound'; Action = 'Allow'; Enabled = 'True'; Program = $r.program; Profile = $r.profiles
          PolicyStore = 'PersistentStore' }
  if ($r.protocol -and $r.protocol -ne 'Any') { $a.Protocol = $r.protocol }
  $ports = @($r.ports | Where-Object { $_ -and $_ -ne 'Any' })
  if ($ports.Count -gt 0 -and ($r.protocol -eq 'TCP' -or $r.protocol -eq 'UDP')) { $a.LocalPort = $ports }
  try { New-NetFirewallRule @a -ErrorAction Stop | Out-Null }
  catch { throw ("New-NetFirewallRule " + ($a | ConvertTo-Json -Compress) + ": " + $_.Exception.Message) }
}`

func applyFirewall(c firewallChange) error {
	// the firewall takes a program's long path (a temp folder's may be 8.3)
	for i := range c.Add {
		c.Add[i].Program = longPath(c.Add[i].Program)
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	cmd := exec.Command(system32(`WindowsPowerShell\v1.0\powershell.exe`), "-NoProfile", "-NonInteractive", "-Command", firewallApply)
	cmd.Stdin = bytes.NewReader(raw)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	var errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &errb, &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("powershell: %w: %s", err, strings.TrimSpace(errb.String()))
	}
	return nil
}
