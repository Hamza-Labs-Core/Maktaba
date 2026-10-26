package cloudlink

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// sendRequest writes a GET's head and empty body on the cloud end.
func sendRequest(t *testing.T, cloudEnd *pipeConn, id uint32, method, path string, body []byte) {
	t.Helper()
	head := method + " " + path + " HTTP/1.1\r\nHost: acme.relay.test\r\n\r\n"
	if err := cloudEnd.WriteFrame(Frame{Kind: KindRequestHead, StreamID: id, Payload: []byte(head)}); err != nil {
		t.Error(err)
		return
	}
	for len(body) > 0 {
		n := min(len(body), 32*1024)
		if err := cloudEnd.WriteFrame(Frame{Kind: KindRequestBody, StreamID: id, Payload: body[:n]}); err != nil {
			return // the agent may have refused it already
		}
		body = body[n:]
	}
	_ = cloudEnd.WriteFrame(Frame{Kind: KindRequestBody, StreamID: id})
}

// cloudReader is the one reader of the cloud end's frames.
func cloudReader(cloudEnd *pipeConn) chan Frame {
	frames := make(chan Frame, 64)
	go func() {
		for {
			f, err := cloudEnd.ReadFrame()
			if err != nil {
				close(frames)
				return
			}
			frames <- f
		}
	}()
	return frames
}

// readBodyUntilQuiet counts RESPONSE_BODY payload bytes for id until EOF
// or quiet for d.
func readBodyUntilQuiet(t *testing.T, frames chan Frame, id uint32, d time.Duration) (int, bool) {
	t.Helper()
	got := 0
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				return got, false
			}
			if f.StreamID == id && f.Kind == KindResponseBody {
				if len(f.Payload) == 0 {
					return got, true
				}
				got += len(f.Payload)
			}
		case <-time.After(d):
			return got, false
		}
	}
}

// With flow control on, the agent sends no more than the window until the
// cloud grants more.
func TestMultiplexer_FlowControlHonoursTheWindow(t *testing.T) {
	total := 3 * InitialWindow
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("v"), total))
	}))
	defer local.Close()
	cloudEnd, clientEnd := newPipe()
	mux := NewMultiplexer(clientEnd, &LocalProxy{BaseURL: local.URL}, WithFlowControl())
	defer mux.Close()

	frames := cloudReader(cloudEnd)
	sendRequest(t, cloudEnd, 1, "GET", "/v", nil)
	got, done := readBodyUntilQuiet(t, frames, 1, 300*time.Millisecond)
	if done || got > InitialWindow {
		t.Fatalf("sent %d bytes (done=%v) on a %d-byte window", got, done, InitialWindow)
	}
	var p [4]byte
	binary.BigEndian.PutUint32(p[:], uint32(total))
	if err := cloudEnd.WriteFrame(Frame{Kind: KindWindowUpdate, StreamID: 1, Payload: p[:]}); err != nil {
		t.Fatal(err)
	}
	more, done := readBodyUntilQuiet(t, frames, 1, 2*time.Second)
	if !done || got+more != total {
		t.Fatalf("after the grant: %d+%d bytes (done=%v), want %d", got, more, done, total)
	}
}

// Without it (an older cloud) the agent streams as before.
func TestMultiplexer_NoFlowStreamsUnwindowed(t *testing.T) {
	total := 3 * InitialWindow
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("v"), total))
	}))
	defer local.Close()
	cloudEnd, clientEnd := newPipe()
	mux := NewMultiplexer(clientEnd, &LocalProxy{BaseURL: local.URL})
	defer mux.Close()
	frames := cloudReader(cloudEnd)
	sendRequest(t, cloudEnd, 1, "GET", "/v", nil)
	got, done := readBodyUntilQuiet(t, frames, 1, 2*time.Second)
	if !done || got != total {
		t.Fatalf("got %d (done=%v), want %d", got, done, total)
	}
}

// A request body over MaxRequestBody is answered 413 without a call.
func TestMultiplexer_RequestBodyTooLarge(t *testing.T) {
	old := MaxRequestBody
	MaxRequestBody = 64 * 1024
	defer func() { MaxRequestBody = old }()
	var called atomic.Bool
	local := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called.Store(true) }))
	defer local.Close()
	cloudEnd, clientEnd := newPipe()
	mux := NewMultiplexer(clientEnd, &LocalProxy{BaseURL: local.URL})
	defer mux.Close()

	go sendRequest(t, cloudEnd, 3, "POST", "/upload", bytes.Repeat([]byte("u"), 200*1024))
	go func() {
		time.Sleep(3 * time.Second)
		_ = cloudEnd.Close() // unblocks ReadFrame below if no answer came
	}()
	for {
		f, err := cloudEnd.ReadFrame()
		if err != nil {
			t.Fatal(err)
		}
		if f.Kind == KindResponseHead && f.StreamID == 3 {
			if !strings.HasPrefix(string(f.Payload), "HTTP/1.1 413") {
				t.Fatalf("head %q", f.Payload)
			}
			break
		}
	}
	if called.Load() {
		t.Fatal("the local API was called with an oversized body")
	}
}

// A tunnel the cloud stopped answering (half-open TCP: no PONG, nothing)
// is closed after PongWait and redialled.
func TestSupervisor_SilentTunnelReconnects(t *testing.T) {
	var dials atomic.Int32
	dial := func(context.Context) (DialResult, error) {
		dials.Add(1)
		clientEnd, cloudEnd := newPipe()
		// the cloud end swallows our PINGs and never answers
		go func() {
			for {
				if _, err := cloudEnd.ReadFrame(); err != nil {
					return
				}
			}
		}()
		return DialResult{Conn: clientEnd, Slug: "acme"}, nil
	}
	sup := &Supervisor{
		Dialer:    &Dialer{},
		Proxy:     &LocalProxy{BaseURL: "http://127.0.0.1:1"},
		BaseWait:  5 * time.Millisecond,
		MaxWait:   10 * time.Millisecond,
		PingEvery: 20 * time.Millisecond,
		PongWait:  100 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = runSupervisorWithDial(ctx, sup, dial); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for dials.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	// the pipe never errors by itself: only the liveness check ends a tunnel
	if n := dials.Load(); n < 3 {
		t.Fatalf("a silent tunnel was redialled %d times in 3s, want ≥3", n)
	}
}

// An agent announces flow control; it uses it only when the cloud agreed.
func TestAuthenticate_NegotiatesFlow(t *testing.T) {
	for _, cloudFlow := range []int{0, FlowVersion} {
		clientEnd, cloudEnd := newPipe()
		go func() {
			f, _ := cloudEnd.ReadFrame()
			var ap authPayload
			_ = json.Unmarshal(f.Payload, &ap)
			if ap.Flow != FlowVersion {
				t.Errorf("the agent announced flow %d", ap.Flow)
			}
			ok, _ := json.Marshal(authOKPayload{Slug: "acme", Flow: cloudFlow})
			_ = cloudEnd.WriteFrame(Frame{Kind: KindAuthOK, Payload: ok})
		}()
		res, err := authenticate(clientEnd, "srv-1", "sek")
		if err != nil {
			t.Fatal(err)
		}
		if res.Flow != (cloudFlow == FlowVersion) {
			t.Fatalf("cloud flow %d: agent flow %v", cloudFlow, res.Flow)
		}
	}
}
