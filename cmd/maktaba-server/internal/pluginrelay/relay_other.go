//go:build !windows

package pluginrelay

import (
	"io"
	"net"
)

// Listener is the Windows relay's; elsewhere the pipeline opens the host's socket itself.
type Listener struct{}

// Listen is Windows only.
func Listen(string) (*Listener, error) { return nil, ErrUnsupported }

// Name is the pipe's name.
func (*Listener) Name() string { return "" }

// ListenFor is Windows only.
func ListenFor(string, ...string) (*Listener, error) { return nil, ErrUnsupported }

// Accept is Windows only.
func (*Listener) Accept() (io.ReadWriteCloser, error) { return nil, ErrUnsupported }

// Close is Windows only.
func (*Listener) Close() error { return nil }

// Serve is Windows only.
func Serve(*Listener, func() (net.Conn, error), func(string, ...any)) error { return ErrUnsupported }

// Dial is Windows only.
func Dial(string) (io.ReadWriteCloser, error) { return nil, ErrUnsupported }
