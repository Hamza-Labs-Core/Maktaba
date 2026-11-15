package broadcast

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// The control socket names the channel's mode on connect (the edge wakes and sleeps only an
// on-demand channel) and hands the edge's wake, sleep and limits to its listener.
func TestControlNamesModeAndHearsDemand(t *testing.T) {
	var mu sync.Mutex
	var gotMode, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotMode, gotAuth = r.Header.Get(ModeHeader), r.Header.Get("Authorization")
		mu.Unlock()
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for _, m := range []string{`{"type":"hello","status":"waiting"}`, `{"type":"wake"}`,
			`{"type":"limits","rungs":["480p"]}`, `{"type":"sleep"}`} {
			if conn.Write(r.Context(), websocket.MessageText, []byte(m)) != nil {
				return
			}
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	var heard []Event
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Control{Mode: "on_demand",
		Creds: func() (Creds, bool) { return Creds{Token: "tok", IngestURL: srv.URL}, true },
		On: func(ev Event) {
			mu.Lock()
			defer mu.Unlock()
			heard = append(heard, ev)
			if len(heard) == 4 {
				close(done)
			}
		}}
	go c.Run(ctx)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the events never came")
	}
	mu.Lock()
	defer mu.Unlock()
	if gotMode != "on_demand" || gotAuth != "Bearer tok" {
		t.Fatalf("connect headers: mode %q, auth %q", gotMode, gotAuth)
	}
	if heard[1].Type != "wake" || heard[2].Type != "limits" || len(heard[2].Rungs) != 1 || heard[2].Rungs[0] != "480p" ||
		heard[3].Type != "sleep" {
		t.Fatalf("heard %+v", heard)
	}
}
