// Package homelink is a full node's way to the home (Epic 35 stories 35.9, 35.10): the
// session checks, the cluster sign-in's exchange, the home's JWKS, the home-class
// requests the node proxies, and the events the home pushes (a new delegation key, a
// revocation).
//
// Link is the interface the node's API depends on. Today's implementation, HTTPLink,
// is the "legacy path" of design §18: plain HTTP to the home's API, every request
// signed with the node's delegation key (cluster/delegation), the key and the
// revocations arriving from the node supervisor's machine agent (it holds the cluster
// channel, design §9.2) over loopback. Story 35.16 swaps in a Link that carries the same
// calls on the machine's mTLS control channel; nothing above this package changes.
package homelink

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/safego"
)

// Errors of a Link call.
var (
	// ErrUnauthorized: the home answered and refused (a dead session, a bad code).
	ErrUnauthorized = errors.New("homelink: refused by the home")
	// ErrUnreachable: no answer from the home (down, the network, no key yet).
	ErrUnreachable = errors.New("homelink: home unreachable")
)

// Event is something the home pushed to this node.
type Event struct {
	// Kind: "keys" (Key set) or "revoked" (Revoked set).
	Kind    string
	Key     *delegation.Key
	Revoked *delegation.Revoked
}

// Link is the node's way to the home.
type Link interface {
	// Transport carries a home-class request to the home (it signs each one).
	Transport() http.RoundTripper
	// HomeURL is the home's API base URL the Transport sends to.
	HomeURL() string
	Introspect(ctx context.Context, req delegation.IntrospectRequest) (*delegation.IntrospectAnswer, error)
	// JWKS is the home's JSON Web Key Set (bearer access tokens are verified locally).
	JWKS(ctx context.Context) ([]byte, error)
	// Self is the home's GET /api/cluster/self (its name, web address, version).
	Self(ctx context.Context) (json.RawMessage, error)
	// SignOut hands the home a sign-out made on this node while it did not answer.
	SignOut(ctx context.Context, req delegation.SignOutRequest) (*delegation.SignOutAnswer, error)
	// Subscribe registers fn for pushed events; it is called from one goroutine.
	Subscribe(fn func(Event))
	// HasKey reports whether the node holds a delegation key yet.
	HasKey() bool
}

// LinkTransport is the machine's cluster link (story 35.16): its control channel carries
// the home calls while it is up.
type LinkTransport interface {
	Connected() bool
	RoundTrip(*http.Request) (*http.Response, error)
}

// HTTPLink is the node's Link: the calls go on the machine's cluster link while it is up
// (its certificate proves the machine: nothing is signed), else as signed HTTP to the
// home (the legacy path of design §18, while the home does not require the link).
type HTTPLink struct {
	Home   string // e.g. http://home:8080
	NodeID string
	// Base is the transport under the signer (nil: a clone of http.DefaultTransport).
	Base http.RoundTripper
	// Link is the machine's cluster link (nil: none, the legacy path only).
	Link   LinkTransport
	Now    func() time.Time
	Logger *slog.Logger

	key   atomic.Pointer[delegation.Key]
	mu    sync.Mutex
	subs  []func(Event)
	once  sync.Once
	trans http.RoundTripper
}

var _ Link = (*HTTPLink)(nil)

func (l *HTTPLink) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// SetKey installs the delegation key (from the machine agent).
func (l *HTTPLink) SetKey(k delegation.Key) {
	l.key.Store(&k)
	l.emit(Event{Kind: "keys", Key: &k})
}

// HasKey implements Link: the node can act for users (a delegation key, or its link up).
func (l *HTTPLink) HasKey() bool {
	return l.key.Load() != nil || (l.Link != nil && l.Link.Connected())
}

// HomeURL implements Link.
func (l *HTTPLink) HomeURL() string { return strings.TrimRight(l.Home, "/") }

// Subscribe implements Link.
func (l *HTTPLink) Subscribe(fn func(Event)) {
	l.mu.Lock()
	l.subs = append(l.subs, fn)
	l.mu.Unlock()
}

// Emit delivers an event to the subscribers (the machine-agent feed calls it).
func (l *HTTPLink) emit(e Event) {
	l.mu.Lock()
	subs := append([]func(Event){}, l.subs...)
	l.mu.Unlock()
	for _, fn := range subs {
		fn(e)
	}
}

// Revoke delivers a revocation to the subscribers.
func (l *HTTPLink) Revoke(r delegation.Revoked) { l.emit(Event{Kind: "revoked", Revoked: &r}) }

type signer struct {
	l    *HTTPLink
	base http.RoundTripper
}

func (s signer) RoundTrip(r *http.Request) (*http.Response, error) {
	if lt := s.l.Link; lt != nil && lt.Connected() {
		resp, err := lt.RoundTrip(r)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
		}
		return resp, nil
	}
	k := s.l.key.Load()
	if k == nil {
		return nil, fmt.Errorf("%w: no delegation key yet (the machine agent has not reached the home)", ErrUnreachable)
	}
	if err := delegation.SignRequest(r, s.l.NodeID, *k, s.l.now()); err != nil {
		return nil, err
	}
	r.Header.Set(delegation.HeaderVia, delegation.ViaNode)
	return s.base.RoundTrip(r)
}

// Transport implements Link.
func (l *HTTPLink) Transport() http.RoundTripper {
	l.once.Do(func() {
		base := l.Base
		if base == nil {
			t := http.DefaultTransport.(*http.Transport).Clone()
			t.MaxIdleConnsPerHost = 32
			t.ResponseHeaderTimeout = 0 // streams and SSE; JSON calls set their own deadline
			base = t
		}
		l.trans = signer{l: l, base: base}
	})
	return l.trans
}

func (l *HTTPLink) call(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, l.HomeURL()+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := l.Transport().RoundTrip(req)
	if err != nil {
		if errors.Is(err, ErrUnreachable) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return ErrUnauthorized
	case resp.StatusCode >= 500:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return fmt.Errorf("%w: %s answered %d", ErrUnreachable, path, resp.StatusCode)
	case resp.StatusCode >= 300:
		return fmt.Errorf("homelink: %s answered %d", path, resp.StatusCode)
	}
	if raw, ok := out.(*[]byte); ok {
		b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		*raw = b
		return err
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

// Introspect implements Link.
func (l *HTTPLink) Introspect(ctx context.Context, req delegation.IntrospectRequest) (*delegation.IntrospectAnswer, error) {
	var a delegation.IntrospectAnswer
	if err := l.call(ctx, http.MethodPost, "/api/cluster/delegate/introspect", req, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// JWKS implements Link.
func (l *HTTPLink) JWKS(ctx context.Context) ([]byte, error) {
	var b []byte
	err := l.call(ctx, http.MethodGet, "/api/.well-known/jwks.json", nil, &b)
	return b, err
}

// Self implements Link.
func (l *HTTPLink) Self(ctx context.Context) (json.RawMessage, error) {
	var b []byte
	err := l.call(ctx, http.MethodGet, "/api/cluster/self", nil, &b)
	return b, err
}

// SignOut implements Link.
func (l *HTTPLink) SignOut(ctx context.Context, req delegation.SignOutRequest) (*delegation.SignOutAnswer, error) {
	var a delegation.SignOutAnswer
	if err := l.call(ctx, http.MethodPost, "/api/cluster/delegate/signout", req, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// ---------- the machine agent's feed ----------

// AgentFeed follows the node supervisor's machine agent on loopback: GET
// <agent>/agent/events (text/event-stream, the per-boot token as a bearer) carries the
// delegation key (event "keys") and the revocations (event "revoked"); it reconnects
// until ctx ends.
type AgentFeed struct {
	URL       string // the supervisor's loopback admin, e.g. http://127.0.0.1:9103
	TokenFile string
	Link      *HTTPLink
	Logger    *slog.Logger
	// Connected is set while the feed is up (the node's /api/cluster/self reports it).
	Connected atomic.Bool
}

type agentKeys struct {
	Delegation struct {
		ID       string    `json:"id"`
		Key      []byte    `json:"key"`
		NotAfter time.Time `json:"not_after"`
	} `json:"delegation"`
}

// Run follows the feed until ctx ends.
func (f *AgentFeed) Run(ctx context.Context) {
	delay := time.Second
	var repeat safego.Repeat // a feed that keeps failing the same way is said once at Warn
	for ctx.Err() == nil {
		start := time.Now()
		err := f.once(ctx)
		f.Connected.Store(false)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > time.Minute {
			delay = time.Second
			repeat.Level(nil) // it ran a while: the next failure is news
		}
		if f.Logger != nil && err != nil {
			f.Logger.Log(ctx, repeat.Level(err), "node: machine agent feed ended; reconnecting", "err", err,
				"event", "node_agent_feed_retry")
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, 15*time.Second)
	}
}

func (f *AgentFeed) once(ctx context.Context) error {
	tok, err := os.ReadFile(f.TokenFile)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(f.URL, "/")+"/agent/events", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("agent events: %d", resp.StatusCode)
	}
	f.Connected.Store(true)
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	var event string
	var data strings.Builder
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			f.dispatch(event, data.String())
			event = ""
			data.Reset()
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(line[len("event:"):])
		case strings.HasPrefix(line, "data:"):
			data.WriteString(strings.TrimSpace(line[len("data:"):]))
		}
	}
	return sc.Err()
}

func (f *AgentFeed) dispatch(event, data string) {
	switch event {
	case "keys":
		var k agentKeys
		if json.Unmarshal([]byte(data), &k) == nil && k.Delegation.ID != "" && len(k.Delegation.Key) == delegation.KeySize {
			f.Link.SetKey(delegation.Key{ID: k.Delegation.ID, Secret: k.Delegation.Key, NotAfter: k.Delegation.NotAfter})
		}
	case "revoked":
		var r delegation.Revoked
		if json.Unmarshal([]byte(data), &r) == nil {
			f.Link.Revoke(r)
		}
	}
}
