package pluginrelay

import (
	"bytes"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// A runner offers a waiting connection; a caller is paired with it: the runner reads Ready
// (a caller is here), the caller reads Ready (the runner answered), and then the bytes pass
// both ways unchanged (story 54.23 on Windows: the runner, in its AppContainer, may not
// create a pipe, so it dials the supervisor and the supervisor hands it its callers).
func TestABrokerPairsACallerWithAWaitingRunner(t *testing.T) {
	b := NewBroker(time.Second)
	runnerEnd, offered := net.Pipe()
	go func() { b.Offer(offered) }()
	callerEnd, accepted := net.Pipe()
	done := make(chan struct{})
	go func() {
		b.Serve(accepted)
		close(done)
	}()

	one := make([]byte, 1)
	if _, err := io.ReadFull(runnerEnd, one); err != nil || one[0] != Ready {
		t.Fatalf("the runner got %v %v, want Ready", one, err)
	}
	if _, err := io.ReadFull(callerEnd, one); err != nil || one[0] != Ready {
		t.Fatalf("the caller got %v %v, want Ready", one, err)
	}
	go func() { _, _ = io.Copy(runnerEnd, runnerEnd) }() // the runner echoes
	want := []byte("hello through the broker")
	go func() { _, _ = callerEnd.Write(want) }()
	got := make([]byte, len(want))
	if _, err := io.ReadFull(callerEnd, got); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("got %q %v", got, err)
	}
	_ = callerEnd.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the pairing did not end when the caller closed")
	}
}

// A runner connection that went away while it waited (the runner exited) is never handed a
// caller: the broker drops it and pairs the caller with the next one.
func TestABrokerSkipsARunnerThatWentAway(t *testing.T) {
	b := NewBroker(time.Second)
	gone, offeredGone := net.Pipe()
	_ = gone.Close()
	b.Offer(offeredGone)
	alive, offeredAlive := net.Pipe()
	go func() { b.Offer(offeredAlive) }()
	got := make(chan byte, 1)
	go func() {
		one := make([]byte, 1)
		if _, err := io.ReadFull(alive, one); err == nil {
			got <- one[0]
		}
	}()
	c, err := b.Take()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	select {
	case v := <-got:
		if v != Ready {
			t.Fatalf("the live runner got %v", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the live runner was never handed the caller")
	}
}

// With no runner waiting (it is not up, or starting), a caller is closed without Ready
// once the wait is over: the worker's open raises as for a refused unix socket and it runs
// the job itself, as on the other systems.
func TestABrokerClosesACallerWhenNoRunnerWaits(t *testing.T) {
	b := NewBroker(50 * time.Millisecond)
	if _, err := b.Take(); !errors.Is(err, ErrNoRunner) {
		t.Fatalf("Take with no runner: %v", err)
	}
	callerEnd, accepted := net.Pipe()
	go b.Serve(accepted)
	_ = callerEnd.SetReadDeadline(time.Now().Add(5 * time.Second))
	one := make([]byte, 1)
	if n, err := callerEnd.Read(one); n != 0 || err == nil {
		t.Fatalf("the caller read %d bytes (%v), want a close without Ready", n, err)
	}
}

// The broker keeps a bounded number of waiting connections: one more is closed, so a
// runner cannot pile up handles in the supervisor.
func TestABrokerBoundsTheWaitingConnections(t *testing.T) {
	b := NewBroker(time.Second)
	for i := 0; i < MaxWaiting; i++ {
		_, c := net.Pipe()
		b.Offer(c)
	}
	extra, c := net.Pipe()
	b.Offer(c)
	_ = extra.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := extra.Read(make([]byte, 1)); err == nil {
		t.Fatal("a connection beyond the bound was kept")
	}
}
