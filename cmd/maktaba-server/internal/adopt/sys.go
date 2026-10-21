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
	// Processes lists the running Maktaba programs (maktaba-*) and pipeline
	// workers.
	Processes() ([]Proc, error)
	// Kill ends a process and its tree.
	Kill(pid int) error
	ReadFile(path string) ([]byte, error)
	Getenv(name string) string
	// Listening reports whether something listens on a loopback port.
	Listening(port string) bool
	// StopAppServer asks the app's server agent to stop (its stop event) and
	// waits until it has exited; nil when none runs.
	StopAppServer() error
}

// ErrUnsupported: finding and moving another install is done on Windows.
var ErrUnsupported = errors.New("moving another install into the app is supported on Windows")

func readFile(p string) ([]byte, error) { return os.ReadFile(p) }
