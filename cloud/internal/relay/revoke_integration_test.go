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
	rev := &Revoker{Registry: reg, Store: store.(StandingStore), Logger: quiet, Every: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go rev.Run(ctx)
	srv := httptest.NewServer(&TunnelServer{Registry: reg, Store: store, Logger: quiet, Tickets: NewTickets([]byte(strings.Repeat("k", 32))), Revoker: rev})
	t.Cleanup(srv.Close)
	return &revokeWorld{t: t, pool: pool, users: stores.NewUsers(pool.DB), reg: reg, store: store, url: srv.URL, dsn: dsn, rev: rev}
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
	down.check(context.Background(), w.reg.All())
	if w.gone(a)() || w.gone(b)() {
		t.Fatal("a tunnel closed while the database did not answer")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go (&Revoker{Registry: w.reg, Store: w.rev.Store, Logger: w.rev.Logger, Every: 50 * time.Millisecond}).Run(ctx)
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
	srv := httptest.NewServer(&TunnelServer{Registry: w.reg, Logger: w.rev.Logger, Revoker: w.rev,
		Store: suspendAfterAuth{TunnelStore: w.store, after: func() { w.status(a, stores.StatusSuspended) }}})
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
	after func()
}

func (s suspendAfterAuth) Authenticate(ctx context.Context, id, secret string) (stores.Server, error) {
	sv, err := s.TunnelStore.Authenticate(ctx, id, secret)
	s.after()
	return sv, err
}

// The first use of a home's secret ends its claim's retry, whatever becomes of the rest of the
// handshake: a tunnel of either role, SetOnline failing, or a call to the api with the secret.
func TestFirstUseOfTheSecretEndsTheClaimsRetry(t *testing.T) {
	ctx := context.Background()
	key := strings.Repeat("c3", 32)
	for name, use := range map[string]func(t *testing.T, w *revokeWorld, h revokeHome){
		"a tunnel whose SetOnline failed": func(t *testing.T, w *revokeWorld, h revokeHome) {
			srv := httptest.NewServer(&TunnelServer{Registry: w.reg, Logger: w.rev.Logger, Store: offlineStore{w.store}})
			t.Cleanup(srv.Close)
			c, f := dialAuth(t, srv.URL, AuthPayload{ServerID: h.id, Secret: h.secret})
			t.Cleanup(func() { _ = c.Close() })
			if f.Kind != KindAuthOK {
				t.Fatalf("%#x %s", f.Kind, f.Payload)
			}
			waitFor(t, "the tunnel", func() bool { _, err := w.reg.Lookup(h.slug); return err == nil })
		},
		// a standby records no online: it authenticates as every tunnel does
		"a standby": func(t *testing.T, w *revokeWorld, h revokeHome) {
			c, _ := dialAuth(t, w.url, AuthPayload{ServerID: h.id, Secret: h.secret, Role: RoleStandby})
			_ = c.Close()
		},
		"a call to the api": func(t *testing.T, w *revokeWorld, h revokeHome) {
			if err := stores.NewServers(w.pool.DB).VerifySecret(ctx, h.id, h.secret); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := newRevokeWorld(t)
			srvs := stores.NewServers(w.pool.DB)
			h, code := w.claimed(key)
			use(t, w, h)
			var seen bool
			if err := w.pool.DB.QueryRow(`SELECT last_seen_at IS NOT NULL FROM servers WHERE id = $1`, h.id).Scan(&seen); err != nil || seen {
				t.Fatalf("last_seen_at is set (%v): this does not test the claim's own mark", err)
			}
			if _, err := srvs.RedeemClaim(ctx, code, "box", "retry-box", "h", "1", nil, key, nil); !errors.Is(err, stores.ErrClaimInvalid) {
				t.Fatalf("redeemed again after the home used its secret: %v", err)
			}
		})
	}

	// a wrong secret is not a use
	w := newRevokeWorld(t)
	srvs := stores.NewServers(w.pool.DB)
	h, code := w.claimed(key)
	if err := srvs.VerifySecret(ctx, h.id, "wrong"); !errors.Is(err, stores.ErrBadServerSecret) {
		t.Fatal(err)
	}
	if _, err := srvs.RedeemClaim(ctx, code, "box", "retry-box", "h", "1", nil, key, nil); err != nil {
		t.Fatalf("after a wrong secret: %v", err)
	}
}

// A retry under way when the first secret is used: the retry replaced that secret, so its use is
// refused, never a tunnel with a dead secret.
func TestTheSecretUsedDuringARetryIsRefused(t *testing.T) {
	ctx := context.Background()
	w := newRevokeWorld(t)
	h, _ := w.claimed(strings.Repeat("c3", 32))
	tx, err := w.pool.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	// what RedeemClaim's retry holds and writes
	if _, err := tx.Exec(`SELECT 1 FROM server_claims WHERE used_server_id = $1 FOR UPDATE`, h.id); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE servers SET server_secret_hash = 'another' WHERE id = $1`, h.id); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := w.store.Authenticate(ctx, h.id, h.secret)
		done <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for waiting := 0; waiting == 0; time.Sleep(10 * time.Millisecond) {
		if err := w.pool.DB.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()
            AND wait_event_type = 'Lock' AND query LIKE '%FROM server_claims WHERE used_server_id%FOR UPDATE%'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("the use did not wait for the retry")
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, stores.ErrBadServerSecret) {
		t.Fatalf("%v, want ErrBadServerSecret", err)
	}
}

// claimed is a home made by a real redeem with a retry key, and its code.
func (w *revokeWorld) claimed(key string) (revokeHome, string) {
	w.t.Helper()
	ctx := context.Background()
	u, err := w.users.Create(ctx, uuid.NewString()+"@example.test", "x", "R", true)
	if err != nil {
		w.t.Fatal(err)
	}
	srvs := stores.NewServers(w.pool.DB)
	code, err := srvs.MintClaim(ctx, u.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	h := revokeHome{owner: u.ID, slug: "retry-box", secret: "sek-retry"}
	hash, err := argon2id.Hash(h.secret, argon2id.DefaultParams())
	if err != nil {
		w.t.Fatal(err)
	}
	sv, err := srvs.RedeemClaim(ctx, code, "box", h.slug, hash, "1", nil, key, nil)
	if err != nil {
		w.t.Fatal(err)
	}
	h.id = sv.ID
	return h, code
}

type offlineStore struct{ TunnelStore }

func (offlineStore) SetOnline(context.Context, string, bool) error { return errors.New("down") }
