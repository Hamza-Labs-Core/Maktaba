package components

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/pgdsn"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/runner"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/runner/loopcheck"
)

// startFiles are where the API starts its background loops (story 51.05 §2): a bare
// safego.Go there that is not a catalog loop must say `// not a loop: <why>`.
var startFiles = []string{"main.go", "cluster_bootstrap.go", "node_bootstrap.go", "node_link.go",
	"internal/router/*.go"}

// T08 (F1, Go loops): every loop the API declares is in the catalog, every catalog loop
// of the API is started somewhere, and each loop of a switchable component goes through
// the runner (a safego.Go cannot be stopped).
func TestAPILoopsMatchTheCatalog(t *testing.T) {
	loopcheck.Check(t, loopcheck.Spec{
		Host: Host, Root: "../..", StartFiles: startFiles,
		Declare: []string{"Declare"}, Plain: []loopcheck.Plain{{Pkg: "safego", Func: "Go", NameArg: 1}},
	})
}

// A joined machine starts while the home's database is gone (story 31.x outage): Start
// never waits on it, whether the database refuses or, as here, takes the connection and
// never answers.
func TestStartNeverWaitsOnTheDatabase(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var held []net.Conn
	var mu sync.Mutex
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c) // accepted, never answered
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			_ = c.Close()
		}
	})
	dsn := "postgres://maktaba@" + ln.Addr().String() + "/maktaba?sslmode=disable"
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r := runner.New(runner.Options{Host: Host, Machine: "4b1d0c3e-node", Context: ctx,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	prev := pgdsn.ConnectTimeout
	pgdsn.ConnectTimeout = time.Second
	t.Cleanup(func() { pgdsn.ConnectTimeout = prev })
	logs := &syncBuf{}
	done := make(chan struct{})
	go func() {
		Start(r, db, func() string { return dsn }, slog.New(slog.NewTextHandler(logs, nil)))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Start waited on a database that never answers")
	}
	// and the listener's dial gives up, so the runner goes on polling
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(logs.String(), "components_listen_failed") {
		if time.Now().After(deadline) {
			t.Fatalf("the listener still waits on the database: %s", logs.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// syncBuf is a log sink the test reads while the runner writes.
type syncBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
