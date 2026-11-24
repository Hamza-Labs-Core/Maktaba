//go:build integration

package relay

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/argon2id"
	cachepkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/cache"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

type revokeWorld struct {
	t     *testing.T
	pool  *db.Pool
	users *stores.Users
	reg   *Registry
	store TunnelStore
	rev   *Revoker
	url   string
	dsn   string
}

func newRevokeWorld(t *testing.T) *revokeWorld {
	t.Helper()
	pool, dsn := dbtest.MigratedURL(t)
	reg := NewRegistry()
	store := NewTunnelStore(stores.NewServers(pool.DB))
	quiet := slog.New(slog.DiscardHandler)
	srv := httptest.NewServer(&TunnelServer{Registry: reg, Store: store, Logger: quiet, Tickets: NewTickets([]byte(strings.Repeat("k", 32)))})
	t.Cleanup(srv.Close)
	return &revokeWorld{t: t, pool: pool, users: stores.NewUsers(pool.DB), reg: reg, store: store, url: srv.URL, dsn: dsn,
		rev: &Revoker{Registry: reg, Store: store.(StandingStore), Logger: quiet, Every: time.Hour}}
}

// listen starts the bus as runRelay does: server_changed reaches the revoker.
func (w *revokeWorld) listen() {
	w.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	w.t.Cleanup(cancel)
	bus := &cachepkg.Bus{}
	bus.Add(cachepkg.Funcs{OnInvalidate: func(kind, slug string) {
		if kind == cachepkg.KindServer {
			w.rev.Changed(slug)
		}
	}, OnReset: w.rev.All})
	if err := bus.Start(ctx, w.dsn); err != nil {
		w.t.Fatal(err)
	}
	// the LISTEN is in place once a notification arrives
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := w.pool.DB.Exec(`SELECT pg_notify('cloud_cache', 'probe:x')`); err != nil {
			w.t.Fatal(err)
		}
		if n, _ := bus.Counts(); n > 0 {
			return
		}
		if time.Now().After(deadline) {
			w.t.Fatal("the bus does not hear")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type revokeHome struct{ owner, id, slug, secret string }

func (w *revokeWorld) home(slug string) revokeHome {
	w.t.Helper()
	u, err := w.users.Create(context.Background(), uuid.NewString()+"@example.test", "x", "R", true)
	if err != nil {
		w.t.Fatal(err)
	}
	h := revokeHome{owner: u.ID, slug: slug, secret: "sek-" + slug}
	hash, err := argon2id.Hash(h.secret, argon2id.DefaultParams())
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.pool.DB.QueryRow(`INSERT INTO servers (owner_user_id, name, slug, server_secret_hash) VALUES ($1, 'h', $2, $3) RETURNING id`,
		u.ID, slug, hash).Scan(&h.id); err != nil {
		w.t.Fatal(err)
	}
	return h
}

func (w *revokeWorld) dial(h revokeHome) (*websocket.Conn, Frame) {
	w.t.Helper()
	return dialAuth(w.t, w.url, AuthPayload{ServerID: h.id, Secret: h.secret, Features: []string{FeatureTicket}})
}

func (w *revokeWorld) up(h revokeHome) *websocket.Conn {
	w.t.Helper()
	c, f := w.dial(h)
	if f.Kind != KindAuthOK {
		w.t.Fatalf("%s refused: %s", h.slug, f.Payload)
	}
	w.t.Cleanup(func() { _ = c.Close() })
	waitFor(w.t, "the tunnel of "+h.slug, func() bool { _, err := w.reg.Lookup(h.slug); return err == nil })
	return c
}

func (w *revokeWorld) status(h revokeHome, status string) {
	w.t.Helper()
	if err := w.users.SetStatus(context.Background(), w.pool.DB, h.owner, status, "test"); err != nil {
		w.t.Fatal(err)
	}
}

func (w *revokeWorld) gone(h revokeHome) func() bool {
	return func() bool { _, err := w.reg.Lookup(h.slug); return err != nil }
}

// A suspended owner's home: its live tunnel closes at the suspension, its bearer and its ticket
// are both refused (the bearer as a wrong secret is), another owner's home stays, and
// reinstating lets it back. A deleted server's tunnel closes too.
func TestSuspendedOwnersTunnelIsClosedAndRefused(t *testing.T) {
	w := newRevokeWorld(t)
	w.listen()
	a, b, d := w.home("rev-a"), w.home("rev-b"), w.home("rev-d")
	conn, f := w.dial(a)
	var ok AuthOKPayload
	if f.Kind != KindAuthOK || json.Unmarshal(f.Payload, &ok) != nil || ok.Ticket == "" {
		t.Fatalf("AUTH_OK: %#x %s", f.Kind, f.Payload)
	}
	waitFor(t, "the tunnel", func() bool { _, err := w.reg.Lookup(a.slug); return err == nil })
	w.up(b)
	w.up(d)

	w.status(a, stores.StatusSuspended)
	waitFor(t, "the suspended owner's tunnel to close", w.gone(a))
	// the home sees its connection end (after the frames already under way)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var err error
	for err == nil {
		_, _, err = conn.ReadMessage()
	}
	var ne interface{ Timeout() bool }
	if errors.As(err, &ne) && ne.Timeout() {
		t.Fatal("the connection stayed open")
	}
	_, f = w.dial(a)
	_, wrong := dialAuth(t, w.url, AuthPayload{ServerID: a.id, Secret: "wrong"})
	if f.Kind != KindAuthFail || string(f.Payload) != string(wrong.Payload) {
		t.Fatalf("a suspended owner's bearer: %#x %s; a wrong secret: %s", f.Kind, f.Payload, wrong.Payload)
	}
	if _, f := dialAuth(t, w.url, AuthPayload{ServerID: a.id, Ticket: ok.Ticket, Features: []string{FeatureTicket}}); f.Kind != KindAuthFail {
		t.Fatalf("a suspended owner's ticket: %#x", f.Kind)
	}
	for _, h := range []revokeHome{b, d} {
		if w.gone(h)() {
			t.Fatalf("%s, whose owner is active, lost its tunnel", h.slug)
		}
	}

	w.status(a, stores.StatusActive)
	w.up(a)

	if _, err := w.pool.DB.Exec(`DELETE FROM servers WHERE id = $1`, d.id); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the deleted server's tunnel to close", w.gone(d))
	if w.gone(a)() || w.gone(b)() {
		t.Fatal("another home lost its tunnel")
	}
}

// A notification nobody heard: the full check closes the tunnel; a database that does not answer
// closes none.
func TestRevokerChecksAllWithoutANotification(t *testing.T) {
	w := newRevokeWorld(t)
	a, b := w.home("rev-a"), w.home("rev-b")
	w.up(a)
	w.up(b)
	w.status(a, stores.StatusSuspended)
	down := &Revoker{Registry: w.reg, Store: noStanding{}, Logger: w.rev.Logger}
	down.check(w.reg.All())
	if w.gone(a)() || w.gone(b)() {
		t.Fatal("a tunnel closed while the database did not answer")
	}
	w.rev.Every = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.rev.Run(ctx)
	waitFor(t, "the suspended owner's tunnel to close", w.gone(a))
	if w.gone(b)() {
		t.Fatal("an active owner's tunnel closed")
	}
}

type noStanding struct{}

func (noStanding) Standing(context.Context, []string) (map[string]bool, error) {
	return nil, errors.New("down")
}

// A suspension that commits between a handshake's check and its tunnel's registration, whose
// notification found nothing to close: the tunnel's own check afterwards closes it.
func TestSuspensionDuringAHandshake(t *testing.T) {
	w := newRevokeWorld(t)
	a := w.home("rev-a")
	srv := httptest.NewServer(&TunnelServer{Registry: w.reg, Logger: w.rev.Logger,
		Store: suspendAfterAuth{TunnelStore: w.store, StandingStore: w.store.(StandingStore), after: func() { w.status(a, stores.StatusSuspended) }}})
	defer srv.Close()
	c, f := dialAuth(t, srv.URL, AuthPayload{ServerID: a.id, Secret: a.secret})
	defer func() { _ = c.Close() }()
	if f.Kind != KindAuthOK {
		t.Fatalf("%#x %s", f.Kind, f.Payload)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, _, err := c.ReadMessage()
		if err == nil {
			continue
		}
		var ne interface{ Timeout() bool }
		if errors.As(err, &ne) && ne.Timeout() {
			t.Fatal("the tunnel stayed up")
		}
		break
	}
	waitFor(t, "the tunnel to go", w.gone(a))
}

type suspendAfterAuth struct {
	TunnelStore
	StandingStore
	after func()
}

func (s suspendAfterAuth) Authenticate(ctx context.Context, id, secret string) (stores.Server, error) {
	sv, err := s.TunnelStore.Authenticate(ctx, id, secret)
	s.after()
	return sv, err
}
