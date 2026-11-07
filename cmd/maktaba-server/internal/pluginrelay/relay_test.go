package pluginrelay

import (
	"bytes"
	"crypto/rand"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// Pump carries every byte both ways at once, unchanged, and closes both ends when either
// side ends (on every OS: the Windows relay is this plus the pipe).
func TestPumpCarriesBothWaysAndClosesBoth(t *testing.T) {
	client, relayIn := net.Pipe()
	relayOut, host := net.Pipe()
	done := make(chan struct{})
	go func() {
		Pump(relayIn, relayOut)
		close(done)
	}()
	// the host echoes; net.Pipe has no buffer, so a pump that did one direction at a time
	// would stall here
	go func() { _, _ = io.Copy(host, host) }()
	want := make([]byte, 1<<20)
	_, _ = rand.Read(want)
	go func() { _, _ = client.Write(want) }()
	got := make([]byte, len(want))
	if _, err := io.ReadFull(client, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("the bytes changed on the way")
	}
	_ = client.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the pump did not end when the client closed")
	}
	if _, err := host.Write([]byte("x")); err == nil {
		t.Fatal("the host's end stayed open after the client closed")
	}
}

// A pipe name is the relay's prefix and 128 random bits, new each time.
func TestPipeNamesAreUnguessable(t *testing.T) {
	a, err := PipeName()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := PipeName()
	const prefix = `\\.\pipe\maktaba-plugind-`
	if !strings.HasPrefix(a, prefix) || len(a) != len(prefix)+32 || a == b {
		t.Fatalf("names %q and %q", a, b)
	}
}
