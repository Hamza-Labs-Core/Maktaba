//go:build !windows

package adopt

import "os"

// RealSys finds nothing outside Windows: a Mac that ran the developer stack
// moves with migrate-from-dev (Epic 36 story 36.9).
func RealSys() Sys { return otherSys{} }

type otherSys struct{}

func (otherSys) Tasks() ([]Task, error)               { return nil, nil }
func (otherSys) TaskXML(string) ([]byte, error)       { return nil, ErrUnsupported }
func (otherSys) HasTask(string) bool                  { return false }
func (otherSys) EndTask(string) error                 { return ErrUnsupported }
func (otherSys) SetTaskEnabled(string, bool) error    { return ErrUnsupported }
func (otherSys) DeleteTask(string) error              { return ErrUnsupported }
func (otherSys) CreateTask(string, string) error      { return ErrUnsupported }
func (otherSys) RunTask(string) error                 { return ErrUnsupported }
func (otherSys) Processes() ([]Proc, error)           { return nil, nil }
func (otherSys) Kill(int) error                       { return ErrUnsupported }
func (otherSys) ReadFile(path string) ([]byte, error) { return readFile(path) }
func (otherSys) Getenv(name string) string            { return os.Getenv(name) }
func (otherSys) Listening(string) bool                { return false }
func (otherSys) StopAppServer() error                 { return ErrUnsupported }
func (otherSys) User() (User, error)                  { return User{}, ErrUnsupported }
