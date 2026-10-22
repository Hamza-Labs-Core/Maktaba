package adopt

import (
	"errors"
	"os"
)

// Proc is a running Maktaba program.
type Proc struct {
	PID     int    `json:"ProcessId"`
	Name    string `json:"Name"`
	Exe     string `json:"ExecutablePath"`
	Cmdline string `json:"CommandLine"`
	// Cwd is a console's current directory (cmd.exe, conhost.exe), what a
	// relative script on its command line is resolved against.
	Cwd string `json:"-"`
}

// Listener is a listening TCP socket: the address it is bound to, and the
// process that holds it.
type Listener struct {
	Addr string
	PID  int
}

// Service is a Windows service that runs a Maktaba program.
type Service struct {
	Name     string `json:"Name"`
	PathName string `json:"PathName"` // its command line
	State    string `json:"State"`    // Running, Stopped, …
}

// Sys is what the move asks of the OS (tests give it a fake).
type Sys interface {
	// Tasks lists the scheduled tasks the user can read.
	Tasks() ([]Task, error)
	// TaskXML is one task's definition (to bring it back).
	TaskXML(name string) ([]byte, error)
	HasTask(name string) bool
	EndTask(name string) error
	SetTaskEnabled(name string, on bool) error
	DeleteTask(name string) error
	// CreateTask registers a task from its XML file.
	CreateTask(name, xmlFile string) error
	RunTask(name string) error
	// Processes lists the running Maktaba programs (maktaba-*), pipeline
	// workers, and the cmd.exe / conhost.exe that run a batch file (a script's
	// restart loop). A process of another user, or of the system, has no path.
	Processes() ([]Proc, error)
	// Kill ends a process and its tree.
	Kill(pid int) error
	// EndProcess ends one process, not its children (a restart loop: the
	// server it started gets its own grace).
	EndProcess(pid int) error
	// Services lists the Windows services whose program is Maktaba's
	// (maktaba-launcher, maktaba-server): one installed with setup --service
	// --system runs as the system, its processes invisible to the user.
	Services() ([]Service, error)
	ReadFile(path string) ([]byte, error)
	Getenv(name string) string
	// Listening reports whether something listens on a loopback port.
	Listening(port string) bool
	// Listeners are the TCP sockets listening on port: the address each is
	// bound to (0.0.0.0 and :: for every one) and its process.
	Listeners(port string) ([]Listener, error)
	// StopAppServer asks the app's server agent to stop (its stop event) and
	// waits until it has exited; nil when none runs.
	StopAppServer() error
	// User is the user this process runs as (whose app task AppTask names).
	User() (User, error)
	// FirewallRules are the firewall rules bound to a program inside dir
	// (read only).
	FirewallRules(dir string) ([]FirewallRule, error)
	// ActiveProfiles are the firewall profiles of the networks in use
	// (Domain, Private, Public).
	ActiveProfiles() ([]string, error)
	// CanElevate: this user can allow an administrator's change with one
	// consent prompt.
	CanElevate() bool
	// AddFirewallRules and RemoveFirewallRules change the firewall as an
	// administrator: one prompt each time (ErrDeclined when refused).
	AddFirewallRules(rules []FirewallRule) error
	RemoveFirewallRules(names []string) error
}

// ErrUnsupported: finding and moving another install is done on Windows.
var ErrUnsupported = errors.New("moving another install into the app is supported on Windows")

func readFile(p string) ([]byte, error) { return os.ReadFile(p) }
