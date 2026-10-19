package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/placement"
)

// fakePG is just enough of a Postgres server for the shutdown test: it accepts
// any login, answers LISTEN, says yes to the relay's "is slot 0125 there"
// probe, and fails every other query. A LISTEN connection then sits in
// WaitForNotification, out of the pool, as on a real database.
type fakePG struct {
	ln      net.Listener
	queries atomic.Int64
	wg      sync.WaitGroup

	mu    sync.Mutex
	conns []net.Conn
}

func newFakePG(t *testing.T) *fakePG {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakePG{ln: ln}
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.conns = append(f.conns, c)
			f.mu.Unlock()
			f.wg.Add(1)
			go func() {
				defer f.wg.Done()
				f.serve(c)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		f.mu.Lock()
		for _, c := range f.conns {
			_ = c.Close()
		}
		f.mu.Unlock()
		f.wg.Wait()
	})
	return f
}

func (f *fakePG) dsn() string {
	return "postgres://u:p@" + f.ln.Addr().String() + "/db?sslmode=disable"
}

func (f *fakePG) serve(c net.Conn) {
	defer c.Close()
	be := pgproto3.NewBackend(c, c)
	msg, err := be.ReceiveStartupMessage()
	if err != nil {
		return
	}
	if _, ok := msg.(*pgproto3.StartupMessage); !ok {
		return // a cancel request: nothing to answer
	}
	be.Send(&pgproto3.AuthenticationOk{})
	// pgx runs simple-protocol queries only with these
	be.Send(&pgproto3.ParameterStatus{Name: "standard_conforming_strings", Value: "on"})
	be.Send(&pgproto3.ParameterStatus{Name: "client_encoding", Value: "UTF8"})
	be.Send(&pgproto3.BackendKeyData{ProcessID: 1, SecretKey: []byte{0, 0, 0, 1}})
	be.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
	if be.Flush() != nil {
		return
	}
	for {
		m, err := be.Receive()
		if err != nil {
			return
		}
		q, ok := m.(*pgproto3.Query)
		if !ok {
			return // Terminate or an unexpected message
		}
		f.queries.Add(1)
		switch {
		case strings.HasPrefix(q.String, "LISTEN"):
			be.Send(&pgproto3.CommandComplete{CommandTag: []byte("LISTEN")})
		case strings.Contains(q.String, "to_regproc('tv_push_credentials')"):
			be.Send(&pgproto3.RowDescription{Fields: []pgproto3.FieldDescription{
				{Name: []byte("ok"), DataTypeOID: 16, DataTypeSize: 1, TypeModifier: -1}}})
			be.Send(&pgproto3.DataRow{Values: [][]byte{[]byte("t")}})
			be.Send(&pgproto3.CommandComplete{CommandTag: []byte("SELECT 1")})
		default:
			be.Send(&pgproto3.ErrorResponse{Severity: "ERROR", Code: "42P01", Message: "fake: no such table"})
		}
		be.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		if be.Flush() != nil {
			return
		}
	}
}

// TestShutdownReleasesThePool is the hang a SIGTERM hit: the push relay and the
// placement listener each hold a LISTEN connection; before the fix they ran on
// context.Background(), so pool.Close waited for them forever while the relay
// kept polling a closed pool. Now Stop ends them, and the pool closes at once.
func TestShutdownReleasesThePool(t *testing.T) {
	pg := newFakePG(t)
	cfg, err := pgxpool.ParseConfig(pg.dsn())
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	bg := newBackground(context.Background())
	b := &tvBase{bg: bg, logger: logger, pool: pool}
	b.bg.Go(func(ctx context.Context) { b.runPushRelay(ctx, nil, nil) })
	bg.Go(func(ctx context.Context) { placement.Listen(ctx, pool, logger, func(uuid.UUID) {}) })
	loop := &cluster.HostLoop{Store: cluster.PGBeatStore{Pool: pool}, HostID: "home", Interval: 10 * time.Millisecond,
		Info: func(context.Context) cluster.BeatInfo { return cluster.BeatInfo{} }}
	bg.Go(loop.Run)
	startShareMap(bg, pool, logger)

	// both LISTEN connections are out of the pool before shutting down
	deadline := time.Now().Add(2 * time.Second)
	for pool.Stat().AcquiredConns() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("the listeners never took their connections (acquired %d)", pool.Stat().AcquiredConns())
		}
		time.Sleep(5 * time.Millisecond)
	}

	start := time.Now()
	if !bg.Stop(2 * time.Second) {
		t.Fatal("background loops did not stop")
	}
	if d := time.Since(start); d > 400*time.Millisecond {
		t.Errorf("stopping the loops took %v", d)
	}

	closed := make(chan struct{})
	go func() {
		pool.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("pool.Close blocked: a loop still holds a connection")
	}

	// nothing touches the database after shutdown (the relay used to poll every 2 s)
	n := pg.queries.Load()
	time.Sleep(50 * time.Millisecond)
	if got := pg.queries.Load(); got != n {
		t.Errorf("%d queries after shutdown", got-n)
	}
}

func TestBackgroundStop(t *testing.T) {
	bg := newBackground(context.Background())
	var hooked atomic.Bool
	bg.OnStop(func() { hooked.Store(true) })
	for range 3 {
		bg.Go(func(ctx context.Context) { tick(ctx, time.Millisecond, func() {}) })
	}
	if !bg.Stop(time.Second) {
		t.Fatal("Stop timed out")
	}
	if !hooked.Load() {
		t.Error("stop hook not run")
	}
	if !bg.Stop(time.Second) {
		t.Error("a second Stop must return at once")
	}

	// a goroutine that ignores the context: Stop reports it instead of hanging
	stuck := newBackground(context.Background())
	release := make(chan struct{})
	stuck.Go(func(context.Context) { <-release })
	if stuck.Stop(20 * time.Millisecond) {
		t.Error("Stop reported done with a goroutine still running")
	}
	close(release)
}
