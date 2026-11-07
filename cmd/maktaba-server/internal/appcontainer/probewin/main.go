//go:build windows

// Command probewin answers #710's questions on a Windows runner (a probe branch only, never
// merged): which transport links the pipeline (outside the AppContainer) and the plugin host
// (inside it).
//
//	probewin uds-serve <path>       listen on an AF_UNIX socket, answer one "ping" with "pong"
//	probewin uds-dial <path>        connect, send "ping", expect "pong"
//	probewin pipe-serve <name> <sddl|default>
//	                                a named pipe server, one "ping" answered with "pong"
//	probewin pipe-dial <name>       connect to the pipe, send "ping", expect "pong"
package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: probewin uds-serve|uds-dial|pipe-serve|pipe-dial <path> [sddl]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "uds-serve":
		err = udsServe(os.Args[2])
	case "uds-dial":
		err = udsDial(os.Args[2])
	case "pipe-serve":
		sddl := "default"
		if len(os.Args) > 3 {
			sddl = os.Args[3]
		}
		err = pipeServe(os.Args[2], sddl)
	case "pipe-dial":
		err = pipeDial(os.Args[2])
	default:
		err = fmt.Errorf("unknown mode %s", os.Args[1])
	}
	if err != nil {
		fmt.Println("RESULT", os.Args[1], "FAIL", err)
		os.Exit(1)
	}
	fmt.Println("RESULT", os.Args[1], "OK")
}

func udsServe(path string) error {
	_ = os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	defer l.Close()
	fmt.Println("listening", path)
	_ = l.(*net.UnixListener).SetDeadline(time.Now().Add(60 * time.Second))
	c, err := l.Accept()
	if err != nil {
		return err
	}
	defer c.Close()
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		return err
	}
	if strings.TrimSpace(line) != "ping" {
		return fmt.Errorf("got %q", line)
	}
	_, err = c.Write([]byte("pong\n"))
	return err
}

func udsDial(path string) error {
	c, err := net.DialTimeout("unix", path, 10*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	if _, err := c.Write([]byte("ping\n")); err != nil {
		return err
	}
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		return err
	}
	if strings.TrimSpace(line) != "pong" {
		return fmt.Errorf("got %q", line)
	}
	return nil
}

func pipeServe(name, sddl string) error {
	var sa *windows.SecurityAttributes
	if sddl != "default" {
		sd, err := windows.SecurityDescriptorFromString(sddl)
		if err != nil {
			return fmt.Errorf("sddl: %w", err)
		}
		sa = &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	}
	n, _ := windows.UTF16PtrFromString(name)
	h, err := windows.CreateNamedPipe(n, windows.PIPE_ACCESS_DUPLEX,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
		1, 4096, 4096, 0, sa)
	if err != nil {
		return fmt.Errorf("CreateNamedPipe: %w", err)
	}
	defer windows.CloseHandle(h)
	fmt.Println("listening", name)
	if err := windows.ConnectNamedPipe(h, nil); err != nil && err != windows.ERROR_PIPE_CONNECTED {
		return fmt.Errorf("ConnectNamedPipe: %w", err)
	}
	buf := make([]byte, 64)
	var got uint32
	if err := windows.ReadFile(h, buf, &got, nil); err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if strings.TrimSpace(string(buf[:got])) != "ping" {
		return fmt.Errorf("got %q", buf[:got])
	}
	var wrote uint32
	return windows.WriteFile(h, []byte("pong\n"), &wrote, nil)
}

func pipeDial(name string) error {
	n, _ := windows.UTF16PtrFromString(name)
	h, err := windows.CreateFile(n, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer windows.CloseHandle(h)
	var wrote uint32
	if err := windows.WriteFile(h, []byte("ping\n"), &wrote, nil); err != nil {
		return err
	}
	buf := make([]byte, 64)
	var got uint32
	if err := windows.ReadFile(h, buf, &got, nil); err != nil {
		return err
	}
	if strings.TrimSpace(string(buf[:got])) != "pong" {
		return fmt.Errorf("got %q", buf[:got])
	}
	return nil
}
