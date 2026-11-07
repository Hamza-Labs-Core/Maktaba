package relay

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	relaypkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// answerConn is a home that answers every request with 200 "ok" (closed:
// it refuses every frame as a closed tunnel would, and calls onWrite first).
type answerConn struct {
	in      chan relaypkg.Frame
	done    chan struct{}
	once    sync.Once
	closed  bool
	onWrite func()
}

func newAnswerConn(closed bool) *answerConn {
	return &answerConn{in: make(chan relaypkg.Frame, 16), done: make(chan struct{}), closed: closed}
}

func (c *answerConn) ReadFrame() (relaypkg.Frame, error) {
	select {
	case f := <-c.in:
		return f, nil
	case <-c.done:
		return relaypkg.Frame{}, errors.New("closed")
	}
}

func (c *answerConn) WriteFrame(f relaypkg.Frame) error {
	if c.closed {
		if c.onWrite != nil {
			c.onWrite()
		}
		return relaypkg.ErrTunnelClosed
	}
	if f.Kind == relaypkg.KindRequestBody && len(f.Payload) == 0 {
		c.in <- relaypkg.Frame{Kind: relaypkg.KindResponseHead, StreamID: f.StreamID, Payload: []byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\n")}
		c.in <- relaypkg.Frame{Kind: relaypkg.KindResponseBody, StreamID: f.StreamID, Payload: []byte("ok")}
		c.in <- relaypkg.Frame{Kind: relaypkg.KindResponseBody, StreamID: f.StreamID}
	}
	return nil
}

func (c *answerConn) Close() error { c.once.Do(func() { close(c.done) }); return nil }

type oneHome struct{}

func (oneHome) BySlug(_ context.Context, slug string) (stores.Server, error) {
	return stores.Server{ID: "id", Slug: slug, Plan: "pro"}, nil
}

// A GET whose tunnel closes under it before the home answered (a drained
// tunnel the home just left: the rehearsal's drain-1k saw one 502) is routed
// again, to the home's new tunnel; a POST is not.
func TestAGetIsRetriedOnTheNewTunnel(t *testing.T) {
	reg := relaypkg.NewRegistry()
	gone := newAnswerConn(true)
	live := relaypkg.NewTunnel("id", "acme", newAnswerConn(false))
	defer live.Close()
	old := relaypkg.NewTunnel("id", "acme", gone)
	defer old.Close()
	reg.Register("acme", old)
	gone.onWrite = func() { reg.Register("acme", live) } // the new tunnel took over meanwhile
	h := (&Deps{Registry: reg, Lookup: oneHome{}, PublicHost: "relay.test", HoldMax: -1, Logger: slog.New(slog.DiscardHandler)}).Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.Host = "acme.relay.test"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if b, _ := io.ReadAll(rec.Body); rec.Code != 200 || string(b) != "ok" {
		t.Fatalf("GET: %d %q", rec.Code, b)
	}

	old2 := relaypkg.NewTunnel("id", "acme", newAnswerConn(true))
	defer old2.Close()
	reg.Register("acme", old2)
	req = httptest.NewRequest(http.MethodPost, "/api/x", nil)
	req.Host = "acme.relay.test"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("POST: %d, want 502 (not retried)", rec.Code)
	}
}
