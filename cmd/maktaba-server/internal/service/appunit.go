package service

// The Linux app's systemd user unit (Epic 39 story 39.5,
// specs/epics/39-linux-app/contract-runtime.md §5): the server runs from the
// app's runtime copy (<data>/runtime/current), as the signed-in user, started
// at boot by linger. The desktop shell and the terminal setup both go through
// `maktaba-server service`, so neither writes a unit of its own.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// AppUnitName is the app's unit, under the name Epic 35's units use. Install
// refuses a user unit of that name it did not write (a self-managed install's)
// and a system-wide one (the maktaba-server package's, or setup --system):
// either would start a second server on the same ports.
const AppUnitName = Name + ".service"

// systemUnitDirs are where a system-wide unit of that name can be.
var systemUnitDirs = []string{"/etc/systemd/system", "/run/systemd/system", "/usr/local/lib/systemd/system",
	"/usr/lib/systemd/system", "/lib/systemd/system"}

// appMarker is the unit's first line: it tells the app's unit from another
// maktaba-server.service (an Epic 35 self-managed install's).
const appMarker = "# Maktaba (Epic 39): written by `maktaba-server service install`; it is replaced on every install."

// ErrServiceTaken: another maktaba-server.service (not the app's) is installed.
var ErrServiceTaken = errors.New("service_taken")

// AppUnit is the app's user unit.
type AppUnit struct {
	// Launcher is the runtime's launcher (<data>/runtime/current/bin/maktaba-launcher).
	Launcher string
	// LogFile is the server's log; the launcher rotates it.
	LogFile string
	// Dir holds user units: where the user's systemd looks (UserManager.UnitDir).
	Dir string
	// Env is the XDG directories this install uses (KEY=VALUE), written into
	// the unit: the server started at boot by systemd has none of the shell's.
	Env []string
}

// xdgDirs are the XDG variables a server's paths come from (config, data,
// logs under state, caches).
var xdgDirs = []struct{ name, def string }{
	{"XDG_CONFIG_HOME", ".config"}, {"XDG_DATA_HOME", ".local/share"},
	{"XDG_STATE_HOME", ".local/state"}, {"XDG_CACHE_HOME", ".cache"},
}

// AppUnitFor is the unit for a data directory (the runtime's parent) and a logs
// directory, with the XDG directories getenv gives (the defaults under home
// otherwise) pinned in its environment. Dir is the default user unit
// directory; UnitDir is where the manager looks.
func AppUnitFor(getenv func(string) string, home, dataDir, logsDir string) AppUnit {
	u := AppUnit{
		Launcher: filepath.Join(dataDir, "runtime", "current", "bin", "maktaba-launcher"),
		LogFile:  filepath.Join(logsDir, "server.log"),
		Dir:      filepath.Join(home, ".config", "systemd", "user"),
	}
	for _, x := range xdgDirs {
		v := getenv(x.name)
		if v == "" || !filepath.IsAbs(v) { // the spec ignores a relative one
			v = filepath.Join(home, filepath.FromSlash(x.def))
		}
		u.Env = append(u.Env, x.name+"="+v)
	}
	return u
}

// Path is the unit file.
func (u AppUnit) Path() string { return filepath.Join(u.Dir, AppUnitName) }

// Render is the unit file's content. KillMode=mixed: SIGTERM reaches the
// launcher only, which drains the supervisor (Postgres stops last); the 90 s
// cover the launcher's 60 s stop grace. A clean stop (Pause) stays stopped
// until Resume or the next boot; anything else is restarted, and exit 75 (a
// new runtime to start) always is.
func (u AppUnit) Render() string {
	return appMarker + `
[Unit]
Description=Maktaba (the server and everything it runs)

[Service]
Type=simple
` + envLines(u.Env) + `ExecStart=` + systemdArgs(u.Launcher, "--log-file", u.LogFile, "serve") + `
Restart=always
RestartSec=5
RestartForceExitStatus=75
KillMode=mixed
TimeoutStopSec=90
LimitNOFILE=65536

[Install]
WantedBy=default.target
`
}

// envLines are Environment= lines, each assignment quoted (backslash and
// quote escaped, % doubled: systemd expands specifiers there, not variables).
func envLines(env []string) string {
	var b strings.Builder
	for _, kv := range env {
		b.WriteString(`Environment="` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%").Replace(kv) + "\"\n")
	}
	return b.String()
}

// systemdArgs is a command line in systemd's syntax: each word quoted when it
// needs it, "%" and "$" doubled (systemd expands specifiers and variables in
// ExecStart).
func systemdArgs(words ...string) string {
	out := make([]string, len(words))
	for i, w := range words {
		w = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(w)
		if w == "" || strings.ContainsAny(w, " \t'\"\\;") {
			w = `"` + w + `"`
		}
		out[i] = w
	}
	return strings.Join(out, " ")
}

// Runner runs a command and returns its standard output (tests replace it).
type Runner func(ctx context.Context, argv []string) (string, error)

// ExecRunner runs commands for real; a failure carries the command's stderr.
func ExecRunner(ctx context.Context, argv []string) (string, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = userManagerEnv(os.Environ())
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return string(out), fmt.Errorf("%s: %s", strings.Join(argv, " "), msg)
		}
		return string(out), fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
	}
	return string(out), nil
}

// userManagerEnv finds the user's manager when the session did not say where
// it is (su, a cron job): XDG_RUNTIME_DIR is /run/user/<uid> when that exists.
func userManagerEnv(env []string) []string {
	for _, kv := range env {
		if strings.HasPrefix(kv, "XDG_RUNTIME_DIR=") && len(kv) > len("XDG_RUNTIME_DIR=") {
			return env
		}
	}
	dir := "/run/user/" + strconv.Itoa(os.Getuid())
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return append(env, "XDG_RUNTIME_DIR="+dir)
	}
	return env
}

// ExecInteractive runs a command on this process's terminal (stdin, stdout and
// stderr), so polkit can ask for a password there; its output is not captured.
func ExecInteractive(ctx context.Context, argv []string) (string, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = userManagerEnv(os.Environ())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
	}
	return "", nil
}

// UserManager is the user's systemd: `systemctl --user`, and loginctl for
// linger.
type UserManager struct {
	Run Runner
	// Ask runs a command that may ask for a password (linger); nil: Run.
	Ask Runner
	// GOOS, Booted and LingerFile are what the manager checks (tests set them).
	GOOS string
	// Booted reports systemd as PID 1 (sd_booted: /run/systemd/system exists).
	Booted func() bool
	// LingerFile is logind's linger flag for this user.
	LingerFile string
	// SystemUnitDirs are where a system-wide unit of the app's name can be.
	SystemUnitDirs []string
}

// LocalUserManager is the user manager of this process's user.
func LocalUserManager() UserManager {
	name := os.Getenv("USER")
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	return UserManager{
		Run:  ExecRunner,
		Ask:  ExecInteractive,
		GOOS: runtime.GOOS,
		Booted: func() bool {
			info, err := os.Stat("/run/systemd/system")
			return err == nil && info.IsDir()
		},
		LingerFile:     filepath.Join("/var/lib/systemd/linger", name),
		SystemUnitDirs: systemUnitDirs,
	}
}

func (m UserManager) systemctl(ctx context.Context, args ...string) (string, error) {
	return m.Run(ctx, append([]string{"systemctl", "--user"}, args...))
}

// Available is nil when a user unit can run here, else why not.
func (m UserManager) Available(ctx context.Context) error {
	if m.GOOS != "linux" {
		return fmt.Errorf("%w: a systemd user service is how Maktaba runs on Linux, not on %s", ErrUnsupported, m.GOOS)
	}
	if m.Booted != nil && !m.Booted() {
		return fmt.Errorf("%w: systemd does not run this system (no /run/systemd/system: a container, or WSL without systemd)", ErrUnsupported)
	}
	if _, err := m.systemctl(ctx, "show-environment"); err != nil {
		return fmt.Errorf("%w: no systemd user manager for this user (%v)", ErrUnsupported, err)
	}
	return nil
}

// UnitDir is where the user's systemd reads units: its own XDG_CONFIG_HOME's
// systemd/user when its environment has one (a variable set only in a shell
// is not the manager's), else def (~/.config/systemd/user).
func (m UserManager) UnitDir(ctx context.Context, def string) string {
	out, err := m.systemctl(ctx, "show-environment")
	if err != nil {
		return def
	}
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "XDG_CONFIG_HOME="); ok && filepath.IsAbs(v) {
			return filepath.Join(v, "systemd", "user")
		}
	}
	return def
}

// SystemUnit is a system-wide maktaba-server.service, or "".
func (m UserManager) SystemUnit() string {
	for _, d := range m.SystemUnitDirs {
		p := filepath.Join(d, AppUnitName)
		if _, err := os.Lstat(p); err == nil {
			return p
		}
	}
	return ""
}

// AppStatus is the app's unit as `service status` reports it.
type AppStatus struct {
	// Unit is the unit file; Installed: it is the app's.
	Unit      string `json:"unit"`
	Installed bool   `json:"installed"`
	// Taken is the ExecStart of another maktaba-server.service in its place.
	Taken string `json:"taken,omitempty"`
	// SystemUnit is a system-wide maktaba-server.service (another server).
	SystemUnit string `json:"system_unit,omitempty"`
	// Enabled: started at boot (with linger) or login.
	Enabled bool `json:"enabled"`
	// Active is systemd's ActiveState (active, inactive, activating,
	// deactivating, failed); SubState and MainPID as systemd reports them.
	Active   string `json:"active"`
	SubState string `json:"sub_state,omitempty"`
	MainPID  int    `json:"main_pid,omitempty"`
	// Linger: the user's manager runs from boot, without a login.
	Linger bool `json:"linger"`
	// Manager is "ok" or "unsupported" (Reason says why).
	Manager string `json:"manager"`
	Reason  string `json:"reason,omitempty"`
}

// Status reads the unit file and asks systemd about it.
func (m UserManager) Status(ctx context.Context, u AppUnit) AppStatus {
	st := AppStatus{Unit: u.Path(), Manager: "ok", Active: "inactive"}
	st.Installed, st.Taken = owned(u.Path())
	st.SystemUnit = m.SystemUnit()
	if m.LingerFile != "" {
		_, err := os.Stat(m.LingerFile)
		st.Linger = err == nil
	}
	if err := m.Available(ctx); err != nil {
		st.Manager, st.Reason = "unsupported", strings.TrimPrefix(err.Error(), ErrUnsupported.Error()+": ")
		return st
	}
	out, err := m.systemctl(ctx, "show", AppUnitName, "--property=LoadState,UnitFileState,ActiveState,SubState,MainPID")
	if err != nil {
		st.Reason = err.Error()
		return st
	}
	props := parseProps(out)
	st.Enabled = props["UnitFileState"] == "enabled"
	if a := props["ActiveState"]; a != "" {
		st.Active = a
	}
	st.SubState = props["SubState"]
	st.MainPID, _ = strconv.Atoi(props["MainPID"])
	return st
}

// owned reports whether the unit file is the app's, or else the ExecStart of
// the unit that holds the name.
func owned(path string) (bool, string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, ""
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	if strings.HasPrefix(text, appMarker+"\n") {
		return true, ""
	}
	for _, line := range strings.Split(text, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "ExecStart="); ok {
			return false, v
		}
	}
	return false, "(no ExecStart)"
}

// parseProps reads `systemctl show` output (Key=Value lines).
func parseProps(out string) map[string]string {
	props := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			props[k] = v
		}
	}
	return props
}

// Install writes the unit, reloads systemd and enables it (start: and starts
// it). Another maktaba-server.service in its place is ErrServiceTaken unless
// replace.
func (m UserManager) Install(ctx context.Context, u AppUnit, start, replace bool) error {
	if err := m.Available(ctx); err != nil {
		return err
	}
	if sys := m.SystemUnit(); sys != "" {
		return fmt.Errorf("%w: a system-wide Maktaba service is installed (%s, from the maktaba-server package or "+
			"setup --system), which would run a second server on the same ports. Remove it first: "+
			"sudo systemctl disable --now maktaba-server, then remove the package (sudo apt remove maktaba-server, "+
			"or sudo dnf remove maktaba-server)", ErrServiceTaken, sys)
	}
	if mine, taken := owned(u.Path()); !mine && taken != "" && !replace {
		return fmt.Errorf("%w: %s already runs %s; it belongs to another install of Maktaba "+
			"(remove that one first, or replace it)", ErrServiceTaken, u.Path(), taken)
	}
	if err := os.MkdirAll(u.Dir, 0o755); err != nil {
		return err
	}
	if err := layout.WriteFileAtomic(u.Path(), []byte(u.Render()), 0o644); err != nil {
		return err
	}
	if _, err := m.systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	args := []string{"enable"}
	if start {
		args = append(args, "--now")
	}
	_, err := m.systemctl(ctx, append(args, AppUnitName)...)
	return err
}

// Uninstall stops and disables the app's unit and removes it (the data stays).
// Another install's unit is never touched.
func (m UserManager) Uninstall(ctx context.Context, u AppUnit) error {
	mine, taken := owned(u.Path())
	if !mine {
		if taken != "" {
			return fmt.Errorf("%w: %s is not the app's (it runs %s): left as it is", ErrServiceTaken, u.Path(), taken)
		}
		return nil // nothing installed
	}
	if err := m.Available(ctx); err == nil {
		_, _ = m.systemctl(ctx, "disable", "--now", AppUnitName) // a unit systemd never loaded
	}
	if err := os.Remove(u.Path()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := m.Available(ctx); err == nil {
		_, err := m.systemctl(ctx, "daemon-reload")
		return err
	}
	return nil
}

// Control starts, stops or restarts the app's unit; stop returns once it is
// inactive (systemctl waits for the stop job).
func (m UserManager) Control(ctx context.Context, action string) error {
	switch action {
	case "start", "stop", "restart":
	default:
		return fmt.Errorf("unknown action %q (start, stop or restart)", action)
	}
	if err := m.Available(ctx); err != nil {
		return err
	}
	_, err := m.systemctl(ctx, action, AppUnitName)
	return err
}

// Linger turns logind's linger on (the user's units start at boot, without a
// login) or off. From an active desktop session polkit allows it without a
// password; elsewhere (SSH) loginctl asks for one, through Ask on this terminal.
func (m UserManager) Linger(ctx context.Context, on bool) error {
	if m.GOOS != "linux" {
		return fmt.Errorf("%w: linger is systemd's", ErrUnsupported)
	}
	verb := "enable-linger"
	if !on {
		verb = "disable-linger"
	}
	run := m.Ask
	if run == nil {
		run = m.Run
	}
	_, err := run(ctx, []string{"loginctl", verb})
	return err
}
