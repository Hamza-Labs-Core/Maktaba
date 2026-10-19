package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/relay"
)

const relaySecret = "plex-token-for-the-relay-test"

type relayPlex struct {
	mu   sync.Mutex
	seen []string
}

func (p *relayPlex) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.seen = append(p.seen, r.Method+" "+r.URL.RequestURI()+" "+r.Header.Get("X-Plex-Token"))
	p.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"MediaContainer":{"Directory":[{"key":"1","type":"movie","thumb":"/t?X-Plex-Token=`+
		relaySecret+`"}]},"pad":"`+strings.Repeat("p", 3000)+`"}`)
}

func (p *relayPlex) calls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.seen...)
}

func withDoor(plexURL string) fixtureOpt {
	return func(c *Config) {
		c.Relay = relay.New(relay.Config{
			Upstream: func(context.Context, *relay.Extension) (relay.Upstream, error) {
				return relay.Upstream{Enabled: true, URL: plexURL, Secret: relaySecret}, nil
			},
			Chunk:  1000,
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
	}
}

// relayCall sends svc.request and collects the answer.
func relayCall(t *testing.T, a *testAgent, id, method, path string) (map[string]any, string) {
	t.Helper()
	a.send(map[string]any{"t": "svc.request", "id": id, "ext": "plex", "method": method, "path": path,
		"accept": "application/json", "timeout_ms": 5000})
	var body strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		m, err := a.recvCtx(ctx)
		if err != nil {
			t.Fatalf("waiting for svc.done: %v", err)
		}
		if m["id"] != id {
			continue
		}
		switch m["t"] {
		case "svc.chunk":
			b, err := base64.StdEncoding.DecodeString(m["data"].(string))
			if err != nil {
				t.Fatal(err)
			}
			body.Write(b)
		case "svc.done":
			return m, body.String()
		}
	}
}

func errCode(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	if e == nil {
		return ""
	}
	s, _ := e["code"].(string)
	return s
}

func TestRelayThroughTheChannel(t *testing.T) {
	plex := &relayPlex{}
	srv := httptest.NewServer(plex)
	defer srv.Close()
	f := newFixture(t, withDoor(srv.URL))
	a, nodeID := f.node("mac")

	done, body := relayCall(t, a, "r1", "GET", "/library/sections")
	if errCode(done) != "" || done["status"].(float64) != 200 || done["content_type"] != "application/json" {
		t.Fatalf("done %v", done)
	}
	if strings.Contains(body, relaySecret) || !strings.Contains(body, `"thumb":"/t?"`) || len(body) < 3000 {
		t.Fatalf("body %.200s", body)
	}
	if got := plex.calls(); len(got) != 1 || got[0] != "GET /library/sections "+relaySecret {
		t.Fatalf("plex saw %v", got)
	}

	// refused calls never reach Plex
	done, _ = relayCall(t, a, "r2", "GET", "/:/prefs")
	if errCode(done) != relay.CodeNotAllowed {
		t.Fatalf("not allowed: %v", done)
	}
	done, _ = relayCall(t, a, "r3", "GET", "/identity?X-Plex-Token=x")
	if errCode(done) != relay.CodeNotAllowed {
		t.Fatalf("token in query: %v", done)
	}
	if len(plex.calls()) != 1 {
		t.Fatalf("plex saw %v", plex.calls())
	}

	// the Machines page: the door's status and the recent calls, admin only
	if resp := f.do("GET", "/api/cluster/relay", "", "viewer"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer: %d", resp.StatusCode)
	}
	resp := f.do("GET", "/api/cluster/relay", "", "admin")
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || strings.Contains(string(raw), relaySecret) {
		t.Fatalf("status %d %s", resp.StatusCode, raw)
	}
	var st relay.Status
	decodeBytes(t, raw, &st)
	if !st.Available || !st.Enabled || len(st.Calls) != 3 || st.Calls[2].NodeID != nodeID || st.Calls[2].Status != 200 ||
		st.Calls[0].Error != relay.CodeNotAllowed || len(st.Extensions) != 1 || !st.Extensions[0].Configured {
		t.Fatalf("status %+v", st)
	}
	var homeOnly []string
	for _, h := range st.HomeOnly {
		homeOnly = append(homeOnly, h.Name)
	}
	if strings.Join(homeOnly, ",") != "cloudflare_stream,dizquetv" {
		t.Fatalf("home only %v", homeOnly)
	}
}

func TestRelayRefusesARevokedNode(t *testing.T) {
	plex := &relayPlex{}
	srv := httptest.NewServer(plex)
	defer srv.Close()
	f := newFixture(t, withDoor(srv.URL))
	a, nodeID := f.node("mac")
	if done, _ := relayCall(t, a, "r1", "GET", "/identity"); errCode(done) != "" {
		t.Fatalf("approved: %v", done)
	}
	// the registry says revoked before the hub closed the session
	if err := f.store.Revoke(context.Background(), nodeID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if done, _ := relayCall(t, a, "r2", "GET", "/identity"); errCode(done) != relay.CodeNotMember {
		t.Fatalf("revoked: %v", done)
	}
	if len(plex.calls()) != 1 {
		t.Fatalf("plex saw %v", plex.calls())
	}
}

func TestRelayWithoutDoor(t *testing.T) {
	f := newFixture(t)
	a, _ := f.node("mac")
	if done, _ := relayCall(t, a, "r1", "GET", "/identity"); errCode(done) != CodeNoDoor {
		t.Fatalf("no door: %v", done)
	}
	resp := f.do("GET", "/api/cluster/relay", "", "admin")
	var st relay.Status
	decode(t, resp, &st)
	if st.Available || st.Enabled {
		t.Fatalf("status %+v", st)
	}
}

func decodeBytes(t *testing.T, b []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}
