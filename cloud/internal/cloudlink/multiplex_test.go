package cloudlink

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"
)

// goroutineCountStable returns runtime.NumGoroutine() after letting
// scheduled-for-exit goroutines actually finish. Goroutine teardown is
// asynchronous, so we poll with Gosched + a short sleep until the count
// settles at/below want (or the deadline elapses, returning the last
// reading for the caller to assert on).
func goroutineCountStable(want int, within time.Duration) int {
	deadline := time.Now().Add(within)
	last := runtime.NumGoroutine()
	for time.Now().Before(deadline) {
		runtime.Gosched()
		time.Sleep(10 * time.Millisecond)
		last = runtime.NumGoroutine()
		if last <= want {
			return last
		}
	}
	return last
}

func writeRequest(t *testing.T, c *pipeConn, streamID uint32, path string) {
	t.Helper()
	var head bytes.Buffer
	head.WriteString("GET " + path + " HTTP/1.1\r\nHost: x\r\n\r\n")
	if err := c.WriteFrame(Frame{Kind: KindRequestHead, StreamID: streamID, Payload: head.Bytes()}); err != nil {
		t.Fatalf("write REQUEST_HEAD: %v", err)
	}
	// Empty REQUEST_BODY = EOF, triggers handoff to handle().
	if err := c.WriteFrame(Frame{Kind: KindRequestBody, StreamID: streamID}); err != nil {
		t.Fatalf("write REQUEST_BODY EOF: %v", err)
	}
}

// TestMultiplexer_CloseStreamCancelsWedgedHandler is the M-4 / I-2
// integration regression: a loopback handler that sends headers then
// blocks the body forever pins the per-stream handle() goroutine. A
// cloud KindCloseStream must cancel that goroutine's context so it
// abandons the wedged body read and exits — previously CloseStream only
// deleted the demux map entry and left the goroutine leaked forever.
func TestMultiplexer_CloseStreamCancelsWedgedHandler(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release // wedge body forever
	}))
	defer local.Close()
	defer close(release)

	base := runtime.NumGoroutine()

	clientEnd, cloudEnd := newPipe()
	mux := NewMultiplexer(clientEnd, &LocalProxy{BaseURL: local.URL})
	defer mux.Close()

	writeRequest(t, cloudEnd, 42, "/wedge")

	// Wait until the loopback handler is actually inside the wedged body.
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatalf("loopback handler never reached the wedged body")
	}

	// Cloud aborts the stream. This must cancel the handle() goroutine.
	if err := cloudEnd.WriteFrame(Frame{Kind: KindCloseStream, StreamID: 42}); err != nil {
		t.Fatalf("write CLOSE_STREAM: %v", err)
	}

	// The handle() goroutine (and the http transport's reader) must drain
	// back toward baseline. Without the fix the goroutine stays blocked
	// in resp.Body.Read forever and the count never returns.
	got := goroutineCountStable(base+2, 5*time.Second)
	if got > base+2 {
		t.Fatalf("goroutines did not settle after CLOSE_STREAM: got %d, baseline %d "+
			"(handle() goroutine leaked — CloseStream did not cancel it)", got, base)
	}
}

// TestMultiplexer_CloseCancelsInflightHandlers proves tunnel teardown
// (Multiplexer.Close) also cancels in-flight per-stream goroutines, so a
// wedged loopback body does not survive the tunnel it belonged to.
func TestMultiplexer_CloseCancelsInflightHandlers(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
	}))
	defer local.Close()
	defer close(release)

	base := runtime.NumGoroutine()

	clientEnd, cloudEnd := newPipe()
	mux := NewMultiplexer(clientEnd, &LocalProxy{BaseURL: local.URL})

	writeRequest(t, cloudEnd, 7, "/wedge")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatalf("loopback handler never reached the wedged body")
	}

	mux.Close() // must cancel the in-flight handle() context

	got := goroutineCountStable(base+2, 5*time.Second)
	if got > base+2 {
		t.Fatalf("goroutines did not settle after Close: got %d, baseline %d "+
			"(in-flight handle() leaked past tunnel teardown)", got, base)
	}
}
