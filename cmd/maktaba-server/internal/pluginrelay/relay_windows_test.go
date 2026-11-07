//go:build windows

package pluginrelay

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// echoHost is a stand-in for maktaba-plugind: an AF_UNIX listener (as the host serves on
// Windows) that echoes every byte it reads.
func echoHost(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "relay")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "d.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	return path
}

// relay serves a new pipe to dial; its name.
func relay(t *testing.T, dial func() (net.Conn, error)) string {
	t.Helper()
	name, err := PipeName()
	if err != nil {
		t.Fatal(err)
	}
	l, err := Listen(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() { _ = Serve(l, dial, t.Logf) }()
	return name
}

func readReady(t *testing.T, c io.Reader) {
	t.Helper()
	b := make([]byte, 1)
	if _, err := io.ReadFull(c, b); err != nil || b[0] != Ready {
		t.Fatalf("no Ready from the relay: %v %v", b, err)
	}
}

// Every byte through, both ways at once, unchanged: 4 MB each way, written while the
// other side is read (the echo host writes back as it reads, so a relay that buffered
// one direction while blocked on the other would stall).
func TestRelayCarriesEveryByteBothWays(t *testing.T) {
	host := echoHost(t)
	name := relay(t, func() (net.Conn, error) { return net.Dial("unix", host) })
	c, err := Dial(name)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	readReady(t, c)
	want := make([]byte, 4<<20)
	_, _ = rand.Read(want)
	errc := make(chan error, 1)
	go func() {
		_, err := c.Write(want)
		errc <- err
	}()
	got := make([]byte, len(want))
	done := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(c, got)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the relay stalled")
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("the bytes changed on the way")
	}
}

// When the host does not answer, the client is closed before Ready (the pipeline raises
// ConnectionRefusedError, as for a refused unix socket).
func TestHostDownClosesTheClientBeforeReady(t *testing.T) {
	name := relay(t, func() (net.Conn, error) { return nil, errors.New("no host") })
	c, err := Dial(name)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	b := make([]byte, 1)
	n, err := c.Read(b)
	if n != 0 || err == nil {
		t.Fatalf("read %d bytes (%v): the client got something from a relay with no host", n, err)
	}
}

// A name someone made first is not served (FILE_FLAG_FIRST_PIPE_INSTANCE).
func TestASquattedNameIsNotServed(t *testing.T) {
	name, _ := PipeName()
	n, _ := windows.UTF16PtrFromString(name)
	h, err := windows.CreateNamedPipe(n, windows.PIPE_ACCESS_DUPLEX, windows.PIPE_TYPE_BYTE|windows.PIPE_WAIT,
		windows.PIPE_UNLIMITED_INSTANCES, 4096, 4096, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if l, err := Listen(name); err == nil {
		_ = l.Close()
		t.Fatal("the relay served a pipe name someone else made first")
	} else if !strings.Contains(err.Error(), "exists already") {
		t.Fatalf("not the squatting refusal: %v", err)
	}
}

var (
	advapi32                = windows.NewLazySystemDLL("advapi32.dll")
	procLogonUserW          = advapi32.NewProc("LogonUserW")
	procImpersonateLoggedOn = advapi32.NewProc("ImpersonateLoggedOnUser")
)

const logon32Network, logon32ProviderDefault = 3, 0

// asUser runs f on a thread impersonating user (a network logon: no right to log on
// locally needed).
func asUser(t *testing.T, user, pass string, f func()) {
	t.Helper()
	u, _ := windows.UTF16PtrFromString(user)
	d, _ := windows.UTF16PtrFromString(".")
	p, _ := windows.UTF16PtrFromString(pass)
	var tok windows.Token
	if r, _, err := procLogonUserW.Call(uintptr(unsafe.Pointer(u)), uintptr(unsafe.Pointer(d)),
		uintptr(unsafe.Pointer(p)), logon32Network, logon32ProviderDefault, uintptr(unsafe.Pointer(&tok))); r == 0 {
		t.Fatalf("LogonUser %s: %v", user, err)
	}
	defer tok.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.LockOSThread() // impersonation is the thread's; this thread ends with it
		if r, _, err := procImpersonateLoggedOn.Call(uintptr(tok)); r == 0 {
			t.Errorf("ImpersonateLoggedOnUser: %v", err)
			return
		}
		defer func() { _ = windows.RevertToSelf() }()
		f()
	}()
	<-done
}

// A client running as another user of this machine is refused: the pipe's DACL admits
// the relay's own user only. MAKTABA_RELAY_TEST_USER and _PASS name a local user CI makes
// for this; MAKTABA_RELAY_TEST_REQUIRE=1 fails rather than skips without one.
func TestAnotherUserIsRefused(t *testing.T) {
	user, pass := os.Getenv("MAKTABA_RELAY_TEST_USER"), os.Getenv("MAKTABA_RELAY_TEST_PASS")
	if user == "" || pass == "" {
		if os.Getenv("MAKTABA_RELAY_TEST_REQUIRE") == "1" {
			t.Fatal("MAKTABA_RELAY_TEST_USER and MAKTABA_RELAY_TEST_PASS are required")
		}
		t.Skip("no second local user (MAKTABA_RELAY_TEST_USER, MAKTABA_RELAY_TEST_PASS)")
	}
	host := echoHost(t)
	name := relay(t, func() (net.Conn, error) { return net.Dial("unix", host) })
	// the positive control: this user connects
	c, err := Dial(name)
	if err != nil {
		t.Fatalf("the relay's own user cannot connect: %v", err)
	}
	readReady(t, c)
	_ = c.Close()
	var other error
	asUser(t, user, pass, func() {
		c, err := Dial(name)
		if err == nil {
			_ = c.Close()
		}
		other = err
	})
	if !errors.Is(other, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("%s opened the relay's pipe (err %v), want access denied", user, other)
	}
}

// The pipeline's own Windows connect (pipeline/src/maktaba_pipeline/plugins/pipe.py, loaded
// alone: no package dependencies) through the real relay: Ready, then bytes both ways; and
// against a relay with no host, the ConnectionRefusedError the dispatcher falls back on.
func TestThePipelinesConnectThroughTheRelay(t *testing.T) {
	py := os.Getenv("MAKTABA_RELAY_TEST_PYTHON")
	if py == "" {
		if os.Getenv("MAKTABA_RELAY_TEST_REQUIRE") == "1" {
			t.Fatal("MAKTABA_RELAY_TEST_PYTHON is required")
		}
		t.Skip("no Python (MAKTABA_RELAY_TEST_PYTHON)")
	}
	src, err := filepath.Abs("../../../../pipeline/src/maktaba_pipeline/plugins/pipe.py")
	if err != nil {
		t.Fatal(err)
	}
	script := `
import asyncio, importlib.util, sys
spec = importlib.util.spec_from_file_location("pipe", sys.argv[1])
pipe = importlib.util.module_from_spec(spec); spec.loader.exec_module(pipe)
async def main(name, want):
    try:
        r, w = await pipe.open_pipe(name, timeout=10)
    except ConnectionRefusedError as e:
        print("REFUSED", e); return
    w.write(b"ping"); await w.drain()
    got = await asyncio.wait_for(r.readexactly(4), 10)
    print("ECHO", got.decode()); w.close()
asyncio.run(main(sys.argv[2], sys.argv[3]))
`
	run := func(name string) string {
		out, err := exec.Command(py, "-c", script, src, name, "x").CombinedOutput()
		if err != nil {
			t.Fatalf("python: %v\n%s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	host := echoHost(t)
	if got := run(relay(t, func() (net.Conn, error) { return net.Dial("unix", host) })); got != "ECHO ping" {
		t.Fatalf("through the relay: %q", got)
	}
	if got := run(relay(t, func() (net.Conn, error) { return nil, errors.New("no host") })); !strings.HasPrefix(got, "REFUSED") {
		t.Fatalf("a relay with no host: %q, want REFUSED", got)
	}
}
