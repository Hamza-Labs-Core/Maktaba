// Command rehearsal-direct-home is the cloud rehearsal's home for direct remote access (Epic 50
// story 50.15, scenario direct-remote, T10): the home's real direct access (directaccess: the
// port mapping, the certificate by CSR over the relay agent, the TLS listener) in front of a
// small stand-in for the home's web port. It runs beside the relay agent (maktaba-cloudlink)
// in tools/rehearsal-cloud/Dockerfile.home. Never shipped: only that image builds it.
//
// Env: DIRECT_DIR (/data/direct), DIRECT_LISTEN (:8444), AGENT_ADDR (127.0.0.1:8765),
// SERVER_ID (the home's cloud server id), UPSTREAM_LISTEN (127.0.0.1:8080).
//
// The stand-in web port answers
//
//	GET /api/health          "ok"
//	GET /media?bytes=N       N bytes of a known pattern (byte i is 'a' + i % 26)
//	GET /rehearsal/vias      how many requests came by each X-Maktaba-Via ("direct", "relay")
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/directaccess"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// upstream is the stand-in web port; it counts requests by how they came.
type upstream struct {
	mu   sync.Mutex
	vias map[string]int
}

func (u *upstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	via := r.Header.Get(directaccess.ViaHeader)
	if r.URL.Path != "/rehearsal/vias" {
		u.mu.Lock()
		u.vias[via]++
		u.mu.Unlock()
	}
	switch r.URL.Path {
	case "/api/health":
		_, _ = w.Write([]byte("ok"))
	case "/media":
		n, _ := strconv.Atoi(r.URL.Query().Get("bytes"))
		if n < 0 || n > 1<<30 {
			http.Error(w, "bytes", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(n))
		buf := make([]byte, 64<<10)
		for i := 0; i < n; {
			k := min(len(buf), n-i)
			for j := range k {
				buf[j] = byte('a' + (i+j)%26)
			}
			if _, err := w.Write(buf[:k]); err != nil {
				return
			}
			i += k
		}
	case "/rehearsal/vias":
		u.mu.Lock()
		defer u.mu.Unlock()
		_ = json.NewEncoder(w).Encode(u.vias)
	default:
		http.NotFound(w, r)
	}
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	listen := env("UPSTREAM_LISTEN", "127.0.0.1:8080")
	up := &upstream{vias: map[string]int{}}
	go func() {
		srv := &http.Server{Addr: listen, Handler: up, ReadHeaderTimeout: 10 * time.Second}
		if err := srv.ListenAndServe(); err != nil {
			log.Error("upstream", "err", err)
			os.Exit(1)
		}
	}()
	serverID := os.Getenv("SERVER_ID")
	s := &directaccess.Service{
		Files:    &directaccess.Files{Dir: env("DIRECT_DIR", "/data/direct")},
		Agent:    &directaccess.Agent{Addr: env("AGENT_ADDR", "127.0.0.1:8765")},
		Listen:   env("DIRECT_LISTEN", ":8444"),
		Upstream: &url.URL{Scheme: "http", Host: listen},
		ServerID: func() string { return serverID },
		Settings: func(context.Context) (directaccess.Settings, error) {
			return directaccess.Settings{Enabled: true, MapPort: true}, nil
		},
		Log:  log,
		Poll: 2 * time.Second,
	}
	var wg sync.WaitGroup
	for _, run := range []func(context.Context){s.RunListener, s.RunPortmap, s.RunCert} {
		wg.Add(1)
		go func() { defer wg.Done(); run(ctx) }()
	}
	log.Info("rehearsal home: direct access on", "server_id", serverID)
	wg.Wait()
}
