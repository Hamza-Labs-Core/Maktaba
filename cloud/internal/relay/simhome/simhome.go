// Package simhome runs simulated homes: real cloudlink agents (Supervisor,
// Dialer, Multiplexer) against a relay, each serving a small loopback API.
// The tunnel handover tests (story 50.03), the rehearsal (50.06) and the load
// test (50.14) use it.
//
// Every home serves, through its tunnel:
//
//	GET /api/health        200 "ok <home>"
//	GET /stream?bytes=N&ms=D  N bytes of a known pattern, written over D ms
//	GET /hang              headers, then nothing until the client leaves
package simhome

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/cloudlink"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// Pattern is byte i of every /stream body, so a client can check each byte.
func Pattern(i int) byte { return byte('a' + i%26) }

// API is the loopback API every simulated home serves (one server for all).
func API() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		home := r.Header.Get("X-Sim-Home")
		switch r.URL.Path {
		case "/api/health":
			_, _ = fmt.Fprintf(w, "ok %s", home)
		case "/stream":
			n, _ := strconv.Atoi(r.URL.Query().Get("bytes"))
			ms, _ := strconv.Atoi(r.URL.Query().Get("ms"))
			w.Header().Set("Content-Length", strconv.Itoa(n))
			w.WriteHeader(http.StatusOK)
			fl, _ := w.(http.Flusher)
			const chunks = 20
			step := time.Duration(ms) * time.Millisecond / chunks
			buf := make([]byte, 0, n/chunks+1)
			for c, i := 0, 0; c < chunks; c++ {
				end := n * (c + 1) / chunks
				buf = buf[:0]
				for ; i < end; i++ {
					buf = append(buf, Pattern(i))
				}
				if _, err := w.Write(buf); err != nil {
					return
				}
				if fl != nil {
					fl.Flush()
				}
				select {
				case <-r.Context().Done():
					return
				case <-time.After(step):
				}
			}
		case "/hang":
			w.WriteHeader(http.StatusOK)
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
}

// Creds names home i: its server id, secret and slug.
func Creds(i int) (id, secret, slug string) {
	return fmt.Sprintf("home-%04d", i), fmt.Sprintf("secret-%04d", i), fmt.Sprintf("home-%04d", i)
}

// Store is a relay.TunnelStore (and relay.ResumeStore) that knows the
// simulated homes: bearer checks compare the secret (counted, so a test can
// prove a ticket handshake did none), and Revoke takes a home away.
type Store struct {
	mu      sync.Mutex
	revoked map[string]bool
	bearers int
}

// NewStore is an empty store: every simulated home is known.
func NewStore() *Store { return &Store{revoked: map[string]bool{}} }

// Bearers is how many bearer (secret) checks the relay asked for.
func (s *Store) Bearers() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bearers
}

// Revoke takes server id away (its secret and its tickets stop working).
func (s *Store) Revoke(id string) {
	s.mu.Lock()
	s.revoked[id] = true
	s.mu.Unlock()
}

func (s *Store) server(id string) (stores.Server, error) {
	var n int
	if _, err := fmt.Sscanf(id, "home-%04d", &n); err != nil {
		return stores.Server{}, fmt.Errorf("simhome: unknown server %q", id)
	}
	if s.revoked[id] {
		return stores.Server{}, relay.ErrServerRevoked
	}
	return stores.Server{ID: id, Slug: id}, nil
}

// Authenticate is the bearer check.
func (s *Store) Authenticate(_ context.Context, id, secret string) (stores.Server, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bearers++
	sv, err := s.server(id)
	if err != nil {
		return sv, err
	}
	var n int
	_, _ = fmt.Sscanf(id, "home-%04d", &n)
	if _, want, _ := Creds(n); secret != want {
		return stores.Server{}, fmt.Errorf("simhome: bad secret")
	}
	return sv, nil
}

// Resume is the ticket's standing check (no secret).
func (s *Store) Resume(_ context.Context, id string) (stores.Server, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.server(id)
}

// SetOnline records nothing.
func (s *Store) SetOnline(context.Context, string, bool) error { return nil }

// Home is one running simulated home.
type Home struct {
	ID, Slug string
	Sup      *cloudlink.Supervisor
	cancel   context.CancelFunc
	done     chan struct{}
}

// Options tunes the homes' agents.
type Options struct {
	BaseWait, MaxWait time.Duration // reconnect backoff (default 50 ms, 1 s)
	PingEvery         time.Duration // keepalive (default 5 s)
	// Legacy homes announce no features (an older agent, no DRAIN, no tickets).
	Legacy bool
	// ServerID replaces Creds' id: the servers row's id when the relay is the
	// real one, on a seeded database (the rehearsal).
	ServerID string
}

// Start runs home i against the relay's tunnel endpoint (ws://…/v1/relay/ws),
// its loopback API at api.
func Start(i int, endpoint, api string, o Options) *Home {
	id, secret, slug := Creds(i)
	if o.ServerID != "" {
		id = o.ServerID
	}
	if o.BaseWait == 0 {
		o.BaseWait = 50 * time.Millisecond
	}
	if o.MaxWait == 0 {
		o.MaxWait = time.Second
	}
	if o.PingEvery == 0 {
		o.PingEvery = 5 * time.Second
	}
	d := &cloudlink.Dialer{Endpoint: endpoint, ServerID: id, Secret: secret, HandshakeTimeout: 10 * time.Second}
	sup := &cloudlink.Supervisor{
		Dialer:    d,
		Proxy:     &cloudlink.LocalProxy{BaseURL: api, Client: homeClient(id)},
		BaseWait:  o.BaseWait,
		MaxWait:   o.MaxWait,
		PingEvery: o.PingEvery,
	}
	if o.Legacy {
		cloudlink.LegacyAgent(sup)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &Home{ID: id, Slug: slug, Sup: sup, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(h.done)
		_ = sup.Run(ctx)
	}()
	return h
}

// Stop ends the home's agent and waits for it.
func (h *Home) Stop() {
	h.cancel()
	<-h.done
}

// Online reports whether the home's agent has a tunnel.
func (h *Home) Online() bool { return h.Sup.State().Status == "online" }

// homeClient tags each loopback call with the home's id.
func homeClient(id string) *http.Client {
	return &http.Client{Transport: tagger{id: id, rt: &http.Transport{MaxIdleConnsPerHost: 4}}}
}

type tagger struct {
	id string
	rt http.RoundTripper
}

func (t tagger) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("X-Sim-Home", t.id)
	return t.rt.RoundTrip(r)
}
