// Command relayfixture is the home side of the extension relay's
// two-process test (pipeline/tests/cluster/test_relay_two_process.py): the
// real cluster hub with the real service door, on a scratch Postgres
// database, reading the node registry (cluster_nodes), rules.relay and the
// Plex extension's row and token (home_secrets) from it.
//
//	MAKTABA_RELAY_FIXTURE_DSN          the scratch database
//	MAKTABA_RELAY_FIXTURE_SECRETS_KEY  the home_secrets key file
//	MAKTABA_RELAY_FIXTURE_DIR          the cluster files directory
//
// It prints one JSON line {"url", "cluster_pub"} and serves until its
// standard input closes. Every request it serves runs as an admin (the
// test reads GET /api/cluster/relay). Test-only: never mounted by the API.
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	_ "github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/relay"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "relayfixture:", err)
		os.Exit(1)
	}
}

func run() error {
	db, err := sql.Open("postgres", os.Getenv("MAKTABA_RELAY_FIXTURE_DSN"))
	if err != nil {
		return err
	}
	defer db.Close()
	key, err := secrets.LoadOrCreateKey(os.Getenv("MAKTABA_RELAY_FIXTURE_SECRETS_KEY"))
	if err != nil {
		return err
	}
	priv, pub, err := cluster.GenerateKey()
	if err != nil {
		return err
	}
	homeKey := make([]byte, 32)
	_, _ = rand.Read(homeKey)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	store := &secrets.DBStore{DB: db, Key: key}
	hub := agent.New(agent.Config{
		Store:           &cluster.PGStore{DB: db},
		HomeKey:         homeKey,
		ClusterKey:      priv,
		HomeAddr:        "127.0.0.1",
		Locator:         &agent.Locator{Override: os.Getenv("MAKTABA_RELAY_FIXTURE_DIR")},
		AgentRatePerMin: -1,
		Logger:          logger,
		Relay: relay.New(relay.Config{Settings: relay.DBSettings(db), Upstream: relay.DBUpstream(db, store, nil),
			Logger: logger}),
	})
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := principal.WithPrincipal(req.Context(), &principal.Principal{UserID: "fixture", IsAdmin: true})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	hub.Mount(r)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)
	srv := &http.Server{Handler: r, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	line, _ := json.Marshal(map[string]string{"url": "http://" + ln.Addr().String(), "cluster_pub": cluster.EncodeKey(pub)})
	fmt.Fprintln(os.Stdout, string(line))
	_, _ = io.Copy(io.Discard, os.Stdin) // until the test closes our stdin
	sctx, scancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer scancel()
	_ = srv.Shutdown(sctx)
	return nil
}
