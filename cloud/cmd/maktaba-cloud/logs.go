package main

// The cloud's logs (Epic 53 story 53.17 §2), set up by environment, so a cloud without them
// (a developer's, the rehearsal's before Epic 50.07) runs as before:
//
//	MAKTABA_CLOUD_LOGS_DIR         this node's keeper (/var/lib/maktaba-cloud/logs): every
//	                               line of this process, 14 days, 5 GB
//	MAKTABA_CLOUD_LOGS_URL         W's private address (http://10.0.0.4:9610): lines go there too
//	MAKTABA_CLOUD_LOGS_TOKEN_FILE  the token W takes them with (never in the repo)
//	MAKTABA_CLOUD_LOGS_INSTANCE    this process's name among its service's on the node (the
//	                               relay's colour)
//
// On W, `maktaba-cloud logs` takes every node's lines (POST /v1/logs/ingest), keeps them under
// each node's name for 14 days, runs the log index over them, and answers the api role's
// reads (GET /v1/logs/search, /around, /errors, /errors/{fp}) with the same token:
//
//	MAKTABA_CLOUD_LOGS_LISTEN      W's private address to answer on (10.0.0.4:9610)
//	MAKTABA_CLOUD_LOGS_DIR         W's copy of every node's lines

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	logkeeper "github.com/Hamza-Labs-Core/Maktaba/shared/logkeeper/go"
	"github.com/Hamza-Labs-Core/Maktaba/shared/logkeeper/go/logindex"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/logship"
)

// Environment of the cloud's logs.
const (
	EnvLogsDir       = "MAKTABA_CLOUD_LOGS_DIR"
	EnvLogsURL       = "MAKTABA_CLOUD_LOGS_URL"
	EnvLogsTokenFile = "MAKTABA_CLOUD_LOGS_TOKEN_FILE"
	EnvLogsListen    = "MAKTABA_CLOUD_LOGS_LISTEN"
	// EnvLogsInstance tells two processes of one service apart on a node (the relay's blue
	// and green): each keeps its own segment folder.
	EnvLogsInstance = "MAKTABA_CLOUD_LOGS_INSTANCE"
)

// EnvSymbolsTokenFile is the file holding the token release symbol tables are uploaded with.
const EnvSymbolsTokenFile = "MAKTABA_CLOUD_SYMBOLS_TOKEN_FILE"

// logsToken reads W's token from its file ("" when unset or unreadable).
func logsToken() string { return tokenFile(EnvLogsTokenFile) }

// tokenFile reads a token from the file env names ("" when unset or unreadable).
func tokenFile(env string) string {
	p := os.Getenv(env)
	if p == "" {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// logPath is this process's keeper and forwarder, from the environment: the writer the
// logger writes to beside stdout, or nil when the cloud keeps no logs of its own.
func logPath(service, node string) io.Writer {
	dir := os.Getenv(EnvLogsDir)
	if dir == "" {
		return nil
	}
	k := logship.NewKeeper(dir, node)
	go k.Run(context.Background())
	tee := &logship.Tee{Keeper: k, Origin: logkeeper.Origin{Service: service, Instance: os.Getenv(EnvLogsInstance)}}
	if u, tok := os.Getenv(EnvLogsURL), logsToken(); u != "" && tok != "" {
		tee.Forwarder = &logship.Forwarder{URL: u, Token: tok, Node: node, Service: service}
		go tee.Forwarder.Run(context.Background())
	}
	return tee
}

// runLogs is `maktaba-cloud logs`, W's role.
func runLogs(_ []string) {
	logger := newLogger("logs")
	dir, listen, tok := os.Getenv(EnvLogsDir), os.Getenv(EnvLogsListen), logsToken()
	if dir == "" || listen == "" || tok == "" {
		logger.Error("logs: set MAKTABA_CLOUD_LOGS_DIR, MAKTABA_CLOUD_LOGS_LISTEN and MAKTABA_CLOUD_LOGS_TOKEN_FILE",
			"event", "cloud_logs_unconfigured")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := serveLogs(ctx, logger, dir, listen, tok); err != nil {
		logger.Error("logs exited", "err", err, "event", "cloud_logs_failed")
		os.Exit(1)
	}
}

// serveLogs runs W's keeper, the index over it (on loopback) and the private listener.
func serveLogs(ctx context.Context, logger *slog.Logger, dir, listen, tok string) error {
	ix, err := logindex.Open(dir, time.Now)
	if err != nil {
		return fmt.Errorf("the log index: %w", err)
	}
	// the keeper and the index stop with ctx and are waited for, then the index is closed:
	// what W took is on disk before the process ends
	// (an early return, a listener that cannot start, stops them too)
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
		_ = ix.Close()
	}()
	k := logship.NewKeeper(dir, "W")
	wg.Add(1)
	go func() { defer wg.Done(); k.Run(ctx) }()
	ix.Retention = logship.Retention
	ix.Log = func(msg string, kv ...any) { logger.Info(msg, append(kv, "event", msg)...) }
	loop, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := ix.Serve(ctx, loop, time.Second); err != nil {
			logger.Error("logs: the index stopped", "err", err, "event", "cloud_log_index_failed")
		}
	}()
	h := logsHandler(&logship.Receiver{Keeper: k, Token: tok, State: logship.StatePath(dir)}, "http://"+loop.Addr().String(), tok)
	srv := &http.Server{Addr: listen, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() { <-ctx.Done(); _ = srv.Close() }()
	logger.Info("logs: taking the nodes' lines", "addr", listen, "event", "cloud_logs_started")
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// logsHandler is W's private listener: the ingest, and the index's reads behind the token.
func logsHandler(rv *logship.Receiver, index, tok string) http.Handler {
	target, _ := url.Parse(index)
	proxy := httputil.NewSingleHostReverseProxy(target)
	mux := http.NewServeMux()
	mux.Handle("POST "+logship.IngestPath, rv)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	read := func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(tok)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		r = r.Clone(r.Context())
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/v1/logs")
		r.Header.Del("Authorization")
		proxy.ServeHTTP(w, r)
	}
	for _, p := range []string{"GET /v1/logs/search", "GET /v1/logs/around", "GET /v1/logs/errors", "GET /v1/logs/errors/{fp}"} {
		mux.HandleFunc(p, read)
	}
	return mux
}
