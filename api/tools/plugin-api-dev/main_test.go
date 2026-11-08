package main

import (
	"io"
	"testing"
	"time"
)

// -watch-stdin: the server stops once its parent's end of stdin closes, and not before.
func TestStopWhenClosedStopsOnlyAfterTheParentCloses(t *testing.T) {
	r, w := io.Pipe()
	stopped := make(chan struct{})
	go stopWhenClosed(r, func() { close(stopped) })

	// the parent still holds its end and writes now and then: no stop
	if _, err := w.Write([]byte("still here\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
		t.Fatal("stopped while the parent still held stdin")
	default:
	}

	// the parent goes away
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("did not stop after stdin closed")
	}
}
