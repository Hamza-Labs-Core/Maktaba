package ws

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// SSE framing tests for plan-06-11 F9 (id: lines, Last-Event-ID replay)
// and the /ws/queues snapshot channel.

type fakeReplayer struct {
	gotChannel string
	gotLast    int64
	events     []ReplayEvent
}

func (f *fakeReplayer) Replay(_ context.Context, ch string, last int64) ([]ReplayEvent, error) {
	f.gotChannel, f.gotLast = ch, last
	return f.events, nil
}

func sseServer(t *testing.T, h *Handler) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: "u"}))
			next.ServeHTTP(w, req)
		})
	})
	h.Mount(r)
	srv := httptest.NewServer(r)
	t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
	return srv
}

// readFrames reads SSE frames (blocks of lines) until n have arrived.
func readFrames(t *testing.T, body io.Reader, n int) []string {
	t.Helper()
	sc := bufio.NewScanner(body)
	var frames []string
	var cur []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		for sc.Scan() {
			line := sc.Text()
			if line == "" {
				if len(cur) > 0 {
					frames = append(frames, strings.Join(cur, "\n"))
					cur = nil
					if len(frames) == n {
						return
					}
				}
				continue
			}
			if strings.HasPrefix(line, ":") {
				continue
			}
			cur = append(cur, line)
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out; frames so far: %q", frames)
	}
	return frames
}

func waitSubscribers(t *testing.T, h *Hub, ch string, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for h.SubscriberCount(ch) < n {
		if time.Now().After(deadline) {
			t.Fatalf("subscriber for %s never attached", ch)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestWriteFrame_IDLines(t *testing.T) {
	tests := []struct {
		name    string
		payload map[string]any
		wantID  string
	}{
		{"int64 id", map[string]any{"_event_id": int64(42)}, "id: 42\n"},
		{"float id (decoded JSON)", map[string]any{"_event_id": float64(7)}, "id: 7\n"},
		{"int id", map[string]any{"_event_id": 9}, "id: 9\n"},
		{"json.Number", map[string]any{"_event_id": json.Number("11")}, "id: 11\n"},
		{"no id -> ephemeral", map[string]any{"x": 1}, ""},
		{"zero id ignored", map[string]any{"_event_id": int64(0)}, ""},
		{"nil payload", nil, ""},
		{"wrong type ignored", map[string]any{"_event_id": "abc"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeFrame(rec, Event{Type: "t", At: time.Unix(0, 0).UTC(), Payload: tc.payload})
			out := rec.Body.String()
			if tc.wantID == "" {
				if strings.Contains(out, "id:") {
					t.Errorf("unexpected id line: %q", out)
				}
			} else if !strings.HasPrefix(out, tc.wantID+"data: ") {
				t.Errorf("frame = %q, want id line %q first", out, tc.wantID)
			}
			if !strings.HasSuffix(out, "\n\n") {
				t.Errorf("frame must end with a blank line: %q", out)
			}
		})
	}
}

func TestSSE_LiveEventsCarryIDLine(t *testing.T) {
	hub := NewHub()
	srv := sseServer(t, &Handler{Hub: hub})
	resp, err := http.Get(srv.URL + "/ws/jobs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	waitSubscribers(t, hub, "jobs", 1)

	hub.Publish("jobs", Event{Type: "job.finished", Payload: map[string]any{"_event_id": int64(123), "job": map[string]any{"id": 1}}})
	hub.Publish("jobs", Event{Type: "job.log", Payload: map[string]any{"msg": "no id"}})
	frames := readFrames(t, resp.Body, 2)
	if !strings.HasPrefix(frames[0], "id: 123\ndata: ") {
		t.Errorf("frame 0 = %q", frames[0])
	}
	if strings.Contains(frames[1], "id:") {
		t.Errorf("frame 1 must have no id: %q", frames[1])
	}
}

func TestSSE_ReconnectReplaysFromLastEventID(t *testing.T) {
	setters := map[string]func(*http.Request){
		"Last-Event-ID header": func(r *http.Request) { r.Header.Set("Last-Event-ID", "41") },
		"last_event_id query": func(r *http.Request) {
			q := r.URL.Query()
			q.Set("last_event_id", "41")
			r.URL.RawQuery = q.Encode()
		},
	}
	for name, set := range setters {
		t.Run(name, func(t *testing.T) {
			hub := NewHub()
			rp := &fakeReplayer{events: []ReplayEvent{
				{Type: "a", At: time.Unix(1, 0).UTC(), Payload: map[string]any{"_event_id": int64(42)}},
				{Type: "b", At: time.Unix(2, 0).UTC(), Payload: map[string]any{"_event_id": int64(43)}},
			}}
			srv := sseServer(t, &Handler{Hub: hub, Replay: rp})
			req, _ := http.NewRequest("GET", srv.URL+"/ws/jobs", nil)
			set(req)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			frames := readFrames(t, resp.Body, 2)
			if rp.gotChannel != "jobs" || rp.gotLast != 41 {
				t.Errorf("replay asked for %s after %d", rp.gotChannel, rp.gotLast)
			}
			if !strings.HasPrefix(frames[0], "id: 42\n") || !strings.HasPrefix(frames[1], "id: 43\n") {
				t.Errorf("replayed frames must carry their ids: %q", frames)
			}
		})
	}
}

func TestSSE_NoReplayForFreshClient(t *testing.T) {
	hub := NewHub()
	rp := &fakeReplayer{}
	srv := sseServer(t, &Handler{Hub: hub, Replay: rp})
	resp, err := http.Get(srv.URL + "/ws/jobs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	waitSubscribers(t, hub, "jobs", 1)
	if rp.gotLast != 0 || rp.gotChannel != "" {
		t.Errorf("fresh client must not trigger replay: %+v", rp)
	}
}

func TestSSE_QueuesChannelSendsInitialSnapshotThenLive(t *testing.T) {
	hub := NewHub()
	h := &Handler{Hub: hub, Initial: func(_ context.Context, ch string) *Event {
		if ch != "queues" {
			return nil
		}
		return &Event{Type: "queues.snapshot", At: time.Unix(5, 0).UTC(), Payload: map[string]any{"seq": "initial"}}
	}}
	srv := sseServer(t, h)
	resp, err := http.Get(srv.URL + "/ws/queues")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	waitSubscribers(t, hub, "queues", 1)
	hub.Publish("queues", Event{Type: "queues.snapshot", Payload: map[string]any{"seq": "tick"}})

	frames := readFrames(t, resp.Body, 2)
	if !strings.Contains(frames[0], `"seq":"initial"`) || !strings.Contains(frames[1], `"seq":"tick"`) {
		t.Errorf("frames = %q", frames)
	}
	for _, f := range frames {
		if strings.Contains(f, "id:") {
			t.Errorf("ephemeral snapshot frames carry no id: %q", f)
		}
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content-type = %s", ct)
	}
}

func TestSSE_OtherChannelsGetNoInitialFrame(t *testing.T) {
	hub := NewHub()
	h := &Handler{Hub: hub, Initial: func(_ context.Context, ch string) *Event {
		if ch != "queues" {
			return nil
		}
		return &Event{Type: "queues.snapshot"}
	}}
	srv := sseServer(t, h)
	resp, err := http.Get(srv.URL + "/ws/jobs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	waitSubscribers(t, hub, "jobs", 1)
	hub.Publish("jobs", Event{Type: "job.started"})
	frames := readFrames(t, resp.Body, 1)
	if !strings.Contains(frames[0], "job.started") {
		t.Errorf("first frame on /ws/jobs = %q", frames[0])
	}
}

func TestSSE_RequiresPrincipal(t *testing.T) {
	r := chi.NewRouter()
	(&Handler{Hub: NewHub()}).Mount(r)
	for _, path := range []string{"/ws/jobs", "/ws/queues"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s -> %d", path, rec.Code)
		}
	}
}

func TestHub_SubscriberCount(t *testing.T) {
	h := NewHub()
	if h.SubscriberCount("queues") != 0 {
		t.Fatal("empty hub")
	}
	a, b := h.Subscribe("queues"), h.Subscribe("queues")
	h.Subscribe("jobs")
	if h.SubscriberCount("queues") != 2 {
		t.Errorf("count = %d", h.SubscriberCount("queues"))
	}
	h.Unsubscribe("queues", a)
	h.Unsubscribe("queues", b)
	if h.SubscriberCount("queues") != 0 {
		t.Errorf("count after unsubscribe = %d", h.SubscriberCount("queues"))
	}
}
