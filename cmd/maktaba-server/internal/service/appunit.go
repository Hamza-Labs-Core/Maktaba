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

// AppUnitName is the app's unit: the name Epic 35's user units use, so one
// machine never runs two Maktaba servers.
const AppUnitName = Name + ".service"

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
	// Dir holds user units ($XDG_CONFIG_HOME/systemd/user).
	Dir string
}

// AppUnitFor is the unit for a data directory (the runtime's parent) and a logs
// directory, in the user's unit directory.
func AppUnitFor(getenv func(string) string, home, dataDir, logsDir string) AppUnit {
	cfg := getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(home, ".config")
	}
	return AppUnit{
		Launcher: filepath.Join(dataDir, "runtime", "current", "bin", "maktaba-launcher"),
		LogFile:  filepath.Join(logsDir, "server.log"),
		Dir:      filepath.Join(cfg, "systemd", "user"),
	}
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
ExecStart=` + systemdArgs(u.Launcher, "--log-file", u.LogFile, "serve") + `
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
		LingerFile: filepath.Join("/var/lib/systemd/linger", name),
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

// AppStatus is the app's unit as `service status` reports it.
type AppStatus struct {
	// Unit is the unit file; Installed: it is the app's.
	Unit      string `json:"unit"`
	Installed bool   `json:"installed"`
	// Taken is the ExecStart of another maktaba-server.service in its place.
	Taken string `json:"taken,omitempty"`
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
