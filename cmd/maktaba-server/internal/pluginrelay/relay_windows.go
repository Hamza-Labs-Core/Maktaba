//go:build windows

package pluginrelay

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Listener serves one pipe name: a client connects to its waiting instance, and the next
// instance is made for the next client.
type Listener struct {
	name   string
	sa     *windows.SecurityAttributes
	mu     sync.Mutex
	next   windows.Handle
	closed bool
}

// userOnly is the pipe's security: a protected DACL admitting only the user this process
// runs as (no inherited entries, no one else), and the AppContainer SIDs named (a
// first-party runner's, story 54.23), which run at low integrity: with any, the pipe's
// mandatory label is low, so they may write to it (no one else is admitted by the DACL).
func userOnly(appContainers ...string) (*windows.SecurityAttributes, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("pluginrelay: this process's user: %w", err)
	}
	sddl := "D:P(A;;GA;;;" + u.User.Sid.String() + ")"
	for _, sid := range appContainers {
		sddl += "(A;;GA;;;" + sid + ")"
	}
	if len(appContainers) > 0 {
		sddl += "S:(ML;;NW;;;LW)"
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, fmt.Errorf("pluginrelay: the pipe's security: %w", err)
	}
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}, nil
}

// Listen makes the pipe's first instance. A name that exists already (someone made it
// first) is not served: FILE_FLAG_FIRST_PIPE_INSTANCE refuses it.
func Listen(name string) (*Listener, error) { return ListenFor(name) }

// ListenFor is Listen admitting, besides this process's user, the AppContainer SIDs named
// (a first-party runner's own pipes, story 54.23 on Windows).
func ListenFor(name string, appContainers ...string) (*Listener, error) {
	sa, err := userOnly(appContainers...)
	if err != nil {
		return nil, err
	}
	l := &Listener{name: name, sa: sa}
	h, err := l.instance(true)
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_PIPE_BUSY) {
			return nil, fmt.Errorf("pluginrelay: %s exists already, not served: %w", name, err)
		}
		return nil, fmt.Errorf("pluginrelay: %s: %w", name, err)
	}
	l.next = h
	return l, nil
}

func (l *Listener) instance(first bool) (windows.Handle, error) {
	n, err := windows.UTF16PtrFromString(l.name)
	if err != nil {
		return windows.InvalidHandle, err
	}
	mode := uint32(windows.PIPE_ACCESS_DUPLEX | windows.FILE_FLAG_OVERLAPPED)
	if first {
		mode |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
	}
	return windows.CreateNamedPipe(n, mode,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
		windows.PIPE_UNLIMITED_INSTANCES, 64<<10, 64<<10, 0, l.sa)
}

// Name is the pipe's name.
func (l *Listener) Name() string { return l.name }

// Accept waits for a client and answers its connection (overlapped: reads and writes in
// both directions at once, through the Go runtime's I/O completion port).
func (l *Listener) Accept() (io.ReadWriteCloser, error) {
	l.mu.Lock()
	h, closed := l.next, l.closed
	l.mu.Unlock()
	if closed {
		return nil, net.ErrClosed
	}
	ev, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = windows.CloseHandle(ev) }()
	ov := &windows.Overlapped{HEvent: ev}
	switch err := windows.ConnectNamedPipe(h, ov); {
	case err == nil, errors.Is(err, windows.ERROR_PIPE_CONNECTED):
	case errors.Is(err, windows.ERROR_IO_PENDING):
		var n uint32
		if err := windows.GetOverlappedResult(h, ov, &n, true); err != nil {
			l.mu.Lock()
			closed := l.closed
			l.mu.Unlock()
			if closed {
				return nil, net.ErrClosed
			}
			return nil, err
		}
	default:
		return nil, err
	}
	nh, err := l.instance(false)
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		if err == nil {
			_ = windows.CloseHandle(nh)
		}
		_ = windows.CloseHandle(h)
		return nil, net.ErrClosed
	}
	if err != nil {
		l.next = windows.InvalidHandle
		l.closed = true
		l.mu.Unlock()
		_ = windows.CloseHandle(h)
		return nil, fmt.Errorf("pluginrelay: the next instance of %s: %w", l.name, err)
	}
	l.next = nh
	l.mu.Unlock()
	return os.NewFile(uintptr(h), l.name), nil
}

// Close stops serving: the waiting instance is closed, a waiting Accept returns.
func (l *Listener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	if l.next != windows.InvalidHandle {
		_ = windows.CancelIoEx(l.next, nil)
		return windows.CloseHandle(l.next)
	}
	return nil
}

// Serve relays every client of l to a fresh connection from dial (the host's AF_UNIX
// socket) until l is closed. A client gets Ready once dial answered; when dial fails, the
// client is closed without it (logged with logf).
func Serve(l *Listener, dial func() (net.Conn, error), logf func(format string, args ...any)) error {
	for {
		c, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go func() {
			t, err := dial()
			if err != nil {
				_ = c.Close()
				if logf != nil {
					logf("pluginrelay: the plugin host is not answering: %v", err)
				}
				return
			}
			if _, err := c.Write([]byte{Ready}); err != nil {
				_ = c.Close()
				_ = t.Close()
				return
			}
			Pump(c, t)
		}()
	}
}

// Dial opens a client end of the pipe (overlapped, as the pipeline's asyncio opens it).
func Dial(name string) (io.ReadWriteCloser, error) {
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(n, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), name), nil
}
