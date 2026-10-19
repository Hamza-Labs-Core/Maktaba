// Package service writes the OS service that runs maktaba-launcher (Epic 35,
// story 35.2 AC7, design §2.3):
//
//	Linux    systemd: system unit (User=maktaba, hardening) or user unit (+ linger)
//	macOS    launchd: LaunchDaemon (home) or LaunchAgent (node, the user's session)
//	Windows  a logon task for the user (default: mapped drives are per user)
//	         or, optionally, a service (the launcher implements svc; no NSSM)
//
// Every unit runs `maktaba-launcher --root <install> serve`. The launcher
// restarts the server on exit 75 itself; the units restart the launcher.
package service

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// Kind is the service manager.
type Kind string

const (
	KindSystemd    Kind = "systemd"
	KindLaunchd    Kind = "launchd"
	KindTask       Kind = "task"       // Windows logon task (default on Windows)
	KindWinService Kind = "winservice" // Windows service (optional)
)

// Name is the unit / task / service name.
const (
	Name         = "maktaba-server"     // systemd unit maktaba-server.service
	LaunchdLabel = "com.maktaba.server" // launchd label
	WindowsName  = "Maktaba"            // task and service name
)

// Spec describes the service to write.
type Spec struct {
	Kind Kind
	Mode layout.Mode
	// Root is the install root (layout); Launcher the launcher's path
	// (default <Root>/maktaba-launcher[.exe]).
	Root     string
	Launcher string
	// ConfigPath, when set, is exported as MAKTABA_CONFIG.
	ConfigPath string
	// User/Group: the account of a Linux system unit (default maktaba).
	User, Group string
	// LogDir: launchd stdout/stderr (default <Root>/logs).
	LogDir string
	// Home is the user's home (user units and agents; default os.UserHomeDir).
	Home string
	// WinUser is the account of a Windows logon task (DOMAIN\user; default
	// the current user): the task starts at that user's logon only.
	WinUser string
	// ReadWritePaths for a hardened system unit (default Root and /var/log/maktaba).
	ReadWritePaths []string
}

// DefaultKind is the service manager for an OS and mode: Windows system mode is
// the optional service, user mode the logon task.
func DefaultKind(goos string, mode layout.Mode) Kind {
	switch goos {
	case "windows":
		if mode == layout.ModeSystem {
			return KindWinService
		}
		return KindTask
	case "darwin":
		return KindLaunchd
	default:
		return KindSystemd
	}
}

func (s Spec) withDefaults() Spec {
	if s.Launcher == "" {
		exe := "maktaba-launcher"
		if s.Kind == KindTask || s.Kind == KindWinService {
			exe += ".exe"
		}
		s.Launcher = s.join(s.Root, exe)
	}
	if s.User == "" {
		s.User = "maktaba"
	}
	if s.Group == "" {
		s.Group = s.User
	}
	if s.Home == "" {
		s.Home, _ = os.UserHomeDir()
	}
	if s.LogDir == "" {
		switch {
		case s.Kind == KindLaunchd && s.Mode == layout.ModeSystem:
			s.LogDir = "/usr/local/var/log/maktaba"
		case s.Kind == KindLaunchd:
			s.LogDir = s.join(s.Home, "Library", "Logs", "Maktaba")
		default:
			s.LogDir = s.join(s.Root, "logs")
		}
	}
	if s.Kind == KindTask && s.WinUser == "" {
		if u := os.Getenv("USERNAME"); u != "" {
			if d := os.Getenv("USERDOMAIN"); d != "" {
				u = d + `\` + u
			}
			s.WinUser = u
		}
	}
	if len(s.ReadWritePaths) == 0 {
		s.ReadWritePaths = []string{s.Root, "/var/log/maktaba"}
	}
	return s
}

// join builds a path for the target OS (POSIX units rendered on Windows in
// tests keep forward slashes).
func (s Spec) join(elem ...string) string {
	if s.Kind == KindTask || s.Kind == KindWinService {
		return filepath.Join(elem...)
	}
	return path.Join(elem...)
}

// UnitPath is where the unit file goes ("" for a Windows service, which
// lives in the SCM, not a file).
func (s Spec) UnitPath() string {
	s = s.withDefaults()
	switch s.Kind {
	case KindSystemd:
		if s.Mode == layout.ModeSystem {
			return "/etc/systemd/system/" + Name + ".service"
		}
		return s.join(s.Home, ".config", "systemd", "user", Name+".service")
	case KindLaunchd:
		if s.Mode == layout.ModeSystem {
			return "/Library/LaunchDaemons/" + LaunchdLabel + ".plist"
		}
		return s.join(s.Home, "Library", "LaunchAgents", LaunchdLabel+".plist")
	case KindTask:
		return s.join(s.Root, "updates", "maktaba-task.xml")
	}
	return ""
}

// Render returns the unit file's content (for a Windows service: a readable
// description of what is registered with the SCM).
func Render(s Spec) (string, error) {
	s = s.withDefaults()
	var tpl string
	switch s.Kind {
	case KindSystemd:
		tpl = systemdUser
		if s.Mode == layout.ModeSystem {
			tpl = systemdSystem
		}
	case KindLaunchd:
		tpl = launchdPlist
	case KindTask:
		tpl = taskXML
	case KindWinService:
		tpl = winServiceText
	default:
		return "", fmt.Errorf("unknown service kind %q", s.Kind)
	}
	t, err := template.New(string(s.Kind)).Funcs(template.FuncMap{
		"xml":    xmlEscape,
		"winarg": winArg,
		"join":   strings.Join,
	}).Parse(tpl)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := t.Execute(&b, s); err != nil {
		return "", err
	}
	return b.String(), nil
}

// Step is one action of an install plan.
type Step struct {
	// WriteFile: Path + Content (UTF16 for a task XML).
	Path    string
	Content string
	UTF16   bool
	// Run: a command.
	Run []string
	// Native: done in Go (the Windows service through the SCM).
	Native string
}

func (st Step) String() string {
	switch {
	case st.Path != "":
		return "write " + st.Path
	case len(st.Run) > 0:
		return "run   " + strings.Join(st.Run, " ")
	default:
		return "scm   " + st.Native
	}
}

// Plan lists what installing the service does.
func Plan(s Spec) ([]Step, error) {
	s = s.withDefaults()
	content, err := Render(s)
	if err != nil {
		return nil, err
	}
	switch s.Kind {
	case KindSystemd:
		steps := []Step{{Path: s.UnitPath(), Content: content}}
		if s.Mode == layout.ModeSystem {
			return append(steps,
				Step{Run: []string{"systemctl", "daemon-reload"}},
				Step{Run: []string{"systemctl", "enable", Name + ".service"}}), nil
		}
		return append(steps,
			Step{Run: []string{"systemctl", "--user", "daemon-reload"}},
			Step{Run: []string{"systemctl", "--user", "enable", Name + ".service"}},
			Step{Run: []string{"loginctl", "enable-linger"}}), nil
	case KindLaunchd:
		domain := "system"
		if s.Mode != layout.ModeSystem {
			domain = "gui/" + uidString()
		}
		return []Step{
			{Path: s.UnitPath(), Content: content},
			{Run: []string{"launchctl", "bootout", domain + "/" + LaunchdLabel}}, // ok to fail: not loaded
			{Run: []string{"launchctl", "bootstrap", domain, s.UnitPath()}},
		}, nil
	case KindTask:
		return []Step{
			{Path: s.UnitPath(), Content: content, UTF16: true},
			{Run: []string{"schtasks", "/Create", "/TN", WindowsName, "/XML", s.UnitPath(), "/F"}},
		}, nil
	case KindWinService:
		return []Step{{Native: "create service " + WindowsName + ": " + s.Launcher + " --root " + s.Root +
			" serve (automatic start; recovery: restart after 5 s, reset after 1 day)"}}, nil
	}
	return nil, fmt.Errorf("unknown service kind %q", s.Kind)
}

// ErrUnsupported: this service kind cannot be installed from this OS.
var ErrUnsupported = errors.New("service kind not supported on this OS")

// Install carries out a plan. Failing "launchctl bootout" (not loaded yet) is
// ignored; every other failure stops it.
func Install(s Spec, log func(string)) error {
	s = s.withDefaults()
	if !Supported(s.Kind, runtime.GOOS) {
		return fmt.Errorf("%w: %s on %s", ErrUnsupported, s.Kind, runtime.GOOS)
	}
	steps, err := Plan(s)
	if err != nil {
		return err
	}
	for _, st := range steps {
		log(st.String())
		switch {
		case st.Path != "":
			data := []byte(st.Content)
			if st.UTF16 {
				data = utf16LE(st.Content)
			}
			if err := layout.WriteFileAtomic(st.Path, data, 0o644); err != nil {
				return err
			}
		case len(st.Run) > 0:
			if err := runCmd(st.Run); err != nil {
				if len(st.Run) > 1 && st.Run[1] == "bootout" {
					continue
				}
				return fmt.Errorf("%s: %w", strings.Join(st.Run, " "), err)
			}
		case st.Native != "":
			if err := installWindowsService(s); err != nil {
				return err
			}
		}
	}
	return nil
}

// Supported reports whether kind can be installed on goos.
func Supported(kind Kind, goos string) bool {
	switch kind {
	case KindSystemd:
		return goos == "linux"
	case KindLaunchd:
		return goos == "darwin"
	case KindTask, KindWinService:
		return goos == "windows"
	}
	return false
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}

// winArg quotes one Windows command-line argument.
func winArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

func utf16LE(s string) []byte {
	out := []byte{0xFF, 0xFE}
	for _, r := range s {
		if r >= 0x10000 {
			r -= 0x10000
			hi, lo := 0xD800+(r>>10), 0xDC00+(r&0x3FF)
			out = append(out, byte(hi), byte(hi>>8), byte(lo), byte(lo>>8))
			continue
		}
		out = append(out, byte(r), byte(r>>8))
	}
	return out
}
